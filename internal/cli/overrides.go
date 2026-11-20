package cli

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/spf13/cobra"
	"tdev/internal/config"
	"tdev/internal/tmux"
)

type forwardOverride struct {
	ListenPort  *int    `yaml:"listen_port,omitempty" json:"listen_port,omitempty"`
	BindAddress *string `yaml:"bind_address,omitempty" json:"bind_address,omitempty"`
	Disabled    bool    `yaml:"disabled,omitempty" json:"disabled,omitempty"`
}
type projectOverride struct {
	Declared      bool                       `yaml:"-" json:"-"`
	ProjectID     string                     `yaml:"project_id" json:"project_id"`
	Forwards      map[string]forwardOverride `yaml:"forwards,omitempty" json:"forwards,omitempty"`
	MountPath     string                     `yaml:"mount_path,omitempty" json:"mount_path,omitempty"`
	LocalForwards []config.Forward           `yaml:"local_forwards,omitempty" json:"local_forwards,omitempty"`
}

type clientOverrides struct {
	SchemaVersion int                                   `yaml:"schema_version"`
	Servers       map[string]map[string]projectOverride `yaml:"servers"`
}

func (a *app) readOverrides() (clientOverrides, error) {
	state, err := a.readClientState()
	return state.overrides, err
}

// Caller holds client/config. New declarations are written to their original files.
func (a *app) writeOverrides(name string, projects map[string]projectOverride) error {
	state, err := a.readClientState()
	if err != nil {
		return err
	}
	updated := state.overrides
	updated.Servers = map[string]map[string]projectOverride{}
	for server, entries := range state.overrides.Servers {
		updated.Servers[server] = entries
	}
	if len(projects) == 0 {
		delete(updated.Servers, name)
	} else {
		updated.Servers[name] = projects
	}
	return a.writeClientState(context.Background(), state, state.prefs, updated)
}

type discoveredProject struct {
	Project       config.Project `json:"project"`
	RemoteProject config.Project `json:"remote_project"`
	Session       tmux.Status    `json:"session"`
	OverrideError string         `json:"override_error,omitempty"`
}

func mergeProject(remote config.Project, override projectOverride) (config.Project, error) {
	if override.ProjectID == "" && len(override.Forwards) == 0 && len(override.LocalForwards) == 0 && override.MountPath == "" && !override.Declared {
		return remote, nil
	}
	if override.ProjectID != remote.ID && !(override.Declared && override.ProjectID == "") {
		return remote, config.Invalid("local overrides refer to an older project identity; use tdev override <project> --reset")
	}
	effective := remote
	effective.Forwards = append([]config.Forward{}, remote.Forwards...)
	seen := map[string]bool{}
	for i := range effective.Forwards {
		f := &effective.Forwards[i]
		patch, ok := override.Forwards[f.ID]
		if !ok {
			continue
		}
		seen[f.ID] = true
		if f.Direction != "L" {
			return remote, config.Invalid("local port overrides only apply to -L forwards")
		}
		if patch.ListenPort != nil {
			f.ListenPort = *patch.ListenPort
		}
		if patch.BindAddress != nil {
			f.BindAddress = *patch.BindAddress
		}
		if err := f.Validate(); err != nil {
			return remote, err
		}
	}
	for id := range override.Forwards {
		if !seen[id] {
			return remote, config.Invalid("remote forward %q no longer exists; reset its local override", id)
		}
	}
	kept := effective.Forwards[:0]
	for _, f := range effective.Forwards {
		if !override.Forwards[f.ID].Disabled {
			kept = append(kept, f)
		}
	}
	effective.Forwards = kept
	for _, f := range override.LocalForwards {
		if f.Direction != "L" {
			return remote, config.Invalid("client-only forwards must use -L")
		}
		appendForwards(&effective, []config.Forward{f})
	}
	if err := effective.Validate(); err != nil {
		return remote, err
	}
	return effective, nil
}

func (a *app) effective(t target, remote config.Project) (config.Project, error) {
	o, err := a.bindOverride(t, remote, t.Server.Projects[remote.Name])
	if err != nil {
		return remote, err
	}
	if t.Server.Projects != nil {
		t.Server.Projects[remote.Name] = o
	}
	return mergeProject(remote, o)
}

func decodeProjectDefinitions(data []byte) ([]discoveredProject, error) {
	var definitions []config.Project
	if err := json.Unmarshal(data, &definitions); err != nil {
		return nil, err
	}
	projects := make([]discoveredProject, 0, len(definitions))
	for _, p := range definitions {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		projects = append(projects, discoveredProject{Project: p, RemoteProject: p, Session: tmux.Status{Project: p.Name, Status: "unknown"}})
	}
	return projects, nil
}

