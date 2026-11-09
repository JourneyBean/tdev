package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tdev/internal/config"
	"tdev/internal/forward"
)

type lease struct {
	active   chan struct{}
	done     chan struct{}
	activate sync.Once
	close    sync.Once
	release  sync.Once
}

type managedMount struct {
	pid     int
	source  string
	path    string
	request Request
	owners  map[string]bool
	durable bool
}

// MountResult separates lifecycle metadata from the public mount response.
type MountResult struct {
	Data     any
	Existing bool
}

func (s *supervisor) beginLease(owner string) error {
	if !config.ValidName(owner) {
		return config.Invalid("invalid lease identity")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping || s.ctx.Err() != nil {
		return config.Invalid("daemon is stopping")
	}
	if s.leases[owner] != nil {
		return config.Invalid("lease already exists")
	}
	l := &lease{active: make(chan struct{}), done: make(chan struct{})}
	s.leases[owner] = l
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-s.ctx.Done():
		case <-l.done:
		case <-timer.C:
		case <-l.active:
			select {
			case <-s.ctx.Done():
			case <-l.done:
			}
		}
		s.releaseLease(owner)
	}()
	return nil
}

func (s *supervisor) holdLease(ctx context.Context, owner string) error {
	s.mu.Lock()
	l := s.leases[owner]
	s.mu.Unlock()
	if l == nil {
		return config.Invalid("lease expired")
	}
	l.activate.Do(func() { close(l.active) })
	select {
	case <-ctx.Done():
	case <-s.ctx.Done():
	case <-l.done:
	}
	s.releaseLease(owner)
	return nil
}

func (s *supervisor) releaseLease(owner string) {
	s.mu.Lock()
	l := s.leases[owner]
	s.mu.Unlock()
	if l == nil {
		return
	}
	l.close.Do(func() { close(l.done) })
	l.release.Do(func() {
		// Serialize mount registration against unmounting the last owner's mount.
		s.mountMu.Lock()
		defer s.mountMu.Unlock()
		s.forwardMu.Lock()
		defer s.forwardMu.Unlock()
		s.mu.Lock()
		var groups []*forward.Group
		var mounts []*managedMount
		for key, e := range s.forwards {
			delete(e.owners, owner)
			if !e.durable && len(e.owners) == 0 {
				delete(s.forwards, key)
				groups = append(groups, e.group)
			}
		}
		for key, e := range s.mounts {
			delete(e.owners, owner)
			if !e.durable && len(e.owners) == 0 {
				delete(s.mounts, key)
				mounts = append(mounts, e)
			}
		}
		s.mu.Unlock()
		for _, g := range groups {
			g.Close()
		}
		for _, entry := range mounts {
			req := entry.request
			req.Op = "unmount"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, err := s.service(ctx, req)
			cancel()
			if err != nil {
				s.mu.Lock()
				s.warnings = append(s.warnings, "mount retained: "+err.Error())
				if len(s.warnings) > 64 {
					s.warnings = s.warnings[len(s.warnings)-64:]
				}
				s.mu.Unlock()
			} else {
				delete(s.ephemeralMounts, entry.path)
				_ = s.saveMountOwnership()
			}
		}
		s.mu.Lock()
		delete(s.leases, owner)
		s.mu.Unlock()
	})
}

func (s *supervisor) startMount(ctx context.Context, req Request) (State, error) {
	s.mountMu.Lock()
	defer s.mountMu.Unlock()
	s.mu.Lock()
	if s.stopping || s.ctx.Err() != nil {
		s.mu.Unlock()
		return State{}, config.Invalid("daemon is stopping")
	}
	if req.Owner != "" && s.leases[req.Owner] == nil {
		s.mu.Unlock()
		return State{}, config.Invalid("lease expired")
	}
	s.mu.Unlock()
	result, err := s.service(ctx, req)
	if err != nil {
		return State{}, err
	}
	existing := false
	if wrapped, ok := result.(MountResult); ok {
		result, existing = wrapped.Data, wrapped.Existing
	}
	b, err := json.Marshal(result)
	if err != nil {
		return State{}, err
	}
	var record struct {
		Path, Source string
		ProcessPID   int `json:"process_pid"`
	}
	if err = json.Unmarshal(b, &record); err != nil {
		return State{}, err
	}
	if record.Path != "" {
		s.mu.Lock()
		e := s.mounts[record.Path]
		if e == nil {
			e = &managedMount{path: record.Path, request: req, owners: map[string]bool{}, durable: existing && !s.ephemeralMounts[record.Path]}
			s.mounts[record.Path] = e
		}
		if req.Owner == "" {
			e.durable = true
		} else {
			e.owners[req.Owner] = true
		}
		e.pid, e.source = record.ProcessPID, record.Source
		if e.durable {
			delete(s.ephemeralMounts, record.Path)
		} else {
			s.ephemeralMounts[record.Path] = true
		}
		s.mu.Unlock()
		if err = s.saveMountOwnership(); err != nil {
			return State{}, err
		}
	}
	return State{Protocol: Protocol, Result: b}, nil
}

func (s *supervisor) loadMountOwnership() error {
	s.ephemeralMounts = map[string]bool{}
	b, err := os.ReadFile(filepath.Join(s.client.Dir, "ephemeral-mounts.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return config.Invalid("daemon mount state exceeds 1 MiB")
	}
	if err = json.Unmarshal(b, &s.ephemeralMounts); err != nil {
		return config.Invalid("invalid daemon mount ownership")
	}
	if s.ephemeralMounts == nil {
		s.ephemeralMounts = map[string]bool{}
	}
	return nil
}

// Callers hold mountMu. Only paths and ephemeral ownership flags are stored.
func (s *supervisor) saveMountOwnership() error {
	b, err := json.Marshal(s.ephemeralMounts)
	if err != nil {
		return err
	}
	return config.AtomicWrite(filepath.Join(s.client.Dir, "ephemeral-mounts.json"), b, 0600)
}
