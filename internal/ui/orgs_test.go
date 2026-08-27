package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/config"
)

func press(m Model, r rune) Model {
	return update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
}

// cursorToOrg puts the picker's cursor on a named organization. Selecting by
// name rather than by index keeps these tests independent of how the names
// happen to sort.
func cursorToOrg(t *testing.T, m Model, name string) Model {
	t.Helper()
	for i, e := range m.orgEntries() {
		if e.name == name {
			m.orgCursor = i
			return m
		}
	}
	t.Fatalf("picker has no organization %q (has %v)", name, orgNames(m))
	return m
}

func orgNames(m Model) []string {
	var out []string
	for _, e := range m.orgEntries() {
		out = append(out, e.name)
	}
	return out
}

func TestOrgPickerListsEveryOrganization(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = press(m, 'O')

	if !m.showOrgs {
		t.Fatal("O should open the organization picker")
	}
	got := orgNames(m)
	if len(got) != 2 {
		t.Fatalf("orgs = %v, want two", got)
	}
	if got[0] != "acme" || got[1] != "octo-dev" {
		t.Errorf("orgs = %v, want them sorted alphabetically", got)
	}
	for _, e := range m.orgEntries() {
		if e.prs == 0 {
			t.Errorf("%s should report a PR count", e.name)
		}
	}
}

func TestHidingAnOrgFiltersTheDashboard(t *testing.T) {
	m := newLoaded(t, 120, 40)
	before := len(m.visiblePRs())

	m = press(m, 'O')
	m = cursorToOrg(t, m, "octo-dev")
	m = press(m, ' ')
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.showOrgs {
		t.Fatal("enter should close the picker")
	}
	if !m.hiddenOrgs["octo-dev"] {
		t.Fatal("octo-dev should be hidden")
	}
	after := m.visiblePRs()
	if len(after) >= before {
		t.Errorf("visible PRs = %d, want fewer than %d", len(after), before)
	}
	for _, p := range after {
		if p.Org() == "octo-dev" {
			t.Errorf("%s leaked past the org filter", p.Key())
		}
	}
	for _, r := range m.rows {
		if strings.HasPrefix(r.repo, "octo-dev/") {
			t.Errorf("row %s still present after hiding its org", r.repo)
		}
	}
}

func TestOnlyThisOrgFocusesASingleOrganization(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = press(m, 'O')
	m = cursorToOrg(t, m, "acme")
	m = press(m, 'o') // only this one
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	for _, p := range m.visiblePRs() {
		if p.Org() != "acme" {
			t.Errorf("%s should have been filtered out", p.Key())
		}
	}
	if len(m.visiblePRs()) == 0 {
		t.Error("focusing an org should still show its PRs")
	}
}

func TestShowAllClearsTheFilter(t *testing.T) {
	m := newLoaded(t, 120, 40)
	total := len(m.visiblePRs())

	m = press(m, 'O')
	m = press(m, 'n') // hide all
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.visiblePRs()) != 0 {
		t.Fatalf("hiding every org left %d PRs", len(m.visiblePRs()))
	}

	m = press(m, 'O')
	m = press(m, 'a') // show all
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(m.visiblePRs()); got != total {
		t.Errorf("visible PRs = %d after showing all, want %d", got, total)
	}
}

func TestEscapeAbandonsOrgEdits(t *testing.T) {
	m := newLoaded(t, 120, 40)
	before := len(m.visiblePRs())

	m = press(m, 'O')
	m = press(m, ' ')
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})

	if m.showOrgs {
		t.Fatal("escape should close the picker")
	}
	if len(m.hiddenOrgs) != 0 {
		t.Errorf("escape should discard edits, got %v", m.hiddenOrgs)
	}
	if got := len(m.visiblePRs()); got != before {
		t.Errorf("visible PRs = %d, want %d unchanged", got, before)
	}
}

func TestHiddenOrgSurvivesAndIsStillListed(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = press(m, 'O')
	m = cursorToOrg(t, m, "octo-dev")
	m = press(m, ' ')
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	// Reopening must still offer the hidden org, or it could never come back.
	m = press(m, 'O')
	found := false
	for _, e := range m.orgEntries() {
		if e.name == "octo-dev" {
			found = true
			if e.visible {
				t.Error("octo-dev should render as hidden")
			}
		}
	}
	if !found {
		t.Error("a hidden org disappeared from the picker")
	}
}

func TestOrgChoicePersistsToConfig(t *testing.T) {
	m := newLoaded(t, 120, 40) // isolateConfig already redirected XDG_CONFIG_HOME
	m = press(m, 'O')
	m = cursorToOrg(t, m, "octo-dev")
	m = press(m, ' ')
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !saved.Hidden("octo-dev") {
		t.Errorf("hidden org not persisted, got %v", saved.HiddenOrgs)
	}

	// A fresh model built from that config starts already filtered.
	next := New(Config{Client: m.cfg.Client, Mode: m.cfg.Mode, Interval: m.cfg.Interval, Max: 200, Prefs: saved})
	next = update(next, tea.WindowSizeMsg{Width: 120, Height: 40})
	next = update(next, fetchDoneMsg{seq: next.fetchSeq, res: loadFixture(t)})
	for _, p := range next.visiblePRs() {
		if p.Org() == "octo-dev" {
			t.Errorf("restored config did not apply the org filter: %s", p.Key())
		}
	}
}

func TestSortAndGroupingPersist(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = press(m, 's') // cycle sort off the default
	m = press(m, 't') // toggle grouping off

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.Sort != m.sortBy.key() {
		t.Errorf("saved sort = %q, want %q", saved.Sort, m.sortBy.key())
	}
	if saved.Grouped {
		t.Error("grouping toggle was not persisted")
	}
	if parseSort(saved.Sort) != m.sortBy {
		t.Error("saved sort does not parse back to the same mode")
	}
}

func TestOrgPickerViewRendersWithinWidth(t *testing.T) {
	m := newLoaded(t, 100, 40)
	m = press(m, 'O')
	for i, ln := range strings.Split(m.View(), "\n") {
		if got := ansi.StringWidth(ln); got > 100 {
			t.Errorf("picker line %d is %d cells: %q", i, got, ansi.Strip(ln))
		}
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{"organizations", "acme", "save", "cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("picker missing %q", want)
		}
	}
}

func TestHeaderReportsHiddenPullRequests(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m = press(m, 'O')
	m = cursorToOrg(t, m, "octo-dev")
	m = press(m, ' ')
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if out := ansi.Strip(m.View()); !strings.Contains(out, "hidden") {
		t.Error("header should say how many PRs the org filter is hiding")
	}
}
