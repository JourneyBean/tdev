package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/testssh"
	"tdev/internal/tmux"
)

func TestLocalProjectsAndNamedShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux required")
	}
	socket := "tdev-local-test-" + config.NewID()
	t.Setenv("TDEV_TMUX_SOCKET", socket)
	t.Setenv("TMUX", "")
	t.Cleanup(func() { _ = exec.Command("tmux", "-u", "-L", socket, "-f", "/dev/null", "kill-server").Run() })
	root, directory := t.TempDir(), t.TempDir()
	code, r := run(t, root, "", "create", "demo", "--directory", directory)
	if code != 0 || !r.OK {
		t.Fatalf("local create: %d %+v", code, r)
	}
	store := config.Store{Root: root}
	p, err := store.Get("demo")
	if err != nil || p.Windows[0].Name != "main" {
		t.Fatal("default window", p, err)
	}
	if _, err = os.Stat(filepath.Join(root, "client.yaml")); !os.IsNotExist(err) {
		t.Fatal("local workflow requires no setup", err)
	}
	code, r = run(t, root, "", "api", "session", "ensure", "demo")
	if code != 0 {
		t.Fatal(r)
	}
	m := tmux.Manager{Store: store, Socket: socket}
	status, err := m.Status(context.Background(), p)
	if err != nil || status.SessionName != "tdev-project-demo" {
		t.Fatal(status, err)
	}
	code, r = run(t, root, "", "sh", "demo", "--name", "debug", "--save")
	if code != 0 {
		t.Fatal(r)
	}
	p, err = store.Get("demo")
	if err != nil || len(p.Windows) != 2 || p.Windows[1].Name != "debug" {
		t.Fatal("named persisted shell", p, err)
	}
	code, r = run(t, root, "", "ls")
	if code != 0 {
		t.Fatal(r)
	}
	data, _ := json.Marshal(r.Data)
	var listing projectListing
	if json.Unmarshal(data, &listing) != nil || listing["local"].Location != "local" || len(listing["local"].Projects) != 1 {
		t.Fatal(string(data))
	}
	// The local transfer path uses the same staging/hash checks without SSH/SFTP.
	source := filepath.Join(t.TempDir(), "中文 文件")
	if err = os.WriteFile(source, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	code, r = run(t, root, "", "push", "local/demo", source, "copied")
	if code != 0 {
		t.Fatal(r)
	}
	destination := filepath.Join(t.TempDir(), "pulled")
	code, r = run(t, root, "", "pull", "demo", "copied", destination)
	if code != 0 {
		t.Fatal(r)
	}
	if b, err := os.ReadFile(destination); err != nil || string(b) != "payload" {
		t.Fatal(string(b), err)
	}
	binary := buildCLI(t, "0.1.0")
	open := terminalCLI(t, binary, root, "", "open", "local/demo")
	open.waitText(t, "\x1b]2;tdev: local/demo\x07")
	open.write(t, "printf 'LOCAL_WORKSPACE_READY\\n'\r")
	open.waitText(t, "LOCAL_WORKSPACE_READY\r\n")
	open.write(t, "sleep 30\r")
	time.Sleep(100 * time.Millisecond)
	open.write(t, "\x03")
	open.write(t, "printf 'LOCAL_INTERRUPT_READY\\n'\r")
	open.waitText(t, "LOCAL_INTERRUPT_READY\r\n")
	open.write(t, "tmux detach-client\r")
	open.exit(t, 0)
	code, r = run(t, root, "", "stop", "local/demo", "-y")
	if code != 0 {
		t.Fatal(r)
	}
	for _, args := range [][]string{{"local", "version"}, {"project", "list"}, {"window", "list"}} {
		var out, stderr bytes.Buffer
		if code := Execute(append([]string{"--config-dir", root}, args...), strings.NewReader(""), &out, &stderr); code == 0 {
			t.Fatalf("removed command accepted: %v", args)
		}
	}
}

