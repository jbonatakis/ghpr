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
