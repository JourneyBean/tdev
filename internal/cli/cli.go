package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/tmux"
)

var Version = "0.5.0"

type app struct {
	root         *cobra.Command
	store        config.Store
	json         bool
	debug        bool
	timeout      time.Duration
	out, errOut  io.Writer
	in           io.Reader
	reader       *bufio.Reader
	sshConfig    string
	useDaemon    bool
	daemonWorker bool
}

func Execute(args []string, in io.Reader, out, errOut io.Writer) int {
	return executeContext(context.Background(), args, in, out, errOut)
}

func executeContext(parent context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	a := &app{store: config.Store{Root: config.DefaultRoot()}, in: in, out: out, errOut: errOut, useDaemon: true}
	r := &cobra.Command{Use: "tdev", Short: "Project workspaces over SSH and tmux", Long: "tdev provides user commands for project workspaces over SSH and tmux.\nUse tdev api for the machine interface invoked by clients on the target host.", TraverseChildren: true, SilenceUsage: true, SilenceErrors: true}
	r.PersistentPreRunE = func(*cobra.Command, []string) error {
		if a.timeout <= 0 {
			return config.Invalid("timeout must be positive")
		}
		return nil
	}
	r.Args = cobra.NoArgs
	r.RunE = func(c *cobra.Command, _ []string) error { return c.Help() }
	a.root = r
	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	r.SetContext(ctx)
	r.SetIn(in)
	r.SetOut(out)
	r.SetErr(errOut)
	r.PersistentFlags().BoolVar(&a.json, "json", false, "write a protocol JSON response")
	r.PersistentFlags().BoolVar(&a.debug, "debug", false, "record successful operations and timings in private diagnostic logs")
	r.PersistentFlags().StringVar(&a.store.Root, "config-dir", a.store.Root, "configuration root on this machine")
	r.PersistentFlags().DurationVar(&a.timeout, "timeout", 30*time.Second, "local/control operation timeout")
	r.AddCommand(&cobra.Command{Use: "version", Short: "Show program, protocol and configuration versions", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return a.result(versionData(), nil)
	}})
	a.api()
	a.client()
	a.daemonCommands()
	a.psCommand()
	a.pathCommand()
	a.completionCommands()
	a.commandHelp()
	r.SetArgs(args)
	started := time.Now()
	executed, err := r.ExecuteC()
	if err != nil {
		if ctx.Err() != nil {
			err = protocol.Fail("CANCELLED", 130, "cancelled")
		}
		e := protocol.Normalize(err)
		if e.Code == "RUNTIME_ERROR" && (strings.HasPrefix(e.Message, "unknown command") || strings.Contains(e.Message, "unknown flag") || strings.Contains(e.Message, "arg(s)") || strings.Contains(e.Message, "required flag")) {
			e.Code = "INVALID_ARGUMENT"
			e.Exit = 2
		}
		err = e
	}
	operation := r.CommandPath()
	if executed != nil {
		operation = executed.CommandPath()
	}
	logPath, logErr := a.recordDiagnostic(operation, "", started, err, nil)
	if !a.json && a.errOut != nil {
		if logErr != nil {
			fmt.Fprintln(a.errOut, "Diagnostic log unavailable:", diagnosticReason(logErr))
		} else if logPath != "" {
			fmt.Fprintln(a.errOut, "Diagnostic log:", logPath)
		}
	}
	if err == nil {
		return 0
	}
	e := protocol.Normalize(err)
	if a.json {
		_ = protocol.Write(out, nil, e)
	} else {
		fmt.Fprintln(errOut, "tdev:", e.Message)
	}
	return e.Exit
}
func (a *app) result(data any, err error) error {
	if err != nil {
		if listing, ok := data.(projectListing); ok && !a.json {
			_ = listing.print(a.out)
		}
		return err
	}
	if a.json {
		return protocol.Write(a.out, data, nil)
	}
	if listing, ok := data.(projectListing); ok {
		return listing.print(a.out)
	}
	if report, ok := data.(doctorReport); ok {
		return report.print(a.out)
	}
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(data)
}
func (a *app) manager() tmux.Manager {
	return tmux.Manager{Store: a.store, Socket: os.Getenv("TDEV_TMUX_SOCKET")}
}
func (a *app) command(parent *cobra.Command, use, short string, args cobra.PositionalArgs, fn func(context.Context, *cobra.Command, []string) (any, error)) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short, Args: args, RunE: func(c *cobra.Command, args []string) error {
		if a.timeout <= 0 {
			return config.Invalid("timeout must be positive")
		}
		ctx := c.Context()
		if a.json || !terminalAvailable(a.in) {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, a.timeout)
			defer cancel()
		}
		data, err := fn(ctx, c, args)
		return a.result(data, err)
	}}
	parent.AddCommand(c)
	return c
}
func expected(c *cobra.Command) int { n, _ := c.Flags().GetInt("expected-revision"); return n }
func revision(c *cobra.Command) {
	c.Flags().Int("expected-revision", 0, "reject if current revision differs (0 reads latest under lock)")
}
func (a *app) read(dst any) error {
	b, err := io.ReadAll(io.LimitReader(a.in, 4*1024*1024+1))
	if err != nil {
		return err
	}
	if len(b) > 4*1024*1024 {
		return config.Invalid("request exceeds 4 MiB")
	}
	return config.Decode(b, dst)
}
func group(parent *cobra.Command, name, short string) *cobra.Command {
	c := &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	parent.AddCommand(c)
	return c
}
func (a *app) api() {
	l := group(a.root, "api", "Client/server machine interface on the target host")
	l.Long = "Machine interface invoked by tdev clients over SSH or on this host.\nOperations use explicit arguments and structured stdin, without confirmation prompts.\nUse --json for protocol responses. These commands operate on this host.\nThe session attach transport requires a terminal and explicit attachment identity."
	l.PersistentPreRunE = func(*cobra.Command, []string) error {
		if a.timeout <= 0 {
			return config.Invalid("timeout must be positive")
		}
		for _, name := range []string{"yes", "ssh-config"} {
			if a.root.Flags().Changed(name) {
				return protocol.Fail("INVALID_ARGUMENT", 2, "--%s is a user command option", name)
			}
		}
		return nil
	}
	p := group(l, "project", "Manage saved projects")
	list := a.command(p, "list", "List saved projects", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		ps, err := a.store.List()
		if err != nil {
			return nil, err
		}
		status, _ := c.Flags().GetBool("status")
		if !status {
			return ps, nil
		}
		out := []any{}
		for _, p := range ps {
			s, err := a.manager().Status(ctx, p)
			if err != nil {
				return nil, err
			}
			out = append(out, map[string]any{"project": p, "session": s})
		}
		return out, nil
	})
	list.Flags().Bool("status", false, "include actual tmux state")
	create := a.command(p, "create <project>", "Create a saved project", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		stdin, _ := c.Flags().GetBool("stdin")
		if stdin {
			var p config.Project
			if err := a.read(&p); err != nil {
				return nil, err
			}
			if p.Name != args[0] {
				return nil, config.Invalid("project name does not match request")
			}
			return a.store.CreateDefinition(ctx, p)
		}
		d, _ := c.Flags().GetString("directory")
		if d == "" {
			return nil, config.Invalid("--directory or --stdin is required")
		}
		return a.store.Create(ctx, args[0], d)
	})
	create.Flags().String("directory", "", "existing project root")
	create.Flags().Bool("stdin", false, "read a complete project draft")
	cfg := p
	a.command(cfg, "get <project>", "Read a validated project", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) { return a.store.Get(args[0]) })
	set := a.command(cfg, "set <project>", "Replace a project from YAML/JSON stdin", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		stdin, _ := c.Flags().GetBool("stdin")
		if !stdin {
			return nil, config.Invalid("--stdin is required")
		}
		var p config.Project
		if err := a.read(&p); err != nil {
			return nil, err
		}
		if expected(c) < 1 {
			return nil, config.Invalid("project set requires --expected-revision")
		}
		if p.Revision != expected(c) {
			return nil, config.Invalid("input revision must equal expected revision")
		}
		return a.store.Update(ctx, args[0], expected(c), func(dst *config.Project) error { *dst = p; return nil })
	})
	set.Flags().Bool("stdin", false, "read replacement from stdin")
	revision(set)
	s := group(l, "session", "Manage project tmux sessions")
	for _, op := range []string{"ensure", "status", "stop", "save", "add-window"} {
		op := op
		c := a.command(s, op+" <project>", op+" the project session", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
			p, err := a.store.Get(args[0])
			if err != nil {
				return nil, err
			}
			m := a.manager()
			switch op {
			case "ensure":
				return m.Ensure(ctx, p)
			case "status":
				return m.Status(ctx, p)
			case "stop":
				return m.Stop(ctx, p)
			case "save":
				if err = m.CheckSaveContext(ctx, p); err != nil {
					return nil, err
				}
				return m.Save(ctx, p, expected(c))
			default:
				name, _ := c.Flags().GetString("name")
				temp, _ := c.Flags().GetBool("temporary")
				return m.AddWindow(ctx, p, name, !temp)
			}
		})
		if op == "save" {
			revision(c)
		}
		if op == "add-window" {
			c.Flags().String("name", "shell", "window name")
			c.Flags().Bool("temporary", false, "exclude from future save")
		}
	}
	a.command(s, "context", "Read the current managed tmux project", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) { return a.manager().Context(ctx) })
	a.command(l, "doctor", "Check local tmux, shell and configuration location", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		data, err := a.manager().Doctor(ctx)
		return map[string]any{"version": versionData(), "runtime": data}, err
	})
	a.apiExtras(l, s)
	var apiGroups func(*cobra.Command)
	apiGroups = func(c *cobra.Command) {
		if c.HasSubCommands() {
			c.RunE = func(c *cobra.Command, _ []string) error {
				if a.json {
					return protocol.Fail("INVALID_ARGUMENT", 2, "specify an API operation")
				}
				return c.Help()
			}
		}
		for _, child := range c.Commands() {
			apiGroups(child)
		}
	}
	apiGroups(l)
}
func forwardFlags(c *cobra.Command) {
	c.Flags().StringArrayP("local", "L", nil, "TCP local forward: [bind:]port:host:port, port:port, or port")
	c.Flags().StringArrayP("reverse", "R", nil, "TCP reverse forward (OpenSSH ordering)")
}
func readForwards(c *cobra.Command) ([]config.Forward, error) {
	var out []config.Forward
	for _, entry := range [][2]string{{"local", "L"}, {"reverse", "R"}} {
		values, _ := c.Flags().GetStringArray(entry[0])
		for _, v := range values {
			f, err := config.ParseForward(entry[1], v)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, config.Invalid("at least one -L or -R is required")
	}
	return out, nil
}
func appendForwards(p *config.Project, fs []config.Forward) {
	for _, f := range fs {
		exists := false
		for _, old := range p.Forwards {
			if old.Direction == f.Direction && old.Spec() == f.Spec() {
				exists = true
			}
		}
		if !exists {
			p.Forwards = append(p.Forwards, f)
		}
	}
}