func TestAPIFrameValidation(t *testing.T) {
	frames, codes, err := splitAPIFrames([]byte("first\n\nMARK 0\nsecond\n\nMARK 4\n"), "MARK")
	if err != nil || len(frames) != 2 || string(frames[1]) != "second\n" || codes[1] != 4 {
		t.Fatal(frames, codes, err)
	}
	for _, text := range []string{"missing", "a\nMARK x\n", "a\nMARK 0", "a\nMARK 0\ntrailing"} {
		if _, _, err := splitAPIFrames([]byte(text), "MARK"); err == nil {
			t.Fatal("accepted invalid frame", text)
		}
	}
}

// The real SSH workflow validates framing, stdin preservation, per-operation
// errors and the number of transport connections, rather than mocking the CLI.
func TestShellBatchAndOldServerGate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0")
	store := config.Store{Root: t.TempDir()}
	p, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &app{store: config.Store{Root: t.TempDir()}, timeout: 5 * time.Second, json: true, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	opts := sshx.Defaults()
	opts.ConfigFile = s.Config
	target := target{Name: "dev", Server: server{SSHAlias: s.Alias, RemoteBinary: binary, RemoteConfigDir: store.Root}, SSH: opts}
	before := s.Accepted.Load()
	results, err := a.calls(context.Background(), target, nil, []string{"project", "get", "demo"}, []string{"session", "status", "demo"})
	if err != nil || len(results) != 2 {
		t.Fatal(results, err)
	}
	if n := s.Accepted.Load() - before; n != 1 {
		t.Fatalf("batch used %d SSH connections", n)
	}
	p.Windows[0].Name = "窗口 '$(false)'"
	data, _ := json.Marshal(p)
	before = s.Accepted.Load()
	if _, err = a.call(context.Background(), target, data, "project", "set", "demo", "--stdin", "--expected-revision", "1"); err != nil {
		t.Fatal(err)
	}
	if n := s.Accepted.Load() - before; n != 1 {
		t.Fatalf("write used %d SSH connections", n)
	}
	fresh, err := store.Get("demo")
	if err != nil || fresh.Windows[0].Name != p.Windows[0].Name {
		t.Fatal("stdin or quoting changed", fresh, err)
	}
	if _, err = a.call(context.Background(), target, data, "project", "set", "demo", "--stdin", "--expected-revision", "1"); protocol.Normalize(err).Code != "CONFIG_CONFLICT" {
		t.Fatal("lost API exit code", err)
	}
	old := filepath.Join(t.TempDir(), "old tdev '$(false)'")
	invoked := filepath.Join(t.TempDir(), "invoked")
	version := `{"protocol_version":1,"ok":true,"data":{"version":"0.0.3","protocol_version":1,"schema_version":1}}`
	script := "#!/bin/sh\nif [ \"$1\" = --json ] && [ \"$2\" = version ]; then printf '%s\\n' " + tmux.Quote(version) + "; exit 0; fi\nprintf '%s\\n' \"$*\" >> " + tmux.Quote(invoked) + "\nprintf '%s\\n' '{\"protocol_version\":1,\"ok\":false,\"error\":{\"code\":\"INVALID_ARGUMENT\",\"message\":\"unknown command api\"}}'\nexit 2\n"
	if err = os.WriteFile(old, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	target.Server.RemoteBinary = old
	target.Server.RemoteConfigDir = ""
	_, err = a.call(context.Background(), target, data, "project", "set", "demo", "--stdin", "--expected-revision", "2")
	if protocol.Normalize(err).Code != "PROTOCOL_INCOMPATIBLE" || !strings.Contains(err.Error(), "0.0.3") || !strings.Contains(err.Error(), "upgrade --remote dev") {
		t.Fatal("missing upgrade guidance", err)
	}
	attempted, _ := os.ReadFile(invoked)
	if strings.Contains(string(attempted), "project set") || !strings.Contains(string(attempted), "api version") {
		t.Fatal("incompatible server received a write", string(attempted))
	}
}

func TestDiscoveryCacheRefreshAndOffline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0")
	root, remoteRoot := t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	if _, err := store.Create(context.Background(), "demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	a := &app{store: config.Store{Root: root}, sshConfig: s.Config, timeout: time.Second, json: true, out: &bytes.Buffer{}, errOut: &bytes.Buffer{}}
	command := &cobra.Command{}
	command.Flags().Bool("all", false, "")
	command.Flags().String("server", "", "")
	data, err := a.listProjects(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	listing := data.(projectListing)
	if listing["dev"].State != "synced" || len(listing["dev"].Projects) != 1 {
		t.Fatal(listing)
	}
	if _, err = store.Create(context.Background(), "new", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	data, err = a.listProjects(context.Background(), command)
	if err != nil || len(data.(projectListing)["dev"].Projects) != 2 {
		t.Fatal(data, err)
	}
	view := terminalCLI(t, binary, root, s.Config, "ls")
	view.waitText(t, "dev [syncing]")
	view.waitText(t, "demo")
	view.exit(t, 0)
	// A successful exit completes the refresh; healthy servers have no status footer.
	finalView := view.output.String()
	if redraw := strings.LastIndex(finalView, "\x1b[J"); redraw >= 0 {
		finalView = finalView[redraw+len("\x1b[J"):]
	}
	if !strings.Contains(finalView, "new") || strings.Contains(finalView, "[syncing]") {
		t.Fatal("interactive refresh did not finish", finalView)
	}
	prefs, _ := a.prefs()
	target, _ := a.serverTarget(prefs, "dev", "")
	path, err := a.listingCachePath(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("cache permissions", info, err)
	}
	// Local names win over cached remote names, with no network lookup.
	if _, err = a.store.Create(context.Background(), "demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	s.Offline(true)
	found, err := a.resolve(context.Background(), "demo", false)
	if err != nil || !found.Local {
		t.Fatal("local-first resolution", found, err)
	}
	data, err = a.listProjects(context.Background(), command)
	if protocol.Normalize(err).Code != "PARTIAL_SUCCESS" {
		t.Fatal("offline list", err)
	}
	offline := data.(projectListing)["dev"]
	if offline.State != "unreachable" || len(offline.Projects) != 2 || offline.UpdatedAt.IsZero() {
		t.Fatal("cached projects lost", offline)
	}
	found, err = a.resolve(context.Background(), "new", false)
	if err != nil || found.Name != "dev" {
		t.Fatal("cached project resolution", found, err)
	}
	if _, err = a.get(context.Background(), found); err == nil {
		t.Fatal("executed against stale configuration")
	}
	s.Offline(false)
	data, err = a.listProjects(context.Background(), command)
	if err != nil || data.(projectListing)["dev"].State != "synced" {
		t.Fatal(data, err)
	}
}

func TestCreateAndEditSharedMappingWizard(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0")
	root, remoteRoot := t.TempDir(), t.TempDir()
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	directory := t.TempDir()
	create := terminalCLI(t, binary, root, s.Config, "create", "dev/demo")
	create.waitText(t, "项目目录（")
	create.write(t, directory+"\r")
	create.waitText(t, "配置启动窗口？")
	create.write(t, "n\r")
	create.waitText(t, "添加端口映射？")
	create.write(t, "y\r")
	create.waitText(t, "方向（")
	create.write(t, "L\r")
	create.waitText(t, "服务地址（")
	create.write(t, "localhost:8000\r")
	create.waitText(t, "本机监听端口")
	priorAdd := strings.Count(create.output.String(), "添加端口映射？")
	create.write(t, "18000\r")
	// Wait for the preview of the completed first mapping before answering the loop.
	create.waitTextAfter(t, "添加端口映射？", priorAdd)
	create.write(t, "n\r")
	create.waitText(t, "保存项目配置？")
	create.write(t, "y\r")
	create.waitText(t, "已创建项目 dev/demo")
	create.exit(t, 0)
	store := config.Store{Root: remoteRoot}
	p, err := store.Get("demo")
	if err != nil || p.Windows[0].Name != "main" || len(p.Forwards) != 1 || p.Forwards[0].ListenPort != 8000 {
		t.Fatal("shared defaults", p, err)
	}
	a := &app{store: config.Store{Root: root}}
	overrides, err := a.readOverrides()
	if err != nil {
		t.Fatal(err)
	}
	patch := overrides.Servers["dev"]["demo"].Forwards[p.Forwards[0].ID]
	if patch.ListenPort == nil || *patch.ListenPort != 18000 {
		t.Fatal("client port override", patch)
	}
	edit := terminalCLI(t, binary, root, s.Config, "edit", "dev/demo")
	edit.waitText(t, "项目目录（")
	edit.write(t, "\r")
	edit.waitText(t, "配置启动窗口？")
	edit.write(t, "y\r")
	edit.waitText(t, "保留窗口 main？")
	edit.write(t, "y\r")
	edit.waitText(t, "窗口名称")
	edit.write(t, "app\r")
	edit.waitText(t, "窗口目录（")
	edit.write(t, "\r")
	edit.waitText(t, "启动命令（")
	edit.write(t, "printf '你好'\r")
	edit.waitText(t, "添加启动窗口？")
	edit.write(t, "n\r")
	edit.waitText(t, "配置已有端口映射？")
	edit.write(t, "n\r")
	edit.waitText(t, "添加端口映射？")
	edit.write(t, "n\r")
	edit.waitText(t, "保存项目配置？")
	edit.write(t, "y\r")
	edit.exit(t, 0)
	p, err = store.Get("demo")
	if err != nil || p.Revision != 2 || p.Windows[0].Name != "app" || p.Windows[0].Command != "printf '你好'" {
		t.Fatal("shared edit wizard", p, err)
	}
}

func (p *terminalProcess) waitTextAfter(t *testing.T, text string, prior int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for strings.Count(p.output.String(), text) <= prior {
		if time.Now().After(deadline) {
			t.Fatalf("waiting for next %q: %q", text, p.output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestOpenMountIsExplicitAndStandaloneMountRemains(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("private sshd integration runs on Linux")
	}
	s := testssh.Start(t)
	binary := buildCLI(t, "0.1.0")
	root, remoteRoot := t.TempDir(), t.TempDir()
	store := config.Store{Root: remoteRoot}
	if _, err := store.Create(context.Background(), "demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	executeBinary(t, binary, root, s.Config, "", 0, "setup", "--name", "dev", "--ssh-alias", s.Alias, "--remote-binary", binary, "--remote-config-dir", remoteRoot)
	mountPath := filepath.Join(t.TempDir(), "nested", "demo")
	executeBinary(t, binary, root, s.Config, "", 0, "override", "dev/demo", "--mount-path", mountPath)
	toolsDir := t.TempDir()
	called := filepath.Join(toolsDir, "called")
	if err := os.WriteFile(filepath.Join(toolsDir, "sshfs"), []byte("#!/bin/sh\nprintf called > "+tmux.Quote(called)+"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolsDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The supervisor resolves local tools from its startup environment.
	executeBinary(t, binary, root, s.Config, "", 0, "daemon", "restart")
	open := terminalCLI(t, binary, root, s.Config, "open", "dev/demo")
	open.write(t, "printf 'OPEN_WITHOUT_MOUNT\\n'\r")
	open.waitText(t, "OPEN_WITHOUT_MOUNT\r\n")
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatal("open mounted without --mount", err)
	}
	open.write(t, "tmux detach-client\r")
	open.exit(t, 0)
	withMount := terminalCLI(t, binary, root, s.Config, "open", "dev/demo", "--mount")
	withMount.waitText(t, "SSHFS exited before mounting")
	withMount.exit(t, 1)
	if _, err := os.Stat(called); err != nil {
		t.Fatal("--mount did not invoke SSHFS", err)
	}
	// No FUSE mount is attempted: the private executable deliberately exits.
	executeBinary(t, binary, root, s.Config, "", 1, "mount", "dev/demo")
	executeBinary(t, binary, root, s.Config, "", 0, "stop", "dev/demo", "-y")
}
