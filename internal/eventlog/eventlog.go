// Package eventlog keeps the activity feed across runs.
//
// The feed is a record of things that happened, and things that happened do not
// stop being true when the dashboard exits. Holding them only in memory meant
// every launch began by asking GitHub to reconstruct a past it had already been
// told about, and asking badly: one search can see the newest twenty comments
// on a pull request and nothing older, so a month reconstructed in one go is
// always thinner than a month watched. A log that accumulates while ghpr runs
// has neither limit.
//
// Only the feed is kept. The pull request list is live state, and a dashboard
// showing yesterday's statuses would be worse than one that takes a moment.
package eventlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// Log is the on-disk activity record.
//
// It is two files. The events are a JSON Lines append log, which survives a
// torn write — a half-written final line is one unparseable line rather than a
// ruined file — and can be added to without rewriting what is already there.
// The watermark is separate because it is rewritten constantly and the events
// are never rewritten at all.
type Log struct {
	dir string
}

const (
	eventsFile    = "events.jsonl"
	watermarkFile = "watermark.json"

	// Retention. Generous, because the whole point is a record denser than any
	// single backfill, and a line of JSON is small.
	maxLines = 20000
	maxAge   = 90 * 24 * time.Hour
)

// Dir is where the log lives: state rather than cache, because it is a record
// worth keeping, and not config, because nobody edits it by hand.
func Dir() (string, error) {
	if x := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); x != "" {
		return filepath.Join(x, "ghpr"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "ghpr"), nil
}

// Open prepares the log. It does not create anything: a first run has nothing
// to read, which is not a failure.
func Open() (*Log, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return &Log{dir: dir}, nil
}

func (l *Log) Path() string { return filepath.Join(l.dir, eventsFile) }

// stored is one line of the log.
type stored struct {
	At     time.Time    `json:"at"`
	Kind   gh.EventKind `json:"kind"`
	Key    string       `json:"key,omitempty"`
	Repo   string       `json:"repo,omitempty"`
	Number int          `json:"number,omitempty"`
	Text   string       `json:"text"`
	Actor  string       `json:"actor,omitempty"`
	URL    string       `json:"url,omitempty"`
}

func toStored(e gh.Event) stored {
	return stored{
		At: e.At, Kind: e.Kind, Key: e.Key, Repo: e.Repo,
		Number: e.Number, Text: e.Text, Actor: e.Actor, URL: e.URL,
	}
}

func (s stored) event() gh.Event {
	return gh.Event{
		At: s.At, Kind: s.Kind, Key: s.Key, Repo: s.Repo,
		Number: s.Number, Text: s.Text, Actor: s.Actor, URL: s.URL,
	}
}

// Load reads the saved feed, oldest first.
//
// A line that will not parse is skipped rather than fatal. The log is appended
// to by a program that can be killed at any moment, and one bad line at the end
// is the expected shape of that — losing the whole record over it would be a
// far worse answer than losing the last event.
func (l *Log) Load() ([]gh.Event, error) {
	f, err := os.Open(l.Path())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // a first run has nothing to read
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []gh.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var s stored
		if err := json.Unmarshal(line, &s); err != nil {
			continue // a torn or unrecognised line, not a ruined log
		}
		if s.At.IsZero() || s.Text == "" {
			continue
		}
		out = append(out, s.event())
	}
	if err := sc.Err(); err != nil {
		// Whatever was read before the trouble is still worth having.
		return out, err
	}
	gh.SortByTime(out)
	return out, nil
}

// Append adds events to the log, creating it if this is the first time.
func (l *Log) Append(events []gh.Event) error {
	if len(events) == 0 {
		return nil
	}
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	// 0600: a record of which pull requests someone watches is nobody else's
	// business, even on a shared machine.
	f, err := os.OpenFile(l.Path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, e := range events {
		if e.Kind == gh.EventSessionStart {
			continue // a mark of where one run began means nothing in the next
		}
		line, err := json.Marshal(toStored(e))
		if err != nil {
			continue // an event that cannot be written is not worth failing over
		}
		w.Write(line)
		w.WriteByte('\n')
	}
	return w.Flush()
}

// Watermark is the point up to which the feed is known to be complete.
//
// It is recorded separately rather than inferred from the newest event, because
// those are different claims: a quiet hour before the last run ended leaves the
// newest event an hour old, and resuming from there would skip over an hour
// nobody had actually looked at.
func (l *Log) Watermark() time.Time {
	raw, err := os.ReadFile(filepath.Join(l.dir, watermarkFile))
	if err != nil {
		return time.Time{}
	}
	var doc struct {
		CoveredUntil time.Time `json:"coveredUntil"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return time.Time{}
	}
	return doc.CoveredUntil
}

// SetWatermark records how far the feed is complete. Only a backfill that
// finished may move it: a poll sees one search's worth of pull requests, which
// is narrower than the feed's scope, so claiming its coverage would leave holes
// the next run would never think to fill.
func (l *Log) SetWatermark(at time.Time) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		CoveredUntil time.Time `json:"coveredUntil"`
	}{at})
	if err != nil {
		return err
	}
	path := filepath.Join(l.dir, watermarkFile)
	tmp, err := os.CreateTemp(l.dir, ".watermark-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Trim rewrites the log without what has aged out of it, and reports how many
// lines were dropped. Called once at startup, where a rewrite costs nothing
// anyone is waiting on.
func (l *Log) Trim(now time.Time) (int, error) {
	events, err := l.Load()
	if err != nil && len(events) == 0 {
		return 0, err
	}
	cutoff := now.Add(-maxAge)
	kept := make([]gh.Event, 0, len(events))
	for _, e := range events {
		if e.At.Before(cutoff) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) > maxLines {
		kept = kept[len(kept)-maxLines:]
	}
	dropped := len(events) - len(kept)
	if dropped <= 0 {
		return 0, nil
	}

	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(l.dir, ".events-*.jsonl")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	w := bufio.NewWriter(tmp)
	for _, e := range kept {
		line, err := json.Marshal(toStored(e))
		if err != nil {
			continue
		}
		w.Write(line)
		w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return 0, err
	}
	return dropped, os.Rename(tmp.Name(), l.Path())
}
