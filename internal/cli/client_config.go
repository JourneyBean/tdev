package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
	"tdev/internal/config"
	"tdev/internal/protocol"
	"tdev/internal/reconnect"
	"tdev/internal/sshx"
)

type httpPreferences struct {
	Port int `yaml:"port"`
}

type clientDeclaration struct {
	HTTP      httpPreferences                `yaml:"http,omitempty"`
	Servers   map[string]server              `yaml:"servers,omitempty"`
	Overrides map[string]overrideDeclaration `yaml:"overrides,omitempty"`
	SSH       sshx.Options                   `yaml:"ssh"`
	Reconnect reconnect.Policies             `yaml:"reconnect"`
}
type overridePort struct {
	Local   *int    `yaml:"local,omitempty"`
	Bind    *string `yaml:"bind,omitempty"`
	Enabled *bool   `yaml:"enabled,omitempty"`
}
type overrideMount struct {
	Path string `yaml:"path"`
}
type overrideDeclaration struct {
	Ports      map[string]overridePort  `yaml:"ports,omitempty"`
	ExtraPorts []config.PortDeclaration `yaml:"extra_ports,omitempty"`
	Mount      *overrideMount           `yaml:"mount,omitempty"`
}
type clientState struct {
	prefs     preferences
	overrides clientOverrides
	document  config.ClientDocument
	legacy    map[string][]byte
}

