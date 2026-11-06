package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/tmux"
)

func (a *app) projectCommands() {
	create := &cobra.Command{Use: "create [server/project]", Short: "Create a project on this machine or a server", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
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
				if len(parts) != 2 {
					return config.Invalid("expected server/project")
				}
				name, project = parts[0], parts[1]
			}
			if !config.ValidName(project) {
				return config.Invalid("invalid project name")
			}
		}
		interactive := project == "" || dir == ""
		if interactive && (a.json || !terminalAvailable(a.in)) {
			return config.Invalid("use tdev create [server/]project --directory PATH without a terminal")
		}
		prefs, err := a.prefs()
		if err != nil {
			return err
		}
		if name == "" {
			name = "local"
			if len(args) == 0 && !c.Flags().Changed("server") {
				name, err = a.chooseServer(c.Context(), prefs, "选择项目所在服务器")
				if err != nil {
					return err
				}
			}
		}
		t, err := a.serverTarget(prefs, name, project)
		if err != nil {
			return err
		}
		p, err := a.createRemoteProject(c.Context(), t, dir, interactive)
		if err == nil && interactive {
			fmt.Fprintf(a.errOut, "接下来运行：\n  tdev open %s/%s\n", t.Name, p.Name)
			return nil
		}
		return a.result(p, err)
	}}
	create.Flags().String("directory", "", "existing project directory")
	create.Flags().String("server", "", "project server (default local)")
	a.root.AddCommand(create)
	a.command(a.root, "show <project>", "Show the project definition, client overrides and effective configuration", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		effective, err := a.effective(t, p)
		return map[string]any{"target": t.Name + "/" + t.Project, "definition": p, "overrides": t.Server.Projects[p.Name], "effective": effective}, err
	})
	edit := a.command(a.root, "edit <project>", "Edit a project using the creation wizard or explicit input", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		original := p
		if expected(c) > 0 && expected(c) != p.Revision {
			return nil, protocol.Fail("CONFIG_CONFLICT", 4, "project configuration changed")
		}
		stdin, _ := c.Flags().GetBool("stdin")
		var override projectOverride
		if stdin {
			if c.Flags().Changed("directory") {
				return nil, config.Invalid("choose --stdin or --directory")
			}
			if expected(c) < 1 {
				return nil, config.Invalid("--stdin requires --expected-revision from tdev show")
			}
			if err = a.read(&p); err != nil {
				return nil, err
			}
			if p.Revision != expected(c) {
				return nil, config.Invalid("input revision must equal expected revision")
			}
		} else if c.Flags().Changed("directory") {
			dir, _ := c.Flags().GetString("directory")
			p.Directory, err = a.remoteDirectory(ctx, t, dir)
			if err != nil {
				return nil, err
			}
		} else {
			if a.json || !terminalAvailable(a.in) {
				return nil, config.Invalid("edit requires a terminal or --directory/--stdin")
			}
			p, override, err = a.projectWizard(ctx, t, p)
			if err != nil {
				return nil, err
			}
		}
		if p.ID != original.ID || p.Name != original.Name {
			return nil, config.Invalid("project identity cannot change")
		}
		data, err := a.writeProject(ctx, t, p, original.Revision)
		if err != nil {
			return nil, err
		}
		if override.ProjectID != "" {
			if err = a.saveOverride(ctx, t, p, func(o *projectOverride) error { *o = override; return nil }); err != nil {
				return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "project saved; client overrides could not be saved: " + err.Error()}
			}
		}
		return data, nil
	})
	edit.Flags().String("directory", "", "existing project directory")
	edit.Flags().Bool("stdin", false, "read a complete project definition")
	revision(edit)
}
func (a *app) writeProject(ctx context.Context, t target, p config.Project, rev int) (json.RawMessage, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return a.call(ctx, t, b, "project", "set", p.Name, "--stdin", "--expected-revision", strconv.Itoa(rev))
}
func (a *app) chooseServer(ctx context.Context, p preferences, label string) (string, error) {
	names := append([]string{"local"}, orderedServers(p)...)
	fmt.Fprintf(a.errOut, "\n%s：\n  1. local（本机，默认）\n", label)
	for i, name := range names[1:] {
		fmt.Fprintf(a.errOut, "  %d. %s\n", i+2, name)
	}
	for {
		value, err := a.prompt(ctx, "输入序号或服务器名（q 退出）", "1")
		if err != nil {
			return "", err
		}
		if value == "q" {
			return "", protocol.Fail("CANCELLED", 130, "server selection cancelled")
		}
		for _, name := range names {
			if value == name {
				return name, nil
			}
		}
		if i, e := strconv.Atoi(value); e == nil && i > 0 && i <= len(names) {
			return names[i-1], nil
		}
		fmt.Fprintln(a.errOut, "请选择列表中的服务器。")
	}
}
func (a *app) remoteDirectory(ctx context.Context, t target, dir string) (string, error) {
	if dir == "" || !config.SafeText(dir) {
		return "", config.Invalid("provide an existing project directory")
	}
	quoted := tmux.Quote(dir)
	if dir == "~" {
		quoted = `"$HOME"`
	} else if strings.HasPrefix(dir, "~/") {
		quoted = `"$HOME"/` + tmux.Quote(dir[2:])
	} else if !strings.HasPrefix(dir, "/") {
		return "", config.Invalid("use an absolute path or ~/path")
	}
	data, err := a.shell(ctx, t, "cd -- "+quoted+" && pwd -P")
	if err != nil {
		return "", fmt.Errorf("project directory unavailable: %w", err)
	}
	path := strings.TrimSuffix(string(data), "\n")
	if !strings.HasPrefix(path, "/") || !config.SafeText(path) {
		return "", config.Invalid("invalid project directory response")
	}
	return path, nil
}
func (a *app) createRemoteProject(ctx context.Context, t target, dir string, interactive bool) (config.Project, error) {
	p := config.Project{SchemaVersion: 1, Revision: 1, ID: config.NewID(), Name: t.Project, Directory: dir, Windows: []config.Window{{ID: "main", Name: "main", Directory: "."}}, Forwards: []config.Forward{}}
	var override projectOverride
	var err error
	if interactive {
		fmt.Fprintf(a.errOut, "\n添加项目 · %s\n", t.Name)
		for !config.ValidName(p.Name) {
			p.Name, err = a.prompt(ctx, "项目名称（字母、数字、-、_）", "")
			if err != nil {
				return p, err
			}
			if !config.ValidName(p.Name) {
				fmt.Fprintln(a.errOut, "请输入有效的项目名称。")
			}
		}
		t.Project = p.Name
		t.Server.Projects = nil
		p, override, err = a.projectWizard(ctx, t, p)
		if err != nil {
			return p, err
		}
	} else {
		p.Directory, err = a.remoteDirectory(ctx, t, dir)
		if err != nil {
			return p, err
		}
	}
	if err = p.Validate(); err != nil {
		return p, err
	}
	b, _ := json.Marshal(p)
	data, err := a.call(ctx, t, b, "project", "create", p.Name, "--stdin")
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if override.ProjectID != "" {
		if err = a.saveOverride(ctx, t, p, func(o *projectOverride) error { *o = override; return nil }); err != nil {
			return p, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "project created; client overrides could not be saved: " + err.Error()}
		}
	}
	if interactive {
		fmt.Fprintf(a.errOut, "✓ 已创建项目 %s/%s\n  %s\n", t.Name, p.Name, p.Directory)
	}
	return p, nil
}

