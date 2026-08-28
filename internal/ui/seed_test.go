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
// from the last hour when it does.
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
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})

	for _, want := range []string{"opened", "new commits", "checks passing", "approved", "new comment"} {
		if !feedHas(m, want) {
			t.Errorf("the seeded feed is missing %q", want)
		}
	}
	if !feedHas(m, "ghpr started") {
		t.Error("the seeded feed does not say where the reconstruction stops")
	}
}

// The marker is the boundary, so nothing reconstructed may sit above it.
func TestTheSessionMarkerIsTheNewestSeededLine(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})

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
	// not asked for one gets.
	if m := newLoaded(t, 140, 40); len(m.events) != 0 {
		t.Errorf("an unasked-for seed filled the feed with %d events", len(m.events))
	}
}

func TestSeedingHappensOncePerSession(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	first := len(m.events)

	// A second poll of the same data changes nothing, so it must add nothing.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if len(m.events) != first {
		t.Errorf("a later poll re-seeded the feed: %d events, was %d", len(m.events), first)
	}
}

// Switching mode starts a new list but not a new feed, and the two searches
// overlap: seeding again would repeat the history of everything in both.
func TestSwitchingModeDoesNotReseed(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	first := len(m.events)

	m = press(m, 'm')
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if len(m.events) != first {
		t.Errorf("switching mode re-seeded the feed: %d events, was %d", len(m.events), first)
	}
}

// A failed first poll can say nothing about the past, so the seed must wait
// for one that succeeds rather than being spent on the error.
func TestAFailedFirstPollDoesNotSpendTheSeed(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: &gh.TransientError{Detail: "upstream had a moment"}})
	if len(m.events) != 0 {
		t.Fatalf("a failed poll produced %d events", len(m.events))
	}

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if !feedHas(m, "opened") {
		t.Error("the seed was lost to the failed poll")
	}
}

// The gutter dot means "changed in the last minute". Seeded history is older
// than that and must not light every row up at launch.
func TestSeededHistoryDoesNotLightUpTheList(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	m.now = now

	if m.isFresh("acme/starfield#44") {
		t.Error("a pull request last touched four minutes ago is marked as just changed")
	}
}

func TestRecentSeededActivityStillCountsAsFresh(t *testing.T) {
	now := time.Now()
	res := history(now)
	res.PRs[0].RecentComments = []gh.Comment{{By: "riley-shaw", At: now.Add(-10 * time.Second)}}

	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: res})
	m.now = now

	if !m.isFresh("acme/starfield#44") {
		t.Error("a comment from ten seconds ago should still be marked fresh")
	}
}

func TestTheSeededFeedRendersWithoutAPullRequestReference(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
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

// A feed filled in behind a closed pane is indistinguishable from one that was
// never filled in at all.
func TestASeededFeedOpensItself(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, time.Hour)
	if m.showEvents {
		t.Fatal("the pane should start closed")
	}

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(now)})
	if !m.showEvents {
		t.Error("a seeded feed left itself hidden")
	}
	if m.eventsFocus {
		t.Error("it should show the feed, not steal the keys")
	}
}

func TestAQuietWindowDoesNotOpenThePane(t *testing.T) {
	now := time.Now()
	quiet := history(now)
	// Everything on it happened long before the window.
	quiet.PRs[0].CreatedAt = now.Add(-30 * 24 * time.Hour)
	quiet.PRs[0].PushedAt = now.Add(-29 * 24 * time.Hour)
	quiet.PRs[0].ChecksAt = now.Add(-29 * 24 * time.Hour)
	quiet.PRs[0].Reviewers = nil
	quiet.PRs[0].RecentComments = nil

	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: quiet})
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
	now := time.Now()
	quiet := history(now)
	quiet.PRs[0].CreatedAt = now.Add(-30 * 24 * time.Hour)
	quiet.PRs[0].PushedAt = now.Add(-29 * 24 * time.Hour)
	quiet.PRs[0].ChecksAt = now.Add(-29 * 24 * time.Hour)
	quiet.PRs[0].Reviewers = nil
	quiet.PRs[0].RecentComments = nil

	m := newSeeding(t, time.Hour)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: quiet})
	m.showEvents = true
	if out := feedText(m); !strings.Contains(out, "nothing in the last 1h") {
		t.Errorf("the empty feed does not say it looked back:\n%s", out)
	}

	// With no window asked for, there is nothing to report having looked at.
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

// End to end over a real captured payload: the seed has to survive the whole
// path from GraphQL JSON through convert, not just hand-built structs. The
// fixture predates committedDate and completedAt, so pushes and checks are
// absent from it — everything else has to come through.
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

	// Wide enough to reach everything in a fixture captured months ago.
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
		got := strings.TrimSpace(eventTime(tc.at, now))
		if got != tc.want {
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

// The whole point: a feed read top to bottom must run backwards in time, and
// look like it does.
func TestABackfilledFeedReadsInOrder(t *testing.T) {
	now := time.Now()
	m := newSeeding(t, 720*time.Hour)

	res := history(now)
	// Spread across days, the case that made the screenshot look shuffled.
	res.PRs[0].CreatedAt = now.AddDate(0, 0, -20)
	res.PRs[0].PushedAt = now.AddDate(0, 0, -18)
	res.PRs[0].ChecksAt = now.AddDate(0, 0, -18)
	res.PRs[0].Reviewers = []gh.Reviewer{{Login: "dana-quill", State: "APPROVED", At: now.AddDate(0, 0, -9)}}
	res.PRs[0].RecentComments = []gh.Comment{
		{By: "github-actions", At: now.AddDate(0, 0, -14)},
		{By: "github-actions", At: now.AddDate(0, 0, -3)},
	}

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: res})
	m.now = now

	for i := 1; i < len(m.events); i++ {
		if m.events[i].At.Before(m.events[i-1].At) {
			t.Fatalf("the feed is out of order at %d: %q then %q",
				i, m.events[i-1].Text, m.events[i].Text)
		}
	}

	// And nothing older than today may show a bare clock time, which is what
	// made a correct order look wrong.
	for _, e := range m.events {
		stamp := strings.TrimSpace(eventTime(e.At, now))
		sameDay := e.At.Local().YearDay() == now.Local().YearDay() && e.At.Local().Year() == now.Local().Year()
		if !sameDay && strings.Contains(stamp, ":") {
			t.Errorf("%q happened on another day but shows the clock time %q", e.Text, stamp)
		}
	}
}
