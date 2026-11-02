package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildInstaller pins authenticated binary hashes and the public key in a release-specific bootstrap.
func BuildInstaller(dir, template string) error {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest Manifest
	if err = json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	pub, err := os.ReadFile(filepath.Join(dir, "release.pub"))
	if err != nil {
		return err
	}
	checks := strings.Builder{}
	for _, asset := range manifest.Artifacts {
		fmt.Fprintf(&checks, "%s  %s\n", asset.SHA256, asset.File)
	}
	for _, marker := range []string{"@TDEV_VERSION@", "@TDEV_PUBLIC_KEY@", "@TDEV_CHECKSUMS@"} {
		if strings.Count(template, marker) != 1 {
			return fmt.Errorf("installer template must contain exactly one %s", marker)
		}
	}
	installer := strings.NewReplacer("@TDEV_VERSION@", strings.TrimPrefix(manifest.Version, "v"), "@TDEV_PUBLIC_KEY@", strings.TrimSpace(string(pub)), "@TDEV_CHECKSUMS@", strings.TrimSpace(checks.String())).Replace(template)
	return os.WriteFile(filepath.Join(dir, "install.sh"), []byte(installer), 0755)
}
