package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// manyPRs is a response carrying n distinct pull requests, each with enough
// dated history to seed several events.
// batch is offset so that successive searches return different pull requests;
// with identical ones the de-duplication would cap what can ever be seeded and
// the cap under test would never be reached.
func manyPRs(t *testing.T, n, batch int) string {
	t.Helper()
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }

	nodes := make([]any, 0, n)
	for j := 0; j < n; j++ {
		i := batch*n + j
		nodes = append(nodes, map[string]any{
			"__typename": "PullRequest",
			"id":         "PR_" + itoa(i), "number": i + 1, "title": "t",
			"url":        "https://example.invalid/" + itoa(i),
			"createdAt":  at(time.Duration(i+1) * time.Minute),
			"updatedAt":  at(time.Duration(i) * time.Minute),
			"mergeable":  "MERGEABLE",
			"repository": map[string]any{"nameWithOwner": "acme/many"},
			"author":     map[string]any{"login": "jbonatakis"},
			"comments": map[string]any{"totalCount": 2, "nodes": []any{
				map[string]any{"author": map[string]any{"login": "brianmuse"},
					"createdAt": at(time.Duration(i+2) * time.Minute), "bodyText": "a"},
				map[string]any{"author": map[string]any{"login": "brianmuse"},
					"createdAt": at(time.Duration(i+3) * time.Minute), "bodyText": "b"},
			}},
		})
	}
	raw, err := json.Marshal(map[string]any{
		"data": map[string]any{
			"viewer":    map[string]any{"login": "jack"},
			"rateLimit": map[string]any{"limit": 5000, "remaining": 4900, "cost": 17},
			"search": map[string]any{
				"issueCount": n,
				"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": ""},
				"nodes":      nodes,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// The backlog keeps only maxEvents, and chunks arrive newest first, so once it
// is full everything still queued is older than what is already filed and would
// be trimmed away on arrival. Asking GitHub for it is work nobody will see.
func TestTheBackfillStopsOnceTheBacklogIsFull(t *testing.T) {
	isolateConfig(t)

	var (
		mu     sync.Mutex
		served int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		batch := served
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(manyPRs(t, 120, batch)))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL

	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: 720 * time.Hour,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	go m.runBackfill()()
	for {
		msg := m.awaitBackfill()()
		if _, done := msg.(backfillDoneMsg); done {
			break
		}
		m = m.applyBackfillChunk(msg.(backfillChunkMsg))
	}

	if !m.backfillStopped {
		t.Fatalf("the backfill ran to the end with %d events filed", m.backfillFound)
	}
	if m.backfillFound < maxEvents {
		t.Errorf("stopped early with only %d events", m.backfillFound)
	}

	// A month is planned as twelve searches. Stopping should spend well under
	// that, though the pool means a few land after the cap is reached.
	mu.Lock()
	defer mu.Unlock()
	if served >= 12 {
		t.Errorf("served %d searches; the cap saved nothing", served)
	}
	t.Logf("filled the backlog after %d of 12 searches", served)
}

// With little to find, the cap never trips and every window is searched.
func TestASmallBackfillStillSearchesEveryWindow(t *testing.T) {
	isolateConfig(t)

	var (
		mu     sync.Mutex
		served int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		batch := served
		served++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(manyPRs(t, 1, batch)))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL

	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: 720 * time.Hour,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	go m.runBackfill()()
	for {
		msg := m.awaitBackfill()()
		if _, done := msg.(backfillDoneMsg); done {
			break
		}
		m = m.applyBackfillChunk(msg.(backfillChunkMsg))
	}

	if m.backfillStopped {
		t.Error("a backfill that found almost nothing gave up early")
	}
	mu.Lock()
	defer mu.Unlock()
	if served != 12 {
		t.Errorf("served %d searches, want all 12 windows", served)
	}
}
