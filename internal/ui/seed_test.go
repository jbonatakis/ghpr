package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// newSeeding returns a model that has not polled yet and will fill the feed in
// from the given window when the backfill lands.
func newSeeding(t *testing.T, window time.Duration) Model {
	t.Helper()
	isolateConfig(t)
	m := New(Config{
		Client: gh.NewClient("test"), Mode: gh.ModeAuthored,
		Interval: 30 * time.Second, Max: 200, Prefs: config.Defaults(),
		Links: true, Seed: window,
	})
	return update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
}

// backfill delivers a reconstruction the way the real one arrives: as its own
// message, off the back of its own pair of searches, not from a poll.
func backfill(m Model, res gh.Result, window time.Duration) Model {
	return update(m, backfillDoneMsg{
		events: gh.Seed(res.PRs, res.FetchedAt.Add(-window), res.Viewer),
	})
}

// history is a snapshot whose own timestamps describe the last half hour.
func history(now time.Time) gh.Result {
	back := func(d time.Duration) time.Time { return now.Add(-d) }
	return gh.Result{
		Viewer: "jack", FetchedAt: now, Complete: true,
		PRs: []gh.PR{{
			Repo: "acme/starfield", Number: 44, Title: "t",
			URL: "https://example.invalid/44", Author: "morgan-bell",
			CreatedAt: back(25 * time.Minute), UpdatedAt: back(4 * time.Minute),
			Mergeable: "MERGEABLE", HeadOID: "abc",
			PushedAt:    back(20 * time.Minute),
			ChecksState: gh.CheckSuccess, ChecksAt: back(12 * time.Minute),
			Reviewers: []gh.Reviewer{
				{Login: "dana-quill", State: "APPROVED", At: back(9 * time.Minute)},
			},
			RecentComments: []gh.Comment{{By: "riley-shaw", At: back(4 * time.Minute)}},
		}},
	}
}

// quiet is the same snapshot with everything on it long out of the window.
func quiet(now time.Time) gh.Result {
	res := history(now)
	res.PRs[0].CreatedAt = now.AddDate(0, 0, -30)
	res.PRs[0].PushedAt = now.AddDate(0, 0, -29)
	res.PRs[0].ChecksAt = now.AddDate(0, 0, -29)
	res.PRs[0].Reviewers = nil
	res.PRs[0].RecentComments = nil
	return res
}

func feedHas(m Model, want string) bool {
	for _, e := range m.events {
		if e.Text == want {
			return true
		}
	}
	return false
}

func TestTheFeedStartsFilledIn(t *testing.T) {
	now := time.Now()
	m := backfill(newSeeding(t, time.Hour), history(now), time.Hour)

	for _, want := range []string{"opened", "new commits", "checks passing", "approved", "new comment"} {
		if !feedHas(m, want) {
			t.Errorf("the filled-in feed is missing %q", want)
		}
	}
	if !feedHas(m, "ghpr started") {
		t.Error("the feed does not say where the reconstruction stops")
	}
}

// The marker is the boundary, so nothing reconstructed may sit above it.
func TestTheSessionMarkerIsTheNewestSeededLine(t *testing.T) {
	now := time.Now()
	m := backfill(newSeeding(t, time.Hour), history(now), time.Hour)

	last := m.events[len(m.events)-1]
	if last.Kind != gh.EventSessionStart {
		t.Fatalf("the feed ends with %q, want the session marker", last.Text)
	}
	for _, e := range m.events[:len(m.events)-1] {
		if e.At.After(last.At) {
			t.Errorf("%q is dated after the session started", e.Text)
		}
	}
}

func TestNoSeedWindowLeavesTheFeedEmpty(t *testing.T) {
	// newLoaded builds a Config with no Seed, which is what a caller that has
	// not asked for one gets — and Init never issues the backfill.
	if m := newLoaded(t, 140, 40); len(m.events) != 0 {
		t.Errorf("an unasked-for backfill filled the feed with %d events", len(m.events))
	}
}

