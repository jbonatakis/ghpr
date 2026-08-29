package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// spread builds n events running back from now, one every ten minutes, so the
// oldest is well outside any seed window worth naming.
func spread(n int, now time.Time) []gh.Event {
	out := make([]gh.Event, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, gh.Event{
			At:   now.Add(-time.Duration(i+1) * 10 * time.Minute),
			Kind: gh.EventComment, Key: "acme/hyperspace#99",
			Repo: "acme/hyperspace", Number: 99, Text: "1 new comment",
			Actor: "brianmuse", URL: "https://example.invalid/99",
		})
	}
	return out
}

// The closing word used to count what had been filed and call all of it "from
// the last 1h". Most of it comes off the saved record and reaches back weeks
// past that window, and the backlog keeps fewer than were filed — so the
// sentence was wrong twice over.
func TestTheClosingWordDescribesTheFeedNotTheSeedWindow(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))

	// A day's worth of saved activity, far more than the seed window covers.
	m.record(spread(144, now))
	m.backfillFound = 144
	m.now = now
	m = m.applyBackfill(backfillDoneMsg{})

	if strings.Contains(m.toast, "1h") {
		t.Errorf("the feed is a day deep and the message still claims the seed window: %q", m.toast)
	}
	if !strings.Contains(m.toast, "144 events") {
		t.Errorf("the count does not match the feed: %q", m.toast)
	}
	// A day of ten-minute steps reaches back about 24 hours.
	if !strings.Contains(m.toast, "going back 23h") && !strings.Contains(m.toast, "going back 1d") {
		t.Errorf("the span does not match the feed: %q", m.toast)
	}
}

// It also has to count what the backlog kept, not what was handed to it.
func TestTheCountIsWhatTheFeedActuallyHolds(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))

	over := maxEvents + 125
	m.record(spread(over, now))
	m.backfillFound = over
	m.now = now
	m = m.applyBackfill(backfillDoneMsg{})

	if m.shownEvents() != maxEvents {
		t.Fatalf("the feed holds %d events, want the cap of %d", m.shownEvents(), maxEvents)
	}
	if strings.Contains(m.toast, itoa(over)) {
		t.Errorf("the message claims %d events the backlog has no room for: %q", over, m.toast)
	}
	if !strings.Contains(m.toast, itoa(maxEvents)) {
		t.Errorf("the message does not count what was kept: %q", m.toast)
	}
}

// The marker for where this run began is not a piece of activity.
func TestTheSessionMarkerIsNotCounted(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))

	m.record(append(spread(3, now), gh.SessionEvent(now)))
	if got := m.shownEvents(); got != 3 {
		t.Errorf("counted %d events, want the three real ones", got)
	}
	if got := m.oldestEvent(); !got.Equal(now.Add(-30 * time.Minute)) {
		t.Errorf("oldest is %s, want the oldest real event", got)
	}
}

// How far the searches were told to look and how far the feed reaches are
// different spans once a saved record is involved, so the waiting message
// names the gap actually being covered.
func TestTheWaitingMessageNamesTheRealGap(t *testing.T) {
	now := time.Now()

	// A fresh watermark: twenty minutes to catch up on, not the seed window.
	short := remembering(t, 720*time.Hour, nil, now.Add(-20*time.Minute))
	short.showEvents = true
	out := feedText(short)
	if strings.Contains(out, "720h") {
		t.Errorf("the message claims the whole seed window: %s", out)
	}
	if !strings.Contains(out, "20m") {
		t.Errorf("the message does not name the real gap: %s", out)
	}

	// With nothing saved, the gap is the seed window and saying so is right.
	full := remembering(t, 6*time.Hour, nil, time.Time{})
	full.showEvents = true
	if out := feedText(full); !strings.Contains(out, "6h") {
		t.Errorf("the message does not name the gap it is covering: %s", out)
	}
}

// The searches stop once there is enough to look at; the feed holds far more,
// because the saved record fills it over time and there is no reason to throw
// most of that away on the way in.
func TestTheFeedHoldsMoreThanTheSearchesGather(t *testing.T) {
	if maxEvents <= backfillEnough {
		t.Errorf("the feed caps at %d and the searches stop at %d; the record cannot accumulate",
			maxEvents, backfillEnough)
	}
}
