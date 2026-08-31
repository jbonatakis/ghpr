package ui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/eventlog"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// remembering returns a model that starts with a saved feed behind it.
func remembering(t *testing.T, seed time.Duration, cached []gh.Event, watermark time.Time) Model {
	t.Helper()
	isolateConfig(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	log, err := eventlog.Open()
	if err != nil {
		t.Fatal(err)
	}
	m := New(Config{
		Client: gh.NewClient("test"), Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: seed,
		Log: log, Cached: cached, Watermark: watermark,
	})
	return update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
}

func cachedEvent(text string, at time.Time) gh.Event {
	return gh.Event{
		At: at, Kind: gh.EventComment, Key: "acme/hyperspace#99",
		Repo: "acme/hyperspace", Number: 99, Text: text, Actor: "brianmuse",
		URL: "https://example.invalid/99",
	}
}

// The saved feed is older than the gap being filled, so it is the oldest window
// of all and goes in behind every search. Painting it at startup instead would
// mean newer activity landing on top of it moments later — the feed jumping,
// which is the one thing it must not do.
func TestTheSavedFeedLandsUnderTheSearches(t *testing.T) {
	now := time.Now()
	watermark := now.Add(-2 * time.Hour)
	cached := []gh.Event{
		cachedEvent("yesterday", now.Add(-20*time.Hour)),
		cachedEvent("last night", now.Add(-9*time.Hour)),
	}
	m := remembering(t, 24*time.Hour, cached, watermark)

	// Nothing is on screen before the searches answer, not even what is
	// already in hand.
	if len(m.events) != 0 {
		t.Fatalf("the saved feed was painted before the gap was filled: %d events", len(m.events))
	}

	// The saved record arrives as the window behind the planned ones.
	last := len(gh.BackfillSearches("", watermark, now)) / gh.BackfillShapes
	m = m.applyBackfillChunk(backfillChunkMsg{
		window: last, needs: 1, cached: cached, since: watermark,
	})
	if len(m.events) != 0 {
		t.Fatal("the saved feed jumped the queue")
	}

	// Only once every search window has reported does any of it appear.
	for w := 0; w < last; w++ {
		m = finish(m, w, watermark, windowPR(w+1, now.Add(-time.Duration(w+1)*time.Minute)))
	}

	if len(m.events) == 0 {
		t.Fatal("nothing was filed at all")
	}
	var seenCached bool
	for _, e := range m.events {
		if e.Text == "yesterday" {
			seenCached = true
		}
	}
	if !seenCached {
		t.Error("the saved feed never made it in")
	}
	for i := 1; i < len(m.events); i++ {
		if m.events[i].At.Before(m.events[i-1].At) {
			t.Fatalf("the feed is out of order at %d", i)
		}
	}
	// And the newest thing on screen came from the searches, not the record.
	if top := m.events[len(m.events)-1]; top.Kind != gh.EventSessionStart {
		t.Errorf("the feed opens with %q, not the session marker", top.Text)
	}
}

// The point of remembering: a dashboard reopened moments after it closed has
// almost nothing to catch up on.
func TestAFreshWatermarkShrinksTheGap(t *testing.T) {
	now := time.Now()
	m := remembering(t, 24*time.Hour, nil, now.Add(-20*time.Minute))

	since := m.backfillSince()
	if gap := m.startedAt.Sub(since); gap > 25*time.Minute {
		t.Errorf("the gap is %s; the record already covers all but twenty minutes", gap)
	}
	// One window of searching rather than a day of it.
	planned := len(gh.BackfillSearches("", since, m.startedAt))
	full := len(gh.BackfillSearches("", m.startedAt.Add(-24*time.Hour), m.startedAt))
	if planned >= full {
		t.Errorf("planned %d searches against %d for the whole window", planned, full)
	}
}

// Away longer than the seed window, the gap is the seed window: the record
// reaching further back does not license an unbounded catch-up.
func TestAnOldWatermarkIsClampedToTheSeedWindow(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*24*time.Hour))

	if gap := m.startedAt.Sub(m.backfillSince()); gap > 65*time.Minute {
		t.Errorf("the gap is %s, want it held to the seed window", gap)
	}
}

func TestNoRecordMeansTheFullSeedWindow(t *testing.T) {
	m := remembering(t, 6*time.Hour, nil, time.Time{})

	if gap := m.startedAt.Sub(m.backfillSince()); gap < 5*time.Hour {
		t.Errorf("the gap is %s, want the whole seed window", gap)
	}
}

// Replayed events came off disk; writing them back would append what is
// already there, a duplicate every launch until the file was all one afternoon.
func TestReplayedEventsAreNotWrittenBack(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))

	m.replay([]gh.Event{cachedEvent("from disk", now.Add(-2*time.Hour))})
	if len(m.unsaved) != 0 {
		t.Errorf("%d replayed events were queued to be written again", len(m.unsaved))
	}

	m.record([]gh.Event{cachedEvent("just happened", now)})
	if len(m.unsaved) != 1 {
		t.Errorf("%d events queued, want the newly observed one", len(m.unsaved))
	}
}

// The marker says where this run began. It means nothing in the next one.
func TestTheSessionMarkerIsNeverQueuedForDisk(t *testing.T) {
	now := time.Now()
	m := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))

	m.record([]gh.Event{gh.SessionEvent(now), cachedEvent("real", now)})
	for _, e := range m.unsaved {
		if e.Kind == gh.EventSessionStart {
			t.Error("the session marker was queued for the record")
		}
	}
	if len(m.unsaved) != 1 {
		t.Errorf("queued %d events, want just the real one", len(m.unsaved))
	}
}

// Coverage is claimed by writing the watermark, and a run that could not
// gather the window has covered nothing. Claiming it anyway would tell the next
// run to skip a gap nobody ever looked at.
func TestOnlyASuccessfulBackfillClaimsCoverage(t *testing.T) {
	now := time.Now()

	clean := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))
	cmd := clean.saveWatermark()
	if cmd == nil {
		t.Fatal("a clean run offered to claim no coverage")
	}
	cmd()
	if got := clean.cfg.Log.Watermark(gh.BackfillScope("")); !got.Equal(clean.startedAt) {
		t.Errorf("claimed coverage up to %s, want the moment this run began (%s)",
			got, clean.startedAt)
	}

	failed := remembering(t, time.Hour, nil, now.Add(-30*time.Minute))
	failed = failed.applyBackfillChunk(backfillChunkMsg{
		window: 0, needs: gh.BackfillShapes, err: &gh.TransientError{Detail: "502"},
	})
	if !failed.seedFailed {
		t.Fatal("the failure went unrecorded")
	}
	if failed.saveWatermark() != nil {
		t.Error("a failed backfill offered to claim coverage it does not have")
	}
}

// Turning it off leaves the feed exactly as it was: in memory, for this run.
func TestWithoutALogNothingIsRememberedOrExpected(t *testing.T) {
	m := newSeeding(t, time.Hour)
	if m.cfg.Log != nil {
		t.Fatal("a log appeared without being asked for")
	}
	m.record([]gh.Event{cachedEvent("x", time.Now())})
	if cmd := m.takeSaved(); cmd != nil {
		t.Error("something was written with no log configured")
	}
	if cmd := m.saveWatermark(); cmd != nil {
		t.Error("coverage was claimed with no log configured")
	}
}
