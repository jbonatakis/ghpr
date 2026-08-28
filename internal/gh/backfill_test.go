package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// backfillPayload is a response shaped like the backfill query's, which the
// captured fixture cannot be: it predates every field that query added.
const backfillPayload = `{
 "data": {
  "viewer": {"login": "jack"},
  "rateLimit": {"limit": 5000, "remaining": 4900, "cost": 17, "resetAt": "2026-08-28T16:00:00Z"},
  "search": {
   "issueCount": 1,
   "pageInfo": {"hasNextPage": false, "endCursor": ""},
   "nodes": [{
    "__typename": "PullRequest",
    "id": "PR_1", "number": 44, "title": "Structured logging",
    "url": "https://github.com/acme/starfield/pull/44",
    "isDraft": false, "bodyText": "cc @jack",
    "state": "MERGED",
    "createdAt": "2026-08-01T09:00:00Z",
    "updatedAt": "2026-08-20T09:00:00Z",
    "mergedAt": "2026-08-20T09:00:00Z",
    "closedAt": "2026-08-20T09:00:00Z",
    "mergeable": "MERGEABLE",
    "repository": {"nameWithOwner": "acme/starfield"},
    "author": {"login": "morgan-bell"},
    "comments": {"totalCount": 2, "nodes": [
      {"author": {"login": "riley-shaw"}, "createdAt": "2026-08-02T09:00:00Z", "bodyText": "looks fine"},
      {"author": {"login": "dana-quill"}, "createdAt": "2026-08-03T09:00:00Z", "bodyText": "one thing"}
    ]},
    "reviews": {"nodes": [
      {"state": "CHANGES_REQUESTED", "author": {"login": "dana-quill"}, "submittedAt": "2026-08-04T09:00:00Z", "bodyText": ""},
      {"state": "APPROVED", "author": {"login": "dana-quill"}, "submittedAt": "2026-08-09T09:00:00Z", "bodyText": ""}
    ]},
    "latestReviews": {"nodes": [
      {"state": "APPROVED", "author": {"login": "dana-quill"}, "submittedAt": "2026-08-09T09:00:00Z", "bodyText": ""}
    ]},
    "reviewThreads": {"totalCount": 1, "nodes": [
      {"isResolved": false, "isOutdated": false, "comments": {"totalCount": 3, "nodes": [
        {"createdAt": "2026-08-05T09:00:00Z", "bodyText": "this line", "author": {"login": "dana-quill"}},
        {"createdAt": "2026-08-06T09:00:00Z", "bodyText": "@jack thoughts?", "author": {"login": "dana-quill"}},
        {"createdAt": "2026-08-07T09:00:00Z", "bodyText": "fixed", "author": {"login": "morgan-bell"}}
      ]}}
    ]},
    "commits": {"nodes": [
      {"commit": {"oid": "aaa", "committedDate": "2026-08-01T10:00:00Z", "author": {"user": {"login": "morgan-bell"}}}},
      {"commit": {"oid": "bbb", "committedDate": "2026-08-08T10:00:00Z", "author": {"user": {"login": "morgan-bell"}}}},
      {"commit": {"oid": "ccc", "committedDate": "2026-08-10T10:00:00Z", "author": {"user": {"login": "morgan-bell"}}}}
    ]}
   }]
  }
 }
}`

