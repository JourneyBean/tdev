package cli

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"tdev/internal/daemon"
	"tdev/internal/tmux"
)

type resourceConnection struct {
	Resource   daemon.ResourceRef `json:"resource"`
	Connection string             `json:"connection"`
	PID        int                `json:"pid,omitempty"`
}

func resourceConnections(snapshot daemon.Inspection) []resourceConnection {
	rows := []resourceConnection{}
	for _, c := range snapshot.Connections {
		pid := c.PID
		if c.Kind == "sshfs" {
			pid = c.HelperPID
		}
		if c.Kind == "local-forward" || c.Kind == "local-web" {
			pid = snapshot.PID
		}
		for _, r := range c.Resources {
			rows = append(rows, resourceConnection{Resource: r, Connection: daemon.ConnectionID(c, snapshot.PID, snapshot.RuntimeDir), PID: pid})
		}
	}
	return rows
}

type listingRow struct {
	cells [6]string
	refs  []daemon.ResourceRef
}

func ref(server, name, kind, detail string) daemon.ResourceRef {
	return daemon.ResourceRef{Server: server, Name: name, Kind: kind, Detail: detail}
}

func (entry serverListing) forwardState(server, name, id, spec string) (string, string) {
	for _, f := range entry.Forwards {
		for _, r := range f.Resources {
			if r.Server == server && r.Name == name && r.Kind == "forward" && r.Detail == id && (spec == "" || f.Spec == spec) {
				return runningStatus(f.State), f.Error
			}
		}
	}
	return "configured", ""
}

func (entry serverListing) webState(project, service, hostname string) (string, string) {
	for _, live := range entry.Web {
		if live.Project == project && live.Name == service && live.Hostname == hostname {
			return runningStatus(live.State), live.Error
		}
	}
	return "stopped", ""
}

func activeState(state string) bool {
	return state != "" && state != "missing" && state != "inactive" && state != "stopped" && state != "configured"
}

func statusPriority(state string) int {
	switch state {
	case "failed", "unauthorized", "unavailable", "unmanaged":
		return 5
	case "waiting", "incomplete":
		return 4
	case "connecting", "syncing":
		return 3
	case "running":
		return 2
	default:
		return 1
	}
}

func clientCount(s tmux.Status) string {
	if s.Clients == nil {
		return "—"
	}
	return strconv.Itoa(*s.Clients)
}

