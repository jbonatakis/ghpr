package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func esc(m Model) (Model, tea.Cmd) {
	return updateCmd(m, tea.KeyMsg{Type: tea.KeyEsc})
}

// esc is the key people press to cancel something. Binding it to quit meant
// the cancel gesture closed the dashboard, which is the one thing it must
// never do.
func TestEscapeDoesNotQuit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(Model) Model
	}{
		{"on the bare list", func(m Model) Model { return m }},
		{"on a repo header", func(m Model) Model { m.cursor = 0; return m }},
		{"with the detail pane open", func(m Model) Model { return press(m, 'd') }},
		{"with the feed open but not focused", func(m Model) Model { return press(m, 'e') }},
		{"with hidden items showing", func(m Model) Model { return press(m, 'H') }},
		{"with drafts hidden", func(m Model) Model { return press(m, 'D') }},
		{"after a committed filter", func(m Model) Model {
			m = typed(press(m, '/'), "starfield")
			return update(m, tea.KeyMsg{Type: tea.KeyEnter})
		}},
	} {
		m := tc.setup(newLoaded(t, 140, 40))
		if _, cmd := esc(m); cmd != nil {
			t.Errorf("%s: esc returned a command, and the only one here is quit", tc.name)
		}
	}
}

// q and ctrl+c are the ways out, and they still are.
func TestQuitKeysStillQuit(t *testing.T) {
	m := newLoaded(t, 140, 40)
	if _, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Error("q no longer quits")
	}
	if _, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("ctrl+c no longer quits")
	}
}

// Having taken esc off the exit, it should do the thing people reach for it
// to do: back out of whatever is narrowing the view.
func TestEscapeClearsACommittedFilter(t *testing.T) {
	m := newLoaded(t, 140, 40)
	all := len(m.visiblePRs())

	m = typed(press(m, '/'), "starfield")
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.filtering {
		t.Fatal("enter should have closed the filter box")
	}
	if narrowed := len(m.visiblePRs()); narrowed == all {
		t.Fatalf("the filter narrowed nothing: still %d", narrowed)
	}

	m, _ = esc(m)
	if got := m.filter.Value(); got != "" {
		t.Errorf("esc left the filter as %q", got)
	}
	if got := len(m.visiblePRs()); got != all {
		t.Errorf("after esc, %d pull requests visible, want all %d", got, all)
	}
}

// With nothing to back out of, esc is inert — not a quit, and not a surprise.
func TestEscapeOnAnUnfilteredListDoesNothing(t *testing.T) {
	m := newLoaded(t, 140, 40)
	before := len(m.rows)

	m, cmd := esc(m)
	if cmd != nil {
		t.Error("esc did something on a list with nothing to cancel")
	}
	if len(m.rows) != before {
		t.Errorf("the list changed from %d rows to %d", before, len(m.rows))
	}
	if m.toast != "" {
		t.Errorf("esc announced something it did not do: %q", m.toast)
	}
}

// The help overlay closes on any key, and esc is now simply one of them.
func TestEscapeDismissesHelpRatherThanQuitting(t *testing.T) {
	m := press(newLoaded(t, 140, 40), '?')
	if !m.showHelp {
		t.Fatal("? did not open the help overlay")
	}

	m, cmd := esc(m)
	if cmd != nil {
		t.Error("esc quit from the help overlay")
	}
	if m.showHelp {
		t.Error("esc did not dismiss the help overlay")
	}
}

// The escape ladder, end to end: out of the feed's filter, out of the feed,
// out of the list's filter, and then nothing — never out of the app.
func TestTheEscapeLadderNeverReachesTheExit(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = mixedFeed()
	m = typed(press(m, '/'), "starfield") // a list filter, committed
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	m = press(press(m, 'e'), 'e') // into the feed
	m = typed(press(m, '/'), "dana")

	var cmd tea.Cmd
	for i, want := range []string{
		"the feed's filter",
		"the feed itself",
		"the list's filter",
		"nothing left",
	} {
		m, cmd = esc(m)
		if cmd != nil {
			t.Fatalf("step %d (%s): esc quit", i+1, want)
		}
	}

	if m.feedFiltered() || m.eventsFocus || m.filter.Value() != "" {
		t.Errorf("the ladder did not unwind: feedFilter=%q focus=%v filter=%q",
			m.feedFilter.Value(), m.eventsFocus, m.filter.Value())
	}
}

// The org picker has always cancelled on esc, and that must survive esc no
// longer being a quit key.
func TestEscapeStillCancelsTheOrgPicker(t *testing.T) {
	m := press(newLoaded(t, 140, 40), 'O')
	if !m.showOrgs {
		t.Fatal("O did not open the organization picker")
	}

	m, cmd := esc(m)
	if cmd != nil {
		t.Error("esc quit from the organization picker")
	}
	if m.showOrgs {
		t.Error("esc did not close the organization picker")
	}
}

func TestTheHelpOverlayNoLongerCallsEscapeAQuit(t *testing.T) {
	out := ansi.Strip(press(newLoaded(t, 104, 46), '?').View())
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "esc") && strings.Contains(ln, "quit") {
			t.Errorf("the help still offers esc as a way out: %q", ln)
		}
	}
	if !strings.Contains(out, "q         quit") {
		t.Errorf("the help no longer names a way out at all:\n%s", out)
	}
}
