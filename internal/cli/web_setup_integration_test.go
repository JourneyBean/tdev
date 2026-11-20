package cli

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"tdev/internal/config"
	"tdev/internal/testssh"
)

func webSetupAnswer(t *testing.T, wizard *terminalProcess, prompt, answer string) {
	t.Helper()
	wizard.waitText(t, prompt)
	wizard.write(t, answer+"\r")
}

func TestWebSetupClientWizard(t *testing.T) {
	root := t.TempDir()
	main := "client:\n  include: [client-prefs.yaml]\n"
	clientConfigFile(t, root, "config.yaml", main)
	clientConfigFile(t, root, "client-prefs.yaml", "http:\n  port: 24880\n")
	binary := buildCLI(t, "0.9.0-dev")
	wizard := terminalCLI(t, binary, root, "", "web", "setup")
	webSetupAnswer(t, wizard, "配置 Web（输入序号", "1")
	webSetupAnswer(t, wizard, "默认域名后缀", "dev.localhost")
	webSetupAnswer(t, wizard, "HTTP 监听端口", "28480")
	wizard.waitText(t, "HTTPS 监听端口")
	wizard.write(t, "28480\r")
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, rest, _ := strings.Cut(wizard.output.String(), "HTTP 和 HTTPS 监听端口必须不同")
		if strings.Contains(rest, "HTTPS 监听端口") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wizard did not retry the HTTPS port: %q", wizard.output.String())
		}
		time.Sleep(15 * time.Millisecond)
	}
	wizard.write(t, "28481\r")
	webSetupAnswer(t, wizard, "保存客户端 Web 配置", "y")
	wizard.exit(t, 0)

	prefs, err := (&app{store: config.Store{Root: root}}).prefs()
	if err != nil || prefs.HTTP.DomainSuffix != "dev.localhost" || prefs.HTTP.Port != 28480 || prefs.HTTP.httpsPort() != 28481 {
		t.Fatalf("client Web config: %+v %v", prefs.HTTP, err)
	}
	content, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil || string(content) != main {
		t.Fatalf("client include was not preserved: %s %v", content, err)
	}
	content, err = os.ReadFile(filepath.Join(root, "client-prefs.yaml"))
	if err != nil || !strings.Contains(string(content), "https_port: 28481") {
		t.Fatalf("client source was not updated: %s %v", content, err)
	}
	executeBinary(t, binary, root, "", "", 2, "web", "setup")
}

func TestWebSetupProjectWizard(t *testing.T) {
	root := t.TempDir()
	store := config.Store{Root: root}
	original, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.9.0-dev")
	wizard := terminalCLI(t, binary, root, "", "web", "setup")
	webSetupAnswer(t, wizard, "配置 Web（输入序号", "2")
	webSetupAnswer(t, wizard, "输入序号或服务器名", "")
	webSetupAnswer(t, wizard, "选择项目（输入序号", "1")
	webSetupAnswer(t, wizard, "选择 local/demo 的 Web 路由（输入序号", "1")
	webSetupAnswer(t, wizard, "路由名称", "")
	webSetupAnswer(t, wizard, "Web 域名", "")
	webSetupAnswer(t, wizard, "上游协议", "https")
	webSetupAnswer(t, wizard, "上游地址", "::1")
	webSetupAnswer(t, wizard, "上游端口", "8443")
	webSetupAnswer(t, wizard, "随 daemon 启动", "n")
	webSetupAnswer(t, wizard, "保存项目 Web 路由", "y")
	wizard.exit(t, 0)

	added, err := store.Get("demo")
	if err != nil || added.Revision != original.Revision+1 || len(added.HTTP) != 1 {
		t.Fatalf("added project route: %+v %v", added, err)
	}
	h := added.HTTP[0]
	if h.Name != "web" || h.Hostname != "demo" || h.Scheme() != "https" || h.TargetHost != "::1" || h.TargetPort != 8443 || h.AutoStart() {
		t.Fatalf("added service: %+v", h)
	}

	edit := terminalCLI(t, binary, root, "", "web", "setup", "local/demo")
	webSetupAnswer(t, edit, "选择 local/demo 的 Web 路由（输入序号", "2")
	webSetupAnswer(t, edit, "路由名称", "app")
	webSetupAnswer(t, edit, "Web 域名", "app.demo.localhost")
	webSetupAnswer(t, edit, "上游协议", "http")
	webSetupAnswer(t, edit, "上游地址", "127.0.0.1")
	webSetupAnswer(t, edit, "上游端口", "8080")
	webSetupAnswer(t, edit, "随 daemon 启动", "")
	webSetupAnswer(t, edit, "保存项目 Web 路由", "y")
	edit.exit(t, 0)

	modified, err := store.Get("demo")
	if err != nil || modified.Revision != original.Revision+2 || len(modified.HTTP) != 1 {
		t.Fatalf("edited project route: %+v %v", modified, err)
	}
	h = modified.HTTP[0]
	if h.Name != "app" || h.Hostname != "app.demo.localhost" || h.Scheme() != "http" || h.TargetHost != "127.0.0.1" || h.TargetPort != 8080 || h.AutoStart() {
		t.Fatalf("edited service: %+v", h)
	}
	modified.HTTP, modified.Revision = original.HTTP, original.Revision
	if !reflect.DeepEqual(modified, original) {
		t.Fatalf("changed unrelated project fields: before %+v after %+v", original, modified)
	}
}

