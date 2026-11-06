package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
)

type apiResponse struct {
	ProtocolVersion int             `json:"protocol_version"`
	OK              bool            `json:"ok"`
	Data            json.RawMessage `json:"data"`
	Error           *protocol.Error `json:"error"`
}

func decodeAPI(data []byte) (json.RawMessage, error) {
	var r apiResponse
	if err := json.Unmarshal(data, &r); err != nil || r.ProtocolVersion != protocol.Version {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "invalid API response; upgrade the server")
	}
	if !r.OK {
		if r.Error == nil {
			return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "missing API error")
		}
		r.Error.Exit = exitForCode(r.Error.Code)
		return nil, r.Error
	}
	return r.Data, nil
}

func apiCapability(args []string) string {
	if len(args) == 0 {
		return "api-v1"
	}
	switch args[0] {
	case "scratch":
		return "scratch-v1"
	case "project":
		return "project-v1"
	case "session":
		if len(args) > 1 && (args[1] == "attach" || args[1] == "attachment") {
			return "attachment-v1"
		}
		return "session-v1"
	case "transfer":
		if len(args) > 1 && args[1] == "plan" {
			for _, arg := range args[2:] {
				if arg == "--source-name" {
					return "transfer-file-directory-v1"
				}
			}
		}
		return "transfer-v1"
	case "binary":
		return "binary-install-v1"
	case "mount-probe":
		return "mount-probe-nfc-v1"
	}
	return "api-v1"
}

// A shell batch is a single SSH invocation, with ordinary CLI operations and
// per-request random framing. The API gate runs before any operation consumes stdin.
func (a *app) call(ctx context.Context, t target, input []byte, args ...string) (json.RawMessage, error) {
	data, err := a.calls(ctx, t, input, args)
	if err != nil {
		return nil, err
	}
	return data[0], nil
}

func (a *app) calls(ctx context.Context, t target, input []byte, operations ...[]string) (data []json.RawMessage, resultErr error) {
	started := time.Now()
	var exitCode *int
	defer func() { _, _ = a.recordDiagnostic("api_batch", t.Name, started, resultErr, exitCode) }()
	if len(input) > 0 && len(operations) > 1 {
		return nil, config.Invalid("a shell batch accepts stdin for one operation")
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	results := make([]json.RawMessage, 0, len(operations))
	if t.Local {
		for _, args := range operations {
			var out, stderr bytes.Buffer
			argv := []string{"--config-dir", a.store.Root, "--timeout", a.timeout.String(), "--json", "api"}
			code := executeContext(ctx, append(argv, args...), bytes.NewReader(input), &out, &stderr)
			data, err := decodeAPI(out.Bytes())
			if err != nil {
				return nil, err
			}
			if code != 0 {
				return nil, protocol.Fail("RUNTIME_ERROR", 1, "API process failed: %s", stderr.String())
			}
			results = append(results, data)
		}
		return results, nil
	}
	marker := "TDEV_" + strings.ReplaceAll(config.NewID(), "-", "")
	// The top-level version exists on older servers. Their lack of api is caught
	// by the gate and reported with their program version, without running a write.
	script := remoteCommand(t, "--json", "version") + "; tdev_rc=$?; printf '\\n" + marker + " %s\\n' \"$tdev_rc\"; test \"$tdev_rc\" -eq 0 || exit \"$tdev_rc\"; "
	gate := []string{"--json", "api", "version", "--require", "api-v1"}
	seen := map[string]bool{"api-v1": true}
	for _, args := range operations {
		cap := apiCapability(args)
		if !seen[cap] {
			gate = append(gate, "--require", cap)
			seen[cap] = true
		}
	}
	script += remoteCommand(t, gate...) + "; tdev_rc=$?; printf '\\n" + marker + " %s\\n' \"$tdev_rc\"; test \"$tdev_rc\" -eq 0 || exit \"$tdev_rc\"; "
	for _, args := range operations {
		argv := append([]string{"--json", "--timeout", a.timeout.String(), "api"}, args...)
		script += remoteCommand(t, argv...) + "; tdev_rc=$?; printf '\\n" + marker + " %s\\n' \"$tdev_rc\"; test \"$tdev_rc\" -eq 0 || exit \"$tdev_rc\"; "
	}
	cmd := sshx.Command(ctx, "ssh", append(t.SSH.Args(t.Batch), "-T", t.Server.SSHAlias, script)...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, processErr := cmd.Output()
	if cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		exitCode = &code
	}
	if ctx.Err() != nil {
		return nil, sshx.Error(ctx.Err(), stderr.String())
	}
	if len(out) == 0 && processErr != nil {
		return nil, sshx.Error(processErr, stderr.String())
	}
	frames, codes, err := splitAPIFrames(out, marker)
	if err != nil {
		return nil, err
	}
	if len(frames) < 2 || codes[0] != 0 || codes[1] != 0 {
		version := "unknown"
		if len(frames) > 0 {
			if data, e := decodeAPI(frames[0]); e == nil {
				var v struct {
					Version string `json:"version"`
				}
				_ = json.Unmarshal(data, &v)
				version = v.Version
			}
		}
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "server %s runs tdev %s; upgrade the server to v0.1.0 or a compatible version (tdev upgrade --remote %s)", t.Name, version, t.Name)
	}
	for i, frame := range frames[2:] {
		data, err := decodeAPI(frame)
		if err != nil {
			return nil, err
		}
		if codes[i+2] != 0 {
			return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "API failed after a successful response")
		}
		results = append(results, data)
	}
	if len(results) != len(operations) {
		return nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "incomplete API batch")
	}
	if processErr != nil {
		return nil, sshx.Error(processErr, stderr.String())
	}
	return results, nil
}

func splitAPIFrames(out []byte, marker string) ([][]byte, []int, error) {
	var frames [][]byte
	var codes []int
	separator := []byte("\n" + marker + " ")
	for len(out) > 0 {
		at := bytes.Index(out, separator)
		if at < 0 {
			return nil, nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "truncated API framing")
		}
		frame := append([]byte{}, out[:at]...)
		out = out[at+len(separator):]
		end := bytes.IndexByte(out, '\n')
		if end < 0 {
			return nil, nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "missing API exit status")
		}
		code, err := strconv.Atoi(string(out[:end]))
		if err != nil || code < 0 || code > 255 {
			return nil, nil, protocol.Fail("PROTOCOL_INCOMPATIBLE", 8, "invalid API exit status: %s", fmt.Sprint(err))
		}
		frames = append(frames, frame)
		codes = append(codes, code)
		out = out[end+1:]
	}
	return frames, codes, nil
}
