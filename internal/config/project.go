package config

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

const SchemaVersion = 1

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var uuidRE = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Project struct {
	SchemaVersion int       `yaml:"schema_version" json:"schema_version"`
	Revision      int       `yaml:"revision" json:"revision"`
	ID            string    `yaml:"id" json:"id"`
	Name          string    `yaml:"name" json:"name"`
	Directory     string    `yaml:"directory" json:"directory"`
	Windows       []Window  `yaml:"windows" json:"windows"`
	Forwards      []Forward `yaml:"forwards" json:"forwards"`
}
type Window struct {
	ID        string `yaml:"id" json:"id"`
	Name      string `yaml:"name" json:"name"`
	Directory string `yaml:"directory" json:"directory"`
	Command   string `yaml:"command,omitempty" json:"command,omitempty"`
	Layout    *Pane  `yaml:"layout,omitempty" json:"layout,omitempty"`
}

// Pane is either a leaf with a stable ID, directory and declared command, or a
// split whose children are ordered left-to-right or top-to-bottom. Weight is
// relative to siblings; zero requests equal space.
type Pane struct {
	ID        string `yaml:"id,omitempty" json:"id,omitempty"`
	Directory string `yaml:"directory,omitempty" json:"directory,omitempty"`
	Command   string `yaml:"command,omitempty" json:"command,omitempty"`
	Split     string `yaml:"split,omitempty" json:"split,omitempty"`
	Panes     []Pane `yaml:"panes,omitempty" json:"panes,omitempty"`
	Weight    int    `yaml:"weight,omitempty" json:"weight,omitempty"`
}

func (p Pane) validate(ids map[string]bool, depth int) error {
	if depth > 16 || p.Weight < 0 || p.Weight > 100000 {
		return Invalid("invalid pane depth or weight")
	}
	if p.Split != "" {
		if (p.Split != "horizontal" && p.Split != "vertical") || len(p.Panes) < 2 || p.ID != "" || p.Directory != "" || p.Command != "" {
			return Invalid("split panes require horizontal/vertical and at least two children")
		}
		for _, child := range p.Panes {
			if err := child.validate(ids, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if len(p.Panes) != 0 || !ValidName(p.ID) || ids[p.ID] || !SafeText(p.Directory) || !utf8.ValidString(p.Command) || strings.ContainsRune(p.Command, 0) {
		return Invalid("invalid or duplicate pane %q", p.ID)
	}
	ids[p.ID] = true
	return nil
}

type Forward struct {
	ID          string `yaml:"id" json:"id"`
	Direction   string `yaml:"direction" json:"direction"`
	BindAddress string `yaml:"bind_address" json:"bind_address"`
	ListenPort  int    `yaml:"listen_port" json:"listen_port"`
	TargetHost  string `yaml:"target_host" json:"target_host"`
	TargetPort  int    `yaml:"target_port" json:"target_port"`
}

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func ValidName(s string) bool { return nameRE.MatchString(s) }
func SafeText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, unicode.IsControl)
}
func Invalid(format string, args ...any) error {
	return protocol.Fail("INVALID_CONFIG", 2, format, args...)
}

func Decode(data []byte, dst any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(dst); err != nil {
		return Invalid("invalid configuration: %v", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return Invalid("exactly one YAML/JSON document required")
	}
	return nil
}
func (p Project) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return Invalid("unsupported schema_version %d", p.SchemaVersion)
	}
	if !ValidName(p.Name) || !uuidRE.MatchString(p.ID) || p.Revision < 1 {
		return Invalid("invalid project name, UUID, or revision")
	}
	if !filepath.IsAbs(p.Directory) || !SafeText(p.Directory) {
		return Invalid("project directory must be an absolute path without control characters")
	}
	if len(p.Windows) == 0 {
		return Invalid("at least one window is required")
	}
	ids := map[string]bool{}
	for _, w := range p.Windows {
		if !ValidName(w.ID) || ids[w.ID] || w.Name == "" || !SafeText(w.Name) || !SafeText(w.Directory) || !utf8.ValidString(w.Command) || strings.ContainsRune(w.Command, 0) {
			return Invalid("invalid or duplicate window %q", w.ID)
		}
		ids[w.ID] = true
		if w.Layout != nil {
			if w.Command != "" {
				return Invalid("window command must be specified on layout leaves")
			}
			if err := w.Layout.validate(map[string]bool{}, 0); err != nil {
				return err
			}
		}
	}
	ids = map[string]bool{}
	listeners := map[string]bool{}
	for _, f := range p.Forwards {
		if err := f.Validate(); err != nil {
			return err
		}
		if !ValidName(f.ID) || ids[f.ID] {
			return Invalid("invalid or duplicate forward ID %q", f.ID)
		}
		ids[f.ID] = true
		key := fmt.Sprintf("%s/%s/%d", f.Direction, f.BindAddress, f.ListenPort)
		if listeners[key] {
			return Invalid("duplicate forward listener %s", key)
		}
		listeners[key] = true
	}
	return nil
}
func (f Forward) Validate() error {
	if f.Direction != "L" && f.Direction != "R" {
		return Invalid("forward direction must be L or R")
	}
	if f.ListenPort < 1 || f.ListenPort > 65535 || f.TargetPort < 1 || f.TargetPort > 65535 {
		return Invalid("ports must be between 1 and 65535")
	}
	for _, s := range []string{f.BindAddress, f.TargetHost} {
		if s == "" || !SafeText(s) || strings.ContainsAny(s, " []/\\") || strings.HasPrefix(s, "-") {
			return Invalid("invalid forwarding host %q", s)
		}
		if strings.Contains(s, ":") && net.ParseIP(s) == nil {
			return Invalid("invalid IPv6 address %q", s)
		}
	}
	return nil
}
func (f Forward) Spec() string {
	host := func(s string) string {
		if strings.Contains(s, ":") {
			return "[" + s + "]"
		}
		return s
	}
	return fmt.Sprintf("%s:%d:%s:%d", host(f.BindAddress), f.ListenPort, host(f.TargetHost), f.TargetPort)
}

