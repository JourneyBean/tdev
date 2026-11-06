package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"tdev/internal/config"
	"tdev/internal/tmux"
)

type cachedListing struct {
	UpdatedAt time.Time           `json:"updated_at"`
	Projects  []discoveredProject `json:"projects"`
	Scratches []tmux.Scratch      `json:"scratches"`
}

func (a *app) listingCachePath(t target) (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("tdev-discovery-%d", os.Getuid()))
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ok || stat.Uid != uint32(os.Getuid()) {
		return "", config.Invalid("discovery cache directory must be private and owned by this user")
	}
	root, err := filepath.Abs(a.store.Root)
	if err != nil {
		return "", err
	}
	identity, _ := json.Marshal([]any{root, t.Name, t.Server.SSHAlias, t.Server.RemoteBinary, t.Server.RemoteConfigDir, t.SSH})
	key := sha256.Sum256(identity)
	return filepath.Join(dir, fmt.Sprintf("%x.json", key)), nil
}
func (a *app) loadListingCache(t target) (cachedListing, bool) {
	var cached cachedListing
	path, err := a.listingCachePath(t)
	if err != nil {
		return cached, false
	}
	f, err := os.Open(path)
	if err != nil {
		return cached, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 8*1024*1024 {
		return cached, false
	}
	if json.NewDecoder(f).Decode(&cached) != nil || cached.UpdatedAt.IsZero() {
		return cached, false
	}
	for i, p := range cached.Projects {
		if p.Project.Validate() != nil {
			return cachedListing{}, false
		}
		cached.Projects[i].Project, err = a.effective(t, p.Project)
		if err != nil {
			cached.Projects[i].Project = p.Project
			cached.Projects[i].OverrideError = err.Error()
		}
	}
	return cached, true
}
func (a *app) saveListingCache(t target, projects []discoveredProject, scratchLists ...[]tmux.Scratch) {
	path, err := a.listingCachePath(t)
	if err != nil {
		return
	}
	// Store raw definitions for discovery, then reapply the current overrides when read.
	raw := make([]discoveredProject, len(projects))
	copy(raw, projects)
	for i := range raw {
		if raw[i].RemoteProject.ID != "" {
			raw[i].Project = raw[i].RemoteProject
		}
	}
	var scratches []tmux.Scratch
	if len(scratchLists) > 0 {
		scratches = scratchLists[0]
	}
	data, err := json.Marshal(cachedListing{UpdatedAt: time.Now().UTC(), Projects: raw, Scratches: scratches})
	if err != nil {
		return
	}
	_ = config.AtomicWrite(path, data, 0600)
}
