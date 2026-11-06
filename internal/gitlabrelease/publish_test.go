package gitlabrelease

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tdev/internal/update"
)

func fixture(t *testing.T) Options {
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
	keyPath := filepath.Join(t.TempDir(), "signing.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tdev-linux-amd64", "tdev-linux-arm64", "tdev-darwin-amd64", "tdev-darwin-arm64"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := update.BuildRelease(dir, "0.2.0", keyPath); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("../../scripts/install.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := update.BuildInstaller(dir, string(template)); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# v0.2.0\nRelease notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return Options{ProjectID: "42", Tag: "v0.2.0", Token: "test-job-token-sentinel", Directory: dir, Notes: notes, TrustedKey: filepath.Join(dir, "release.pub")}
}

func TestPublishAndResume(t *testing.T) {
	o := fixture(t)
	files := map[string][]byte{}
	created, writes, failUpload := false, 0, true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("JOB-TOKEN") != o.Token {
			t.Error("missing job-token authentication")
			w.WriteHeader(401)
			return
		}
		if strings.Contains(r.URL.Path, "/packages/generic/tdev/0.2.0/") {
			name := filepath.Base(r.URL.Path)
			if r.Method == http.MethodGet {
				data, ok := files[name]
				if !ok {
					w.WriteHeader(404)
					return
				}
				_, _ = w.Write(data)
				return
			}
			if r.Method != http.MethodPut {
				t.Error("unexpected method", r.Method)
				w.WriteHeader(405)
				return
			}
			if failUpload && len(files) == 2 {
				w.WriteHeader(500)
				return
			}
			data, _ := io.ReadAll(r.Body)
			if _, ok := files[name]; ok {
				t.Error("overwrote existing package file")
			}
			files[name] = data
			writes++
			w.WriteHeader(201)
			return
		}
		if r.Method == http.MethodGet {
			if created {
				w.WriteHeader(200)
			} else {
				w.WriteHeader(404)
			}
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/api/v4/projects/42/releases" {
			t.Error("unexpected release request", r.Method, r.URL.Path)
		}
		var payload struct {
			Tag         string `json:"tag_name"`
			Description string `json:"description"`
			Assets      struct {
				Links []link `json:"links"`
			} `json:"assets"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Tag != o.Tag || !strings.Contains(payload.Description, "Release notes") || len(payload.Assets.Links) != 14 || len(files) != 14 {
			t.Error("incomplete release", payload, len(files))
		}
		for _, link := range payload.Assets.Links {
			if link.Path != "/"+link.Name || !strings.HasSuffix(link.URL, "/"+link.Name) {
				t.Error("invalid permanent asset path", link)
			}
		}
		created = true
		w.WriteHeader(201)
	}))
	defer server.Close()
	o.APIURL, o.ProjectURL = server.URL+"/api/v4", server.URL+"/group/demo"
	if err := Publish(context.Background(), o, server.Client()); err == nil || created || len(files) != 2 {
		t.Fatal("partial upload must not create a release", err)
	}
	failUpload = false
	if err := Publish(context.Background(), o, server.Client()); err != nil {
		t.Fatal(err)
	}
	if !created || writes != 14 {
		t.Fatal("resume must reuse matching uploaded files", created, writes)
	}
	if err := Publish(context.Background(), o, server.Client()); err == nil || writes != 14 {
		t.Fatal("existing release must remain intact", err)
	}
}

func TestPublishRejectsChangedBundleBeforeNetwork(t *testing.T) {
	for _, name := range []string{"tdev-linux-amd64", "tdev-darwin-arm64.tar.gz", "SHA256SUMS", "manifest.sig"} {
		t.Run(name, func(t *testing.T) {
			o := fixture(t)
			if err := os.WriteFile(filepath.Join(o.Directory, name), []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
			defer s.Close()
			o.APIURL, o.ProjectURL = s.URL, s.URL
			if err := Publish(context.Background(), o, s.Client()); err == nil || requests != 0 {
				t.Fatal("tampered bundle reached API", err, requests)
			}
		})
	}
}

func TestPublishRejectsConflictsAndDropsRedirectToken(t *testing.T) {
	for _, mode := range []string{"conflict", "redirect", "denied"} {
		t.Run(mode, func(t *testing.T) {
			o := fixture(t)
			writes, redirected := 0, false
			var server *httptest.Server
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				if mode == "denied" {
					w.WriteHeader(403)
					_, _ = io.WriteString(w, o.Token)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/releases/v0.2.0") {
					w.WriteHeader(404)
					return
				}
				if strings.HasPrefix(r.URL.Path, "/object/") {
					redirected = true
					if r.Header.Get("JOB-TOKEN") != "" {
						t.Error("job token leaked on redirect")
					}
					data, _ := os.ReadFile(filepath.Join(o.Directory, filepath.Base(r.URL.Path)))
					_, _ = w.Write(data)
					return
				}
				if mode == "redirect" {
					http.Redirect(w, r, server.URL+"/object/"+filepath.Base(r.URL.Path), 302)
					return
				}
				_, _ = io.WriteString(w, "different bytes")
			}))
			defer server.Close()
			o.APIURL, o.ProjectURL = server.URL, server.URL
			err := Publish(context.Background(), o, server.Client())
			if err == nil || strings.Contains(err.Error(), o.Token) {
				t.Fatal("expected sanitized API failure", err)
			}
			if mode == "redirect" {
				if !redirected || writes != 1 {
					t.Fatal("matching redirected downloads should reach release creation", redirected, writes)
				}
			} else if writes != 0 {
				t.Fatal("preflight failure performed writes", writes)
			}
		})
	}
}
