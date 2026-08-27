package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// reopen rebuilds a model from whatever is on disk, standing in for a restart.
func reopen(t *testing.T, m Model) Model {
	t.Helper()
	saved, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	next := New(Config{
		Client: m.cfg.Client, Mode: m.cfg.Mode,
		Interval: m.cfg.Interval, Max: 200, Prefs: saved,
	})
	next = update(next, tea.WindowSizeMsg{Width: 120, Height: 40})
	return update(next, fetchDoneMsg{seq: next.fetchSeq, res: loadFixture(t)})
}

func cursorToRepo(t *testing.T, m Model, repo string) Model {
	t.Helper()
	for i, r := range m.rows {
		if r.isRepo() && r.repo == repo {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no repo row for %s", repo)
	return m
}

func cursorToPR(t *testing.T, m Model, key string) Model {
	t.Helper()
	for i, r := range m.rows {
		if !r.isRepo() && r.pr.Key() == key {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("no PR row for %s", key)
	return m
}

const hideRepo = "acme/starfield"

// --- folds ---------------------------------------------------------------

func TestRepoFoldsPersistAcrossSessions(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, ' ')

	if !m.collapsed[hideRepo] {
		t.Fatal("repo did not fold")
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.CollapsedRepos) != 1 || saved.CollapsedRepos[0] != hideRepo {
		t.Fatalf("CollapsedRepos = %v, want [%s]", saved.CollapsedRepos, hideRepo)
	}

	next := reopen(t, m)
	if !next.collapsed[hideRepo] {
		t.Error("fold did not survive a restart")
	}
	for _, r := range next.rows {
		if !r.isRepo() && r.repo == hideRepo {
			t.Error("a folded repo should list no PR rows after restart")
		}
	}
}

func TestUnfoldingPersistsToo(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, ' ') // fold
	m = press(m, ' ') // unfold

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range saved.CollapsedRepos {
		if r == hideRepo {
			t.Errorf("%s is still recorded as folded", hideRepo)
		}
	}
	if next := reopen(t, m); next.collapsed[hideRepo] {
		t.Error("repo came back folded")
	}
}

func TestFoldAllPersistsEveryRepo(t *testing.T) {
	m := newLoaded(t, 120, 40)
	var repos int
	for _, r := range m.rows {
		if r.isRepo() {
			repos++
		}
	}
	m = press(m, 'z')

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.CollapsedRepos) != repos {
		t.Errorf("saved %d folded repos, want all %d", len(saved.CollapsedRepos), repos)
	}
	next := reopen(t, m)
	for _, r := range next.rows {
		if !r.isRepo() {
			t.Fatal("everything should still be folded after a restart")
		}
	}
}

// --- hiding --------------------------------------------------------------

func TestHidingAPullRequestRemovesItAndPersists(t *testing.T) {
	m := newLoaded(t, 120, 40)
	before := len(m.visiblePRs())
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')

	if got := len(m.visiblePRs()); got != before-1 {
		t.Errorf("visible PRs = %d, want %d", got, before-1)
	}
	if hasVisibleRow(m, victim) {
		t.Error("a hidden PR should not be listed")
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.PRHidden(victim) {
		t.Errorf("HiddenPRs = %v, want it to include %s", saved.HiddenPRs, victim)
	}
	if next := reopen(t, m); hasVisibleRow(next, victim) {
		t.Error("the PR came back after a restart")
	}
}

func TestHidingARepoRemovesAllOfItsPullRequests(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, 'h')

	for _, p := range m.visiblePRs() {
		if p.Repo == hideRepo {
			t.Errorf("%s survived hiding its repo", p.Key())
		}
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !saved.RepoHidden(hideRepo) {
		t.Errorf("HiddenRepos = %v", saved.HiddenRepos)
	}
	next := reopen(t, m)
	for _, p := range next.visiblePRs() {
		if p.Repo == hideRepo {
			t.Error("the repo came back after a restart")
		}
	}
}

func TestPeekingShowsHiddenItemsMarkedAsSuch(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 140, 40)
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')
	if hasVisibleRow(m, victim) {
		t.Fatal("PR should be concealed first")
	}

	m = press(m, 'H')
	if !m.showHidden {
		t.Fatal("H should reveal hidden items")
	}
	if !hasVisibleRow(m, victim) {
		t.Fatal("peeking should list the hidden PR")
	}

	out := ansi.Strip(m.View())
	if !strings.Contains(out, "HIDDEN") {
		t.Error("a revealed item should be marked HIDDEN")
	}
	if !strings.Contains(out, "hidden (shown)") {
		t.Errorf("the header should say hidden items are on show: %q", firstLines(out, 1))
	}

	m = press(m, 'H')
	if m.showHidden || hasVisibleRow(m, victim) {
		t.Error("H again should conceal them")
	}
}

func TestUnhidingWhilePeeking(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')
	m = press(m, 'H') // peek
	m = cursorToPR(t, m, victim)
	m = press(m, 'h') // and restore it

	if m.hiddenPRs[victim] {
		t.Fatal("h on a revealed item should unhide it")
	}
	m = press(m, 'H') // stop peeking
	if !hasVisibleRow(m, victim) {
		t.Error("the restored PR should be listed normally again")
	}
	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.PRHidden(victim) {
		t.Errorf("unhide was not persisted: %v", saved.HiddenPRs)
	}
}

func TestUnhidingAPullRequestHiddenByItsRepo(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, 'h') // hide the whole repo
	m = press(m, 'H') // peek

	// Pressing h on one of its PRs should do the obvious thing: bring the
	// repository back, rather than silently doing nothing.
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')

	if m.hiddenRepos[hideRepo] {
		t.Error("h on a repo-hidden PR should unhide the repo")
	}
	m = press(m, 'H')
	if !hasVisibleRow(m, victim) {
		t.Error("the PR should be back in the normal list")
	}
}

