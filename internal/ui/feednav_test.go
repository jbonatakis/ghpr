package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// backlog builds n events, oldest first, so "event 00" is the far end of the
// history and "event NN" is what just happened.
func backlog(n int) []gh.Event {
	out := make([]gh.Event, 0, n)
	for i := 0; i < n; i++ {
		e := eventFor("acme/starfield", i, fmt.Sprintf("event %02d", i))
		e.URL = fmt.Sprintf("https://github.com/acme/starfield/pull/%d", i)
		out = append(out, e)
	}
	return out
}

// inFeed opens the activity pane and steps into it.
func inFeed(t *testing.T, events int) Model {
	t.Helper()
	m := newLoaded(t, 140, 40)
	m.events = backlog(events)
	return press(press(m, 'e'), 'e')
}

// deeperThanThePane returns a focused feed holding more than it can draw, so
// there is genuinely something below the fold to go looking for.
func deeperThanThePane(t *testing.T) Model {
	t.Helper()
	m := inFeed(t, 4)
	m.events = backlog(m.eventRowCount() * 3)
	m.clampEvents()
	return m
}

func TestTheActivityKeyCyclesShowScrollHide(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = backlog(30)

	m = press(m, 'e')
	if !m.showEvents || m.eventsFocus {
		t.Error("the first press should show the feed without taking the keys")
	}
	m = press(m, 'e')
	if !m.showEvents || !m.eventsFocus {
		t.Error("the second press should step into the feed")
	}
	m = press(m, 'e')
	if m.showEvents || m.eventsFocus {
		t.Error("the third press should put the feed away")
	}
}

// Opening the pane without focusing it is the behaviour that existed before
// the feed could be scrolled, and it is what anyone watching activity go by
// while working the list still wants.
func TestAWatchedFeedLeavesTheListKeysAlone(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = backlog(30)
	m = press(m, 'e')

	before := m.cursor
	m = press(m, 'j')
	if m.cursor == before {
		t.Error("j moved nothing; an unfocused feed should not swallow the list keys")
	}
}

func TestScrollingBackReachesOlderActivity(t *testing.T) {
	m := deeperThanThePane(t)
	newest := m.events[len(m.events)-1].Text

	out := feedText(m)
	if !strings.Contains(out, newest) {
		t.Error("the newest event should be at the top of the pane")
	}
	if strings.Contains(out, "event 00") {
		t.Fatal("the fixture is too small: the whole backlog fits without scrolling")
	}

	// A page and a half down: far enough that the top of the pane has moved.
	for i := 0; i < m.eventRowCount()+5; i++ {
		m = press(m, 'j')
	}
	out = feedText(m)
	if strings.Contains(out, newest) {
		t.Errorf("the window should have moved off %q:\n%s", newest, out)
	}
	if !strings.Contains(out, m.selectedEvent().Text) {
		t.Error("the cursor scrolled out of its own pane")
	}
}

// The newest event is drawn at the top, so g and G run with the screen rather
// than with the clock.
func TestTheFeedJumpsToEitherEnd(t *testing.T) {
	m := deeperThanThePane(t)
	newest := m.events[len(m.events)-1].Text

	m = press(m, 'G')
	if !strings.Contains(feedText(m), "event 00") {
		t.Error("G should reach the oldest thing in the backlog")
	}
	if strings.Contains(feedText(m), newest) {
		t.Error("G left the newest event on screen")
	}
	m = press(m, 'g')
	if !strings.Contains(feedText(m), newest) {
		t.Error("g should come back to the newest")
	}
}

func TestScrollingStopsAtTheEndsOfTheBacklog(t *testing.T) {
	m := inFeed(t, 30)
	for i := 0; i < 200; i++ {
		m = press(m, 'j')
	}
	if m.eventCursor != 29 {
		t.Errorf("cursor ran to %d, want it held at the oldest event", m.eventCursor)
	}
	for i := 0; i < 200; i++ {
		m = press(m, 'k')
	}
	if m.eventCursor != 0 {
		t.Errorf("cursor ran to %d, want it held at the newest event", m.eventCursor)
	}
}

// A poll landing mid-read must not shuffle the line being looked at.
func TestNewActivityDoesNotYankTheFeedYouAreReading(t *testing.T) {
	m := inFeed(t, 30)
	for i := 0; i < 5; i++ {
		m = press(m, 'j')
	}

	holding := m.selectedEvent().Text
	if holding != "event 24" {
		t.Fatalf("cursor landed on %q, expected event 24", holding)
	}

	m.record([]gh.Event{eventFor("acme/starfield", 99, "just landed")})

	if got := m.selectedEvent().Text; got != holding {
		t.Errorf("the cursor slid from %q to %q when a poll arrived", holding, got)
	}
	if !strings.Contains(feedText(m), holding) {
		t.Error("the line being read scrolled out of the pane")
	}
}

// A feed sitting at the top is being watched, not read, so it should follow
// along instead of freezing.
func TestAFeedAtTheTopKeepsFollowingLive(t *testing.T) {
	m := inFeed(t, 30)

	m.record([]gh.Event{eventFor("acme/starfield", 99, "just landed")})

	if m.eventCursor != 0 {
		t.Errorf("cursor drifted to %d; a feed left at the top should stay there", m.eventCursor)
	}
	if !strings.Contains(feedText(m), "just landed") {
		t.Error("new activity did not appear in a feed that was following live")
	}
}

