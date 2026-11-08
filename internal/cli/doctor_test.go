package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/tmux"
)

func TestDoctorExplainsFindings(t *testing.T) {
	tests := []struct {
		name   string
		report doctorReport
		want   []string
		absent []string
	}{
		{name: "stopped is normal", report: doctorReport{Servers: projectListing{"local": {State: "synced", Projects: []discoveredProject{{Project: config.Project{Name: "demo"}, Session: tmux.Status{Status: "missing"}}}}}}, want: []string{"未发现需要处理的问题", "会话尚未启动", "tdev open local/demo"}},
		{name: "offline cache is not live", report: doctorReport{Servers: projectListing{"dev-host": {State: "unreachable", Error: "connection refused", Projects: []discoveredProject{{Project: config.Project{Name: "demo"}, Session: tmux.Status{Status: "running"}}}}}}, want: []string{"本次检查未完成", "网络及 SSH", "connection refused", "缓存"}, absent: []string{"会话正在运行", "未发现需要处理的问题"}},
		{name: "optional dependency", report: doctorReport{Client: doctorClient{Dependencies: map[string]doctorDependency{"sshfs": {Available: false}}}}, want: []string{"SSHFS 挂载需要它", "只使用终端时可忽略"}},
		{name: "override", report: doctorReport{Project: &discoveredProject{Project: config.Project{Name: "demo"}, Session: tmux.Status{Status: "running"}, OverrideError: "identity mismatch"}, projectLabel: "dev-host/demo"}, want: []string{"客户端覆盖配置无法应用", "tdev override dev-host/demo", "identity mismatch"}},
		{name: "configuration read failure", report: doctorReport{checkError: &protocol.Error{Message: "invalid configuration"}}, want: []string{"项目和服务器信息读取失败", "invalid configuration"}, absent: []string{"未发现需要处理的问题"}},
		{name: "incomplete", report: doctorReport{Project: &discoveredProject{Project: config.Project{Name: "demo"}, Session: tmux.Status{Status: "incomplete"}}}, want: []string{"初始化未完成", "启动命令"}, absent: []string{"未发现需要处理的问题"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := tt.report.print(&out); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("missing %q in %s", want, out.String())
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(out.String(), absent) {
					t.Errorf("unexpected %q in %s", absent, out.String())
				}
			}
		})
	}
}

func TestDoctorResultKeepsJSONProtocol(t *testing.T) {
	report := doctorReport{Client: doctorClient{Version: "test"}, Servers: projectListing{"local": {State: "synced"}}}
	var out bytes.Buffer
	a := app{out: &out, json: true}
	if err := a.result(report, nil); err != nil {
		t.Fatal(err)
	}
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Client struct {
				Version string `json:"version"`
			} `json:"client"`
			Servers projectListing `json:"servers"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.Data.Client.Version != "test" || response.Data.Servers["local"].State != "synced" {
		t.Fatalf("unexpected protocol: %s", out.String())
	}
	out.Reset()
	a.json = false
	if err := a.result(report, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "项目和会话信息读取成功") || strings.Contains(out.String(), `"client":`) {
		t.Fatalf("unexpected human output: %s", out.String())
	}
}