func (entry serverListing) rows(server string) ([]listingRow, [6]string) {
	rows := []listingRow{}
	header := [6]string{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "WEB"}
	if entry.View == "forward" || entry.View == "web" {
		return entry.mappingRows(server)
	}
	if entry.View == "session" {
		header = [6]string{"SERVER", "NAME", "TYPE", "STATUS", "CLIENTS", "SESSION"}
	}
	if entry.View == "project" {
		header = [6]string{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "WEB"}
	}
	for _, p := range entry.Projects {
		state := runningStatus(p.Session.Status)
		refs := []daemon.ResourceRef{ref(server, p.Project.Name, "session", "")}
		active := activeState(state)
		if entry.View != "session" {
			for _, f := range p.Project.Forwards {
				s, _ := entry.forwardState(server, p.Project.Name, f.ID, "-"+f.Direction+" "+f.Spec())
				active = active || activeState(s)
				if statusPriority(s) > statusPriority(state) {
					state = s
				}
				refs = append(refs, ref(server, p.Project.Name, "forward", f.ID))
			}
			for _, f := range entry.Forwards {
				for _, r := range f.Resources {
					if r.Server == server && r.Name == p.Project.Name && r.Kind == "forward" {
						s := runningStatus(f.State)
						active = active || activeState(s)
						if statusPriority(s) > statusPriority(state) {
							state = s
						}
						refs = append(refs, r)
					}
				}
			}
			for _, h := range p.Project.HTTP {
				s, _ := entry.webState(p.Project.Name, h.Name, h.WithDomainSuffix(entry.webDomainSuffix).Hostname)
				active = active || activeState(s)
				if statusPriority(s) > statusPriority(state) {
					state = s
				}
				refs = append(refs, ref(server, p.Project.Name, "web", h.Name))
			}
			for _, live := range entry.Web {
				if live.Project == p.Project.Name {
					s := runningStatus(live.State)
					active = active || activeState(s)
					if statusPriority(s) > statusPriority(state) {
						state = s
					}
					refs = append(refs, ref(server, p.Project.Name, "web", live.Name))
				}
			}
			for _, m := range entry.Mounts {
				for _, r := range m.Resources {
					if r.Name == p.Project.Name && r.Server == server {
						active = true
						refs = append(refs, r)
						if !activeState(state) {
							state = "running"
						}
					}
				}
			}
		}
		if entry.ActiveOnly && !active {
			continue
		}
		if entry.State != "synced" && entry.State != "syncing" {
			state = "unknown"
		}
		if entry.View == "project" {
			state = "configured"
		}
		if p.Session.Warning != "" || p.OverrideError != "" {
			state += ",warning"
		}
		if entry.View == "session" {
			session := p.Session.SessionName
			if session == "" {
				session = "—"
			}
			rows = append(rows, listingRow{[6]string{server, p.Project.Name, "project", state, clientCount(p.Session), session}, refs})
			continue
		}
		fs, hs := []string{}, []string{}
		for _, f := range p.Project.Forwards {
			fs = append(fs, forwardMapping(f))
		}
		for _, h := range p.Project.HTTP {
			live := entry.Web
			if entry.View == "project" {
				live = nil
			}
			hs = append(hs, webMapping(h.WithDomainSuffix(entry.webDomainSuffix), entry.WebPort, live, p.Project.Name))
		}
		if entry.ActiveOnly {
			fs = []string{}
			seen := map[string]bool{}
			for _, f := range entry.Forwards {
				for _, r := range f.Resources {
					if r.Server == server && r.Name == p.Project.Name && r.Kind == "forward" && !seen[f.Spec] {
						fs = append(fs, temporaryForwardMapping(f.Spec))
						seen[f.Spec] = true
					}
				}
			}
			hs = []string{}
			for _, live := range entry.Web {
				if live.Project == p.Project.Name {
					hs = append(hs, liveWebMapping(live))
				}
			}
		}
		for i := 0; i < max(1, max(len(fs), len(hs))); i++ {
			cells := [6]string{}
			if i == 0 {
				cells = [6]string{server, p.Project.Name, "project", state, "—", "—"}
			}
			if i < len(fs) {
				cells[4] = fs[i]
			}
			if i < len(hs) {
				cells[5] = hs[i]
			}
			rowRefs := []daemon.ResourceRef{}
			if i == 0 {
				rowRefs = refs
			}
			rows = append(rows, listingRow{cells, rowRefs})
		}
	}
	if entry.View == "" {
		rows = append(rows, entry.orphanRows(server, rows)...)
	}
	if entry.View != "project" {
		for _, s := range entry.Scratches {
			state := runningStatus(s.Status.Status)
			if entry.State != "synced" && entry.State != "syncing" {
				state = "unknown"
			}
			cells := [6]string{server, s.Name, "scratch", state, "—", "—"}
			if entry.View == "session" {
				cells[4], cells[5] = clientCount(s.Status), s.SessionName
			}
			rows = append(rows, listingRow{cells, []daemon.ResourceRef{ref(server, s.Name, "scratch", "")}})
		}
	}
	if entry.View == "" || entry.View == "temporary" {
		grouped := map[string]int{}
		for _, f := range entry.Forwards {
			if f.Name == "" {
				continue
			}
			state := runningStatus(f.State)
			if entry.View == "temporary" {
				state = f.State
			}
			if entry.ActiveOnly && !activeState(state) {
				continue
			}
			if i, ok := grouped[f.Name]; ok {
				rows[i].cells[4] += "\n" + temporaryForwardMapping(f.Spec)
				if statusPriority(state) > statusPriority(rows[i].cells[3]) {
					rows[i].cells[3] = state
				}
			} else {
				mapping := temporaryForwardMapping(f.Spec)
				if entry.View == "temporary" {
					mapping = f.Spec
				}
				grouped[f.Name] = len(rows)
				rows = append(rows, listingRow{[6]string{server, f.Name, "forward", state, mapping, "—"}, []daemon.ResourceRef{ref(server, f.Name, "temporary", "")}})
			}
		}
	}
	if len(rows) == 0 && !entry.ActiveOnly && entry.View != "project" {
		rows = append(rows, listingRow{cells: [6]string{server, "—", "—", entry.State, "—", "—"}})
	}
	return rows, header
}

