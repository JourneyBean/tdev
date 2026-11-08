package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/tmux"
)

func (a *app) workspaceCommands() {
	a.scratchCommand()
	ls := a.command(a.root, "ls", "List projects and scratch sessions across configured servers", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return a.listProjects(ctx, c)
	})
	ls.Flags().String("type", "", "filter by project or scratch")
	ls.Flags().Bool("all", false, "query every configured server (the default)")
	ls.Flags().String("server", "", "only list this saved server")
	for _, op := range []string{"save", "stop", "sh"} {
		op := op
		c := a.command(a.root, op+" [project]", "Run project "+op, cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			t, err := a.resolve(ctx, arg, true)
			if err != nil {
				return nil, err
			}
			if op == "stop" && arg != "" {
				if err = a.confirm(fmt.Sprintf("Stop %s and its foreground processes, disconnecting other clients?", arg)); err != nil {
					return nil, err
				}
			}
			p := config.Project{Name: t.Project}
			if op == "save" {
				p, err = a.get(ctx, t)
				if err != nil {
					return nil, err
				}
			}
			if op == "sh" {
				if !a.json && !terminalAvailable(a.in) {
					return nil, config.Invalid("sh requires a terminal; --json creates a window without attaching")
				}
				save, _ := c.Flags().GetBool("save")
				name, _ := c.Flags().GetString("name")
				argv := []string{"session", "add-window", p.Name, "--name", name}
				if !save {
					argv = append(argv, "--temporary")
				}
				operations := [][]string{argv}
				if !a.json && !(t.Local && os.Getenv("TMUX") != "") {
					operations = append(operations, []string{"session", "status", p.Name})
				}
				data, err := a.calls(ctx, t, nil, operations...)
				if err != nil {
					return nil, err
				}
				var w map[string]string
				if err = json.Unmarshal(data[0], &w); err != nil {
					return nil, err
				}
				if t.Local && !a.json && os.Getenv("TMUX") != "" {
					return w, a.manager().Select(ctx, w["window_id"])
				}
				if a.json {
					return w, nil
				}
				var s tmux.Status
				if err = json.Unmarshal(data[1], &s); err != nil {
					return nil, err
				}
				policy, err := commandPolicy(c, t.Reconnect.Terminal)
				if err != nil {
					return nil, err
				}
				return nil, a.attach(c.Context(), t, s, w["window_id"], &reconnect.Budget{Policy: policy})
			}
			argv := []string{"session", op, p.Name}
			if op == "save" {
				argv = append(argv, "--expected-revision", strconv.Itoa(p.Revision))
			}
			return a.call(ctx, t, nil, argv...)
		})
		if op == "sh" {
			c.Flags().Bool("save", false, "persist the new shell definition")
			c.Flags().String("name", "shell", "name of the new tmux window")
			retryFlags(c)
		}
	}
	open := &cobra.Command{Use: "open <name>", Short: "Open a project workspace or an existing scratch session", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if a.json || !terminalAvailable(a.in) {
			return config.Invalid("open requires an interactive terminal")
		}
		t, err := a.resolveOpen(c.Context(), args[0])
		if err != nil {
			return err
		}
		policy, err := commandPolicy(c, t.Reconnect.Terminal)
		if err != nil {
			return err
		}
		budget := &reconnect.Budget{Policy: policy}
		var p config.Project
		var s tmux.Status
		for {
			{
				p, err = a.get(c.Context(), t)
				if err == nil || protocol.Normalize(err).Code == "PROJECT_NOT_FOUND" {
					scratch, scratchErr := a.findScratch(c.Context(), t)
					if scratchErr != nil {
						err = scratchErr
					} else if scratch != nil {
						if err == nil {
							return protocol.Fail("SESSION_CONFLICT", 4, "%s/%s matches both a project and a scratch; rename one before opening", t.Name, t.Project)
						}
						t.Scratch = true
						s = scratch.Status
						break
					} else if t.Scratch {
						return protocol.Fail("SESSION_NOT_FOUND", 5, "scratch session ended; refresh with tdev ls")
					}
				}

				if err != nil && protocol.Normalize(err).Code == "PROJECT_NOT_FOUND" {
					fmt.Fprintf(a.errOut, "服务器 %s 上还没有项目 %s。\n", t.Name, t.Project)
					create, promptErr := a.wizardYes(c.Context(), "现在配置并打开这个项目？", true)
					if promptErr != nil {
						return promptErr
					}
					if !create {
						return err
					}
					p, err = a.createRemoteProject(c.Context(), t, "", true)
					if err == nil {
						prefs, e := a.prefs()
						if e != nil {
							return e
						}
						t, e = a.serverTarget(prefs, t.Name, p.Name)
						if e != nil {
							return e
						}
						fmt.Fprintln(a.errOut, "正在打开工作区…")
					}
				}
			}
			if err == nil {
				p, err = a.effective(t, p)
			}
			if err == nil {
				var data json.RawMessage
				data, err = a.call(c.Context(), t, nil, "session", "ensure", p.Name)
				if err == nil {
					err = json.Unmarshal(data, &s)
				}
			}
			if err == nil {
				break
			}
			if !reconnect.Retryable(err) {
				return err
			}
			fmt.Fprintln(a.errOut, err)
			if err = budget.Next(c.Context()); err != nil {
				return err
			}
			t.Batch = true
		}
		if t.Scratch {
			doMount, _ := c.Flags().GetBool("mount")
			if doMount {
				return config.Invalid("--mount requires a project")
			}
			if out, ok := a.out.(*os.File); ok && terminalAvailable(out) {
				if _, err = fmt.Fprintf(out, "\x1b]2;tdev: %s/%s\x07", t.Name, t.Project); err != nil {
					return err
				}
			}
			return a.attach(c.Context(), t, s, "", budget)
		}
		if s.Warning != "" {
			fmt.Fprintf(a.errOut, "warning: %s\n", s.Warning)
		}
		doMount, _ := c.Flags().GetBool("mount")
		if doMount {
			if _, err = a.mountProject(c.Context(), t, p, ""); err != nil {
				return err
			}
		}
		closeForwards, err := a.openForwards(c.Context(), t, p.Forwards)
		if err != nil {
			return err
		}
		defer closeForwards()
		if out, ok := a.out.(*os.File); ok && terminalAvailable(out) {
			if _, err = fmt.Fprintf(out, "\x1b]2;tdev: %s/%s\x07", t.Name, t.Project); err != nil {
				return err
			}
		}
		return a.attach(c.Context(), t, s, "", budget)
	}}
	a.root.AddCommand(open)
	retryFlags(open)
	open.Flags().Bool("mount", false, "mount the project directory before attaching")
	doctor := a.command(a.root, "doctor [project]", "Diagnose project warnings, versions and dependencies", cobra.MaximumNArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		local := a.clientDoctor()
		if len(args) == 0 {
			listing, err := a.refreshProjects(ctx, c, false)
			report := doctorReport{Client: local, Servers: listing}
			if err != nil {
				if listing == nil {
					report.checkError = protocol.Normalize(err)
				}
				if !a.json {
					if writeErr := a.result(report, nil); writeErr != nil {
						return nil, writeErr
					}
				}
				failure := protocol.Normalize(err)
				failure.Details = report
				return report, failure
			}
			return report, nil
		}
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		remote, err := a.call(ctx, t, nil, "doctor")
		if err != nil {
			return nil, err
		}
		projects, err := a.discover(ctx, t)
		if err != nil {
			return doctorReport{Client: local, Remote: remote, ProjectError: protocol.Normalize(err)}, nil
		}
		for _, project := range projects {
			if project.Project.Name == t.Project {
				return doctorReport{Client: local, Remote: remote, Project: &project, projectLabel: t.Name + "/" + t.Project}, nil
			}
		}
		return nil, protocol.Fail("PROJECT_NOT_FOUND", 3, "project %q does not exist", t.Project)
	})
	_ = doctor
}