// The same draft editor is used for create, open-on-missing and edit. No write
// occurs until all fields and the final confirmation have been accepted.
func (a *app) projectWizard(ctx context.Context, t target, p config.Project) (config.Project, projectOverride, error) {
	override := cloneOverride(t.Server.Projects[p.Name])
	if override.ProjectID != "" && override.ProjectID != p.ID {
		return p, override, config.Invalid("project identity changed; reset client overrides first")
	}
	for {
		dir, err := a.promptWithCompletion(ctx, "项目目录（绝对路径或 ~/路径，Tab 补全）", p.Directory, func(ctx context.Context, prefix string) ([]string, error) {
			return a.completeRemoteDirectories(ctx, t, prefix)
		})
		if err != nil {
			return p, override, err
		}
		p.Directory, err = a.remoteDirectory(ctx, t, dir)
		if err == nil {
			break
		}
		fmt.Fprintln(a.errOut, err)
	}
	editWindows, err := a.wizardYes(ctx, "配置启动窗口？", false)
	if err != nil {
		return p, override, err
	}
	if editWindows {
		windows := []config.Window{}
		for _, w := range p.Windows {
			keep, err := a.wizardYes(ctx, "保留窗口 "+w.Name+"？", true)
			if err != nil {
				return p, override, err
			}
			if !keep {
				continue
			}
			w, err = a.windowWizard(ctx, w)
			if err != nil {
				return p, override, err
			}
			windows = append(windows, w)
		}
		for {
			add, err := a.wizardYes(ctx, "添加启动窗口？", len(windows) == 0)
			if err != nil {
				return p, override, err
			}
			if !add {
				if len(windows) == 0 {
					fmt.Fprintln(a.errOut, "至少需要一个窗口。")
					continue
				}
				break
			}
			w, err := a.windowWizard(ctx, config.Window{ID: "w" + config.NewID(), Name: "shell", Directory: "."})
			if err != nil {
				return p, override, err
			}
			windows = append(windows, w)
		}
		p.Windows = windows
	}
	if len(p.Forwards) > 0 {
		edit, err := a.wizardYes(ctx, "配置已有端口映射？", false)
		if err != nil {
			return p, override, err
		}
		if edit {
			forwards := []config.Forward{}
			for _, f := range p.Forwards {
				keep, err := a.wizardYes(ctx, "保留映射 "+f.Spec()+"？", true)
				if err != nil {
					return p, override, err
				}
				if !keep {
					delete(override.Forwards, f.ID)
					continue
				}
				f, override, err = a.forwardWizard(ctx, t, p.ID, f, override)
				if err != nil {
					return p, override, err
				}
				forwards = append(forwards, f)
			}
			p.Forwards = forwards
		}
	}
	for {
		add, err := a.wizardYes(ctx, "添加端口映射？", false)
		if err != nil {
			return p, override, err
		}
		if !add {
			break
		}
		f, o, err := a.forwardWizard(ctx, t, p.ID, config.Forward{}, override)
		if err != nil {
			return p, override, err
		}
		check := p
		check.Forwards = append(append([]config.Forward{}, p.Forwards...), f)
		if _, err = a.effectiveWithOverride(check, o); err != nil {
			fmt.Fprintln(a.errOut, err)
			continue
		}
		p = check
		override = o
	}
	effective, err := a.effectiveWithOverride(p, override)
	if err != nil {
		return p, override, err
	}
	fmt.Fprintf(a.errOut, "\n项目：%s/%s\n目录：%s\n", t.Name, p.Name, p.Directory)
	for _, w := range p.Windows {
		fmt.Fprintf(a.errOut, "窗口：%s  %s\n", w.Name, w.Command)
	}
	for _, f := range effective.Forwards {
		fmt.Fprintf(a.errOut, "映射：-%s %s\n", f.Direction, f.Spec())
	}
	yes, err := a.wizardYes(ctx, "保存项目配置？", true)
	if err != nil {
		return p, override, err
	}
	if !yes {
		return p, override, protocol.Fail("CANCELLED", 130, "project changes cancelled")
	}
	return p, override, nil
}
func (a *app) effectiveWithOverride(p config.Project, o projectOverride) (config.Project, error) {
	if err := p.Validate(); err != nil {
		return p, err
	}
	return mergeProject(p, o)
}
func (a *app) windowWizard(ctx context.Context, w config.Window) (config.Window, error) {
	var err error
	w.Name, err = a.prompt(ctx, "窗口名称", w.Name)
	if err != nil {
		return w, err
	}
	w.Directory, err = a.prompt(ctx, "窗口目录（相对项目根目录）", w.Directory)
	if err != nil {
		return w, err
	}
	w.Command, err = a.prompt(ctx, "启动命令（- 清空，空命令进入 shell）", w.Command)
	if w.Command == "-" {
		w.Command = ""
	}
	return w, err
}
func (a *app) forwardWizard(ctx context.Context, t target, projectID string, f config.Forward, o projectOverride) (config.Forward, projectOverride, error) {
	o = cloneOverride(o)
	if o.ProjectID != "" && o.ProjectID != projectID {
		return f, o, config.Invalid("project identity changed; reset client overrides first")
	}
	if f.ID == "" {
		f.ID = "f" + config.NewID()
	}
	if f.Direction == "" {
		f.Direction = "L"
	}
	for {
		value, err := a.prompt(ctx, "方向（L 本机到远端 / R 远端到本机）", f.Direction)
		if err != nil {
			return f, o, err
		}
		value = strings.ToUpper(value)
		if value == "L" || value == "R" {
			f.Direction = value
			break
		}
		fmt.Fprintln(a.errOut, "请输入 L 或 R。")
	}
	fallback := ""
	if f.TargetHost != "" {
		fallback = net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort))
	}
	for {
		value, err := a.prompt(ctx, "服务地址（host:port）", fallback)
		if err != nil {
			return f, o, err
		}
		host, port, err := net.SplitHostPort(value)
		if err == nil {
			n, e := strconv.Atoi(port)
			check := config.Forward{Direction: f.Direction, BindAddress: "localhost", ListenPort: n, TargetHost: host, TargetPort: n}
			if e == nil && check.Validate() == nil {
				f.TargetHost, f.TargetPort = host, n
				break
			}
		}
		fmt.Fprintln(a.errOut, "请输入完整服务地址，例如 localhost:8000 或 [::1]:8000。")
	}
	if f.BindAddress == "" {
		f.BindAddress = "localhost"
	}
	if f.ListenPort == 0 {
		f.ListenPort = f.TargetPort
	}
	port := f.ListenPort
	if patch := o.Forwards[f.ID]; patch.ListenPort != nil {
		port = *patch.ListenPort
	}
	for {
		label := "本机监听端口"
		if f.Direction == "R" {
			label = "远端监听端口"
		}
		value, err := a.prompt(ctx, label, strconv.Itoa(port))
		if err != nil {
			return f, o, err
		}
		n, err := strconv.Atoi(value)
		if err == nil && n > 0 && n <= 65535 {
			port = n
			break
		}
		fmt.Fprintln(a.errOut, "端口需要在 1–65535 之间。")
	}
	if f.Direction == "L" && !t.Local {
		if port != f.ListenPort {
			if o.Forwards == nil {
				o.Forwards = map[string]forwardOverride{}
			}
			patch := o.Forwards[f.ID]
			patch.ListenPort = &port
			o.Forwards[f.ID] = patch
			o.ProjectID = projectID
		} else if patch, ok := o.Forwards[f.ID]; ok {
			patch.ListenPort = nil
			o.Forwards[f.ID] = patch
		}
	} else {
		f.ListenPort = port
		delete(o.Forwards, f.ID)
	}
	return f, o, f.Validate()
}

func cloneOverride(o projectOverride) projectOverride {
	data, _ := json.Marshal(o)
	var copy projectOverride
	_ = json.Unmarshal(data, &copy)
	return copy
}
