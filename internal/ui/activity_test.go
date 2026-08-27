package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// Polling twice with identical data must produce no activity at all.
func TestIdenticalPollsProduceNoFreshMarkers(t *testing.T) {
	for _, fixture := range []string{"search_authored.json", "search_review_requested.json"} {
		m := newLoaded(t, 120, 40)
		// Establish this fixture as the baseline first; switching fixtures is
		// a genuine change and rightly reports every PR as newly opened.
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixtureFile(t, fixture)})
		baseline := len(m.events)

		// Now poll the very same data again.
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: loadFixtureFile(t, fixture)})

		if got := len(m.events) - baseline; got != 0 {
			for _, e := range m.events[baseline:] {
				t.Logf("spurious: %s#%d %s", e.Repo, e.Number, e.Text)
			}
			t.Errorf("%s: %d spurious events from identical data", fixture, got)
		}
		m.changed = map[string]time.Time{}
		for _, e := range m.events[baseline:] {
			m.changed[e.Key] = m.now
		}
		var marked int
		for _, p := range m.prs {
			if m.isFresh(p.Key()) {
				marked++
			}
		}
		if marked != 0 {
			t.Errorf("%s: %d PRs marked fresh with no change", fixture, marked)
		}
	}
}

// Which real-world transitions light a PR up?
func TestWhatTriggersAFreshMarker(t *testing.T) {
	base := gh.PR{Repo: "o/r", Number: 1, Mergeable: "MERGEABLE", ChecksState: gh.CheckSuccess, HeadOID: "a"}
	for _, tc := range []struct {
		name   string
		change func(*gh.PR)
	}{
		{"a check starts running", func(p *gh.PR) { p.ChecksState = gh.CheckPending }},
		{"a check finishes", func(p *gh.PR) { p.ChecksState = gh.CheckFailure }},
		{"a comment arrives", func(p *gh.PR) { p.IssueComments = 1 }},
		{"someone approves", func(p *gh.PR) {
			p.Reviewers = []gh.Reviewer{{Login: "morgan-bell", State: "APPROVED", At: time.Now()}}
		}},
		{"a push lands", func(p *gh.PR) { p.HeadOID = "b" }},
	} {
		next := base
		tc.change(&next)
		events := gh.Diff([]gh.PR{base}, []gh.PR{next}, gh.DiffOpts{Now: time.Now(), PrevComplete: true})
		if len(events) == 0 {
			t.Errorf("%s: produced no event", tc.name)
		} else {
			t.Logf("%-24s -> %s (by %q)", tc.name, events[0].Text, events[0].Actor)
		}
	}
}

// TestHelpLegendIsAligned guards against styled strings containing newlines:
// lipgloss block-pads those, which silently shifts the following row.
func TestHelpLegendIsAligned(t *testing.T) {
	withColor(t)
	m := newLoaded(t, 100, 44)
	m = press(m, '?')

	var legend []string
	seen := false
	for _, ln := range strings.Split(ansi.Strip(m.View()), "\n") {
		if strings.Contains(ln, "markers") {
			seen = true
			continue
		}
		if seen {
			if strings.TrimSpace(ln) == "" {
				break
			}
			legend = append(legend, ln)
		}
	}
	if !seen {
		t.Fatal("help overlay has no marker legend")
	}
	if len(legend) < 4 {
		t.Fatalf("legend has %d rows, want the full set", len(legend))
	}
	for _, ln := range legend {
		if !strings.HasPrefix(ln, "   ") || strings.HasPrefix(ln, "    ") {
			t.Errorf("legend row is misaligned: %q", ln)
		}
	}
	if !strings.Contains(strings.Join(legend, "\n"), "changed in the last minute") {
		t.Error("legend should explain the freshness dot")
	}
}

// TestAppearingOldPullRequestsAreNotAnnounced replays the burst of "+ opened"
// lines seen for ci-pipelines pull requests that were months old: an earlier
// poll's pagination had simply missed a page of them.
func TestAppearingOldPullRequestsAreNotAnnounced(t *testing.T) {
	full := loadFixtureFile(t, "search_review_requested.json")
	if len(full.PRs) < 12 {
		t.Fatalf("fixture has only %d PRs", len(full.PRs))
	}

	// Poll one sees a partial set, as if a page boundary shifted under it.
	partial := full
	partial.PRs = append([]gh.PR{}, full.PRs[:len(full.PRs)-8]...)
	partial.Complete = true

	// First poll of the session sees the partial set; a first load is never
	// reported as activity, so the events all come from the second poll.
	m := newUnloaded(t, 120, 40)
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: partial})
	baseline := len(m.events)

	// Poll two sees them all. The eight extras are old, not new.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: full})

	var opened []gh.Event
	for _, e := range m.events[baseline:] {
		if e.Kind == gh.EventOpened {
			opened = append(opened, e)
		}
	}
	if len(opened) > 0 {
		for _, e := range opened {
			var age string
			for _, p := range full.PRs {
				if p.Key() == e.Key {
					age = time.Since(p.CreatedAt).Round(time.Hour).String()
				}
			}
			t.Errorf("announced %s (created %s ago) as opened", e.Key, age)
		}
	}

	// They should still join the list; they just do so quietly.
	if len(m.prs) != len(full.PRs) {
		t.Errorf("tracked %d PRs, want all %d", len(m.prs), len(full.PRs))
	}
	if out := ansi.Strip(m.View()); strings.Count(out, "opened") > 0 {
		t.Errorf("the activity pane still shows an opened line: %q", firstLines(out, 3))
	}
}

