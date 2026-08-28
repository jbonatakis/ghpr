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

func runBackfill(t *testing.T, c *gh.Client, window time.Duration) backfillDoneMsg {
	t.Helper()
	isolateConfig(t)
	m := New(Config{
		Client: c, Mode: gh.ModeAuthored, Interval: 30 * time.Second,
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: window,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	msg, ok := m.backfillCmd()().(backfillDoneMsg)
	if !ok {
		t.Fatal("backfillCmd did not return a backfillDoneMsg")
	}
	return msg
}

// The dashboard is on authored mode here, and the backfill must still look
// wider than that — otherwise a morning spent reviewing other people's work
// reconstructs as an empty feed.
func TestTheBackfillIsNotScopedToTheDashboardMode(t *testing.T) {
	srv := &recordingServer{}
	runBackfill(t, srv.start(t, onePR(t)), 720*time.Hour)

	asked := srv.asked()
	if len(asked) != 2 {
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
	msg := runBackfill(t, srv.start(t, onePR(t)), 720*time.Hour)

	if msg.err != nil {
		t.Fatalf("backfill: %v", msg.err)
	}
	if len(msg.events) == 0 {
		t.Fatal("the backfill seeded nothing")
	}

	counts := map[string]int{}
	for _, e := range msg.events {
		counts[e.Kind.Icon()+e.Text+e.Actor]++
	}
	for what, n := range counts {
		if n > 1 {
			t.Errorf("%q was seeded %d times; the two searches returned the same pull request", what, n)
		}
	}

	// And what it found is what one pull request carries.
	var kinds []string
	for _, e := range msg.events {
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
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: 720 * time.Hour,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	msg := m.backfillCmd()().(backfillDoneMsg)
	if msg.err != nil {
		t.Errorf("a half-successful backfill reported failure: %v", msg.err)
	}
	if len(msg.events) == 0 {
		t.Error("the surviving search's findings were thrown away")
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
		Max: 200, Prefs: config.Defaults(), Links: true, Seed: time.Hour,
	})
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})

	msg := m.backfillCmd()().(backfillDoneMsg)
	if msg.err == nil {
		t.Error("both searches failed and the backfill called it a success")
	}
}
