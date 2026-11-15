package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// declaration is the human-authored format. Project remains the machine API model.
type declaration struct {
	Dir     string       `yaml:"dir"`
	Windows []declWindow `yaml:"windows,omitempty"`
	Ports   []declPort   `yaml:"ports,omitempty"`
}
type declWindow struct {
	Name    string    `yaml:"name"`
	Dir     string    `yaml:"dir,omitempty"`
	Command string    `yaml:"command,omitempty"`
	Layout  *declPane `yaml:"layout,omitempty"`
}
type declPane struct {
	Name    string     `yaml:"name,omitempty"`
	Dir     string     `yaml:"dir,omitempty"`
	Command string     `yaml:"command,omitempty"`
	Row     []declPane `yaml:"row,omitempty"`
	Column  []declPane `yaml:"column,omitempty"`
	Weight  int        `yaml:"weight,omitempty"`
}
type declPort struct {
	Short     string `yaml:"-"`
	Name      string `yaml:"name,omitempty"`
	Direction string `yaml:"direction,omitempty"`
	Local     int    `yaml:"local"`
	Remote    int    `yaml:"remote"`
	Host      string `yaml:"host,omitempty"`
	Bind      string `yaml:"bind,omitempty"`
}

func (p *declPort) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		if n.Tag != "!!str" && n.Tag != "!!int" {
			return Invalid("line %d: port must be a string, number or mapping", n.Line)
		}
		p.Short = n.Value
		return nil
	}
	type plain declPort
	return strictNode(n, (*plain)(p))
}
func (p declPort) MarshalYAML() (any, error) {
	if p.Short != "" {
		return p.Short, nil
	}
	type plain declPort
	return plain(p), nil
}
func strictNode(n *yaml.Node, dst any) error {
	b, err := yaml.Marshal(n)
	if err != nil {
		return err
	}
	return Decode(b, dst)
}
func expandHome(dir string) (string, error) {
	if dir == "~" || strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(dir, "~"), "/")), nil
	}
	return dir, nil
}
func shortID(prefix, value string) string {
	return fmt.Sprintf("%s%x", prefix, sha256.Sum256([]byte(value)))[:len(prefix)+24]
}
func (d declaration) project(name string, old Project) (Project, error) {
	dir, err := expandHome(d.Dir)
	if err != nil {
		return Project{}, err
	}
	p := Project{SchemaVersion: SchemaVersion, ID: old.ID, Revision: old.Revision, Name: name, Directory: dir, Forwards: []Forward{}}
	if p.ID == "" {
		p.ID, p.Revision = NewID(), 1
	}
	if len(d.Windows) == 0 {
		d.Windows = []declWindow{{Name: "main"}}
	}
	names := map[string]bool{}
	for _, w := range d.Windows {
		if w.Name == "" || names[w.Name] {
			return p, Invalid("window names must be nonempty and unique: %q", w.Name)
		}
		names[w.Name] = true
		id := shortID("w", w.Name)
		for _, previous := range old.Windows {
			if previous.Name == w.Name {
				id = previous.ID
				break
			}
		}
		wd, err := expandHome(w.Dir)
		if err != nil {
			return p, err
		}
		win := Window{ID: id, Name: w.Name, Directory: wd, Command: w.Command}
		if win.Directory == "" {
			win.Directory = "."
		}
		if w.Layout != nil {
			var previousLayout *Pane
			for _, previous := range old.Windows {
				if previous.ID == win.ID {
					previousLayout = previous.Layout
					break
				}
			}
			pane, err := w.Layout.pane("p", win.Directory, 0, previousLayout)
			if err != nil {
				return p, err
			}
			win.Layout = &pane
		}
		p.Windows = append(p.Windows, win)
	}
	for _, port := range d.Ports {
		var f Forward
		if port.Short != "" {
			f, err = ParseForward("L", port.Short)
			if err != nil {
				return p, err
			}
			if strings.Count(port.Short, ":") <= 1 {
				f.TargetHost = "127.0.0.1"
			}
		} else {
			f = Forward{Direction: "L", BindAddress: port.Bind, TargetHost: port.Host, ListenPort: port.Local, TargetPort: port.Remote}
			if port.Direction == "remote" {
				f.Direction, f.ListenPort, f.TargetPort = "R", port.Remote, port.Local
			} else if port.Direction != "" && port.Direction != "local" {
				return p, Invalid("port direction must be local or remote")
			}
			if f.BindAddress == "" {
				f.BindAddress = "127.0.0.1"
			}
			if f.TargetHost == "" {
				f.TargetHost = "127.0.0.1"
			}
		}
		if port.Name != "" {
			f.ID = port.Name
		} else {
			f.ID = shortID("f", f.Direction+":"+f.Spec())
			for _, previous := range old.Forwards {
				if previous.Direction == f.Direction && previous.Spec() == f.Spec() {
					f.ID = previous.ID
					break
				}
			}
		}
		p.Forwards = append(p.Forwards, f)
	}
	return p, p.Validate()
}
func (d declPane) pane(path, inherited string, depth int, old *Pane) (Pane, error) {
	if depth > 16 {
		return Pane{}, Invalid("pane layout exceeds 16 levels")
	}
	p := Pane{Weight: d.Weight}
	if d.Row != nil || d.Column != nil {
		if (d.Row != nil && d.Column != nil) || d.Name != "" || d.Dir != "" || d.Command != "" {
			return p, Invalid("split requires exactly one of row or column and no leaf fields")
		}
		children := d.Row
		p.Split = "horizontal"
		if d.Column != nil {
			children, p.Split = d.Column, "vertical"
		}
		for i, child := range children {
			var previous *Pane
			if old != nil && i < len(old.Panes) {
				previous = &old.Panes[i]
			}
			pane, err := child.pane(fmt.Sprintf("%s-%d", path, i), inherited, depth+1, previous)
			if err != nil {
				return p, err
			}
			p.Panes = append(p.Panes, pane)
		}
	} else {
		p.ID, p.Directory, p.Command = d.Name, d.Dir, d.Command
		if p.ID == "" {
			p.ID = path
			if old != nil && old.Split == "" && old.ID != "" {
				p.ID = old.ID
			}
		}
		if p.Directory == "" {
			p.Directory = inherited
		}
		var err error
		p.Directory, err = expandHome(p.Directory)
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

var generatedForwardID = regexp.MustCompile(`^f(?:[a-f0-9]{24}|[a-f0-9]{32})$`)
var generatedPaneID = regexp.MustCompile(`^[pw](?:[a-f0-9]{24}|[a-f0-9]{32})$`)

func declare(p Project) declaration {
	d := declaration{Dir: p.Directory}
	for _, w := range p.Windows {
		v := declWindow{Name: w.Name, Dir: w.Directory, Command: w.Command}
		if v.Dir == "." {
			v.Dir = ""
		}
		if w.Layout != nil {
			v.Layout = declarePane(*w.Layout)
		}
		d.Windows = append(d.Windows, v)
	}
	for _, f := range p.Forwards {
		port := declPort{Name: f.ID, Local: f.ListenPort, Remote: f.TargetPort, Host: f.TargetHost, Bind: f.BindAddress}
		if generatedForwardID.MatchString(port.Name) {
			port.Name = ""
		}
		if f.Direction == "R" {
			port.Direction, port.Local, port.Remote = "remote", f.TargetPort, f.ListenPort
		}
		if port.Host == "127.0.0.1" {
			port.Host = ""
		}
		if port.Bind == "127.0.0.1" {
			port.Bind = ""
		}
		d.Ports = append(d.Ports, port)
	}
	return d
}
func declarePane(p Pane) *declPane {
	d := &declPane{Weight: p.Weight}
	if p.Split != "" {
		var children []declPane
		for _, child := range p.Panes {
			children = append(children, *declarePane(child))
		}
		if p.Split == "horizontal" {
			d.Row = children
		} else {
			d.Column = children
		}
	} else {
		d.Name, d.Dir, d.Command = p.ID, p.Directory, p.Command
		if generatedPaneID.MatchString(d.Name) {
			d.Name = ""
		}
		if d.Dir == "." {
			d.Dir = ""
		}
	}
	return d
}

// PortDeclaration is the human-facing short or expanded port syntax.
type PortDeclaration = declPort

func ParsePorts(ports []PortDeclaration) ([]Forward, error) {
	p, err := (declaration{Dir: "/", Ports: ports}).project("ports", Project{})
	return p.Forwards, err
}
func DeclarePorts(ports []Forward) []PortDeclaration { return declare(Project{Forwards: ports}).Ports }
func ExpandHome(path string) (string, error)         { return expandHome(path) }
