// Package shellpath manages a small, idempotent PATH block in user shell files.
package shellpath

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tdev/internal/config"
)

const start = "# >>> tdev PATH >>>"
const end = "# <<< tdev PATH <<<"

type Change struct {
	File    string `json:"file"`
	Changed bool   `json:"changed"`
	Backup  string `json:"backup,omitempty"`
}

func Quote(value, shell string) string {
	if shell == "fish" {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), "'", `\'`) + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func Snippet(directory, shell string) string {
	quoted := Quote(directory, shell)
	if shell == "fish" {
		return "contains -- " + quoted + " $PATH; or set -gx PATH " + quoted + " $PATH"
	}
	return "case \":$PATH:\" in\n  *:\"" + directoryPattern(directory) + ":\"*) ;;\n  *) export PATH=" + quoted + ":\"$PATH\" ;;\nesac"
}

func directoryPattern(directory string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(directory)
}

func Targets(home, shell, zdotdir, xdg string) ([]string, error) {
	switch shell {
	case "zsh":
		if zdotdir == "" {
			zdotdir = home
		}
		if !filepath.IsAbs(zdotdir) {
			return nil, fmt.Errorf("ZDOTDIR must be absolute")
		}
		return []string{filepath.Join(zdotdir, ".zshrc")}, nil
	case "bash":
		login := filepath.Join(home, ".profile")
		for _, name := range []string{".bash_profile", ".bash_login"} {
			path := filepath.Join(home, name)
			if _, err := os.Stat(path); err == nil {
				login = path
				break
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		return []string{filepath.Join(home, ".bashrc"), login}, nil
	case "fish":
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		if !filepath.IsAbs(xdg) {
			return nil, fmt.Errorf("XDG_CONFIG_HOME must be absolute")
		}
		return []string{filepath.Join(xdg, "fish", "conf.d", "tdev.fish")}, nil
	case "sh", "dash", "ksh":
		return []string{filepath.Join(home, ".profile")}, nil
	default:
		return nil, fmt.Errorf("unsupported shell %q; choose bash, zsh, fish or sh", shell)
	}
}

func Merge(original []byte, block string) ([]byte, error) {
	return mergeBlock(original, block, "PATH")
}

func mergeBlock(original []byte, block, section string) ([]byte, error) {
	start, end := "# >>> tdev "+section+" >>>", "# <<< tdev "+section+" <<<"
	text := string(original)
	a, b := strings.Index(text, start), strings.Index(text, end)
	if (a < 0) != (b < 0) || (a >= 0 && (b < a || strings.Count(text, start) != 1 || strings.Count(text, end) != 1)) {
		return nil, fmt.Errorf("%s block markers are incomplete or duplicated; repair the tdev block first", section)
	}
	managed := start + "\n" + block + "\n" + end
	if a >= 0 {
		return []byte(text[:a] + managed + text[b+len(end):]), nil
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return []byte(text + "\n" + managed + "\n"), nil
}

func Write(path, block string) (Change, error) {
	return WriteBlock(path, block, "PATH")
}

// WriteBlock preserves independent managed sections in the same startup file.
func WriteBlock(path, block, section string) (Change, error) {
	change := Change{File: path}
	if section != "PATH" && section != "completion" {
		return change, fmt.Errorf("unsupported shell configuration section")
	}
	// Preserve a user's dotfile symlink rather than replacing the link itself.
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return change, err
		}
	} else if err != nil && !os.IsNotExist(err) {
		return change, err
	}
	original, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !os.IsNotExist(err) {
		return change, err
	}
	mode := os.FileMode(0600)
	if exists {
		info, err := os.Stat(path)
		if err != nil {
			return change, err
		}
		if !info.Mode().IsRegular() {
			return change, fmt.Errorf("shell startup file is not regular")
		}
		mode = info.Mode().Perm()
	}
	updated, err := mergeBlock(original, block, section)
	if err != nil || bytes.Equal(original, updated) {
		return change, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return change, err
	}
	if exists {
		backup, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tdev-backup-*")
		if err != nil {
			return change, err
		}
		change.Backup = backup.Name()
		_, writeErr := backup.Write(original)
		closeErr := backup.Close()
		if writeErr != nil {
			return change, writeErr
		}
		if closeErr != nil {
			return change, closeErr
		}
	}
	current, readErr := os.ReadFile(path)
	if (!exists && !os.IsNotExist(readErr)) || (exists && (readErr != nil || !bytes.Equal(current, original))) {
		return change, fmt.Errorf("shell startup file changed concurrently; retry")
	}
	if err = config.AtomicWrite(path, updated, mode); err != nil {
		return change, err
	}
	change.Changed = true
	return change, nil
}