// Polling reports differences. It never reconstructs, so no amount of it can
// add to the backfilled past — including across a mode switch, where the two
// searches overlap and re-seeding would repeat everything in both.
func TestPollingNeverAddsToTheBackfill(t *testing.T) {
	now := time.Now()
	m := backfill(newSeeding(t, time.Hour), history(now), time.Hour)
	first := len(m.events)

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if len(m.events) != first {
		t.Errorf("polling re-seeded the feed: %d events, was %d", len(m.events), first)
	}

	m = press(m, 'm')
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if len(m.events) != first {
		t.Errorf("switching mode re-seeded the feed: %d events, was %d", len(m.events), first)
	}
}

// Not knowing what happened before launch is a disappointment, not a failure:
// the dashboard's own job is unaffected, so it says so and carries on.
func TestAFailedBackfillIsNotFatal(t *testing.T) {
	m := newSeeding(t, time.Hour)
	m = update(m, backfillDoneMsg{err: &gh.TransientError{Detail: "upstream had a moment"}})

	if len(m.events) != 0 {
		t.Errorf("a failed backfill produced %d events", len(m.events))
	}
	if m.err != nil {
		t.Errorf("a failed backfill took the dashboard down: %v", m.err)
	}
	if !strings.Contains(m.toast, "could not fill the feed in") {
		t.Errorf("nothing told the user the backfill failed; toast is %q", m.toast)
	}
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(time.Now())})
	if !m.loaded {
		t.Error("the dashboard did not load after a failed backfill")
	}
}

// The gutter dot means "changed in the last minute". Reconstructed history is
// older than that and must not light every row up at launch.
func TestSeededHistoryDoesNotLightUpTheList(t *testing.T) {
	now := time.Now()
	m := backfill(newSeeding(t, time.Hour), history(now), time.Hour)
	m.now = now

	if m.isFresh("acme/starfield#44") {
		t.Error("a pull request last touched four minutes ago is marked as just changed")
	}
}

func TestRecentSeededActivityStillCountsAsFresh(t *testing.T) {
	now := time.Now()
	res := history(now)
	res.PRs[0].RecentComments = []gh.Comment{{By: "riley-shaw", At: now.Add(-10 * time.Second)}}

	m := backfill(newSeeding(t, time.Hour), res, time.Hour)
	m.now = now

	if !m.isFresh("acme/starfield#44") {
		t.Error("a comment from ten seconds ago should still be marked fresh")
	}
}

func TestTheSeededFeedRendersWithoutAPullRequestReference(t *testing.T) {
	m := backfill(newSeeding(t, time.Hour), history(time.Now()), time.Hour)
	m = press(m, 'e')

	out := feedText(m)
	if !strings.Contains(out, "ghpr started") {
		t.Fatalf("the marker is not on screen:\n%s", out)
	}
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, "ghpr started") && strings.Contains(ln, "#") {
			t.Errorf("the session marker claims a pull request: %q", ln)
		}
	}
}

// A feed filled in behind a closed pane is indistinguishable from one that was
// never filled in at all.
func TestASeededFeedOpensItself(t *testing.T) {
	m := newSeeding(t, time.Hour)
	if m.showEvents {
		t.Fatal("the pane should start closed")
	}

	m = backfill(m, history(time.Now()), time.Hour)
	if !m.showEvents {
		t.Error("a filled-in feed left itself hidden")
	}
	if m.eventsFocus {
		t.Error("it should show the feed, not steal the keys")
	}
	if !strings.Contains(m.toast, "e to scroll") {
		t.Errorf("nothing said how to get into it; toast is %q", m.toast)
	}
}

func TestAQuietWindowDoesNotOpenThePane(t *testing.T) {
	m := backfill(newSeeding(t, time.Hour), quiet(time.Now()), time.Hour)

	if len(m.events) != 0 {
		t.Fatalf("the window should have been quiet, got %d events", len(m.events))
	}
	if m.showEvents {
		t.Error("opening onto nothing is just a pane in the way")
	}
}

