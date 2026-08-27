package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// serveTestdata replays a captured GitHub response, optionally rewriting it
// first, and returns a client pointed at the replay server.
func serveTestdata(t *testing.T, file string, mutate func(map[string]any)) *Client {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if mutate != nil {
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		mutate(doc)
		if raw, err = json.Marshal(doc); err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Query == "" {
			t.Error("empty query")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test-token")
	c.Endpoint = srv.URL
	return c
}

func fetchAll(t *testing.T, c *Client) Result {
	t.Helper()
	res, err := c.Fetch(context.Background(), "is:open is:pr author:@me", 200)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return res
}

func byKey(t *testing.T, res Result, key string) PR {
	t.Helper()
	for _, p := range res.PRs {
		if p.Key() == key {
			return p
		}
	}
	t.Fatalf("no PR %q in result", key)
	return PR{}
}

func TestFetchParsesCapturedResponse(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))

	if got, want := len(res.PRs), 11; got != want {
		t.Errorf("PR count = %d, want %d", got, want)
	}
	if got, want := res.Viewer, "octo-dev"; got != want {
		t.Errorf("Viewer = %q, want %q", got, want)
	}
	// Deliberately not pinned to a snapshot value: the budget moves whenever
	// the fixture is re-captured.
	if res.RateLimit.Limit != 5000 {
		t.Errorf("RateLimit.Limit = %d, want 5000", res.RateLimit.Limit)
	}
	if res.RateLimit.Remaining <= 0 || res.RateLimit.Remaining > res.RateLimit.Limit {
		t.Errorf("RateLimit.Remaining = %d, outside 1..%d", res.RateLimit.Remaining, res.RateLimit.Limit)
	}
	if res.RateLimit.Cost <= 0 {
		t.Errorf("RateLimit.Cost = %d, want the query's cost", res.RateLimit.Cost)
	}
	if res.RateLimit.ResetAt.IsZero() {
		t.Error("RateLimit.ResetAt not parsed")
	}
}

func TestConvertDerivesCommentsAndThreads(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))

	// #96 carries 2 conversation comments and 15 spread over 7 review threads,
	// 4 of which are neither resolved nor outdated.
	p := byKey(t, res, "acme/starfield#96")
	if got, want := p.IssueComments, 2; got != want {
		t.Errorf("IssueComments = %d, want %d", got, want)
	}
	if got, want := p.ReviewComments, 15; got != want {
		t.Errorf("ReviewComments = %d, want %d", got, want)
	}
	if got, want := p.Comments(), 17; got != want {
		t.Errorf("Comments() = %d, want %d", got, want)
	}
	if got, want := p.UnresolvedThreads, 4; got != want {
		t.Errorf("UnresolvedThreads = %d, want %d", got, want)
	}
	if got, want := p.TotalThreads, 7; got != want {
		t.Errorf("TotalThreads = %d, want %d", got, want)
	}
}

func TestConvertDerivesChecks(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))

	failing := byKey(t, res, "acme/starfield#96")
	if failing.ChecksState != CheckFailure {
		t.Errorf("#96 ChecksState = %v, want CheckFailure", failing.ChecksState)
	}
	if failing.ChecksFailed == 0 {
		t.Error("#96 should tally at least one failed check")
	}
	if len(failing.Checks) == 0 {
		t.Error("#96 should list its individual checks")
	}
	if total := failing.ChecksPassed + failing.ChecksFailed + failing.ChecksPending; total > len(failing.Checks) {
		t.Errorf("tallies (%d) exceed checks (%d)", total, len(failing.Checks))
	}

	passing := byKey(t, res, "acme/sensor-presence-collector#828")
	if passing.ChecksState != CheckSuccess {
		t.Errorf("#828 ChecksState = %v, want CheckSuccess", passing.ChecksState)
	}
	// Not pinned to a snapshot count: the repository gains checks over time.
	if passing.ChecksPassed == 0 {
		t.Error("#828 should tally its passing checks")
	}
	if passing.ChecksFailed != 0 || passing.ChecksPending != 0 {
		t.Errorf("#828 is green but tallies %d failed / %d pending",
			passing.ChecksFailed, passing.ChecksPending)
	}

	none := byKey(t, res, "octo-dev/sketchpad-cli#2")
	if none.ChecksState != CheckNone {
		t.Errorf("sketchpad-cli#2 ChecksState = %v, want CheckNone", none.ChecksState)
	}
	if len(none.Checks) != 0 {
		t.Errorf("sketchpad-cli#2 should have no checks, got %d", len(none.Checks))
	}
}

