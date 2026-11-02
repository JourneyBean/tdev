package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"golang.org/x/text/unicode/norm"
	"tdev/internal/config"
	"tdev/internal/mount"
	"tdev/internal/protocol"
)

func (a *app) mountCommands() {
	a.command(a.root, "mount <project> <local_path>", "Mount the project root using SSHFS", cobra.ExactArgs(2), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		p, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		if err = a.handshake(ctx, t, "mount-probe-nfc-v1"); err != nil {
			return nil, err
		}
		return mount.New(a.store).Mount(ctx, t.Name, p.Name, p.ID, t.Server.SSHAlias, p.Directory, args[1], t.SSH, func(ctx context.Context, token string) error {
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
				return protocol.Fail("DEPENDENCY_MISSING", 7, "SSHFS backend did not suppress AppleDouble files; metadata guard verification failed")
			}
			return nil
		})
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
		return mount.New(a.store).Unmount(ctx, t.Name, t.Project, path)
	})
}
func (a *app) localMountProbe(local *cobra.Command) {
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
