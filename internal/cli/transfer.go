package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"tdev/internal/protocol"
	"tdev/internal/sshx"
	"tdev/internal/transfer"
)

func (a *app) localTransfer(local *cobra.Command) {
	g := group(local, "transfer", "Plan, stage and commit exact file/directory copies")
	for _, op := range []string{"describe", "plan", "prepare", "export", "commit", "abort"} {
		op := op
		c := a.command(g, op+" <project>", op+" a transfer on this machine", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
			p, err := a.store.Get(args[0])
			if err != nil {
				return nil, err
			}
			path, _ := c.Flags().GetString("path")
			if path != "" && !filepath.IsAbs(path) {
				path = filepath.Join(p.Directory, path)
			}
			switch op {
			case "describe":
				return transfer.Describe(ctx, path)
			case "export":
				return transfer.Export(ctx, a.store, path)
			case "plan":
				var request struct {
					Destination string            `yaml:"destination"`
					Manifest    transfer.Manifest `yaml:"manifest"`
				}
				if err = a.read(&request); err != nil {
					return nil, err
				}
				destination := request.Destination
				if !filepath.IsAbs(destination) {
					destination = filepath.Join(p.Directory, destination)
				}
				return transfer.PlanDestination(ctx, destination, request.Manifest)
			case "prepare":
				var plan transfer.Plan
				if err = a.read(&plan); err != nil {
					return nil, err
				}
				return transfer.Prepare(a.store, plan)
			case "commit":
				id, _ := c.Flags().GetString("id")
				return transfer.Commit(ctx, a.store, id)
			default:
				id, _ := c.Flags().GetString("id")
				return map[string]string{"status": "removed"}, transfer.Abort(a.store, id)
			}
		})
		if op == "describe" || op == "export" {
			c.Flags().String("path", "", "absolute or project-relative path")
			_ = c.MarkFlagRequired("path")
		}
		if op == "commit" || op == "abort" {
			c.Flags().String("id", "", "owned transfer ID")
			_ = c.MarkFlagRequired("id")
		}
	}
}
func (a *app) transferCommands() {
	for _, op := range []string{"push", "pull"} {
		op := op
		c := &cobra.Command{Use: op + " <project> <source> <destination>", Short: op + " files or directory contents without deleting unrelated files", Args: cobra.ExactArgs(3), RunE: func(c *cobra.Command, args []string) error {
			if _, err := exec.LookPath("scp"); err != nil {
				return protocol.Fail("DEPENDENCY_MISSING", 7, "OpenSSH scp with SFTP support is required")
			}
			t, err := a.resolve(c.Context(), args[0], false)
			if err != nil {
				return err
			}
			if err = a.handshake(c.Context(), t, "transfer-v1"); err != nil {
				return err
			}
			if _, err = a.get(c.Context(), t); err != nil {
				return err
			}
			var result any
			if op == "push" {
				result, err = a.push(c.Context(), t, args[1], args[2])
			} else {
				result, err = a.pull(c.Context(), t, args[1], args[2])
			}
			return a.result(result, err)
		}}
		a.root.AddCommand(c)
	}
}
func (a *app) remoteAbort(t target, id string) {
	t.Batch = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.call(ctx, t, nil, "transfer", "abort", t.Project, "--id", id); err != nil {
		fmt.Fprintf(a.errOut, "Transfer staging may remain (id %s): %v\n", id, err)
	}
}
func (a *app) scp(ctx context.Context, t target, source, destination string) error {
	args := append([]string{"-s", "-q", "-B"}, t.SSH.Args(true)...)
	args = append(args, "--", source, destination)
	cmd := sshx.Command(ctx, "scp", args...)
	var stderr sshx.Buffer
	cmd.Stderr = &stderr
	return sshx.Error(cmd.Run(), stderr.String())
}
func (a *app) push(ctx context.Context, t target, source, destination string) (any, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	manifest, err := transfer.Describe(ctx, source)
	if err != nil {
		return nil, err
	}
	request, _ := json.Marshal(map[string]any{"destination": destination, "manifest": manifest})
	b, err := a.call(ctx, t, request, "transfer", "plan", t.Project)
	if err != nil {
		return nil, err
	}
	var plan transfer.Plan
	if err = json.Unmarshal(b, &plan); err != nil {
		return nil, err
	}
	if plan.Overwrites > 0 {
		if err = a.confirm(fmt.Sprintf("Overwrite %d existing destination files at %s?", plan.Overwrites, plan.Destination)); err != nil {
			return nil, err
		}
	}
	request, _ = json.Marshal(plan)
	b, err = a.call(ctx, t, request, "transfer", "prepare", t.Project)
	if err != nil {
		return nil, err
	}
	var stage transfer.Stage
	if err = json.Unmarshal(b, &stage); err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			a.remoteAbort(t, stage.ID)
		}
	}()
	for i, e := range manifest.Entries {
		if !e.Directory {
			if err = a.scp(ctx, t, filepath.Join(source, e.Path), t.Server.SSHAlias+":"+transfer.StageFile(stage, i)); err != nil {
				return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "upload incomplete: " + err.Error(), Details: stage.Path}
			}
		}
	}
	b, err = a.call(ctx, t, nil, "transfer", "commit", t.Project, "--id", stage.ID)
	if err != nil {
		return nil, err
	}
	complete = true
	return json.RawMessage(b), nil
}
func (a *app) pull(ctx context.Context, t target, source, destination string) (any, error) {
	// Describe before exporting so that declining an overwrite does not create a remote stage.
	b, err := a.call(ctx, t, nil, "transfer", "describe", t.Project, "--path", source)
	if err != nil {
		return nil, err
	}
	var manifest transfer.Manifest
	if err = json.Unmarshal(b, &manifest); err != nil {
		return nil, err
	}
	plan, err := transfer.PlanDestination(ctx, destination, manifest)
	if err != nil {
		return nil, err
	}
	if plan.Overwrites > 0 {
		if err = a.confirm(fmt.Sprintf("Overwrite %d existing local files at %s?", plan.Overwrites, plan.Destination)); err != nil {
			return nil, err
		}
	}
	b, err = a.call(ctx, t, nil, "transfer", "export", t.Project, "--path", source)
	if err != nil {
		return nil, err
	}
	var remote transfer.Stage
	if err = json.Unmarshal(b, &remote); err != nil {
		return nil, err
	}
	defer a.remoteAbort(t, remote.ID)
	// The source must still match the manifest the user approved.
	old, _ := json.Marshal(manifest)
	now, _ := json.Marshal(remote.Plan.Manifest)
	if string(old) != string(now) {
		return nil, protocol.Fail("SOURCE_CHANGED", 4, "source changed after preflight")
	}
	local, err := transfer.Prepare(a.store, plan)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = transfer.Abort(a.store, local.ID)
		}
	}()
	for i, e := range manifest.Entries {
		if !e.Directory {
			if err = a.scp(ctx, t, t.Server.SSHAlias+":"+transfer.StageFile(remote, i), transfer.StageFile(local, i)); err != nil {
				return nil, &protocol.Error{Code: "PARTIAL_SUCCESS", Exit: 9, Message: "download incomplete: " + err.Error(), Details: local.Path}
			}
		}
	}
	result, err := transfer.Commit(ctx, a.store, local.ID)
	if err == nil {
		complete = true
	}
	return result, err
}
