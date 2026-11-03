package sshx

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tdev/internal/config"
)

// HostEntry contains display metadata only; discovery never evaluates Match exec or reads keys.
type HostEntry struct{ Alias, HostName, User, Port string }
type configLine struct {
	key    string
	values []string
}

// ConfigWords parses quotes, escaped characters, comments and key=value SSH configuration syntax.
func ConfigWords(line string) ([]string, error) {
	var words []string
	var current strings.Builder
	quote := rune(0)
	escaped, started := false, false
	flush := func() {
		if started {
			words = append(words, current.String())
			current.Reset()
			started = false
		}
	}
	for _, ch := range line {
		if escaped {
			current.WriteRune(ch)
			started = true
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if ch == quote {
				quote = 0
			} else {
				current.WriteRune(ch)
			}
			started = true
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			started = true
			continue
		}
		if ch == '#' {
			break
		}
		if ch == ' ' || ch == '\t' || ch == '\r' || (ch == '=' && (len(words) == 0 || (len(words) == 1 && !started))) {
			flush()
			continue
		}
		current.WriteRune(ch)
		started = true
	}
	if quote != 0 || escaped {
		return nil, fmt.Errorf("unterminated SSH configuration quote")
	}
	flush()
	return words, nil
}

// DiscoverHosts reads literal Host names and Include files; wildcard/Match execution stays with OpenSSH.
func DiscoverHosts(path, home string) ([]HostEntry, error) {
	if path == "" {
		path = filepath.Join(home, ".ssh", "config")
	}
	var lines []configLine
	active := map[string]bool{}
	files := 0
	var read func(string, int) error
	read = func(path string, depth int) error {
		if depth > 16 || files >= 256 {
			return fmt.Errorf("SSH Include nesting/file limit exceeded")
		}
		resolved, err := filepath.EvalSymlinks(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if active[resolved] {
			return fmt.Errorf("recursive SSH Include")
		}
		active[resolved] = true
		defer delete(active, resolved)
		files++
		f, err := os.Open(resolved)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return fmt.Errorf("SSH configuration must be a regular file under 1 MiB")
		}
		scanner := bufio.NewScanner(io.LimitReader(f, 1024*1024+1))
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			words, err := ConfigWords(scanner.Text())
			if err != nil {
				return err
			}
			if len(words) < 2 {
				continue
			}
			key := strings.ToLower(words[0])
			if key == "include" {
				for _, pattern := range words[1:] {
					pattern = strings.ReplaceAll(pattern, "%d", home)
					if strings.HasPrefix(pattern, "~/") {
						pattern = filepath.Join(home, pattern[2:])
					}
					if !filepath.IsAbs(pattern) {
						pattern = filepath.Join(home, ".ssh", pattern)
					}
					paths, err := filepath.Glob(pattern)
					if err != nil {
						return err
					}
					for _, included := range paths {
						if err = read(included, depth+1); err != nil {
							return err
						}
					}
				}
			} else {
				lines = append(lines, configLine{key, words[1:]})
			}
		}
		return scanner.Err()
	}
	if err := read(path, 0); err != nil {
		return nil, err
	}
	var aliases []string
	seen := map[string]bool{}
	for _, line := range lines {
		if line.key == "host" {
			for _, alias := range line.values {
				if !strings.ContainsAny(alias, "*?![]") && CheckAlias(alias) == nil && !seen[alias] {
					aliases = append(aliases, alias)
					seen[alias] = true
				}
			}
		}
	}
	result := make([]HostEntry, 0, len(aliases))
	for _, alias := range aliases {
		entry := HostEntry{Alias: alias}
		enabled := true
		for _, line := range lines {
			switch line.key {
			case "host":
				enabled = hostMatches(alias, line.values)
			case "match":
				enabled = false
			default:
				if !enabled {
					continue
				}
				value := line.values[0]
				if !config.SafeText(value) {
					continue
				}
				switch line.key {
				case "hostname":
					if entry.HostName == "" {
						entry.HostName = strings.ReplaceAll(value, "%h", alias)
					}
				case "user":
					if entry.User == "" {
						entry.User = value
					}
				case "port":
					if entry.Port == "" {
						entry.Port = value
					}
				}
			}
		}
		if entry.HostName == "" {
			entry.HostName = alias
		}
		if entry.Port == "" {
			entry.Port = "22"
		}
		result = append(result, entry)
	}
	return result, nil
}

func hostMatches(alias string, patterns []string) bool {
	matched := false
	for _, pattern := range patterns {
		negative := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		ok, _ := filepath.Match(strings.ToLower(pattern), strings.ToLower(alias))
		if ok && negative {
			return false
		}
		matched = matched || ok
	}
	return matched
}
