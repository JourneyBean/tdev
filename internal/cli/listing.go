package cli

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
)

func (a *app) listProjects(ctx context.Context, c *cobra.Command) (any, error) {
	p, err := a.prefs()
	if err != nil {
		return nil, err
	}
	all, _ := c.Flags().GetBool("all")
	selected, _ := c.Flags().GetString("server")
	if all && selected != "" {
		return nil, config.Invalid("choose --server or --all")
	}
	if selected == "" {
		selected = p.DefaultServer
	}
	if !all {
		if _, err = a.serverTarget(p, selected, ""); err != nil {
			return nil, err
		}
	}
	results := projectListing{}
	failures := map[string]string{}
	for name := range p.Servers {
		if !all && name != selected {
			continue
		}
		t, _ := a.serverTarget(p, name, "")
		data, err := a.discover(ctx, t)
		if err != nil {
			failures[name] = err.Error()
		} else {
			results[name] = data
		}
	}
	if len(failures) > 0 {
		return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "some servers could not be listed", Details: map[string]any{"projects": results, "errors": failures}}
	}
	return results, nil
}

type projectListing map[string][]discoveredProject

func (listing projectListing) print(out io.Writer) error {
	servers := make([]string, 0, len(listing))
	for name := range listing {
		servers = append(servers, name)
	}
	sort.Strings(servers)
	for _, name := range servers {
		projects := listing[name]
		if _, err := fmt.Fprintf(out, "%s · %d 个项目\n", name, len(projects)); err != nil {
			return err
		}
		if len(projects) == 0 {
			if _, err := fmt.Fprintf(out, "  添加项目：tdev create --server %s\n", name); err != nil {
				return err
			}
		}
		for _, project := range projects {
			if _, err := fmt.Fprintf(out, "  %s/%s  [%s]\n    %s\n", name, project.Project.Name, project.Session.Status, project.Project.Directory); err != nil {
				return err
			}
			for _, f := range project.Project.Forwards {
				if _, err := fmt.Fprintf(out, "    %s  -%s %s\n", f.ID, f.Direction, f.Spec()); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
