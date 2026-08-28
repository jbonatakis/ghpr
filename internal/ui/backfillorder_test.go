package ui

import (
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// windowPR is a pull request whose activity sits inside one backfill window.
func windowPR(n int, at time.Time) gh.PR {
	return gh.PR{
		Repo: "acme/hyperspace", Number: n, Title: "t",
		URL:       "https://example.invalid/" + itoa(n),
		Author:    "jbonatakis",
		CreatedAt: at, UpdatedAt: at, Mergeable: "MERGEABLE",
		RecentComments: []gh.Comment{{By: "brianmuse", At: at.Add(time.Minute)}},
	}
}

// deliver hands the model one search's answer for a window.
func deliver(m Model, window int, since time.Time, prs ...gh.PR) Model {
	return m.applyBackfillChunk(backfillChunkMsg{
		prs: prs, viewer: "jbonatakis", since: since, window: window,
	})
}

// topOfFeed is what the pane is showing at the very top — the newest line.
func topOfFeed(m Model) string {
	ev := m.feedEvents()
	if len(ev) == 0 {
		return ""
	}
	return ev[len(ev)-1].Text
}

// The pool answers out of order, so a later window routinely lands before an
// earlier one. Filing them as they arrive drops newer activity in above what
// is already on screen and the feed jumps; windows are released in order so it
// only ever grows downwards.
func TestOutOfOrderWindowsDoNotJumpTheFeed(t *testing.T) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	m := newSeeding(t, 24*time.Hour)

	// Window 2 (oldest of the three used here) comes back first.
	m = deliver(m, 2, since, windowPR(1, now.Add(-20*time.Hour)))
	m = deliver(m, 2, since)
	if got := len(m.events); got != 0 {
		t.Fatalf("an out-of-order window was filed early: %d events", got)
	}

	// Then window 1.
	m = deliver(m, 1, since, windowPR(2, now.Add(-14*time.Hour)))
	m = deliver(m, 1, since)
	if got := len(m.events); got != 0 {
		t.Fatalf("window 1 was filed while window 0 was outstanding: %d events", got)
	}

	// Only when the newest window lands does anything appear — and everything
	// held behind it goes in at once, underneath.
	m = deliver(m, 0, since, windowPR(3, now.Add(-2*time.Hour)))
	m = deliver(m, 0, since)

	top := topOfFeed(m)
	if top != "ghpr started" {
		t.Fatalf("the feed opens with %q, want the session marker", top)
	}
	for i := 1; i < len(m.events); i++ {
		if m.events[i].At.Before(m.events[i-1].At) {
			t.Errorf("the feed is out of order at %d", i)
		}
	}
	if len(m.events) < 6 {
		t.Errorf("only %d events were filed; some window was dropped", len(m.events))
	}
}

// The top line is the thing a reader is looking at, and once the newest window
// has landed nothing that arrives later may displace it.
func TestTheTopOfTheFeedSettlesWithTheFirstWindow(t *testing.T) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	m := newSeeding(t, 24*time.Hour)

	m = deliver(m, 0, since, windowPR(10, now.Add(-90*time.Minute)))
	m = deliver(m, 0, since)

	settled := topOfFeed(m)
	if settled == "" {
		t.Fatal("the newest window filed nothing")
	}
	newest := m.events[len(m.events)-1].At

	// Every later window, in whatever order the pool finishes them.
	for _, w := range []int{3, 1, 2} {
		m = deliver(m, w, since, windowPR(20+w, now.Add(-time.Duration(4+w*4)*time.Hour)))
		m = deliver(m, w, since)

		if got := topOfFeed(m); got != settled {
			t.Errorf("window %d moved the top of the feed from %q to %q", w, settled, got)
		}
		if got := m.events[len(m.events)-1].At; !got.Equal(newest) {
			t.Errorf("window %d put something newer at the top", w)
		}
	}
}

// A window whose searches both fail never completes. Everything behind it must
// still be released rather than gathered and silently dropped.
func TestAFailedWindowDoesNotStrandTheOnesBehindIt(t *testing.T) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	m := newSeeding(t, 24*time.Hour)

	// Window 0 fails outright.
	fail := backfillChunkMsg{since: since, window: 0, err: &gh.TransientError{Detail: "502"}}
	m = m.applyBackfillChunk(fail)
	m = m.applyBackfillChunk(fail)

	// Window 1 answers normally and, with window 0 accounted for, goes in.
	m = deliver(m, 1, since, windowPR(5, now.Add(-8*time.Hour)))
	m = deliver(m, 1, since)

	if len(m.events) == 0 {
		t.Fatal("a failed window stranded the ones behind it")
	}
	if !m.seedFailed {
		t.Error("the failure went unrecorded")
	}
}

// If the searches stop early — the backlog filled, or something failed — what
// was already gathered but not yet released must still be filed.
func TestWhatIsHeldAtTheEndIsStillFiled(t *testing.T) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	m := newSeeding(t, 24*time.Hour)

	// Window 1 answers in full; window 0 never does, so nothing is released.
	m = deliver(m, 1, since, windowPR(7, now.Add(-9*time.Hour)))
	m = deliver(m, 1, since)
	if len(m.events) != 0 {
		t.Fatal("window 1 was released while window 0 was outstanding")
	}

	m = m.applyBackfill(backfillDoneMsg{})
	if len(m.events) == 0 {
		t.Error("what was held when the searches stopped was thrown away")
	}
	if m.backfilling {
		t.Error("the backfill is still marked as running")
	}
}
