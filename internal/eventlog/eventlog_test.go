package eventlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

func scratch(t *testing.T) *Log {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	l, err := Open()
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return l
}

func ev(text string, at time.Time) gh.Event {
	return gh.Event{
		At: at, Kind: gh.EventComment, Key: "acme/hyperspace#99",
		Repo: "acme/hyperspace", Number: 99, Text: text, Actor: "brianmuse",
		URL: "https://example.invalid/99",
	}
}

func TestARoundTripKeepsEverythingTheFeedShows(t *testing.T) {
	l := scratch(t)
	at := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	want := gh.Event{
		At: at, Kind: gh.EventMention, Key: "acme/rfcs#9", Repo: "acme/rfcs",
		Number: 9, Text: "mentioned you", Actor: "dana-quill",
		URL: "https://example.invalid/9",
	}

	if err := l.Append([]gh.Event{want}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := l.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("read back %d events", len(got))
	}
	if !got[0].At.Equal(want.At) {
		t.Errorf("At = %s, want %s", got[0].At, want.At)
	}
	got[0].At = want.At // compared above; time.Time is not == comparable by wall clock
	if got[0] != want {
		t.Errorf("read back %+v, want %+v", got[0], want)
	}
}

// Kinds are stored by name. Were they stored by number, adding a constant in
// the middle of the list would silently relabel every event already on disk.
func TestKindsSurviveTheirConstantsMoving(t *testing.T) {
	l := scratch(t)
	at := time.Now().UTC().Truncate(time.Second)
	for _, k := range []gh.EventKind{
		gh.EventOpened, gh.EventMerged, gh.EventMention,
		gh.EventReviewRequested, gh.EventChecks,
	} {
		e := ev("x", at)
		e.Kind = k
		if err := l.Append([]gh.Event{e}); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"kind":"opened"`, `"kind":"merged"`, `"kind":"mention"`, `"kind":"review-requested"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the log does not name its kinds: want %s", want)
		}
	}
}

