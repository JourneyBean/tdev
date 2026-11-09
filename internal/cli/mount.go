package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
	"tdev/internal/config"
	"tdev/internal/daemon"
	"tdev/internal/mount"
	"tdev/internal/protocol"
)

func (a *app) mountCommands() {

	a.command(a.root, "mount <project> [local_path]", "Mount the project root using SSHFS", cobra.RangeArgs(1, 2), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		path := ""
		if len(args) == 2 {
			path = args[1]
		}
		return a.mountProject(ctx, t, p, path)
	})
	a.command(a.root, "unmount <project> [local_path]", "Unmount only an identity-matching managed mount, without force", cobra.RangeArgs(1, 2), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		path := ""
		if len(args) == 2 {
			path = args[1]
		}
		if t.Local {
			return map[string]string{"status": "local"}, nil
		}
		return mount.New(a.store).Unmount(ctx, t.Name, t.Project, path)
	})
}
func (a *app) mountProject(ctx context.Context, t target, p config.Project, path string) (any, error) {
	if t.Local {
		return map[string]string{"status": "local", "directory": p.Directory}, nil
	}
	if path == "" {
		path = t.Server.Projects[p.Name].MountPath
	}
	if path == "" {
		if a.json || !terminalAvailable(a.in) {
			return nil, config.Invalid("specify a mount directory or set tdev override --mount-path")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path, err = a.prompt(ctx, "本机挂载目录", filepath.Join(home, "tdev", t.Name, p.Name))
		if err != nil {
			return nil, err
		}
	}
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	if a.useDaemon && !a.daemonWorker {
		t, err = daemonTarget(t)
		if err != nil {
			return nil, err
		}
		client, err := a.daemonClient(ctx)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(mountServiceRequest{Target: t, Project: p, Path: path})
		if err != nil {
			return nil, err
		}
		state, err := client.Call(ctx, daemon.Request{Op: "mount", Payload: payload, Owner: t.Lease})
		if err != nil {
			return nil, err
		}
		return state.Result, nil
	}
	result, err := mount.New(a.store).Mount(ctx, t.Name, p.Name, p.ID, t.Server.SSHAlias, p.Directory, path, t.SSH, func(ctx context.Context, token string) error {
		b, err := a.call(ctx, t, nil, "mount-probe", p.Name, "--token", token)
		if err != nil {
			return err
		}
		var result struct {
			Created bool `json:"created"`
		}
		if err = json.Unmarshal(b, &result); err != nil {
			return err
		}
		if result.Created {
			return protocol.Fail("DEPENDENCY_MISSING", 7, "SSHFS backend did not suppress AppleDouble files")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = a.saveOverride(ctx, t, p, func(o *projectOverride) error { o.ProjectID = p.ID; o.MountPath = result.Path; return nil })
	if err != nil {
		return nil, protocol.Fail("PARTIAL_SUCCESS", 9, "mounted successfully; mount preference could not be saved: %v", err)
	}
	return result, nil
}

func (a *app) apiMountProbe(local *cobra.Command) {
	c := a.command(local, "mount-probe <project>", "Inspect and remove only a unique tdev metadata probe", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		p, err := a.store.Get(args[0])
		if err != nil {
			return nil, err
		}
		token, _ := c.Flags().GetString("token")
		if !config.ValidName(token) {
			return nil, config.Invalid("invalid metadata probe token")
		}
		expected := mount.ProbeName(token)
		entries, err := os.ReadDir(p.Directory)
		if err != nil {
			return nil, err
		}
		found, metadata, changedNormalization := false, false, false
		for _, entry := range entries {
			name := entry.Name()
			canonical := norm.NFC.String(name)
			if canonical != expected && canonical != "._"+expected {
				continue
			}
			path := filepath.Join(p.Directory, name)
			if canonical == expected {
				info, err := entry.Info()
				if err != nil {
					return nil, err
				}
				if !info.Mode().IsRegular() {
					return nil, config.Invalid("metadata probe identity changed")
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				if string(data) != "tdev mount verification" {
					return nil, config.Invalid("metadata probe content mismatch; file retained")
				}
				found = true
				changedNormalization = name != expected
			} else {
				metadata = true
			}
			if err = os.Remove(path); err != nil {
				return nil, err
			}
		}
		if !found || changedNormalization {
			return nil, protocol.Fail("MOUNT_UNVERIFIED", 7, "SSHFS UTF-8/CJK filename probe did not preserve the expected remote NFC name")
		}
		return map[string]bool{"created": metadata, "utf8_verified": true}, nil
	})
	c.Flags().String("token", "", "unique probe token")
	_ = c.MarkFlagRequired("token")
}