func TestConvertHeadOIDAndRefs(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))
	p := byKey(t, res, "acme/starfield#96")
	if p.HeadOID == "" {
		t.Error("HeadOID should be populated for push detection")
	}
	if p.BaseRef == "" || p.HeadRef == "" {
		t.Errorf("refs not parsed: head=%q base=%q", p.HeadRef, p.BaseRef)
	}
}

func TestConvertMergesReviewersPreferringGivenReviews(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))
	p := byKey(t, res, "acme/sensor-presence-collector#828")

	if len(p.Reviewers) == 0 {
		t.Fatal("#828 has review requests, expected reviewers")
	}
	seen := map[string]int{}
	for _, r := range p.Reviewers {
		seen[r.Login]++
	}
	for login, n := range seen {
		if n > 1 {
			t.Errorf("reviewer %q listed %d times, want 1", login, n)
		}
	}
	if len(p.PendingReviewers()) == 0 {
		t.Error("expected at least one pending reviewer on #828")
	}
}

// TestStatusMatchesRealWorldRows pins the bucket each captured PR falls into,
// so a change to the precedence rules is visible.
func TestStatusMatchesRealWorldRows(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))

	want := map[string]Status{
		"acme/starfield#44":                  StatusChangesRequested, // outranks its failing checks
		"acme/starfield#96":                  StatusChecksFailing,
		"acme/design-docs#9":                 StatusChecksFailing,
		"acme/starfield#84":                  StatusDraft, // draft outranks its conflict
		"acme/agent-runtime#609":             StatusConflicts,
		"octo-dev/dashboard#4":               StatusConflicts,
		"acme/database-tooling#25":           StatusAwaitingReview,
		"acme/sensor-presence-collector#828": StatusAwaitingReview,
		"octo-dev/sketchpad-cli#2":           StatusAwaitingReview,
	}
	for key, wantStatus := range want {
		if got := byKey(t, res, key).Status(); got != wantStatus {
			t.Errorf("%s status = %v, want %v", key, got, wantStatus)
		}
	}
}

func TestStatusReadyToMergeAndUnresolved(t *testing.T) {
	base := PR{Mergeable: "MERGEABLE", ReviewDecision: "APPROVED", ChecksState: CheckSuccess}
	if got := base.Status(); got != StatusReadyToMerge {
		t.Errorf("approved+green = %v, want StatusReadyToMerge", got)
	}

	stillRunning := base
	stillRunning.ChecksState = CheckPending
	if got := stillRunning.Status(); got == StatusReadyToMerge {
		t.Error("approved with checks still running should not read as ready")
	}

	unresolved := PR{Mergeable: "MERGEABLE", ChecksState: CheckSuccess, UnresolvedThreads: 3}
	if got := unresolved.Status(); got != StatusUnresolved {
		t.Errorf("unresolved threads = %v, want StatusUnresolved", got)
	}
}

func TestFetchSurfacesGraphQLErrors(t *testing.T) {
	c := serveTestdata(t, "search_authored.json", func(doc map[string]any) {
		doc["errors"] = []any{map[string]any{"message": "Bad credentials"}}
	})
	if _, err := c.Fetch(context.Background(), "q", 50); err == nil {
		t.Fatal("expected an error when the response carries GraphQL errors")
	}
}

