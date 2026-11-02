package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"golang.org/x/term"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
)

type policyPatch struct {
	Attempts *reconnect.Attempts `yaml:"attempts,omitempty"`
	Interval *time.Duration      `yaml:"interval,omitempty"`
}
type policyPatches struct {
	Terminal policyPatch `yaml:"terminal"`
	Forward  policyPatch `yaml:"forward"`
	HTTP     policyPatch `yaml:"http"`
}

func (p policyPatch) apply(v reconnect.Policy) reconnect.Policy {
	if p.Attempts != nil {
		v.Attempts = *p.Attempts
	}
	if p.Interval != nil {
		v.Interval = *p.Interval
	}
	return v
}

type server struct {
	SSHAlias        string        `yaml:"ssh_alias"`
	SSHConfig       string        `yaml:"ssh_config,omitempty"`
	RemoteBinary    string        `yaml:"remote_binary"`
	RemoteConfigDir string        `yaml:"remote_config_dir,omitempty"`
	Reconnect       policyPatches `yaml:"reconnect,omitempty"`
}
type preferences struct {
	SchemaVersion int                `yaml:"schema_version"`
	DefaultServer string             `yaml:"default_server"`
	Servers       map[string]server  `yaml:"servers"`
	Reconnect     reconnect.Policies `yaml:"reconnect"`
	SSH           sshx.Options       `yaml:"ssh"`
}
type target struct {
	Server        server
	Name, Project string
	Local, Batch  bool
	SSH           sshx.Options
	Reconnect     reconnect.Policies
}

