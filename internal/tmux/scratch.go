package tmux

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

// Scratch sessions have their own marker and never carry a project identity.
type Scratch struct {
	Name      string `json:"name"`
	Directory string `json:"directory"`
	Status
}

func (m Manager) Scratches(ctx context.Context) ([]Scratch, error) {
	result := []Scratch{}
	out, err := m.run(ctx, "list-sessions", "-F", "#{session_id}\t#{@tdev_scratch_name}\t#{@tdev_generation}\t#{session_name}\t#{@tdev_scratch_directory}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "No such file or directory") || strings.Contains(err.Error(), "no sessions") {
			return result, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 5)
		if len(f) != 5 {
			return nil, protocol.Fail("RUNTIME_ERROR", 1, "invalid scratch session metadata")
		}
		if !config.ValidName(f[1]) || f[2] == "" {
			continue
		}
		result = append(result, Scratch{Name: f[1], Directory: f[4], Status: Status{Status: "running", SessionID: f[0], Generation: f[2], SessionName: f[3]}})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func (m Manager) ScratchStatus(ctx context.Context, name string) (Scratch, error) {
	if !config.ValidName(name) {
		return Scratch{}, config.Invalid("invalid scratch name")
	}
	all, err := m.Scratches(ctx)
	if err != nil {
		return Scratch{}, err
	}
	var found *Scratch
	for i := range all {
		if all[i].Name == name {
			if found != nil {
				return Scratch{}, protocol.Fail("SESSION_CONFLICT", 4, "multiple sessions carry this scratch name")
			}
			found = &all[i]
		}
	}
	if found == nil {
		return Scratch{}, protocol.Fail("SESSION_NOT_FOUND", 5, "scratch session %q is missing", name)
	}
	return *found, nil
}
func (m Manager) CreateScratch(ctx context.Context, name, dir string) (Scratch, error) {
	if name == "" {
		name = config.NewID()
	}
	if !config.ValidName(name) {
		return Scratch{}, config.Invalid("invalid scratch name")
	}
	if !config.SafeText(dir) {
		return Scratch{}, config.Invalid("invalid scratch directory")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Scratch{}, err
	}
	if dir == "" || dir == "~" {
		dir = home
	} else if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, dir[2:])
	}
	if !filepath.IsAbs(dir) {
		return Scratch{}, config.Invalid("scratch directory must be absolute or start with ~/")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return Scratch{}, config.Invalid("scratch directory is missing")
	}
	unlock, err := m.Store.Lock(ctx, name, "scratch-session")
	if err != nil {
		return Scratch{}, err
	}
	defer unlock()
	all, err := m.Scratches(ctx)
	if err != nil {
		return Scratch{}, err
	}
	for _, s := range all {
		if s.Name == name {
			return Scratch{}, protocol.Fail("SESSION_CONFLICT", 4, "scratch name already exists; use tdev open with the server and name %s", name)
		}
	}
	// Explicit targets preserve the caller session when invoked inside tmux.
	_, err = m.run(ctx, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", "tdev-scratch-"+name, "-c", dir,
		";", "set-option", "-t", "tdev-scratch-"+name, "@tdev_scratch_name", name,
		";", "set-option", "-t", "tdev-scratch-"+name, "@tdev_scratch_directory", dir,
		";", "set-option", "-t", "tdev-scratch-"+name, "@tdev_generation", config.NewID())
	if err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			return Scratch{}, protocol.Fail("SESSION_CONFLICT", 4, "scratch tmux name is occupied")
		}
		return Scratch{}, err
	}
	s, err := m.ScratchStatus(ctx, name)
	if err != nil {
		return s, err
	}
	err = m.configureAlerts(ctx, s.SessionID)
	return s, err
}

// Attachment callers pass a name without a project UUID for scratch sessions.
func (m Manager) attachmentSession(ctx context.Context, p config.Project) (Status, error) {
	if p.ID == "" {
		s, err := m.ScratchStatus(ctx, p.Name)
		return s.Status, err
	}
	return m.Require(ctx, p)
}
