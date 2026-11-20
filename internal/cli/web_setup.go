package cli

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func (a *app) runWebSetup(ctx context.Context, args []string) error {
	if len(args) == 1 {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return err
		}
		return a.setupProjectWeb(ctx, t)
	}

	choice, err := a.webSetupMenu(ctx, "配置 Web", []string{"客户端代理", "项目 Web 路由"})
	if err != nil {
		return err
	}
	if choice == 0 {
		return a.setupClientWeb(ctx)
	}
	prefs, err := a.prefs()
	if err != nil {
		return err
	}
	server, err := a.chooseServer(ctx, prefs, "选择项目所在服务器")
	if err != nil {
		return err
	}
	t, err := a.serverTarget(prefs, server, "")
	if err != nil {
		return err
	}
	data, err := a.call(ctx, t, nil, "project", "list")
	if err != nil {
		return err
	}
	projects, err := decodeProjectDefinitions(data)
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		return config.Invalid("no projects on %s; use tdev create --server %s", server, server)
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Project.Name)
	}
	choice, err = a.webSetupMenu(ctx, "选择项目", names)
	if err != nil {
		return err
	}
	t.Project = names[choice]
	return a.setupProjectWeb(ctx, t)
}

func (a *app) webSetupMenu(ctx context.Context, title string, choices []string) (int, error) {
	fmt.Fprintln(a.errOut)
	for i, choice := range choices {
		fmt.Fprintf(a.errOut, "  %d. %s\n", i+1, choice)
	}
	for {
		value, err := a.prompt(ctx, title+"（输入序号，q 退出）", "1")
		if err != nil {
			return 0, err
		}
		if value == "q" {
			return 0, protocol.Fail("CANCELLED", 130, "web setup cancelled")
		}
		if i, err := strconv.Atoi(value); err == nil && i >= 1 && i <= len(choices) {
			return i - 1, nil
		}
		fmt.Fprintln(a.errOut, "请输入列表中的序号。")
	}
}

func (a *app) setupClientWeb(ctx context.Context) error {
	state, err := a.readClientState()
	if err != nil {
		return err
	}
	previous := state.prefs.HTTP
	updated := previous
	for {
		updated.DomainSuffix, err = a.prompt(ctx, "默认域名后缀", updated.DomainSuffix)
		if err != nil {
			return err
		}
		if config.ValidHTTPDomainSuffix(updated.DomainSuffix) {
			break
		}
		fmt.Fprintln(a.errOut, "请输入小写 DNS 名称，首尾省略点号。")
	}
	updated.Port, err = a.webSetupPort(ctx, "HTTP 监听端口", updated.Port, 0)
	if err != nil {
		return err
	}
	defaultHTTPS := config.DefaultWebHTTPSPort(updated.Port)
	suggestion := updated.httpsPort()
	if suggestion == updated.Port {
		suggestion = defaultHTTPS
	}
	port, err := a.webSetupPort(ctx, "HTTPS 监听端口", suggestion, updated.Port)
	if err != nil {
		return err
	}
	if previous.HTTPSPort != nil || port != defaultHTTPS {
		updated.HTTPSPort = &port
	}
	if reflect.DeepEqual(updated, previous) {
		fmt.Fprintln(a.errOut, "当前客户端 Web 配置已符合输入。")
		return nil
	}
	fmt.Fprintf(a.errOut, "\n默认域名后缀 %s，HTTP 端口 %d，HTTPS 端口 %d\n", updated.DomainSuffix, updated.Port, updated.httpsPort())
	if err := a.confirmWebSetup(ctx, "保存客户端 Web 配置？", true); err != nil {
		return err
	}
	if err := a.saveClientWeb(ctx, previous, updated); err != nil {
		return err
	}
	fmt.Fprintln(a.errOut, "已保存客户端 Web 配置。运行 tdev daemon restart 应用端口和后缀。")
	return nil
}

func (a *app) saveClientWeb(ctx context.Context, previous, updated httpPreferences) error {
	unlock, err := a.store.Lock(ctx, "client", "config")
	if err != nil {
		return err
	}
	defer unlock()
	latest, err := a.readClientState()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(latest.prefs.HTTP, previous) {
		return protocol.Fail("CONFIG_CONFLICT", 4, "client Web settings changed during setup; retry")
	}
	prefs := latest.prefs
	prefs.HTTP = updated
	return a.writeClientState(ctx, latest, prefs, latest.overrides)
}