func validOverrideTarget(target string) bool {
	parts := strings.Split(target, "/")
	return len(parts) == 2 && config.ValidName(parts[0]) && config.ValidName(parts[1]) && parts[1] != "projects" && parts[1] != "client"
}
func (a *app) readClientState() (clientState, error) {
	state := clientState{prefs: defaultPreferences(), overrides: clientOverrides{SchemaVersion: 1, Servers: map[string]map[string]projectOverride{}}, legacy: map[string][]byte{}}
	doc, err := a.store.ReadClient()
	if err != nil {
		return state, err
	}
	state.document = doc
	if !doc.Present {
		for _, file := range []string{"client.yaml", "overrides.yaml"} {
			b, err := os.ReadFile(filepath.Join(a.store.Root, file))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return state, err
			}
			state.legacy[file] = b
			if file == "client.yaml" {
				input := struct {
					Current              preferences `yaml:",inline"`
					IgnoredDefaultServer string      `yaml:"default_server,omitempty"`
				}{Current: state.prefs}
				if err := config.Decode(b, &input); err != nil {
					return state, err
				}
				state.prefs = input.Current
			} else {
				if err := config.Decode(b, &state.overrides); err != nil {
					return state, err
				}
			}
		}
		if state.prefs.SchemaVersion != 1 || state.overrides.SchemaVersion != 1 {
			return state, config.Invalid("unsupported legacy client schema")
		}
	} else {
		decl := clientDeclaration{HTTP: state.prefs.HTTP, Servers: map[string]server{}, Overrides: map[string]overrideDeclaration{}, SSH: state.prefs.SSH, Reconnect: state.prefs.Reconnect}
		b, err := yaml.Marshal(doc.Data)
		if err != nil {
			return state, err
		}
		if err := config.Decode(b, &decl); err != nil {
			return state, config.Invalid("client configuration: %v", err)
		}
		state.prefs.HTTP = decl.HTTP
		state.prefs.Servers, state.prefs.SSH, state.prefs.Reconnect = decl.Servers, decl.SSH, decl.Reconnect
		bindings, err := a.readBindings()
		if err != nil {
			return state, err
		}
		for target, d := range decl.Overrides {
			if !validOverrideTarget(target) {
				return state, config.Invalid("override target must be server/project: %q", target)
			}
			parts := strings.Split(target, "/")
			o := projectOverride{ProjectID: bindings[target], Declared: true, Forwards: map[string]forwardOverride{}}
			for name, p := range d.Ports {
				if !config.ValidName(name) {
					return state, config.Invalid("invalid override port name %q", name)
				}
				patch := forwardOverride{ListenPort: p.Local, BindAddress: p.Bind}
				if p.Enabled != nil {
					patch.Disabled = !*p.Enabled
				}
				probe := config.Forward{ID: name, Direction: "L", ListenPort: 1, TargetPort: 1, BindAddress: "127.0.0.1", TargetHost: "127.0.0.1"}
				if p.Local != nil {
					probe.ListenPort = *p.Local
				}
				if p.Bind != nil {
					probe.BindAddress = *p.Bind
				}
				if err := probe.Validate(); err != nil {
					return state, err
				}
				o.Forwards[name] = patch
			}
			o.LocalForwards, err = config.ParsePorts(d.ExtraPorts)
			if err != nil {
				return state, err
			}
			for _, f := range o.LocalForwards {
				if f.Direction != "L" {
					return state, config.Invalid("extra_ports support local forwarding only")
				}
			}
			if d.Mount != nil {
				o.MountPath, err = config.ExpandHome(d.Mount.Path)
				if err != nil {
					return state, err
				}
				if !filepath.IsAbs(o.MountPath) || !config.SafeText(o.MountPath) {
					return state, config.Invalid("client mount.path must be absolute or ~/ prefixed")
				}
			}
			if state.overrides.Servers[parts[0]] == nil {
				state.overrides.Servers[parts[0]] = map[string]projectOverride{}
			}
			state.overrides.Servers[parts[0]][parts[1]] = o
		}
		// Defaults apply to concise declarations, preserving strict legacy validation.
		for name, s := range state.prefs.Servers {
			if s.RemoteBinary == "" {
				s.RemoteBinary = ".local/bin/tdev"
			}
			state.prefs.Servers[name] = s
		}
	}
	if state.prefs.Servers == nil {
		state.prefs.Servers = map[string]server{}
	}
	if state.overrides.Servers == nil {
		state.overrides.Servers = map[string]map[string]projectOverride{}
	}
	for server, projects := range state.overrides.Servers {
		if !config.ValidName(server) {
			return state, config.Invalid("invalid override server name")
		}
		for name, o := range projects {
			if !config.ValidName(name) || (o.ProjectID == "" && !o.Declared) {
				return state, config.Invalid("invalid project override identity")
			}
		}
	}
	return state, nil
}
func declareClient(p preferences, o clientOverrides) *yaml.Node {
	decl := struct {
		HTTP      *httpPreferences               `yaml:"http,omitempty"`
		Servers   map[string]server              `yaml:"servers,omitempty"`
		Overrides map[string]overrideDeclaration `yaml:"overrides,omitempty"`
		SSH       *sshx.Options                  `yaml:"ssh,omitempty"`
		Reconnect *reconnect.Policies            `yaml:"reconnect,omitempty"`
	}{Servers: map[string]server{}, Overrides: map[string]overrideDeclaration{}}
	for name, server := range p.Servers {
		if server.RemoteBinary == ".local/bin/tdev" {
			server.RemoteBinary = ""
		}
		decl.Servers[name] = server
	}
	if p.HTTP != defaultPreferences().HTTP {
		decl.HTTP = &p.HTTP
	}
	options := p.SSH
	options.ConfigFile = ""
	options.MasterDirectory = ""
	if !reflect.DeepEqual(options, sshx.Defaults()) {
		decl.SSH = &options
	}
	if !reflect.DeepEqual(p.Reconnect, reconnect.Defaults()) {
		decl.Reconnect = &p.Reconnect
	}

	for server, projects := range o.Servers {
		for name, override := range projects {
			d := overrideDeclaration{Ports: map[string]overridePort{}, ExtraPorts: config.DeclarePorts(override.LocalForwards)}
			for id, patch := range override.Forwards {
				port := overridePort{Local: patch.ListenPort, Bind: patch.BindAddress}
				if patch.Disabled {
					enabled := false
					port.Enabled = &enabled
				}
				d.Ports[id] = port
			}
			if override.MountPath != "" {
				d.Mount = &overrideMount{Path: override.MountPath}
			}
			decl.Overrides[server+"/"+name] = d
		}
	}
	var n yaml.Node
	_ = n.Encode(decl)
	return &n
}
func (a *app) writeClientState(ctx context.Context, old clientState, p preferences, o clientOverrides) error {
	for file, b := range old.legacy {
		path := filepath.Join(a.store.Root, file)
		current, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, b) {
			return protocol.Fail("CONFIG_CONFLICT", 4, "legacy client configuration changed: %s", path)
		}
		backup := path + ".legacy.bak"
		existing, err := os.ReadFile(backup)
		if errors.Is(err, os.ErrNotExist) {
			if err := config.AtomicWrite(backup, b, 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if !bytes.Equal(existing, b) {
			return protocol.Fail("CONFIG_CONFLICT", 4, "legacy client backup already exists with different contents: %s", backup)
		}
	}
	unlock, err := a.store.Lock(ctx, "client", "bindings")
	if err != nil {
		return err
	}
	defer unlock()
	bindings, err := a.readBindings()
	if err != nil {
		return err
	}
	for server, projects := range old.overrides.Servers {
		for name := range projects {
			if _, ok := o.Servers[server][name]; !ok {
				delete(bindings, server+"/"+name)
			}
		}
	}
	for server, projects := range o.Servers {
		for name, override := range projects {
			if override.ProjectID != "" {
				bindings[server+"/"+name] = override.ProjectID
			}
		}
	}
	if err := a.store.SaveClient(ctx, old.document, declareClient(old.prefs, old.overrides), declareClient(p, o)); err != nil {
		return err
	}
	if err := a.writeBindings(bindings); err != nil {
		return protocol.Fail("PARTIAL_SUCCESS", 9, "client configuration saved but identity bindings could not be saved: %v", err)
	}
	return nil
}

var projectUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (a *app) readBindings() (map[string]string, error) {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(a.store.Root, "state", "client-bindings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, config.Invalid("invalid client identity bindings: %v", err)
	}
	if out == nil {
		out = map[string]string{}
	}
	for target, id := range out {
		if !validOverrideTarget(target) || !projectUID.MatchString(id) {
			return nil, config.Invalid("invalid client identity binding for %q", target)
		}
	}
	return out, nil
}
func (a *app) writeBindings(bindings map[string]string) error {
	b, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	return config.AtomicWrite(filepath.Join(a.store.Root, "state", "client-bindings.json"), b, 0600)
}
func (a *app) bindOverride(t target, p config.Project, o projectOverride) (projectOverride, error) {
	if !o.Declared {
		return o, nil
	}
	candidate := o
	candidate.ProjectID = p.ID
	candidate.Declared = false
	if _, err := mergeProject(p, candidate); err != nil {
		return o, err
	}
	unlock, err := a.store.Lock(context.Background(), "client", "bindings")
	if err != nil {
		return o, err
	}
	defer unlock()
	bindings, err := a.readBindings()
	if err != nil {
		return o, err
	}
	key := t.Name + "/" + p.Name
	if bound := bindings[key]; bound != "" && bound != p.ID {
		return o, config.Invalid("project identity changed for %s; use tdev override %s --reset", key, key)
	}
	if bindings[key] == "" {
		bindings[key] = p.ID
		if err := a.writeBindings(bindings); err != nil {
			return o, err
		}
	}
	return candidate, nil
}
