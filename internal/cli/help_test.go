package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandAudienceBoundary(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"--help"}, {"api", "--help"}, {"edit", "--help"}} {
		var out, stderr bytes.Buffer
		if code := Execute(append([]string{"--config-dir", root}, args...), strings.NewReader(""), &out, &stderr); code != 0 {
			t.Fatalf("%v: exit=%d stderr=%s", args, code, stderr.String())
		}
		help := out.String()
		if args[0] == "--help" {
			for _, heading := range []string{"Workspace commands:", "Client configuration and tools:", "Client/server machine interface:"} {
				if !strings.Contains(help, heading) {
					t.Fatalf("missing %q in %s", heading, help)
				}
			}
		}
		for _, flag := range []string{"--ssh-config", "--yes"} {
			if strings.Contains(help, flag) == (args[0] == "api") {
				t.Fatalf("unexpected client flag visibility for %v: %s", args, help)
			}
		}
	}
	for _, flag := range []string{"--ssh-config=unused", "--yes"} {
		for _, args := range [][]string{{"api", "version", flag}, {flag, "api", "version"}} {
			code, response := run(t, root, "", args...)
			if code != 2 || response.OK || response.Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("local accepted client flag %v: %d %+v", args, code, response)
			}
		}
	}
	// User flags remain valid before and after nested user commands.
	for _, args := range [][]string{
		{"--yes", "--ssh-config=unused", "version"},
		{"show", "--yes", "--ssh-config=unused"},
	} {
		_, response := run(t, root, "", args...)
		if !response.OK && strings.Contains(response.Error.Message, "unknown flag") {
			t.Fatalf("user flags rejected for %v: %+v", args, response)
		}
	}
}
