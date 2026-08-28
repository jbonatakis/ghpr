package gh

import (
	"testing"
	"time"
)

// Two kinds of change matter because of who they are aimed at rather than what
// they are: a review asked of you, and a comment that says your name.

var mentionBase = time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)

func watched() PR {
	return PR{
		Repo: "acme/starfield", Number: 44, Mergeable: "MERGEABLE",
		ChecksState: CheckSuccess, HeadOID: "a", IssueComments: 3,
	}
}

func byKind(events []Event) map[EventKind]Event {
	out := map[EventKind]Event{}
	for _, e := range events {
		out[e.Kind] = e
	}
	return out
}

func diffOne(prev, next PR) []Event {
	return Diff([]PR{prev}, []PR{next}, DiffOpts{
		Now: mentionBase.Add(time.Hour), PrevComplete: true, Viewer: "jack",
	})
}

func TestBeingAddedAsAReviewerIsReported(t *testing.T) {
	prev := watched()
	next := watched()
	next.ReviewRequests = []Reviewer{{Login: "jack", State: "PENDING"}}

	got := byKind(diffOne(prev, next))
	e, ok := got[EventReviewRequested]
	if !ok {
		t.Fatalf("no review-requested event: %+v", got)
	}
	if e.Text != "review requested" {
		t.Errorf("event reads %q", e.Text)
	}
	// GitHub does not say who asked without a second, costlier query.
	if e.Actor != "" {
		t.Errorf("actor is %q; it should be left empty rather than guessed", e.Actor)
	}
}

func TestAReviewRequestOfSomeoneElseIsNotYours(t *testing.T) {
	prev := watched()
	next := watched()
	next.ReviewRequests = []Reviewer{{Login: "dana-quill", State: "PENDING"}}

	if _, ok := byKind(diffOne(prev, next))[EventReviewRequested]; ok {
		t.Error("someone else's review request was reported as yours")
	}
}

func TestAStandingReviewRequestIsReportedOnlyOnce(t *testing.T) {
	prev := watched()
	prev.ReviewRequests = []Reviewer{{Login: "jack", State: "PENDING"}}
	next := prev

	if _, ok := byKind(diffOne(prev, next))[EventReviewRequested]; ok {
		t.Error("a request that was already outstanding was announced again")
	}
}

func TestBeingMentionedIsReported(t *testing.T) {
	prev := watched()
	next := watched()
	next.IssueComments = 4
	next.LastMentionAt, next.LastMentionBy = mentionBase, "dana-quill"

	got := byKind(diffOne(prev, next))
	e, ok := got[EventMention]
	if !ok {
		t.Fatalf("no mention event: %+v", got)
	}
	if e.Text != "mentioned you" {
		t.Errorf("event reads %q", e.Text)
	}
	if e.Actor != "dana-quill" {
		t.Errorf("mention attributed to %q", e.Actor)
	}
}

// The mention stands in for the comment line rather than joining it: two rows
// at the same second on the same pull request would say one thing twice.
func TestAMentionReplacesTheCommentLine(t *testing.T) {
	prev := watched()
	next := watched()
	next.IssueComments = 6
	next.LastMentionAt, next.LastMentionBy = mentionBase, "dana-quill"

	got := byKind(diffOne(prev, next))
	if _, ok := got[EventMention]; !ok {
		t.Fatal("the mention itself went missing")
	}
	if e, ok := got[EventComment]; ok {
		t.Errorf("the comment line was reported alongside the mention: %q", e.Text)
	}
}

func TestCommentsWithoutAMentionStillReport(t *testing.T) {
	prev := watched()
	next := watched()
	next.IssueComments = 4

	got := byKind(diffOne(prev, next))
	if _, ok := got[EventComment]; !ok {
		t.Error("an ordinary comment stopped being reported")
	}
	if _, ok := got[EventMention]; ok {
		t.Error("a mention was invented from an ordinary comment")
	}
}

// The scanned window slides: a mention can drop out of the last few comments
// as newer ones arrive. That is a regression in the timestamp, and must not be
// mistaken for a fresh mention.
func TestAMentionFallingOutOfTheWindowIsNotReAnnounced(t *testing.T) {
	prev := watched()
	prev.LastMentionAt, prev.LastMentionBy = mentionBase, "dana-quill"
	next := watched()
	next.IssueComments = 9 // three newer comments pushed the mention out of view

	if _, ok := byKind(diffOne(prev, next))[EventMention]; ok {
		t.Error("a mention scrolling out of the query window was reported as new")
	}
}

// A review body that names you says two things — the verdict and the summons —
// and both are worth a line.
func TestAMentionInsideAReviewKeepsTheVerdict(t *testing.T) {
	prev := watched()
	next := watched()
	next.Reviewers = []Reviewer{{Login: "dana-quill", State: "CHANGES_REQUESTED", At: mentionBase}}
	next.LastMentionAt, next.LastMentionBy = mentionBase, "dana-quill"

	got := byKind(diffOne(prev, next))
	if _, ok := got[EventMention]; !ok {
		t.Error("the mention was dropped")
	}
	if e, ok := got[EventReview]; !ok || e.Text != "changes requested" {
		t.Errorf("the review verdict was lost: %+v", got[EventReview])
	}
}

func TestNoViewerMeansNeitherEvent(t *testing.T) {
	prev := watched()
	next := watched()
	next.ReviewRequests = []Reviewer{{Login: "jack", State: "PENDING"}}
	next.LastMentionAt, next.LastMentionBy = mentionBase, "dana-quill"

	events := Diff([]PR{prev}, []PR{next}, DiffOpts{Now: mentionBase, PrevComplete: true})
	got := byKind(events)
	if _, ok := got[EventReviewRequested]; ok {
		t.Error("a review request was claimed as yours with nobody logged in")
	}
	// A mention needs a login to have been found in the first place, so the
	// only honest thing an empty viewer can produce here is nothing.
	if _, ok := got[EventMention]; ok {
		t.Error("a mention was reported with nobody logged in")
	}
}
