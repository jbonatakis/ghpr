package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// typed sends a string a character at a time, the way a filter is really used.
func typed(m Model, s string) Model {
	for _, r := range s {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// mixedFeed is a backlog worth filtering: two repositories, several actors and
// several kinds of change.
func mixedFeed() []gh.Event {
	at := time.Now().Add(-time.Hour)
	mk := func(repo string, n int, kind gh.EventKind, text, actor string) gh.Event {
		at = at.Add(time.Minute)
		return gh.Event{
			At: at, Kind: kind, Repo: repo, Number: n,
			Key: repo + "#" + itoa(n), Text: text, Actor: actor,
			URL: "https://example.invalid/" + itoa(n),
		}
	}
	return []gh.Event{
		mk("acme/starfield", 44, gh.EventOpened, "opened", "morgan-bell"),
		mk("acme/starfield", 44, gh.EventComment, "new comment", "dana-quill"),
		mk("acme/starfield", 44, gh.EventReview, "changes requested", "dana-quill"),
		mk("acme/rfcs", 9, gh.EventComment, "new comment", "github-actions"),
		mk("acme/rfcs", 9, gh.EventComment, "review comment", "riley-shaw"),
		mk("acme/rfcs", 9, gh.EventChecks, "checks failing", ""),
		mk("acme/design-docs", 12, gh.EventMerged, "merged", ""),
		mk("acme/design-docs", 12, gh.EventMention, "mentioned you", "dana-quill"),
	}
}

// inFilteredFeed steps into the feed and opens its filter box.
func inFilteredFeed(t *testing.T) Model {
	t.Helper()
	m := newLoaded(t, 140, 40)
	m.events = mixedFeed()
	m = press(press(m, 'e'), 'e')
	return press(m, '/')
}

func TestSlashInsideTheFeedFiltersTheFeed(t *testing.T) {
	m := typed(inFilteredFeed(t), "rfcs")

	out := feedText(m)
	if !strings.Contains(out, "rfcs#9") {
		t.Errorf("the filter hid what it should have matched:\n%s", out)
	}
	for _, gone := range []string{"starfield#44", "design-docs#12"} {
		if strings.Contains(out, gone) {
			t.Errorf("%s survived a filter it does not match:\n%s", gone, out)
		}
	}
}

// The pull request list is what / used to narrow, and it must be left exactly
// as it was while the feed has the keys.
func TestFilteringTheFeedLeavesTheListAlone(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = mixedFeed()
	before := len(m.rows)

	m = press(press(m, 'e'), 'e')
	m = typed(press(m, '/'), "rfcs")

	if len(m.rows) != before {
		t.Errorf("the list went from %d rows to %d; the feed filter should not touch it",
			before, len(m.rows))
	}
	if got := m.filter.Value(); got != "" {
		t.Errorf("the list's own filter picked up %q", got)
	}
}

// And the reverse, which the feed has always guaranteed: the list's filter is
// not a view onto the record.
func TestFilteringTheListLeavesTheFeedAlone(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = mixedFeed()
	m = press(m, 'e') // open, but the keys still belong to the list

	m = typed(press(m, '/'), "design-docs")
	if got := m.feedFilter.Value(); got != "" {
		t.Errorf("the feed's filter picked up %q", got)
	}
	out := feedText(m)
	for _, want := range []string{"starfield#44", "rfcs#9", "design-docs#12"} {
		if !strings.Contains(out, want) {
			t.Errorf("filtering the list dropped %s from the feed:\n%s", want, out)
		}
	}
}

func TestTheFeedFilterSearchesWhatTheLineShows(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  string
		why   string
	}{
		{"dana", "dana-quill", "the actor"},
		{"checks", "checks failing", "what happened"},
		{"starfield", "opened", "the repository"},
		{"12", "merged", "the pull request number"},
		{"MERGED", "merged", "case-insensitively"},
	} {
		m := typed(inFilteredFeed(t), tc.query)
		if out := feedText(m); !strings.Contains(out, tc.want) {
			t.Errorf("%q should match %s:\n%s", tc.query, tc.why, out)
		}
	}
}

func TestTheTitleSaysTheFeedIsFiltered(t *testing.T) {
	m := typed(inFilteredFeed(t), "dana")

	out := feedText(m)
	if !strings.Contains(out, "filtered") {
		t.Errorf("nothing says the feed is a slice of something larger:\n%s", out)
	}
	// Three of the eight lines are dana-quill's, and the cursor is on the
	// first of those three.
	if !strings.Contains(out, "1/3 · filtered from 8") {
		t.Errorf("the title does not account for what is hidden:\n%s", out)
	}
}

func TestAFilterMatchingNothingSaysSo(t *testing.T) {
	m := typed(inFilteredFeed(t), "zzzznothing")

	out := feedText(m)
	if !strings.Contains(out, "matches this filter") {
		t.Errorf("an empty result is indistinguishable from a quiet feed:\n%s", out)
	}
	if strings.Contains(out, "watching for changes") {
		t.Error("an empty filter result claimed the feed itself was empty")
	}
}

// esc backs out one step at a time: the filter first, the feed second.
func TestEscapeClearsTheFilterBeforeLeavingTheFeed(t *testing.T) {
	m := typed(inFilteredFeed(t), "rfcs")

	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.feedFilter.Value() != "" {
		t.Error("the first esc should clear the filter")
	}
	if !m.eventsFocus {
		t.Error("the first esc should not also leave the feed")
	}
	if !strings.Contains(feedText(m), "starfield#44") {
		t.Error("clearing the filter did not restore the whole record")
	}

	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.eventsFocus {
		t.Error("the second esc should hand the keys back to the list")
	}
}

// enter commits the filter and gives the navigation keys back, so a filtered
// feed can still be scrolled and opened.
func TestEnterCommitsTheFilterAndReturnsTheKeys(t *testing.T) {
	m := typed(inFilteredFeed(t), "quill")
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.feedFiltering {
		t.Error("enter should close the filter box")
	}
	if m.feedFilter.Value() != "quill" {
		t.Errorf("enter discarded the filter: %q", m.feedFilter.Value())
	}
	m = press(m, 'j')
	if e := m.selectedEvent(); e == nil || e.Actor != "dana-quill" {
		t.Errorf("scrolling a filtered feed left it: %+v", e)
	}
}

// Typing into the filter must not be read as commands — "e" would otherwise
// close the very pane being filtered.
func TestTypingInTheFilterIsNotACommand(t *testing.T) {
	m := typed(inFilteredFeed(t), "merged")

	if !m.showEvents {
		t.Error("typing e into the filter closed the activity pane")
	}
	if m.feedFilter.Value() != "merged" {
		t.Errorf("the filter box lost characters to the keymap: %q", m.feedFilter.Value())
	}
}

// The cursor and the window have to stay inside a filtered feed, not the
// backlog it is a slice of.
func TestFilteringKeepsTheCursorInBounds(t *testing.T) {
	m := press(press(newLoadedWith(t, mixedFeed()), 'e'), 'e')
	m = press(m, 'G') // the far end of the unfiltered feed

	m = typed(press(m, '/'), "design-docs")
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	if n := len(m.feedEvents()); m.eventCursor >= n {
		t.Errorf("cursor is at %d in a feed of %d", m.eventCursor, n)
	}
	if e := m.selectedEvent(); e == nil {
		t.Error("the cursor points at nothing after filtering")
	} else if e.Repo != "acme/design-docs" {
		t.Errorf("the cursor is on %s, outside the filter", e.Repo)
	}
}

// Leaving the feed puts it back to the whole record, the same way it goes back
// to the live view: a pane nobody is reading should not stay narrowed.
func TestLeavingTheFeedClearsItsFilter(t *testing.T) {
	m := typed(inFilteredFeed(t), "rfcs")
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc}) // clears filter
	m = typed(press(m, '/'), "rfcs")
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})

	m = update(m, tea.KeyMsg{Type: tea.KeyEsc}) // leaves the feed
	if m.feedFiltered() {
		t.Errorf("the filter outlived the feed: %q", m.feedFilter.Value())
	}
	if !strings.Contains(feedText(m), "starfield#44") {
		t.Error("the pane is still narrowed after the keys went back")
	}
}

func TestTheFooterOffersTheFilterInsideTheFeed(t *testing.T) {
	m := newLoadedWith(t, mixedFeed())
	m = press(press(m, 'e'), 'e')

	if out := ansi.Strip(m.View()); !strings.Contains(out, "/ filter") {
		t.Errorf("the feed's footer never mentions the filter:\n%s", out)
	}
}

// newLoadedWith is newLoaded with a backlog already in place.
func newLoadedWith(t *testing.T, events []gh.Event) Model {
	t.Helper()
	m := newLoaded(t, 140, 40)
	m.events = events
	return m
}
