package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
)

func (a *app) prompt(ctx context.Context, label, fallback string) (string, error) {
	return a.promptWithCompletion(ctx, label, fallback, nil)
}

func (a *app) promptWithCompletion(ctx context.Context, label, fallback string, complete directoryCompleter) (string, error) {
	if terminalAvailable(a.in) {
		return a.editLine(ctx, label, fallback, complete)
	}
	if fallback == "" {
		fmt.Fprint(a.errOut, label+": ")
	} else {
		fmt.Fprintf(a.errOut, "%s [%s]: ", label, fallback)
	}
	if a.reader == nil {
		a.reader = bufio.NewReader(a.in)
	}
	type answer struct {
		line string
		err  error
	}
	answers := make(chan answer, 1)
	go func() { line, err := a.reader.ReadString('\n'); answers <- answer{line, err} }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-answers:
		if result.err != nil && (result.err != io.EOF || result.line == "") {
			return "", protocol.Fail("CANCELLED", 130, "setup cancelled")
		}
		line := strings.TrimSpace(result.line)
		if line == "" {
			line = fallback
		}
		return line, nil
	}
}
func (a *app) wizardYes(ctx context.Context, label string, defaultYes bool) (bool, error) {
	fallback := "n"
	if defaultYes {
		fallback = "y"
	}
	for {
		answer, err := a.prompt(ctx, label+" (y/n)", fallback)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(a.errOut, "请输入 y 或 n。")
	}
}

func serverName(value string) string {
	var out strings.Builder
	for _, ch := range value {
		if ch < 128 && (unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '-' || ch == '_') {
			out.WriteRune(ch)
		} else {
			out.WriteByte('-')
		}
	}
	name := strings.Trim(out.String(), "-_")
	if name == "" {
		name = "dev"
	}
	if len(name) > 55 {
		name = name[:55]
	}
	return name
}

type serverAddress struct {
	Host, User string
	Port       int
}

func parseServerAddress(input string) (serverAddress, error) {
	address := serverAddress{Port: 22}
	value := strings.TrimSpace(input)
	if strings.Contains(value, "@") {
		parts := strings.Split(value, "@")
		if len(parts) != 2 || parts[0] == "" {
			return address, config.Invalid("expected [user@]hostname[:port]")
		}
		address.User, value = parts[0], parts[1]
	}
	if strings.HasPrefix(value, "[") || strings.Count(value, ":") == 1 {
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			value = strings.Trim(value, "[]")
		} else {
			host, port, err := net.SplitHostPort(value)
			if err != nil {
				return address, config.Invalid("expected [user@]hostname[:port], with brackets around IPv6")
			}
			address.Port, err = strconv.Atoi(port)
			if err != nil {
				return address, config.Invalid("invalid port")
			}
			value = host
		}
	}
	if (net.ParseIP(value) == nil && !hostRE.MatchString(value)) || address.Port < 1 || address.Port > 65535 || !config.SafeText(address.User) || strings.ContainsAny(address.User, " /\\:@") || strings.HasPrefix(address.User, "-") {
		return address, config.Invalid("invalid server address, user or port")
	}
	address.Host = value
	return address, nil
}

type connectionChoice struct {
	Name, Alias, Label string
	Existing           *server
}

func cloneServer(s server) server {
	b, _ := json.Marshal(s)
	var copy server
	_ = json.Unmarshal(b, &copy)
	return copy
}