func (a *app) setupProjectWeb(ctx context.Context, t target) error {
	p, err := a.get(ctx, t)
	if err != nil {
		return err
	}
	routes := make([]string, 0, len(p.HTTP))
	for _, route := range p.HTTP {
		routes = append(routes, fmt.Sprintf("%s  %s → %s", route.Name, route.Hostname, webUpstream(route)))
	}
	choices := append([]string{"添加 Web 路由"}, routes...)
	deleteChoice := len(choices)
	if len(routes) > 0 {
		choices = append(choices, "删除已有 Web 路由")
	}
	name := t.Name + "/" + p.Name
	choice, err := a.webSetupMenu(ctx, "选择 "+name+" 的 Web 路由", choices)
	if err != nil {
		return err
	}
	if choice == deleteChoice {
		return a.deleteProjectWebRoute(ctx, t, p, routes)
	}
	services := slices.Clone(p.HTTP)
	index := choice - 1
	if choice == 0 {
		index = len(services)
		services = append(services, config.HTTPService{TargetHost: "localhost"})
		if len(p.HTTP) == 0 {
			services[index].Name = "web"
			host := strings.ToLower(p.Name)
			if host != "localhost" && config.ValidHTTPDomainSuffix(host) {
				services[index].Hostname = host
			}
		}
	}
	route := services[index]
	for {
		route, err = a.promptWebRoute(ctx, route)
		if err != nil {
			return err
		}
		services[index] = route
		if err = config.ValidateHTTP(services); err == nil {
			_, err = effectiveHTTP(services, t.HTTP.DomainSuffix)
		}
		if err == nil {
			break
		}
		fmt.Fprintln(a.errOut, err)
	}
	if reflect.DeepEqual(p.HTTP, services) {
		fmt.Fprintln(a.errOut, "项目配置已包含这条 Web 路由。")
		return nil
	}
	host := route.WithDomainSuffix(t.HTTP.DomainSuffix).Hostname
	fmt.Fprintf(a.errOut, "\n%s  %s → %s\n", name, host, webUpstream(route))
	if err := a.confirmWebSetup(ctx, "保存项目 Web 路由？", true); err != nil {
		return err
	}
	p.HTTP = services
	if _, err := a.writeProject(ctx, t, p, p.Revision); err != nil {
		return err
	}
	fmt.Fprintf(a.errOut, "已保存项目 %s 的 Web 路由。运行 tdev open %s --only web 启动。\n", name, name)
	fmt.Fprintf(a.errOut, "若该项目已有活动 Web 路由，先运行 tdev web stop %s。\n", name)
	return nil
}

func (a *app) deleteProjectWebRoute(ctx context.Context, t target, p config.Project, routes []string) error {
	name := t.Name + "/" + p.Name
	index, err := a.webSetupMenu(ctx, "选择要删除的 Web 路由", routes)
	if err != nil {
		return err
	}
	route := p.HTTP[index]
	host := route.WithDomainSuffix(t.HTTP.DomainSuffix).Hostname
	fmt.Fprintf(a.errOut, "\n%s  %s  %s → %s\n", name, route.Name, host, webUpstream(route))
	if err := a.confirmWebSetup(ctx, "删除这条 Web 路由？", false); err != nil {
		return err
	}
	p.HTTP = slices.Delete(p.HTTP, index, index+1)
	if _, err := a.writeProject(ctx, t, p, p.Revision); err != nil {
		return err
	}
	fmt.Fprintf(a.errOut, "已从项目 %s 删除 Web 路由 %s。\n", name, route.Name)
	fmt.Fprintf(a.errOut, "若该项目仍有活动 Web 路由，运行 tdev web stop %s。\n", name)
	if len(p.HTTP) > 0 {
		fmt.Fprintf(a.errOut, "运行 tdev open %s --only web 启动剩余路由。\n", name)
	}
	return nil
}

func webUpstream(route config.HTTPService) string {
	return route.Scheme() + "://" + net.JoinHostPort(route.TargetHost, strconv.Itoa(route.TargetPort))
}

func (a *app) promptWebRoute(ctx context.Context, route config.HTTPService) (config.HTTPService, error) {
	var err error
	route.Name, err = a.prompt(ctx, "路由名称", route.Name)
	if err != nil {
		return route, err
	}
	route.Hostname, err = a.prompt(ctx, "Web 域名（短名或完整 .localhost 域名）", route.Hostname)
	if err != nil {
		return route, err
	}
	for {
		scheme, err := a.prompt(ctx, "上游协议（http/https）", route.Scheme())
		if err != nil {
			return route, err
		}
		scheme = strings.ToLower(scheme)
		if scheme == "http" || scheme == "https" {
			if scheme != route.Scheme() {
				route.TargetScheme = scheme
			}
			break
		}
		fmt.Fprintln(a.errOut, "请输入 http 或 https。")
	}
	route.TargetHost, err = a.prompt(ctx, "上游地址（主机名或 IP）", route.TargetHost)
	if err != nil {
		return route, err
	}
	route.TargetPort, err = a.webSetupPort(ctx, "上游端口", route.TargetPort, 0)
	if err != nil {
		return route, err
	}
	auto, err := a.wizardYes(ctx, "随 daemon 启动该路由？", route.AutoStart())
	if err == nil && auto != route.AutoStart() {
		route.Autostart = &auto
	}
	return route, err
}

func (a *app) webSetupPort(ctx context.Context, label string, current, exclude int) (int, error) {
	fallback := ""
	if current >= 1 && current <= 65535 {
		fallback = strconv.Itoa(current)
	}
	for {
		value, err := a.prompt(ctx, label, fallback)
		if err != nil {
			return 0, err
		}
		port, err := strconv.Atoi(value)
		switch {
		case err != nil || port < 1 || port > 65535:
			fmt.Fprintln(a.errOut, "端口需要在 1–65535 之间。")
		case port == exclude:
			fmt.Fprintln(a.errOut, "HTTP 和 HTTPS 监听端口必须不同。")
		default:
			return port, nil
		}
	}
}

func (a *app) confirmWebSetup(ctx context.Context, label string, defaultYes bool) error {
	yes, err := a.wizardYes(ctx, label, defaultYes)
	if err != nil {
		return err
	}
	if !yes {
		return protocol.Fail("CANCELLED", 130, "web setup changes cancelled")
	}
	return nil
}
