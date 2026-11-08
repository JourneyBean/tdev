package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"tdev/internal/protocol"
)

type doctorDependency struct {
	Available bool   `json:"available"`
	Path      string `json:"path"`
}

type doctorClient struct {
	OS              string                      `json:"os"`
	Arch            string                      `json:"arch"`
	Version         string                      `json:"version"`
	ProtocolVersion int                         `json:"protocol_version"`
	Dependencies    map[string]doctorDependency `json:"dependencies"`
	ConfigRoot      string                      `json:"config_root"`
}

type doctorReport struct {
	projectLabel string
	checkError   *protocol.Error
	Client       doctorClient       `json:"client"`
	Servers      projectListing     `json:"servers,omitempty"`
	Remote       json.RawMessage    `json:"remote,omitempty"`
	Project      *discoveredProject `json:"project,omitempty"`
	ProjectError *protocol.Error    `json:"project_error,omitempty"`
}

// Interpret existing checks without claiming to have exercised optional features.
func (r doctorReport) print(out io.Writer) error {
	var body bytes.Buffer
	issues := 0
	problem := func(format string, args ...any) {
		issues++
		fmt.Fprintf(&body, format+"\n", args...)
	}
	if r.checkError != nil {
		problem("项目和服务器信息读取失败。请根据原因检查配置或连接后重试：%s", r.checkError.Message)
	}
	fmt.Fprintf(&body, "本机使用 tdev %s（%s/%s）。\n", r.Client.Version, r.Client.OS, r.Client.Arch)
	for _, dep := range []struct{ name, impact string }{
		{"ssh", "远端连接需要它"},
		{"scp", "使用 scp 的文件传输需要它"},
		{"sshfs", "SSHFS 挂载需要它；只使用终端时可忽略"},
		{"umount", "使用 umount 卸载挂载目录时需要它"},
	} {
		if d, checked := r.Client.Dependencies[dep.name]; checked && !d.Available {
			problem("本机找不到 %s，%s。需要该功能时，请安装并确认命令在 PATH 中。", dep.name, dep.impact)
		}
	}
	project := func(label string, p discoveredProject) {
		switch p.Session.Status {
		case "running":
			fmt.Fprintf(&body, "项目 %s 的会话正在运行。\n", label)
		case "missing":
			fmt.Fprintf(&body, "项目 %s 的会话尚未启动，需要使用时运行 tdev open %s。\n", label, label)
		case "incomplete":
			problem("项目 %s 的会话初始化未完成；请检查 tmux 中的窗口、目录及启动命令，再决定是否停止并重建会话。", label)
		case "unmanaged":
			problem("项目 %s 的会话归属需要检查；请先用 tmux list-sessions 核对现有会话。", label)
		default:
			problem("项目 %s 的会话状态尚未确认，请重试 tdev doctor %s。", label, label)
		}
		if warning := p.Session.Warning; warning != "" {
			switch {
			case strings.HasPrefix(warning, "multiple tmux sessions carry"):
				problem("项目 %s 有多个 tmux 会话携带同一项目标记，无法唯一确定归属；请核对各会话的项目标记后处理重复项。", label)
			case strings.Contains(warning, "has no matching project UUID"):
				problem("项目 %s 的同名 tmux 会话未匹配项目标记，现有会话已保留；open 会使用独立的托管会话。", label)
			case strings.HasPrefix(warning, "project session is named"):
				problem("项目 %s 的会话名称与项目名不同，已通过项目标记找到会话，可以继续使用。当前会话名：%s。", label, p.Session.SessionName)
			default:
				problem("项目 %s 有会话警告：%s", label, warning)
			}
		}
		if p.OverrideError != "" {
			problem("项目 %s 的客户端覆盖配置无法应用，当前展示原始项目配置。请检查 tdev override %s；确认放弃覆盖时可使用 --reset。原因：%s", label, label, p.OverrideError)
		}
	}
	for _, name := range listingNames(r.Servers) {
		entry := r.Servers[name]
		if entry.State != "synced" || entry.Error != "" {
			advice := "请根据原因检查配置后重试。"
			switch entry.State {
			case "unreachable":
				advice = "请检查网络及 SSH 连接配置后重试。"
			case "upgrade-required":
				advice = "请检查客户端与服务端版本，并按错误提示升级。"
			}
			problem("%s 的本次检查未完成。%s 原因：%s", name, advice, entry.Error)
			if len(entry.Projects) > 0 {
				fmt.Fprintf(&body, "  保留了 %d 个项目的缓存；这些数据无法确认当前会话状态。\n", len(entry.Projects))
			}
			continue
		}
		fmt.Fprintf(&body, "%s 的项目和会话信息读取成功，共 %d 个项目。\n", name, len(entry.Projects))
		if entry.ScratchWarning != "" {
			problem("%s 的临时会话信息检查有提示：%s", name, entry.ScratchWarning)
		}
		for _, p := range entry.Projects {
			project(name+"/"+p.Project.Name, p)
		}
	}
	if len(r.Remote) > 0 {
		var remote struct {
			Version struct {
				Version string `json:"version"`
			} `json:"version"`
			Runtime struct {
				Tmux  string `json:"tmux"`
				Shell string `json:"shell"`
			} `json:"runtime"`
		}
		if err := json.Unmarshal(r.Remote, &remote); err != nil {
			problem("目标机器的检查结果无法解析，请检查两端 tdev 的版本。")
		} else {
			fmt.Fprintf(&body, "目标机器使用 tdev %s；%s 可执行，shell 路径 %s 存在。\n", remote.Version.Version, remote.Runtime.Tmux, remote.Runtime.Shell)
		}
	}
	if r.Project != nil {
		label := r.projectLabel
		if label == "" {
			label = r.Project.Project.Name
		}
		project(label, *r.Project)
	}
	if r.ProjectError != nil {
		problem("目标机器的基础检查已完成，但项目信息读取失败。请根据原因检查项目配置：%s", r.ProjectError.Message)
	}
	summary := "本次检查未发现需要处理的问题。"
	if issues > 0 {
		summary = "本次检查有以下提示，请按需要处理："
	}
	_, err := fmt.Fprintf(out, "%s\n%s", summary, body.String())
	return err
}
