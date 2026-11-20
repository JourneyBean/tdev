package sshx

import (
	"errors"
	"testing"

	"tdev/internal/protocol"
)

func TestOpenSSHAuthenticationError(t *testing.T) {
	for _, test := range []struct {
		stderr string
		code   string
	}{
		{"developer@dev-host: Permission denied (password).", AuthRequired},
		{"Received disconnect: Too many authentication failures", AuthRequired},
		{"scp: dest open /project/file: Permission denied", "CONNECTION_FAILED"},
		{"ssh: connect to host dev-host port 22: Connection refused", "CONNECTION_FAILED"},
		{"Host key verification failed.", "CONNECTION_FAILED"},
	} {
		t.Run(test.stderr, func(t *testing.T) {
			result := protocol.Normalize(Error(errors.New("exit status 255"), test.stderr))
			if result.Code != test.code || result.Exit != 6 {
				t.Fatalf("error = %+v, want code %s and exit 6", result, test.code)
			}
		})
	}
}
