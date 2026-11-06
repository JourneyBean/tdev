package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerDownloadAndUpgradeSources(t *testing.T) {
	for _, mode := range []string{"github", "gitlab", "explicit"} {
		t.Run(mode, func(t *testing.T) {
			dir, _ := releaseFixture(t, "0.2.0")
			template, err := os.ReadFile("../../scripts/install.sh")
			if err != nil {
				t.Fatal(err)
			}
			releaseURL, upgradeURL := "", ""
			wantDownload := "https://github.com/JourneyBean/tdev/releases/download/v0.2.0"
			wantUpgrade := "https://github.com/JourneyBean/tdev/releases/latest/download"
			if mode != "github" {
				releaseURL = "https://gitlab.example.invalid/group/demo's/-/releases/v0.2.0/downloads"
				upgradeURL = "https://gitlab.example.invalid/group/demo's/-/releases/permalink/latest/downloads"
				wantDownload, wantUpgrade = releaseURL, upgradeURL
			}
			if err := BuildInstallerWithSource(dir, string(template), releaseURL, upgradeURL); err != nil {
				t.Fatal(err)
			}
			tmp := t.TempDir()
			// Curl is replaced only in this test. The real generated installer and
			// signed fixture binary execute, with no public network dependency.
			curl := "#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\ncase \"$1\" in -o) output=$2; shift 2;; https://*) source=$1; shift;; *) shift;; esac\ndone\nprintf '%s\\n' \"$source\" >> \"$TDEV_TEST_FETCH_LOG\"\ncat \"$TDEV_TEST_RELEASE_DIR/${source##*/}\" > \"$output\"\n"
			if err := os.WriteFile(filepath.Join(tmp, "curl"), []byte(curl), 0700); err != nil {
				t.Fatal(err)
			}
			argv := []string{filepath.Join(dir, "install.sh"), "--no-modify-path", "--completion", "none", "--config-dir", filepath.Join(tmp, "config")}
			if mode == "explicit" {
				wantDownload, wantUpgrade = "https://mirror.example.invalid/tdev/0.2.0", "https://mirror.example.invalid/tdev/0.2.0"
				argv = append(argv, "--release-url", wantDownload)
			}
			argsFile, fetchLog := filepath.Join(tmp, "args"), filepath.Join(tmp, "fetches")
			cmd := exec.Command("sh", argv...)
			cmd.Env = append(os.Environ(), "PATH="+tmp+":"+os.Getenv("PATH"), "TDEV_TEST_INSTALL_ARGS="+argsFile, "TDEV_TEST_FETCH_LOG="+fetchLog, "TDEV_TEST_RELEASE_DIR="+dir, "OPENSSL=/missing/openssl")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("installer: %v %s", err, out)
			}
			fetches, err := os.ReadFile(fetchLog)
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range strings.Fields(string(fetches)) {
				if !strings.HasPrefix(source, wantDownload+"/") {
					t.Fatal("incorrect download source", source)
				}
			}
			args, err := os.ReadFile(argsFile)
			if err != nil || !strings.Contains(string(args), "--source-url\n"+wantUpgrade+"\n") {
				t.Fatal("incorrect remembered upgrade source", string(args), err)
			}
		})
	}
}

func TestInstallerRejectsUnsafeSources(t *testing.T) {
	for _, source := range []string{"http://server.example.invalid/release", "https://token@server.example.invalid/release", "https://server.example.invalid/release?token=secret", "https://server.example.invalid/release#fragment", "https://server.example.invalid/\nrelease"} {
		if err := BuildInstallerWithSource(t.TempDir(), "", source, source); err == nil {
			t.Fatal("accepted unsafe installer source")
		}
	}
	if err := BuildInstallerWithSource(t.TempDir(), "", "https://server.example.invalid/release", ""); err == nil {
		t.Fatal("accepted missing upgrade source")
	}
}