func (entry serverListing) wideCells(refs []daemon.ResourceRef) (string, string) {
	matched := map[string]int{}
	for _, want := range refs {
		for _, c := range entry.Connections {
			if want.Server == c.Resource.Server && want.Name == c.Resource.Name && want.Kind == c.Resource.Kind && (want.Detail == "" || want.Detail == c.Resource.Detail) {
				matched[c.Connection] = c.PID
			}
		}
	}
	ids := []string{}
	for id := range matched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "—", "—"
	}
	pids := []string{}
	for _, id := range ids {
		pids = append(pids, pidText(matched[id]))
	}
	return strings.Join(ids, ","), strings.Join(pids, ",")
}

func (listing projectListing) printResources(out io.Writer) error {
	data := [][]string{}
	notes := []string{}
	warnings := false
	wide := false
	header := [6]string{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "WEB"}
	for _, server := range listingNames(listing) {
		entry := listing[server]
		wide = wide || entry.Wide
		rows, h := entry.rows(server)
		header = h
		for _, row := range rows {
			// Expand multiline mappings into aligned continuation rows.
			count := 1
			for _, cell := range row.cells {
				count = max(count, len(strings.Split(cell, "\n")))
			}
			for n := 0; n < count; n++ {
				cells := make([]string, 6)
				for i, cell := range row.cells {
					lines := strings.Split(cell, "\n")
					if n < len(lines) {
						cells[i] = lines[n]
					}
				}
				if entry.Wide {
					connection, pid := "", ""
					if n == 0 {
						connection, pid = entry.wideCells(row.refs)
					}
					cells = append(cells, connection, pid)
				}
				data = append(data, cells)
			}
		}
		if entry.State != "synced" {
			notes = append(notes, fmt.Sprintf("%s [%s]", server, entry.State))
		}
		if entry.ScratchWarning != "" {
			notes = append(notes, server+": "+entry.ScratchWarning)
		}
		if entry.Error != "" {
			notes = append(notes, server+": "+entry.Error)
			if !entry.UpdatedAt.IsZero() {
				notes = append(notes, fmt.Sprintf("%s 缓存：%s", server, entry.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")))
			}
		}
		for _, p := range entry.Projects {
			warnings = warnings || p.Session.Warning != "" || p.OverrideError != ""
		}
		for _, f := range entry.Forwards {
			if f.Error != "" {
				notes = append(notes, server+"/"+f.Name+": "+f.Error)
			}
		}
	}
	headers := append([]string{}, header[:]...)
	if wide {
		headers = append(headers, "CONNECTION", "PID")
	}
	if err := printColumns(out, headers, data); err != nil {
		return err
	}
	if warnings {
		notes = append(notes, "部分项目有 warning，运行 tdev doctor 查看诊断详情。")
	}
	if len(notes) > 0 {
		_, err := fmt.Fprintln(out, "\n"+strings.Join(notes, "\n"))
		return err
	}
	return nil
}

func (entry serverListing) orphanRows(server string, existing []listingRow) []listingRow {
	seen := map[string]bool{}
	for _, row := range existing {
		if row.cells[2] == "project" {
			seen[row.cells[1]] = true
		}
	}
	groups := map[string]*listingRow{}
	add := func(r daemon.ResourceRef, state, mapping string, column int) {
		if r.Server != server || seen[r.Name] {
			return
		}
		row := groups[r.Name]
		if row == nil {
			row = &listingRow{cells: [6]string{server, r.Name, "project", state, "—", "—"}}
			groups[r.Name] = row
		}
		if statusPriority(state) > statusPriority(row.cells[3]) {
			row.cells[3] = state
		}
		row.refs = append(row.refs, r)
		if mapping != "" {
			if row.cells[column] == "—" {
				row.cells[column] = mapping
			} else {
				row.cells[column] += "\n" + mapping
			}
		}
	}
	for _, f := range entry.Forwards {
		if f.Name != "" {
			continue
		}
		for _, r := range f.Resources {
			add(r, runningStatus(f.State), temporaryForwardMapping(f.Spec), 4)
		}
	}
	for _, live := range entry.Web {
		add(ref(server, live.Project, "web", live.Name), runningStatus(live.State), liveWebMapping(live), 5)
	}
	for _, m := range entry.Mounts {
		for _, r := range m.Resources {
			add(r, "running", "", 4)
		}
	}
	names := []string{}
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := []listingRow{}
	for _, name := range names {
		rows = append(rows, *groups[name])
	}
	return rows
}
