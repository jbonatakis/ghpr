package gh

import (
	"strings"
	"testing"
	"time"
)

// The searches have to cover every way a pull request can matter to someone,
// and the gaps between GitHub's qualifiers are not obvious.
//
//   - involves is documented as author OR assignee OR mentions OR commenter.
//     Reviewing is not on that list.
//   - review-requested matches only while a review is still outstanding.
//     Submitting the review removes you from it.
//
// So a pull request you reviewed and did not comment on fell between them, and
// everything that happened on it afterwards — the commits pushed in answer to
// your review, most of all — was invisible to the feed.
func TestAPullRequestYouReviewedIsSearchedFor(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	got := BackfillSearches("", now.Add(-24*time.Hour), now)

	var queries []string
	for _, p := range got {
		queries = append(queries, p.Query)
	}
	joined := strings.Join(queries, " || ")

	if !strings.Contains(joined, "reviewed-by:@me") {
		t.Fatal("nothing searches for pull requests you have reviewed; " +
			"involves does not cover reviewing and review-requested stops " +
			"matching once the review is in")
	}

	// Every window gets every shape, or the coverage has holes in time as well.
	perWindow := map[int]map[string]bool{}
	for _, p := range got {
		if perWindow[p.Window] == nil {
			perWindow[p.Window] = map[string]bool{}
		}
		for _, shape := range []string{"involves:@me", "review-requested:@me", "reviewed-by:@me"} {
			if strings.Contains(p.Query, shape) {
				perWindow[p.Window][shape] = true
			}
		}
	}
	for window, shapes := range perWindow {
		if len(shapes) != BackfillShapes {
			t.Errorf("window %d is searched %d ways, want %d", window, len(shapes), BackfillShapes)
		}
	}
}

// BackfillShapes is what tells a caller when a window has been answered in
// full. If it drifts from the number of searches, windows either never release
// or release before they are complete.
func TestTheShapeCountMatchesTheSearches(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if got := len(BackfillSearches("", now.Add(-20*time.Minute), now)); got != BackfillShapes {
		t.Errorf("one window is %d searches, but BackfillShapes says %d", got, BackfillShapes)
	}
	if len(backfillShapes) != BackfillShapes {
		t.Errorf("%d shapes defined, BackfillShapes says %d", len(backfillShapes), BackfillShapes)
	}
}

// A push is the thing most likely to follow a review, and it has to survive
// the whole path into the feed.
func TestCommitsOnAReviewedPullRequestBecomeEvents(t *testing.T) {
	now := time.Now().UTC()
	p := PR{
		Repo: "acme/hyperspace", Number: 99, URL: "https://example.invalid/99",
		Author: "brianmuse", CreatedAt: now.Add(-48 * time.Hour),
		AllReviews: []Reviewer{{Login: "jack", State: "APPROVED", At: now.Add(-3 * time.Hour)}},
		Pushes: []Push{
			{By: "brianmuse", At: now.Add(-2 * time.Hour)},
			{By: "brianmuse", At: now.Add(-90 * time.Minute)},
		},
	}

	got := Seed([]PR{p}, now.Add(-24*time.Hour), "jack")
	var pushes, reviews int
	for _, e := range got {
		switch e.Kind {
		case EventPush:
			pushes++
			if e.Actor != "brianmuse" {
				t.Errorf("a commit is attributed to %q", e.Actor)
			}
		case EventReview:
			reviews++
		}
	}
	if pushes != 2 {
		t.Errorf("seeded %d commits, want one line per commit", pushes)
	}
	if reviews != 1 {
		t.Errorf("seeded %d reviews, want the one that was given", reviews)
	}
}