func (a *app) chooseConnection(ctx context.Context, c *cobra.Command, p preferences) (string, server, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", server{}, err
	}
	hosts, err := sshx.DiscoverHosts(a.sshConfig, home)
	if err != nil {
		fmt.Fprintln(a.errOut, "部分 SSH 配置无法读取；可选择已有 tdev 服务器或直接添加地址。")
	}
	lookup := map[string]sshx.HostEntry{}
	for _, host := range hosts {
		lookup[host.Alias] = host
	}
	var choices []connectionChoice
	names := make([]string, 0, len(p.Servers))
	for name := range p.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	if p.DefaultServer != "" {
		for i, name := range names {
			if name == p.DefaultServer {
				names = append([]string{name}, append(names[:i], names[i+1:]...)...)
				break
			}
		}
	}
	seen := map[string]bool{}
	label := func(host sshx.HostEntry) string {
		name := host.HostName
		if name == "" {
			name = host.Alias
		}
		if host.User != "" {
			name = host.User + "@" + name
		}
		if host.Port != "" {
			name += ":" + host.Port
		}
		return name
	}
	for _, name := range names {
		s := p.Servers[name]
		host := lookup[s.SSHAlias]
		if s.SSHConfig != "" {
			if entries, err := sshx.DiscoverHosts(s.SSHConfig, home); err == nil {
				for _, entry := range entries {
					if entry.Alias == s.SSHAlias {
						host = entry
						break
					}
				}
			}
		}
		if host.Alias == "" {
			host.Alias = s.SSHAlias
		}
		copy := cloneServer(s)
		choices = append(choices, connectionChoice{name, s.SSHAlias, name + "  " + label(host) + "  (已登记)", &copy})
		seen[s.SSHAlias] = true
	}
	for _, host := range hosts {
		if !seen[host.Alias] {
			choices = append(choices, connectionChoice{"", host.Alias, host.Alias + "  " + label(host), nil})
		}
	}
	fmt.Fprintln(a.errOut, "\n设置开发服务器\n已自动读取本机 SSH 与 tdev 配置。")
	visible := choices
	for {
		fmt.Fprintln(a.errOut, "\n选择服务器：")
		for i, choice := range visible {
			if i >= 12 {
				fmt.Fprintf(a.errOut, "  … 共 %d 项，可输入名称或地址搜索\n", len(visible))
				break
			}
			fmt.Fprintf(a.errOut, "  %d. %s\n", i+1, choice.Label)
		}
		fmt.Fprintln(a.errOut, "  0. 添加新服务器")
		fallback := "0"
		if len(visible) > 0 {
			fallback = "1"
		}
		input, err := a.prompt(ctx, "输入序号、搜索文字或服务器地址（q 退出）", fallback)
		if err != nil {
			return "", server{}, err
		}
		if input == "q" {
			return "", server{}, protocol.Fail("CANCELLED", 130, "setup cancelled")
		}
		if index, err := strconv.Atoi(input); err == nil && index > 0 && index <= len(visible) {
			choice := visible[index-1]
			if err = c.Flags().Set("host", ""); err != nil {
				return "", server{}, err
			}
			if err = c.Flags().Set("ssh-alias", choice.Alias); err != nil {
				return "", server{}, err
			}
			if choice.Existing != nil {
				return choice.Name, cloneServer(*choice.Existing), nil
			}
			name, err := a.chooseName(ctx, c, p, choice.Alias)
			return name, server{SSHAlias: choice.Alias}, err
		}
		if input != "0" {
			visible = nil
			query := strings.ToLower(input)
			for _, choice := range choices {
				if strings.Contains(strings.ToLower(choice.Label), query) {
					visible = append(visible, choice)
				}
			}
			if len(visible) > 0 {
				continue
			}
			if !strings.ContainsAny(input, "@.:") {
				fmt.Fprintln(a.errOut, "没有匹配的连接。输入 0 添加新服务器。")
				visible = choices
				continue
			}
		} else {
			input, err = a.prompt(ctx, "服务器地址（例如 developer@dev.example.com:22）", "")
			if err != nil {
				return "", server{}, err
			}
		}
		address, err := parseServerAddress(input)
		if err != nil {
			fmt.Fprintln(a.errOut, err)
			continue
		}
		if address.User == "" {
			fallback := ""
			if current, err := user.Current(); err == nil {
				fallback = current.Username
			}
			address.User, err = a.prompt(ctx, "用户名", fallback)
			if err != nil {
				return "", server{}, err
			}
		}
		if !strings.Contains(input, ":") {
			port, err := a.prompt(ctx, "SSH 端口", "22")
			if err != nil {
				return "", server{}, err
			}
			address.Port, err = strconv.Atoi(port)
			if err != nil || address.Port < 1 || address.Port > 65535 {
				fmt.Fprintln(a.errOut, "端口需要在 1–65535 之间。")
				continue
			}
		}
		name, err := a.chooseName(ctx, c, p, address.Host)
		if err != nil {
			return "", server{}, err
		}
		for flag, value := range map[string]string{"host": address.Host, "port": strconv.Itoa(address.Port), "user": address.User, "ssh-alias": name} {
			if err = c.Flags().Set(flag, value); err != nil {
				return "", server{}, err
			}
		}
		return name, server{SSHAlias: name}, nil
	}
}

