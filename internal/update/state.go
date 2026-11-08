package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tdev/internal/config"
	"tdev/internal/transfer"
)

// migrateInstallation runs under the installation lock. Copy the entire recorded
// rollback chain before publishing its new root; only then remove old files.
// AtomicWrite also supports migration across filesystems.
func migrateInstallation(store config.Store, destination string) error {
	r, err := ReadReceipt(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	backupDir, err := backupDirectory(store, destination)
	if err != nil {
		return err
	}
	oldConfigDir, err := transfer.Canonical(filepath.Join(store.Root, "binary-backups", lockKey(destination)))
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	visited := map[string]bool{}
	var migrate func(Receipt, int) (Receipt, error)
	migrate = func(current Receipt, depth int) (Receipt, error) {
		if current.PreviousPath == "" {
			return current, nil
		}
		if depth > 1024 || visited[current.PreviousPath] {
			return current, VerifyError("cyclic or excessive rollback history")
		}
		visited[current.PreviousPath] = true
		old := current.PreviousPath
		managed := filepath.Dir(old) == backupDir && strings.HasPrefix(filepath.Base(old), "tdev.backup-")
		adjacent := filepath.Dir(old) == filepath.Dir(destination) && strings.HasPrefix(old, destination+".backup-")
		configBackup := filepath.Dir(old) == oldConfigDir && strings.HasPrefix(filepath.Base(old), "tdev.backup-")
		if !managed && !adjacent && !configBackup {
			return current, VerifyError("invalid backup path")
		}
		data, err := os.ReadFile(old)
		if err != nil {
			return current, err
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(data))
		if hash != current.PreviousSHA256 {
			return current, VerifyError("backup digest mismatch")
		}
		next := old
		preserveLegacy := false
		if !managed {
			next = filepath.Join(backupDir, fmt.Sprintf("tdev.backup-legacy-%x", sha256.Sum256([]byte(old))))
		}
		if current.PreviousReceipt != "" {
			if current.PreviousReceipt != old+".json" {
				return current, VerifyError("invalid backup receipt path")
			}
			data, err := os.ReadFile(current.PreviousReceipt)
			if err != nil {
				return current, err
			}
			var prior Receipt
			if json.Unmarshal(data, &prior) != nil || prior.Destination != destination {
				return current, VerifyError("previous receipt does not match backup")
			}
			if prior.SHA256 != hash {
				if managed {
					return current, VerifyError("previous receipt does not match backup")
				}
				// Older installers copied stale receipts after external binary
				// replacements. The parent digest still verifies this backup,
				// but its receipt cannot describe it or authorize older history.
				// Leave the original pair untouched for manual inspection.
				preserveLegacy = true
				current.PreviousReceipt = ""
			} else {
				prior, err = migrate(prior, depth+1)
				if err != nil {
					return current, err
				}
				encoded, _ := json.MarshalIndent(prior, "", "  ")
				if err = writeStateFile(next+".json", encoded, 0600); err != nil {
					return current, err
				}
				if !managed {
					removed[current.PreviousReceipt] = true
				}
				current.PreviousReceipt = next + ".json"
			}
		}
		if !managed {
			if err = writeStateFile(next, data, 0700); err != nil {
				return current, err
			}
			if !preserveLegacy {
				removed[old] = true
			}
		}
		current.PreviousPath = next
		return current, nil
	}
	// An externally replaced binary must still be rejected by rollback. Installation
	// may adopt identical authenticated bytes and clear the stale rollback reference.
	hash, hashErr := transfer.Hash(destination)
	if hashErr == nil && hash == r.SHA256 {
		r, err = migrate(r, 0)
		if err != nil {
			return err
		}
	}
	encoded, _ := json.MarshalIndent(r, "", "  ")
	if err = writeStateFile(ReceiptPath(destination), encoded, 0600); err != nil {
		return err
	}
	removed[legacyReceiptPath(destination)] = true
	for path := range removed {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeStateFile(path string, data []byte, mode os.FileMode) error {
	before, err := os.ReadFile(path)
	if err == nil && bytes.Equal(before, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return config.AtomicWrite(path, data, mode)
}
