package cli

import (
	"bytes"
	"encoding/json"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"tdev/internal/config"
	"testing"
	"time"

	"tdev/internal/protocol"
)

func run(t *testing.T, root, input string, args ...string) (int, protocol.Response) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := Execute(append([]string{"--config-dir", root, "--json"}, args...), strings.NewReader(input), &out, &stderr)
	var r protocol.Response
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("stdout not one JSON response: %s stderr=%s err=%v", out.String(), stderr.String(), err)
	}
	return code, r
}
func TestProtocolAndRevisionConflict(t *testing.T) {
	root := t.TempDir()
	dir := t.TempDir()
	code, r := run(t, root, "", "api", "project", "create", "demo", "--directory", dir)
	if code != 0 || !r.OK {
		t.Fatalf("%d %+v", code, r)
	}
	p, _ := (config.Store{Root: root}).Get("demo")
	p.Revision = 99
	input, _ := json.Marshal(p)
	code, r = run(t, root, string(input), "api", "project", "set", "demo", "--stdin", "--expected-revision", "99")
	if code != 4 || r.Error.Code != "CONFIG_CONFLICT" {
		t.Fatalf("%d %+v", code, r)
	}
	code, r = run(t, root, "", "api", "project", "create", "../oops", "--directory", dir)
	if code != 2 || r.OK {
		t.Fatalf("%d %+v", code, r)
	}
	code, r = run(t, root, "", "api", "project", "get", "missing")
	if code != 3 || r.Error.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("%d %+v", code, r)
	}
}
func TestInvalidBatchDoesNotPartiallySave(t *testing.T) {
	root := t.TempDir()
	run(t, root, "", "api", "project", "create", "demo", "--directory", t.TempDir())
	before, _ := os.ReadFile(filepath.Join(root, "projects", "demo.yaml"))
	code, r := run(t, root, "", "forward", "local", "-L", "8000", "-R", "0")
	if code != 2 || r.OK {
		t.Fatalf("%d %+v", code, r)
	}
	after, _ := os.ReadFile(filepath.Join(root, "projects", "demo.yaml"))
	if !bytes.Equal(before, after) {
		t.Fatal("invalid batch partially saved")
	}
}
func TestRemoteQuoting(t *testing.T) {
	s := remoteCommand(target{Server: server{RemoteBinary: "/home/user/a b/tdev"}}, "api", "project", "get", "a'$(false)")
	if s != "'/home/user/a b/tdev' 'api' 'project' 'get' 'a'\"'\"'$(false)'" {
		t.Fatal(s)
	}
}
func TestConfirmFromPipeAndNoInput(t *testing.T) {
	for _, tc := range []struct {
		input  string
		accept bool
	}{{"yes\n", true}, {"y\n", true}, {"", false}, {"no\n", false}} {
		a := &app{in: strings.NewReader(tc.input), errOut: &bytes.Buffer{}}
		a.root = &cobra.Command{}
		a.root.Flags().Bool("yes", false, "")
		err := a.confirm("test")
		if (err == nil) != tc.accept {
			t.Fatalf("%q %v", tc.input, err)
		}
	}
}

func TestRetryClassification(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		retry         bool
	}{
		{"CONNECTION_FAILED", "Connection timed out", true},
		{"CONNECTION_FAILED", "Connection reset by peer", true},
		{"CONNECTION_FAILED", "Permission denied (publickey)", false},
		{"CONNECTION_FAILED", "Host key verification failed", false},
		{"CONNECTION_FAILED", "unclassified exit status 255", false},
		{"SESSION_NOT_FOUND", "Connection closed", false},
	} {
		err := protocol.Fail(tc.code, 6, "%s", tc.message)
		if got := retryable(err); got != tc.retry {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}

func TestPrivateConnectionProfileAndPolicyOverrides(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(t.TempDir(), "身份 key")
	if err := os.WriteFile(identity, []byte("fixture metadata only"), 0600); err != nil {
		t.Fatal(err)
	}
	c := &cobra.Command{}
	setupConnectionFlags(c)
	_ = c.Flags().Set("host", "example.invalid")
	_ = c.Flags().Set("port", "2222")
	_ = c.Flags().Set("user", "developer")
	_ = c.Flags().Set("identity-file", identity)
	_ = c.Flags().Set("terminal-reconnect-attempts", "always")
	_ = c.Flags().Set("forward-reconnect-interval", "125ms")
	a := &app{store: config.Store{Root: root}}
	s, cleanup, err := a.newConnection(c, server{SSHAlias: "demo", RemoteBinary: ".local/bin/tdev"})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	b, err := os.ReadFile(s.SSHConfig)
	if err != nil || !bytes.Contains(b, []byte("身份 key")) || !bytes.Contains(b, []byte("Port 2222")) {
		t.Fatalf("profile %q %v", b, err)
	}
	info, err := os.Stat(s.SSHConfig)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("profile permissions", err)
	}
	prefs := defaultPreferences()
	if err = applySetupPolicies(c, &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs.Reconnect.Terminal.Attempts != -1 || prefs.Reconnect.Forward.Interval != 125*time.Millisecond {
		t.Fatal(prefs.Reconnect)
	}
	yamlText := "schema_version: 1\ndefault_server: demo\nservers:\n  demo:\n    ssh_alias: demo\n    remote_binary: .local/bin/tdev\n    reconnect:\n      terminal:\n        attempts: 0\n"
	if err = os.WriteFile(filepath.Join(root, "client.yaml"), []byte(yamlText), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.serverTarget(p, "demo", "project")
	if err != nil || target.Reconnect.Terminal.Attempts != 0 || target.Reconnect.Terminal.Interval != 2*time.Second || target.Reconnect.Forward.Attempts != 5 {
		t.Fatalf("inheritance %+v %v", target.Reconnect, err)
	}
}