func TestWebSetupRemoteProjectRevisionConflict(t *testing.T) {
	ssh := testssh.Start(t)
	root, remote := t.TempDir(), t.TempDir()
	store := config.Store{Root: remote}
	original, err := store.Create(context.Background(), "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.9.0-dev")
	executeBinary(t, binary, root, ssh.Config, "", 0, "setup", "--name", "fixture", "--ssh-alias", ssh.Alias, "--remote-binary", binary, "--remote-config-dir", remote)

	for _, conflict := range []bool{true, false} {
		wizard := terminalCLI(t, binary, root, ssh.Config, "web", "setup", "fixture/demo")
		webSetupAnswer(t, wizard, "选择 fixture/demo 的 Web 路由（输入序号", "1")
		webSetupAnswer(t, wizard, "路由名称", "")
		webSetupAnswer(t, wizard, "Web 域名", "")
		webSetupAnswer(t, wizard, "上游协议", "https")
		webSetupAnswer(t, wizard, "上游地址", "")
		webSetupAnswer(t, wizard, "上游端口", "8443")
		webSetupAnswer(t, wizard, "随 daemon 启动", "")
		wizard.waitText(t, "保存项目 Web 路由")
		if conflict {
			_, err = store.Update(context.Background(), "demo", original.Revision, func(p *config.Project) error {
				p.Windows[0].Name = "updated"
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wizard.write(t, "y\r")
			wizard.exit(t, 4)
			got, err := store.Get("demo")
			if err != nil || len(got.HTTP) != 0 {
				t.Fatalf("stale wizard wrote remote routes: %+v %v", got, err)
			}
			continue
		}
		wizard.write(t, "y\r")
		wizard.exit(t, 0)
	}
	p, err := store.Get("demo")
	if err != nil || p.Revision != original.Revision+2 || len(p.HTTP) != 1 || p.HTTP[0].Scheme() != "https" ||
		p.HTTP[0].TargetPort != 8443 || p.Windows[0].Name != "updated" {
		t.Fatalf("remote Web route: %+v %v", p, err)
	}
	projects, err := (config.Store{Root: root}).List()
	if err != nil || len(projects) != 0 {
		t.Fatalf("remote route stored on client: %+v %v", projects, err)
	}
}

func TestWebSetupDeleteProjectRoute(t *testing.T) {
	root := t.TempDir()
	store := config.Store{Root: root}
	ctx := context.Background()
	original, err := store.Create(ctx, "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := config.HTTPService{Name: "web", Hostname: "demo", TargetHost: "localhost", TargetPort: 8080}
	second := config.HTTPService{Name: "api", Hostname: "api", TargetScheme: "https", TargetHost: "localhost", TargetPort: 8443}
	original, err = store.Update(ctx, "demo", original.Revision, func(p *config.Project) error {
		p.HTTP = []config.HTTPService{first, second}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	binary := buildCLI(t, "0.9.0-dev")

	for _, attempt := range []struct {
		name, action, selection, host, confirm string
		revision, exit                         int
		routes                                 []config.HTTPService
	}{
		{name: "default declines", action: "4", selection: "2", host: "api.tdev.localhost", revision: original.Revision, exit: 130, routes: original.HTTP},
		{name: "delete selected route", action: "4", selection: "2", host: "api.tdev.localhost", confirm: "y", revision: original.Revision + 1, routes: []config.HTTPService{first}},
		{name: "delete last route", action: "3", selection: "1", host: "demo.tdev.localhost", confirm: "y", revision: original.Revision + 2},
	} {
		wizard := terminalCLI(t, binary, root, "", "web", "setup", "local/demo")
		webSetupAnswer(t, wizard, "选择 local/demo 的 Web 路由（输入序号", attempt.action)
		webSetupAnswer(t, wizard, "选择要删除的 Web 路由（输入序号", attempt.selection)
		wizard.waitText(t, attempt.host)
		webSetupAnswer(t, wizard, "删除这条 Web 路由", attempt.confirm)
		wizard.exit(t, attempt.exit)

		p, err := store.Get("demo")
		if err != nil || p.Revision != attempt.revision || !slices.Equal(p.HTTP, attempt.routes) {
			t.Fatalf("%s: project after deletion prompt: %+v %v", attempt.name, p, err)
		}
		p.HTTP, p.Revision = original.HTTP, original.Revision
		if !reflect.DeepEqual(p, original) {
			t.Fatalf("changed unrelated project fields: before %+v after %+v", original, p)
		}
	}
}
