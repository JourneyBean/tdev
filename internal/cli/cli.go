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

var Version = "0.0.2"

type app struct {
	root        *cobra.Command
	store       config.Store
	json        bool
	timeout     time.Duration
	out, errOut io.Writer
	in          io.Reader
	reader      *bufio.Reader
	sshConfig   string
}

func Execute(args []string, in io.Reader, out, errOut io.Writer) int {
	a := &app{store: config.Store{Root: config.DefaultRoot()}, in: in, out: out, errOut: errOut}
	r := &cobra.Command{Use: "tdev", Short: "Project workspaces over SSH and tmux", SilenceUsage: true, SilenceErrors: true}
	a.root = r
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	r.SetContext(ctx)
	r.SetIn(in)
	r.SetOut(out)
	r.SetErr(errOut)
	r.PersistentFlags().BoolVar(&a.json, "json", false, "write a protocol JSON response")
	r.PersistentFlags().StringVar(&a.store.Root, "config-dir", a.store.Root, "configuration root on this machine")
	r.PersistentFlags().DurationVar(&a.timeout, "timeout", 30*time.Second, "local/control operation timeout")
	r.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		return a.result(versionData(), nil)
	}})
	a.local()
	a.client()
	a.pathCommand()
	r.SetArgs(args)
	err := r.Execute()
	if err == nil {
		return 0
	}
	if ctx.Err() != nil {
		err = protocol.Fail("CANCELLED", 130, "cancelled")
	}
	e := protocol.Normalize(err)
	if e.Code == "RUNTIME_ERROR" && (strings.HasPrefix(e.Message, "unknown command") || strings.Contains(e.Message, "unknown flag") || strings.Contains(e.Message, "arg(s)") || strings.Contains(e.Message, "required flag")) {
		e.Code = "INVALID_ARGUMENT"
		e.Exit = 2
	}
	if a.json {
		_ = protocol.Write(out, nil, e)
	} else {
		fmt.Fprintln(errOut, "tdev:", e.Message)
	}
	return e.Exit
}
func (a *app) result(data any, err error) error {
	if err != nil {
		return err
	}
	if a.json {
		return protocol.Write(a.out, data, nil)
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
		ctx, cancel := context.WithTimeout(c.Context(), a.timeout)
		defer cancel()
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
	c := &cobra.Command{Use: name, Short: short}
	parent.AddCommand(c)
	return c
}
func (a *app) local() {
	l := group(a.root, "local", "Noninteractive operations on this machine; never connects over SSH")
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
		d, _ := c.Flags().GetString("directory")
		return a.store.Create(ctx, args[0], d)
	})
	create.Flags().String("directory", "", "existing project root")
	_ = create.MarkFlagRequired("directory")
	cfg := group(l, "config", "Read and atomically replace project definitions")
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
			return nil, config.Invalid("config set requires --expected-revision")
		}
		if p.Revision != expected(c) {
			return nil, config.Invalid("input revision must equal expected revision")
		}
		return a.store.Update(ctx, args[0], expected(c), func(dst *config.Project) error { *dst = p; return nil })
	})
	set.Flags().Bool("stdin", false, "read replacement from stdin")
	revision(set)
	w := group(l, "window", "Edit saved window definitions, without changing running sessions")
	add := a.command(w, "add <project>", "Add a saved window", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		name, _ := c.Flags().GetString("name")
		cmd, _ := c.Flags().GetString("command")
		dir, _ := c.Flags().GetString("directory")
		id, _ := c.Flags().GetString("id")
		if id == "" {
			id = "w" + strings.ReplaceAll(config.NewID(), "-", "")
		}
		return a.store.Update(ctx, args[0], expected(c), func(p *config.Project) error {
			p.Windows = append(p.Windows, config.Window{ID: id, Name: name, Command: cmd, Directory: dir})
			return nil
		})
	})
	add.Flags().String("name", "", "window name")
	_ = add.MarkFlagRequired("name")
	add.Flags().String("id", "", "stable window ID (generated if omitted)")
	add.Flags().String("command", "", "explicit shell startup command")
	add.Flags().String("directory", ".", "absolute path or path relative to project")
	revision(add)
	update := a.command(w, "update <project>", "Replace a window definition from stdin", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		id, _ := c.Flags().GetString("id")
		stdin, _ := c.Flags().GetBool("stdin")
		if !stdin {
			return nil, config.Invalid("--stdin is required")
		}
		var win config.Window
		if err := a.read(&win); err != nil {
			return nil, err
		}
		if win.ID != id {
			return nil, config.Invalid("window ID cannot change")
		}
		return a.store.Update(ctx, args[0], expected(c), func(p *config.Project) error {
			for i := range p.Windows {
				if p.Windows[i].ID == id {
					p.Windows[i] = win
					return nil
				}
			}
			return protocol.Fail("WINDOW_NOT_FOUND", 2, "window %q not found", id)
		})
	})
	update.Flags().String("id", "", "window ID")
	_ = update.MarkFlagRequired("id")
	update.Flags().Bool("stdin", false, "read YAML/JSON window")
	revision(update)
	remove := a.command(w, "remove <project>", "Remove a saved window", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		id, _ := c.Flags().GetString("id")
		return a.store.Update(ctx, args[0], expected(c), func(p *config.Project) error {
			for i, w := range p.Windows {
				if w.ID == id {
					p.Windows = append(p.Windows[:i], p.Windows[i+1:]...)
					return nil
				}
			}
			return protocol.Fail("WINDOW_NOT_FOUND", 2, "window %q not found", id)
		})
	})
	remove.Flags().String("id", "", "window ID")
	_ = remove.MarkFlagRequired("id")
	revision(remove)
	f := group(l, "forward", "Edit saved TCP forwarding definitions")
	fa := a.command(f, "add <project>", "Save one or more L/R forwarding definitions", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		fs, err := readForwards(c)
		if err != nil {
			return nil, err
		}
		return a.store.Update(ctx, args[0], expected(c), func(p *config.Project) error { appendForwards(p, fs); return nil })
	})
	forwardFlags(fa)
	revision(fa)
	fr := a.command(f, "remove <project>", "Remove a saved forwarding definition", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		id, _ := c.Flags().GetString("id")
		return a.store.Update(ctx, args[0], expected(c), func(p *config.Project) error {
			for i, f := range p.Forwards {
				if f.ID == id {
					p.Forwards = append(p.Forwards[:i], p.Forwards[i+1:]...)
					return nil
				}
			}
			return protocol.Fail("FORWARD_NOT_FOUND", 2, "forward %q not found", id)
		})
	})
	fr.Flags().String("id", "", "forward ID")
	_ = fr.MarkFlagRequired("id")
	revision(fr)
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
	a.command(l, "context", "Read the current managed tmux project", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) { return a.manager().Context(ctx) })
	a.command(l, "doctor", "Check local tmux, shell and configuration location", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		data, err := a.manager().Doctor(ctx)
		return map[string]any{"version": versionData(), "runtime": data}, err
	})
	a.localExtras(l, s)
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
