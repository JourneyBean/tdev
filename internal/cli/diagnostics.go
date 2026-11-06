package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tdev/internal/protocol"
)

const diagnosticLimit = 1 << 20

type diagnosticEvent struct {
	Time       string `json:"time"`
	PID        int    `json:"pid"`
	Level      string `json:"level"`
	Operation  string `json:"operation"`
	Server     string `json:"server,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Code       string `json:"code,omitempty"`
	Reason     string `json:"reason,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

// Error text can contain remote output, commands, paths or secrets. Persist only
// known diagnostic categories; never persist arbitrary error messages or argv.
func diagnosticReason(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	for _, item := range []struct{ pattern, reason string }{
		{"context deadline exceeded", "deadline_exceeded"},
		{"context canceled", "cancelled"},
		{"cancelled", "cancelled"},
		{"permission denied", "permission_denied"},
		{"host key verification failed", "host_key_verification_failed"},
		{"remote host identification has changed", "host_key_changed"},
		{"could not resolve hostname", "hostname_resolution_failed"},
		{"connection refused", "connection_refused"},
		{"no route to host", "no_route_to_host"},
		{"network is unreachable", "network_unreachable"},
		{"timed out", "connection_timeout"},
		{"connection reset", "connection_reset"},
		{"connection closed", "connection_closed"},
		{"no matches found", "shell_glob_no_match"},
		{"syntax error", "shell_syntax_error"},
		{"bad substitution", "shell_bad_substitution"},
		{"command not found", "command_not_found"},
		{"too many directories", "too_many_directories"},
		{"invalid directory prefix", "invalid_directory_prefix"},
		{"use an absolute remote path", "absolute_path_required"},
		{"executable file not found", "executable_not_found"},
		{"not found", "file_or_command_not_found"},
	} {
		if strings.Contains(s, item.pattern) {
			return item.reason
		}
	}
	return "operation_failed"
}

// Each append/rotation holds a kernel lock shared by all CLI processes. Logging
// has its own short budget so a cancelled operation can still leave diagnostics.
func (a *app) recordDiagnostic(operation, server string, started time.Time, err error, exitCode *int) (string, error) {
	if err == nil && !a.debug {
		return "", nil
	}
	event := diagnosticEvent{Time: time.Now().UTC().Format(time.RFC3339Nano), PID: os.Getpid(), Level: "debug", Operation: operation, Server: server, DurationMS: time.Since(started).Milliseconds(), ExitCode: exitCode}
	if err != nil {
		event.Level, event.Code, event.Reason = "error", protocol.Normalize(err).Code, diagnosticReason(err)
	}
	data, marshalErr := json.Marshal(event)
	if marshalErr != nil {
		return "", marshalErr
	}
	if a.store.Root == "" {
		return "", errors.New("configuration root unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	unlock, lockErr := a.store.Lock(ctx, "diagnostics", "log")
	if lockErr != nil {
		return "", lockErr
	}
	defer unlock()
	dir := filepath.Join(a.store.Root, "logs")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, statErr := os.Lstat(dir)
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("diagnostic directory must be a directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "diagnostics.jsonl")
	info, statErr = os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", statErr
	}
	if statErr == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", errors.New("diagnostic file must be a private regular file")
		}
		if info.Size()+int64(len(data)+1) > diagnosticLimit {
			for i := 3; i >= 1; i-- {
				source := path
				if i > 1 {
					source = fmt.Sprintf("%s.%d", path, i-1)
				}
				if err := os.Rename(source, fmt.Sprintf("%s.%d", path, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return "", err
				}
			}
		}
	}
	f, openErr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if openErr != nil {
		return "", openErr
	}
	_, writeErr := f.Write(append(data, '\n'))
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", err
	}
	return path, nil
}

func (a *app) completionFailure(err error, started time.Time) string {
	path, logErr := a.recordDiagnostic("directory_completion", "", started, err, nil)
	if logErr != nil {
		return "诊断日志写入失败（" + diagnosticReason(logErr) + "）"
	}
	return "诊断日志：" + path
}