func backfillClient(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		// The whole point of the second document is that it asks for more.
		for _, want := range []string{"reviewThreads", "comments(last: 20)", "commits(last: 20)", "reviews(last: 20)"} {
			if !strings.Contains(body.Query, want) {
				t.Errorf("the backfill query does not ask for %s", want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(backfillPayload))
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test")
	c.Endpoint = srv.URL
	return c
}

// Everything the backfill query adds has to survive convert, or the extra cost
// buys nothing.
func TestBackfillReadsTheRicherResponse(t *testing.T) {
	res, err := backfillClient(t).Backfill(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if len(res.PRs) != 1 {
		t.Fatalf("got %d pull requests", len(res.PRs))
	}
	p := res.PRs[0]

	if len(p.ThreadComments) != 3 {
		t.Errorf("kept %d review-thread comments, want 3 — this is the whole point", len(p.ThreadComments))
	}
	if len(p.AllReviews) != 2 {
		t.Errorf("kept %d reviews, want both from the same reviewer", len(p.AllReviews))
	}
	if len(p.Pushes) != 3 {
		t.Errorf("kept %d commits, want 3", len(p.Pushes))
	}
	if p.State != StateMerged {
		t.Errorf("state is %q, want MERGED", p.State)
	}
	// commits(last: n) comes back oldest first, so the head is the last one.
	if p.HeadOID != "ccc" {
		t.Errorf("head is %q, want the newest commit", p.HeadOID)
	}
	if want := "2026-08-10T10:00:00Z"; p.PushedAt.UTC().Format(time.RFC3339) != want {
		t.Errorf("head dated %s, want %s", p.PushedAt.UTC().Format(time.RFC3339), want)
	}
	// The mention is inside a review thread, invisible to the polling query.
	if p.LastMentionBy != "dana-quill" {
		t.Errorf("mention attributed to %q, want the thread comment's author", p.LastMentionBy)
	}
}

// And Seed has to turn all of it into lines.
func TestBackfillSeedsWhatThePollCannotSee(t *testing.T) {
	res, err := backfillClient(t).Backfill(context.Background(), "q", 200)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	since := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	got := Seed(res.PRs, since, res.Viewer)

	counts := map[EventKind]int{}
	for _, e := range got {
		counts[e.Kind]++
	}
	for _, tc := range []struct {
		kind EventKind
		want int
		why  string
	}{
		{EventOpened, 1, "opened"},
		{EventMerged, 1, "merged — invisible to an is:open search"},
		{EventPush, 3, "one line per commit, not one for the branch"},
		{EventReview, 2, "both reviews from the same reviewer"},
		{EventComment, 4, "two conversation comments and two thread comments"},
		{EventMention, 2, "the description's mention and the one in the review thread"},
	} {
		if counts[tc.kind] != tc.want {
			t.Errorf("%s: got %d, want %d", tc.why, counts[tc.kind], tc.want)
		}
	}

	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("seed is out of order at %d", i)
		}
	}
	t.Logf("a single pull request seeded %d events", len(got))
}

// The feed spans every mode by design, so the searches that fill it in must
// not be scoped to whichever one the dashboard happens to be showing.
func TestBackfillSearchesCoverEverythingYouTouch(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	since := now.Add(-30 * time.Minute) // one window's worth, so two searches
	got := BackfillSearches("", since, now)

	if len(got) != 2 {
		t.Fatalf("got %d searches: %+v", len(got), got)
	}
	var queries []string
	for _, p := range got {
		queries = append(queries, p.Query)
	}
	joined := strings.Join(queries, " || ")
	// involves:@me covers authoring, commenting, assignment and mentions, but
	// not a review merely requested and not yet acted on.
	for _, want := range []string{"involves:@me", "review-requested:@me"} {
		if !strings.Contains(joined, want) {
			t.Errorf("nothing searches %s: %q", want, queries)
		}
	}
	for _, plan := range got {
		q := plan.Query
		if !strings.Contains(q, "is:pr") || !strings.Contains(q, "archived:false") {
			t.Errorf("%q is not a pull request search", q)
		}
		// Neither is:open nor is:closed: what finished inside the window is
		// exactly the part an open-only search would leave out.
		if strings.Contains(q, "is:open") || strings.Contains(q, "is:closed") {
			t.Errorf("%q restricts the lifecycle and would miss half the window", q)
		}
		// Bounded, or the backfill fetches pull requests that cannot possibly
		// contribute a line to it.
		if !strings.Contains(q, "updated:") {
			t.Errorf("%q is unbounded", q)
		}
		if strings.Contains(q, "author:@me") {
			t.Errorf("%q is scoped to one mode", q)
		}
	}
}

// The count stays logarithmic in the span. An even split fine enough to make
// the first window half an hour would cut a month into 1,440 searches.
func TestBackfillWindowCountStaysSmall(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		span      time.Duration
		maxWindow int
	}{
		{30 * time.Minute, 1},
		{time.Hour, 2},
		{24 * time.Hour, 5},
		{168 * time.Hour, 7},
		{720 * time.Hour, 8},
		{8760 * time.Hour, backfillMaxChunks}, // a year, still bounded
	} {
		got := backfillWindows(now.Add(-tc.span), now)
		if len(got) > tc.maxWindow {
			t.Errorf("%s span produced %d windows, want at most %d",
				tc.span, len(got), tc.maxWindow)
		}
		if len(got) == 0 {
			t.Errorf("%s span produced no windows at all", tc.span)
		}
		// Two search shapes per window.
		if want := len(got) * BackfillShapes; len(BackfillSearches("", now.Add(-tc.span), now)) != want {
			t.Errorf("%s span: searches and windows disagree", tc.span)
		}
	}
}

