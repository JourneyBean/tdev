package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
)

type processHost struct {
	Server    string           `json:"server"`
	State     string           `json:"state"`
	Inventory daemon.Inventory `json:"inventory"`
	Error     *protocol.Error  `json:"error,omitempty"`
}

type processListing struct {
	Hosts []processHost `json:"hosts"`
}

func (a *app) psCommand() {
	c := &cobra.Command{Use: "ps", Short: "Inspect daemon processes, SSH connections and allocated channels", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(c.Context(), a.timeout)
		defer cancel()
		server, _ := c.Flags().GetString("server")
		listing, err := a.processes(ctx, server)
		if !a.json {
			if printErr := listing.print(a.out); printErr != nil {
				return printErr
			}
			return err
		}
		return a.result(listing, err)
	}}
	c.Flags().String("server", "", "only inspect local or this registered server")
	a.root.AddCommand(c)
}

func (a *app) processes(ctx context.Context, selected string) (processListing, error) {
	result := processListing{Hosts: []processHost{}}
	prefs, err := a.prefs()
	if err != nil {
		return result, err
	}
	names := []string{"local"}
	names = append(names, orderedServers(prefs)...)
	if selected != "" {
		if selected != "local" {
			if _, ok := prefs.Servers[selected]; !ok {
				return result, config.Invalid("unknown server %q", selected)
			}
		}
		names = []string{selected}
	}
	result.Hosts = make([]processHost, len(names))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 8)
	for index, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row := processHost{Server: name, State: "ready", Inventory: daemon.Inventory{Daemons: []daemon.DaemonView{}}}
			var err error
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				err = ctx.Err()
			}
			if err == nil && name == "local" {
				row.Inventory, err = daemon.InventoryForUser(ctx)
			} else if err == nil {
				var t target
				t, err = a.serverTarget(prefs, name, "")
				if err == nil {
					// The observation connection is independent and batch-only: ps
					// must not launch a supervisor or add persistent SSH masters.
					t.Batch = true
					reader := *a
					reader.useDaemon = false
					var data json.RawMessage
					data, err = reader.call(ctx, t, nil, "ps")
					if err == nil {
						err = json.Unmarshal(data, &row.Inventory)
					}
				}
			}
			if err != nil {
				row.Error = protocol.Normalize(err)
				row.State = "error"
				if row.Error.Code == "CONNECTION_FAILED" {
					row.State = "unreachable"
				}
				if row.Error.Code == "PROTOCOL_INCOMPATIBLE" {
					row.State = "upgrade-required"
				}
			}
			result.Hosts[index] = row
		}()
	}
	wg.Wait()
	for _, row := range result.Hosts {
		failed := row.Error != nil
		for _, d := range row.Inventory.Daemons {
			failed = failed || d.State == "unavailable"
		}
		if failed {
			return result, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "some process inventories are unavailable", Details: result}
		}
	}
	return result, nil
}

func (p processListing) print(out io.Writer) error {
	// Quote user-controlled identifiers to keep terminal control bytes inert.
	var b strings.Builder
	for _, host := range p.Hosts {
		fmt.Fprintf(&b, "%s [%s]\n", host.Server, host.State)
		if host.Error != nil {
			fmt.Fprintf(&b, "  %s: %q\n", host.Error.Code, host.Error.Message)
			continue
		}
		if len(host.Inventory.Daemons) == 0 {
			b.WriteString("  No running tdev daemons.\n")
		}
		for _, d := range host.Inventory.Daemons {
			fmt.Fprintf(&b, "  daemon pid=%d %s config=%q\n", d.PID, displayToken(d.State), d.ConfigRoot)
			if d.ConfigRoot == "" {
				fmt.Fprintf(&b, "    runtime=%q\n", d.RuntimeDir)
			}
			if len(d.Connections) == 0 && d.State == "running" {
				b.WriteString("    No managed connections.\n")
			}
			for _, warning := range d.Warnings {
				fmt.Fprintf(&b, "    warning: %q\n", warning)
			}
			for _, c := range d.Connections {
				fmt.Fprintf(&b, "    %s alias=%q pid=%d %s transport=%s owners=%d background=%t", displayToken(c.Kind), c.Alias, c.PID, displayToken(c.Status), displayToken(c.State), c.Owners, c.Durable)
				if c.Spec != "" {
					fmt.Fprintf(&b, " resource=%q", c.Spec)
				}
				if c.HelperPID != 0 {
					fmt.Fprintf(&b, " helper-pid=%d", c.HelperPID)
				}
				b.WriteByte('\n')
				for _, allocation := range c.Allocations {
					fmt.Fprintf(&b, "      allocation %s %s project=%q client-pid=%d\n", displayToken(allocation.ID), displayToken(allocation.Kind), allocation.Project, allocation.ClientPID)
				}
				if c.ChannelsStatus != "available" {
					fmt.Fprintf(&b, "      channels: %s\n", displayToken(c.ChannelsStatus))
				} else if len(c.Channels) == 0 {
					b.WriteString("      channels: 0\n")
				} else {
					for _, ch := range c.Channels {
						fmt.Fprintf(&b, "      channel #%d %s %s input=%d/%dB output=%d/%dB\n", ch.ID, displayToken(ch.Type), displayToken(ch.State), ch.InputState, ch.InputBytes, ch.OutputState, ch.OutputBytes)
					}
				}
			}
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}

func displayToken(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.@", r) {
			return r
		}
		return '?'
	}, value)
}
