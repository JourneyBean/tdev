package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
)

func TestPSAcrossLocalAndRemoteDaemonsWithoutStartingStoppedServices(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration requires Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.4.1")
	root, remoteRoot := t.TempDir(), t.TempDir()
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	store := config.Store{Root: remoteRoot}
	if _, err := store.Create(context.Background(), "demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// A second config root represents an independently running remote daemon.
	executeBinary(t, binary, remoteRoot, s.Config, "", 0, "daemon", "start")
	remoteClient, err := daemon.New(remoteRoot)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	transport, err := remoteClient.Call(ctx, daemon.Request{Op: "acquire", Alias: s.Alias, SSH: opts})
	if err != nil {
		t.Fatal(err)
	}
	release, err := remoteClient.TrackChannel(ctx, daemon.Allocation{ID: "remoteallocation", Socket: transport.Socket, Kind: "api", Project: "demo", ClientPID: os.Getpid()})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "stop")
	localClient, err := daemon.New(root)
	if err != nil {
		t.Fatal(err)
	}
	query := func(wantPartial bool, args ...string) processListing {
		t.Helper()
		argv := append([]string{"--config-dir", root, "--ssh-config", s.Config, "--json", "--timeout", "5s", "ps"}, args...)
		cmd := exec.Command(binary, argv...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != 0 && code != 9 || wantPartial && code != 9 {
			t.Fatalf("ps returned unexpected exit %d", code)
		}
		var response protocol.Response
		if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
			t.Fatal("ps did not return one protocol JSON object")
		}
		payload := response.Data
		if code == 9 {
			if response.Error == nil || response.Error.Code != "PARTIAL_SUCCESS" {
				t.Fatal("missing partial process inventory")
			}
			payload = response.Error.Details
		}
		b, _ := json.Marshal(payload)
		var listing processListing
		if err := json.Unmarshal(b, &listing); err != nil {
			t.Fatal(err)
		}
		return listing
	}
	listing := query(false)
	if len(listing.Hosts) != 2 || listing.Hosts[0].Server != "local" || listing.Hosts[1].Server != "fixture" {
		t.Fatal("missing local/remote process inventories")
	}
	found := false
	for _, d := range listing.Hosts[1].Inventory.Daemons {
		if d.ConfigRoot != remoteRoot {
			continue
		}
		for _, c := range d.Connections {
			if c.Socket == transport.Socket && c.PID > 0 && len(c.Allocations) == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("remote inventory omitted the independent daemon, SSH master or allocation")
	}
	if _, err := localClient.Call(ctx, daemon.Request{Op: "status"}); err == nil {
		t.Fatal("ps started a stopped local daemon")
	}
	// A real attached terminal is attributed to its project and CLI PID.
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "start")
	terminal := terminalCLI(t, binary, root, s.Config, "open", "fixture/demo", "--only", "shell")
	terminal.write(t, "printf 'PS_READY\\n'\r")
	terminal.waitText(t, "PS_READY\r\n")
	listing = query(false, "--server", "local")
	found = false
	for _, d := range listing.Hosts[0].Inventory.Daemons {
		if d.ConfigRoot != root {
			continue
		}
		for _, c := range d.Connections {
			for _, a := range c.Allocations {
				if a.Kind == "terminal" && a.Project == "demo" && a.ClientPID == terminal.cmd.Process.Pid {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("terminal channel allocation missing")
	}
	terminal.write(t, "tmux detach-client\r")
	terminal.exit(t, 0)
	s.Offline(true)
	listing = query(true)
	if listing.Hosts[0].State != "ready" || listing.Hosts[1].State != "unreachable" {
		t.Fatal("offline remote discarded local results or lost transport classification")
	}
	s.Offline(false)
}

func TestPSRenderingQuotesUntrustedFields(t *testing.T) {
	listing := processListing{Hosts: []processHost{{Server: "local", State: "ready", Inventory: daemon.Inventory{Daemons: []daemon.DaemonView{{State: "running", Inspection: daemon.Inspection{PID: 10, ConfigRoot: "config\x1b]2;injected\a", Connections: []daemon.ConnectionView{{Kind: "control\x1b[2J", Status: "active", Inspection: sshx.Inspection{State: "master-running", ChannelsStatus: "available", Channels: []sshx.Channel{{ID: 3, Type: "session", State: "open"}}}}}}}}}}}}
	var out bytes.Buffer
	if err := listing.print(&out); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\a") || !strings.Contains(out.String(), "channel #3 session open") {
		t.Fatal("unsafe or incomplete process rendering")
	}
}
