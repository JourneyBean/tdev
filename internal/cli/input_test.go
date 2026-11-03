package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCompletionViewportAndResize(t *testing.T) {
	m := &promptModel{ctx: context.Background(), label: "远端已有项目目录（绝对路径或 ~/路径，Tab 补全）", input: textinput.New(), selected: 49}
	m.input.SetValue("~/" + strings.Repeat("目录很长/", 40))
	for i := range 50 {
		m.matches = append(m.matches, fmt.Sprintf("~/projects/%02d-%s/", i, strings.Repeat("中文🙂项目", 40)))
	}
	for _, size := range [][2]int{{40, 8}, {120, 24}, {24, 6}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View().Content
		lines := strings.Split(view, "\n")
		if len(lines) >= size[1] {
			t.Fatalf("menu exceeds terminal height: %d rows for %v", len(lines), size)
		}
		for _, line := range lines {
			if width := ansi.StringWidth(line); width >= size[0] {
				t.Fatalf("menu exceeds terminal width: %d columns for %v: %q", width, size, line)
			}
		}
		if !strings.Contains(view, "> 49-") {
			t.Fatal("selected candidate scrolled out of view", view)
		}
		if !strings.Contains(view, "…") {
			t.Fatal("long names should be truncated for display")
		}
	}
	if !strings.HasSuffix(m.matches[49], strings.Repeat("中文🙂项目", 40)+"/") {
		t.Fatal("display truncation changed actual completion")
	}
}

func TestCompletionDiscardsStaleResults(t *testing.T) {
	m := &promptModel{ctx: context.Background(), input: textinput.New(), selected: -1, queryID: 3}
	m.input.SetValue("~/new")
	m.Update(completionResult{id: 2, matches: []string{"~/old-project/"}})
	if m.input.Value() != "~/new" || len(m.matches) != 0 {
		t.Fatal("stale result overwrote edited input")
	}
}
