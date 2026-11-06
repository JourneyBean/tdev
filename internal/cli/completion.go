package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
)

const completionTimeout = 1500 * time.Millisecond

func (a *app) completionCommands() {
	completion := group(a.root, "completion", "Generate shell completions for bash or zsh")
	a.completionInstallCommand(completion)
	for _, shell := range []string{"bash", "zsh"} {
		shell := shell
		c := &cobra.Command{Use: shell, Short: "Generate " + shell + " completion script", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if shell == "bash" {
				var script bytes.Buffer
				if err := a.root.GenBashCompletionV2(&script, true); err != nil {
					return err
				}
				// Keep helpers private and work in shells without bash-completion.
				text := strings.ReplaceAll(script.String(), "_filedir", "__tdev_filedir") + bashCompletionFallback
				_, err := a.out.Write([]byte(text))
				return err
			}
			return a.root.GenZshCompletion(a.out)
		}}
		completion.AddCommand(c)
	}
	var register func(*cobra.Command, bool)
	register = func(c *cobra.Command, local bool) {
		local = local || c.Name() == "api"
		if (c.Name() == "open" && !local) || strings.Contains(c.Use, "<project>") || strings.Contains(c.Use, "[project]") || strings.Contains(c.Use, "<server/project>") || strings.HasPrefix(c.Use, "create [server/") {
			c.ValidArgsFunction = func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				if len(args) == 0 {
					if c.Name() == "create" && !local {
						return a.completeServers(prefix, true)
					}
					return a.completeProjects(c, prefix, local)
				}
				if (c.Name() == "push" && len(args) == 1) || (c.Name() == "pull" && len(args) == 2) {
					return nil, cobra.ShellCompDirectiveDefault
				}
				if c.Name() == "mount" || c.Name() == "unmount" {
					return nil, cobra.ShellCompDirectiveFilterDirs
				}
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
		}
		if c.Name() == "scratch" && !local {
			c.ValidArgsFunction = func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				return a.completeServers(prefix, false)
			}
		}
		for _, flag := range []string{"server", "remote"} {
			if c.Flags().Lookup(flag) != nil && c.Name() != "forward" {
				_ = c.RegisterFlagCompletionFunc(flag, func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
					return a.completeServers(prefix, false)
				})
			}
		}
		if c.Flags().Lookup("ssh-alias") != nil {
			_ = c.RegisterFlagCompletionFunc("ssh-alias", func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				hosts, _ := sshx.DiscoverHosts(a.sshConfig, home)
				var names []string
				for _, host := range hosts {
					if strings.HasPrefix(host.Alias, prefix) {
						names = append(names, host.Alias)
					}
				}
				return names, cobra.ShellCompDirectiveNoFileComp
			})
		}
		if c.Flags().Lookup("shell") != nil {
			_ = c.RegisterFlagCompletionFunc("shell", func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				var shells []string
				choices := []string{"auto", "bash", "zsh", "fish", "sh"}
				if c.Parent() != nil && c.Parent().Name() == "completion" {
					choices = []string{"auto", "bash", "zsh", "all"}
				}
				for _, shell := range choices {
					if strings.HasPrefix(shell, prefix) {
						shells = append(shells, shell)
					}
				}
				return shells, cobra.ShellCompDirectiveNoFileComp
			})
		}
		if c.Flags().Lookup("completion") != nil {
			_ = c.RegisterFlagCompletionFunc("completion", func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
				var choices []string
				for _, value := range []string{"auto", "none", "bash", "zsh", "all"} {
					if strings.HasPrefix(value, prefix) {
						choices = append(choices, value)
					}
				}
				return choices, cobra.ShellCompDirectiveNoFileComp
			})
		}
		if c.Flags().Lookup("forward") != nil {
			_ = c.RegisterFlagCompletionFunc("forward", a.completeForwardIDs())
		}
		if c.Flags().Lookup("id") != nil && c.Parent().Name() == "forward" && c.Name() != "add" {
			_ = c.RegisterFlagCompletionFunc("id", a.completeForwardIDs())
		}
		for _, child := range c.Commands() {
			register(child, local)
		}
	}
	register(a.root, false)
}

