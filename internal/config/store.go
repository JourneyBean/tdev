package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

type Store struct{ Root string }

// StateRoot is shared by processes using different configuration roots.
func StateRoot() string {
	if root := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(root) {
		return filepath.Join(root, "tdev")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "tdev")
}

func DefaultRoot() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "tdev")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".config", "tdev")
}
func (s Store) path(name string) (string, error) {
	if !ValidName(name) {
		return "", Invalid("invalid project name %q", name)
	}
	return filepath.Join(s.Root, "projects", name+".yaml"), nil
}

// Kernel locks release on process exit; never delete a lock file while another process may hold it.
func (s Store) Lock(ctx context.Context, name, kind string) (func(), error) {
	if !ValidName(name) || !ValidName(kind) {
		return nil, Invalid("invalid lock name")
	}
	d := filepath.Join(s.Root, "locks")
	if err := os.MkdirAll(d, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(d, name+"."+kind+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (s Store) Get(name string) (Project, error) {
	var p Project
	path, err := s.path(name)
	if err != nil {
		return p, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, protocol.Fail("PROJECT_NOT_FOUND", 3, "project %q does not exist", name)
	}
	if err != nil {
		return p, err
	}
	if err = Decode(b, &p); err != nil {
		return p, err
	}
	if err = p.Validate(); err != nil {
		return p, err
	}
	if p.Name != name {
		return p, Invalid("project name does not match filename")
	}
	return p, nil
}
func (s Store) List() ([]Project, error) {
	entries, err := os.ReadDir(filepath.Join(s.Root, "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return []Project{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Project{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		p, err := s.Get(e.Name()[:len(e.Name())-5])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tdev-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s Store) Create(ctx context.Context, name, dir string) (Project, error) {
	p := Project{SchemaVersion: 1, Revision: 1, ID: NewID(), Name: name, Windows: []Window{{ID: "main", Name: "main", Directory: "."}}, Forwards: []Forward{}, Directory: dir}
	return s.CreateDefinition(ctx, p)
}

// CreateDefinition validates the complete draft before creating any configuration.
func (s Store) CreateDefinition(ctx context.Context, p Project) (Project, error) {
	name, dir := p.Name, p.Directory
	if p.Revision != 1 {
		return p, Invalid("new project revision must be 1")
	}
	var err error
	p.Directory, err = filepath.Abs(dir)
	if err != nil {
		return p, err
	}
	if err = p.Validate(); err != nil {
		return p, err
	}
	info, err := os.Stat(p.Directory)
	if err != nil || !info.IsDir() {
		return p, Invalid("directory does not exist: %s", p.Directory)
	}
	unlock, err := s.Lock(ctx, name, "config")
	if err != nil {
		return p, err
	}
	defer unlock()
	path, _ := s.path(name)
	if _, err = os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return p, err
		}
		return p, protocol.Fail("CONFIG_CONFLICT", 4, "project already exists")
	}
	b, err := yaml.Marshal(p)
	if err != nil {
		return p, err
	}
	return p, AtomicWrite(path, b, 0600)
}
func (s Store) Update(ctx context.Context, name string, expected int, fn func(*Project) error) (Project, error) {
	var p Project
	if expected < 0 {
		return p, Invalid("expected revision must be nonnegative")
	}
	unlock, err := s.Lock(ctx, name, "config")
	if err != nil {
		return p, err
	}
	defer unlock()
	p, err = s.Get(name)
	if err != nil {
		return p, err
	}
	if expected > 0 && p.Revision != expected {
		return p, &protocol.Error{Code: "CONFIG_CONFLICT", Exit: 4, Message: "project configuration changed", Details: map[string]int{"expected_revision": expected, "actual_revision": p.Revision}}
	}
	old := p
	oldBytes, err := yaml.Marshal(p)
	if err != nil {
		return p, err
	}
	if err = fn(&p); err != nil {
		return p, err
	}
	if p.ID != old.ID || p.Name != old.Name {
		return p, Invalid("project identity cannot be changed")
	}
	p.Revision = old.Revision + 1
	if err = p.Validate(); err != nil {
		return p, err
	}
	b, err := yaml.Marshal(p)
	if err != nil {
		return p, err
	}
	path, _ := s.path(name)
	backup := fmt.Sprintf("%s.rev-%d.bak", path, old.Revision)
	if _, err = os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return p, protocol.Fail("CONFIG_CONFLICT", 4, "backup already exists or cannot be inspected: %s", backup)
	}
	if err = AtomicWrite(backup, oldBytes, 0600); err != nil {
		return p, err
	}
	return p, AtomicWrite(path, b, 0600)
}
