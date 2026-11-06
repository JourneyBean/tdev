package gitlabrelease

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitLabBuildGuardsAndKeyCleanup(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not installed")
	}
	script, err := filepath.Abs("../../scripts/gitlab-build.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "unprotected", "version_mismatch", "wrong_key", "debug_trace"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"docs/releases", "internal/cli"} {
				if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			_, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			der, _ := x509.MarshalPKCS8PrivateKey(private)
			publicDER, _ := x509.MarshalPKIXPublicKey(private.Public())
			keyPath := filepath.Join(t.TempDir(), "signing.pem")
			if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
				t.Fatal(err)
			}
			pub := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
			if mode == "wrong_key" {
				pub = []byte("different public key")
			}
			files := map[string]string{
				"docs/release.pub":        string(pub),
				"docs/releases/v0.2.0.md": "# v0.2.0\n",
				"internal/cli/cli.go":     "var Version = \"0.2.0\"\n",
				"Makefile":                "VERSION ?= 0.2.0\nrelease:\n\t@test -r \"$$TDEV_SIGNING_KEY\"\n\t@printf '%s\\n' \"$$TDEV_RELEASE_URL\" \"$$TDEV_UPGRADE_URL\" \"$$TDEV_SIGNING_KEY\" > result\n",
			}
			if mode == "version_mismatch" {
				files["internal/cli/cli.go"] = "var Version = \"0.1.0\"\n"
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			protected, trace := "true", "false"
			if mode == "unprotected" {
				protected = "false"
			}
			if mode == "debug_trace" {
				trace = "true"
			}
			cmd := exec.Command("sh", script)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "CI_COMMIT_REF_PROTECTED="+protected, "CI_DEBUG_TRACE="+trace, "CI_COMMIT_TAG=v0.2.0", "CI_PROJECT_URL=https://gitlab.example.invalid/group/demo", "TDEV_SIGNING_KEY="+keyPath)
			output, err := cmd.CombinedOutput()
			data, readErr := os.ReadFile(filepath.Join(dir, "result"))
			if mode != "success" {
				if err == nil || !os.IsNotExist(readErr) {
					t.Fatal("invalid build reached signing", mode, err)
				}
				return
			}
			if err != nil || readErr != nil {
				t.Fatalf("build script: %v %v %s", err, readErr, output)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) != 3 || lines[0] != "https://gitlab.example.invalid/group/demo/-/releases/v0.2.0/downloads" || lines[1] != "https://gitlab.example.invalid/group/demo/-/releases/permalink/latest/downloads" {
				t.Fatal("incorrect embedded sources", string(data))
			}
			if lines[2] == keyPath {
				t.Fatal("signing key was not copied to isolated staging")
			}
			if _, err := os.Stat(lines[2]); !os.IsNotExist(err) {
				t.Fatal("staged signing key retained after build", err)
			}
			if _, err := os.Stat(keyPath); err != nil {
				t.Fatal("original signing key changed", err)
			}
		})
	}
}