func (a *app) discover(ctx context.Context, t target, withStatus bool) ([]discoveredProject, error) {
	args := []string{"project", "list"}
	if withStatus {
		args = append(args, "--status")
	}
	data, err := a.call(ctx, t, nil, args...)
	if err != nil {
		return nil, err
	}
	var projects []discoveredProject
	if withStatus {
		err = json.Unmarshal(data, &projects)
	} else {
		projects, err = decodeProjectDefinitions(data)
	}
	if err != nil {
		return nil, err
	}
	for i := range projects {
		remote := projects[i].Project
		if err = remote.Validate(); err != nil {
			return nil, err
		}
		if !withStatus {
			projects[i].Session.Status = "missing"
		}
		projects[i].RemoteProject = remote
		projects[i].Project, err = a.effective(t, remote)
		if err != nil {
			projects[i].Project = remote
			projects[i].OverrideError = err.Error()
		}
	}
	return projects, nil
}

func (a *app) saveOverride(ctx context.Context, t target, remote config.Project, edit func(*projectOverride) error) error {
	unlock, err := a.store.Lock(ctx, "client", "config")
	if err != nil {
		return err
	}
	defer unlock()
	p, err := a.prefs()
	if err != nil {
		return err
	}
	s, ok := p.Servers[t.Name]
	if t.Local {
		overrides, e := a.readOverrides()
		if e != nil {
			return e
		}
		s = server{Projects: overrides.Servers["local"]}
		ok = true
	}
	if !ok {
		return config.Invalid("server registration changed; rerun setup")
	}
	if s.Projects == nil {
		s.Projects = map[string]projectOverride{}
	}
	o := s.Projects[remote.Name]
	if err = edit(&o); err != nil {
		return err
	}
	if o.Declared && o.ProjectID == "" {
		o.ProjectID = remote.ID
	}
	if o.ProjectID == "" {
		delete(s.Projects, remote.Name)
	} else {
		if _, err = mergeProject(remote, o); err != nil {
			return err
		}
		s.Projects[remote.Name] = o
	}
	return a.writeOverrides(t.Name, s.Projects)
}

func (a *app) overrideCommand() {
	c := a.command(a.root, "override <project>", "Inspect or change client-only local port mappings after remote discovery", cobra.ExactArgs(1), func(ctx context.Context, c *cobra.Command, args []string) (any, error) {
		t, err := a.resolve(ctx, args[0], false)
		if err != nil {
			return nil, err
		}
		remote, err := a.get(ctx, t)
		if err != nil {
			return nil, err
		}
		mountPath, _ := c.Flags().GetString("mount-path")
		reset, _ := c.Flags().GetBool("reset")
		id, _ := c.Flags().GetString("forward")
		port, _ := c.Flags().GetInt("local-port")
		bind, _ := c.Flags().GetString("bind-address")
		disable, _ := c.Flags().GetBool("disable")
		enable, _ := c.Flags().GetBool("enable")
		changed := c.Flags().Changed("local-port") || c.Flags().Changed("bind-address") || disable || enable
		mountChanged := c.Flags().Changed("mount-path")
		if reset && (id != "" || changed || mountChanged) {
			return nil, config.Invalid("--reset cannot be combined with forward changes")
		}
		if disable && enable {
			return nil, config.Invalid("choose --disable or --enable")
		}
		if changed && id == "" {
			return nil, config.Invalid("--forward ID is required")
		}
		if reset || changed || mountChanged {
			err = a.saveOverride(ctx, t, remote, func(o *projectOverride) error {
				if reset {
					*o = projectOverride{}
					return nil
				}
				if o.ProjectID != "" && o.ProjectID != remote.ID {
					return config.Invalid("project identity changed; reset local overrides first")
				}
				o.ProjectID = remote.ID
				if mountChanged {
					if mountPath == "" || !filepath.IsAbs(mountPath) || !config.SafeText(mountPath) {
						return config.Invalid("mount path must be absolute")
					}
					o.MountPath = mountPath
				}
				if !changed {
					return nil
				}
				if o.Forwards == nil {
					o.Forwards = map[string]forwardOverride{}
				}
				patch := o.Forwards[id]
				if c.Flags().Changed("local-port") {
					patch.ListenPort = &port
				}
				if c.Flags().Changed("bind-address") {
					patch.BindAddress = &bind
				}
				if disable {
					patch.Disabled = true
				}
				if enable {
					patch.Disabled = false
				}
				o.Forwards[id] = patch
				return nil
			})
			if err != nil {
				return nil, err
			}
			p, err := a.prefs()
			if err != nil {
				return nil, err
			}
			t, err = a.serverTarget(p, t.Name, t.Project)
			if err != nil {
				return nil, err
			}
		}
		effective, err := a.effective(t, remote)
		if err != nil {
			return nil, err
		}
		return map[string]any{"remote_project": remote, "project": effective, "overrides": t.Server.Projects[remote.Name]}, nil
	})
	c.Flags().String("mount-path", "", "default local mount directory")
	c.Flags().String("forward", "", "stable remote forward ID (shown by tdev show or tdev override)")
	c.Flags().Int("local-port", 0, "override the local listening port")
	c.Flags().String("bind-address", "", "override the local bind address")
	c.Flags().Bool("disable", false, "disable this local forward on this client")
	c.Flags().Bool("enable", false, "enable this local forward on this client")
	c.Flags().Bool("reset", false, "remove this client's overrides for the discovered project")
}
