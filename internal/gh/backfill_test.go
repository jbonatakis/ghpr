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

// The closed search is the other half: a month described without what was
// merged in it is a month with its best days left out.
func TestClosedQueryLooksForFinishedWork(t *testing.T) {
	since := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	q := ModeAuthored.ClosedQuery("", since)

	for _, want := range []string{"is:pr", "is:closed", "author:@me", "updated:>=2026-07-29"} {
		if !strings.Contains(q, want) {
			t.Errorf("closed query %q is missing %q", q, want)
		}
	}
	if strings.Contains(q, "is:open") {
		t.Errorf("closed query asks for open pull requests: %q", q)
	}
	if q := ModeReviewRequested.ClosedQuery("org:acme", since); !strings.Contains(q, "review-requested:@me") || !strings.Contains(q, "org:acme") {
		t.Errorf("the mode or the extra qualifiers were lost: %q", q)
	}
}
