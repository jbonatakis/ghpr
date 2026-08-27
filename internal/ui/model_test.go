package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// isolateConfig points the config file at a scratch directory so tests never
// read or overwrite the developer's real preferences.
func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// loadFixture replays the captured GitHub payload through the real client so
// the UI is exercised with genuinely-shaped data.
func loadFixture(t *testing.T) gh.Result { return loadFixtureFile(t, "search_authored.json") }

func loadFixtureFile(t *testing.T, name string) gh.Result {
	t.Helper()
	raw, err := os.ReadFile("../gh/testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
		map[string]any{"hasNextPage": false, "endCursor": ""}
	if raw, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	res, err := c.Fetch(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("fetch fixture: %v", err)
	}
	return res
}

// newUnloaded returns a sized model that has not polled yet, so a test can
// choose what its very first snapshot looks like.
func newUnloaded(t *testing.T, w, h int) Model {
	t.Helper()
	isolateConfig(t)
	m := New(Config{Client: gh.NewClient("test"), Mode: gh.ModeAuthored, Interval: 30 * time.Second, Max: 200, Prefs: config.Defaults(), Links: true})
	return update(m, tea.WindowSizeMsg{Width: w, Height: h})
}

// newLoaded returns a model sized to w x h with the fixture already applied.
func newLoaded(t *testing.T, w, h int) Model {
	t.Helper()
	isolateConfig(t)
	res := loadFixture(t)
	m := New(Config{Client: gh.NewClient("test"), Mode: gh.ModeAuthored, Interval: 30 * time.Second, Max: 200, Prefs: config.Defaults(), Links: true})
	m = update(m, tea.WindowSizeMsg{Width: w, Height: h})
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: res})
	return m
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func lines(s string) []string { return strings.Split(s, "\n") }

func TestViewFitsWithinTerminalWidth(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{60, 20}, {80, 24}, {100, 30}, {120, 40}, {200, 50},
	} {
		m := newLoaded(t, size.w, size.h)
		m.showDetail = true
		m.showEvents = true
		m.rebuild()
		for i, ln := range lines(m.View()) {
			if got := ansi.StringWidth(ln); got > size.w {
				t.Errorf("width %d: line %d is %d cells wide: %q", size.w, i, got, ansi.Strip(ln))
			}
		}
	}
}

func TestViewFitsWithinTerminalHeight(t *testing.T) {
	for _, size := range []struct{ w, h int }{{100, 24}, {100, 40}, {80, 20}} {
		m := newLoaded(t, size.w, size.h)
		if got := len(lines(m.View())); got > size.h {
			t.Errorf("size %dx%d rendered %d lines", size.w, size.h, got)
		}
		m.showDetail, m.showEvents = true, true
		if got := len(lines(m.View())); got > size.h {
			t.Errorf("size %dx%d with panes rendered %d lines", size.w, size.h, got)
		}
	}
}

func TestViewShowsReposAndPullRequests(t *testing.T) {
	m := newLoaded(t, 140, 60)
	out := ansi.Strip(m.View())

	for _, want := range []string{"acme/starfield", "#96", "FAILING", "octo-dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestGroupingProducesRepoHeaders(t *testing.T) {
	m := newLoaded(t, 120, 60)

	var repoRows, prRows int
	for _, r := range m.rows {
		if r.isRepo() {
			repoRows++
		} else {
			prRows++
		}
	}
	if repoRows != 9 {
		t.Errorf("repo header rows = %d, want 9 distinct repos", repoRows)
	}
	if prRows != 11 {
		t.Errorf("PR rows = %d, want 11", prRows)
	}
}

func TestCollapsingRepoHidesItsPullRequests(t *testing.T) {
	m := newLoaded(t, 120, 60)
	before := len(m.rows)

	m.cursor = 0
	repo := m.rows[0].repo
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})

	if !m.collapsed[repo] {
		t.Fatalf("%s did not collapse", repo)
	}
	if len(m.rows) >= before {
		t.Errorf("rows = %d after collapsing, want fewer than %d", len(m.rows), before)
	}
	for _, r := range m.rows {
		if !r.isRepo() && r.repo == repo {
			t.Errorf("%s still lists PR rows while collapsed", repo)
		}
	}
}

func TestUngroupedViewIsFlat(t *testing.T) {
	m := newLoaded(t, 120, 60)
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	if m.grouped {
		t.Fatal("expected grouping to toggle off")
	}
	for _, r := range m.rows {
		if r.isRepo() {
			t.Fatal("ungrouped view should contain no repo headers")
		}
	}
	if len(m.rows) != 11 {
		t.Errorf("flat rows = %d, want 11", len(m.rows))
	}
}

