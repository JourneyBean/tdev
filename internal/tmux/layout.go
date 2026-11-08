package tmux

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"tdev/internal/config"
	"tdev/internal/protocol"
)

func panePath(p config.Project, directory string) string {
	return config.WindowPath(p, config.Window{Directory: directory})
}

func validatePaneDirectories(p config.Project, node config.Pane) error {
	if node.Split == "" {
		path := panePath(p, node.Directory)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return config.Invalid("pane directory missing: %s", path)
		}
	}
	for _, child := range node.Panes {
		if err := validatePaneDirectories(p, child); err != nil {
			return err
		}
	}
	return nil
}

func (m Manager) startPane(ctx context.Context, pane, id, directory, command string) error {
	if _, err := m.run(ctx, "set-option", "-p", "-t", pane, "@tdev_pane_id", id); err != nil {
		return err
	}
	// Let tmux open its normal interactive shell in the pane directory.
	if _, err := m.run(ctx, "respawn-pane", "-k", "-t", pane, "-c", directory); err != nil {
		return err
	}
	if command == "" {
		return nil
	}
	// Queue literal input so shell initialization completes before execution.
	if _, err := m.run(ctx, "send-keys", "-t", pane, "-l", "--", command); err != nil {
		return err
	}
	_, err := m.run(ctx, "send-keys", "-t", pane, "Enter")
	return err
}

func (m Manager) restoreLayout(ctx context.Context, p config.Project, pane string, node config.Pane) error {
	if node.Split == "" {
		return m.startPane(ctx, pane, node.ID, panePath(p, node.Directory), node.Command)
	}
	direction := "-h"
	if node.Split == "vertical" {
		direction = "-v"
	}
	targets := []string{pane}
	current := pane
	for i := 1; i < len(node.Panes); i++ {
		weight := func(n config.Pane) int {
			if n.Weight == 0 {
				return 1
			}
			return n.Weight
		}
		total, remaining := weight(node.Panes[i-1]), 0
		for _, child := range node.Panes[i:] {
			remaining += weight(child)
		}
		total += remaining
		percent := (100*remaining + total/2) / total
		if percent < 1 {
			percent = 1
		}
		if percent > 99 {
			percent = 99
		}
		next, err := m.run(ctx, "split-window", "-d", direction, "-l", strconv.Itoa(percent)+"%", "-t", current, "-c", p.Directory, "-P", "-F", "#{pane_id}")
		if err != nil {
			return err
		}
		targets = append(targets, next)
		current = next
	}
	for i, child := range node.Panes {
		if err := m.restoreLayout(ctx, p, targets[i], child); err != nil {
			return err
		}
	}
	return nil
}

type livePane struct{ PaneID, ID, Directory string }

// tmux window_layout contains geometry and pane numbers, never shell history or
// process arguments. Parse its tree to retain split direction and proportions.
type layoutParser struct {
	text     string
	at       int
	panes    map[string]livePane
	commands map[string]string
	adopt    map[string]string
}

func (p *layoutParser) number() (int, error) {
	start := p.at
	for p.at < len(p.text) && p.text[p.at] >= '0' && p.text[p.at] <= '9' {
		p.at++
	}
	n, err := strconv.Atoi(p.text[start:p.at])
	return n, err
}
func (p *layoutParser) take(c byte) error {
	if p.at >= len(p.text) || p.text[p.at] != c {
		return fmt.Errorf("invalid tmux layout")
	}
	p.at++
	return nil
}
func (p *layoutParser) node() (config.Pane, int, int, error) {
	var node config.Pane
	width, err := p.number()
	if err != nil {
		return node, 0, 0, err
	}
	if err = p.take('x'); err != nil {
		return node, 0, 0, err
	}
	height, err := p.number()
	if err != nil {
		return node, 0, 0, err
	}
	for i := 0; i < 2; i++ {
		if err = p.take(','); err != nil {
			return node, 0, 0, err
		}
		if _, err = p.number(); err != nil {
			return node, 0, 0, err
		}
	}
	if p.at < len(p.text) && (p.text[p.at] == '{' || p.text[p.at] == '[') {
		opening := p.text[p.at]
		p.at++
		closing := byte('}')
		node.Split = "horizontal"
		if opening == '[' {
			closing = ']'
			node.Split = "vertical"
		}
		for {
			child, w, h, err := p.node()
			if err != nil {
				return node, 0, 0, err
			}
			child.Weight = w
			if node.Split == "vertical" {
				child.Weight = h
			}
			node.Panes = append(node.Panes, child)
			if p.at < len(p.text) && p.text[p.at] == closing {
				p.at++
				break
			}
			if err = p.take(','); err != nil {
				return node, 0, 0, err
			}
		}
	} else {
		if err = p.take(','); err != nil {
			return node, 0, 0, err
		}
		n, err := p.number()
		if err != nil {
			return node, 0, 0, err
		}
		pane, ok := p.panes["%"+strconv.Itoa(n)]
		if !ok {
			return node, 0, 0, fmt.Errorf("tmux pane changed during layout collection")
		}
		node.ID = pane.ID
		if node.ID == "" {
			node.ID = "p" + strings.ReplaceAll(config.NewID(), "-", "")
			p.adopt[pane.PaneID] = node.ID
		}
		node.Directory = filepath.Clean(pane.Directory)
		node.Command = p.commands[node.ID]
	}
	return node, width, height, nil
}

func savedLayout(w LiveWindow, old config.Window, adopt map[string]string) (*config.Pane, error) {
	commands := map[string]string{}
	var collect func(config.Pane)
	collect = func(n config.Pane) {
		if n.Split == "" {
			commands[n.ID] = n.Command
		}
		for _, c := range n.Panes {
			collect(c)
		}
	}
	if old.Layout != nil {
		collect(*old.Layout)
	} else {
		commands[old.ID] = old.Command
	}
	panes := map[string]livePane{}
	identifiedOriginal := false
	for _, pane := range w.Leaves {
		if pane.ID == old.ID && pane.ID != "" {
			identifiedOriginal = true
		}
	}
	for i, pane := range w.Leaves {
		// Older sessions have no pane markers. A split service window needs an
		// explicit identity before its declared command can be retained safely.
		if i == 0 && pane.ID == "" && old.Layout == nil && old.ID != "" && !identifiedOriginal {
			if w.Panes > 1 && old.Command != "" {
				return nil, protocol.Fail("SESSION_CONFLICT", 4, "window %q predates pane identities; mark the original service pane with @tdev_pane_id=%s before saving", w.Name, old.ID)
			}
			pane.ID = old.ID
			adopt[pane.PaneID] = pane.ID
		}
		panes[pane.PaneID] = pane
	}
	_, text, ok := strings.Cut(w.Layout, ",")
	if !ok {
		return nil, fmt.Errorf("invalid tmux layout checksum prefix")
	}
	parser := layoutParser{text: text, panes: panes, commands: commands, adopt: adopt}
	node, _, _, err := parser.node()
	if err != nil {
		return nil, err
	}
	if parser.at != len(text) {
		return nil, fmt.Errorf("trailing tmux layout data")
	}
	return &node, nil
}
