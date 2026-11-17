package cli

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/webproxy"
)

func forwardMapping(f config.Forward) string {
	direction := "->"
	if f.Direction == "R" {
		direction = "<-"
	}
	return fmt.Sprintf("%s %s %s", net.JoinHostPort(f.BindAddress, strconv.Itoa(f.ListenPort)), direction, net.JoinHostPort(f.TargetHost, strconv.Itoa(f.TargetPort)))
}

func webMapping(h config.HTTPService, port int, live []webproxy.State, project string) string {
	address := net.JoinHostPort(h.Hostname, strconv.Itoa(port))
	targetHost, targetPort := h.TargetHost, h.TargetPort
	for _, route := range live {
		if route.Project == project && route.Name == h.Name && route.Hostname == h.Hostname {
			if route.TargetHost != "" && route.TargetPort > 0 {
				targetHost, targetPort = route.TargetHost, route.TargetPort
			}
			if u, err := url.Parse(route.URL); err == nil && u.Host != "" {
				address = u.Host
			}
			break
		}
	}
	return address + " -> " + net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
}

func (entry serverListing) mappingRows(server string) ([]listingRow, [6]string) {
	rows := []listingRow{}
	if entry.View == "forward" {
		header := [6]string{"SERVER", "NAME", "TYPE", "STATUS", "FORWARD", "ERROR"}
		for _, p := range entry.Projects {
			for _, f := range p.Project.Forwards {
				state, failure := entry.forwardState(server, p.Project.Name, f.ID, "-"+f.Direction+" "+f.Spec())
				if failure == "" {
					failure = p.OverrideError
				}
				if entry.ActiveOnly && !activeState(state) {
					continue
				}
				rows = append(rows, listingRow{[6]string{server, p.Project.Name, "project", state, forwardMapping(f), failure}, []daemon.ResourceRef{ref(server, p.Project.Name, "forward", f.ID)}})
			}
		}
		for _, f := range entry.Forwards {
			if f.Name == "" {
				for _, r := range f.Resources {
					if r.Server != server || r.Kind != "forward" {
						continue
					}
					found := false
					for _, row := range rows {
						for _, seen := range row.refs {
							if seen == r && row.cells[4] == temporaryForwardMapping(f.Spec) {
								found = true
							}
						}
					}
					if !found {
						rows = append(rows, listingRow{[6]string{server, r.Name, "project", runningStatus(f.State), temporaryForwardMapping(f.Spec), f.Error}, []daemon.ResourceRef{r}})
					}
				}

				continue
			}
			rows = append(rows, listingRow{[6]string{server, f.Name, "temporary", runningStatus(f.State), temporaryForwardMapping(f.Spec), f.Error}, []daemon.ResourceRef{ref(server, f.Name, "temporary", "")}})
		}
		return rows, header
	}
	header := [6]string{"SERVER", "PROJECT", "SERVICE", "STATUS", "WEB", "ERROR"}
	seen := map[string]bool{}
	for _, p := range entry.Projects {
		for _, h := range p.Project.HTTP {
			state, failure := entry.webState(p.Project.Name, h.Name, h.Hostname)
			seen[p.Project.Name+"/"+h.Name+"/"+h.Hostname] = true
			if entry.ActiveOnly && !activeState(state) {
				continue
			}
			rows = append(rows, listingRow{[6]string{server, p.Project.Name, h.Name, state, webMapping(h, entry.WebPort, entry.Web, p.Project.Name), failure}, []daemon.ResourceRef{ref(server, p.Project.Name, "web", h.Name)}})
		}
	}
	for _, live := range entry.Web {
		if !seen[live.Project+"/"+live.Name+"/"+live.Hostname] {
			rows = append(rows, listingRow{[6]string{server, live.Project, live.Name, runningStatus(live.State), liveWebMapping(live), live.Error}, []daemon.ResourceRef{ref(server, live.Project, "web", live.Name)}})
		}
	}
	return rows, header
}

func temporaryForwardMapping(spec string) string {
	direction, value, ok := strings.Cut(spec, " ")
	if ok && (direction == "-L" || direction == "-R") {
		if f, err := config.ParseForward(direction[1:], value); err == nil {
			return forwardMapping(f)
		}
	}
	return spec
}

func liveWebMapping(live webproxy.State) string {
	if live.TargetHost == "" || live.TargetPort == 0 {
		return live.URL
	}
	address := live.URL
	if u, err := url.Parse(live.URL); err == nil && u.Host != "" {
		address = u.Host
	}
	return address + " -> " + net.JoinHostPort(live.TargetHost, strconv.Itoa(live.TargetPort))
}
