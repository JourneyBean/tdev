package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/tmux"
)

type serverListing struct {
	Location       string              `json:"location"`
	State          string              `json:"state"`
	UpdatedAt      time.Time           `json:"updated_at,omitempty"`
	Error          string              `json:"error,omitempty"`
	Projects       []discoveredProject `json:"projects"`
	Scratches      []tmux.Scratch      `json:"scratches"`
	ScratchWarning string              `json:"scratch_warning,omitempty"`
}
type projectListing map[string]serverListing

func orderedServers(p preferences) []string {
	names := make([]string, 0, len(p.Servers))
	for name := range p.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func listingNames(listing projectListing) []string {
	names := make([]string, 0, len(listing))
	for name := range listing {
		if name != "local" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if _, ok := listing["local"]; ok {
		names = append([]string{"local"}, names...)
	}
	return names
}

func (a *app) listProjects(ctx context.Context, c *cobra.Command) (any, error) {
	return a.refreshProjects(ctx, c, true)
}

func (a *app) refreshProjects(ctx context.Context, c *cobra.Command, display bool) (projectListing, error) {
	kind, _ := c.Flags().GetString("type")
	if kind != "" && kind != "project" && kind != "scratch" {
		return nil, config.Invalid("--type must be project or scratch")
	}
	p, err := a.prefs()
	if err != nil {
		return nil, err
	}
	all, _ := c.Flags().GetBool("all")
	selected, _ := c.Flags().GetString("server")
	if all && selected != "" {
		return nil, config.Invalid("choose --server or --all")
	}
	names := append([]string{"local"}, orderedServers(p)...)
	if selected != "" {
		if _, err = a.serverTarget(p, selected, ""); err != nil {
			return nil, err
		}
		names = []string{selected}
	}
	results := projectListing{}
	type response struct {
		name      string
		projects  []discoveredProject
		err       error
		scratches []tmux.Scratch
		warning   string
	}
	updates := make(chan response, len(names))
	targets := make([]target, 0, len(names))
	for _, name := range names {
		t, _ := a.serverTarget(p, name, "")
		entry := serverListing{Location: "remote", State: "syncing", Projects: []discoveredProject{}, Scratches: []tmux.Scratch{}}
		if t.Local {
			entry.Location = "local"
		}
		if cached, ok := a.loadListingCache(t); ok {
			entry.Projects = cached.Projects
			entry.Scratches = append([]tmux.Scratch{}, cached.Scratches...)
			entry.UpdatedAt = cached.UpdatedAt
		}
		results[name] = entry
		targets = append(targets, t)
	}
	// Render cached data before starting discovery. Piped output and JSON get one final result.
	outTerminal, ok := a.out.(*os.File)
	interactive := display && !a.json && ok && terminalAvailable(outTerminal)
	lines := 0
	redraw := func(clearOnly bool) {
		if !interactive {
			return
		}
		if lines > 0 {
			fmt.Fprintf(a.out, "\x1b[%dA\r\x1b[J", lines)
		}
		lines = 0
		if clearOnly {
			return
		}
		var view strings.Builder
		_ = filterListing(results, kind).print(&view)
		rows := strings.Split(strings.TrimSuffix(view.String(), "\n"), "\n")
		width, height, err := term.GetSize(int(outTerminal.Fd()))
		if err == nil && width > 0 && height > 1 {
			if len(rows) > height-1 {
				rows = append(rows[:max(0, height-2)], "… 正在刷新其余服务器")
			}
			for i, row := range rows {
				rows[i] = ansi.Truncate(row, max(1, width-1), "…")
			}
		}
		text := strings.Join(rows, "\n") + "\n"
		fmt.Fprint(a.out, text)
		lines = len(rows)
	}
	redraw(false)
	for _, t := range targets {
		go func(t target) {
			projects, err := a.discover(ctx, t)
			r := response{name: t.Name, projects: projects, err: err}
			if err == nil {
				data, e := a.call(ctx, t, nil, "scratch", "list")
				if e == nil {
					e = json.Unmarshal(data, &r.scratches)
				}
				if e != nil && protocol.Normalize(e).Code == "PROTOCOL_INCOMPATIBLE" {
					r.warning = "scratch listing requires a server with scratch-v1; upgrade the server"
				} else if e != nil {
					r.err = e
				}
			}
			updates <- r
		}(t)
	}
	failed := false
	for range names {
		update := <-updates
		entry := results[update.name]
		if update.err != nil {
			entry.State = "error"
			switch protocol.Normalize(update.err).Code {
			case "CONNECTION_FAILED":
				entry.State = "unreachable"
			case "PROTOCOL_INCOMPATIBLE":
				entry.State = "upgrade-required"
			}
			entry.Error = update.err.Error()
			failed = true
		} else {
			entry.State = "synced"
			entry.Projects = update.projects
			entry.Scratches = append([]tmux.Scratch{}, update.scratches...)
			entry.ScratchWarning = update.warning
			entry.UpdatedAt = time.Now().UTC()
			if entry.Projects == nil {
				entry.Projects = []discoveredProject{}
			}
			t, _ := a.serverTarget(p, update.name, "")
			a.saveListingCache(t, entry.Projects, entry.Scratches)
		}
		results[update.name] = entry
		redraw(false)
	}
	redraw(true)
	results = filterListing(results, kind)
	if failed {
		return results, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "some servers could not be refreshed", Details: results}
	}
	return results, nil
}
func (listing projectListing) print(out io.Writer) error {
	hasWarnings := false
	for _, name := range listingNames(listing) {
		entry := listing[name]
		location := "远端"
		if entry.Location == "local" {
			location = "本机"
		}
		if _, err := fmt.Fprintf(out, "%s · %d 个项目 · %s [%s]\n", name, len(entry.Projects), location, entry.State); err != nil {
			return err
		}
		if entry.ScratchWarning != "" {
			fmt.Fprintf(out, "  %s\n", entry.ScratchWarning)
		}
		if len(entry.Scratches) > 0 {
			fmt.Fprintf(out, "  %d 个 scratch 会话\n", len(entry.Scratches))
		}
		if entry.Error != "" {
			fmt.Fprintf(out, "  %s\n", entry.Error)
			if !entry.UpdatedAt.IsZero() {
				fmt.Fprintf(out, "  缓存：%s\n", entry.UpdatedAt.Format(time.RFC3339))
			}
		}
		if len(entry.Projects) == 0 && len(entry.Scratches) == 0 && entry.State == "synced" {
			fmt.Fprintf(out, "  添加项目：tdev create --server %s\n", name)
		}
		for _, project := range entry.Projects {
			status := project.Session.Status
			if project.Session.Warning != "" || project.OverrideError != "" {
				status += ",warning"
				hasWarnings = true
			}
			fmt.Fprintf(out, "  %s/%s [%s] [project]\n    %s\n", name, project.Project.Name, status, project.Project.Directory)
			for _, f := range project.Project.Forwards {
				fmt.Fprintf(out, "    %s -%s %s\n", f.ID, f.Direction, f.Spec())
			}
		}
		for _, scratch := range entry.Scratches {
			fmt.Fprintf(out, "  %s/%s [scratch,%s]\n    %s\n    恢复：tdev open %s/%s\n", name, scratch.Name, scratch.Status.Status, scratch.Directory, name, scratch.Name)
		}
	}
	if hasWarnings {
		_, err := fmt.Fprintln(out, "\n部分项目有 warning，运行 tdev doctor 查看诊断详情。")
		return err
	}
	return nil
}

func filterListing(listing projectListing, kind string) projectListing {
	result := projectListing{}
	for name, entry := range listing {
		if kind == "scratch" {
			entry.Projects = []discoveredProject{}
		}
		if kind == "project" {
			entry.Scratches = []tmux.Scratch{}
			entry.ScratchWarning = ""
		}
		result[name] = entry
	}
	return result
}
