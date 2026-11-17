package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"tdev/internal/daemon"
)

type connectionRow struct {
	Connection string               `json:"connection"`
	PID        int                  `json:"pid,omitempty"`
	Server     string               `json:"server"`
	Type       string               `json:"type"`
	Status     string               `json:"status"`
	Resources  []daemon.ResourceRef `json:"resources"`
}

func runningStatus(state string) string {
	switch state {
	case "ready", "mounted", "registered", "active", "idle", "master-running", "local":
		return "running"
	case "missing":
		return "inactive"
	default:
		return state
	}
}

func (p processListing) connectionRows() []connectionRow {
	rows := []connectionRow{}
	indexes := map[string]int{}
	for _, host := range p.Hosts {
		for _, d := range host.Inventory.Daemons {
			for _, c := range d.Connections {
				id := daemon.ConnectionID(c, d.PID, d.RuntimeDir)
				pid, kind := c.PID, c.Kind
				if kind == "sshfs" {
					pid = c.HelperPID
				}
				if kind == "local-forward" || kind == "local-web" {
					pid, kind = d.PID, "daemon"
				}
				status := runningStatus(c.Status)
				if kind == "daemon" {
					status = d.State
				}
				if c.Socket != "" && c.State != "master-running" && status == "running" {
					status = "unavailable"
				}
				key := host.Server + "/" + id
				i, exists := indexes[key]
				if !exists {
					i = len(rows)
					indexes[key] = i
					rows = append(rows, connectionRow{Connection: id, PID: pid, Server: host.Server, Type: kind, Status: status, Resources: []daemon.ResourceRef{}})
				}
				refs := append([]daemon.ResourceRef{}, c.Resources...)
				// Allocation fallback supports daemons predating explicit resource references.
				if len(refs) == 0 {
					for _, a := range c.Allocations {
						if a.Project == "" {
							continue
						}
						k := a.Kind
						if k == "terminal" {
							k = "session"
						}
						if a.Scratch {
							k = "scratch"
						}
						refs = append(refs, daemon.ResourceRef{Server: a.Server, Name: a.Project, Kind: k})
					}
				}
				for _, r := range refs {
					found := false
					for _, old := range rows[i].Resources {
						if old == r {
							found = true
							break
						}
					}
					if !found {
						rows[i].Resources = append(rows[i].Resources, r)
					}
				}
			}
		}
	}
	for i := range rows {
		sort.Slice(rows[i].Resources, func(a, b int) bool { return rows[i].Resources[a].Label() < rows[i].Resources[b].Label() })
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Server != rows[j].Server {
			return rows[i].Server < rows[j].Server
		}
		return rows[i].Connection < rows[j].Connection
	})
	return rows
}

func pidText(pid int) string {
	if pid <= 0 {
		return "—"
	}
	return strconv.Itoa(pid)
}

func (p processListing) print(out io.Writer) error {
	rows := [][]string{}
	for _, c := range p.connectionRows() {
		labels := []string{}
		for _, r := range c.Resources {
			labels = append(labels, r.Label())
		}
		users := strings.Join(labels, ", ")
		if users == "" {
			users = "—"
		}
		rows = append(rows, []string{c.Connection, pidText(c.PID), c.Server, c.Type, c.Status, users})
	}
	if err := printColumns(out, []string{"CONNECTION", "PID", "SERVER", "TYPE", "STATUS", "RESOURCES"}, rows); err != nil {
		return err
	}
	for _, host := range p.Hosts {
		if host.Error != nil {
			if _, err := fmt.Fprintf(out, "\n%s [%s]: %q\n", displayToken(host.Server), displayToken(host.State), host.Error.Message); err != nil {
				return err
			}
		}
		for _, warning := range host.Inventory.Warnings {
			fmt.Fprintf(out, "\n%s: %q\n", displayToken(host.Server), warning)
		}
		for _, d := range host.Inventory.Daemons {
			if d.State != "running" {
				fmt.Fprintf(out, "\n%s daemon %d: %s\n", displayToken(host.Server), d.PID, displayToken(d.State))
			}
			for _, warning := range d.Warnings {
				fmt.Fprintf(out, "\n%s daemon %d: %q\n", displayToken(host.Server), d.PID, warning)
			}
		}
	}
	return nil
}
