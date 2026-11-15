package config

import (
	"bytes"
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
	if _, err := declare(p).project(name, p); err != nil {
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
	unlockStore, err := s.Lock(ctx, "projects", "store")
	if err != nil {
		return p, err
	}
	defer unlockStore()
	if reservedName(name) {
		return p, Invalid("project name is reserved")
	}
	sources, err := s.sources()
	if err != nil {
		return p, err
	}
	if _, exists := sources[name]; exists {
		return p, protocol.Fail("CONFIG_CONFLICT", 4, "project already exists")
	}
	path := filepath.Join(s.Root, "config.yaml")
	var f *sourceFile
	if _, err := os.Stat(path); err == nil {
		f, err = parseFile(path)
		if err != nil {
			return p, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return p, err
	} else {
		path, _ = s.path(name)
		if _, err := os.Stat(path); err == nil {
			f, err = parseFile(path)
			if err != nil {
				return p, err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return p, err
		}
	}
	if f == nil {
		f = &sourceFile{path: path, doc: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}}
	}
	if f.legacy || isLegacy(f.doc.Content[0]) {
		return p, protocol.Fail("CONFIG_CONFLICT", 4, "destination contains a legacy project; move it to another file first")
	}
	if f.data != nil {
		canonical, err := filepath.EvalSymlinks(f.path)
		if err != nil {
			return p, err
		}
		f.path = canonical
	}
	node := encodeNode(declare(p))
	f.doc.Content[0].Content = append(f.doc.Content[0].Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, node)
	b, err := encodeDocument(f.doc)
	if err != nil {
		return p, err
	}
	if err := checkSource(f); err != nil {
		return p, err
	}
	if f.data != nil {
		backup := fmt.Sprintf("%s.%s.rev-0.bak", f.path, name)
		if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
			return p, protocol.Fail("CONFIG_CONFLICT", 4, "backup already exists or cannot be inspected: %s", backup)
		}
		if err := AtomicWrite(backup, f.data, 0600); err != nil {
			return p, err
		}
		if err := checkSource(f); err != nil {
			return p, err
		}
	}
	if err := AtomicWrite(f.path, b, 0600); err != nil {
		return p, err
	}
	return p, s.commitRecord(projectSource{file: f, node: node, name: name}, p)
}
func checkSource(f *sourceFile) error {
	current, err := os.ReadFile(f.path)
	if f.data == nil && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(current, f.data) {
		return protocol.Fail("CONFIG_CONFLICT", 4, "configuration file changed during update: %s", f.path)
	}
	return nil
}
func (s Store) Update(ctx context.Context, name string, expected int, fn func(*Project) error) (Project, error) {
	var p Project
	if !ValidName(name) || reservedName(name) {
		return p, Invalid("invalid or reserved project name %q", name)
	}
	if expected < 0 {
		return p, Invalid("expected revision must be nonnegative")
	}
	unlock, err := s.Lock(ctx, name, "config")
	if err != nil {
		return p, err
	}
	defer unlock()
	unlockStore, err := s.Lock(ctx, "projects", "store")
	if err != nil {
		return p, err
	}
	defer unlockStore()
	sources, err := s.sources()
	if err != nil {
		return p, err
	}
	src, ok := sources[name]
	if !ok {
		return p, protocol.Fail("PROJECT_NOT_FOUND", 3, "project %q does not exist", name)
	}
	p, err = s.resolve(src)
	if err != nil {
		return p, err
	}
	if expected > 0 && p.Revision != expected {
		return p, &protocol.Error{Code: "CONFIG_CONFLICT", Exit: 4, Message: "project configuration changed", Details: map[string]int{"expected_revision": expected, "actual_revision": p.Revision}}
	}
	oldID, oldRevision := p.ID, p.Revision
	before := encodeNode(declare(p))
	if err := fn(&p); err != nil {
		return p, err
	}
	if p.ID != oldID || p.Name != name {
		return p, Invalid("project identity cannot be changed")
	}
	p.Revision = oldRevision + 1
	if err := p.Validate(); err != nil {
		return p, err
	}
	// The authored representation requires unique display names as well as API IDs.
	if _, err := declare(p).project(name, p); err != nil {
		return p, err
	}
	next := encodeNode(declare(p))
	f := src.file
	if f.legacy {
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: f.doc.Content[0].HeadComment}
		root.Content = []*yaml.Node{{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, next}
		f.doc.Content[0] = root
	} else {
		root := f.doc.Content[0]
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == name {
				next = patchNode(root.Content[i+1], before, next)
				root.Content[i+1] = next
				break
			}
		}
	}
	b, err := encodeDocument(f.doc)
	if err != nil {
		return p, err
	}
	if err := checkSource(f); err != nil {
		return p, err
	}
	backup := fmt.Sprintf("%s.rev-%d.bak", f.path, oldRevision)
	// Different projects in a shared file may have the same revision.
	if !f.legacy {
		backup = fmt.Sprintf("%s.%s.rev-%d.bak", f.path, name, oldRevision)
	}
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		return p, protocol.Fail("CONFIG_CONFLICT", 4, "backup already exists or cannot be inspected: %s", backup)
	}
	if err := AtomicWrite(backup, f.data, 0600); err != nil {
		return p, err
	}
	if err := checkSource(f); err != nil {
		return p, err
	}
	if err := AtomicWrite(f.path, b, 0600); err != nil {
		return p, err
	}
	return p, s.commitRecord(projectSource{file: f, node: next, name: name}, p)
}

// The source has already committed when this helper is called.
func (s Store) commitRecord(src projectSource, p Project) error {
	if err := s.saveRecord(src, p); err != nil {
		return protocol.Fail("PARTIAL_SUCCESS", 9, "configuration saved but internal project record could not be saved: %v", err)
	}
	return nil
}