func TestEscapeLeavesTheFeedWithoutQuitting(t *testing.T) {
	m := inFeed(t, 30)
	for i := 0; i < 12; i++ {
		m = press(m, 'j')
	}

	m, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Error("esc inside the feed should hand back the keys, not quit")
	}
	if m.eventsFocus {
		t.Error("esc should leave the feed")
	}
	if !m.showEvents {
		t.Error("esc should hand back the keys without closing the pane")
	}
	if !strings.Contains(feedText(m), "event 29") {
		t.Error("a feed nobody is reading should be back on the live view")
	}
}

func TestTheFeedSaysWhereYouAreInIt(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.events = backlog(30)

	m = press(m, 'e')
	if strings.Contains(feedText(m), "/30") {
		t.Error("an unfocused feed should not claim a position")
	}
	m = press(m, 'e')
	if !strings.Contains(feedText(m), "1/30") {
		t.Errorf("the focused feed should show its position:\n%s", feedText(m))
	}
	m = press(m, 'j')
	if !strings.Contains(feedText(m), "2/30") {
		t.Errorf("the position should follow the cursor:\n%s", feedText(m))
	}
}

func TestTheFooterExplainsTheFeedKeys(t *testing.T) {
	m := inFeed(t, 30)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "back to the list") {
		t.Errorf("the footer should say how to get out of the feed:\n%s", out)
	}
}

func TestEnterOpensTheEventUnderTheCursor(t *testing.T) {
	m := inFeed(t, 30)
	for i := 0; i < 4; i++ {
		m = press(m, 'j')
	}

	e := m.selectedEvent()
	if e == nil || e.Text != "event 25" {
		t.Fatalf("selected %+v, want event 25", e)
	}
	if _, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Error("enter in the feed should open the pull request the line names")
	}
}

// The pane below the list must always be the same height, or everything under
// it moves whenever activity arrives.
func TestTheFeedPaneIsAlwaysTheSameHeight(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 9, 30} {
		m := newLoaded(t, 140, 40)
		m.events = backlog(n)

		m = press(m, 'e') // shown, but not focused
		if got := strings.Count(m.eventsView(), "\n"); got != eventsHeight {
			t.Errorf("%d events: resting pane emitted %d lines, want %d", n, got, eventsHeight)
		}
		m = press(m, 'e') // focused, and grown into the room the list is not using
		if !m.eventsFocus {
			t.Fatalf("%d events: the feed did not take focus", n)
		}
		for _, stage := range []string{"focused", "scrolled"} {
			if got, want := strings.Count(m.eventsView(), "\n"), m.eventsPaneHeight(); got != want {
				t.Errorf("%d events, %s: pane emitted %d lines, want %d", n, stage, got, want)
			}
			m = press(m, 'G')
		}
	}
}

// The selection band runs to the right edge, so a focused feed is the frame
// most likely to overflow a narrow terminal.
func TestAFocusedFeedFitsTheTerminal(t *testing.T) {
	for _, size := range []struct{ w, h int }{{60, 20}, {80, 24}, {104, 30}, {140, 40}, {200, 50}} {
		m := newLoaded(t, size.w, size.h)
		m.events = backlog(30)
		m = press(press(m, 'e'), 'e')
		for i := 0; i < 4; i++ {
			m = press(m, 'j')
		}
		for i, ln := range lines(m.View()) {
			if got := ansi.StringWidth(ln); got > size.w {
				t.Errorf("width %d: line %d is %d cells: %q", size.w, i, got, ansi.Strip(ln))
			}
		}
	}
}

func TestAFocusedFeedNeverOverflowsTheScreen(t *testing.T) {
	for _, size := range []struct{ w, h int }{{60, 12}, {80, 20}, {104, 24}, {140, 40}, {200, 60}} {
		m := newLoaded(t, size.w, size.h)
		m.events = backlog(200)
		m = press(press(m, 'e'), 'e')
		for _, withDetail := range []bool{false, true} {
			m.showDetail = withDetail
			m.clampScroll()
			if got := len(lines(m.View())); got > size.h {
				t.Errorf("%dx%d detail=%v: frame is %d lines", size.w, size.h, withDetail, got)
			}
			if m.listHeight() < 0 {
				t.Errorf("%dx%d detail=%v: the list was squeezed to %d", size.w, size.h, withDetail, m.listHeight())
			}
		}
	}
}

// The footer has one line to say how to get out of the feed, and truncating it
// mid-word is exactly where that matters least and is needed most.
func TestTheFeedHintsFitANarrowTerminal(t *testing.T) {
	for _, w := range []int{100, 104, 120} {
		m := newLoaded(t, w, 30)
		m.events = backlog(30)
		m = press(press(m, 'e'), 'e')
		if out := ansi.Strip(m.View()); !strings.Contains(out, "esc back to the list") {
			t.Errorf("width %d: the way out was truncated away:\n%s", w, lastLine(out))
		}
	}
}

func lastLine(s string) string {
	ls := lines(strings.TrimRight(s, "\n"))
	return ls[len(ls)-1]
}

// The dashboard keys that have nothing to do with the feed still work from
// inside it, so stepping in is not a trap.
func TestTheDashboardStillRespondsFromInsideTheFeed(t *testing.T) {
	m := inFeed(t, 30)

	m = press(m, '?')
	if !m.showHelp {
		t.Error("? should still open the help overlay from inside the feed")
	}
}