// Split outside IPv6 brackets; retain OpenSSH ordering for both directions.
func ParseForward(direction, spec string) (Forward, error) {
	var parts []string
	start, depth := 0, 0
	for i, c := range spec {
		switch c {
		case '[':
			depth++
			if depth != 1 {
				return Forward{}, Invalid("invalid brackets")
			}
		case ']':
			depth--
			if depth != 0 {
				return Forward{}, Invalid("invalid brackets")
			}
		case ':':
			if depth == 0 {
				parts = append(parts, spec[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return Forward{}, Invalid("invalid brackets")
	}
	parts = append(parts, spec[start:])
	f := Forward{ID: "f" + strings.ReplaceAll(NewID(), "-", ""), Direction: direction, BindAddress: "127.0.0.1", TargetHost: "localhost"}
	var lp, tp string
	switch len(parts) {
	case 1:
		lp, tp = parts[0], parts[0]
	case 2:
		lp, tp = parts[0], parts[1]
	case 3:
		lp, f.TargetHost, tp = parts[0], parts[1], parts[2]
	case 4:
		f.BindAddress, lp, f.TargetHost, tp = parts[0], parts[1], parts[2], parts[3]
	default:
		return f, Invalid("unsupported TCP forwarding specification %q", spec)
	}
	unbracket := func(s string) string {
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			return s[1 : len(s)-1]
		}
		return s
	}
	for _, host := range []string{f.BindAddress, f.TargetHost} {
		if strings.ContainsAny(host, "[]") && (!strings.HasPrefix(host, "[") || !strings.HasSuffix(host, "]") || net.ParseIP(unbracket(host)) == nil) {
			return f, Invalid("brackets require a valid IPv6 address")
		}
	}
	f.BindAddress = unbracket(f.BindAddress)
	f.TargetHost = unbracket(f.TargetHost)
	var err error
	f.ListenPort, err = strconv.Atoi(lp)
	if err != nil {
		return f, Invalid("invalid listen port")
	}
	f.TargetPort, err = strconv.Atoi(tp)
	if err != nil {
		return f, Invalid("invalid target port")
	}
	return f, f.Validate()
}
func WindowPath(p Project, w Window) string {
	if filepath.IsAbs(w.Directory) {
		return filepath.Clean(w.Directory)
	}
	return filepath.Join(p.Directory, w.Directory)
}
