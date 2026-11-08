package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"tdev/internal/config"
)

func TestAutomaticForwards(t *testing.T) {
	var defs []config.Forward
	for _, direction := range []string{"L", "R"} {
		for _, host := range []string{"localhost", "127.0.0.1", "::1", "LOCALHOST", "server.example.invalid", "192.0.2.1", "2001:db8::1"} {
			defs = append(defs, config.Forward{ID: direction + host, Direction: direction, TargetHost: host})
		}
	}
	original := append([]config.Forward(nil), defs...)
	for _, local := range []bool{true, false} {
		t.Run(map[bool]string{true: "local", false: "remote"}[local], func(t *testing.T) {
			want := defs
			if local {
				want = append(append([]config.Forward(nil), defs[4:7]...), defs[11:14]...)
			}
			if got := automaticForwards(target{Local: local}, defs); !reflect.DeepEqual(got, want) {
				t.Fatalf("automatic forwards = %v, want %v", got, want)
			}
			if !reflect.DeepEqual(defs, original) {
				t.Fatal("saved forwarding definitions changed")
			}
		})
	}
}

func TestOpenLocalLoopbackForwardsSkipOccupiedListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	for _, host := range []string{"localhost", "127.0.0.1", "::1"} {
		t.Run(host, func(t *testing.T) {
			root := t.TempDir()
			var diagnostics bytes.Buffer
			a := &app{store: config.Store{Root: root}, errOut: &diagnostics}
			cleanup, err := a.openForwards(context.Background(), target{Local: true}, []config.Forward{{
				ID: "api", Direction: "L", BindAddress: "127.0.0.1", ListenPort: port, TargetHost: host, TargetPort: port,
			}})
			if err != nil {
				t.Fatal(err)
			}
			cleanup()
			if diagnostics.Len() != 0 {
				t.Fatalf("unexpected forwarding diagnostics: %s", &diagnostics)
			}
			if _, err := os.Stat(filepath.Join(root, "runtime")); !os.IsNotExist(err) {
				t.Fatalf("skipped forwarding created runtime files: %v", err)
			}
		})
	}
}