// "watching for changes" alone cannot tell you whether the backfill ran and
// found nothing or never ran at all.
func TestTheEmptyFeedSaysWhetherItLookedBack(t *testing.T) {
	m := backfill(newSeeding(t, time.Hour), quiet(time.Now()), time.Hour)
	m.showEvents = true
	if out := feedText(m); !strings.Contains(out, "nothing in the last 1h") {
		t.Errorf("the empty feed does not say it looked back:\n%s", out)
	}

	plain := newLoaded(t, 140, 40)
	plain.showEvents = true
	out := feedText(plain)
	if strings.Contains(out, "nothing in the last") {
		t.Errorf("a feed that was never seeded claims it looked back:\n%s", out)
	}
	if !strings.Contains(out, "watching for changes") {
		t.Errorf("the plain empty state changed:\n%s", out)
	}
}

// The whole point: a feed read top to bottom must run backwards in time, and
// look like it does.
func TestABackfilledFeedReadsInOrder(t *testing.T) {
	now := time.Now()
	res := history(now)
	// Spread across days, the case that made a screenshot look shuffled.
	res.PRs[0].CreatedAt = now.AddDate(0, 0, -20)
	res.PRs[0].PushedAt = now.AddDate(0, 0, -18)
	res.PRs[0].ChecksAt = now.AddDate(0, 0, -18)
	res.PRs[0].Reviewers = []gh.Reviewer{{Login: "dana-quill", State: "APPROVED", At: now.AddDate(0, 0, -9)}}
	res.PRs[0].RecentComments = []gh.Comment{
		{By: "github-actions", At: now.AddDate(0, 0, -14)},
		{By: "github-actions", At: now.AddDate(0, 0, -3)},
	}

	m := backfill(newSeeding(t, 720*time.Hour), res, 720*time.Hour)
	m.now = now

	for i := 1; i < len(m.events); i++ {
		if m.events[i].At.Before(m.events[i-1].At) {
			t.Fatalf("the feed is out of order at %d: %q then %q",
				i, m.events[i-1].Text, m.events[i].Text)
		}
	}

	for _, e := range m.events {
		stamp := strings.TrimSpace(eventTime(e.At, now))
		at, n := e.At.Local(), now.Local()
		sameDay := at.YearDay() == n.YearDay() && at.Year() == n.Year()
		if !sameDay && strings.Contains(stamp, ":") {
			t.Errorf("%q happened on another day but shows the clock time %q", e.Text, stamp)
		}
	}
}

// A backfill is slow, so it routinely lands after live events it predates.
// Appending would put a month-old line at the top of the feed.
func TestALateBackfillSortsUnderTheLiveEvents(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, 720*time.Hour)

	// A poll gets there first and reports something happening now.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	next := history(now)
	next.PRs[0].IssueComments = 5
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})
	if len(m.events) == 0 {
		t.Fatal("the poll reported nothing to race with")
	}

	old := history(now)
	old.PRs[0].CreatedAt = now.AddDate(0, 0, -20)
	old.PRs[0].RecentComments = []gh.Comment{{By: "github-actions", At: now.AddDate(0, 0, -14)}}
	m = backfill(m, old, 720*time.Hour)

	for i := 1; i < len(m.events); i++ {
		if m.events[i].At.Before(m.events[i-1].At) {
			t.Fatalf("a late backfill left the feed out of order at %d: %q then %q",
				i, m.events[i-1].Text, m.events[i].Text)
		}
	}
	if m.events[len(m.events)-1].At.Before(now.AddDate(0, 0, -1)) {
		t.Error("the newest line in the feed is a month old")
	}
}

// Reading back through the feed survives the backfill landing under the cursor.
func TestALateBackfillDoesNotMoveTheLineYouAreReading(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = backlog(40)
	m = press(press(m, 'e'), 'e')
	for i := 0; i < 6; i++ {
		m = press(m, 'j')
	}
	holding := m.selectedEvent().Text

	old := make([]gh.Event, 0, 5)
	for i := 0; i < 5; i++ {
		e := eventFor("acme/design-docs", i, "ancient")
		e.At = time.Now().Add(-time.Duration(30-i) * 24 * time.Hour)
		old = append(old, e)
	}
	m.record(old)

	if got := m.selectedEvent().Text; got != holding {
		t.Errorf("the cursor slid from %q to %q when the backfill landed", holding, got)
	}
}

