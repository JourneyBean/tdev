package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
	"tdev/internal/protocol"
)

// ClientDocument retains the sources needed for a checked, comment-preserving edit.
// Callers hold client/config while reading and saving a client transaction.
type ClientDocument struct {
	Data    *yaml.Node
	Present bool
	main    *sourceFile
	files   []*sourceFile
	origins map[string]projectSource
}

func emptyMapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
func setField(n *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if value == nil {
				n.Content = append(n.Content[:i], n.Content[i+2:]...)
			} else {
				n.Content[i+1] = value
			}
			return
		}
	}
	if value != nil {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	}
}
func (s Store) ReadClient() (ClientDocument, error) {
	d := ClientDocument{Data: emptyMapping(), origins: map[string]projectSource{}}
	path := filepath.Join(s.Root, "config.yaml")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return d, nil
	} else if err != nil {
		return d, err
	}
	f, err := parseFile(path)
	if err != nil {
		return d, err
	}
	f.path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return d, err
	}
	d.main = f
	d.files = append(d.files, f)
	client := field(f.doc.Content[0], "client")
	if client == nil {
		return d, nil
	}
	if client.Kind != yaml.MappingNode {
		return d, Invalid("%s:%d: client must be a mapping", path, client.Line)
	}
	d.Present = true
	paths := []string{}
	if include := field(client, "include"); include != nil {
		var patterns []string
		if err := strictNode(include, &patterns); err != nil {
			return d, Invalid("%s:%d: client.include: %v", path, include.Line, err)
		}
		for _, pattern := range patterns {
			if pattern == "" {
				return d, Invalid("client.include path cannot be empty")
			}
			if !filepath.IsAbs(pattern) {
				pattern = filepath.Join(filepath.Dir(path), pattern)
			}
			if strings.ContainsAny(pattern, "*?[") {
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return d, Invalid("invalid client.include pattern: %v", err)
				}
				paths = append(paths, matches...)
			} else {
				paths = append(paths, pattern)
			}
		}
	}
	add := func(file *sourceFile, node *yaml.Node) error {
		if node.Kind != yaml.MappingNode {
			return Invalid("%s:%d: expected client settings mapping", file.path, node.Line)
		}
		for i := 0; i < len(node.Content); i += 2 {
			key, value := node.Content[i].Value, node.Content[i+1]
			if key == "include" && file == f {
				continue
			}
			switch key {
			case "servers", "overrides":
				if value.Kind != yaml.MappingNode {
					return Invalid("%s:%d: %s must be a mapping", file.path, value.Line, key)
				}
				dst := field(d.Data, key)
				if dst == nil {
					dst = emptyMapping()
					setField(d.Data, key, dst)
				}
				for j := 0; j < len(value.Content); j += 2 {
					name, v := value.Content[j].Value, value.Content[j+1]
					id := key + "/" + name
					if previous, ok := d.origins[id]; ok {
						return Invalid("duplicate client %s %q at %s:%d and %s:%d", key, name, previous.file.path, previous.node.Line, file.path, v.Line)
					}
					setField(dst, name, v)
					d.origins[id] = projectSource{file: file, node: v, name: name}
				}
			case "ssh", "reconnect":
				if previous, ok := d.origins[key]; ok {
					return Invalid("duplicate client %s at %s:%d and %s:%d", key, previous.file.path, previous.node.Line, file.path, value.Line)
				}
				setField(d.Data, key, value)
				d.origins[key] = projectSource{file: file, node: value}
			default:
				return Invalid("%s:%d: unknown client field %q (client is a reserved top-level name)", file.path, node.Content[i].Line, key)
			}
		}
		return nil
	}
	if err := add(f, client); err != nil {
		return d, err
	}
	seen := map[string]bool{f.path: true}
	for _, path := range paths {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return d, Invalid("client.include %s: %v", path, err)
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return d, err
		}
		mainCanonical, _ := filepath.Abs(f.path)
		if seen[canonical] || canonical == mainCanonical {
			return d, Invalid("client file included more than once: %s", path)
		}
		seen[canonical] = true
		fragment, err := parseFile(canonical)
		if err != nil {
			return d, err
		}
		if err := add(fragment, fragment.doc.Content[0]); err != nil {
			return d, err
		}
		d.files = append(d.files, fragment)
	}
	return d, nil
}
func backupClientFile(f *sourceFile) error {
	digest := sha256.Sum256(f.data)
	path := fmt.Sprintf("%s.client-%x.bak", f.path, digest[:6])
	if existing, err := os.ReadFile(path); err == nil {
		if !reflect.DeepEqual(existing, f.data) {
			return protocol.Fail("CONFIG_CONFLICT", 4, "client backup conflicts: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return AtomicWrite(path, f.data, 0600)
}

// SaveClient patches canonical changes into their original files. All files are
// checked before writing; partial multi-file commits are reported explicitly.
func (s Store) SaveClient(ctx context.Context, d ClientDocument, before, after *yaml.Node) error {
	unlock, err := s.Lock(ctx, "projects", "store")
	if err != nil {
		return err
	}
	defer unlock()
	for _, f := range d.files {
		if err := checkSource(f); err != nil {
			return err
		}
	}
	if d.main == nil {
		path := filepath.Join(s.Root, "config.yaml")
		d.main = &sourceFile{path: path, doc: &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{emptyMapping()}}}
		if err := checkSource(d.main); err != nil {
			return err
		}
		// Creating the main entry must preserve discovery of existing project fragments.
		setField(d.main.doc.Content[0], "projects", encodeNode(struct {
			Include []string `yaml:"include"`
		}{[]string{"./projects/*.yaml"}}))
	}
	if !d.Present {
		before = emptyMapping()
	}
	changed := map[*sourceFile]bool{}
	destination := func() *yaml.Node {
		root := d.main.doc.Content[0]
		node := field(root, "client")
		if node == nil {
			node = emptyMapping()
			setField(root, "client", node)
		}
		return node
	}
	if !d.Present {
		_ = destination()
	}
	// Iterate the union so removals write back to the source defining the entry.
	keys := map[string]bool{}
	for _, n := range []*yaml.Node{before, after} {
		if n != nil {
			for i := 0; i < len(n.Content); i += 2 {
				keys[n.Content[i].Value] = true
			}
		}
	}
	// Stable field order for newly generated declarations.
	for _, section := range []string{"servers", "overrides", "ssh", "reconnect"} {
		if !keys[section] {
			continue
		}
		prev, next := field(before, section), field(after, section)
		names := []string{""}
		if section == "servers" || section == "overrides" {
			names = nil
			seen := map[string]bool{}
			for _, n := range []*yaml.Node{prev, next} {
				if n != nil {
					for i := 0; i < len(n.Content); i += 2 {
						name := n.Content[i].Value
						if !seen[name] {
							names = append(names, name)
							seen[name] = true
						}
					}
				}
			}
		}
		for _, name := range names {
			a, b := prev, next
			id := section
			if name != "" {
				a, b = field(prev, name), field(next, name)
				id += "/" + name
			}
			if reflect.DeepEqual(nodeValue(a), nodeValue(b)) {
				continue
			}
			origin, exists := d.origins[id]
			file := d.main
			parent := destination()
			key := section
			if exists {
				file = origin.file
				parent = file.doc.Content[0]
				if file == d.main {
					parent = field(parent, "client")
				}
			}
			if name != "" {
				mapping := field(parent, section)
				if mapping == nil {
					mapping = emptyMapping()
					setField(parent, section, mapping)
				}
				parent, key = mapping, name
			}
			setField(parent, key, patchNode(field(parent, key), a, b))
			changed[file] = true
		}
	}
	if !d.Present {
		changed[d.main] = true
	}
	files := []*sourceFile{d.main}
	for _, f := range d.files {
		if f != d.main {
			files = append(files, f)
		}
	}
	type pending struct {
		f *sourceFile
		b []byte
	}
	plans := []pending{}
	for _, f := range files {
		if changed[f] {
			b, err := encodeDocument(f.doc)
			if err != nil {
				return err
			}
			plans = append(plans, pending{f, b})
		}
	}
	for _, plan := range plans {
		if err := checkSource(plan.f); err != nil {
			return err
		}
		if plan.f.data != nil {
			if err := backupClientFile(plan.f); err != nil {
				return err
			}
		}
	}
	for i, plan := range plans {
		err := checkSource(plan.f)
		if err == nil {
			err = AtomicWrite(plan.f.path, plan.b, 0600)
		}
		if err != nil {
			if i > 0 {
				return protocol.Fail("PARTIAL_SUCCESS", 9, "some client configuration files were saved: %v", err)
			}
			return err
		}
	}
	return nil
}