const bashCompletionFallback = `
# Prefer bash-completion when installed; provide a private Bash 3+ fallback.
__tdev_init_completion() {
    COMPREPLY=()
    if declare -F _get_comp_words_by_ref >/dev/null 2>&1; then
        _get_comp_words_by_ref "$@" cur prev words cword
        return
    fi
    words=()
    local i token last=-1 prior=''
    for ((i=0; i<=COMP_CWORD; i++)); do
        token=${COMP_WORDS[i]}
        if ((last>=0)) && { [[ $token == '=' || $token == ':' ]] || [[ $prior == '=' || $prior == ':' ]]; }; then
            words[last]="${words[last]}${token}"
        else
            words[${#words[@]}]=$token
            last=$((last+1))
        fi
        prior=$token
    done
    cword=$last
    cur=${words[cword]}
    prev=''
    if ((cword>0)); then prev=${words[cword-1]}; fi
}
__tdev_filedir() {
    if declare -F _filedir >/dev/null 2>&1; then _filedir "$@"; return; fi
    local kind=-f item
    if [[ ${1-} == '-d' ]]; then kind=-d; fi
    while IFS= read -r item; do
        COMPREPLY[${#COMPREPLY[@]}]=$item
    done < <(compgen "$kind" -- "$cur")
}
`

func (a *app) completeServers(prefix string, namespace bool) ([]string, cobra.ShellCompDirective) {
	prefs, err := a.prefs()
	directive := cobra.ShellCompDirectiveNoFileComp
	if namespace {
		directive |= cobra.ShellCompDirectiveNoSpace
	}
	if err != nil {
		return nil, directive
	}
	var names []string
	for _, name := range append([]string{"local"}, orderedServers(prefs)...) {
		if namespace {
			name += "/"
		}
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, directive
}

func (a *app) completeProjects(c *cobra.Command, prefix string, api bool) ([]string, cobra.ShellCompDirective) {
	directive := cobra.ShellCompDirectiveNoFileComp
	if api {
		projects, _ := a.store.List()
		var names []string
		for _, p := range projects {
			if strings.HasPrefix(p.Name, prefix) {
				names = append(names, p.Name)
			}
		}
		return names, directive
	}
	prefs, err := a.prefs()
	if err != nil {
		return nil, directive
	}
	servers := append([]string{"local"}, orderedServers(prefs)...)
	namespace := ""
	if strings.Contains(prefix, "/") {
		parts := strings.SplitN(prefix, "/", 2)
		servers = []string{parts[0]}
		namespace = parts[0] + "/"
	}
	ctx, cancel := context.WithTimeout(c.Context(), completionTimeout)
	defer cancel()
	type result struct {
		name      string
		projects  []config.Project
		scratches []tmux.Scratch
	}
	results := make(chan result, len(servers))
	for _, name := range servers {
		t, err := a.serverTarget(prefs, name, "")
		if err != nil {
			continue
		}
		go func(t target) {
			t.Batch = true
			data, err := a.call(ctx, t, nil, "project", "list")
			var projects []config.Project
			if err == nil {
				_ = json.Unmarshal(data, &projects)
			}
			var scratches []tmux.Scratch
			if c.Name() == "open" {
				if b, e := a.call(ctx, t, nil, "scratch", "list"); e == nil {
					_ = json.Unmarshal(b, &scratches)
				}
			}
			results <- result{name: t.Name, projects: projects, scratches: scratches}
		}(t)
	}
	seen := map[string]bool{}
	for range servers {
		select {
		case res := <-results:
			for _, p := range res.projects {
				if p.Validate() != nil {
					continue
				}
				values := []string{res.name + "/" + p.Name}
				if namespace == "" {
					values = append(values, p.Name)
				}
				for _, value := range values {
					if strings.HasPrefix(value, prefix) {
						seen[value] = true
					}
				}
			}
			for _, scratch := range res.scratches {
				if !config.ValidName(scratch.Name) {
					continue
				}
				values := []string{res.name + "/" + scratch.Name}
				if namespace == "" {
					values = append(values, scratch.Name)
				}
				for _, value := range values {
					if strings.HasPrefix(value, prefix) {
						seen[value] = true
					}
				}
			}
		case <-ctx.Done():
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, directive
}

func (a *app) completeForwardIDs() func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(c *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		ctx, cancel := context.WithTimeout(c.Context(), completionTimeout)
		defer cancel()
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		t.Batch = true
		data, err := a.call(ctx, t, nil, "project", "get", t.Project)
		var project config.Project
		if err == nil {
			err = json.Unmarshal(data, &project)
		}
		if err != nil || project.Validate() != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var ids []string
		for _, forward := range project.Forwards {
			if strings.HasPrefix(forward.ID, prefix) {
				ids = append(ids, forward.ID)
			}
		}
		sort.Strings(ids)
		return ids, cobra.ShellCompDirectiveNoFileComp
	}
}
