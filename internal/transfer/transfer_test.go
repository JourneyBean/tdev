package transfer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0640); err != nil {
		t.Fatal(err)
	}
}
func fill(t *testing.T, ctx context.Context, source string, s Stage) {
	t.Helper()
	for i, e := range s.Plan.Manifest.Entries {
		if !e.Directory {
			if err := copyFile(ctx, filepath.Join(source, e.Path), StageFile(s, i), e); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestExactDirectorySemanticsAndConflict(t *testing.T) {
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	source := filepath.Join(t.TempDir(), "source")
	name := "quoted ' [x] 中文.txt"
	put(t, filepath.Join(source, "nested", name), "first")
	if err := os.Mkdir(filepath.Join(source, "empty"), 0750); err != nil {
		t.Fatal(err)
	}
	m, err := Describe(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, exists := range []bool{false, true} {
		destination := filepath.Join(t.TempDir(), "exact destination")
		if exists {
			put(t, filepath.Join(destination, "unrelated"), "keep")
		}
		p, err := PlanDestination(ctx, destination, m)
		if err != nil {
			t.Fatal(err)
		}
		s, err := Prepare(store, p)
		if err != nil {
			t.Fatal(err)
		}
		fill(t, ctx, source, s)
		if _, err = Commit(ctx, store, s.ID); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(destination, "nested", name))
		if err != nil || string(b) != "first" {
			t.Fatalf("wrong directory layer: %s %v", b, err)
		}
		if exists {
			b, _ := os.ReadFile(filepath.Join(destination, "unrelated"))
			if string(b) != "keep" {
				t.Fatal("deleted unrelated file")
			}
		}
		info, err := os.Stat(filepath.Join(destination, "empty"))
		if err != nil || !info.IsDir() {
			t.Fatal("lost empty directory")
		}
	}
	destination := filepath.Join(t.TempDir(), "out")
	put(t, filepath.Join(destination, "nested", name), "old")
	p, err := PlanDestination(ctx, destination, m)
	if err != nil || p.Overwrites != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	s, err := Prepare(store, p)
	if err != nil {
		t.Fatal(err)
	}
	defer Abort(store, s.ID)
	fill(t, ctx, source, s)
	put(t, filepath.Join(destination, "nested", name), "concurrent change")
	_, err = Commit(ctx, store, s.ID)
	if err == nil || protocol.Normalize(err).Code != "DESTINATION_CHANGED" {
		t.Fatal("overwrote concurrent change", err)
	}
	b, _ := os.ReadFile(filepath.Join(destination, "nested", name))
	if string(b) != "concurrent change" {
		t.Fatal("lost concurrent change")
	}
}
func TestTamperAndSymlinkRefusal(t *testing.T) {
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "file")
	put(t, source, "good")
	m, err := Describe(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "file")
	p, err := PlanDestination(ctx, destination, m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Prepare(store, p)
	if err != nil {
		t.Fatal(err)
	}
	defer Abort(store, s.ID)
	put(t, StageFile(s, 0), "evil")
	if _, err = Commit(ctx, store, s.ID); err == nil {
		t.Fatal("accepted tampered payload")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("tampered data reached destination")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Describe(ctx, link); err == nil {
		t.Fatal("followed source symlink")
	}
	if _, err = PlanDestination(ctx, link, m); err == nil {
		t.Fatal("followed destination symlink")
	}
}
func TestTraversalAndCanceledCommit(t *testing.T) {
	for _, path := range []string{"../outside", "/absolute", "a/../../x", "a\nname"} {
		m := Manifest{Entries: []Entry{{Path: path}}}
		if m.Validate() == nil {
			t.Fatal("accepted", path)
		}
	}
	ctx := context.Background()
	source := filepath.Join(t.TempDir(), "file")
	put(t, source, "ok")
	m, _ := Describe(ctx, source)
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "file")
	p, _ := PlanDestination(ctx, destination, m)
	s, err := Prepare(store, p)
	if err != nil {
		t.Fatal(err)
	}
	defer Abort(store, s.ID)
	fill(t, ctx, source, s)
	c, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = Commit(c, store, s.ID); err == nil {
		t.Fatal("canceled commit succeeded")
	}
	if _, err = os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("canceled commit wrote data")
	}
}

func TestMacUnicodeAndCaseAliasesRejectedBeforeWrites(t *testing.T) {
	for _, pair := range [][2]string{{"é", "e\u0301"}, {"한글", "한글"}, {"Readme", "README"}} {
		m := Manifest{Entries: []Entry{{Path: ".", Directory: true}, {Path: pair[0]}, {Path: pair[1]}}}
		if err := checkAliases(m, "darwin"); err == nil {
			t.Fatalf("accepted ambiguous macOS names %+v", pair)
		}
		if err := checkAliases(m, "linux"); err != nil {
			t.Fatal("rewrote/restricted Linux filenames", err)
		}
	}
}