func TestUnhidingARepoFromItsHeader(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, 'h')
	m = press(m, 'H')
	m = cursorToRepo(t, m, hideRepo)
	m = press(m, 'h')

	if m.hiddenRepos[hideRepo] {
		t.Error("h on a revealed repo header should unhide it")
	}
}

// TestPeekingIsNotPersisted keeps "hidden" meaning hidden on a fresh start.
func TestPeekingIsNotPersisted(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')
	m = press(m, 'H')

	next := reopen(t, m)
	if next.showHidden {
		t.Error("a restart should not resume peeking at hidden items")
	}
	if hasVisibleRow(next, victim) {
		t.Error("the hidden PR should be concealed again after a restart")
	}
}

func TestHiddenPullRequestsAreExcludedFromTheAttentionCount(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m = cursorToPR(t, m, victim) // a FAILING PR
	before := ansi.Strip(m.View())
	m = press(m, 'h')
	after := ansi.Strip(m.View())

	if before == after {
		t.Fatal("header did not change after hiding a PR needing work")
	}
	if !strings.Contains(after, "1 hidden") {
		t.Errorf("header should count the hidden PR: %q", firstLines(after, 1))
	}
}

func TestHidingSurvivesARefresh(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})
	if hasVisibleRow(m, victim) {
		t.Error("a refresh should not resurrect a hidden PR")
	}
	// And it is still tracked, so hiding is not lost when the data reloads.
	if !m.hiddenPRs[victim] {
		t.Error("hidden set was cleared by a refresh")
	}
}

func TestHideOnAnEmptyListIsHarmless(t *testing.T) {
	isolateConfig(t)
	m := New(Config{Client: gh.NewClient("t"), Mode: gh.ModeAuthored, Interval: time.Minute, Max: 10, Prefs: config.Defaults()})
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 24})
	m = press(m, 'h')
	m = press(m, 'H')
	if len(strings.Split(m.View(), "\n")) > 24 {
		t.Error("frame broke with no rows")
	}
}