func TestFetchStopsWhenPaginationHasNoNextPage(t *testing.T) {
	// A server that always claims another page would spin forever if Fetch
	// trusted hasNextPage without also honouring the max.
	c := serveTestdata(t, "search_authored.json", func(doc map[string]any) {
		search := doc["data"].(map[string]any)["search"].(map[string]any)
		search["pageInfo"] = map[string]any{"hasNextPage": true, "endCursor": "CURSOR"}
	})
	done := make(chan Result, 1)
	go func() {
		res, err := c.Fetch(context.Background(), "q", 25)
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	select {
	case res := <-done:
		if len(res.PRs) > 25+11 {
			t.Errorf("fetched %d PRs, expected the max to bound paging", len(res.PRs))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Fetch did not terminate: pagination loop is unbounded")
	}
}

func TestFetchRejectsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient("bad")
	c.Endpoint = srv.URL
	_, err := c.Fetch(context.Background(), "q", 10)
	if err == nil {
		t.Fatal("expected 401 to surface as an error")
	}
}

func TestModeQueries(t *testing.T) {
	for _, tc := range []struct {
		mode Mode
		want string
	}{
		{ModeAuthored, "author:@me"},
		{ModeReviewRequested, "review-requested:@me"},
		{ModeInvolved, "involves:@me"},
	} {
		q := tc.mode.Query("")
		if !contains(q, tc.want) {
			t.Errorf("%v query = %q, want it to contain %q", tc.mode, q, tc.want)
		}
		if !contains(q, "is:open is:pr") {
			t.Errorf("%v query = %q, missing base qualifiers", tc.mode, q)
		}
	}
	if q := ModeAuthored.Query("org:acme"); !contains(q, "org:acme") {
		t.Errorf("extra qualifiers dropped: %q", q)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestConvertCapturesNodeIDs(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_authored.json", nil))
	for _, p := range res.PRs {
		if p.ID == "" {
			t.Errorf("%s has no node id; a vanished PR could not be verified", p.Key())
		}
	}
}

func TestFetchMarksCompletenessHonestly(t *testing.T) {
	done := serveTestdata(t, "search_authored.json", func(doc map[string]any) {
		doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
			map[string]any{"hasNextPage": false, "endCursor": ""}
	})
	res, err := done.Fetch(context.Background(), "q", 200)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Complete {
		t.Error("a search that ran out of pages should report Complete")
	}

	// Cut short by max: absence of a PR then proves nothing.
	truncated := serveTestdata(t, "search_review_requested.json", nil)
	res, err = truncated.Fetch(context.Background(), "q", 25)
	if err != nil {
		t.Fatal(err)
	}
	if res.Complete {
		t.Error("a search stopped by max must not claim to be complete")
	}
}

func TestConvertCapturesActors(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_review_requested.json", func(doc map[string]any) {
		doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
			map[string]any{"hasNextPage": false, "endCursor": ""}
	}))

	var commenters, reviewers, pushers int
	for _, p := range res.PRs {
		if p.LastCommentBy != "" {
			commenters++
			if p.LastCommentAt.IsZero() {
				t.Errorf("%s has a commenter but no timestamp", p.Key())
			}
		}
		if p.LastReviewBy != "" {
			reviewers++
			if p.LastReviewAt.IsZero() {
				t.Errorf("%s has a reviewer but no timestamp", p.Key())
			}
		}
		if p.PushedBy != "" {
			pushers++
		}
	}
	if commenters == 0 {
		t.Error("no comment authors parsed from the fixture")
	}
	if reviewers == 0 {
		t.Error("no review authors parsed from the fixture")
	}
	if pushers == 0 {
		t.Error("no commit authors parsed from the fixture")
	}
	t.Logf("attribution available on %d/%d comments, %d reviews, %d pushes",
		commenters, len(res.PRs), reviewers, pushers)
}

func TestReviewersCarrySubmissionTimes(t *testing.T) {
	res := fetchAll(t, serveTestdata(t, "search_review_requested.json", func(doc map[string]any) {
		doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
			map[string]any{"hasNextPage": false, "endCursor": ""}
	}))
	var checked int
	for _, p := range res.PRs {
		for _, r := range p.Reviewers {
			if r.Pending() {
				if !r.At.IsZero() {
					t.Errorf("%s: pending request for %s should have no submission time", p.Key(), r.Login)
				}
				continue
			}
			checked++
			if r.At.IsZero() {
				t.Errorf("%s: review by %s has no submission time", p.Key(), r.Login)
			}
		}
	}
	if checked == 0 {
		t.Error("fixture has no submitted reviews to check")
	}
}
