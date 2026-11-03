package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/tmux"
)

// Client project management uses the same revision-checked remote API as local
// administration. Raw remote definitions, rather than client overrides, are edited.
func (a *app) projectCommands() {
	p := group(a.root, "project", "Manage projects saved on a remote server")
	list := a.command(p, "list", "Discover remote projects", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return a.listProjects(ctx, c)
	})
	list.Flags().String("server", "", "only list this saved server")
	list.Flags().Bool("all", false, "query every configured server")
	createCommand := func() *cobra.Command {
		create := &cobra.Command{Use: "create [server/project]", Short: "Create a remote project, with a wizard when directory is omitted", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
			if a.timeout <= 0 {
				return config.Invalid("timeout must be positive")
			}
			dir, _ := c.Flags().GetString("directory")
			name, _ := c.Flags().GetString("server")
			project := ""
			if len(args) > 0 {
				project = args[0]
				if strings.Contains(project, "/") {
					if c.Flags().Changed("server") {
						return config.Invalid("choose --server or server/project")
					}
					parts := strings.Split(project, "/")
					if len(parts) != 2 || parts[0] == "" {
						return config.Invalid("expected server/project")
					}
					name, project = parts[0], parts[1]
				}
				if !config.ValidName(project) {
					return config.Invalid("invalid project name")
				}
			}
			interactive := project == "" || dir == "" || name == ""
			if interactive && (a.json || !terminalAvailable(a.in)) {
				return config.Invalid("use tdev create <server/project> --directory <remote-directory> without a terminal")
			}
			prefs, err := a.prefs()
			if err != nil {
				return err
			}
			if name == "" {
				name, err = a.chooseProjectServer(c.Context(), prefs)
				if err != nil {
					return err
				}
			}
			t, err := a.serverTarget(prefs, name, project)
			if err != nil {
				return err
			}
			created, err := a.createRemoteProject(c.Context(), t, dir, interactive)
			if err == nil && interactive {
				fmt.Fprintf(a.errOut, "接下来运行：\n  tdev open %s/%s\n", t.Name, created.Name)
				return nil
			}
			return a.result(created, err)
		}}
		create.Flags().String("directory", "", "existing directory on the remote server")
		create.Flags().String("server", "", "server for the project creation wizard")
		return create
	}
	a.root.AddCommand(createCommand())
	compatCreate := createCommand()
	compatCreate.Hidden = true
	p.AddCommand(compatCreate)
	a.command(p, "show <server/project>", "Read the shared remote definition", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		return a.get(ctx, t)
	})
	update := a.command(p, "update <server/project>", "Update the remote directory or replace a definition from stdin", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		current, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		if expected(c) > 0 && expected(c) != current.Revision {
			return nil, protocol.Fail("CONFIG_CONFLICT", 4, "project configuration changed")
		}
		stdin, _ := c.Flags().GetBool("stdin")
		if stdin {
			if c.Flags().Changed("directory") {
				return nil, config.Invalid("choose --stdin or --directory")
			}
			if expected(c) < 1 {
				return nil, config.Invalid("--stdin requires --expected-revision from project show")
			}
			if err = a.read(&current); err != nil {
				return nil, err
			}
			if current.Revision != expected(c) {
				return nil, config.Invalid("input revision must equal expected revision")
			}
		} else {
			if !c.Flags().Changed("directory") {
				return nil, config.Invalid("provide --directory or --stdin")
			}
			dir, _ := c.Flags().GetString("directory")
			current.Directory, err = a.remoteDirectory(ctx, t, dir)
			if err != nil {
				return nil, err
			}
		}
		b, err := json.Marshal(current)
		if err != nil {
			return nil, err
		}
		return a.call(ctx, t, b, "config", "set", t.Project, "--stdin", "--expected-revision", strconv.Itoa(current.Revision))
	})
	update.Flags().String("directory", "", "existing remote project root")
	update.Flags().Bool("stdin", false, "read a complete remote project definition as YAML/JSON")
	revision(update)
	// These commands intentionally edit shared remote definitions. Local listening
	// port preferences remain available through tdev override.
	w := group(p, "window", "Manage remote startup windows; changes apply to future sessions")
	for _, op := range []string{"add", "update", "remove"} {
		op := op
		c := a.remoteDefinitionCommand(w, "window", op)
		c.Flags().String("id", "", "stable window ID")
		if op != "add" {
			_ = c.MarkFlagRequired("id")
		}
		if op == "add" {
			c.Flags().String("name", "", "window display name")
			_ = c.MarkFlagRequired("name")
			c.Flags().String("command", "", "shell startup command")
			c.Flags().String("directory", ".", "remote directory relative to project root")
		}
		if op == "update" {
			c.Flags().Bool("stdin", false, "read replacement window as YAML/JSON")
			_ = c.MarkFlagRequired("stdin")
		}
	}
	f := group(p, "forward", "Manage shared remote forwarding definitions")
	add := a.remoteDefinitionCommand(f, "forward", "add")
	forwardFlags(add)
	remove := a.remoteDefinitionCommand(f, "forward", "remove")
	remove.Flags().String("id", "", "stable forward ID")
	_ = remove.MarkFlagRequired("id")
}

