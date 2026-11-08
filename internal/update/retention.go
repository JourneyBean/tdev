package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"tdev/internal/config"
)

const retainedBackupVersions = 2

var backupFilename = regexp.MustCompile(`^tdev\.backup-([0-9]{8}T[0-9]{6}\.[0-9]{9}-[0-9a-f]{8}|legacy-[0-9a-f]{64})(\.json)?$`)

// pruneBackups runs under the installation lock after a successful operation.
// Publish the shortened rollback chain before removing unreferenced backups.
func pruneBackups(store config.Store, destination string) error {
	root, err := ReadReceipt(destination)
	if os.IsNotExist(err) {
		root = Receipt{}
	} else if err != nil {
		return err
	}
	dir, err := backupDirectory(store, destination)
	if err != nil {
		return err
	}
	keep := make(map[string]bool)
	current := root
	for depth := 0; depth < retainedBackupVersions && current.PreviousPath != ""; depth++ {
		path := current.PreviousPath
		if filepath.Dir(path) != dir || !backupFilename.MatchString(filepath.Base(path)) {
			return VerifyError("invalid retained backup path")
		}
		keep[path] = true
		if current.PreviousReceipt == "" {
			break
		}
		if current.PreviousReceipt != path+".json" {
			return VerifyError("invalid retained backup receipt path")
		}
		data, err := os.ReadFile(current.PreviousReceipt)
		if err != nil {
			return err
		}
		var prior Receipt
		if json.Unmarshal(data, &prior) != nil || prior.Destination != destination || prior.SHA256 != current.PreviousSHA256 {
			return VerifyError("retained receipt does not match backup")
		}
		keep[current.PreviousReceipt] = true
		if depth == retainedBackupVersions-1 {
			prior.PreviousPath = ""
			prior.PreviousSHA256 = ""
			prior.PreviousReceipt = ""
			encoded, _ := json.MarshalIndent(prior, "", "  ")
			if err := writeStateFile(current.PreviousReceipt, encoded, 0600); err != nil {
				return err
			}
		}
		current = prior
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		// Leave unrelated files, directories and symbolic links untouched.
		if keep[path] || !backupFilename.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
