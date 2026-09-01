package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// theirPR is somebody else's pull request — the kind you review rather than
// write, which is exactly what an authored-mode poll never looks at.
func theirPR(number int, headOID string, comments int) gh.PR {
	return gh.PR{
		Repo: "robinpowered/robin-ai-agents", Number: number, Title: "t",
		URL: "https://example.invalid/" + itoa(number), Author: "mike-guerrette",
		CreatedAt: time.Now().Add(-2 * time.Hour), UpdatedAt: time.Now(),
		Mergeable: "MERGEABLE", HeadOID: headOID, PushedBy: "mike-guerrette",
		IssueComments: comments,
	}
}

func feedPoll(m Model, prs ...gh.PR) Model {
	next, _ := m.applyFeedPoll(feedPollDoneMsg{
		seq: m.fetchSeq, prs: prs, viewer: "jbonatakis", at: time.Now(),
	})
	return next
}

// The reported bug. Watching somebody push to a pull request you review
// produced nothing at all while ghpr ran, and the commits turned up only after
// closing and reopening it — because the poll asks author:@me and the backfill
// asks three wider questions, and only one of those runs while the dashboard
// is up.
func TestAPushToAPullRequestYouReviewIsReportedWhileRunning(t *testing.T) {
	m := newLoaded(t, 140, 40) // authored mode; the list holds none of these

	// First sweep sees it as it stands.
	m = feedPoll(m, theirPR(747, "aaa", 1))
	before := len(m.events)

	// Mike pushes.
	m = feedPoll(m, theirPR(747, "bbb", 1))

	if len(m.events) == before {
		t.Fatal("a push to a pull request outside the list produced no event")
	}
	var pushed bool
	for _, e := range m.events[before:] {
		if e.Kind == gh.EventPush {
			pushed = true
			if e.Key != "robinpowered/robin-ai-agents#747" {
				t.Errorf("the event names %q", e.Key)
			}
		}
	}
	if !pushed {
		t.Errorf("no push event; got %+v", m.events[before:])
	}
}

// Comments on somebody else's pull request are the same story.
func TestACommentOnSomebodyElsesPullRequestIsReported(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m = feedPoll(m, theirPR(747, "aaa", 1))
	before := len(m.events)

	m = feedPoll(m, theirPR(747, "aaa", 3))

	var commented bool
	for _, e := range m.events[before:] {
		if e.Kind == gh.EventComment {
			commented = true
		}
	}
	if !commented {
		t.Errorf("a comment outside the list produced no event; got %+v", m.events[before:])
	}
}

// The dashboard's own poll already diffs what is on screen. Reporting the same
// change from both sweeps would put one comment in the feed twice.
func TestTheSweepLeavesWhatIsOnScreenToTheListPoll(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, 0) // no backfill; just the two polls

	// A pull request the list is holding.
	mine := history(now).PRs[0]
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	before := len(m.events)

	// The sweep sees the very same pull request change. The list poll will
	// report it; the sweep must not.
	changed := mine
	changed.IssueComments += 2
	m = feedPoll(m, changed)

	if len(m.events) != before {
		t.Errorf("the sweep reported %d events for a pull request the list already covers",
			len(m.events)-before)
	}
}

// A sweep bounded by updated:>= returns only what changed, so a pull request
// seen for the first time has not necessarily just arrived — and an old one
// must not be announced as though it had.
func TestAFirstSightingIsNotAnArrival(t *testing.T) {
	m := newLoaded(t, 140, 40)

	old := theirPR(700, "aaa", 4)
	old.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
	m = feedPoll(m, old)

	for _, e := range m.events {
		if e.Kind == gh.EventOpened || e.Kind == gh.EventArrived {
			t.Errorf("a month-old pull request was announced as %q on first sight", e.Text)
		}
	}
}

// One that really is new should say so, which is the other half of the same
// rule.
func TestAGenuinelyNewPullRequestIsReportedAsOpened(t *testing.T) {
	m := newLoaded(t, 140, 40)
	// The last sweep was ten minutes ago, so a pull request opened five minutes
	// ago appeared inside this one's window — nobody else has reported it.
	m.lastFeedPoll = time.Now().Add(-10 * time.Minute)

	fresh := theirPR(747, "aaa", 0)
	fresh.CreatedAt = time.Now().Add(-5 * time.Minute)
	m = feedPoll(m, fresh)

	var opened bool
	for _, e := range m.events {
		if e.Kind == gh.EventOpened {
			opened = true
		}
	}
	if !opened {
		t.Errorf("a pull request opened five minutes ago was not reported; got %+v", m.events)
	}
}

// A failed sweep is not worth interrupting anyone over, and must not move the
// bound — the next one has to cover the same ground.
func TestAFailedSweepKeepsItsPlace(t *testing.T) {
	m := newLoaded(t, 140, 40)
	was := m.lastFeedPoll

	next, _ := m.applyFeedPoll(feedPollDoneMsg{
		seq: m.fetchSeq, err: &gh.TransientError{Detail: "502"},
	})
	if !next.lastFeedPoll.Equal(was) {
		t.Error("a failed sweep moved the bound, so its ground is never covered again")
	}
	if next.err != nil {
		t.Errorf("a failed sweep took the dashboard down: %v", next.err)
	}
	if len(next.events) != 0 {
		t.Errorf("a failed sweep produced %d events", len(next.events))
	}
}

