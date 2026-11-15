package config

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

type sourceFile struct {
	path   string
	data   []byte
	doc    *yaml.Node
	legacy bool
}
type projectSource struct {
	file   *sourceFile
	node   *yaml.Node
	name   string
	legacy *Project
}
type projectRecord struct {
	Digest  string  `json:"digest"`
	Project Project `json:"project"`
}

func (s Store) recordPath(name string) string {
	return filepath.Join(s.Root, "state", "projects", name+".json")
}
func (s Store) readRecord(name string) (projectRecord, error) {
	var r projectRecord
	b, err := os.ReadFile(s.recordPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, Invalid("invalid internal project record for %q: %v", name, err)
	}
	if err = r.Project.Validate(); err != nil || r.Project.Name != name {
		return r, Invalid("invalid internal project identity for %q", name)
	}
	return r, nil
}
func sourceDigest(n *yaml.Node) string {
	b, _ := yaml.Marshal(n)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func (s Store) saveRecord(src projectSource, p Project) error {
	b, err := json.Marshal(projectRecord{Digest: sourceDigest(src.node), Project: p})
	if err != nil {
		return err
	}
	return AtomicWrite(s.recordPath(p.Name), b, 0600)
}
func (s Store) resolve(src projectSource) (Project, error) {
	if src.legacy != nil {
		r, err := s.readRecord(src.name)
		if err != nil {
			return Project{}, err
		}
		if r.Digest == sourceDigest(src.node) {
			return *src.legacy, nil
		}
		return *src.legacy, s.saveRecord(src, *src.legacy)
	}
	r, err := s.readRecord(src.name)
	if err != nil {
		return Project{}, err
	}
	digest := sourceDigest(src.node)
	if r.Digest == digest {
		return r.Project, nil
	}
	var d declaration
	if err := strictNode(src.node, &d); err != nil {
		return Project{}, Invalid("%s:%d: project %q: %v", src.file.path, src.node.Line, src.name, err)
	}
	p, err := d.project(src.name, r.Project)
	if err != nil {
		return p, Invalid("%s:%d: project %q: %v", src.file.path, src.node.Line, src.name, err)
	}
	// Comments, formatting and explicit defaults do not change the revision.
	if r.Digest != "" {
		previous, err := declare(r.Project).project(src.name, r.Project)
		if err != nil {
			return p, err
		}
		current := p
		previous.Revision, current.Revision = 0, 0
		if !reflect.DeepEqual(previous, current) {
			p.Revision++
		} else {
			p = r.Project
		}
	}
	if err := s.saveRecord(src, p); err != nil {
		return p, err
	}
	return p, nil
}
func parseFile(path string) (*sourceFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&doc); err != nil {
		return nil, Invalid("%s: %v", path, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, Invalid("%s: expected a mapping of project names", path)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, Invalid("%s: exactly one YAML document required", path)
	}
	if err := checkNodes(doc.Content[0], 0); err != nil {
		return nil, Invalid("%s: %v", path, err)
	}
	return &sourceFile{path: path, data: b, doc: &doc}, nil
}
func checkNodes(n *yaml.Node, depth int) error {
	if depth > 64 {
		return Invalid("line %d: configuration nesting exceeds 64 levels", n.Line)
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!merge" {
		return Invalid("line %d: YAML anchors, aliases and merge keys are unsupported", n.Line)
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || seen[k.Value] {
				return Invalid("line %d: invalid or duplicate key %q", k.Line, k.Value)
			}
			seen[k.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := checkNodes(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}
func reservedName(name string) bool { return name == "projects" || name == "client" }

func isLegacy(root *yaml.Node) bool {
	n := field(root, "schema_version")
	return n != nil && n.Kind == yaml.ScalarNode
}

func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func (s Store) sources() (map[string]projectSource, error) {
	out := map[string]projectSource{}
	main := filepath.Join(s.Root, "config.yaml")
	paths := []string{}
	var mainFile *sourceFile
	if _, err := os.Stat(main); err == nil {
		f, err := parseFile(main)
		if err != nil {
			return nil, err
		}
		mainFile = f
		paths = append(paths, main)
		if control := field(f.doc.Content[0], "projects"); control != nil {
			var includes struct {
				Include []string `yaml:"include"`
			}
			if err := strictNode(control, &includes); err != nil {
				return nil, Invalid("%s:%d: projects: %v", main, control.Line, err)
			}
			for _, pattern := range includes.Include {
				if pattern == "" {
					return nil, Invalid("%s: include path cannot be empty", main)
				}
				if !filepath.IsAbs(pattern) {
					pattern = filepath.Join(filepath.Dir(main), pattern)
				}
				if strings.ContainsAny(pattern, "*?[") {
					matches, err := filepath.Glob(pattern)
					if err != nil {
						return nil, Invalid("%s: invalid include pattern: %v", main, err)
					}
					paths = append(paths, matches...)
				} else {
					paths = append(paths, pattern)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		directory := filepath.Join(s.Root, "projects")
		entries, err := os.ReadDir(directory)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, Invalid("read projects directory: %v", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
				paths = append(paths, filepath.Join(directory, entry.Name()))
			}
		}
	}
	// A file explicitly included twice is an error, including self-inclusion.
	seen := map[string]bool{}
	for _, path := range paths {
		canonical, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		canonical, err = filepath.EvalSymlinks(canonical)
		if err != nil {
			return nil, Invalid("include %s: %v", path, err)
		}
		if seen[canonical] {
			return nil, Invalid("configuration file included more than once: %s", path)
		}
		seen[canonical] = true
		var f *sourceFile
		if path == main {
			f = mainFile
		} else {
			f, err = parseFile(path)
			if err != nil {
				return nil, err
			}
		}
		// Resolve writes to the actual source, preserving an include symlink.
		f.path = canonical
		root := f.doc.Content[0]
		if isLegacy(root) {
			var p Project
			if err := Decode(f.data, &p); err != nil {
				return nil, Invalid("%s: %v", path, err)
			}
			if err := p.Validate(); err != nil {
				return nil, Invalid("%s: %v", path, err)
			}
			if reservedName(p.Name) {
				return nil, Invalid("project name is reserved")
			}
			f.legacy = true
			if previous, ok := out[p.Name]; ok {
				return nil, duplicateProject(p.Name, previous, projectSource{file: f, node: root})
			}
			copy := p
			out[p.Name] = projectSource{file: f, node: root, name: p.Name, legacy: &copy}
			continue
		}
		for i := 0; i < len(root.Content); i += 2 {
			key, node := root.Content[i], root.Content[i+1]
			if key.Value == "client" {
				if path != main {
					return nil, Invalid("%s:%d: client settings belong in config.yaml or client.include files", path, key.Line)
				}
				continue
			}
			if key.Value == "projects" {
				if path != main {
					return nil, Invalid("%s:%d: projects.include is allowed only in config.yaml", path, key.Line)
				}
				continue
			}
			if !ValidName(key.Value) {
				return nil, Invalid("%s:%d: invalid project name %q", path, key.Line, key.Value)
			}
			src := projectSource{file: f, node: node, name: key.Value}
			if previous, ok := out[key.Value]; ok {
				return nil, duplicateProject(key.Value, previous, src)
			}
			out[key.Value] = src
		}
	}
	return out, nil
}
func duplicateProject(name string, a, b projectSource) error {
	return Invalid("duplicate project %q at %s:%d and %s:%d", name, a.file.path, a.node.Line, b.file.path, b.node.Line)
}
func (s Store) getUnlocked(name string) (Project, error) {
	if !ValidName(name) || reservedName(name) {
		return Project{}, Invalid("invalid or reserved project name %q", name)
	}
	sources, err := s.sources()
	if err != nil {
		return Project{}, err
	}
	src, ok := sources[name]
	if !ok {
		return Project{}, protocol.Fail("PROJECT_NOT_FOUND", 3, "project %q does not exist", name)
	}
	return s.resolve(src)
}
func (s Store) Get(name string) (Project, error) {
	unlock, err := s.Lock(context.Background(), "projects", "store")
	if err != nil {
		return Project{}, err
	}
	defer unlock()
	return s.getUnlocked(name)
}
func (s Store) List() ([]Project, error) {
	unlock, err := s.Lock(context.Background(), "projects", "store")
	if err != nil {
		return nil, err
	}
	defer unlock()
	sources, err := s.sources()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	out := []Project{}
	for _, name := range names {
		p, err := s.resolve(sources[name])
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func encodeNode(v any) *yaml.Node { var n yaml.Node; _ = n.Encode(v); return &n }
func nodeValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}
	var v any
	_ = n.Decode(&v)
	return v
}

// patchNode edits only changed fields, leaving comments, shortcuts and defaults intact.
func patchNode(original, before, after *yaml.Node) *yaml.Node {
	if reflect.DeepEqual(nodeValue(before), nodeValue(after)) {
		return original
	}
	if original != nil && before != nil && after != nil && original.Kind == yaml.MappingNode && before.Kind == yaml.MappingNode && after.Kind == yaml.MappingNode {
		for i := 0; i < len(before.Content); i += 2 {
			key := before.Content[i].Value
			if field(after, key) == nil {
				for j := 0; j < len(original.Content); j += 2 {
					if original.Content[j].Value == key {
						original.Content = append(original.Content[:j], original.Content[j+2:]...)
						break
					}
				}
			}
		}
		for i := 0; i < len(after.Content); i += 2 {
			key, next := after.Content[i], after.Content[i+1]
			prev := field(before, key.Value)
			if reflect.DeepEqual(nodeValue(prev), nodeValue(next)) {
				continue
			}
			found := false
			for j := 0; j < len(original.Content); j += 2 {
				if original.Content[j].Value == key.Value {
					original.Content[j+1] = patchNode(original.Content[j+1], prev, next)
					found = true
					break
				}
			}
			if !found {
				original.Content = append(original.Content, key, next)
			}
		}
		return original
	}
	if original != nil && before != nil && after != nil && original.Kind == yaml.SequenceNode && before.Kind == yaml.SequenceNode && after.Kind == yaml.SequenceNode {
		content := []*yaml.Node{}
		for i, next := range after.Content {
			j := i
			if name := field(next, "name"); name != nil {
				j = -1
				for k, prev := range before.Content {
					if n := field(prev, "name"); n != nil && n.Value == name.Value {
						j = k
						break
					}
				}
			}
			if j >= 0 && j < len(original.Content) && j < len(before.Content) {
				content = append(content, patchNode(original.Content[j], before.Content[j], next))
			} else {
				content = append(content, next)
			}
		}
		original.Content = content
		return original
	}
	if original != nil && after != nil {
		after.HeadComment, after.LineComment, after.FootComment = original.HeadComment, original.LineComment, original.FootComment
	}
	return after
}
func encodeDocument(doc *yaml.Node) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
