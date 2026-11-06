// Package transfer gives exact file/directory semantics to system scp using numbered staging files.
// Directories merge into the specified destination; unrelated destination files are never removed.
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

type Entry struct {
	Path      string `json:"path" yaml:"path"`
	Directory bool   `json:"directory" yaml:"directory"`
	Size      int64  `json:"size" yaml:"size"`
	Mode      uint32 `json:"mode" yaml:"mode"`
	SHA256    string `json:"sha256,omitempty" yaml:"sha256,omitempty"`
}
type Manifest struct {
	Entries []Entry `json:"entries" yaml:"entries"`
}
type Snapshot struct {
	Kind      string `json:"kind" yaml:"kind"`
	Signature string `json:"signature" yaml:"signature"`
}
type Plan struct {
	Destination string     `json:"destination" yaml:"destination"`
	Manifest    Manifest   `json:"manifest" yaml:"manifest"`
	Before      []Snapshot `json:"before" yaml:"before"`
	Overwrites  int        `json:"overwrites" yaml:"overwrites"`
}
type Stage struct {
	ID     string `json:"id" yaml:"id"`
	Path   string `json:"path" yaml:"path"`
	Plan   Plan   `json:"plan" yaml:"plan"`
	Export bool   `json:"export" yaml:"export"`
}

