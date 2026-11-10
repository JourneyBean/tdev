package cli

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	state, err := os.MkdirTemp("", "tdev-test-state-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = os.Setenv("XDG_STATE_HOME", state); err != nil {
		os.RemoveAll(state)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(state)
	os.Exit(code)
}