// The newest window is the one everything waits on, so it is deliberately the
// narrowest — a half hour is nearly always a single page, and pages within one
// search are sequential.
func TestTheNewestWindowIsTheNarrowest(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	got := backfillWindows(now.Add(-720*time.Hour), now)

	if len(got) < 3 {
		t.Fatalf("a month gave only %d windows", len(got))
	}
	if w := got[0].to.Sub(got[0].from); w != backfillFirstWindow {
		t.Errorf("the newest window is %s wide, want %s", w, backfillFirstWindow)
	}
	// Widening as they go back is what keeps the count down.
	for i := 1; i < len(got)-1; i++ {
		prev := got[i-1].to.Sub(got[i-1].from)
		this := got[i].to.Sub(got[i].from)
		if this <= prev {
			t.Errorf("window %d (%s) is no wider than window %d (%s)", i, this, i-1, prev)
		}
	}
	// And the last one is however wide it needs to be to reach the start.
	if last := got[len(got)-1]; !last.from.Equal(now.Add(-720 * time.Hour)) {
		t.Errorf("the oldest window starts at %s, leaving a hole", last.from)
	}
}

// A span shorter than the first window is one search, not a fragment of one.
func TestAShortSpanIsASingleWindow(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	for _, span := range []time.Duration{time.Minute, 10 * time.Minute, 30 * time.Minute} {
		got := backfillWindows(now.Add(-span), now)
		if len(got) != 1 {
			t.Errorf("%s span produced %d windows, want 1", span, len(got))
		}
		if !got[0].from.Equal(now.Add(-span)) {
			t.Errorf("%s span starts at %s", span, got[0].from)
		}
	}
}

// Newest first, because that is what the reader wants first and what the top
// of the feed is — and because it lets the rest be abandoned once the backlog
// is full.
func TestBackfillSearchesRunNewestWindowFirst(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	got := BackfillSearches("", now.Add(-24*time.Hour), now)

	for i := 1; i < len(got); i++ {
		if got[i].From.After(got[i-1].From) {
			t.Fatalf("search %d covers a newer window than %d", i, i-1)
		}
	}
	if !got[0].Newest {
		t.Error("the first search is not the newest window")
	}
	// The newest window is left open at the top, so anything updated while the
	// backfill itself runs is still caught.
	if strings.Contains(got[0].Query, "..") {
		t.Errorf("the newest window is closed at the top: %q", got[0].Query)
	}
	if !strings.Contains(got[len(got)-1].Query, "..") {
		t.Errorf("an older window is unbounded: %q", got[len(got)-1].Query)
	}
}

// Together the windows have to cover the whole span with no hole in the middle.
func TestBackfillWindowsLeaveNoGap(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	since := now.Add(-720 * time.Hour)
	got := backfillWindows(since, now)

	if got[0].to != now {
		t.Errorf("the newest window ends at %s, want now", got[0].to)
	}
	if last := got[len(got)-1]; !last.from.Equal(since) {
		t.Errorf("the oldest window starts at %s, want %s", last.from, since)
	}
	for i := 1; i < len(got); i++ {
		if !got[i].to.Equal(got[i-1].from) {
			t.Errorf("gap between window %d and %d: %s then %s",
				i-1, i, got[i-1].from, got[i].to)
		}
	}
}

func TestBackfillSearchesKeepTheUsersOwnQualifiers(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	for _, plan := range BackfillSearches("org:acme", now.Add(-24*time.Hour), now) {
		if !strings.Contains(plan.Query, "org:acme") {
			t.Errorf("-query was dropped from %q", plan.Query)
		}
	}
}
