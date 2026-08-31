package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/eventlog"
	"github.com/jbonatakis/ghpr/internal/gh"
	"github.com/jbonatakis/ghpr/internal/ui"
)

// lookupServer answers the direct lookup, then the searches. found decides
// whether the searches come back with the pull request.
func lookupServer(t *testing.T, teamOnly, found bool) *gh.Client {
	t.Helper()

	requests := []any{
		map[string]any{"requestedReviewer": map[string]any{
			"__typename": "Team", "slug": "ai-agents-maintainers",
			"organization": map[string]any{"login": "robinpowered"},
		}},
	}
	if !teamOnly {
		requests = append(requests, map[string]any{"requestedReviewer": map[string]any{
			"__typename": "User", "login": "jack",
		}})
	}

	lookup, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{"login": "jack"},
			"repository": map[string]any{"pullRequest": map[string]any{
				"number": 745, "title": "Agent-side conflict policy",
				"url": "https://example.invalid/745", "state": "OPEN",
				"createdAt": "2026-08-31T20:00:00Z", "updatedAt": "2026-08-31T22:02:43Z",
				"author":         map[string]any{"login": "kunle-lawal"},
				"repository":     map[string]any{"nameWithOwner": "robinpowered/robin-ai-agents"},
				"reviewRequests": map[string]any{"nodes": requests},
				"latestReviews":  map[string]any{"nodes": []any{}},
			}},
		},
	})

	var nodes []any
	if found {
		nodes = append(nodes, map[string]any{
			"__typename": "PullRequest", "id": "PR_745", "number": 745, "title": "t",
			"url": "https://example.invalid/745", "state": "OPEN",
			"createdAt": "2026-08-31T20:00:00Z", "updatedAt": "2026-08-31T22:02:43Z",
			"mergeable":  "MERGEABLE",
			"repository": map[string]any{"nameWithOwner": "robinpowered/robin-ai-agents"},
			"author":     map[string]any{"login": "kunle-lawal"},
		})
	}
	search, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"viewer":    map[string]any{"login": "jack"},
			"rateLimit": map[string]any{"limit": 5000, "remaining": 4900, "cost": 3},
			"search": map[string]any{
				"issueCount": len(nodes),
				"pageInfo":   map[string]any{"hasNextPage": false, "endCursor": ""},
				"nodes":      nodes,
			},
		},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(body.Query, "pullRequest(number:") {
			w.Write(lookup)
			return
		}
		w.Write(search)
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	return c
}

func whyPR(t *testing.T, cfg ui.Config) string {
	t.Helper()
	return capture(t, func() error { return explainOne(cfg, "robinpowered/robin-ai-agents#745") })
}

// The point of naming one pull request: the direct lookup shows who the review
// is really requested of, which the search results never reveal, and a
// CODEOWNERS rule usually names a team.
func TestWhyPRShowsWhoTheReviewIsRequestedOf(t *testing.T) {
	cfg := ui.Config{
		Client: lookupServer(t, true, true), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 48 * time.Hour, Watch: gh.AllShapes,
	}
	out := whyPR(t, cfg)

	if !strings.Contains(out, "team   robinpowered/ai-agents-maintainers") {
		t.Errorf("the team request is not shown:\n%s", out)
	}
	if !strings.Contains(out, "reaches it") {
		t.Errorf("no search was reported as reaching it:\n%s", out)
	}
	if !strings.Contains(out, "in the feed's scope") {
		t.Errorf("the verdict does not say it is covered:\n%s", out)
	}
}

// A team-only request that nothing reaches is the case that would need
// team-review-requested, and it has to be called out rather than left to be
// inferred from three lines of "does not".
func TestWhyPRNamesTheTeamGapWhenNothingReachesIt(t *testing.T) {
	cfg := ui.Config{
		Client: lookupServer(t, true, false), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 48 * time.Hour, Watch: gh.AllShapes,
	}
	out := whyPR(t, cfg)

	if !strings.Contains(out, "requested of a team rather than") {
		t.Errorf("the team gap is not named:\n%s", out)
	}
	if !strings.Contains(out, "team-review-requested") {
		t.Errorf("the fix is not named:\n%s", out)
	}
}

// "It is not in my feed" and "it was never captured" are different complaints,
// and the saved record knows which one it is.
func TestWhyPRSaysWhetherItIsAlreadyOnRecord(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	log, err := eventlog.Open()
	if err != nil {
		t.Fatal(err)
	}
	base := ui.Config{
		Client: lookupServer(t, true, true), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 48 * time.Hour, Watch: gh.AllShapes,
		Log: log,
	}

	// Nothing kept for it yet.
	if out := whyPR(t, base); !strings.Contains(out, "has not been captured before") {
		t.Errorf("an unseen pull request is not reported as unseen:\n%s", out)
	}

	// Now with a couple of lines on record.
	at := time.Now().Add(-3 * time.Hour)
	with := base
	with.Cached = []gh.Event{
		{At: at, Kind: gh.EventOpened, Key: "robinpowered/robin-ai-agents#745", Text: "opened"},
		{At: at.Add(time.Hour), Kind: gh.EventComment, Key: "robinpowered/robin-ai-agents#745", Text: "new comment"},
	}
	out := whyPR(t, with)
	if !strings.Contains(out, "already holds 2 lines for it") {
		t.Errorf("the saved record was not consulted:\n%s", out)
	}
	if !strings.Contains(out, "filter with /") {
		t.Errorf("nothing says how to find it in the feed:\n%s", out)
	}
}

// With nothing kept between runs there is no record to consult, and saying
// "never captured" would be a claim it cannot make.
func TestWhyPRDoesNotClaimAMissWithoutARecord(t *testing.T) {
	cfg := ui.Config{
		Client: lookupServer(t, true, true), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 48 * time.Hour, Watch: gh.AllShapes,
	}
	out := whyPR(t, cfg)

	if strings.Contains(out, "has not been captured before") {
		t.Errorf("a miss was claimed with no record to check:\n%s", out)
	}
	if !strings.Contains(out, "no record") {
		t.Errorf("nothing explains why the record cannot answer:\n%s", out)
	}
}
