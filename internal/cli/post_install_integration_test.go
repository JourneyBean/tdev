package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/tmux"
)

func TestPostInstallMigratesLiveSessionAndRetries(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	socket := "tdev-post-install-" + config.NewID()
	t.Setenv("TDEV_TMUX_SOCKET", socket)
	installer, target := buildCLI(t, "0.5.0"), buildCLI(t, "0.5.1")
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "signing.pem")
	if err = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	release := signedRelease(t, target, "0.5.1", key)
	root := t.TempDir()
	m := tmux.Manager{Store: config.Store{Root: root}, Socket: socket}
	ctx := context.Background()
	p, err := m.Store.Create(ctx, "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Ensure(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	runTmux := func(args ...string) string {
		t.Helper()
		b, e := exec.Command("tmux", m.Args(args...)...).CombinedOutput()
		if e != nil {
			t.Fatalf("tmux %v: %s %v", args, b, e)
		}
		return strings.TrimSpace(string(b))
	}
	t.Cleanup(func() { _ = exec.Command("tmux", m.Args("kill-server")...).Run() })
	terminal := terminalCLI(t, installer, root, "", "open", "local/demo")
	terminal.write(t, "printf 'POST_INSTALL_READY\\n'\r")
	terminal.waitText(t, "POST_INSTALL_READY\r\n")
	pane := runTmux("display-message", "-p", "-t", s.SessionID, "#{pane_id}:#{pane_pid}")
	stale := func() {
		runTmux("set-option", "-t", s.SessionID, "set-titles-string", "legacy-live-title")
		runTmux("set-option", "-u", "-t", s.SessionID, "@tdev_runtime_config_version")
	}
	stale()
	destination := filepath.Join(t.TempDir(), "tdev")
	checkReport := func(data any) {
		t.Helper()
		b, _ := json.Marshal(data)
		var decoded struct {
			PostInstall postInstallReport `json:"post_install"`
		}
		if json.Unmarshal(b, &decoded) != nil || decoded.PostInstall.Version != "0.5.1" || len(decoded.PostInstall.Hooks) != 2 {
			t.Fatalf("target hooks missing: %s", b)
		}
	}
	// The destination is new, but existing sessions still require migration.
	r := executeBinary(t, installer, root, "", "", 0, "install", "--release-dir", release, "--public-key", filepath.Join(release, "release.pub"), "--destination", destination, "--completion", "none", "-y")
	checkReport(r.Data)
	waitAlert := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			parts := strings.Split(terminal.output.String(), "\x1b]0;")
			if len(parts) > 1 && strings.HasPrefix(parts[len(parts)-1], s.SessionName+":main\a") {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("live terminal title did not migrate")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitAlert()
	if got := runTmux("display-message", "-p", "-t", s.SessionID, "#{pane_id}:#{pane_pid}"); got != pane {
		t.Fatal("live pane replaced", got)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	stale()
	r = executeBinary(t, destination, root, "", "", 0, "api", "binary", "install", "--release-dir", release, "--public-key", filepath.Join(release, "release.pub"), "--destination", destination)
	checkReport(r.Data)
	after, _ := os.Stat(destination)
	if !os.SameFile(info, after) {
		t.Fatal("identical installation replaced executable")
	}
	waitAlert()
	// A future schema produces partial success while later hooks still run.
	runTmux("set-option", "-t", s.SessionID, "@tdev_runtime_config_version", "2")
	r = executeBinary(t, destination, root, "", "", 9, "upgrade", "--release-dir", release, "-y")
	if r.Error == nil || r.Error.Code != "PARTIAL_SUCCESS" {
		t.Fatalf("future schema: %+v", r)
	}
	b, _ := json.Marshal(r.Error.Details)
	var details struct {
		PostInstall postInstallReport `json:"post_install"`
	}
	if json.Unmarshal(b, &details) != nil || len(details.PostInstall.Hooks) != 2 || details.PostInstall.Hooks[0].Status != "failed" || details.PostInstall.Hooks[1].Status != "succeeded" {
		t.Fatalf("partial hook report: %s", b)
	}
	if runTmux("show-options", "-v", "-t", s.SessionID, "@tdev_runtime_config_version") != "2" {
		t.Fatal("future schema overwritten")
	}
	stale()
	r = executeBinary(t, destination, root, "", "", 0, "post-install")
	b, _ = json.Marshal(r.Data)
	var retry postInstallReport
	if json.Unmarshal(b, &retry) != nil || len(retry.Hooks) != 2 {
		t.Fatalf("retry result: %s", b)
	}
	waitAlert()
	if current, e := m.Status(ctx, p); e != nil || current.Generation != s.Generation {
		t.Fatal("session identity changed")
	}
	runTmux("detach-client", "-s", s.SessionID)
	terminal.exit(t, 0)
}
