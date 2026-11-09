package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

type ConnectionView struct {
	ID          string       `json:"id"`
	Kind        string       `json:"kind"`
	Alias       string       `json:"alias,omitempty"`
	Socket      string       `json:"socket,omitempty"`
	Spec        string       `json:"spec,omitempty"`
	Status      string       `json:"status"`
	Owners      int          `json:"owners"`
	Durable     bool         `json:"durable"`
	HelperPID   int          `json:"helper_pid,omitempty"`
	Allocations []Allocation `json:"allocations"`
	sshx.Inspection
}

type Inspection struct {
	PID         int              `json:"pid"`
	ConfigRoot  string           `json:"config_root,omitempty"`
	RuntimeDir  string           `json:"runtime_dir"`
	Connections []ConnectionView `json:"connections"`
	Warnings    []string         `json:"warnings,omitempty"`
}

type DaemonView struct {
	State string `json:"state"`
	Inspection
}

type Inventory struct {
	Daemons  []DaemonView `json:"daemons"`
	Warnings []string     `json:"warnings,omitempty"`
}

func (s *supervisor) inspect(ctx context.Context) Inspection {
	result := Inspection{PID: os.Getpid(), ConfigRoot: s.client.Root, RuntimeDir: s.client.Dir, Connections: []ConnectionView{}}
	s.mu.Lock()
	controls := make(map[string]*connection, len(s.connections))
	for id, c := range s.connections {
		controls[id] = c
	}
	for key, f := range s.forwards {
		socket := ""
		for _, value := range f.group.Sockets() {
			socket = value
		}
		kind := "forward"
		if f.request.Local {
			kind = "local-forward"
		}
		result.Connections = append(result.Connections, ConnectionView{ID: key, Kind: kind, Alias: f.request.Alias, Socket: socket, Spec: f.state.Spec, Status: f.state.State, Owners: len(f.owners), Durable: f.durable})
	}
	for path, m := range s.mounts {
		alias, _, _ := strings.Cut(m.source, ":")
		result.Connections = append(result.Connections, ConnectionView{ID: path, Kind: "sshfs", Alias: alias, Spec: path, Status: "registered", Owners: len(m.owners), Durable: m.durable, HelperPID: m.pid,
			Inspection: sshx.Inspection{State: "independent-transport", ChannelsStatus: "unavailable", Channels: []sshx.Channel{}}})
	}
	result.Warnings = append(result.Warnings, s.warnings...)
	allocations := make(map[string][]Allocation)
	for _, a := range s.channels {
		allocations[a.Socket] = append(allocations[a.Socket], a)
	}
	s.mu.Unlock()
	for id, c := range controls {
		row := ConnectionView{ID: id, Kind: "control", Alias: c.request.Alias, Status: "connecting"}
		// Acquisition can involve a slow network. A snapshot must not wait for it.
		if c.mu.TryLock() {
			row.Socket = c.transport.Socket
			row.Status = "idle"
			if c.err != nil {
				row.Status = "failed"
				if reconnect.Retryable(c.err) {
					row.Status = "waiting"
				}
			}
			c.mu.Unlock()
		}
		result.Connections = append(result.Connections, row)
	}
	seen := map[string]bool{}
	for _, row := range result.Connections {
		if row.Socket != "" {
			seen[row.Socket] = true
		}
	}
	// Also expose allocated masters between resource registration and readiness.
	for _, pattern := range []string{"ssh-*/m", "tdev-ssh-*/m"} {
		paths, _ := filepath.Glob(filepath.Join(s.client.Dir, pattern))
		for _, path := range paths {
			if !seen[path] {
				result.Connections = append(result.Connections, ConnectionView{ID: path, Kind: "unassigned", Socket: path, Status: "allocated"})
			}
		}
	}
	for socket := range allocations {
		if !seen[socket] {
			found := false
			for _, row := range result.Connections {
				found = found || row.Socket == socket
			}
			if !found {
				result.Connections = append(result.Connections, ConnectionView{ID: socket, Kind: "retired", Socket: socket, Status: "closing"})
			}
		}
	}
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for index := range result.Connections {
		row := &result.Connections[index]
		row.Allocations = allocations[row.Socket]
		if row.Allocations == nil {
			row.Allocations = []Allocation{}
		}
		if row.Kind == "control" && row.Status == "idle" && len(row.Allocations) > 0 {
			row.Status = "active"
		}
		sort.Slice(row.Allocations, func(i, j int) bool { return row.Allocations[i].ID < row.Allocations[j].ID })
		if row.Inspection.State == "" {
			row.Inspection = sshx.Inspection{State: "unavailable", ChannelsStatus: "unavailable", Channels: []sshx.Channel{}}
		}
		if row.Socket == "" {
			if row.Kind == "local-forward" {
				row.Inspection.State, row.Inspection.ChannelsStatus = "local", "not-applicable"
			}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-limit }()
			probe, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			row.Inspection = sshx.Inspect(probe, row.Socket)
		}()
	}
	wg.Wait()
	sort.Slice(result.Connections, func(i, j int) bool { return result.Connections[i].ID < result.Connections[j].ID })
	return result
}

// InventoryForUser enumerates only this user's private tdev runtime directories.
// No daemon is created, reconnected, stopped, or otherwise managed by this query.
func InventoryForUser(ctx context.Context) (Inventory, error) {
	return inventoryIn(ctx, "/tmp")
}

func inventoryIn(ctx context.Context, base string) (Inventory, error) {
	result := Inventory{Daemons: []DaemonView{}}
	paths, err := filepath.Glob(filepath.Join(base, fmt.Sprintf("tdev-daemon-%d-*", os.Getuid())))
	if err != nil {
		return result, err
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
			continue
		}
		client := Client{Dir: path}
		socket, err := os.Lstat(client.Socket())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || socket.Mode()&os.ModeSocket == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case limit <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-limit }()
			probe, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			state, err := client.Call(probe, Request{Op: "inspect"})
			row := DaemonView{State: "unavailable", Inspection: Inspection{RuntimeDir: client.Dir, Connections: []ConnectionView{}}}
			if err == nil && state.Inspection != nil {
				row.State, row.Inspection = "running", *state.Inspection
			} else if err != nil && protocol.Normalize(err).Code == "INVALID_CONFIG" {
				// Older daemons retain status compatibility, but cannot report channels.
				if state, err = client.Call(probe, Request{Op: "status"}); err == nil {
					row.State, row.PID = "running-legacy", state.PID
					row.Warnings = []string{"restart this daemon with the current tdev to inspect connections"}
				}
			}
			mu.Lock()
			result.Daemons = append(result.Daemons, row)
			mu.Unlock()
		}()
	}
	wg.Wait()
	sort.Slice(result.Daemons, func(i, j int) bool { return result.Daemons[i].RuntimeDir < result.Daemons[j].RuntimeDir })
	return result, ctx.Err()
}