func defaultPreferences() preferences {
	return preferences{SchemaVersion: 1, Servers: map[string]server{}, Reconnect: reconnect.Defaults(), SSH: sshx.Defaults()}
}
func (a *app) prefs() (preferences, error) {
	p := defaultPreferences()
	b, err := os.ReadFile(filepath.Join(a.store.Root, "client.yaml"))
	if err != nil {
		return p, protocol.Fail("SETUP_REQUIRED", 2, "run tdev setup: %v", err)
	}
	if err = config.Decode(b, &p); err != nil {
		return p, err
	}
	if p.SchemaVersion != 1 || p.Servers[p.DefaultServer].SSHAlias == "" {
		return p, config.Invalid("invalid client configuration")
	}
	if err = p.SSH.Validate(); err != nil {
		return p, err
	}
	for _, policy := range []reconnect.Policy{p.Reconnect.Terminal, p.Reconnect.Forward, p.Reconnect.HTTP} {
		if err = policy.Validate(); err != nil {
			return p, err
		}
	}
	for n, s := range p.Servers {
		if err = validateServer(n, s); err != nil {
			return p, err
		}
		for _, v := range []reconnect.Policy{s.Reconnect.Terminal.apply(p.Reconnect.Terminal), s.Reconnect.Forward.apply(p.Reconnect.Forward), s.Reconnect.HTTP.apply(p.Reconnect.HTTP)} {
			if err = v.Validate(); err != nil {
				return p, err
			}
		}
	}
	p.SSH.ConfigFile = a.sshConfig
	return p, nil
}
func validateServer(n string, s server) error {
	if !config.ValidName(n) || sshx.CheckAlias(s.SSHAlias) != nil || s.RemoteBinary == "" || strings.HasPrefix(s.RemoteBinary, "-") || !config.SafeText(s.RemoteBinary) || !config.SafeText(s.RemoteConfigDir) || !config.SafeText(s.SSHConfig) {
		return config.Invalid("invalid server name, SSH alias or remote binary")
	}
	return nil
}
func (a *app) serverTarget(p preferences, n, project string) (target, error) {
	s, ok := p.Servers[n]
	if !ok {
		return target{}, config.Invalid("unknown server %q", n)
	}
	policies := p.Reconnect
	if a.sshConfig == "" && s.SSHConfig != "" {
		p.SSH.ConfigFile = s.SSHConfig
	}
	policies.Terminal = s.Reconnect.Terminal.apply(policies.Terminal)
	policies.Forward = s.Reconnect.Forward.apply(policies.Forward)
	policies.HTTP = s.Reconnect.HTTP.apply(policies.HTTP)
	return target{Server: s, Name: n, Project: project, SSH: p.SSH, Reconnect: policies}, nil
}
func (a *app) resolve(ctx context.Context, arg string, confirm bool) (target, error) {
	if arg == "" {
		p, err := a.manager().Context(ctx)
		if err != nil {
			return target{}, err
		}
		if confirm {
			if err = a.confirm(fmt.Sprintf("Detected local project %q. Continue?", p.Name)); err != nil {
				return target{}, err
			}
		}
		return target{Project: p.Name, Local: true, Reconnect: reconnect.Defaults(), SSH: sshx.Defaults()}, nil
	}
	p, err := a.prefs()
	if err != nil {
		return target{}, err
	}
	n, project := p.DefaultServer, arg
	if strings.Contains(arg, "/") {
		parts := strings.Split(arg, "/")
		if len(parts) != 2 {
			return target{}, config.Invalid("expected server/project")
		}
		n, project = parts[0], parts[1]
	}
	if !config.ValidName(project) {
		return target{}, config.Invalid("invalid project")
	}
	return a.serverTarget(p, n, project)
}
func (a *app) confirm(message string) error {
	yes, _ := a.root.PersistentFlags().GetBool("yes")
	if yes {
		return nil
	}
	fmt.Fprint(a.errOut, message, " [y/N] ")
	if a.reader == nil {
		a.reader = bufio.NewReader(a.in)
	}
	line, err := a.reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	s := strings.ToLower(strings.TrimSpace(line))
	if s != "y" && s != "yes" {
		return protocol.Fail("CANCELLED", 130, "cancelled")
	}
	return nil
}
func remoteCommand(t target, args ...string) string {
	all := []string{t.Server.RemoteBinary}
	if t.Server.RemoteConfigDir != "" {
		all = append(all, "--config-dir", t.Server.RemoteConfigDir)
	}
	all = append(all, args...)
	for i := range all {
		all[i] = tmux.Quote(all[i])
	}
	return strings.Join(all, " ")
}
func (a *app) call(ctx context.Context, t target, input []byte, args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	var out []byte
	var err error
	if t.Local {
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		cmd := sshx.Command(ctx, exe, append([]string{"--config-dir", a.store.Root, "--json", "local"}, args...)...)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err = cmd.Output()
		if err != nil && len(out) == 0 {
			return nil, protocol.Fail("RUNTIME_ERROR", 1, "local command: %v: %s", err, stderr.String())
		}
	} else {
		cmd := sshx.Command(ctx, "ssh", append(t.SSH.Args(t.Batch), "-T", t.Server.SSHAlias, remoteCommand(t, append([]string{"--json", "local"}, args...)...))...)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err = cmd.Output()
		if err != nil && len(out) == 0 {
			return nil, sshx.Error(err, stderr.String())
		}
	}
	var r struct {
		ProtocolVersion int             `json:"protocol_version"`
		OK              bool            `json:"ok"`
		Data            json.RawMessage `json:"data"`
		Error           *protocol.Error `json:"error"`
	}
	if e := json.Unmarshal(out, &r); e != nil {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "invalid control response: %v", e)
	}
	if r.ProtocolVersion != protocol.Version {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "unsupported protocol version %d", r.ProtocolVersion)
	}
	if !r.OK {
		if r.Error == nil {
			return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "missing remote error")
		}
		r.Error.Exit = exitForCode(r.Error.Code)
		return nil, r.Error
	}
	if err != nil {
		return nil, sshx.Error(err, "control process failed after responding")
	}
	return r.Data, nil
}
func exitForCode(code string) int {
	switch code {
	case "PROJECT_NOT_FOUND":
		return 3
	case "CONFIG_CONFLICT", "SESSION_CONFLICT", "DESTINATION_CHANGED", "SOURCE_CHANGED":
		return 4
	case "SESSION_NOT_FOUND":
		return 5
	case "CONNECTION_FAILED", "RETRIES_EXHAUSTED":
		return 6
	case "DEPENDENCY_MISSING", "MOUNT_UNVERIFIED":
		return 7
	case "PROTOCOL_INCOMPATIBLE":
		return 8
	case "PARTIAL_SUCCESS":
		return 9
	case "CANCELLED":
		return 130
	case "INVALID_CONFIG", "INVALID_ARGUMENT", "UNSUPPORTED_LAYOUT", "WINDOW_NOT_FOUND", "FORWARD_NOT_FOUND", "NO_PROJECT_CONTEXT", "VERIFICATION_FAILED", "UNSAFE_PATH":
		return 2
	default:
		return 1
	}
}
func (a *app) get(ctx context.Context, t target) (config.Project, error) {
	var p config.Project
	if err := a.handshake(ctx, t, "project-v1"); err != nil {
		return p, err
	}
	b, err := a.call(ctx, t, nil, "config", "get", t.Project)
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err == nil {
		err = p.Validate()
	}
	return p, err
}
func (a *app) handshake(ctx context.Context, t target, required ...string) error {
	data, err := a.call(ctx, t, nil, "version")
	if err != nil {
		if protocol.Normalize(err).Code == "CONNECTION_FAILED" {
			return err
		}
		return protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "remote capability handshake failed: %v", err)
	}
	var v struct {
		ProtocolVersion int      `json:"protocol_version"`
		SchemaVersion   int      `json:"schema_version"`
		Capabilities    []string `json:"capabilities"`
	}
	if err = json.Unmarshal(data, &v); err != nil {
		return protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "invalid remote capability response")
	}
	if v.ProtocolVersion != 1 || v.SchemaVersion != 1 {
		return protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "remote protocol/configuration is incompatible")
	}
	available := map[string]bool{}
	for _, c := range v.Capabilities {
		available[c] = true
	}
	for _, c := range required {
		if !available[c] {
			return protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "remote lacks required capability %s; upgrade it first", c)
		}
	}
	return nil
}
func (a *app) client() {
	a.root.PersistentFlags().BoolP("yes", "y", false, "accept displayed project, overwrite and reverse-forward confirmations")
	a.root.PersistentFlags().StringVar(&a.sshConfig, "ssh-config", "", "alternative OpenSSH configuration (also used by scp/SSHFS)")
	a.setupCommand()
	a.workspaceCommands()
	a.forwardCommand()
	a.transferCommands()
	a.mountCommands()
	a.updateCommands()
}
func (a *app) setupCommand() {
	c := a.command(a.root, "setup", "Register SSH connectivity and install/verify the remote binary", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		name, _ := c.Flags().GetString("name")
		alias, _ := c.Flags().GetString("ssh-alias")
		binary, _ := c.Flags().GetString("remote-binary")
		remoteConfig, _ := c.Flags().GetString("remote-config-dir")
		host, _ := c.Flags().GetString("host")
		if alias == "" && host != "" {
			alias = name
			if alias == "" {
				return nil, config.Invalid("--name is required when using --host")
			}
		}
		if alias == "" {
			fmt.Fprint(a.errOut, "Existing SSH alias: ")
			if a.reader == nil {
				a.reader = bufio.NewReader(a.in)
			}
			line, err := a.reader.ReadString('\n')
			if err != nil && err != io.EOF {
				return nil, err
			}
			alias = strings.TrimSpace(line)
		}
		if name == "" {
			name = alias
		}
		s := server{SSHAlias: alias, RemoteBinary: binary, RemoteConfigDir: remoteConfig}
		if err := validateServer(name, s); err != nil {
			return nil, err
		}
		p := defaultPreferences()
		if _, err := os.Stat(filepath.Join(a.store.Root, "client.yaml")); err == nil {
			p, err = a.prefs()
			if err != nil {
				return nil, err
			}
		}
		p.SSH.ConfigFile = a.sshConfig
		var cleanup func()
		var err error
		s, cleanup, err = a.newConnection(c, s)
		if err != nil {
			return nil, err
		}
		keepConnection := false
		defer func() {
			if !keepConnection {
				cleanup()
			}
		}()
		if s.SSHConfig != "" {
			p.SSH.ConfigFile = s.SSHConfig
		}
		if err = applySetupPolicies(c, &p); err != nil {
			return nil, err
		}
		t := target{Server: s, SSH: p.SSH, Reconnect: p.Reconnect}
		install, _ := c.Flags().GetBool("install")
		if install {
			if _, err := a.installRemote(ctx, t, c); err != nil {
				return nil, err
			}
		}
		if err := a.handshake(ctx, t, "session-v1", "attachment-v1"); err != nil {
			return nil, err
		}
		if _, err := a.call(ctx, t, nil, "doctor"); err != nil {
			return nil, err
		}
		unlock, err := a.store.Lock(ctx, "client", "config")
		if err != nil {
			return nil, err
		}
		defer unlock()
		if _, err = os.Stat(filepath.Join(a.store.Root, "client.yaml")); err == nil {
			p, err = a.prefs()
			if err != nil {
				return nil, err
			}
		}
		if err = applySetupPolicies(c, &p); err != nil {
			return nil, err
		}
		p.Servers[name] = s
		p.DefaultServer = name
		b, err := yaml.Marshal(p)
		if err != nil {
			return nil, err
		}
		if err = config.AtomicWrite(filepath.Join(a.store.Root, "client.yaml"), b, 0600); err != nil {
			return nil, err
		}
		keepConnection = true
		return map[string]string{"server": name, "ssh_alias": alias}, nil
	})
	c.Flags().String("name", "", "local server name")
	c.Flags().String("ssh-alias", "", "existing SSH alias")
	c.Flags().String("remote-binary", ".local/bin/tdev", "absolute or home-relative remote binary")
	c.Flags().String("remote-config-dir", "", "optional remote configuration root")
	c.Flags().Bool("install", false, "install a verified release on the remote before registration")
	releaseFlags(c)
	setupConnectionFlags(c)
}
func terminalAvailable(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
func retryFlags(c *cobra.Command) {
	c.Flags().String("reconnect-attempts", "", "override attempts: 0, nonnegative integer, or always")
	c.Flags().Duration("reconnect-interval", 0, "override wait between attempts")
}
func commandPolicy(c *cobra.Command, p reconnect.Policy) (reconnect.Policy, error) {
	if c.Flags().Changed("reconnect-attempts") {
		s, _ := c.Flags().GetString("reconnect-attempts")
		n, err := reconnect.ParseAttempts(s)
		if err != nil {
			return p, config.Invalid("%v", err)
		}
		p.Attempts = reconnect.Attempts(n)
	}
	if c.Flags().Changed("reconnect-interval") {
		p.Interval, _ = c.Flags().GetDuration("reconnect-interval")
	}
	return p, p.Validate()
}
func retryable(err error) bool { return reconnect.Retryable(err) }