func (a *app) chooseName(ctx context.Context, c *cobra.Command, p preferences, suggestion string) (string, error) {
	name, _ := c.Flags().GetString("name")
	if name != "" {
		return name, nil
	}
	base := serverName(suggestion)
	name = base
	for i := 2; p.Servers[name].SSHAlias != ""; i++ {
		name = base + "-" + strconv.Itoa(i)
	}
	for {
		answer, err := a.prompt(ctx, "给服务器起个名字", name)
		if err != nil {
			return "", err
		}
		if config.ValidName(answer) && p.Servers[answer].SSHAlias == "" {
			return answer, nil
		}
		fmt.Fprintln(a.errOut, "使用未占用的英文字母、数字、短横线或下划线名称。")
	}
}

func (a *app) runSetup(ctx context.Context, c *cobra.Command) (any, error) {
	if a.timeout <= 0 {
		return nil, config.Invalid("timeout must be positive")
	}
	p := defaultPreferences()
	configPath := filepath.Join(a.store.Root, "client.yaml")
	if _, err := os.Stat(configPath); err == nil {
		p, err = a.prefs()
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	name, _ := c.Flags().GetString("name")
	alias, _ := c.Flags().GetString("ssh-alias")
	host, _ := c.Flags().GetString("host")
	interactive, _ := c.Flags().GetBool("interactive")
	interactive = interactive || (alias == "" && host == "")
	if interactive && (a.json || !terminalAvailable(a.in)) {
		return nil, config.Invalid("setup wizard requires a terminal; use --host HOST --user USER or --ssh-alias ALIAS for noninteractive setup")
	}
	s := server{}
	var err error
	if interactive {
		name, s, err = a.chooseConnection(ctx, c, p)
		if err != nil {
			return nil, err
		}
		alias = s.SSHAlias
	} else {
		if name == "" {
			if alias != "" {
				name = serverName(alias)
			} else {
				name = serverName(host)
			}
		}
		if alias == "" {
			alias = name
		}
		s = cloneServer(p.Servers[name])
		s.SSHAlias = alias
	}
	oldServer, existed := p.Servers[name]
	if s.RemoteBinary == "" || c.Flags().Changed("remote-binary") {
		s.RemoteBinary, _ = c.Flags().GetString("remote-binary")
	}
	if c.Flags().Changed("remote-config-dir") {
		s.RemoteConfigDir, _ = c.Flags().GetString("remote-config-dir")
	}
	if err = validateServer(name, s); err != nil {
		return nil, err
	}
	if s.SSHConfig == "" && a.sshConfig != "" {
		s.SSHConfig = a.sshConfig
	}
	if err = applySetupPolicies(c, &p); err != nil {
		return nil, err
	}
	keep := false
	cleanup := func() {}
	defer func() {
		if !keep {
			cleanup()
		}
	}()
	s, cleanup, err = a.newConnection(c, s)
	if err != nil {
		return nil, err
	}
	p.SSH.ConfigFile = a.sshConfig
	if s.SSHConfig != "" {
		p.SSH.ConfigFile = s.SSHConfig
	}
	t := target{Server: s, Name: name, SSH: p.SSH, Reconnect: p.Reconnect}
	install, _ := c.Flags().GetBool("install")
	if interactive {
		fmt.Fprintln(a.errOut, "\n正在检查 SSH 连接…")
		for {
			_, err = a.shell(ctx, t, "printf tdev-ssh-ready")
			if err == nil {
				break
			}
			fmt.Fprintln(a.errOut, err)
			retry, promptErr := a.wizardYes(ctx, "重试连接（可先在另一个终端修复 SSH 配置）？", true)
			if promptErr != nil {
				return nil, promptErr
			}
			if !retry {
				return nil, err
			}
		}
		fmt.Fprintln(a.errOut, "✓ SSH 连接成功")
		probe := "if test -x " + tmux.Quote(s.RemoteBinary) + "; then printf '%s\\n' " + tmux.Quote(s.RemoteBinary) + "; elif command -v tdev >/dev/null 2>&1; then command -v tdev; fi"
		if c.Flags().Changed("remote-binary") {
			probe = "if test -x " + tmux.Quote(s.RemoteBinary) + "; then printf '%s\\n' " + tmux.Quote(s.RemoteBinary) + "; fi"
		}
		found, err := a.shell(ctx, t, probe)
		if err != nil {
			return nil, err
		}
		binary := strings.TrimSpace(string(found))
		if binary == "" {
			install, err = a.wizardYes(ctx, "远端尚未安装 tdev。安装官方签名版本到用户目录？", true)
			if err != nil {
				return nil, err
			}
			if !install {
				return nil, config.Invalid("remote tdev is required; rerun setup after installation")
			}
			t.InstallApproved = true
		} else if !c.Flags().Changed("remote-binary") {
			s.RemoteBinary = binary
			t.Server = s
		}
	}
	if install {
		if _, err = a.installRemote(ctx, t, c); err != nil {
			return nil, err
		}
	}
	if err = a.handshake(ctx, t, "session-v1", "attachment-v1"); err != nil {
		return nil, err
	}
	if _, err = a.call(ctx, t, nil, "doctor"); err != nil {
		return nil, fmt.Errorf("remote dependencies are not ready (tmux is required): %w", err)
	}
	// Read target definitions without caching them into client.yaml.
	data, err := a.call(ctx, t, nil, "project", "list", "--status")
	if err != nil {
		return nil, err
	}
	var projects []discoveredProject
	if err = json.Unmarshal(data, &projects); err != nil {
		return nil, err
	}
	for _, project := range projects {
		if err = project.Project.Validate(); err != nil {
			return nil, err
		}
	}
	if interactive {
		fmt.Fprintf(a.errOut, "✓ 读取远端已有配置，发现 %d 个项目\n", len(projects))
		for _, project := range projects {
			fmt.Fprintf(a.errOut, "  %s  %s  (%d 个窗口，%d 条转发)\n", project.Project.Name, project.Project.Directory, len(project.Project.Windows), len(project.Project.Forwards))
		}
		if len(projects) == 0 {
			fmt.Fprintln(a.errOut, "远端尚无项目。保存连接后可立即添加。")
		}
		s, err = a.wizardPorts(ctx, s, projects)
		if err != nil {
			return nil, err
		}
	}
	for i := range projects {
		projects[i].RemoteProject = projects[i].Project
		projects[i].Project, err = mergeProject(projects[i].Project, s.Projects[projects[i].Project.Name])
		if err != nil {
			return nil, err
		}
	}
	unlock, err := a.store.Lock(ctx, "client", "config")
	if err != nil {
		return nil, err
	}
	defer unlock()
	latest := defaultPreferences()
	if _, err = os.Stat(configPath); err == nil {
		latest, err = a.prefs()
		if err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	current, stillExists := latest.Servers[name]
	if existed != stillExists || (existed && !reflect.DeepEqual(current, oldServer)) {
		return nil, protocol.Fail("CONFIG_CONFLICT", 4, "server registration changed during setup; retry")
	}
	if err = applySetupPolicies(c, &latest); err != nil {
		return nil, err
	}
	latest.Servers[name] = s
	latest.DefaultServer = name
	b, err := yaml.Marshal(latest)
	if err != nil {
		return nil, err
	}
	if err = a.writeOverrides(name, s.Projects); err != nil {
		return nil, err
	}
	if err = config.AtomicWrite(configPath, b, 0600); err != nil {
		return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "local overrides saved; connection registration could not be saved: %v", err)
	}
	keep = true
	if interactive {
		fmt.Fprintf(a.errOut, "\n✓ 已保存连接 %s\n接下来运行：\n  tdev ls\n", name)
		if len(projects) > 0 {
			fmt.Fprintf(a.errOut, "  tdev open %s/%s\n", name, projects[0].Project.Name)
		} else {
			fmt.Fprintf(a.errOut, "  tdev create --server %s\n", name)
			create, promptErr := a.wizardYes(ctx, "现在添加第一个项目？", true)
			if promptErr != nil {
				return nil, promptErr
			}
			if create {
				if _, err = a.createRemoteProject(ctx, t, "", true); err != nil {
					return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "connection saved; project creation did not complete: %v", err)
				}
				data, err := a.call(ctx, t, nil, "project", "list", "--status")
				if err != nil {
					return nil, err
				}
				if err = json.Unmarshal(data, &projects); err != nil {
					return nil, err
				}
				if len(projects) > 0 {
					fmt.Fprintf(a.errOut, "接下来运行：\n  tdev open %s/%s\n", name, projects[0].Project.Name)
				}
			}
		}
	}
	return map[string]any{"server": name, "ssh_alias": s.SSHAlias, "projects": projects}, nil
}

