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
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/tmux"
	"tdev/internal/webproxy"
)

type serverListing struct {
	Items       []map[string]string  `json:"items"`
	Wide        bool                 `json:"-"`
	ActiveOnly  bool                 `json:"-"`
	Connections []resourceConnection `json:"connections,omitempty"`
	Mounts      []daemon.MountState  `json:"mounts,omitempty"`

	View            string           `json:"-"`
	Web             []webproxy.State `json:"web"`
	WebPort         int              `json:"web_port"`
	webDomainSuffix string

	Forwards       []daemon.ForwardState `json:"forwards"`
	Location       string                `json:"location"`
	State          string                `json:"state"`
	UpdatedAt      time.Time             `json:"updated_at,omitempty"`
	Error          string                `json:"error,omitempty"`
	Projects       []discoveredProject   `json:"projects"`
	Scratches      []tmux.Scratch        `json:"scratches"`
	ScratchWarning string                `json:"scratch_warning,omitempty"`
}
type projectListing map[string]serverListing

func (a *app) sessionListCommand(parent *cobra.Command, name string) {
	c := a.command(parent, name, "List active work items with TCP and Web mappings", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return a.listProjects(ctx, c)
	})
	c.Flags().String("type", "", "filter by project, scratch or forward (legacy temporary forwards)")
	listingFlags(c, parent == a.root)
	if parent.Name() == "session" {
		c.Short = "List project and scratch sessions and attached client counts"
		c.Annotations = map[string]string{"listing-view": "session"}
	}
}

func listingFlags(c *cobra.Command, overview bool) {
	c.Flags().Bool("wide", false, "show connection IDs and PIDs for each resource")
	c.Flags().Bool("include-inactive", false, "include configured resources that are stopped")
	allUsage := "query every configured server (the default)"
	if overview {
		allUsage = "include inactive work items in the selected servers"
	}
	c.Flags().Bool("all", false, allUsage)
	c.Flags().String("server", "", "only list this saved server")
}

