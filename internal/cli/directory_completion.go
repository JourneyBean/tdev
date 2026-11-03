package cli

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/tmux"
)

func (a *app) completeRemoteDirectories(ctx context.Context, t target, input string) ([]string, error) {
	if !config.SafeText(input) {
		return nil, config.Invalid("invalid directory prefix")
	}
	if input == "~" {
		return []string{"~/"}, nil
	}
	if input != "" && !strings.HasPrefix(input, "/") && !strings.HasPrefix(input, "~/") {
		return nil, config.Invalid("use an absolute remote path or ~/path")
	}
	prefix, leaf := "~/", input
	if slash := strings.LastIndex(input, "/"); slash >= 0 {
		prefix, leaf = input[:slash+1], input[slash+1:]
	}
	base := tmux.Quote(prefix)
	if strings.HasPrefix(prefix, "~/") {
		base = `"$HOME"/` + tmux.Quote(strings.TrimPrefix(prefix, "~/"))
	}
	// Quote all user input; the only glob is our fixed directory enumeration.
	// NUL delimiters preserve whitespace, quotes and shell metacharacters.
	script := "base=" + base + "; leaf=" + tmux.Quote(leaf) + `; count=0
for entry in "$base"/* "$base"/.[!.]* "$base"/..?*; do
  test -d "$entry" || continue
  name=${entry##*/}
  case "$name" in "$leaf"*) ;; *) continue ;; esac
  case "$name" in .*) case "$leaf" in .*) ;; *) continue ;; esac ;; esac
  printf '%s\000' "$name"
  count=$((count+1))
  test "$count" -le 200 || break
done`
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	t.Batch = true
	data, err := a.shell(ctx, t, script)
	if err != nil {
		return nil, err
	}
	names := bytes.Split(data, []byte{0})
	if len(names) > 201 {
		return nil, config.Invalid("too many directories; type a longer prefix")
	}
	var matches []string
	for _, name := range names {
		value := string(name)
		if value == "" || !config.SafeText(value) || strings.Contains(value, "/") {
			continue
		}
		matches = append(matches, prefix+value+"/")
	}
	sort.Strings(matches)
	return matches, nil
}