// An answer to a superseded search describes a moment that has moved on.
func TestASupersededSweepIsDiscarded(t *testing.T) {
	m := newLoaded(t, 140, 40)

	next, _ := m.applyFeedPoll(feedPollDoneMsg{
		seq: m.fetchSeq - 1, prs: []gh.PR{theirPR(747, "aaa", 1)}, at: time.Now(),
	})
	if len(next.feedSeen) != 0 {
		t.Error("a superseded sweep was filed anyway")
	}
}

// The sweep asks the same questions the backfill does, and is bounded so that
// asking them every interval stays affordable.
func TestTheSweepSearchesTheFeedsScopeNotTheModes(t *testing.T) {
	since := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	got := gh.FeedPollSearches("", since, gh.AllShapes)

	if len(got) != len(gh.AllShapes) {
		t.Fatalf("%d searches for %d shapes", len(got), len(gh.AllShapes))
	}
	joined := strings.Join(got, " || ")
	for _, want := range []string{"involves:@me", "review-requested:@me", "reviewed-by:@me"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the sweep never asks %s: %q", want, got)
		}
	}
	if strings.Contains(joined, "author:@me") {
		t.Errorf("the sweep inherited the dashboard's mode: %q", got)
	}
	for _, q := range got {
		if !strings.Contains(q, "updated:>=2026-09-01T10:00:00+00:00") {
			t.Errorf("unbounded sweep would refetch everything every interval: %q", q)
		}
		// Emphatically not is:open. A merge is the one change that removes a
		// pull request from an open-only search, so asking that way guarantees
		// the sweep can never see the thing most worth reporting.
		if strings.Contains(q, "is:open") {
			t.Errorf("an open-only sweep can never watch a merge happen: %q", q)
		}
	}
}

// merged returns the same pull request after it has been merged.
func merged(p gh.PR, by string, at time.Time) gh.PR {
	p.State, p.FinishedAt, p.FinishedBy = gh.StateMerged, at, by
	p.UpdatedAt = at
	return p
}

// The reported miss. A merge is the one change that takes a pull request out of
// an open-only search, so a sweep asking is:open watched it disappear and said
// nothing at all.
func TestTheSweepReportsAMerge(t *testing.T) {
	m := newLoaded(t, 140, 40)

	open := theirPR(747, "aaa", 2)
	m = feedPoll(m, open)
	before := len(m.events)

	m = feedPoll(m, merged(open, "mike-guerrette", time.Now()))

	var got gh.Event
	var found bool
	for _, e := range m.events[before:] {
		if e.Kind == gh.EventMerged {
			got, found = e, true
		}
	}
	if !found {
		t.Fatalf("the merge produced no event; got %+v", m.events[before:])
	}
	if got.Text != "merged" {
		t.Errorf("the event reads %q", got.Text)
	}
	if got.Actor != "mike-guerrette" {
		t.Errorf("the merge is attributed to %q", got.Actor)
	}
	if got.Key != "robinpowered/robin-ai-agents#747" {
		t.Errorf("the event names %q", got.Key)
	}
}

func TestTheSweepReportsAClose(t *testing.T) {
	m := newLoaded(t, 140, 40)

	open := theirPR(748, "aaa", 1)
	m = feedPoll(m, open)
	before := len(m.events)

	closed := open
	closed.State, closed.FinishedAt, closed.FinishedBy = gh.StateClosed, time.Now(), "dana-quill"
	m = feedPoll(m, closed)

	var found bool
	for _, e := range m.events[before:] {
		if e.Kind == gh.EventClosed {
			found = true
		}
	}
	if !found {
		t.Errorf("the close produced no event; got %+v", m.events[before:])
	}
}

// Once it has finished it stays finished, and every later sweep must not say so
// again.
func TestAMergeIsReportedOnlyOnce(t *testing.T) {
	m := newLoaded(t, 140, 40)

	open := theirPR(747, "aaa", 2)
	m = feedPoll(m, open)
	done := merged(open, "mike-guerrette", time.Now())
	m = feedPoll(m, done)
	after := len(m.events)

	m = feedPoll(m, done)
	m = feedPoll(m, done)

	if len(m.events) != after {
		t.Errorf("later sweeps reported the merge %d more times", len(m.events)-after)
	}
}

// A pull request merged before the sweep ever saw it open is still news, so
// long as it finished inside the window this sweep was asked for.
func TestAMergeIsReportedEvenOnAFirstSighting(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.lastFeedPoll = time.Now().Add(-10 * time.Minute)

	old := theirPR(700, "aaa", 4)
	old.CreatedAt = time.Now().Add(-30 * 24 * time.Hour)
	m = feedPoll(m, merged(old, "mike-guerrette", time.Now().Add(-2*time.Minute)))

	var found bool
	for _, e := range m.events {
		if e.Kind == gh.EventMerged {
			found = true
		}
	}
	if !found {
		t.Errorf("a merge on a pull request never seen open went unreported; got %+v", m.events)
	}
}

// The dashboard's own poll is is:open, so nothing it returns ever carries a
// finished state — and it must not start inventing merges from that.
func TestTheListPollStillReportsNoMerges(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, 0)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	before := len(m.events)

	next := history(now)
	next.PRs[0].IssueComments = 9
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})

	for _, e := range m.events[before:] {
		if e.Kind == gh.EventMerged || e.Kind == gh.EventClosed {
			t.Errorf("the list poll invented %q from an open-only search", e.Text)
		}
	}
}
