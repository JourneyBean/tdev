package sshx

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseChannelsExcludesCommandsAndMuxControls(t *testing.T) {
	report := "The following connections are open:\r\n" +
		"  #0 arbitrary SECRET_COMMAND (t4 [session] r1 nm0 i0/12 o1/34 e[write]/0 fd 3/4/5 sock -1 cc 1)\r\n" +
		"  #1 local (t16 [mux-control] nr0 i0/0 o0/0 e[closed]/0 fd 6/6/-1 sock 6 cc -1)\r\n" +
		"  #2 private address (t4 [direct-tcpip] r2 i0/0 o0/0 e[closed]/0 fd 7/7/-1 sock 7 cc -1)\r\n"
	channels, status := parseChannels(report)
	if status != "available" || len(channels) != 2 || channels[0].ID != 0 || channels[0].InputBytes != 12 || channels[0].OutputBytes != 34 || channels[1].Type != "direct-tcpip" {
		t.Fatalf("unexpected channel snapshot: %+v %s", channels, status)
	}
	b, _ := json.Marshal(channels)
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "private address") {
		t.Fatal("channel descriptions leaked")
	}
	for _, output := range []string{"unexpected output", "The following connections are open:\n #2 unknown-format"} {
		if channels, status := parseChannels(output); status != "unrecognized" || len(channels) != 0 {
			t.Fatal("unknown report treated as empty")
		}
	}
}

func TestInspectUnsupportedChannelsPreservesMasterState(t *testing.T) {
	dir := t.TempDir()
	// A local command fixture verifies that no fallback starts an SSH connection.
	code := "#!/bin/sh\ncase \" $* \" in\n*' -O check '*) echo 'Master running (pid=123)' >&2; exit 0;;\n*' -O channels '*) echo 'Invalid multiplex command.' >&2; exit 255;;\nesac\nexit 90\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(code), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	i := Inspect(context.Background(), filepath.Join(dir, "socket"))
	if i.PID != 123 || i.State != "master-running" || i.ChannelsStatus != "unsupported-or-unavailable" {
		t.Fatalf("%+v", i)
	}
}
