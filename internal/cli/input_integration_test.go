package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"

	"golang.org/x/term"
	"tdev/internal/config"
	"tdev/internal/testssh"
)

func TestWizardEditingAndRemoteCompletion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0-dev")
	version := terminalCLI(t, binary, t.TempDir(), s.Config, "--json", "version")
	version.waitText(t, "protocol_version")
	version.exit(t, 0)
	if strings.Contains(version.output.String(), "\x1b") {
		t.Fatal("noninteractive command emitted terminal queries")
	}
	root, remoteRoot, base := t.TempDir(), t.TempDir(), t.TempDir()
	names := []string{"目录 空格'$(false)", "服务-a", "服务-b", ".隐藏"}
	for _, name := range names {
		if err := os.Mkdir(filepath.Join(base, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "普通文件"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	a := &app{store: config.Store{Root: root}, sshConfig: s.Config, timeout: 5 * time.Second}
	prefs, err := a.prefs()
	if err != nil {
		t.Fatal(err)
	}
	target, err := a.serverTarget(prefs, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	matches, err := a.completeRemoteDirectories(context.Background(), target, base+"/")
	if err != nil || len(matches) != 3 {
		t.Fatal("completion should list visible directories only", matches, err)
	}
	matches, err = a.completeRemoteDirectories(context.Background(), target, base+"/.")
	if err != nil || !reflect.DeepEqual(matches, []string{base + "/.隐藏/"}) {
		t.Fatal("hidden directory completion", matches, err)
	}
	matches, err = a.completeRemoteDirectories(context.Background(), target, base+"/服务-")
	if err != nil || !reflect.DeepEqual(matches, []string{base + "/服务-a/", base + "/服务-b/"}) {
		t.Fatal("multiple candidates", matches, err)
	}
	matches, err = a.completeRemoteDirectories(context.Background(), target, "~")
	if err != nil || !reflect.DeepEqual(matches, []string{"~/"}) {
		t.Fatal("home completion", matches, err)
	}
	// A real editor repairs the project name using Home, End, Left and Delete,
	// then completes a remote path containing CJK, whitespace and shell syntax.
	wizard := terminalCLI(t, binary, root, s.Config, "create", "--server", "fixture")
	wizard.waitText(t, "项目名称（")
	wizard.write(t, "xeditedx\x1b[H\x1b[3~\x1b[F\x1b[D\x1b[3~\r")
	wizard.waitText(t, "Tab 补全")
	wizard.write(t, base+"/目录\t")
	wizard.waitText(t, "$(false)/")
	wizard.write(t, "\r")
	finishProjectWizard(t, wizard)
	wizard.waitText(t, "已创建项目 fixture/edited")
	wizard.exit(t, 0)
	store := config.Store{Root: remoteRoot}
	p, err := store.Get("edited")
	if err != nil || p.Directory != filepath.Join(base, names[0]) {
		t.Fatal("remote completion changed the path", p, err)
	}
	// Exercise selection mode and arrow navigation among multiple candidates.
	choice := terminalCLI(t, binary, root, s.Config, "create", "fixture/choice")
	choice.waitText(t, "Tab 补全")
	choice.write(t, base+"/服务-\t")
	choice.waitText(t, "b/")
	menu := choice.output.String()
	emulator := vt.NewEmulator(100, 30)
	drained := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, emulator); close(drained) }()
	_, renderErr := emulator.Write([]byte(menu))
	screen := emulator.String()
	_ = emulator.InputPipe().(io.Closer).Close()
	<-drained
	if renderErr != nil {
		t.Fatal(renderErr)
	}
	candidateRows := 0
	for _, row := range strings.Split(screen, "\n") {
		if strings.Contains(row, "服务-a/") || strings.Contains(row, "服务-b/") {
			if !strings.HasPrefix(row, "  服务-") {
				t.Fatalf("candidate menu is not aligned: %q\n%s", row, screen)
			}
			candidateRows++
		}
	}
	if candidateRows != 2 {
		t.Fatalf("candidate menu missing from terminal screen:\n%s", screen)
	}
	if strings.Contains(menu, base+"/服务-a/") {
		t.Fatal("candidate menu should show directory names rather than repeat full paths")
	}
	choice.write(t, "\t\x1b[C\r\r")
	finishProjectWizard(t, choice)
	choice.waitText(t, "已创建项目 fixture/choice")
	choice.exit(t, 0)
	p, err = store.Get("choice")
	if err != nil || p.Directory != filepath.Join(base, "服务-b") {
		t.Fatal("completion arrow selection", p, err)
	}
	// Ctrl-C and external cancellation both restore terminal settings.
	for _, mode := range []string{"interrupt", "eof", "signal"} {
		cancelled := terminalCLI(t, binary, root, s.Config, "create", "--server", "fixture")
		before, err := term.GetState(int(cancelled.terminal.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		cancelled.waitText(t, "项目名称（")
		if mode == "signal" {
			_ = cancelled.cmd.Process.Signal(syscall.SIGTERM)
		} else if mode == "eof" {
			cancelled.write(t, "\x04")
		} else {
			cancelled.write(t, "\x03")
		}
		cancelled.exit(t, 130)
		after, err := term.GetState(int(cancelled.terminal.Fd()))
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("terminal settings were not restored", err)
		}
	}
	// Typed CJK remains editable at rune boundaries, including a wrapped path.
	edit := terminalCLI(t, binary, root, s.Config, "create", "fixture/unicode")
	edit.waitText(t, "Tab 补全")
	edit.write(t, base+"/目录 空格'$(false)错\x1b[D\x1b[3~\r")
	finishProjectWizard(t, edit)
	edit.waitText(t, "已创建项目 fixture/unicode")
	edit.exit(t, 0)
	p, err = store.Get("unicode")
	if err != nil || !strings.HasSuffix(p.Directory, names[0]) {
		t.Fatal("CJK cursor editing", p, err)
	}
	// Drop the real SSH transport after the editor is ready. Completion errors
	// must remain visible and leave a private diagnostic without the typed path.
	failureRoot := t.TempDir()
	executeBinary(t, binary, failureRoot, s.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	failure := terminalCLI(t, binary, failureRoot, s.Config, "create", "fixture/failed-completion")
	failure.waitText(t, "Tab 补全")
	s.Offline(true)
	failure.write(t, "~/private-directory-sentinel\t")
	failure.waitText(t, "补全失败")
	failure.waitText(t, "诊断日志：")
	failure.write(t, "\x03")
	failure.exit(t, 130)
	events, logs := diagnosticEvents(t, failureRoot)
	found := false
	for _, event := range events {
		if event.Operation == "directory_completion" && event.Code == "CONNECTION_FAILED" {
			found = true
		}
	}
	if !found || strings.Contains(logs, "private-directory-sentinel") {
		t.Fatal("SSH completion failure was not logged privately", logs)
	}
}
