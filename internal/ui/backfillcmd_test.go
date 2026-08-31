package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// recordingServer answers every backfill search with the same payload and
// remembers what was asked. Returning the same pull request to both searches is
// the point: they overlap in reality, and the overlap must not be seeded twice.
type recordingServer struct {
	mu      sync.Mutex
	queries []string
}

func (r *recordingServer) start(t *testing.T, payload string) *gh.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Variables struct {
				Q string `json:"q"`
			} `json:"variables"`
		}
		json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.queries = append(r.queries, body.Variables.Q)
		r.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	return c
}

func (r *recordingServer) asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...)
}

// onePR is a response with a single merged pull request, dated recently enough
// that any sane window reaches it.
func onePR(t *testing.T) string {
	t.Helper()
	now := time.Now().UTC()
	at := func(d time.Duration) string { return now.Add(-d).Format(time.RFC3339) }
	doc := map[string]any{
		"data": map[string]any{
			"viewer":    map[string]any{"login": "jack"},
			"rateLimit": map[string]any{"limit": 5000, "remaining": 4900, "cost": 17},
			"search": map[string]any{
				"issueCount": 1,
				"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": ""},
				"nodes": []any{map[string]any{
					"__typename": "PullRequest",
					"id":         "PR_1", "number": 99, "title": "t",
					"url":        "https://example.invalid/99",
					"state":      "MERGED",
					"createdAt":  at(3 * time.Hour),
					"updatedAt":  at(time.Hour),
					"mergedAt":   at(time.Hour),
					"closedAt":   at(time.Hour),
					"mergeable":  "MERGEABLE",
					"repository": map[string]any{"nameWithOwner": "acme/hyperspace"},
					"author":     map[string]any{"login": "jbonatakis"},
					"comments": map[string]any{"totalCount": 1, "nodes": []any{
						map[string]any{
							"author":    map[string]any{"login": "brianmuse"},
							"createdAt": at(2 * time.Hour), "bodyText": "looks good",
						},
					}},
					"reviews": map[string]any{"nodes": []any{
						map[string]any{
							"state": "APPROVED", "author": map[string]any{"login": "brianmuse"},
							"submittedAt": at(90 * time.Minute), "bodyText": "",
						},
					}},
				}},
			},
		},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// runBackfill drives the real thing: the producer fans out across its pool
// while the model drains the answers one at a time, exactly as the event loop
// does, and returns the model plus every chunk that arrived.
func runBackfill(t *testing.T, c *gh.Client, window time.Duration) (Model, []backfillChunkMsg) {
	t.Helper()
	isolateConfig(t)
	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: window, Watch: gh.AllShapes,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	go m.runBackfill()()

	var chunks []backfillChunkMsg
	done := time.After(20 * time.Second)
	for {
		type result struct{ msg tea.Msg }
		got := make(chan result, 1)
		go func(await tea.Cmd) { got <- result{await()} }(m.awaitBackfill())

		select {
		case r := <-got:
			if _, finished := r.msg.(backfillDoneMsg); finished {
				return m.applyBackfill(backfillDoneMsg{}), chunks
			}
			chunk := r.msg.(backfillChunkMsg)
			chunks = append(chunks, chunk)
			m = m.applyBackfillChunk(chunk)
		case <-done:
			t.Fatal("the backfill never finished")
		}
	}
}

// The dashboard is on authored mode here, and the backfill must still look
// wider than that — otherwise a morning spent reviewing other people's work
// reconstructs as an empty feed.
func TestTheBackfillIsNotScopedToTheDashboardMode(t *testing.T) {
	srv := &recordingServer{}
	// One window's worth, so the two searches are the two shapes.
	runBackfill(t, srv.start(t, onePR(t)), 20*time.Minute)

	asked := srv.asked()
	if len(asked) != len(gh.AllShapes) {
		t.Fatalf("ran %d searches: %q", len(asked), asked)
	}
	joined := strings.Join(asked, " || ")
	for _, want := range []string{"involves:@me", "review-requested:@me"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the backfill never searched %s: %q", want, asked)
		}
	}
	if strings.Contains(joined, "author:@me") {
		t.Errorf("the backfill inherited the dashboard's mode: %q", asked)
	}
	for _, q := range asked {
		if !strings.Contains(q, "updated:>=") {
			t.Errorf("unbounded search would fetch pull requests that cannot contribute: %q", q)
		}
	}
}