func TestGenuinelyNewPullRequestIsStillAnnounced(t *testing.T) {
	full := loadFixture(t)

	m := newLoaded(t, 120, 40)

	fresh := full.PRs[0]
	fresh.Number = 99999
	fresh.ID = "new-id"
	fresh.CreatedAt = time.Now().Add(-90 * time.Second)
	next := full
	next.PRs = append(append([]gh.PR{}, full.PRs...), fresh)
	next.Complete = true

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})

	var found bool
	for _, e := range m.events {
		if e.Kind == gh.EventOpened && e.Number == 99999 {
			found = true
		}
	}
	if !found {
		t.Error("a pull request opened 90 seconds ago should be reported")
	}
}

// TestReviewRequestArrivesInTheFeed drives the whole model: an existing pull
// request enters the review-requested search because the viewer was added as a
// reviewer, and that should show up.
func TestReviewRequestArrivesInTheFeed(t *testing.T) {
	full := loadFixtureFile(t, "search_review_requested.json")
	full.Complete = true

	// A complete baseline poll without the newcomer.
	baseline := full
	baseline.PRs = append([]gh.PR{}, full.PRs[:len(full.PRs)-1]...)

	m := newUnloaded(t, 140, 40)
	m.cfg.Mode = gh.ModeReviewRequested
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: baseline})
	if len(m.events) != 0 {
		t.Fatalf("first load should be quiet, got %d events", len(m.events))
	}

	// Now the request lands: an older pull request, just touched.
	arriving := full.PRs[len(full.PRs)-1]
	arriving.UpdatedAt = time.Now().Add(-15 * time.Second)
	next := full
	next.PRs = append(append([]gh.PR{}, baseline.PRs...), arriving)

	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})

	var found *gh.Event
	for i := range m.events {
		if m.events[i].Kind == gh.EventArrived {
			found = &m.events[i]
		}
	}
	if found == nil {
		t.Fatalf("no arrival reported, got %d events", len(m.events))
	}
	if found.Key != arriving.Key() {
		t.Errorf("arrival is for %s, want %s", found.Key, arriving.Key())
	}

	m.showEvents = true
	out := ansi.Strip(m.eventsView())
	if !strings.Contains(out, "review requested") {
		t.Errorf("feed should say a review was requested:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("#%d", arriving.Number)) {
		t.Errorf("feed should name the pull request:\n%s", out)
	}
}

// TestArrivalIsSilentAfterAPartialPoll keeps the earlier burst from returning:
// if the previous snapshot was incomplete, appearances prove nothing.
func TestArrivalIsSilentAfterAPartialPoll(t *testing.T) {
	full := loadFixtureFile(t, "search_review_requested.json")

	partial := full
	partial.PRs = append([]gh.PR{}, full.PRs[:len(full.PRs)-8]...)
	partial.Complete = false // cut short

	m := newUnloaded(t, 120, 40)
	m.cfg.Mode = gh.ModeReviewRequested
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: partial})

	// Even if the newcomers look freshly touched, an incomplete baseline means
	// we cannot tell an arrival from something we simply had not fetched.
	recent := full
	recent.Complete = true
	recent.PRs = append([]gh.PR{}, full.PRs...)
	for i := range recent.PRs {
		recent.PRs[i].UpdatedAt = time.Now().Add(-10 * time.Second)
	}
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: recent})

	for _, e := range m.events {
		if e.Kind == gh.EventArrived || e.Kind == gh.EventOpened {
			t.Errorf("announced %s (%s) after an incomplete baseline", e.Key, e.Text)
		}
	}
}

func TestModeSwitchResetsTheArrivalBaseline(t *testing.T) {
	m := newLoaded(t, 120, 40)
	if !m.lastComplete && m.loaded {
		// newLoaded's fixture may or may not be flagged complete; set it up.
		res := loadFixture(t)
		res.Complete = true
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: res})
	}
	m = press(m, 'm')
	if m.lastComplete {
		t.Error("switching mode should drop the previous baseline, not carry it across searches")
	}
}