func hasVisibleRow(m Model, key string) bool {
	for _, r := range m.rows {
		if !r.isRepo() && r.pr.Key() == key {
			return true
		}
	}
	return false
}

// TestOrgFilterIsNotCountedAsHidden reproduces a header reading
// "7 open · 4 hidden" when nothing had been hidden with h at all: the four were
// filtered by the organization picker, which H does not reveal.
func TestOrgFilterIsNotCountedAsHidden(t *testing.T) {
	isolateConfig(t)
	// Exactly the user's config: one hidden org, nothing dismissed with h.
	prefs := config.Defaults()
	prefs.HiddenOrgs = []string{"octo-dev"}

	m := New(Config{Client: gh.NewClient("t"), Mode: gh.ModeAuthored, Interval: 30 * time.Second, Max: 200, Prefs: prefs})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})

	if got := m.hiddenCount(); got != 0 {
		t.Errorf("hiddenCount = %d, want 0: nothing was dismissed with h", got)
	}
	if got := m.orgHiddenCount(); got == 0 {
		t.Fatal("expected the org filter to be holding PRs back")
	}

	header := firstLines(ansi.Strip(m.View()), 1)
	if strings.Contains(header, "in hidden orgs") == false {
		t.Errorf("header should attribute them to the org filter: %q", header)
	}
	// The bare word must not appear as its own count, since H cannot reveal them.
	if strings.Contains(header, fmt.Sprintf("%d hidden ·", m.orgHiddenCount())) ||
		strings.HasSuffix(strings.TrimSpace(header), fmt.Sprintf("%d hidden", m.orgHiddenCount())) {
		t.Errorf("org-filtered PRs are still reported as plain 'hidden': %q", header)
	}

	// And pressing H must genuinely change nothing.
	before := len(m.visiblePRs())
	m = press(m, 'H')
	if got := len(m.visiblePRs()); got != before {
		t.Errorf("H revealed %d extra PRs, but none were dismissed with h", got-before)
	}
	if strings.Contains(ansi.Strip(m.View()), "HIDDEN") {
		t.Error("nothing should be marked HIDDEN")
	}
}

// TestBothFiltersAreReportedSeparately checks the two counts stay distinct.
func TestBothFiltersAreReportedSeparately(t *testing.T) {
	isolateConfig(t)
	prefs := config.Defaults()
	prefs.HiddenOrgs = []string{"octo-dev"}

	m := New(Config{Client: gh.NewClient("t"), Mode: gh.ModeAuthored, Interval: 30 * time.Second, Max: 200, Prefs: prefs})
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})

	byOrg := m.orgHiddenCount()
	m = cursorToPR(t, m, victim)
	m = press(m, 'h')

	if got := m.hiddenCount(); got != 1 {
		t.Errorf("hiddenCount = %d, want 1", got)
	}
	if got := m.orgHiddenCount(); got != byOrg {
		t.Errorf("orgHiddenCount changed to %d, want %d", got, byOrg)
	}

	header := firstLines(ansi.Strip(m.View()), 1)
	if !strings.Contains(header, fmt.Sprintf("%d in hidden orgs", byOrg)) {
		t.Errorf("header missing the org count: %q", header)
	}
	if !strings.Contains(header, "1 hidden") {
		t.Errorf("header missing the dismissed count: %q", header)
	}
	// The two must not be double counted in the open total.
	want := len(m.prs) - byOrg - 1
	if !strings.Contains(header, fmt.Sprintf("%d open", want)) {
		t.Errorf("header should show %d open: %q", want, header)
	}

	// H reveals only the one dismissed with h.
	before := len(m.visiblePRs())
	m = press(m, 'H')
	if got := len(m.visiblePRs()); got != before+1 {
		t.Errorf("H revealed %d PRs, want exactly the 1 dismissed with h", got-before)
	}
}
