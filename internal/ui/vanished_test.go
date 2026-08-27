package ui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// statesClient answers verification look-ups from a fixed id -> state table.
func statesClient(t *testing.T, table map[string]string) *gh.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		type node struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		var nodes []node
		for _, id := range body.Variables.IDs {
			nodes = append(nodes, node{ID: id, State: table[id]})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"nodes": nodes}})
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	return c
}

func updateCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return next.(Model), cmd
}

// dropPR removes one PR from a result, simulating it falling off a search page.
func dropPR(res gh.Result, key string) (gh.Result, gh.PR) {
	var removed gh.PR
	kept := make([]gh.PR, 0, len(res.PRs))
	for _, p := range res.PRs {
		if p.Key() == key {
			removed = p
			continue
		}
		kept = append(kept, p)
	}
	res.PRs = kept
	return res, removed
}

func closureEvents(m Model) []gh.Event {
	var out []gh.Event
	for _, e := range m.events {
		if e.Kind == gh.EventClosed || e.Kind == gh.EventMerged {
			out = append(out, e)
		}
	}
	return out
}

const victim = "acme/starfield#96"

// TestPageBoundaryFlapProducesNoEvents is the regression test for a pull
// request that sat on a search page boundary being announced as "merged or
// closed" and then "opened" again a poll later, while it was open the whole
// time.
func TestPageBoundaryFlapProducesNoEvents(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "OPEN"})

	// Poll where the PR has slipped off the page.
	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})

	if got := closureEvents(m); len(got) != 0 {
		t.Errorf("absence alone raised %v", got)
	}
	if !hasPR(m, victim) {
		t.Error("the PR should stay on screen while its fate is unknown")
	}
	if cmd == nil {
		t.Fatal("expected a verification look-up")
	}

	// GitHub confirms it is still open.
	m = update(m, cmd())

	if got := closureEvents(m); len(got) != 0 {
		t.Errorf("a still-open PR raised %v", got)
	}
	if !hasPR(m, victim) {
		t.Error("a still-open PR must not be dropped")
	}

	// And when it reappears in the next poll, it is not "opened" either.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})
	for _, e := range m.events {
		if e.Key == victim {
			t.Errorf("the flap produced an event after all: %s %s", e.Key, e.Text)
		}
	}
	if out := ansi.Strip(m.View()); strings.Contains(out, "merged or closed") {
		t.Error("the old guessed wording is still reachable")
	}
}

func TestMergedPullRequestIsReportedAccurately(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "MERGED"})

	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	m = update(m, cmd())

	events := closureEvents(m)
	if len(events) != 1 {
		t.Fatalf("closure events = %v, want one", events)
	}
	if events[0].Kind != gh.EventMerged || events[0].Text != "merged" {
		t.Errorf("event = %+v, want a merged event", events[0])
	}
	if hasPR(m, victim) {
		t.Error("a merged PR should leave the list")
	}
}

func TestClosedPullRequestIsReportedAccurately(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "CLOSED"})

	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	m = update(m, cmd())

	events := closureEvents(m)
	if len(events) != 1 || events[0].Kind != gh.EventClosed || events[0].Text != "closed" {
		t.Fatalf("closure events = %v, want one closed event", events)
	}
	if hasPR(m, victim) {
		t.Error("a closed PR should leave the list")
	}
}

func TestIncompleteFetchNeverVerifiesOrReports(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m.cfg.Client = statesClient(t, map[string]string{})

	// Complete=false: the PR may simply be beyond the cut-off.
	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = false
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})

	if cmd != nil {
		t.Error("a truncated search should not trigger a look-up")
	}
	if !hasPR(m, victim) {
		t.Error("the PR should be kept when the search was cut short")
	}
	if got := closureEvents(m); len(got) != 0 {
		t.Errorf("truncation raised %v", got)
	}
}

func TestFailedVerificationLeavesThePullRequestAlone(t *testing.T) {
	m := newLoaded(t, 120, 40)
	missing, target := dropPR(loadFixture(t), victim)
	missing.Complete = true
	m, _ = updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})

	m = update(m, verifyDoneMsg{seq: m.fetchSeq, err: context.DeadlineExceeded, checked: []gh.PR{target}})

	if got := closureEvents(m); len(got) != 0 {
		t.Errorf("a failed look-up must not conclude anything, got %v", got)
	}
	if !hasPR(m, victim) {
		t.Error("the PR should remain listed after a failed look-up")
	}
}

func TestVerificationOnlyAsksOncePerDisappearance(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "OPEN"})

	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true

	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	if cmd == nil {
		t.Fatal("first disappearance should trigger a look-up")
	}
	// Still missing on the next poll, but already pending: no second request.
	m, cmd2 := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	if cmd2 != nil {
		t.Error("a pending look-up should not be duplicated every poll")
	}
	_ = m
}

func byPRKey(t *testing.T, m Model, key string) gh.PR {
	t.Helper()
	for _, p := range m.prs {
		if p.Key() == key {
			return p
		}
	}
	t.Fatalf("fixture has no %s", key)
	return gh.PR{}
}

func hasPR(m Model, key string) bool {
	for _, p := range m.prs {
		if p.Key() == key {
			return true
		}
	}
	return false
}

// TestPersistentlyAbsentOpenPullRequestIsRetired covers a pull request that is
// still open but no longer matches the search — a withdrawn review request,
// say. It must not be carried and re-queried forever, nor reported as closed.
func TestPersistentlyAbsentOpenPullRequestIsRetired(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "OPEN"})

	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true

	// It is held and asked about on the first absence.
	m, cmd := updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	if cmd == nil {
		t.Fatal("expected a look-up on first absence")
	}
	m = update(m, cmd())
	if !hasPR(m, victim) {
		t.Fatal("a still-open PR should be held, not dropped immediately")
	}

	// Kept for a few polls, then retired quietly.
	for i := 0; i < maxAbsentPolls+1; i++ {
		m, _ = updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	}
	if hasPR(m, victim) {
		t.Errorf("still carried after %d absent polls", maxAbsentPolls+1)
	}
	if got := closureEvents(m); len(got) != 0 {
		t.Errorf("retiring an open PR must not claim it finished, got %v", got)
	}
	if len(m.absent) != 0 {
		t.Errorf("absence bookkeeping leaked: %v", m.absent)
	}
}

func TestCarriedPullRequestsDoNotAccumulate(t *testing.T) {
	m := newLoaded(t, 120, 40)
	m.cfg.Client = statesClient(t, map[string]string{})
	full := loadFixture(t)

	missing, _ := dropPR(full, victim)
	missing.Complete = true
	for i := 0; i < 10; i++ {
		m, _ = updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	}
	if len(m.prs) > len(full.PRs) {
		t.Errorf("tracking %d PRs after repeated polls, fixture has %d", len(m.prs), len(full.PRs))
	}
}

func TestReturningPullRequestClearsItsAbsence(t *testing.T) {
	m := newLoaded(t, 120, 40)
	target := byPRKey(t, m, victim)
	m.cfg.Client = statesClient(t, map[string]string{target.ID: "OPEN"})

	missing, _ := dropPR(loadFixture(t), victim)
	missing.Complete = true
	m, _ = updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: missing})
	if len(m.absent) == 0 {
		t.Fatal("absence not recorded")
	}

	m, _ = updateCmd(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixture(t)})
	if len(m.absent) != 0 {
		t.Errorf("a returning PR should clear its absence, got %v", m.absent)
	}
	if !hasPR(m, victim) {
		t.Error("the PR should be listed normally again")
	}
}
