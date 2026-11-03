package sshx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverIncludesAndDoesNotExecuteMatch(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".ssh", "profiles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(home, "must-not-exist")
	config := "Include \"profiles/*.conf\"\nHost * !excluded\n User developer\n Port 2222\nHost excluded\n HostName excluded.example.invalid\nMatch exec \"touch " + marker + "\"\n HostName must-not-apply\n"
	if err := os.WriteFile(filepath.Join(home, ".ssh/config"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev.conf"), []byte("Host = dev gpu\n HostName dev.example.invalid\n Port=2200\nHost *.internal !other\n IdentityFile /do/not/read/secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := DiscoverHosts("", home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	if entries[0].Alias != "dev" || entries[0].HostName != "dev.example.invalid" || entries[0].User != "developer" || entries[0].Port != "2200" {
		t.Fatal(entries[0])
	}
	if entries[2].User != "" || entries[2].HostName != "excluded.example.invalid" {
		t.Fatal(entries[2])
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed Match command during discovery")
	}
}

func TestDiscoveryRecursiveIncludeAndQuotes(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config")
	if err := os.WriteFile(path, []byte("Include "+path+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverHosts(path, home); err == nil {
		t.Fatal("accepted recursive Include")
	}
	words, err := ConfigWords(`Include = "~/SSH 配置/a.conf" # comment`)
	if err != nil || len(words) != 2 || words[1] != "~/SSH 配置/a.conf" {
		t.Fatal(words, err)
	}
	if _, err := ConfigWords(`Host "unfinished`); err == nil {
		t.Fatal("accepted unmatched quote")
	}
}