// The log is appended to by a program that can be killed mid-write, so a torn
// final line is the expected shape of damage. Losing the whole record over one
// bad line would be a far worse answer than losing the last event.
func TestATornLineDoesNotRuinTheLog(t *testing.T) {
	l := scratch(t)
	at := time.Now().UTC().Truncate(time.Second)
	if err := l.Append([]gh.Event{ev("first", at), ev("second", at.Add(time.Minute))}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(l.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"at":"2026-08-28T10:00:00Z","kind":"comm`)
	f.Close()

	got, err := l.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("read back %d events, want the two intact ones", len(got))
	}
}

// A kind written by a newer ghpr is skipped rather than guessed at.
func TestAnUnknownKindIsSkipped(t *testing.T) {
	l := scratch(t)
	if err := os.MkdirAll(filepath.Dir(l.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"at":"2026-08-28T10:00:00Z","kind":"teleported","text":"?"}` + "\n" +
		`{"at":"2026-08-28T10:01:00Z","kind":"comment","text":"real"}` + "\n"
	if err := os.WriteFile(l.Path(), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := l.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "real" {
		t.Errorf("read back %+v, want only the line it understands", got)
	}
}

// The mark of where one run began means nothing in the next.
func TestTheSessionMarkerIsNotKept(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := l.Append([]gh.Event{ev("real", now), gh.SessionEvent(now)}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Load()
	for _, e := range got {
		if e.Kind == gh.EventSessionStart {
			t.Error("a session marker was kept for the next run to be confused by")
		}
	}
	if len(got) != 1 {
		t.Errorf("read back %d events, want just the real one", len(got))
	}
}

// The watermark is a separate claim from the newest event: a quiet hour before
// the last run ended leaves the newest event an hour old, and resuming from it
// would skip an hour nobody looked at.
func TestTheWatermarkIsNotTheNewestEvent(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)

	if got := l.Watermark(); !got.IsZero() {
		t.Errorf("a fresh log claims coverage up to %s", got)
	}
	if err := l.Append([]gh.Event{ev("old", now.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if got := l.Watermark(); !got.IsZero() {
		t.Error("appending an event moved the watermark")
	}

	if err := l.SetWatermark(now); err != nil {
		t.Fatalf("set watermark: %v", err)
	}
	if got := l.Watermark(); !got.Equal(now) {
		t.Errorf("watermark = %s, want %s", got, now)
	}
}

func TestPrepareDropsWhatHasAgedOut(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := l.Append([]gh.Event{
		ev("ancient", now.Add(-120*24*time.Hour)),
		ev("recent", now.Add(-time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	kept, dropped, err := l.Prepare(now)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped %d, want the one that aged out", dropped)
	}
	if len(kept) != 1 || kept[0].Text != "recent" {
		t.Errorf("kept %+v", kept)
	}
	got, _ := l.Load()
	if len(got) != 1 || got[0].Text != "recent" {
		t.Errorf("the file still holds %+v", got)
	}
}

func TestPrepareRewritesNothingWhenNothingHasAgedOut(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := l.Append([]gh.Event{ev("recent", now.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	if _, dropped, err := l.Prepare(now); err != nil || dropped != 0 {
		t.Errorf("prepare dropped %d (%v), want nothing", dropped, err)
	}
}

// A record of which pull requests someone watches is nobody else's business,
// even on a shared machine.
func TestTheLogIsPrivate(t *testing.T) {
	l := scratch(t)
	if err := l.Append([]gh.Event{ev("x", time.Now())}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetWatermark(time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{eventsFile, watermarkFile} {
		info, err := os.Stat(filepath.Join(l.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s is mode %04o, want 0600", name, perm)
		}
	}
}

// A first run has nothing to read, which is not a failure.
func TestAFirstRunReadsNothingQuietly(t *testing.T) {
	l := scratch(t)
	got, err := l.Load()
	if err != nil {
		t.Errorf("a missing log was an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("read %d events from nowhere", len(got))
	}
}

func TestLoadReturnsOldestFirst(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	// Appended out of order, as two sessions writing at different times would.
	if err := l.Append([]gh.Event{ev("newer", now), ev("older", now.Add(-time.Hour))}); err != nil {
		t.Fatal(err)
	}
	got, _ := l.Load()
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("out of order at %d: %q then %q", i, got[i-1].Text, got[i].Text)
		}
	}
}

// One comment can reach the log twice: once dated by the poll that noticed it,
// once dated truly by a later backfill over the same stretch. Left alone they
// read as two comments seconds apart, and the file fills with the pairs.
func TestCompactDropsPollDatedLinesTheBackfillHasRedone(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-time.Hour)

	observed := func(text string, at time.Time) gh.Event {
		e := ev(text, at)
		e.Observed = true
		return e
	}

	if err := l.Append([]gh.Event{
		observed("polled, inside the gap", now.Add(-30*time.Minute)),
		observed("polled, before the gap", now.Add(-3*time.Hour)),
		ev("reconstructed, inside the gap", now.Add(-20*time.Minute)),
		ev("reconstructed, before the gap", now.Add(-4*time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}

	dropped, err := l.Compact(since, now)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped %d lines, want the one poll-dated line inside the gap", dropped)
	}

	kept, _ := l.Load()
	var texts []string
	for _, e := range kept {
		texts = append(texts, e.Text)
	}
	has := func(want string) bool {
		for _, got := range texts {
			if got == want {
				return true
			}
		}
		return false
	}
	for _, want := range []string{
		"polled, before the gap",        // outside the gap: nothing will redo it
		"reconstructed, inside the gap", // already carries GitHub's own timestamp
		"reconstructed, before the gap",
	} {
		if !has(want) {
			t.Errorf("prepare dropped %q, which nothing is going to replace", want)
		}
	}
	if has("polled, inside the gap") {
		t.Error("a poll-dated line the backfill is about to redo was kept")
	}
}

// Three runs over one comment, which is how the duplicate actually arises.
func TestOneCommentEndsUpOnFileOnce(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	trueTime := now.Add(-3 * time.Hour)

	comment := func(at time.Time, observed bool) gh.Event {
		e := ev("1 new comment", at)
		e.Observed = observed
		return e
	}

	// Run 1 notices it by polling, twenty seconds late.
	l.Append([]gh.Event{comment(trueTime.Add(20*time.Second), true)})

	// Run 2's backfill covers that stretch and reports the real moment...
	l.Append([]gh.Event{comment(trueTime, false)})
	// ...and only then does the approximate copy go.
	if _, err := l.Compact(trueTime.Add(-time.Minute), now); err != nil {
		t.Fatal(err)
	}

	// Run 3 loads the log.
	got, _ := l.Load()
	if len(got) != 1 {
		t.Fatalf("one comment is on file %d times: %+v", len(got), got)
	}
	if !got[0].At.Equal(trueTime) {
		t.Errorf("the surviving line is dated %s, want the real moment %s", got[0].At, trueTime)
	}
}

// Compaction is a bet on a reconstruction that has already happened. Doing it
// up front would stake the only copy of an event on a backfill that had not
// run yet, and a failed one would take it with it.
func TestNothingIsDroppedBeforeTheBackfillHasCoveredIt(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)

	seen := ev("noticed by a poll", now.Add(-30*time.Minute))
	seen.Observed = true
	if err := l.Append([]gh.Event{seen}); err != nil {
		t.Fatal(err)
	}

	// Startup only ages the file out; it knows nothing yet about what the
	// backfill will manage to gather.
	kept, _, err := l.Prepare(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("startup dropped a line before anything had replaced it: %+v", kept)
	}
	if onDisk, _ := l.Load(); len(onDisk) != 1 {
		t.Fatalf("startup rewrote the file without a replacement in hand: %+v", onDisk)
	}
}

// And a stretch nothing covered keeps its poll-dated lines, because nothing is
// coming to replace them.
func TestCompactLeavesWhatIsOutsideTheCoveredStretch(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)

	older := ev("well before the gap", now.Add(-5*time.Hour))
	older.Observed = true
	inside := ev("inside the gap", now.Add(-10*time.Minute))
	inside.Observed = true
	if err := l.Append([]gh.Event{older, inside}); err != nil {
		t.Fatal(err)
	}

	dropped, err := l.Compact(now.Add(-time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Errorf("dropped %d, want only the line inside the covered stretch", dropped)
	}
	got, _ := l.Load()
	if len(got) != 1 || got[0].Text != "well before the gap" {
		t.Errorf("kept %+v, want the line nothing is going to replace", got)
	}
}

func TestCompactOverNoStretchDoesNothing(t *testing.T) {
	l := scratch(t)
	now := time.Now().UTC().Truncate(time.Second)
	e := ev("x", now.Add(-time.Minute))
	e.Observed = true
	l.Append([]gh.Event{e})

	if dropped, err := l.Compact(now, now); err != nil || dropped != 0 {
		t.Errorf("compact over an empty stretch dropped %d (%v)", dropped, err)
	}
	if got, _ := l.Load(); len(got) != 1 {
		t.Errorf("the log lost a line to a compaction of nothing: %+v", got)
	}
}
