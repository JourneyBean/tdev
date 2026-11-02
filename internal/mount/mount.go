package mount

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
)

type Entry struct{ Path, Source, Type, ID string }
type Record struct {
	Path             string `json:"path"`
	Source           string `json:"source"`
	Server           string `json:"server"`
	ProjectID        string `json:"project_id"`
	ProjectName      string `json:"project_name"`
	External         bool   `json:"external,omitempty"`
	Token            string `json:"token"`
	MountID          string `json:"mount_id"`
	MetadataVerified bool   `json:"metadata_verified"`
	UTF8Verified     bool   `json:"utf8_verified"`
	ActualSource     string `json:"actual_source"`
	ProcessPID       int    `json:"process_pid,omitempty"`
	Log              string `json:"log,omitempty"`
}
type Manager struct {
	Store     config.Store
	Inventory func() ([]Entry, error)
	Run       func(context.Context, string, ...string) error
	OS        string
	Lookup    func(string) (string, error)
	Start     func(context.Context, string, ...string) (*Process, error)
	Busy      func(context.Context, string) ([]int, error)
}

func New(store config.Store) Manager {
	return Manager{Store: store, Inventory: Inventory, Run: run, OS: runtime.GOOS, Lookup: exec.LookPath, Start: launch, Busy: busyProcesses}
}
func run(ctx context.Context, name string, args ...string) error {
	c := sshx.Command(ctx, name, args...)
	b, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(b)))
	}
	return nil
}
func key(path string) string {
	hash := sha256.Sum256([]byte(path))
	return hex.EncodeToString(hash[:16])
}
func (m Manager) state(path string) string {
	return filepath.Join(m.Store.Root, "runtime", "mounts", key(path)+".json")
}
func (m Manager) record(path string) (Record, error) {
	var r Record
	b, err := os.ReadFile(m.state(path))
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	if r.Path != path {
		return r, config.Invalid("mount record path mismatch")
	}
	return r, err
}
func find(entries []Entry, path string) (Entry, bool) {
	for _, e := range entries {
		if filepath.Clean(e.Path) == path {
			return e, true
		}
	}
	return Entry{}, false
}
func match(r Record, e Entry) bool {
	return (e.Source == r.ActualSource || (r.ActualSource == "" && e.Source == "tdev-"+r.Token)) && e.ID == r.MountID && (strings.Contains(e.Type, "fuse") || e.Type == "sshfs")
}
func (m Manager) Mount(ctx context.Context, server, projectName, projectID, alias, directory, path string, opts sshx.Options, verify func(context.Context, string) error) (Record, error) {
	r := Record{Server: server, ProjectID: projectID, ProjectName: projectName, Source: alias + ":" + directory, Token: config.NewID()}
	if !config.SafeText(directory) || directory == "" {
		return r, config.Invalid("invalid source directory")
	}
	var err error
	r.Path, err = filepath.Abs(path)
	if err != nil {
		return r, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(r.Path))
	if err != nil {
		return r, err
	}
	r.Path = filepath.Join(parent, filepath.Base(r.Path))
	if r.Path == "/" {
		return r, config.Invalid("filesystem root cannot be used as a mountpoint")
	}
	unlock, err := m.Store.Lock(ctx, key(r.Path), "mount")
	if err != nil {
		return r, err
	}
	defer unlock()
	entries, err := m.Inventory()
	if err != nil {
		return r, err
	}
	if live, ok := find(entries, r.Path); ok {
		old, err := m.record(r.Path)
		if err == nil && match(old, live) && old.Source == r.Source && old.ProjectID == projectID && old.Server == server {
			return old, nil
		}
		if live.Source == r.Source {
			r.External = true
			r.MountID = live.ID
			return r, nil
		}
		return r, protocol.Fail("MOUNT_CONFLICT", 4, "mountpoint is already occupied by %s", live.Source)
	}
	if info, err := os.Lstat(r.Path); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return r, config.Invalid("mountpoint must be a real directory")
		}
		children, err := os.ReadDir(r.Path)
		if err != nil {
			return r, err
		}
		if len(children) != 0 {
			return r, protocol.Fail("MOUNT_CONFLICT", 4, "unmounted target directory is not empty")
		}
	} else if os.IsNotExist(err) {
		if err = os.Mkdir(r.Path, 0700); err != nil {
			return r, err
		}
	} else {
		return r, err
	}
	lookup := m.Lookup
	if lookup == nil {
		lookup = exec.LookPath
	}
	if _, err = lookup("sshfs"); err != nil {
		return r, protocol.Fail("DEPENDENCY_MISSING", 7, "SSHFS and a working FUSE backend are required")
	}
	args := []string{r.Source, r.Path, "-f", "-o", "fsname=tdev-" + r.Token, "-o", fmt.Sprintf("reconnect,ConnectTimeout=%d,ServerAliveInterval=%d,ServerAliveCountMax=%d", max(1, int(opts.ConnectTimeout.Seconds())), max(1, int(opts.ServerAliveInterval.Seconds())), opts.ServerAliveCountMax), "-o", "BatchMode=yes", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ssh_command=ssh -oClearAllForwardings=yes -oRemoteCommand=none"}
	if opts.ConfigFile != "" {
		args = append(args, "-F", opts.ConfigFile)
	}
	if m.OS == "darwin" {
		args = append(args, "-o", "noappledouble,noapplexattr", "-o", "modules=iconv,from_code=UTF-8,to_code=UTF-8-MAC")
	}
	var process *Process
	if m.Start != nil {
		logDir := filepath.Join(m.Store.Root, "runtime", "logs")
		if err = os.MkdirAll(logDir, 0700); err != nil {
			return r, err
		}
		r.Log = filepath.Join(logDir, "sshfs-"+r.Token+".log")
		process, err = m.Start(ctx, r.Log, args...)
		if err != nil {
			return r, err
		}
		r.ProcessPID = process.PID
	} else {
		if err = m.Run(ctx, "sshfs", args...); err != nil {
			return r, err
		}
	}
	retainProcess := false
	defer func() {
		if process == nil || retainProcess {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		entries, err := m.Inventory()
		if err != nil {
			return
		}
		if e, ok := find(entries, r.Path); ok && (e.Source == r.Source || e.Source == "tdev-"+r.Token) {
			owned := r
			owned.ActualSource = e.Source
			owned.MountID = e.ID
			if err = m.unmount(cleanup, owned); err != nil {
				return
			}
		}
		process.Stop()
	}()
	startup, cancel := context.WithTimeout(ctx, opts.ConnectTimeout+5*time.Second)
	defer cancel()
	var live Entry
	for {
		entries, err = m.Inventory()
		if err != nil {
			return r, err
		}
		var ok bool
		live, ok = find(entries, r.Path)
		if ok {
			if live.Source != r.Source && live.Source != "tdev-"+r.Token {
				return r, protocol.Fail("MOUNT_CONFLICT", 4, "another source occupied the target during startup")
			}
			break
		}
		if process == nil {
			return r, protocol.Fail("MOUNT_UNVERIFIED", 1, "expected mount is absent")
		}
		select {
		case <-process.Done:
			return r, fmt.Errorf("SSHFS exited before mounting: %v (log: %s)", process.Err(), r.Log)
		case <-startup.Done():
			return r, fmt.Errorf("SSHFS startup cancelled/timed out: %w (log: %s)", startup.Err(), r.Log)
		case <-time.After(20 * time.Millisecond):
		}
	}
	r.MountID = live.ID
	r.ActualSource = live.Source
	// Register before probing so a failed verification always leaves an identifiable owned mount.
	b, _ := json.Marshal(r)
	if err = config.AtomicWrite(m.state(r.Path), b, 0600); err != nil {
		return r, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "mount state could not be saved; inspect the target and log: " + err.Error(), Details: r}
	}
	if m.OS == "darwin" {
		// The backend may accept but ignore options. Independently inspect the remote filesystem.
		normal := filepath.Join(r.Path, ProbeName(r.Token))
		probeErr := os.WriteFile(normal, []byte("tdev mount verification"), 0600)
		_ = os.WriteFile(filepath.Join(r.Path, "._"+ProbeName(r.Token)), []byte("tdev metadata guard probe"), 0600)
		xattrAccepted := false
		if probeErr == nil {
			xattrAccepted = setAppleAttribute(normal) == nil
		}
		if verify == nil {
			err = protocol.Fail("MOUNT_UNVERIFIED", 7, "macOS metadata verification callback is required")
		} else {
			err = verify(ctx, r.Token)
		}
		if err == nil && probeErr != nil {
			err = protocol.Fail("MOUNT_UNVERIFIED", 7, "writable project root required for macOS metadata verification: %v", probeErr)
		}
		if err == nil && xattrAccepted {
			err = protocol.Fail("MOUNT_UNVERIFIED", 7, "SSHFS backend accepted a prohibited com.apple.* attribute")
		}
		if err != nil {
			cleanupErr := m.unmount(ctx, r)
			if cleanupErr != nil {
				return r, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: err.Error() + "; owned mount could not be removed: " + cleanupErr.Error(), Details: r}
			}
			return r, err
		}
		r.MetadataVerified = true
		r.UTF8Verified = true
		b, _ = json.Marshal(r)
		if err = config.AtomicWrite(m.state(r.Path), b, 0600); err != nil {
			return r, err
		}
	}
	retainProcess = true
	return r, nil
}
func (m Manager) Records(server, projectID string) ([]Record, error) {
	entries, err := os.ReadDir(filepath.Join(m.Store.Root, "runtime", "mounts"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(m.Store.Root, "runtime", "mounts", e.Name()))
		if err != nil {
			return nil, err
		}
		var r Record
		if err = json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		if r.Server == server && (r.ProjectID == projectID || r.ProjectName == projectID) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (m Manager) Unmount(ctx context.Context, server, projectID, path string) (Record, error) {
	records, err := m.Records(server, projectID)
	if err != nil {
		return Record{}, err
	}
	if path != "" {
		path, err = filepath.Abs(path)
		if err != nil {
			return Record{}, err
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return Record{}, err
		}
		path = filepath.Join(parent, filepath.Base(path))
		for _, r := range records {
			if r.Path == path {
				return r, m.unmount(ctx, r)
			}
		}
		return Record{}, config.Invalid("no managed mount for this project at %s", path)
	}
	if len(records) != 1 {
		return Record{}, config.Invalid("found %d managed mounts; specify local_path", len(records))
	}
	return records[0], m.unmount(ctx, records[0])
}
func (m Manager) unmount(ctx context.Context, r Record) error {
	entries, err := m.Inventory()
	if err != nil {
		return err
	}
	live, ok := find(entries, r.Path)
	if !ok {
		return protocol.Fail("STALE_MOUNT", 4, "recorded mount is no longer present; state retained for inspection")
	}
	if !match(r, live) {
		return protocol.Fail("MOUNT_CONFLICT", 4, "mount identity changed; refusing to unmount another filesystem")
	}
	if m.Busy != nil {
		pids, err := m.Busy(ctx, r.Path)
		if err != nil {
			return err
		}
		if len(pids) > 0 {
			return fmt.Errorf("mount is busy in processes %v; close their files/directories before unmounting", pids)
		}
	}
	entries, err = m.Inventory()
	if err != nil {
		return err
	}
	current, exists := find(entries, r.Path)
	if !exists || !match(r, current) {
		return protocol.Fail("MOUNT_CONFLICT", 4, "mount changed during unmount preflight")
	}
	name, args := "umount", []string{r.Path}
	if m.OS == "linux" {
		if _, err := exec.LookPath("fusermount3"); err == nil {
			name, args = "fusermount3", []string{"-u", r.Path}
		}
	}
	if err = m.Run(ctx, name, args...); err != nil {
		return fmt.Errorf("unmount failed (busy mounts are not forced): %w", err)
	}
	entries, err = m.Inventory()
	if err != nil {
		return err
	}
	if _, present := find(entries, r.Path); present {
		return fmt.Errorf("mount still present after unmount")
	}
	err = os.Remove(m.state(r.Path))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
