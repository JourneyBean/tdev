package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"tdev/internal/config"
	"tdev/internal/transfer"
)

func releaseFixture(t *testing.T, version string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "test-signing.pem")
	if err = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	program := "#!/bin/sh\nprintf '%s\\n' '{\"protocol_version\":1,\"ok\":true,\"data\":{\"version\":\"" + version + "\",\"protocol_version\":1,\"schema_version\":1}}'\n"
	for _, goos := range []string{"linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if err = os.WriteFile(filepath.Join(dir, "tdev-"+goos+"-"+arch), []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = BuildRelease(dir, version, keyPath); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "release.pub")
}
func TestSignaturesTamperAndVersion(t *testing.T) {
	ctx := context.Background()
	dir, pub := releaseFixture(t, "0.1.0")
	b, err := Load(ctx, dir, "", pub, "0.1.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	_, other := releaseFixture(t, "0.1.0")
	if _, err = Load(ctx, dir, "", other, "", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted unrelated trust key")
	}
	if _, err = Load(ctx, dir, "", pub, "0.2.0", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("ignored requested version")
	}
	path := filepath.Join(dir, "tdev-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err = os.WriteFile(path, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(ctx, dir, "", pub, "", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted changed artifact")
	}
}
func TestPackagedInstallerRejectsTamperedBinary(t *testing.T) {
	dir, _ := releaseFixture(t, "0.1.0")
	template, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err = BuildInstaller(dir, string(template)); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "executed")
	asset := filepath.Join(dir, "tdev-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err = os.WriteFile(asset, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(dir, "install.sh"), "--release-dir", dir, "--no-modify-path")
	cmd.Env = append(os.Environ(), "OPENSSL=/nonexistent/openssl")
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("accepted tampered packaged binary: %s", output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed unauthenticated bytes")
	}
}

func TestInstallUpgradeRollbackAndExternalChanges(t *testing.T) {
	ctx := context.Background()
	store := config.Store{Root: t.TempDir()}
	destination := filepath.Join(t.TempDir(), "bin", "tdev")
	v1, pub1 := releaseFixture(t, "0.1.0")
	b, err := Load(ctx, v1, "", pub1, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := Install(ctx, store, b, destination)
	if err != nil {
		t.Fatal(err)
	}
	if r1.PreviousPath != "" {
		t.Fatal("invented previous binary")
	}
	v2, pub2 := releaseFixture(t, "0.1.1")
	next, err := Load(ctx, v2, "", pub2, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Install(ctx, store, next, destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(r2.PreviousPath); err != nil {
		t.Fatal("backup missing", err)
	}
	if _, err = Rollback(ctx, store, destination); err != nil {
		t.Fatal(err)
	}
	if err = Candidate(ctx, destination, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	r, err := ReadReceipt(destination)
	if err != nil || r.Version != "0.1.0" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err = Install(ctx, store, next, destination); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(destination, []byte("outside edit"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(ctx, store, destination); err == nil {
		t.Fatal("rolled back over external modification")
	}
}
func TestUntrustedManifestAndSymbolicTarget(t *testing.T) {
	dir, pub := releaseFixture(t, "0.1.0")
	data, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	signature, _ := os.ReadFile(filepath.Join(dir, "manifest.sig"))
	key, _ := os.ReadFile(pub)
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	m["version"] = "0.9.9"
	changed, _ := json.Marshal(m)
	if _, err := VerifyManifest(changed, signature, key); err == nil {
		t.Fatal("accepted modified signed manifest")
	}
	b, err := Load(context.Background(), dir, "", pub, "", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "original")
	if err = os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err = os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Install(context.Background(), config.Store{Root: t.TempDir()}, b, link); err == nil {
		t.Fatal("followed install symlink")
	}
	hash, _ := transfer.Hash(victim)
	if hash == b.Artifact.SHA256 {
		t.Fatal("overwrote symlink victim")
	}
}

func TestBootstrapRefusesTamperBeforeExecution(t *testing.T) {
	dir, pub := releaseFixture(t, "0.1.0")
	marker := filepath.Join(t.TempDir(), "executed")
	binary := filepath.Join(dir, "tdev-"+runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "../../scripts/install.sh", "--release-dir", dir, "--public-key", pub, "--destination", filepath.Join(t.TempDir(), "tdev"))
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("bootstrap accepted tamper: %s", output)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unverified candidate was executed")
	}
	if _, err = Load(context.Background(), "", "http://example.invalid/release", pub, "", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted insecure release source")
	}
}

func TestHTTPSDownloadAndRedirectPolicy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("isolated test CA uses Linux SSL_CERT_FILE")
	}
	dir, pub := releaseFixture(t, "0.1.0")
	server := httptest.NewTLSServer(http.FileServer(http.Dir(dir)))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", ca)
	b, err := Load(context.Background(), "", server.URL, pub, "0.1.0", runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	path := b.Directory
	b.Close()
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("download staging was not cleaned")
	}
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/insecure", http.StatusFound)
	}))
	defer redirect.Close()
	if _, err = Load(context.Background(), "", redirect.URL, pub, "", runtime.GOOS, runtime.GOARCH); err == nil {
		t.Fatal("accepted HTTPS downgrade redirect")
	}
}
