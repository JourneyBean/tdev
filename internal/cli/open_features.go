package cli

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/daemon"
)

type openFeatures struct{ shell, forward, sshfs, web bool }

func completeOpenFeatures(c *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	leading, leaf := "", prefix
	selected, _ := c.Flags().GetStringSlice("only")
	if comma := strings.LastIndex(prefix, ","); comma >= 0 {
		leading, leaf = prefix[:comma+1], prefix[comma+1:]
		selected = append(selected, strings.Split(prefix[:comma], ",")...)
	}
	used := map[string]bool{}
	for _, value := range selected {
		used[value] = true
	}
	var values []string
	for _, value := range []string{"shell", "forward", "sshfs", "web"} {
		if !used[value] && strings.HasPrefix(value, leaf) {
			values = append(values, leading+value)
		}
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}

func (a *app) restoreOpenServices(t target, p config.Project, f openFeatures) func(context.Context, string) error {
	return func(ctx context.Context, owner string) error {
		t.Lease = owner
		if f.sshfs && !t.Local {
			prefs, err := a.prefs()
			if err != nil {
				return err
			}
			current, err := a.serverTarget(prefs, t.Name, t.Project)
			if err != nil {
				return err
			}
			path := current.Server.Projects[p.Name].MountPath
			if path == "" {
				return config.Invalid("mount path missing during service recovery")
			}
			if _, err = a.mountProject(ctx, t, p, path); err != nil {
				return err
			}
		}
		if f.forward {
			_, err := a.daemonForwards(ctx, t, p.Forwards)
			return err
		}
		return nil
	}
}

func features(c *cobra.Command) (openFeatures, error) {
	if !c.Flags().Changed("only") {
		mount, _ := c.Flags().GetBool("mount")
		return openFeatures{shell: true, forward: true, sshfs: mount, web: true}, nil
	}
	if c.Flags().Changed("mount") {
		return openFeatures{}, config.Invalid("use --only shell,sshfs instead of combining --only and --mount")
	}
	values, _ := c.Flags().GetStringSlice("only")
	if len(values) == 0 {
		return openFeatures{}, config.Invalid("--only requires shell, forward, sshfs or web")
	}
	var f openFeatures
	for _, value := range values {
		switch strings.TrimSpace(value) {
		case "shell":
			f.shell = true
		case "forward":
			f.forward = true
		case "sshfs":
			f.sshfs = true
		case "web":
			f.web = true
		default:
			return openFeatures{}, config.Invalid("unknown open feature %q; use shell,forward,sshfs,web", value)
		}
	}
	return f, nil
}

func (a *app) daemonForwards(ctx context.Context, t target, fs []config.Forward) (any, error) {
	fs = automaticForwards(t, fs)
	if len(fs) == 0 {
		return map[string]string{"status": "no-forwards"}, nil
	}
	var err error
	t, err = daemonTarget(t)
	if err != nil {
		return nil, err
	}
	client, err := a.daemonClient(ctx)
	if err != nil {
		return nil, err
	}
	refs := []daemon.ResourceRef{}
	for _, f := range fs {
		refs = append(refs, daemon.ResourceRef{Server: t.Name, Name: t.Project, Kind: "forward", Detail: f.ID})
	}
	return client.Call(ctx, daemon.Request{Resources: refs, Op: "forward", Alias: t.Server.SSHAlias, SSH: t.SSH, Local: t.Local, Forwards: fs, Policy: t.Reconnect.Forward, Owner: t.Lease})
}

func (a *app) openServices(ctx context.Context, t target, f openFeatures) (any, error) {
	p, err := a.get(ctx, t)
	if err != nil {
		return nil, err
	}
	p, err = a.effective(t, p)
	if err != nil {
		return nil, err
	}
	result := map[string]any{}
	if f.web {
		result["web"], err = a.daemonWeb(ctx, t, p)
		if err != nil {
			return nil, err
		}
	}
	if f.sshfs {
		result["sshfs"], err = a.mountProject(ctx, t, p, "")
		if err != nil {
			return nil, err
		}
	}
	if f.forward {
		result["forward"], err = a.daemonForwards(ctx, t, p.Forwards)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}
