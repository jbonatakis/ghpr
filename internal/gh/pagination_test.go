package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

func loadDoc(t *testing.T, file string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestReviewRequestedFixtureParses(t *testing.T) {
	c := serveTestdata(t, "search_review_requested.json", func(doc map[string]any) {
		// One page only, so the fetch terminates on this fixture.
		doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
			map[string]any{"hasNextPage": false, "endCursor": ""}
	})
	res, err := c.Fetch(context.Background(), ModeReviewRequested.Query(""), 200)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(res.PRs) != 25 {
		t.Fatalf("PRs = %d, want the 25 on this page", len(res.PRs))
	}

	// These are other people's pull requests, unlike the authored fixture.
	var mine int
	for _, p := range res.PRs {
		if p.Author == res.Viewer {
			mine++
		}
		if p.Repo == "" || p.Number == 0 || p.URL == "" {
			t.Errorf("incomplete PR parsed: %+v", p)
		}
		if p.Status() < StatusReadyToMerge || p.Status() > StatusDraft {
			t.Errorf("%s has an out-of-range status %v", p.Key(), p.Status())
		}
	}
	if mine == len(res.PRs) {
		t.Error("review-requested results should include PRs by other authors")
	}
}

func TestPullRequestsWithoutChecksAreHandled(t *testing.T) {
	c := serveTestdata(t, "search_review_requested.json", func(doc map[string]any) {
		doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
			map[string]any{"hasNextPage": false, "endCursor": ""}
	})
	res, err := c.Fetch(context.Background(), "q", 200)
	if err != nil {
		t.Fatal(err)
	}

	var none int
	for _, p := range res.PRs {
		if p.ChecksState == CheckNone {
			none++
			if len(p.Checks) != 0 {
				t.Errorf("%s reports no rollup but lists %d checks", p.Key(), len(p.Checks))
			}
		}
	}
	if none == 0 {
		t.Error("expected some PRs with no status checks in this fixture")
	}
}

// TestFetchFollowsCursorAcrossPages walks a two-page search and checks the
// cursor is threaded through, since a paginated review-requested search is
// exactly where this matters.
func TestFetchFollowsCursorAcrossPages(t *testing.T) {
	page1 := loadDoc(t, "search_review_requested.json")
	page2 := loadDoc(t, "search_authored.json")
	page2["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
		map[string]any{"hasNextPage": false, "endCursor": ""}

	var mu sync.Mutex
	var calls int
	var cursors []any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		calls++
		n := calls
		cursors = append(cursors, body.Variables["after"])
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			json.NewEncoder(w).Encode(page1)
			return
		}
		json.NewEncoder(w).Encode(page2)
	}))
	defer srv.Close()

	c := NewClient("test-token")
	c.Endpoint = srv.URL

	res, err := c.Fetch(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("made %d requests, want 2", calls)
	}
	if cursors[0] != nil {
		t.Errorf("first request sent a cursor: %v", cursors[0])
	}
	if cursors[1] == nil || cursors[1] == "" {
		t.Error("second request did not carry the endCursor")
	}
	if want := 25 + 11; len(res.PRs) != want {
		t.Errorf("PRs = %d, want %d across both pages", len(res.PRs), want)
	}
}

func TestFetchHonoursMaxAcrossPages(t *testing.T) {
	c := serveTestdata(t, "search_review_requested.json", nil) // hasNextPage stays true
	res, err := c.Fetch(context.Background(), "q", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PRs) < 25 {
		t.Errorf("PRs = %d, want at least one full page", len(res.PRs))
	}
	if len(res.PRs) > 60 {
		t.Errorf("PRs = %d, max should bound an endlessly-paging server", len(res.PRs))
	}
}

func TestThreadTruncationIsFlagged(t *testing.T) {
	c := serveTestdata(t, "search_authored.json", func(doc map[string]any) {
		search := doc["data"].(map[string]any)["search"].(map[string]any)
		for _, n := range search["nodes"].([]any) {
			node := n.(map[string]any)
			rt := node["reviewThreads"].(map[string]any)
			// Claim far more threads exist than the page returned.
			rt["totalCount"] = 500
		}
	})
	res, err := c.Fetch(context.Background(), "q", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.PRs {
		if !p.ThreadsTruncated {
			t.Errorf("%s should be flagged as truncated (%d threads reported)", p.Key(), p.TotalThreads)
		}
	}
}

func TestThreadTruncationNotFlaggedWhenComplete(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))
	for _, p := range res.PRs {
		if p.ThreadsTruncated {
			t.Errorf("%s wrongly flagged as truncated (%d threads)", p.Key(), p.TotalThreads)
		}
	}
}
