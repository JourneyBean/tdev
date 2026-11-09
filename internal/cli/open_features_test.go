package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenFeatureValidationPrecedesEffects(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"--only", "unknown"}, "unknown open feature"},
		{[]string{"--only", "web"}, "future HTTP"},
		{[]string{"--only="}, "requires"},
		{[]string{"--only", "shell", "--mount"}, "combining"},
		{[]string{"--only", "shell"}, "requires an interactive terminal"},
	} {
		var out, stderr bytes.Buffer
		args := append([]string{"--config-dir", root, "open", "missing"}, tc.args...)
		code := Execute(args, strings.NewReader(""), &out, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), tc.message) {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
	}
	_, r := run(t, root, "", "create", "demo", "--directory", t.TempDir())
	if !r.OK {
		t.Fatal(r)
	}
	// Service-only opens work without a terminal and don't ensure a tmux session.
	code, r := run(t, root, "", "open", "local/demo", "--only", "forward")
	if code != 0 || !r.OK {
		t.Fatalf("service-only open: %d %+v", code, r)
	}
	code, r = run(t, root, "", "api", "session", "status", "demo")
	if code != 0 || !r.OK {
		t.Fatal(r)
	}
	data, err := json.Marshal(r.Data)
	if err != nil {
		t.Fatal(err)
	}
	b := string(data)
	if !strings.Contains(b, `"status":"missing"`) {
		t.Fatal("service-only open created a session", b)
	}
}

func TestOpenFeatureCompletion(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Execute([]string{"--config-dir", t.TempDir(), "__complete", "open", "--only", "shell,f"}, strings.NewReader(""), &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), "shell,forward\n") {
		t.Fatalf("comma completion: %d %s %s", code, out.String(), stderr.String())
	}
}
