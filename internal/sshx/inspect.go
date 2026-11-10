package sshx

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

type Channel struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	State       string `json:"state"`
	StateCode   int    `json:"state_code"`
	InputState  int    `json:"input_state"`
	OutputState int    `json:"output_state"`
	InputBytes  int    `json:"input_bytes"`
	OutputBytes int    `json:"output_bytes"`
}

type Inspection struct {
	PID            int       `json:"pid,omitempty"`
	State          string    `json:"state"`
	ChannelsStatus string    `json:"channels_status"`
	Channels       []Channel `json:"channels"`
}

var masterPID = regexp.MustCompile(`Master running \(pid=([0-9]+)\)`)

// OpenSSH reports extended types such as session:command and
// session:subsystem:<name>. Keep the base type only: subsystem names may
// contain application arguments rather than a fixed OpenSSH type token.
var channelStatus = regexp.MustCompile(`^\s*#([0-9]+) .* \(t([0-9]+) \[([a-zA-Z0-9_.@-]+)(?::[^\]\r\n]*)?\] .* i([0-9]+)/([0-9]+) o([0-9]+)/([0-9]+) `)

// Inspect uses only local mux requests. It never authenticates or starts SSH.
func Inspect(ctx context.Context, socket string) Inspection {
	i := Inspection{State: "unavailable", ChannelsStatus: "unavailable", Channels: []Channel{}}
	query := func(op string) ([]byte, error) {
		return Command(ctx, "ssh", "-F", "/dev/null", "-S", socket, "-o", "ControlMaster=no", "-O", op, "tdev-owned").CombinedOutput()
	}
	b, err := query("check")
	if err != nil {
		return i
	}
	i.State = "master-running"
	if match := masterPID.FindSubmatch(b); match != nil {
		i.PID, _ = strconv.Atoi(string(match[1]))
	}
	b, err = query("channels")
	if err != nil {
		i.ChannelsStatus = "unsupported-or-unavailable"
		return i
	}
	i.Channels, i.ChannelsStatus = parseChannels(string(b))
	return i
}

// Discard remote names: they can contain application commands or arbitrary text.
// Keep only OpenSSH's numeric status fields and bounded channel type names.
func parseChannels(output string) ([]Channel, string) {
	channels := []Channel{}
	if !strings.Contains(output, "The following connections are open:") {
		return channels, "unrecognized"
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		match := channelStatus.FindStringSubmatch(line)
		if match == nil {
			return []Channel{}, "unrecognized"
		}
		if strings.HasPrefix(match[3], "mux-") {
			continue // Local multiplexing controls are not SSH wire channels.
		}
		n := func(index int) int { v, _ := strconv.Atoi(match[index]); return v }
		state := map[int]string{3: "opening", 4: "open", 5: "closing", 7: "x11-opening", 10: "initializing", 12: "connecting", 13: "dynamic", 20: "proxy", 21: "dynamic-opening", 22: "dynamic-connecting"}[n(2)]
		if state == "" {
			state = "unknown"
		}
		channels = append(channels, Channel{ID: n(1), Type: match[3], State: state, StateCode: n(2), InputState: n(4), InputBytes: n(5), OutputState: n(6), OutputBytes: n(7)})
	}
	return channels, "available"
}