func TestFilterNarrowsRowsAndClearsOnEscape(t *testing.T) {
	m := newLoaded(t, 120, 60)

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if !m.filtering {
		t.Fatal("expected filter mode")
	}
	for _, r := range "starfield" {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	var prRows int
	for _, row := range m.rows {
		if !row.isRepo() {
			prRows++
			if !strings.Contains(row.repo, "starfield") {
				t.Errorf("filter leaked %s", row.repo)
			}
		}
	}
	if prRows != 3 {
		t.Errorf("filtered PR rows = %d, want 3 starfield PRs", prRows)
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.filtering {
		t.Error("escape should leave filter mode")
	}
	if got := len(m.visiblePRs()); got != 11 {
		t.Errorf("after clearing, visible PRs = %d, want 11", got)
	}
}

func TestFilterMatchesStatusAndNumber(t *testing.T) {
	m := newLoaded(t, 120, 60)
	m.filter.SetValue("conflict")
	m.rebuild()
	if got := len(m.visiblePRs()); got != 2 {
		t.Errorf("status filter matched %d PRs, want 2 conflicting", got)
	}

	m.filter.SetValue("96")
	m.rebuild()
	if got := len(m.visiblePRs()); got != 1 {
		t.Errorf("number filter matched %d PRs, want 1", got)
	}
}

func TestHideDraftsToggle(t *testing.T) {
	m := newLoaded(t, 120, 60)
	full := len(m.visiblePRs())

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if got := len(m.visiblePRs()); got != full-1 {
		t.Errorf("hiding drafts left %d PRs, want %d", got, full-1)
	}
	for _, p := range m.visiblePRs() {
		if p.IsDraft {
			t.Errorf("%s is a draft and should be hidden", p.Key())
		}
	}
}

func TestSortByAttentionPutsUrgentWorkFirst(t *testing.T) {
	m := newLoaded(t, 120, 60)
	m.grouped = false
	m.rebuild()

	var prev gh.Status
	for i, r := range m.rows {
		s := r.pr.Status()
		if i > 0 && s < prev {
			t.Errorf("row %d (%s, %v) sorts after %v", i, r.pr.Key(), s, prev)
		}
		prev = s
	}
}

func TestCursorNavigationStaysInBounds(t *testing.T) {
	m := newLoaded(t, 120, 30)

	for i := 0; i < len(m.rows)+20; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.cursor != len(m.rows)-1 {
		t.Errorf("cursor = %d after scrolling past the end, want %d", m.cursor, len(m.rows)-1)
	}
	for i := 0; i < len(m.rows)+20; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	}
	if m.cursor != 0 {
		t.Errorf("cursor = %d after scrolling past the top, want 0", m.cursor)
	}
	if m.top != 0 {
		t.Errorf("scroll offset = %d at the top, want 0", m.top)
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	m := newLoaded(t, 120, 14) // deliberately short: fewer rows than PRs
	for i := 0; i < len(m.rows)-1; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	if m.cursor < m.top || m.cursor >= m.top+m.listHeight() {
		t.Errorf("cursor %d outside window [%d,%d)", m.cursor, m.top, m.top+m.listHeight())
	}
}

func TestSelectionSurvivesRefresh(t *testing.T) {
	m := newLoaded(t, 120, 60)
	for i := 0; i < 4; i++ {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	want := m.selectedKey()
	if want == "" {
		t.Fatal("nothing selected")
	}

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})
	if got := m.selectedKey(); got != want {
		t.Errorf("selection moved across refresh: got %q, want %q", got, want)
	}
}

func TestRefreshRecordsActivityAndMarksPRsFresh(t *testing.T) {
	m := newLoaded(t, 120, 60)
	if len(m.events) != 0 {
		t.Fatalf("first load should not generate activity, got %d events", len(m.events))
	}

	next := loadFixture(t)
	for i := range next.PRs {
		if next.PRs[i].Key() == "acme/starfield#96" {
			next.PRs[i].IssueComments += 2
		}
	}
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})

	if len(m.events) == 0 {
		t.Fatal("expected an activity event after comments appeared")
	}
	if !m.isFresh("acme/starfield#96") {
		t.Error("changed PR should be marked fresh")
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "2 new comments") {
		m.showEvents = true
		if out = ansi.Strip(m.View()); !strings.Contains(out, "2 new comments") {
			t.Error("activity pane should show the new comments")
		}
	}
}

func TestFailedRefreshKeepsDataAndSchedulesAnother(t *testing.T) {
	m := newLoaded(t, 120, 60)
	rows := len(m.rows)

	// A timeout is transient, so it is reported softly rather than as a fault
	// of the user's; see resilience_test.go for the escalation path.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: context.DeadlineExceeded})
	if m.warn == "" && m.err == nil {
		t.Fatal("a failed poll should be reported somehow")
	}
	if len(m.rows) != rows {
		t.Errorf("rows changed on a failed refresh: %d -> %d", rows, len(m.rows))
	}
	if !m.nextFetch.After(time.Now()) {
		t.Error("a failed poll should still schedule the next one")
	}
}

func TestDetailPaneDescribesSelection(t *testing.T) {
	m := newLoaded(t, 140, 60)
	m.grouped = false
	m.rebuild()
	m.cursor = 0
	m.showDetail = true

	p := m.selected()
	if p == nil {
		t.Fatal("no selection")
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{p.Repo, p.URL, "state", "checks", "comments"} {
		if !strings.Contains(out, want) {
			t.Errorf("detail pane missing %q", want)
		}
	}
}

func TestHelpOverlayRenders(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.showHelp {
		t.Fatal("help not shown")
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "keys") || !strings.Contains(out, "filter") {
		t.Error("help overlay is missing its key list")
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if m.showHelp {
		t.Error("any key should dismiss help")
	}
}

func TestEmptyResultRendersCleanly(t *testing.T) {
	isolateConfig(t)
	m := New(Config{Client: gh.NewClient("t"), Mode: gh.ModeAuthored, Interval: time.Minute, Max: 10, Prefs: config.Defaults(), Links: true})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: gh.Result{Viewer: "octo-dev", FetchedAt: time.Now()}})

	out := m.View()
	if !strings.Contains(ansi.Strip(out), "no open pull requests") {
		t.Error("expected an empty-state message")
	}
	if got := len(lines(out)); got > 24 {
		t.Errorf("empty state rendered %d lines, want <= 24", got)
	}
}

func TestTickSchedulesRefreshWhenDue(t *testing.T) {
	m := newLoaded(t, 100, 30)
	m.nextFetch = time.Now().Add(-time.Second)
	m.loading = false

	next, cmd := m.Update(tickMsg(time.Now()))
	if cmd == nil {
		t.Fatal("expected a command batch on tick")
	}
	if !next.(Model).loading {
		t.Error("an overdue tick should start a refresh")
	}
}