func (a *app) serviceListCommand(parent *cobra.Command, view string) {
	c := a.command(parent, "ls", "List "+view+" mappings", cobra.NoArgs, func(ctx context.Context, c *cobra.Command, _ []string) (any, error) {
		return a.listProjects(ctx, c)
	})
	c.Annotations = map[string]string{"listing-view": view}
	if view == "project" {
		c.Short = "List configured projects and mappings"
	}
	listingFlags(c, false)
}

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
	view := c.Annotations["listing-view"]
	wide, _ := c.Flags().GetBool("wide")
	includeInactive, _ := c.Flags().GetBool("include-inactive")
	if kind != "" && kind != "project" && kind != "scratch" && kind != "forward" {
		return nil, config.Invalid("--type must be project, scratch or forward")
	}
	p, err := a.prefs()
	if err != nil {
		return nil, err
	}
	all, _ := c.Flags().GetBool("all")
	selected, _ := c.Flags().GetString("server")
	overview := c.Name() == "ls" && c.Parent() == a.root
	if overview {
		includeInactive = includeInactive || all
	} else if all && selected != "" {
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
	// Temporary forwards belong to this client, independent of remote project discovery.
	client, err := daemon.New(a.store.Root)
	if err != nil {
		return nil, err
	}
	state, err := client.Call(ctx, daemon.Request{Op: "status"})
	if err != nil && protocol.Normalize(err).Code != "DAEMON_UNAVAILABLE" {
		return nil, err
	}
	liveForwards := map[string][]daemon.ForwardState{}
	for _, f := range state.Forwards {
		servers := map[string]bool{}
		if f.Name != "" {
			servers[f.Server] = true
		}
		for _, r := range f.Resources {
			servers[r.Server] = true
		}
		for server := range servers {
			liveForwards[server] = append(liveForwards[server], f)
		}
	}
	bindings := []resourceConnection{}
	if wide && err == nil {
		snapshot, inspectErr := client.Call(ctx, daemon.Request{Op: "inspect"})
		if inspectErr == nil && snapshot.Inspection != nil {
			bindings = resourceConnections(*snapshot.Inspection)
		}
	}
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
		t.Batch = true
		entry := serverListing{Wide: wide, ActiveOnly: !includeInactive && view != "project", View: view, WebPort: p.HTTP.Port, webDomainSuffix: p.HTTP.DomainSuffix, Web: []webproxy.State{}, Forwards: append([]daemon.ForwardState{}, liveForwards[name]...), Location: "remote", State: "syncing", Projects: []discoveredProject{}, Scratches: []tmux.Scratch{}}
		for _, binding := range bindings {
			if binding.Resource.Server == name {
				entry.Connections = append(entry.Connections, binding)
			}
		}
		for _, m := range state.Mounts {
			for _, r := range m.Resources {
				if r.Server == name {
					entry.Mounts = append(entry.Mounts, m)
					break
				}
			}
		}
		for _, route := range state.Web {
			if route.Server == name {
				entry.Web = append(entry.Web, route)
			}
		}
		if t.Local {
			entry.Location = "local"
		}
		if cached, ok := a.loadListingCache(t); ok && kind != "forward" {
			entry.Projects = cached.Projects
			entry.Scratches = append([]tmux.Scratch{}, cached.Scratches...)
			entry.UpdatedAt = cached.UpdatedAt
		}
		if kind == "forward" {
			entry.State = "synced"
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
			if kind == "forward" {
				updates <- response{name: t.Name}
				return
			}
			allowMissingTmux := overview && t.Local
			projects, err := a.discover(ctx, t, true)
			if allowMissingTmux && err != nil && protocol.Normalize(err).Code == "DEPENDENCY_MISSING" {
				projects, err = a.discover(ctx, t, false)
			}
			r := response{name: t.Name, projects: projects, err: err}
			if err == nil && (view == "" || view == "session") {
				data, e := a.call(ctx, t, nil, "scratch", "list")
				if e == nil {
					e = json.Unmarshal(data, &r.scratches)
				}
				if e != nil {
					switch protocol.Normalize(e).Code {
					case "PROTOCOL_INCOMPATIBLE":
						r.warning = "scratch listing requires a server with scratch-v1; upgrade the server"
					case "DEPENDENCY_MISSING":
						if !allowMissingTmux {
							r.err = e
						}
					default:
						r.err = e
					}
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
			case sshx.AuthRequired:
				entry.State = "unauthorized"
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
			if kind != "forward" && (view == "" || view == "session") {
				a.saveListingCache(t, entry.Projects, entry.Scratches)
			}
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
func (listing projectListing) print(out io.Writer) error { return listing.printResources(out) }

// Measure terminal cells so CJK names and paths align with ASCII text.
func printListingTable(out io.Writer, rows [][6]string) error {
	return printTable(out, [6]string{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "WEB"}, rows)
}

func printTable(out io.Writer, header [6]string, rows [][6]string) error {
	data := make([][]string, 0, len(rows))
	for _, row := range rows {
		data = append(data, append([]string{}, row[:]...))
	}
	return printColumns(out, header[:], data)
}

// printColumns measures terminal cells and quotes control bytes in public output.
func printColumns(out io.Writer, header []string, rows [][]string) error {
	clean := func(cell string) string {
		return strings.Map(func(r rune) rune {
			if r < 32 || (r >= 127 && r < 160) {
				return '�'
			}
			return r
		}, cell)
	}
	widths := make([]int, len(header))
	for i, cell := range header {
		widths[i] = ansi.StringWidth(cell)
	}
	data := make([][]string, len(rows))
	for n, row := range rows {
		data[n] = make([]string, len(header))
		for i := range header {
			if i < len(row) {
				data[n][i] = clean(row[i])
			}
			widths[i] = max(widths[i], ansi.StringWidth(data[n][i]))
		}
	}
	write := func(row []string) error {
		var line strings.Builder
		line.WriteString("  ")
		for i, cell := range row {
			line.WriteString(cell)
			if i < len(row)-1 {
				line.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(cell)+2))
			}
		}
		_, err := fmt.Fprintln(out, strings.TrimRight(line.String(), " "))
		return err
	}
	if err := write(header); err != nil {
		return err
	}
	separator := make([]string, len(header))
	for i := range header {
		separator[i] = strings.Repeat("─", widths[i])
	}
	if err := write(separator); err != nil {
		return err
	}
	for _, row := range data {
		if err := write(row); err != nil {
			return err
		}
	}
	return nil
}

func filterListing(listing projectListing, kind string) projectListing {
	result := projectListing{}
	for name, entry := range listing {
		if kind == "forward" {
			entry.View = "temporary"
		}
		if kind == "scratch" || kind == "forward" {
			entry.Projects = []discoveredProject{}
		}
		if kind == "project" || kind == "forward" {
			entry.Scratches = []tmux.Scratch{}
			entry.ScratchWarning = ""
		}
		if kind != "" && kind != "forward" {
			entry.Forwards = []daemon.ForwardState{}
		}
		rows, header := entry.rows(name)
		entry.Items = []map[string]string{}
		for _, row := range rows {
			item := map[string]string{}
			for i, cell := range row.cells {
				item[strings.ToLower(header[i])] = cell
			}
			if entry.Wide {
				item["connection"], item["pid"] = entry.wideCells(row.refs)
			}
			entry.Items = append(entry.Items, item)
		}
		result[name] = entry
	}
	return result
}
