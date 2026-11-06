// Package gitlabrelease publishes an existing signed bundle to GitLab.
package gitlabrelease

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tdev/internal/update"
)

type Options struct {
	APIURL, ProjectURL, ProjectID, Tag, Token string
	Directory, Notes, TrustedKey              string
}

type asset struct {
	name, path, hash string
}

type link struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Path string `json:"direct_asset_path"`
	Type string `json:"link_type"`
}

var tagRE = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var idRE = regexp.MustCompile(`^[0-9]+$`)

func validURL(source string) bool {
	u, err := url.Parse(source)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(source, "\r\n")
}

func hashReader(r io.Reader) (string, error) {
	h := sha256.New()
	_, err := io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil)), err
}

func bundle(o Options) ([]asset, error) {
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(o.Directory, name)) }
	pub, err := os.ReadFile(o.TrustedKey)
	if err != nil {
		return nil, fmt.Errorf("cannot read trusted public key")
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest")
	}
	sig, err := read("manifest.sig")
	if err != nil {
		return nil, fmt.Errorf("cannot read manifest signature")
	}
	m, err := update.VerifyManifest(manifest, sig, pub)
	if err != nil {
		return nil, fmt.Errorf("release manifest verification failed")
	}
	if m.Version != strings.TrimPrefix(o.Tag, "v") {
		return nil, fmt.Errorf("signed version differs from tag")
	}
	key, err := update.PublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("invalid trusted public key")
	}
	bundlePub, err := read("release.pub")
	if err != nil {
		return nil, fmt.Errorf("cannot read bundled public key")
	}
	bundleKey, err := update.PublicKey(bundlePub)
	if err != nil || !bytes.Equal(key, bundleKey) {
		return nil, fmt.Errorf("bundled public key differs from trusted key")
	}
	checks, err := read("SHA256SUMS")
	if err != nil {
		return nil, fmt.Errorf("cannot read checksums")
	}
	checkSig, err := read("SHA256SUMS.sig")
	if err != nil || !ed25519.Verify(key, checks, checkSig) {
		return nil, fmt.Errorf("checksum signature verification failed")
	}
	names := []string{"manifest.json", "manifest.sig", "SHA256SUMS", "SHA256SUMS.sig", "release.pub", "install.sh"}
	expected := map[string]string{}
	var expectedChecks strings.Builder
	for _, a := range m.Artifacts {
		names = append(names, a.File, a.Archive)
		expected[a.File], expected[a.Archive] = a.SHA256, a.ArchiveSHA256
		fmt.Fprintf(&expectedChecks, "%s  %s\n%s  %s\n", a.SHA256, a.File, a.ArchiveSHA256, a.Archive)
	}
	if string(checks) != expectedChecks.String() {
		return nil, fmt.Errorf("checksums differ from signed manifest")
	}
	var assets []asset
	for _, name := range names {
		path := filepath.Join(o.Directory, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > update.MaxArtifact {
			return nil, fmt.Errorf("invalid release file: %s", name)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cannot read release file: %s", name)
		}
		hash, err := hashReader(f)
		_ = f.Close()
		if err != nil || (expected[name] != "" && hash != expected[name]) {
			return nil, fmt.Errorf("release file verification failed: %s", name)
		}
		assets = append(assets, asset{name, path, hash})
	}
	return assets, nil
}

// Publish verifies all files before network writes. Existing matching package
// files are reused after an interrupted upload; differing files are immutable.
// Existing Releases are left intact. Tokens and API response bodies stay out of errors.
func Publish(ctx context.Context, o Options, client *http.Client) error {
	if !tagRE.MatchString(o.Tag) || !idRE.MatchString(o.ProjectID) || o.Token == "" || strings.ContainsAny(o.Token, "\r\n") || !validURL(o.APIURL) || !validURL(o.ProjectURL) {
		return fmt.Errorf("valid GitLab HTTPS URLs, numeric project ID, vMAJOR.MINOR.PATCH tag and job token are required")
	}
	assets, err := bundle(o)
	if err != nil {
		return err
	}
	notes, err := os.ReadFile(o.Notes)
	if err != nil || len(notes) == 0 {
		return fmt.Errorf("release notes are required")
	}
	if client == nil {
		client = &http.Client{}
	}
	c := *client
	c.Timeout = 2 * time.Minute
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request := func(method, endpoint string, body io.Reader, size int64) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
		if err != nil {
			return nil, fmt.Errorf("cannot construct GitLab request")
		}
		req.Header.Set("JOB-TOKEN", o.Token)
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		if body != nil {
			req.ContentLength = size
		}
		requestClient := c
		if method == http.MethodGet {
			// Object storage may redirect package downloads. A redirect receives
			// no job token, including when its host matches the GitLab host.
			requestClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
				if len(via) > 5 || next.URL.Scheme != "https" || next.URL.User != nil {
					return fmt.Errorf("unsafe download redirect")
				}
				next.Header.Del("JOB-TOKEN")
				return nil
			}
		}
		resp, err := requestClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GitLab %s request failed", method)
		}
		return resp, nil
	}
	project := strings.TrimRight(o.APIURL, "/") + "/projects/" + o.ProjectID
	releases := project + "/releases"
	resp, err := request(http.MethodGet, releases+"/"+o.Tag, nil, 0)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return fmt.Errorf("release already exists; existing release left intact")
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("release preflight returned HTTP %d", resp.StatusCode)
	}
	packageURL := project + "/packages/generic/tdev/" + strings.TrimPrefix(o.Tag, "v")
	var missing []asset
	var links []link
	for _, a := range assets {
		endpoint := packageURL + "/" + a.name
		links = append(links, link{a.name, endpoint, "/" + a.name, "package"})
		resp, err := request(http.MethodGet, endpoint, nil, 0)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusNotFound {
			missing = append(missing, a)
			_ = resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return fmt.Errorf("package preflight for %s returned HTTP %d", a.name, resp.StatusCode)
		}
		hash, readErr := hashReader(io.LimitReader(resp.Body, update.MaxArtifact+1))
		_ = resp.Body.Close()
		if readErr != nil || hash != a.hash {
			return fmt.Errorf("existing package file differs: %s; left intact", a.name)
		}
	}
	for _, a := range missing {
		f, err := os.Open(a.path)
		if err != nil {
			return fmt.Errorf("cannot open upload: %s", a.name)
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("cannot stat upload: %s", a.name)
		}
		resp, err := request(http.MethodPut, packageURL+"/"+a.name, f, info.Size())
		_ = f.Close()
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("upload of %s returned HTTP %d", a.name, resp.StatusCode)
		}
	}
	payload := struct {
		Name        string `json:"name"`
		Tag         string `json:"tag_name"`
		Description string `json:"description"`
		Assets      struct {
			Links []link `json:"links"`
		} `json:"assets"`
	}{Name: o.Tag, Tag: o.Tag, Description: string(notes)}
	payload.Assets.Links = links
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err = request(http.MethodPost, releases, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("release creation returned HTTP %d; uploaded files retained for retry", resp.StatusCode)
	}
	return nil
}