// The two searches overlap by design, so anything found by both must be seeded
// once.
func TestOverlappingSearchesSeedEachPullRequestOnce(t *testing.T) {
	srv := &recordingServer{}
	// Long enough to be chunked, so the same pull request comes back from
	// several searches at once.
	m, chunks := runBackfill(t, srv.start(t, onePR(t)), 24*time.Hour)

	if len(chunks) < 2 {
		t.Fatalf("expected several searches, got %d", len(chunks))
	}
	if len(m.events) == 0 {
		t.Fatal("the backfill seeded nothing")
	}

	counts := map[string]int{}
	for _, e := range m.events {
		if e.Kind == gh.EventSessionStart {
			continue
		}
		counts[e.Kind.Icon()+e.Text+e.Actor]++
	}
	for what, n := range counts {
		if n > 1 {
			t.Errorf("%q was seeded %d times; every search returned the same pull request", what, n)
		}
	}

	var kinds []string
	for _, e := range m.events {
		kinds = append(kinds, e.Text)
	}
	for _, want := range []string{"opened", "merged", "approved", "new comment"} {
		var found bool
		for _, k := range kinds {
			if k == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the backfill did not seed %q; got %q", want, kinds)
		}
	}
}

// The searches run at once rather than one after another. Timing is the only
// way to tell, so the server holds every request until enough have arrived
// together — which never happens if they are issued serially.
func TestTheSearchesRunInParallel(t *testing.T) {
	isolateConfig(t)
	payload := onePR(t)

	const want = backfillWorkers
	var (
		mu             sync.Mutex
		inFlight, peak int
	)
	gate := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		reached := inFlight >= want
		mu.Unlock()

		if reached {
			// Release everyone the moment a full pool is waiting together.
			select {
			case <-gate:
			default:
				close(gate)
			}
		}
		select {
		case <-gate:
		case <-time.After(5 * time.Second):
		}

		mu.Lock()
		inFlight--
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	runBackfill(t, c, 24*time.Hour) // 4 windows x 2 shapes = 8 searches

	mu.Lock()
	defer mu.Unlock()
	if peak < 2 {
		t.Errorf("peak concurrency was %d; the searches ran one at a time", peak)
	}
	if peak > backfillWorkers {
		t.Errorf("peak concurrency was %d, above the pool of %d", peak, backfillWorkers)
	}
}

// One search failing still leaves the other's findings worth showing.
func TestOneFailedSearchDoesNotLoseTheOther(t *testing.T) {
	isolateConfig(t)
	payload := onePR(t)

	var n int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		first := n == 1
		mu.Unlock()
		if first {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("upstream had a moment"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL

	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: 720 * time.Hour, Watch: gh.AllShapes,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	go m.runBackfill()()
	var events int
	for {
		msg := m.awaitBackfill()()
		if _, done := msg.(backfillDoneMsg); done {
			break
		}
		m = m.applyBackfillChunk(msg.(backfillChunkMsg))
		events = len(m.events)
	}
	if events == 0 {
		t.Error("the surviving search's findings were thrown away")
	}
	if m = m.applyBackfill(backfillDoneMsg{}); m.err != nil {
		t.Errorf("a half-successful backfill took the dashboard down: %v", m.err)
	}
}

// A complete washout is a failure, and has to say so rather than pass for a
// quiet window.
func TestBothSearchesFailingIsReportedAsAnError(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte("upstream had a moment"))
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL

	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: time.Hour, Watch: gh.AllShapes,
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
	m = m.applyBackfill(backfillDoneMsg{})

	if !m.seedFailed {
		t.Error("every search failed and the backfill called it a success")
	}
	if !strings.Contains(m.toast, "could not fill the feed in") {
		t.Errorf("nothing told the user; toast is %q", m.toast)
	}
}