func fail(format string, args ...any) error { return protocol.Fail("UNSAFE_PATH", 2, format, args...) }
func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (m Manifest) Validate() error {
	if len(m.Entries) == 0 || len(m.Entries) > 100000 {
		return fail("manifest must contain 1..100000 entries")
	}
	seen := map[string]bool{}
	for i, e := range m.Entries {
		if e.Path == "" || !config.SafeText(e.Path) || filepath.IsAbs(e.Path) || filepath.Clean(e.Path) != e.Path || e.Path == ".." || strings.HasPrefix(e.Path, ".."+string(filepath.Separator)) || seen[e.Path] || e.Mode > 0777 || e.Size < 0 {
			return fail("invalid manifest path or metadata %q", e.Path)
		}
		if i == 0 && e.Path != "." {
			return fail("manifest root must be first")
		}
		if i > 0 && !m.Entries[0].Directory {
			return fail("file manifest cannot have children")
		}
		if e.Path != "." && !seen[filepath.Dir(e.Path)] {
			return fail("manifest parents must precede children")
		}
		if !e.Directory {
			b, err := hex.DecodeString(e.SHA256)
			if err != nil || len(b) != sha256.Size {
				return fail("invalid SHA-256")
			}
		}
		seen[e.Path] = true
	}
	return nil
}
func Describe(ctx context.Context, path string) (Manifest, error) {
	m := Manifest{}
	path, err := filepath.Abs(path)
	if err != nil {
		return m, err
	}
	err = filepath.WalkDir(path, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fail("only regular files/directories are supported (symlink/device refused): %s", p)
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		e := Entry{Path: rel, Directory: info.IsDir(), Size: info.Size(), Mode: uint32(info.Mode().Perm())}
		if !e.Directory {
			e.SHA256, err = Hash(p)
			if err != nil {
				return err
			}
		}
		m.Entries = append(m.Entries, e)
		return nil
	})
	if err != nil {
		return m, err
	}
	return m, m.Validate()
}
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if abs == string(filepath.Separator) {
		return abs, nil
	}
	parent := filepath.Dir(abs)
	suffix := []string{filepath.Base(abs)}
	for {
		_, err = os.Lstat(parent)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		suffix = append([]string{filepath.Base(parent)}, suffix...)
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		parent = next
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{parent}, suffix...)...), nil
}
func SnapshotPath(path string) (Snapshot, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return Snapshot{Kind: "missing"}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Snapshot{}, fail("symbolic destination refused: %s", path)
	}
	if info.IsDir() {
		stat := info.Sys().(*syscall.Stat_t)
		return Snapshot{Kind: "directory", Signature: fmt.Sprintf("%d/%d/%o", stat.Dev, stat.Ino, info.Mode().Perm())}, nil
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, fail("nonregular destination refused: %s", path)
	}
	hash, err := Hash(path)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Kind: "file", Signature: fmt.Sprintf("%s/%d/%o/%d", hash, info.Size(), info.Mode().Perm(), info.ModTime().UnixNano())}, nil
}
func noLinks(path string) error {
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fail("symbolic destination ancestor refused: %s", p)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

// PlanCopyDestination accepts an existing directory for a single-file copy.
// Directory sources retain the exact contents-merge semantics of PlanDestination.
func PlanCopyDestination(ctx context.Context, destination, sourceName string, m Manifest) (Plan, error) {
	if err := m.Validate(); err != nil {
		return Plan{}, err
	}
	if !m.Entries[0].Directory {
		if sourceName == "" || sourceName == "." || sourceName == ".." || filepath.Base(sourceName) != sourceName || !config.SafeText(sourceName) {
			return Plan{}, fail("source filename must be a single path component")
		}
		path, err := Canonical(destination)
		if err != nil {
			return Plan{}, err
		}
		snapshot, err := SnapshotPath(path)
		if err != nil {
			return Plan{}, err
		}
		if snapshot.Kind == "directory" {
			destination = filepath.Join(path, sourceName)
		}
	}
	return PlanDestination(ctx, destination, m)
}

func PlanDestination(ctx context.Context, destination string, m Manifest) (Plan, error) {
	p := Plan{Manifest: m}
	if err := nativeAliases(m); err != nil {
		return p, err
	}
	if err := m.Validate(); err != nil {
		return p, err
	}
	var err error
	p.Destination, err = Canonical(destination)
	if err != nil {
		return p, err
	}
	if p.Destination == string(filepath.Separator) {
		return p, fail("filesystem root is not a transfer destination")
	}
	for _, e := range m.Entries {
		if ctx.Err() != nil {
			return p, ctx.Err()
		}
		target := filepath.Join(p.Destination, e.Path)
		if err = noLinks(target); err != nil {
			return p, err
		}
		snap, err := SnapshotPath(target)
		if err != nil {
			return p, err
		}
		if (e.Directory && snap.Kind == "file") || (!e.Directory && snap.Kind == "directory") {
			return p, fail("source/destination type mismatch: %s", target)
		}
		p.Before = append(p.Before, snap)
		if snap.Kind == "file" {
			p.Overwrites++
		}
	}
	return p, nil
}
func (p Plan) Validate() error {
	if !filepath.IsAbs(p.Destination) || filepath.Clean(p.Destination) != p.Destination || p.Destination == string(filepath.Separator) || !config.SafeText(p.Destination) || len(p.Before) != len(p.Manifest.Entries) {
		return fail("invalid transfer plan")
	}
	return p.Manifest.Validate()
}
func (p Plan) Unchanged() error {
	if err := p.Validate(); err != nil {
		return err
	}
	for i, e := range p.Manifest.Entries {
		target := filepath.Join(p.Destination, e.Path)
		if err := noLinks(target); err != nil {
			return err
		}
		now, err := SnapshotPath(target)
		if err != nil {
			return err
		}
		if now != p.Before[i] {
			return protocol.Fail("DESTINATION_CHANGED", 4, "destination changed since preflight: %s", target)
		}
	}
	return nil
}
func statePath(store config.Store, id string) (string, error) {
	if !config.ValidName(id) {
		return "", fail("invalid transfer ID")
	}
	return filepath.Join(store.Root, "runtime", "transfers", id+".json"), nil
}
func stageFile(s Stage, index int) string { return filepath.Join(s.Path, fmt.Sprintf("f%06d", index)) }
func StageFile(s Stage, index int) string { return stageFile(s, index) }
func Prepare(store config.Store, p Plan) (Stage, error) {
	s := Stage{ID: "t" + config.NewID(), Plan: p}
	if err := p.Unchanged(); err != nil {
		return s, err
	}
	parent := filepath.Dir(p.Destination)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return s, err
	}
	if err := noLinks(parent); err != nil {
		return s, err
	}
	dir, err := os.MkdirTemp(parent, ".tdev-transfer-")
	if err != nil {
		return s, err
	}
	s.Path = dir
	if err = saveStage(store, s); err != nil {
		os.RemoveAll(dir)
		return s, err
	}
	return s, nil
}
func saveStage(store config.Store, s Stage) error {
	path, err := statePath(store, s.ID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err = config.AtomicWrite(filepath.Join(s.Path, "receipt.json"), b, 0600); err != nil {
		return err
	}
	return config.AtomicWrite(path, b, 0600)
}
func Load(store config.Store, id string) (Stage, error) {
	var s Stage
	path, err := statePath(store, id)
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, err
	}
	if s.ID != id || !filepath.IsAbs(s.Path) || !strings.HasPrefix(filepath.Base(s.Path), ".tdev-transfer-") {
		return s, fail("invalid transfer receipt")
	}
	info, err := os.Lstat(s.Path)
	if err != nil {
		return s, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return s, fail("transfer stage identity changed")
	}
	receipt, err := os.ReadFile(filepath.Join(s.Path, "receipt.json"))
	if err != nil || string(receipt) != string(b) {
		return s, fail("transfer receipt mismatch")
	}
	return s, nil
}
func Abort(store config.Store, id string) error {
	s, err := Load(store, id)
	if err != nil {
		return err
	}
	if err = os.RemoveAll(s.Path); err != nil {
		return err
	}
	path, _ := statePath(store, id)
	return os.Remove(path)
}
func Export(ctx context.Context, store config.Store, path string) (Stage, error) {
	m, err := Describe(ctx, path)
	if err != nil {
		return Stage{}, err
	}
	s := Stage{ID: "t" + config.NewID(), Export: true, Plan: Plan{Manifest: m, Destination: path}}
	s.Path, err = os.MkdirTemp("", ".tdev-transfer-")
	if err != nil {
		return s, err
	}
	if err = saveStage(store, s); err != nil {
		os.RemoveAll(s.Path)
		return s, err
	}
	for i, e := range m.Entries {
		if !e.Directory {
			if err = copyFile(ctx, filepath.Join(path, e.Path), stageFile(s, i), e); err != nil {
				_ = Abort(store, s.ID)
				return s, err
			}
		}
	}
	return s, nil
}
func copyFile(ctx context.Context, source, destination string, e Entry) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fail("source changed or is not a regular file")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	h, err := Hash(destination)
	if err != nil {
		return err
	}
	if h != e.SHA256 {
		return protocol.Fail("SOURCE_CHANGED", 4, "source changed during transfer: %s", source)
	}
	return nil
}
func Commit(ctx context.Context, store config.Store, id string) (any, error) {
	unlock, err := store.Lock(ctx, id, "transfer")
	if err != nil {
		return nil, err
	}
	defer unlock()
	s, err := Load(store, id)
	if err != nil {
		return nil, err
	}
	if s.Export {
		return nil, fail("export stages cannot be committed")
	}
	if err = s.Plan.Unchanged(); err != nil {
		return nil, err
	}
	for i, e := range s.Plan.Manifest.Entries {
		if !e.Directory {
			info, err := os.Lstat(stageFile(s, i))
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() || info.Size() != e.Size {
				return nil, fail("staged file size/type mismatch")
			}
			h, err := Hash(stageFile(s, i))
			if err != nil {
				return nil, err
			}
			if h != e.SHA256 {
				return nil, protocol.Fail("VERIFICATION_FAILED", 2, "staged file checksum mismatch: %s", e.Path)
			}
		}
	}
	written := []string{}
	partial := func(err error) (any, error) {
		return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "directory transfer interrupted; committed files are retained: " + err.Error(), Details: map[string]any{"written": written, "stage": s.Path, "transfer_id": id}}
	}
	for i, e := range s.Plan.Manifest.Entries {
		if ctx.Err() != nil {
			return partial(ctx.Err())
		}
		path := filepath.Join(s.Plan.Destination, e.Path)
		if err = noLinks(path); err != nil {
			return partial(err)
		}
		if e.Directory {
			if err = os.MkdirAll(path, os.FileMode(e.Mode)|0700); err != nil {
				return partial(err)
			}
			continue
		}
		now, err := SnapshotPath(path)
		if err != nil {
			return partial(err)
		}
		if now != s.Plan.Before[i] {
			return partial(protocol.Fail("DESTINATION_CHANGED", 4, "destination changed: %s", path))
		}
		if err = os.Chmod(stageFile(s, i), os.FileMode(e.Mode)); err != nil {
			return partial(err)
		}
		if err = os.Rename(stageFile(s, i), path); err != nil {
			return partial(err)
		}
		written = append(written, path)
	}
	// Change modes on newly created directories only; preserve existing directory access settings.
	for i := len(s.Plan.Manifest.Entries) - 1; i >= 0; i-- {
		e := s.Plan.Manifest.Entries[i]
		if e.Directory && s.Plan.Before[i].Kind == "missing" {
			if err = os.Chmod(filepath.Join(s.Plan.Destination, e.Path), os.FileMode(e.Mode)); err != nil {
				return partial(err)
			}
		}
	}
	if err = Abort(store, id); err != nil {
		return partial(err)
	}
	return map[string]any{"destination": s.Plan.Destination, "files": len(written), "status": "complete"}, nil
}