func TestTidyDuration(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"1h", "1h"},
		{"2h", "2h"},
		{"30m", "30m"},
		{"1h30m", "1h30m"},
		{"90s", "1m30s"},
		{"0s", "0s"},
	} {
		d, err := time.ParseDuration(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := tidyDuration(d); got != tc.want {
			t.Errorf("tidyDuration(%s) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A backfilled feed spans days, and a bare clock time cannot say which one.
// Worse than useless: yesterday afternoon reads as newer than this morning,
// so a correctly ordered feed looks shuffled.
func TestEventTimeDistinguishesTodayFromEarlier(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 27, 36, 0, time.Local)

	for _, tc := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"this second", now, "15:27:36"},
		{"this morning", time.Date(2026, 8, 28, 9, 58, 5, 0, time.Local), "09:58:05"},
		{"just after midnight today", time.Date(2026, 8, 28, 0, 0, 1, 0, time.Local), "00:00:01"},
		{"yesterday afternoon", time.Date(2026, 8, 27, 16, 26, 11, 0, time.Local), "23h"},
		{"three days ago", now.AddDate(0, 0, -3), "3d"},
		{"three weeks ago", now.AddDate(0, 0, -21), "3w"},
		{"last year", now.AddDate(-1, 0, 0), "1y"},
	} {
		if got := strings.TrimSpace(eventTime(tc.at, now)); got != tc.want {
			t.Errorf("%s: eventTime = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Whatever it renders has to stay inside its column, or every line after it
// shifts.
func TestEventTimeKeepsItsColumn(t *testing.T) {
	now := time.Date(2026, 8, 28, 15, 27, 36, 0, time.Local)
	for _, at := range []time.Time{
		now,
		now.Add(-time.Hour),
		now.AddDate(0, 0, -1),
		now.AddDate(0, 0, -400),
		now.AddDate(-9, 0, 0),
	} {
		if got := len(eventTime(at, now)); got != evTimeWidth {
			t.Errorf("eventTime(%s) is %d cells, want %d", at, got, evTimeWidth)
		}
	}
}

// The seed window has no in-app control, so it is only ever read from the file.
// savePrefs rewrites that whole file on every toggle, and a field it forgot to
// carry would be erased the first time the user folded a repo.
func TestFoldingARepoDoesNotEraseTheSeedWindow(t *testing.T) {
	isolateConfig(t)
	prefs := config.Defaults()
	prefs.Seed = "2h"

	m := New(Config{
		Client: gh.NewClient("test"), Mode: gh.ModeAuthored,
		Interval: 30 * time.Second, Max: 200, Prefs: prefs, Links: true,
		Seed: 2 * time.Hour,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = press(m, 't') // group by repo — the first thing that writes the file

	saved, err := config.Load()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if saved.Seed != "2h" {
		t.Errorf("saved seed is %q, want it carried through untouched", saved.Seed)
	}
}

// End to end over a real captured payload: the seed has to survive the whole
// path from GraphQL JSON through convert, not just hand-built structs.
func TestSeedingARealPayload(t *testing.T) {
	res := loadFixture(t)
	if len(res.PRs) == 0 {
		t.Fatal("fixture has no pull requests")
	}

	var comments int
	for _, p := range res.PRs {
		comments += len(p.RecentComments)
	}
	if comments == 0 {
		t.Error("convert kept no dated comments from a real payload")
	}

	got := gh.Seed(res.PRs, time.Now().Add(-100000*time.Hour), res.Viewer)
	if len(got) == 0 {
		t.Fatal("a real payload seeded nothing at all")
	}

	kinds := map[gh.EventKind]int{}
	for _, e := range got {
		kinds[e.Kind]++
		if e.At.IsZero() {
			t.Errorf("%q was seeded with no timestamp", e.Text)
		}
		if e.Key == "" {
			t.Errorf("%q was seeded without a pull request", e.Text)
		}
	}
	for _, want := range []gh.EventKind{gh.EventOpened, gh.EventComment, gh.EventReview} {
		if kinds[want] == 0 {
			t.Errorf("nothing of kind %v came out of a real payload", want)
		}
	}
	t.Logf("seeded %d events from %d pull requests: %v", len(got), len(res.PRs), kinds)
}
