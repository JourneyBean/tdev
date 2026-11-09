package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
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
	SSHAlias        string                     `yaml:"ssh_alias"`
	SSHConfig       string                     `yaml:"ssh_config,omitempty"`
	RemoteBinary    string                     `yaml:"remote_binary"`
	RemoteConfigDir string                     `yaml:"remote_config_dir,omitempty"`
	Reconnect       policyPatches              `yaml:"reconnect,omitempty"`
	Projects        map[string]projectOverride `yaml:"-"`
}
type preferences struct {
	SchemaVersion int                `yaml:"schema_version"`
	Servers       map[string]server  `yaml:"servers"`
	Reconnect     reconnect.Policies `yaml:"reconnect"`
	SSH           sshx.Options       `yaml:"ssh"`
}
type target struct {
	Server          server
	Name, Project   string
	Local, Batch    bool
	Scratch         bool
	InstallApproved bool
	SSH             sshx.Options
	Reconnect       reconnect.Policies
	Lease           string
}

func defaultPreferences() preferences {
	return preferences{SchemaVersion: 1, Servers: map[string]server{}, Reconnect: reconnect.Defaults(), SSH: sshx.Defaults()}
}
func (a *app) prefs() (preferences, error) {
	p := defaultPreferences()
	b, err := os.ReadFile(filepath.Join(a.store.Root, "client.yaml"))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	// Accept the retired input field when reading existing client files, then
	// discard it. preferences itself cannot use or serialize a default server.
	input := struct {
		Current              preferences `yaml:",inline"`
		IgnoredDefaultServer string      `yaml:"default_server,omitempty"`
	}{Current: p}
	if err = config.Decode(b, &input); err != nil {
		return p, err
	}
	p = input.Current
	if p.SchemaVersion != 1 {
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
	overrides, err := a.readOverrides()
	if err != nil {
		return p, err
	}
	for name, s := range p.Servers {
		s.Projects = overrides.Servers[name]
		p.Servers[name] = s
		if err = validateServer(name, s); err != nil {
			return p, err
		}
	}
	return p, nil
}
func validateServer(n string, s server) error {
	if n == "local" || !config.ValidName(n) || sshx.CheckAlias(s.SSHAlias) != nil || s.RemoteBinary == "" || strings.HasPrefix(s.RemoteBinary, "-") || !config.SafeText(s.RemoteBinary) || !config.SafeText(s.RemoteConfigDir) || !config.SafeText(s.SSHConfig) {
		return config.Invalid("invalid server name, SSH alias or remote binary")
	}
	for name, override := range s.Projects {
		if !config.ValidName(name) || override.ProjectID == "" {
			return config.Invalid("invalid local project override")
		}
	}
	return nil
}
func (a *app) serverTarget(p preferences, n, project string) (target, error) {
	if n == "local" {
		overrides, err := a.readOverrides()
		if err != nil {
			return target{}, err
		}
		return target{Name: "local", Project: project, Local: true, Server: server{Projects: overrides.Servers["local"]}, SSH: p.SSH, Reconnect: p.Reconnect}, nil
	}
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
	return a.resolveTarget(ctx, arg, confirm, false)
}
func (a *app) resolveOpen(ctx context.Context, arg string) (target, error) {
	return a.resolveTarget(ctx, arg, false, true)
}
func (a *app) resolveTarget(ctx context.Context, arg string, confirm, includeScratch bool) (target, error) {
	prefs, err := a.prefs()
	if err != nil {
		return target{}, err
	}
	if arg == "" {
		p, err := a.manager().Context(ctx)
		if err != nil {
			return target{}, err
		}
		if confirm {
			if err = a.confirm(fmt.Sprintf("Detected project local/%s. Continue?", p.Name)); err != nil {
				return target{}, err
			}
		}
		return a.serverTarget(prefs, "local", p.Name)
	}
	if strings.Contains(arg, "/") {
		parts := strings.Split(arg, "/")
		if len(parts) != 2 || !config.ValidName(parts[0]) || !config.ValidName(parts[1]) {
			return target{}, config.Invalid("expected server/project")
		}
		return a.serverTarget(prefs, parts[0], parts[1])
	}
	if !config.ValidName(arg) {
		return target{}, config.Invalid("invalid project")
	}
	if _, err := a.store.Get(arg); err == nil {
		return a.serverTarget(prefs, "local", arg)
	} else if protocol.Normalize(err).Code != "PROJECT_NOT_FOUND" {
		return target{}, err
	}
	if includeScratch {
		local, _ := a.serverTarget(prefs, "local", arg)
		scratch, err := a.findScratch(ctx, local)
		if err != nil {
			return target{}, err
		}
		if scratch != nil {
			local.Scratch = true
			return local, nil
		}
	}
	names := orderedServers(prefs)
	// Resolve known names from the complete cached index before opening any
	// connection. A missing cache for another server does not delay a known target.
	for _, name := range names {
		t, _ := a.serverTarget(prefs, name, arg)
		if cached, ok := a.loadListingCache(t); ok {
			if includeScratch {
				for _, scratch := range cached.Scratches {
					if scratch.Name == arg {
						t.Scratch = true
						return t, nil
					}
				}
			}
			for _, p := range cached.Projects {
				if p.Project.Name == arg {
					if !a.json && a.errOut != nil {
						fmt.Fprintf(a.errOut, "Project: %s/%s\n", name, arg)
					}
					return t, nil
				}
			}
		}
	}
	// Resolve from definitions alone: unrelated session state must not block
	// project operations such as downloading an updated client from an old server.
	for _, name := range names {
		t, _ := a.serverTarget(prefs, name, arg)
		lookup := t
		lookup.Batch = true
		_, err := a.get(ctx, lookup)
		if err != nil {
			if protocol.Normalize(err).Code == "PROJECT_NOT_FOUND" {
				if includeScratch {
					scratch, e := a.findScratch(ctx, lookup)
					if e != nil {
						return target{}, e
					}
					if scratch != nil {
						t.Scratch = true
						return t, nil
					}
				}
				continue
			}
			return target{}, err
		}
		if !a.json && a.errOut != nil {
			fmt.Fprintf(a.errOut, "Project: %s/%s\n", name, arg)
		}
		return t, nil
	}
	return a.serverTarget(prefs, "local", arg)
}

func (a *app) confirm(message string) error {
	yes, _ := a.root.Flags().GetBool("yes")
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
func exitForCode(code string) int {
	switch code {
	case "PROJECT_NOT_FOUND":
		return 3
	case "CONFIG_CONFLICT", "SESSION_CONFLICT", "SESSION_UNMANAGED", "MOUNT_CONFLICT", "DESTINATION_CHANGED", "SOURCE_CHANGED":
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
	b, err := a.call(ctx, t, nil, "project", "get", t.Project)
	if err == nil {
		err = json.Unmarshal(b, &p)
	}
	if err == nil {
		err = p.Validate()
	}
	return p, err
}
func (a *app) client() {
	a.root.Flags().BoolP("yes", "y", false, "accept project, overwrite and installation confirmations")
	a.root.Flags().StringVar(&a.sshConfig, "ssh-config", "", "alternative OpenSSH configuration (also used by scp/SSHFS)")
	a.setupCommand()
	a.workspaceCommands()
	a.forwardCommand()
	a.transferCommands()
	a.mountCommands()
	a.updateCommands()
	a.overrideCommand()
	a.projectCommands()
}
func (a *app) setupCommand() {
	c := &cobra.Command{Use: "setup", Short: "Discover servers and remote projects with a connection wizard", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		wizard, _ := c.Flags().GetBool("interactive")
		alias, _ := c.Flags().GetString("ssh-alias")
		host, _ := c.Flags().GetString("host")
		wizard = wizard || (alias == "" && host == "")
		result, err := a.runSetup(c.Context(), c)
		if err == nil && wizard && !a.json {
			return nil
		}
		return a.result(result, err)
	}}
	a.root.AddCommand(c)
	c.Flags().String("name", "", "local server name")
	c.Flags().String("ssh-alias", "", "existing SSH alias")
	c.Flags().String("remote-binary", ".local/bin/tdev", "absolute or home-relative remote binary")
	c.Flags().String("remote-config-dir", "", "optional remote configuration root")
	c.Flags().Bool("install", false, "install a verified release on the remote before registration")
	c.Flags().Bool("interactive", false, "use the connection wizard even when connection flags are provided")
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
