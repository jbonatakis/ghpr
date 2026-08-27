package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// withColor forces a color profile so styling is actually emitted; tests do
// not run on a terminal, where lipgloss would otherwise strip every escape.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// selectedLine finds the rendered row the cursor is on.
func selectedLine(t *testing.T, m Model) string {
	t.Helper()
	for _, ln := range strings.Split(m.View(), "\n") {
		if strings.Contains(ansi.Strip(ln), "▸") {
			return ln
		}
	}
	t.Fatal("no selected row found in the frame")
	return ""
}

func countBackgrounds(s string) int {
	return strings.Count(s, "\x1b[48;2;")
}

func TestSelectedRowIsHighlightedAcrossEveryColumn(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 150, 40)
	m.grouped = false
	m.rebuild()
	m.cursor = 0

	line := selectedLine(t, m)

	// The band must reach the right edge, not stop after the title.
	if got := ansi.StringWidth(line); got != 150 {
		t.Errorf("selected row is %d cells wide, want the full 150", got)
	}
	// Applied per cell, so each column keeps its own foreground colour while
	// still sitting on the highlight.
	if n := countBackgrounds(line); n < 8 {
		t.Errorf("background appears %d times; expected it on every cell", n)
	}
	// The very end of the row carries it too.
	if !strings.Contains(line[len(line)/2:], "\x1b[48;2;") {
		t.Error("the right-hand columns are not highlighted")
	}
}

func TestUnselectedRowsAreNotHighlighted(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 150, 40)
	m.grouped = false
	m.rebuild()
	m.cursor = 0

	lines := strings.Split(m.View(), "\n")
	var highlighted int
	for _, ln := range lines {
		if countBackgrounds(ln) > 0 {
			highlighted++
		}
	}
	if highlighted != 1 {
		t.Errorf("%d lines are highlighted, want exactly the selected row", highlighted)
	}
}

func TestHighlightFollowsTheCursor(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 150, 40)
	m.grouped = false
	m.rebuild()

	first := ansi.Strip(selectedLine(t, m))
	m = press(m, 'j')
	second := ansi.Strip(selectedLine(t, m))

	if first == second {
		t.Error("the highlight did not move with the cursor")
	}
	if got := ansi.StringWidth(selectedLine(t, m)); got != 150 {
		t.Errorf("row after moving is %d cells wide, want 150", got)
	}
}

func TestSelectedRepoHeaderIsHighlightedToFullWidth(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 150, 40)
	m.cursor = 0 // grouped, so row 0 is a repo header

	if !m.rows[0].isRepo() {
		t.Fatal("expected a repo header first")
	}
	line := selectedLine(t, m)
	if got := ansi.StringWidth(line); got != 150 {
		t.Errorf("selected repo header is %d cells wide, want 150", got)
	}
}

func TestHighlightPreservesPerColumnColors(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 150, 40)
	m.grouped = false
	m.rebuild()
	m.cursor = 0 // the most urgent PR: a red status badge

	line := selectedLine(t, m)
	// Foreground colours must survive alongside the background.
	if !strings.Contains(line, "\x1b[38;2;") {
		t.Error("selected row lost its foreground colours")
	}
	if !strings.Contains(ansi.Strip(line), "CHANGES") {
		t.Errorf("expected the urgent status badge on the first row: %q", ansi.Strip(line))
	}
}

func TestNarrowTerminalStillHighlightsFullWidth(t *testing.T) {
	withColor(t)
	for _, w := range []int{60, 80, 100, 116} {
		m := newLoaded(t, w, 30)
		m.grouped = false
		m.rebuild()
		m.cursor = 0
		if got := ansi.StringWidth(selectedLine(t, m)); got != w {
			t.Errorf("width %d: selected row is %d cells", w, got)
		}
	}
}
