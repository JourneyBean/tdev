package update

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// BuildInstaller pins authenticated binary hashes and the public key in a release-specific bootstrap.
func BuildInstaller(dir, template string) error {
	return BuildInstallerWithSource(dir, template, "", "")
}

// BuildInstallerWithSource embeds a version-specific download directory and a
// stable upgrade directory. Empty sources retain the GitHub defaults.
func BuildInstallerWithSource(dir, template, releaseURL, upgradeURL string) error {
	if (releaseURL == "") != (upgradeURL == "") {
		return fmt.Errorf("release and upgrade URLs must be supplied together")
	}
	for _, source := range []string{releaseURL, upgradeURL} {
		if source == "" {
			continue
		}
		u, err := url.Parse(source)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(source, "\r\n") {
			return fmt.Errorf("installer source must be an HTTPS directory without credentials/query/fragment")
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	if !versionRE.MatchString(manifest.Version) {
		return fmt.Errorf("invalid installer version")
	}
	pub, err := os.ReadFile(filepath.Join(dir, "release.pub"))
	if err != nil {
		return err
	}
	checks := strings.Builder{}
	for _, asset := range manifest.Artifacts {
		fmt.Fprintf(&checks, "%s  %s\n", asset.SHA256, asset.File)
	}
	for _, marker := range []string{"@TDEV_VERSION@", "@TDEV_PUBLIC_KEY@", "@TDEV_CHECKSUMS@", "@TDEV_RELEASE_URL@", "@TDEV_UPGRADE_URL@"} {
		if strings.Count(template, marker) != 1 {
			return fmt.Errorf("installer template must contain exactly one %s", marker)
		}
	}
	quoteContent := func(s string) string { return strings.ReplaceAll(strings.TrimRight(s, "/"), "'", "'\"'\"'") }
	installer := strings.NewReplacer("@TDEV_VERSION@", strings.TrimPrefix(manifest.Version, "v"), "@TDEV_PUBLIC_KEY@", strings.TrimSpace(string(pub)), "@TDEV_CHECKSUMS@", strings.TrimSpace(checks.String()), "@TDEV_RELEASE_URL@", quoteContent(releaseURL), "@TDEV_UPGRADE_URL@", quoteContent(upgradeURL)).Replace(template)
	return os.WriteFile(filepath.Join(dir, "install.sh"), []byte(installer), 0755)
}
