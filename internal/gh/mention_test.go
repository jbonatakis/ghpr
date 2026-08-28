package gh

import (
	"testing"
	"time"
)

func TestMentionsRespectsLoginBoundaries(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
		why  string
	}{
		{"@jack can you take a look?", true, "plain mention"},
		{"cc @Jack", true, "logins are case-insensitive"},
		{"(@jack)", true, "punctuation on both sides"},
		{"ping @jack, please", true, "trailing comma"},
		{"@jack", true, "whole body"},
		{"", false, "empty body"},
		{"jack knows this code", false, "the name without an @"},
		{"@jack-bonatakis owns this", false, "a longer login that starts the same"},
		{"@acme/jack", false, "a team path, not this person"},
		{"@jack/reviewers", false, "a team under a matching name"},
		{"mail notify@jack.example", false, "an address, not a mention"},
		{"see PR@jack", false, "@ glued to a preceding word"},
	} {
		if got := Mentions(tc.text, "jack"); got != tc.want {
			t.Errorf("Mentions(%q) = %v, want %v — %s", tc.text, got, tc.want, tc.why)
		}
	}
}

// node builds the smallest GraphQL node convert will accept.
func node(body string) prNode {
	n := prNode{
		Typename:  "PullRequest",
		Number:    7,
		CreatedAt: "2026-08-01T10:00:00Z",
		UpdatedAt: "2026-08-20T10:00:00Z",
		BodyText:  body,
	}
	n.Repository.NameWithOwner = "acme/starfield"
	n.Author.Login = "morgan-bell"
	return n
}

func addComment(n *prNode, login, at, body string) {
	var c struct {
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		CreatedAt string `json:"createdAt"`
		BodyText  string `json:"bodyText"`
	}
	c.Author.Login = login
	c.CreatedAt = at
	c.BodyText = body
	n.Comments.Nodes = append(n.Comments.Nodes, c)
	n.Comments.TotalCount = len(n.Comments.Nodes)
}

func addReview(n *prNode, login, state, at, body string) {
	var r struct {
		State  string `json:"state"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		SubmittedAt string `json:"submittedAt"`
		BodyText    string `json:"bodyText"`
	}
	r.State = state
	r.Author.Login = login
	r.SubmittedAt = at
	r.BodyText = body
	n.LatestReviews.Nodes = append(n.LatestReviews.Nodes, r)
}

func addRequest(n *prNode, name string, team bool) {
	var q struct {
		RequestedReviewer struct {
			Typename string `json:"__typename"`
			Login    string `json:"login"`
			Name     string `json:"name"`
		} `json:"requestedReviewer"`
	}
	if team {
		q.RequestedReviewer.Typename, q.RequestedReviewer.Name = "Team", name
	} else {
		q.RequestedReviewer.Typename, q.RequestedReviewer.Login = "User", name
	}
	n.ReviewRequests.Nodes = append(n.ReviewRequests.Nodes, q)
}

func TestConvertKeepsTheNewestMention(t *testing.T) {
	n := node("nothing for anyone here")
	addComment(&n, "morgan-bell", "2026-08-02T10:00:00Z", "@jack early thought")
	addComment(&n, "dana-quill", "2026-08-05T10:00:00Z", "unrelated chatter")
	addReview(&n, "dana-quill", "CHANGES_REQUESTED", "2026-08-06T10:00:00Z", "@jack this needs you")

	p := convert(n, "jack")
	if p.LastMentionBy != "dana-quill" {
		t.Errorf("mention attributed to %q, want the most recent one", p.LastMentionBy)
	}
	if want := "2026-08-06T10:00:00Z"; p.LastMentionAt.Format(time.RFC3339) != want {
		t.Errorf("mention dated %s, want %s", p.LastMentionAt.Format(time.RFC3339), want)
	}
}

// A description that names you is dated by the pull request's creation, not its
// last update. Dating it by UpdatedAt would re-announce the same standing
// mention every time anything at all happened to the pull request.
func TestDescriptionMentionIsDatedByCreation(t *testing.T) {
	p := convert(node("cc @jack for context"), "jack")
	if want := "2026-08-01T10:00:00Z"; p.LastMentionAt.Format(time.RFC3339) != want {
		t.Errorf("description mention dated %s, want the creation time %s",
			p.LastMentionAt.Format(time.RFC3339), want)
	}
	if p.LastMentionBy != "morgan-bell" {
		t.Errorf("description mention attributed to %q, want the author", p.LastMentionBy)
	}
}

func TestYourOwnMentionsAreNotYours(t *testing.T) {
	n := node("")
	addComment(&n, "jack", "2026-08-05T10:00:00Z", "as @jack said earlier")

	if p := convert(n, "jack"); !p.LastMentionAt.IsZero() {
		t.Errorf("quoting your own handle counted as a mention, by %q", p.LastMentionBy)
	}
}

func TestNoViewerMeansNoMentions(t *testing.T) {
	n := node("@jack look at this")
	if p := convert(n, ""); !p.LastMentionAt.IsZero() {
		t.Error("a mention was recorded with nobody logged in")
	}
}

// Reviewers merges a review already given over a later request from the same
// person, which is exactly the case that must not hide a re-request.
func TestReReviewRequestSurvivesTheReviewerMerge(t *testing.T) {
	n := node("")
	addReview(&n, "jack", "APPROVED", "2026-08-04T10:00:00Z", "looks good")
	addRequest(&n, "jack", false)

	p := convert(n, "jack")
	if !p.ReviewRequestedFrom("jack") {
		t.Error("a re-request after an approval was lost")
	}
	if len(p.Reviewers) != 1 || p.Reviewers[0].State != "APPROVED" {
		t.Errorf("the displayed reviewer list changed: %+v", p.Reviewers)
	}
}

func TestTeamRequestsAreNotReadAsPersonal(t *testing.T) {
	n := node("")
	addRequest(&n, "jack", true) // a team that happens to share the name

	if convert(n, "jack").ReviewRequestedFrom("jack") {
		t.Error("a team request was read as a request of this person")
	}
}

func TestReviewRequestIsCaseInsensitive(t *testing.T) {
	n := node("")
	addRequest(&n, "Jack", false)

	if !convert(n, "jack").ReviewRequestedFrom("jack") {
		t.Error("a login differing only in case was missed")
	}
}
