package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// The startup searches are far slower than a poll. Until they answer, the feed
// is waiting on them — not idly watching for changes, which describes something
// else entirely and reads as "there is nothing to show".
func TestTheFeedSaysItIsStillLookingBack(t *testing.T) {
	m := newSeeding(t, 720*time.Hour)
	m.showEvents = true

	out := feedText(m)
	if !strings.Contains(out, "looking back over the last 720h") {
		t.Errorf("the feed does not say the startup query is running:\n%s", out)
	}
	if strings.Contains(out, "watching for changes") {
		t.Errorf("the feed claims to be idle while the backfill runs:\n%s", out)
	}
}

// And once it answers, the waiting message goes away.
func TestTheLookingBackMessageStopsWhenTheBackfillLands(t *testing.T) {
	m := newSeeding(t, time.Hour)
	m.showEvents = true
	if !strings.Contains(feedText(m), "looking back") {
		t.Fatal("the feed was not waiting to begin with")
	}

	m = backfill(m, quiet(time.Now()), time.Hour)
	out := feedText(m)
	if strings.Contains(out, "looking back") {
		t.Errorf("the feed is still waiting after the backfill landed:\n%s", out)
	}
	if !strings.Contains(out, "nothing in the last 1h") {
		t.Errorf("the feed does not report what the backfill found:\n%s", out)
	}
}

// A backfill that failed must not read like one that succeeded and found a
// quiet window. The toast saying so has expired long before anyone opens the
// pane.
func TestAFailedBackfillSaysSoInTheFeed(t *testing.T) {
	m := newSeeding(t, 720*time.Hour)
	m.showEvents = true
	m = update(m, backfillDoneMsg{err: &gh.TransientError{Detail: "upstream had a moment"}})

	out := feedText(m)
	if !strings.Contains(out, "could not look back over the last 720h") {
		t.Errorf("a failed backfill is indistinguishable from a quiet one:\n%s", out)
	}
	if strings.Contains(out, "looking back over") {
		t.Error("the feed is still waiting on a backfill that already failed")
	}
}

// A poll can fill the feed before the backfill answers, and then the empty
// state — where the waiting is otherwise announced — is never drawn.
func TestTheTitleSaysItIsFillingInEvenWithEventsOnScreen(t *testing.T) {
	m := newSeeding(t, 720*time.Hour)
	m.showEvents = true
	m.events = mixedFeed()

	out := feedText(m)
	if !strings.Contains(out, "filling in…") {
		t.Errorf("nothing on screen says the backfill is still running:\n%s", out)
	}

	m = backfill(m, quiet(time.Now()), 720*time.Hour)
	if out := feedText(m); strings.Contains(out, "filling in…") {
		t.Errorf("the title still claims to be filling in:\n%s", out)
	}
}

// A frozen spinner over a feed that is still working reads as a hang. The
// first poll finishes long before the backfill, so the animation cannot be
// tied to the poll alone.
func TestTheSpinnerKeepsTurningWhileTheBackfillRuns(t *testing.T) {
	m := newSeeding(t, 720*time.Hour)

	// The first poll lands, which is what used to stop the animation.
	m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: history(time.Now())})
	if m.loading {
		t.Fatal("the poll should have finished")
	}
	if !m.backfilling {
		t.Fatal("the backfill should still be running")
	}

	_, cmd := updateCmd(m, spinner.TickMsg{})
	if cmd == nil {
		t.Error("the spinner stopped while the backfill was still running")
	}

	// Once nothing is in flight it must stop, or an idle dashboard repaints
	// ten times a second for hours.
	m = backfill(m, quiet(time.Now()), 720*time.Hour)
	if _, cmd := updateCmd(m, spinner.TickMsg{}); cmd != nil {
		t.Error("the spinner kept turning with nothing left in flight")
	}
}

// Asking for no backfill means there is nothing to wait for, and the feed
// should not imply otherwise.
func TestNoBackfillMeansNoWaiting(t *testing.T) {
	m := newLoaded(t, 140, 40) // Seed is zero here
	m.showEvents = true

	out := feedText(m)
	if strings.Contains(out, "looking back") || strings.Contains(out, "filling in") {
		t.Errorf("a dashboard that asked for no backfill claims to be running one:\n%s", out)
	}
	if !strings.Contains(out, "watching for changes") {
		t.Errorf("the plain empty state changed:\n%s", out)
	}
	if _, cmd := updateCmd(m, spinner.TickMsg{}); cmd != nil {
		t.Error("the spinner is turning with nothing in flight")
	}
}
