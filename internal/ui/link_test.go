package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

const prURL = "https://github.com/acme/starfield/pull/96"

func linkFeed() []gh.Event {
	now := time.Now()
	return []gh.Event{
		{At: now, Kind: gh.EventComment, Repo: "acme/starfield", Number: 96,
			Key: "acme/starfield#96", Text: "1 new comment", Actor: "morgan-bell", URL: prURL},
		{At: now, Kind: gh.EventChecks, Repo: "acme/sensor-presence-collector", Number: 828,
			Key: "acme/sensor-presence-collector#828", Text: "checks passing",
			URL: "https://github.com/acme/sensor-presence-collector/pull/828"},
	}
}

func TestLinkWrapsTextInOSC8(t *testing.T) {
	got := link(true, prURL, "starfield#96")

	if !strings.HasPrefix(got, "\x1b]8;;"+prURL) {
		t.Errorf("missing OSC 8 opener: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b]8;;\x1b\\") {
		t.Errorf("missing OSC 8 closer: %q", got)
	}
	if ansi.Strip(got) != "starfield#96" {
		t.Errorf("visible text = %q, want the bare reference", ansi.Strip(got))
	}
	if w := ansi.StringWidth(got); w != 12 {
		t.Errorf("a hyperlink must carry no width, got %d", w)
	}
}

func TestLinkIsInertWhenDisabledOrURLIsUnknown(t *testing.T) {
	if got := link(false, prURL, "starfield#96"); got != "starfield#96" {
		t.Errorf("disabled link should pass text through, got %q", got)
	}
	if got := link(true, "", "starfield#96"); got != "starfield#96" {
		t.Errorf("no URL should pass text through, got %q", got)
	}
}

// TestActivityReferencesAreClickable is the point of the feature.
func TestActivityReferencesAreClickable(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = linkFeed()

	out := m.eventsView()
	if !strings.Contains(out, "\x1b]8;;"+prURL) {
		t.Errorf("feed reference is not a hyperlink to the pull request:\n%q", out)
	}
	// The visible text is untouched.
	if !strings.Contains(ansi.Strip(out), "starfield#96") {
		t.Error("feed lost the visible reference")
	}
}

func TestActivityLinkPointsAtTheEventsOwnURL(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = linkFeed()

	out := m.eventsView()
	// Each row links to its own pull request, not a URL built by convention:
	// that is what keeps GitHub Enterprise working.
	for _, want := range []string{prURL, "https://github.com/acme/sensor-presence-collector/pull/828"} {
		if !strings.Contains(out, "\x1b]8;;"+want) {
			t.Errorf("missing link to %s", want)
		}
	}
}

func TestListRowNumberIsClickable(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.grouped = false
	m.rebuild()

	out := m.View()
	first := m.rows[0].pr
	if !strings.Contains(out, "\x1b]8;;"+first.URL) {
		t.Errorf("list row does not link to %s", first.URL)
	}
}

// TestHyperlinksDoNotChangeTheLayout is the guarantee that matters: the escape
// sequences are invisible, so the rendered frame must be identical with them on
// and off, character for character.
func TestHyperlinksDoNotChangeTheLayout(t *testing.T) {
	for _, size := range []struct{ w, h int }{{60, 20}, {80, 24}, {100, 30}, {140, 40}, {200, 50}} {
		with := newLoaded(t, size.w, size.h)
		with.showDetail, with.showEvents = true, true
		with.events = linkFeed()
		with.rebuild()

		without := with
		without.cfg.Links = false

		a, b := ansi.Strip(with.View()), ansi.Strip(without.View())
		if a != b {
			t.Errorf("size %dx%d: hyperlinks changed the visible frame", size.w, size.h)
			as, bs := strings.Split(a, "\n"), strings.Split(b, "\n")
			for i := range as {
				if i < len(bs) && as[i] != bs[i] {
					t.Errorf("  line %d\n   with: %q\n    w/o: %q", i, as[i], bs[i])
					break
				}
			}
		}
		for i, ln := range strings.Split(with.View(), "\n") {
			if got := ansi.StringWidth(ln); got > size.w {
				t.Errorf("size %dx%d: line %d is %d cells with links on", size.w, size.h, i, got)
			}
		}
	}
}

func TestHyperlinkSurvivesReferenceTruncation(t *testing.T) {
	m := newLoaded(t, 72, 30) // narrow: the repo name gets elided
	m.showEvents = true
	m.events = []gh.Event{{
		At: time.Now(), Kind: gh.EventReview,
		Repo:   "acme/retention-policy-enforcer-export-history",
		Number: 99, Key: "acme/retention-policy-enforcer-export-history#99",
		Text: "approved", Actor: "priya-shah",
		URL: "https://github.com/acme/retention-policy-enforcer-export-history/pull/99",
	}}

	out := m.eventsView()
	if !strings.Contains(out, "\x1b]8;;https://github.com/acme/retention-policy-enforcer-export-history/pull/99") {
		t.Error("a truncated reference should still be clickable")
	}
	if !strings.Contains(ansi.Strip(out), "#99") {
		t.Error("the number should survive alongside the link")
	}
	for _, ln := range strings.Split(out, "\n") {
		if got := ansi.StringWidth(ln); got > 72 {
			t.Errorf("line is %d cells: %q", got, ansi.Strip(ln))
		}
	}
}

func TestDetailPaneURLIsClickable(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.grouped = false
	m.rebuild()
	m.cursor = 0
	m.showDetail = true

	p := m.selected()
	if p == nil {
		t.Fatal("no selection")
	}
	if !strings.Contains(m.View(), "\x1b]8;;"+p.URL) {
		t.Error("detail pane URL should be clickable")
	}
}

func TestDiffAttachesThePullRequestURL(t *testing.T) {
	prev := loadFixture(t)
	next := loadFixture(t)
	var target string
	for i := range next.PRs {
		if next.PRs[i].Key() == victim {
			next.PRs[i].IssueComments += 1
			target = next.PRs[i].URL
		}
	}
	events := gh.Diff(prev.PRs, next.PRs, gh.DiffOpts{Now: time.Now(), PrevComplete: true})
	if len(events) == 0 {
		t.Fatal("expected an event")
	}
	for _, e := range events {
		if e.Key == victim && e.URL != target {
			t.Errorf("event URL = %q, want %q", e.URL, target)
		}
	}
	if target == "" {
		t.Fatal("fixture PR has no URL")
	}
}
