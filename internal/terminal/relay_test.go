package terminal

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/term"
	"tdev/internal/sshx"
)

func TestRelayRestoresTerminalAndStopsReaders(t *testing.T) {
	for _, mode := range []string{"exit", "cancel", "start-error"} {
		t.Run(mode, func(t *testing.T) {
			master, slave, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer slave.Close()
			if err := pty.Setsize(slave, &pty.Winsize{Rows: 37, Cols: 109}); err != nil {
				t.Fatal(err)
			}
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := "printf 'RELAY_READY\\n'; read line; stty size; printf 'RELAY_FINAL:%s\\n' \"$line\""
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
			var output sshx.Buffer
			if mode == "start-error" {
				cmd = exec.CommandContext(ctx, "/path/that/does/not/exist")
			}
			wait, err := StartRelay(cmd, slave, &output)
			if mode == "start-error" {
				if err == nil {
					t.Fatal("start unexpectedly succeeded")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if mode == "cancel" {
					cancel()
				} else {
					if _, err := master.WriteString("input\n"); err != nil {
						t.Fatal(err)
					}
				}
				if err := wait(); mode == "exit" && err != nil {
					t.Fatal(err)
				}
				if mode == "exit" && (!strings.Contains(output.String(), "RELAY_FINAL:input") || !strings.Contains(output.String(), "37 109")) {
					t.Fatalf("input, size or final output lost: %q", output.String())
				}
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("TTY not restored: %v", err)
			}
		})
	}
}

type failingOutput struct{}

var errOutput = errors.New("output closed")

func (failingOutput) Write([]byte) (int, error) { return 0, errOutput }

func TestRelayOutputFailureStopsChild(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf READY; read line")
	wait, err := StartRelay(cmd, slave, failingOutput{})
	if err != nil {
		t.Fatal(err)
	}
	if err := wait(); !errors.Is(err, errOutput) {
		t.Fatalf("output failure lost: %v", err)
	}
}
