package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Every other line in the feed names someone. A merge is somebody's deliberate
// act, and GitHub says whose, so leaving it blank was a gap rather than a
// limit — unlike checks, where genuinely nobody is behind it.

func finishedPayload(state, mergedBy, closedBy string) string {
	node := map[string]any{
		"__typename": "PullRequest",
		"id":         "PR_1", "number": 99, "title": "t",
		"url":        "https://example.invalid/99",
		"state":      state,
		"createdAt":  "2026-08-01T09:00:00Z",
		"updatedAt":  "2026-08-20T09:00:00Z",
		"mergedAt":   "2026-08-20T09:00:00Z",
		"closedAt":   "2026-08-20T09:00:00Z",
		"mergeable":  "MERGEABLE",
		"repository": map[string]any{"nameWithOwner": "acme/hyperspace"},
		"author":     map[string]any{"login": "jbonatakis"},
	}
	if mergedBy != "" {
		node["mergedBy"] = map[string]any{"login": mergedBy}
	}
	if closedBy != "" {
		node["timelineItems"] = map[string]any{
			"nodes": []any{map[string]any{"actor": map[string]any{"login": closedBy}}},
		}
	}
	raw, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"viewer":    map[string]any{"login": "jack"},
			"rateLimit": map[string]any{"limit": 5000, "remaining": 4900, "cost": 3},
			"search": map[string]any{
				"issueCount": 1,
				"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": ""},
				"nodes":      []any{node},
			},
		},
	})
	return string(raw)
}

func finishedClient(t *testing.T, payload string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	c := NewClient("test")
	c.Endpoint = srv.URL
	return c
}

func TestABackfilledMergeNamesWhoMergedIt(t *testing.T) {
	res, err := finishedClient(t, finishedPayload("MERGED", "brianmuse", "")).
		Backfill(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if got := res.PRs[0].FinishedBy; got != "brianmuse" {
		t.Fatalf("FinishedBy = %q, want the merger", got)
	}

	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range Seed(res.PRs, since, "jack") {
		if e.Kind == EventMerged {
			if e.Actor != "brianmuse" {
				t.Errorf("the merged line has no one attached: %+v", e)
			}
			return
		}
	}
	t.Error("no merged line was seeded at all")
}

// GitHub names the merger outright but not the closer, so a plain close comes
// off the last close on the timeline.
func TestABackfilledCloseNamesWhoClosedIt(t *testing.T) {
	res, err := finishedClient(t, finishedPayload("CLOSED", "", "dana-quill")).
		Backfill(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, e := range Seed(res.PRs, since, "jack") {
		if e.Kind == EventClosed {
			if e.Actor != "dana-quill" {
				t.Errorf("the closed line has no one attached: %+v", e)
			}
			return
		}
	}
	t.Error("no closed line was seeded at all")
}

// A blank name is still better than a wrong one: some merges are made by apps
// or by GitHub itself, and the field comes back empty.
func TestAnUnattributedMergeStillReportsTheMerge(t *testing.T) {
	res, err := finishedClient(t, finishedPayload("MERGED", "", "")).
		Backfill(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}

	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	var found bool
	for _, e := range Seed(res.PRs, since, "jack") {
		if e.Kind == EventMerged {
			found = true
			if e.Actor != "" {
				t.Errorf("an actor was invented: %q", e.Actor)
			}
		}
	}
	if !found {
		t.Error("an unattributed merge went unreported")
	}
}

// The other path to a merged line: a pull request that vanished from the
// search and was looked up directly.
func TestAVerifiedMergeNamesWhoMergedIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"nodes": []any{
				map[string]any{
					"id": "a", "state": "MERGED",
					"mergedBy": map[string]any{"login": "brianmuse"},
				},
				map[string]any{
					"id": "b", "state": "CLOSED",
					"timelineItems": map[string]any{"nodes": []any{
						map[string]any{"actor": map[string]any{"login": "dana-quill"}},
					}},
				},
			}},
		})
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test")
	c.Endpoint = srv.URL
	got, err := c.States(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("States: %v", err)
	}

	if got["a"].State != StateMerged || got["a"].By != "brianmuse" {
		t.Errorf("merged outcome = %+v", got["a"])
	}
	if got["b"].State != StateClosed || got["b"].By != "dana-quill" {
		t.Errorf("closed outcome = %+v", got["b"])
	}

	now := time.Now()
	if e := ClosureEvent(PR{Repo: "acme/hyperspace", Number: 99}, got["a"], now); e.Actor != "brianmuse" {
		t.Errorf("the verified merge lost its actor: %+v", e)
	}
}
