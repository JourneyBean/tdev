package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/reconnect"
)

var hostRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// This private SSH config includes the user's existing config. It never edits ~/.ssh/config or keys.
func (a *app) newConnection(c *cobra.Command, s server) (server, func(), error) {
	host, _ := c.Flags().GetString("host")
	if host == "" {
		return s, func() {}, nil
	}
	port, _ := c.Flags().GetInt("port")
	user, _ := c.Flags().GetString("user")
	identity, _ := c.Flags().GetString("identity-file")
	if (net.ParseIP(strings.Trim(host, "[]")) == nil && !hostRE.MatchString(host)) || port < 1 || port > 65535 || !config.SafeText(user) || strings.ContainsAny(user, " \t") || strings.HasPrefix(user, "-") {
		return s, nil, config.Invalid("invalid SSH host, port or user")
	}
	if identity != "" {
		if strings.HasPrefix(identity, "~/") {
			home, _ := os.UserHomeDir()
			identity = filepath.Join(home, identity[2:])
		}
		var err error
		identity, err = filepath.Abs(identity)
		if err != nil {
			return s, nil, err
		}
		info, err := os.Stat(identity)
		if err != nil || !info.Mode().IsRegular() {
			return s, nil, config.Invalid("identity file must already exist (it is never read or copied by tdev)")
		}
	}
	text := fmt.Sprintf("Host %s\n  HostName %s\n  Port %d\n", s.SSHAlias, strconv.Quote(strings.Trim(host, "[]")), port)
	if user != "" {
		text += "  User " + strconv.Quote(user) + "\n"
	}
	if identity != "" {
		text += "  IdentityFile " + strconv.Quote(identity) + "\n  IdentitiesOnly yes\n"
	}
	base := a.sshConfig
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".ssh", "config")
	}
	text += "\nInclude " + strconv.Quote(base) + "\n"
	dir := filepath.Join(a.store.Root, "ssh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return s, nil, err
	}
	f, err := os.CreateTemp(dir, "connection-*.conf")
	if err != nil {
		return s, nil, err
	}
	path := f.Name()
	_ = f.Close()
	if err = config.AtomicWrite(path, []byte(text), 0600); err != nil {
		os.Remove(path)
		return s, nil, err
	}
	s.SSHConfig = path
	return s, func() { _ = os.Remove(path) }, nil
}
func setupConnectionFlags(c *cobra.Command) {
	c.Flags().String("host", "", "create a private SSH profile for this hostname/IP instead of an existing alias")
	c.Flags().Int("port", 22, "SSH port for --host")
	c.Flags().String("user", "", "SSH user for --host (defaults to OpenSSH/user config)")
	c.Flags().String("identity-file", "", "existing identity file reference for --host; otherwise use SSH agent/defaults")
	for _, kind := range []string{"terminal", "forward"} {
		c.Flags().String(kind+"-reconnect-attempts", "", "set global "+kind+" attempts (nonnegative integer or always)")
		c.Flags().Duration(kind+"-reconnect-interval", 0, "set global "+kind+" retry interval")
	}
}
func applySetupPolicies(c *cobra.Command, p *preferences) error {
	for _, item := range []struct {
		name   string
		policy *reconnect.Policy
	}{{"terminal", &p.Reconnect.Terminal}, {"forward", &p.Reconnect.Forward}} {
		s, _ := c.Flags().GetString(item.name + "-reconnect-attempts")
		if s != "" {
			n, err := reconnect.ParseAttempts(s)
			if err != nil {
				return config.Invalid("%v", err)
			}
			item.policy.Attempts = reconnect.Attempts(n)
		}
		if c.Flags().Changed(item.name + "-reconnect-interval") {
			item.policy.Interval, _ = c.Flags().GetDuration(item.name + "-reconnect-interval")
		}
		if err := item.policy.Validate(); err != nil {
			return err
		}
	}
	return nil
}
