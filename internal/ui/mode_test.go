package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// reviewRequestedResult stands in for a slow review-requested search.
func reviewRequestedResult(t *testing.T) gh.Result {
	t.Helper()
	res := loadFixtureFile(t, "search_review_requested.json")
	res.Complete = true
	return res
}

func authoredResult(t *testing.T) gh.Result {
	t.Helper()
	res := loadFixture(t)
	res.Complete = true
	return res
}

func repoSet(m Model) map[string]bool {
	out := map[string]bool{}
	for _, p := range m.prs {
		out[p.Repo] = true
	}
	return out
}

// TestSlowResponseFromAPreviousModeIsDiscarded is the regression test for the
// list filling with review-requested pull requests while the header said
// "authored": that search is slow and paginated, so its answer could arrive
// after the faster authored one and overwrite it.
func TestSlowResponseFromAPreviousModeIsDiscarded(t *testing.T) {
	m := newUnloaded(t, 120, 40)

	// Poll for review-requested, then switch away before it answers.
	inFlight := m.fetchSeq
	m = press(m, 'm')
	if m.cfg.Mode != gh.ModeReviewRequested {
		t.Fatalf("mode = %v", m.cfg.Mode)
	}
	m = press(m, 'm')
	m = press(m, 'm')
	if m.cfg.Mode != gh.ModeAuthored {
		t.Fatalf("cycled back to %v, want authored", m.cfg.Mode)
	}

	// The authored answer lands first.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: authoredResult(t)})
	authored := repoSet(m)
	if len(authored) == 0 {
		t.Fatal("authored results were not applied")
	}

	// Now the stale review-requested answer finally arrives.
	m = update(m, fetchDoneMsg{seq: inFlight, res: reviewRequestedResult(t)})

	if got := repoSet(m); len(got) != len(authored) {
		t.Errorf("stale response changed the list: %d repos, want %d", len(got), len(authored))
	}
	for _, p := range m.prs {
		if p.Author != "octo-dev" {
			t.Errorf("%s by %s leaked in from review-requested mode", p.Key(), p.Author)
		}
	}
	if !strings.Contains(ansi.Strip(m.View()), "authored") {
		t.Error("header should still say authored")
	}
}

func TestStaleResponseDoesNotClearTheLoadingState(t *testing.T) {
	m := newUnloaded(t, 120, 40)
	stale := m.fetchSeq
	m = press(m, 'm') // supersedes the first request

	if !m.loading {
		t.Fatal("a mode switch should start loading")
	}
	m = update(m, fetchDoneMsg{seq: stale, res: authoredResult(t)})

	if !m.loading {
		t.Error("a superseded answer must not report the current fetch as done")
	}
	if m.loaded {
		t.Error("a superseded answer must not populate the list")
	}
}

func TestStaleErrorDoesNotRaiseAWarning(t *testing.T) {
	m := newLoaded(t, 120, 40)
	stale := m.fetchSeq
	m = press(m, 'm')

	m = update(m, fetchDoneMsg{seq: stale, err: transient502()})
	if m.warn != "" || m.err != nil || m.failures != 0 {
		t.Errorf("a superseded failure should be ignored: warn=%q err=%v failures=%d",
			m.warn, m.err, m.failures)
	}
}

func TestModeSwitchClearsThePreviousList(t *testing.T) {
	m := newLoaded(t, 120, 40)
	if len(m.prs) == 0 {
		t.Fatal("expected loaded data")
	}
	m = press(m, 'm')

	if len(m.prs) != 0 || len(m.rows) != 0 {
		t.Error("switching mode should clear the old list rather than show it under a new heading")
	}
	if !strings.Contains(ansi.Strip(m.View()), "loading") {
		t.Error("the list should say it is loading the new mode")
	}
}

func TestRefreshWhileLoadingDoesNotStartASecondFetch(t *testing.T) {
	m := newUnloaded(t, 120, 40)
	seq := m.fetchSeq

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next.(Model)
	if cmd != nil {
		t.Error("r should be ignored while a fetch is already in flight")
	}
	if m.fetchSeq != seq {
		t.Errorf("fetchSeq moved to %d, want %d", m.fetchSeq, seq)
	}
}

func TestEachNewFetchSupersedesTheLast(t *testing.T) {
	m := newLoaded(t, 120, 40)
	seen := map[int]bool{m.fetchSeq: true}

	for i := 0; i < 5; i++ {
		m = press(m, 'm')
		if seen[m.fetchSeq] {
			t.Fatalf("fetchSeq %d reused; a stale answer could be mistaken for the current one", m.fetchSeq)
		}
		seen[m.fetchSeq] = true
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: authoredResult(t)})
	}
}

func TestStaleVerificationIsDiscarded(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "MERGED"})

	missing, gone := dropPR(authoredResult(t), victim)
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	if cmd == nil {
		t.Fatal("expected a look-up")
	}
	stale := cmd()

	// The user switches mode before the look-up answers.
	m = press(m, 'm')
	m = update(m, stale)

	for _, e := range m.events {
		if e.Key == gone.Key() && (e.Kind == gh.EventMerged || e.Kind == gh.EventClosed) {
			t.Errorf("a look-up for the abandoned mode still reported %s as %s", e.Key, e.Text)
		}
	}
}