func (a *app) chooseProjectServer(ctx context.Context, prefs preferences) (string, error) {
	names := make([]string, 0, len(prefs.Servers))
	for name := range prefs.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", protocol.Fail("SETUP_REQUIRED", 2, "run tdev setup to add a server")
	}
	fmt.Fprintln(a.errOut, "\n选择项目所在服务器：")
	defaultIndex := "1"
	for i, name := range names {
		label := ""
		if name == prefs.DefaultServer {
			label = "（默认）"
			defaultIndex = strconv.Itoa(i + 1)
		}
		fmt.Fprintf(a.errOut, "  %d. %s  %s %s\n", i+1, name, prefs.Servers[name].SSHAlias, label)
	}
	for {
		value, err := a.prompt(ctx, "输入序号或服务器名（q 退出）", defaultIndex)
		if err != nil {
			return "", err
		}
		if value == "q" {
			return "", protocol.Fail("CANCELLED", 130, "creation cancelled")
		}
		if _, exists := prefs.Servers[value]; exists {
			return value, nil
		}
		if index, err := strconv.Atoi(value); err == nil && index > 0 && index <= len(names) {
			return names[index-1], nil
		}
		fmt.Fprintln(a.errOut, "请选择列表中的服务器。")
	}
}

func (a *app) remoteDefinitionCommand(parent *cobra.Command, kind, op string) *cobra.Command {
	c := a.command(parent, op+" <server/project>", op+" a shared remote "+kind+" definition", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		rev := expected(c)
		if rev == 0 {
			rev = p.Revision
		}
		remoteArgs := []string{kind, op, t.Project, "--expected-revision", strconv.Itoa(rev)}
		for _, name := range []string{"id", "name", "command", "directory"} {
			if c.Flags().Changed(name) {
				value, _ := c.Flags().GetString(name)
				remoteArgs = append(remoteArgs, "--"+name, value)
			}
		}
		for _, name := range []string{"local", "reverse"} {
			if c.Flags().Lookup(name) != nil {
				values, _ := c.Flags().GetStringArray(name)
				for _, value := range values {
					remoteArgs = append(remoteArgs, "--"+name, value)
				}
			}
		}
		var input []byte
		if op == "update" {
			stdin, _ := c.Flags().GetBool("stdin")
			if !stdin {
				return nil, config.Invalid("--stdin is required")
			}
			var win config.Window
			if err = a.read(&win); err != nil {
				return nil, err
			}
			input, err = json.Marshal(win)
			if err != nil {
				return nil, err
			}
			remoteArgs = append(remoteArgs, "--stdin")
		}
		return a.call(ctx, t, input, remoteArgs...)
	})
	revision(c)
	return c
}

func (a *app) remoteDirectory(ctx context.Context, t target, dir string) (string, error) {
	if dir == "" || !config.SafeText(dir) {
		return "", config.Invalid("provide an existing remote directory")
	}
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := a.shell(ctx, t, `printf '%s' "$HOME"`)
		if err != nil {
			return "", err
		}
		dir = path.Join(string(home), strings.TrimPrefix(dir, "~"))
	}
	if !path.IsAbs(dir) {
		return "", config.Invalid("use an absolute remote path or ~/path")
	}
	data, err := a.shell(ctx, t, "cd -- "+tmux.Quote(dir)+" && pwd -P")
	if err != nil {
		return "", fmt.Errorf("remote directory unavailable: %w", err)
	}
	resolved := strings.TrimSuffix(string(data), "\n")
	if !path.IsAbs(resolved) || !config.SafeText(resolved) {
		return "", config.Invalid("invalid remote directory response")
	}
	return resolved, nil
}

func (a *app) createRemoteProject(ctx context.Context, t target, dir string, interactive bool) (config.Project, error) {
	var p config.Project
	if err := a.handshake(ctx, t, "project-v1"); err != nil {
		return p, err
	}
	if interactive {
		fmt.Fprintf(a.errOut, "\n添加远端项目 · %s\n", t.Name)
		for !config.ValidName(t.Project) {
			name, err := a.prompt(ctx, "项目名称（字母、数字、-、_）", "")
			if err != nil {
				return p, err
			}
			t.Project = name
			if !config.ValidName(name) {
				fmt.Fprintln(a.errOut, "请输入有效的项目名称。")
			}
		}
	}
	for {
		if interactive && dir == "" {
			value, err := a.promptWithCompletion(ctx, "远端已有项目目录（绝对路径或 ~/路径，Tab 补全）", "", func(ctx context.Context, prefix string) ([]string, error) {
				return a.completeRemoteDirectories(ctx, t, prefix)
			})
			if err != nil {
				return p, err
			}
			dir = value
		}
		resolved, err := a.remoteDirectory(ctx, t, dir)
		if err != nil {
			if !interactive {
				return p, err
			}
			fmt.Fprintln(a.errOut, err)
			dir = ""
			continue
		}
		data, err := a.call(ctx, t, nil, "project", "create", t.Project, "--directory", resolved)
		if err != nil {
			return p, err
		}
		if err = json.Unmarshal(data, &p); err != nil {
			return p, err
		}
		if err = p.Validate(); err != nil {
			return p, err
		}
		if interactive {
			fmt.Fprintf(a.errOut, "✓ 已创建项目 %s/%s\n  %s\n已添加 shell 窗口。\n", t.Name, p.Name, p.Directory)
		}
		return p, nil
	}
}
