package tmux

import (
	"context"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func TestScratchLifecycleAndOwnership(t *testing.T) {
	m, p, ctx := fixture(t)
	existing, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.CreateScratch(ctx, "debug", p.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "debug" || s.SessionName != "tdev-scratch-debug" || s.Generation == "" || s.SessionID == existing.SessionID || s.Directory != p.Directory {
		t.Fatalf("bad scratch: %+v", s)
	}
	marker, err := m.run(ctx, "show-options", "-qv", "-t", s.SessionID, "@tdev_project_id")
	if err != nil || marker != "" {
		t.Fatal("scratch has project marker", marker, err)
	}
	if _, err = m.CreateScratch(ctx, "debug", p.Directory); protocol.Normalize(err).Code != "SESSION_CONFLICT" {
		t.Fatal(err)
	}
	if _, err = m.CreateScratch(ctx, "../bad", p.Directory); err == nil {
		t.Fatal("invalid name accepted")
	}
	if _, err = m.CreateScratch(ctx, "bad-dir", "relative"); err == nil {
		t.Fatal("relative directory accepted")
	}
	if _, err = m.run(ctx, "new-session", "-d", "-s", "ordinary"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.run(ctx, "new-session", "-d", "-s", "tdev-scratch-occupied"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateScratch(ctx, "occupied", p.Directory); protocol.Normalize(err).Code != "SESSION_CONFLICT" {
		t.Fatal(err)
	}
	all, err := m.Scratches(ctx)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	// A stale attachment identity must never enter a replacement session.
	if err = m.Attach(ctx, config.Project{Name: "debug"}, s.SessionID, "wrong-generation", "", "token", strings.NewReader(""), nil, nil); protocol.Normalize(err).Code != "SESSION_NOT_FOUND" {
		t.Fatal(err)
	}
	if _, err = m.run(ctx, "kill-session", "-t", s.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ScratchStatus(ctx, "debug"); protocol.Normalize(err).Code != "SESSION_NOT_FOUND" {
		t.Fatal(err)
	}
	replacement, err := m.CreateScratch(ctx, "debug", p.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Generation == s.Generation {
		t.Fatal("reused generation")
	}
	current, err := m.Require(context.Background(), p)
	if err != nil || current.SessionID != existing.SessionID {
		t.Fatal("project changed", current, err)
	}
	generated, err := m.CreateScratch(ctx, "", "")
	if err != nil || len(generated.Name) != 36 || generated.SessionName != "tdev-scratch-"+generated.Name {
		t.Fatal(generated, err)
	}
	if _, err = m.CreateScratch(ctx, strings.Repeat("a", 64), p.Directory); err != nil {
		t.Fatal("maximum length name", err)
	}
	projects, err := m.Store.List()
	if err != nil || len(projects) != 1 {
		t.Fatal("scratch persisted as project", projects, err)
	}
}