func (a *app) wizardPorts(ctx context.Context, s server, projects []discoveredProject) (server, error) {
	count := 0
	for _, project := range projects {
		for _, f := range project.Project.Forwards {
			if f.Direction == "L" {
				count++
			}
		}
	}
	if count == 0 {
		return s, nil
	}
	edit, err := a.wizardYes(ctx, "要调整这些项目在本机使用的端口吗？", false)
	if err != nil || !edit {
		return s, err
	}
	if s.Projects == nil {
		s.Projects = map[string]projectOverride{}
	}
	for _, project := range projects {
		remote := project.Project
		override := s.Projects[remote.Name]
		if override.ProjectID != "" && override.ProjectID != remote.ID {
			return s, config.Invalid("local overrides refer to an older project; reset them with tdev override --reset")
		}
		if override.Forwards == nil {
			override.Forwards = map[string]forwardOverride{}
		}
		for _, f := range remote.Forwards {
			if f.Direction != "L" {
				continue
			}
			patch := override.Forwards[f.ID]
			port := f.ListenPort
			if patch.ListenPort != nil {
				port = *patch.ListenPort
			}
			for {
				value, err := a.prompt(ctx, fmt.Sprintf("%s / %s → %s:%d 的本机端口", remote.Name, f.ID, f.TargetHost, f.TargetPort), strconv.Itoa(port))
				if err != nil {
					return s, err
				}
				parsed, err := strconv.Atoi(value)
				if err != nil || parsed < 1 || parsed > 65535 {
					fmt.Fprintln(a.errOut, "端口需要在 1–65535 之间。")
					continue
				}
				port = parsed
				break
			}
			if port != f.ListenPort {
				patch.ListenPort = &port
			} else {
				patch.ListenPort = nil
			}
			if patch.ListenPort == nil && patch.BindAddress == nil && !patch.Disabled {
				delete(override.Forwards, f.ID)
			} else {
				override.Forwards[f.ID] = patch
			}
		}
		if len(override.Forwards) > 0 || len(override.LocalForwards) > 0 {
			override.ProjectID = remote.ID
			if _, err := mergeProject(remote, override); err != nil {
				return s, err
			}
			s.Projects[remote.Name] = override
		} else {
			delete(s.Projects, remote.Name)
		}
	}
	return s, nil
}
