// Package update verifies signed release manifests before selecting or executing an artifact.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/transfer"
)

var versionRE = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$`)

const MaxArtifact = 256 * 1024 * 1024

type Artifact struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	File          string `json:"file"`
	SHA256        string `json:"sha256"`
	Size          int64  `json:"size"`
	Archive       string `json:"archive"`
	ArchiveSHA256 string `json:"archive_sha256"`
}
type Manifest struct {
	FormatVersion       int        `json:"format_version"`
	Version             string     `json:"version"`
	ProtocolVersion     int        `json:"protocol_version"`
	ConfigSchemaVersion int        `json:"config_schema_version"`
	Artifacts           []Artifact `json:"artifacts"`
}
type Bundle struct {
	Manifest                            Manifest
	Artifact                            Artifact
	Directory, Path, SourceURL          string
	PublicKey, ManifestBytes, Signature []byte
	cleanup                             func()
}

func (b Bundle) Close() {
	if b.cleanup != nil {
		b.cleanup()
	}
}
func VerifyError(format string, args ...any) error {
	return protocol.Fail("VERIFICATION_FAILED", 2, format, args...)
}
func PublicKey(data []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, VerifyError("expected one PEM public key")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, VerifyError("release key must be Ed25519")
	}
	return pub, nil
}
func VerifyManifest(data, sig, pub []byte) (Manifest, error) {
	var m Manifest
	key, err := PublicKey(pub)
	if err != nil {
		return m, err
	}
	if len(sig) != ed25519.SignatureSize || !ed25519.Verify(key, data, sig) {
		return m, VerifyError("release manifest signature is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return m, VerifyError("manifest contains trailing data")
	}
	if m.FormatVersion != 1 || m.ProtocolVersion != protocol.Version || m.ConfigSchemaVersion != config.SchemaVersion || !versionRE.MatchString(m.Version) {
		return m, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "unsupported release protocol/schema/version")
	}
	seen := map[string]bool{}
	for _, a := range m.Artifacts {
		expected := "tdev-" + a.OS + "-" + a.Arch
		if (a.OS != "linux" && a.OS != "darwin") || (a.Arch != "amd64" && a.Arch != "arm64") || a.File != expected || a.Archive != expected+".tar.gz" || a.Size < 1 || a.Size > MaxArtifact || seen[expected] {
			return m, VerifyError("invalid/duplicate release artifact")
		}
		for _, h := range []string{a.SHA256, a.ArchiveSHA256} {
			raw, err := hex.DecodeString(h)
			if err != nil || len(raw) != 32 {
				return m, VerifyError("invalid artifact digest")
			}
		}
		seen[expected] = true
	}
	if len(seen) != 4 {
		return m, VerifyError("release must include all four supported platforms")
	}
	return m, nil
}
func (b Bundle) Check() error {
	info, err := os.Lstat(b.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != b.Artifact.Size {
		return VerifyError("artifact size/type mismatch")
	}
	h, err := transfer.Hash(b.Path)
	if err != nil {
		return err
	}
	if h != b.Artifact.SHA256 {
		return VerifyError("artifact SHA-256 mismatch")
	}
	return nil
}
func Load(ctx context.Context, dir, baseURL, keyFile, version, goos, arch string) (Bundle, error) {
	b := Bundle{Directory: dir, SourceURL: baseURL}
	pub, err := os.ReadFile(keyFile)
	if err != nil {
		return b, err
	}
	b.PublicKey = pub
	if dir != "" && baseURL != "" {
		return b, VerifyError("choose --release-dir or --release-url")
	}
	if dir == "" && baseURL == "" {
		return b, VerifyError("a release source is required")
	}
	if baseURL != "" {
		u, err := url.Parse(baseURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return b, VerifyError("release URL must be an HTTPS directory without credentials/query/fragment")
		}
		b.Directory, err = os.MkdirTemp("", "tdev-release-")
		if err != nil {
			return b, err
		}
		b.cleanup = func() { _ = os.RemoveAll(b.Directory) }
		for _, file := range []string{"manifest.json", "manifest.sig"} {
			if err = download(ctx, strings.TrimRight(baseURL, "/")+"/"+file, filepath.Join(b.Directory, file), 4*1024*1024); err != nil {
				b.Close()
				return Bundle{}, err
			}
		}
	}
	failure := func(err error) (Bundle, error) { b.Close(); return Bundle{}, err }
	b.ManifestBytes, err = os.ReadFile(filepath.Join(b.Directory, "manifest.json"))
	if err != nil {
		return failure(err)
	}
	b.Signature, err = os.ReadFile(filepath.Join(b.Directory, "manifest.sig"))
	if err != nil {
		return failure(err)
	}
	b.Manifest, err = VerifyManifest(b.ManifestBytes, b.Signature, pub)
	if err != nil {
		return failure(err)
	}
	if version != "" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(b.Manifest.Version, "v") {
		return failure(VerifyError("requested version %s differs from signed version %s", version, b.Manifest.Version))
	}
	for _, a := range b.Manifest.Artifacts {
		if a.OS == goos && a.Arch == arch {
			b.Artifact = a
		}
	}
	if b.Artifact.File == "" {
		return failure(VerifyError("release has no artifact for %s/%s", goos, arch))
	}
	b.Path = filepath.Join(b.Directory, b.Artifact.File)
	if baseURL != "" {
		if err = download(ctx, strings.TrimRight(baseURL, "/")+"/"+b.Artifact.File, b.Path, MaxArtifact); err != nil {
			return failure(err)
		}
	}
	if err = b.Check(); err != nil {
		return failure(err)
	}
	return b, nil
}
func download(ctx context.Context, source, destination string, limit int64) error {
	client := &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.User != nil || len(via) > 5 {
			return VerifyError("unsafe release redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release download HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if n > limit {
		return VerifyError("release download exceeds size limit")
	}
	return nil
}

// BuildRelease reads an externally supplied PKCS8 signing key; it never generates/stores a release secret.
func BuildRelease(dir, version, keyFile string) error {
	if !versionRE.MatchString(version) {
		return VerifyError("invalid release version")
	}
	info, err := os.Stat(keyFile)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0077 != 0 {
		return VerifyError("signing key permissions must exclude group/other access")
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return VerifyError("invalid signing key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return err
	}
	private, ok := key.(ed25519.PrivateKey)
	if !ok {
		return VerifyError("signing key must be Ed25519")
	}
	m := Manifest{FormatVersion: 1, Version: version, ProtocolVersion: 1, ConfigSchemaVersion: 1}
	checks := strings.Builder{}
	for _, goos := range []string{"linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			file := "tdev-" + goos + "-" + arch
			path := filepath.Join(dir, file)
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			hash, err := transfer.Hash(path)
			if err != nil {
				return err
			}
			archive := file + ".tar.gz"
			if err = archiveBinary(path, filepath.Join(dir, archive)); err != nil {
				return err
			}
			archiveHash, err := transfer.Hash(filepath.Join(dir, archive))
			if err != nil {
				return err
			}
			m.Artifacts = append(m.Artifacts, Artifact{OS: goos, Arch: arch, File: file, SHA256: hash, Size: info.Size(), Archive: archive, ArchiveSHA256: archiveHash})
			fmt.Fprintf(&checks, "%s  %s\n%s  %s\n", hash, file, archiveHash, archive)
		}
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	manifest = append(manifest, '\n')
	sum := []byte(checks.String())
	publicDER, err := x509.MarshalPKIXPublicKey(private.Public())
	if err != nil {
		return err
	}
	for name, content := range map[string][]byte{"manifest.json": manifest, "manifest.sig": ed25519.Sign(private, manifest), "SHA256SUMS": sum, "SHA256SUMS.sig": ed25519.Sign(private, sum), "release.pub": pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})} {
		if err = config.AtomicWrite(filepath.Join(dir, name), content, 0644); err != nil {
			return err
		}
	}
	return nil
}
func archiveBinary(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = tw.WriteHeader(&tar.Header{Name: "tdev", Mode: 0755, Size: info.Size(), ModTime: time.Unix(0, 0)})
	if err == nil {
		_, err = io.Copy(tw, in)
	}
	for _, close := range []func() error{tw.Close, gz.Close, out.Close} {
		if e := close(); err == nil {
			err = e
		}
	}
	return err
}
func Fingerprint(pub []byte) (string, error) {
	key, err := PublicKey(pub)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(key)
	return hex.EncodeToString(hash[:]), nil
}
