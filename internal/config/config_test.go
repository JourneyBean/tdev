package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

func TestStrictDecode(t *testing.T) {
	for _, text := range []string{"schema_version: 1\nschema_version: 2\n", "typo: true\n", "schema_version: 1\n---\nschema_version: 1\n"} {
		var p Project
		if err := Decode([]byte(text), &p); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}
func TestForwardOpenSSHOrdering(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"8000", "127.0.0.1:8000:localhost:8000"},
		{"3000:8000", "127.0.0.1:3000:localhost:8000"},
		{"9000:localhost:3000", "127.0.0.1:9000:localhost:3000"},
		{"[::1]:9000:[2001:db8::1]:3000", "[::1]:9000:[2001:db8::1]:3000"},
	} {
		for _, direction := range []string{"L", "R"} {
			f, err := ParseForward(direction, tc.input)
			if err != nil || f.Spec() != tc.want {
				t.Fatalf("%s %s: %s %v", direction, tc.input, f.Spec(), err)
			}
		}
	}
	for _, v := range []string{"0", "65536", "3000:/tmp/socket", "1:2:3:4:5", "[::1:3:4", "1:host:0", "-1", "1:host:2\n", "1:[garbage]:2"} {
		if _, err := ParseForward("L", v); err == nil {
			t.Fatalf("accepted invalid spec %q", v)
		}
	}
}
func TestConcurrentRevisionAndBackup(t *testing.T) {
	s := Store{Root: t.TempDir()}
	ctx := context.Background()
	p, err := s.Create(ctx, "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update(ctx, "demo", p.Revision, func(p *Project) error { p.Windows[0].Name = "updated"; return nil })
			if err == nil {
				successes.Add(1)
			} else if protocol.Normalize(err).Code == "CONFIG_CONFLICT" {
				conflicts.Add(1)
			} else {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || conflicts.Load() != 11 {
		t.Fatalf("success=%d conflict=%d", successes.Load(), conflicts.Load())
	}
	now, err := s.Get("demo")
	if err != nil || now.Revision != 2 {
		t.Fatalf("%+v %v", now, err)
	}
	backup := filepath.Join(s.Root, "projects", "demo.yaml.rev-1.bak")
	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	var old Project
	if err = Decode(b, &old); err != nil || old.Revision != 1 || old.Windows[0].Name != "main" {
		t.Fatalf("backup changed: %s %v", b, err)
	}
	for _, path := range []string{backup, filepath.Join(s.Root, "projects", "demo.yaml")} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("private permissions: %v %v", st, err)
		}
	}
	_, err = s.Update(ctx, "demo", 2, func(p *Project) error { p.Windows = nil; return nil })
	if err == nil {
		t.Fatal("invalid update succeeded")
	}
	unchanged, _ := s.Get("demo")
	if unchanged.Revision != 2 {
		t.Fatal("invalid update changed revision")
	}
}
func TestLockCancellation(t *testing.T) {
	s := Store{Root: t.TempDir()}
	unlock, err := s.Lock(context.Background(), "demo", "config")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = s.Lock(ctx, "demo", "config"); err == nil {
		t.Fatal("lock did not respect cancellation")
	}
}
func TestIdentityAndTraversal(t *testing.T) {
	s := Store{Root: t.TempDir()}
	for _, name := range []string{"../bad", "a/b", "a.b", "-bad", strings.Repeat("x", 65)} {
		if _, err := s.Create(context.Background(), name, t.TempDir()); err == nil {
			t.Fatal("accepted", name)
		}
	}
	p, err := s.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.ID = NewID()
	_, err = s.Update(context.Background(), "demo", 1, func(dst *Project) error { *dst = p; return nil })
	if err == nil {
		t.Fatal("identity changed")
	}
}
func TestWindowAndForwardValidation(t *testing.T) {
	s := Store{Root: t.TempDir()}
	p, err := s.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.Windows = append(p.Windows, p.Windows[0])
	if p.Validate() == nil {
		t.Fatal("duplicate window accepted")
	}
	p.Windows = p.Windows[:1]
	f, _ := ParseForward("R", "9000:3000")
	p.Forwards = []Forward{f, f}
	if p.Validate() == nil {
		t.Fatal("duplicate forward accepted")
	}
	b, _ := yaml.Marshal(p)
	var decoded Project
	if err = Decode(b, &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestUTF8Validation(t *testing.T) {
	for _, s := range []string{"中文_日本語_한국어_🙂", "e\u0301", "é"} {
		if !SafeText(s) {
			t.Fatalf("valid UTF-8 rejected %q", s)
		}
	}
	if SafeText(string([]byte{0xff, 0xfe})) {
		t.Fatal("invalid UTF-8 would be silently replaced in JSON")
	}
	if ValidName("中文") {
		t.Fatal("project identifiers must still follow the documented ASCII slug schema")
	}
}
