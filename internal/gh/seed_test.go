package gh

import (
	"testing"
	"time"
)

var seedNow = time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) time.Time { return seedNow.Add(-d) }

// busy is a pull request with something of every dateable kind on it.
func busy() PR {
	return PR{
		Repo: "acme/starfield", Number: 44, URL: "https://example.invalid/44",
		Author: "morgan-bell", CreatedAt: ago(20 * time.Minute),
		Mergeable: "MERGEABLE", HeadOID: "abc",
		PushedAt:    ago(15 * time.Minute),
		ChecksState: CheckFailure, ChecksAt: ago(10 * time.Minute),
		Reviewers: []Reviewer{
			{Login: "dana-quill", State: "CHANGES_REQUESTED", At: ago(8 * time.Minute)},
		},
		RecentComments: []Comment{
			{By: "riley-shaw", At: ago(6 * time.Minute)},
			{By: "sam-okafor", At: ago(4 * time.Minute)},
		},
	}
}

func texts(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Text
	}
	return out
}

func has(events []Event, text string) bool {
	for _, e := range events {
		if e.Text == text {
			return true
		}
	}
	return false
}

func TestSeedRebuildsTheRecentPast(t *testing.T) {
	got := Seed([]PR{busy()}, ago(time.Hour), "jack")

	for _, want := range []string{
		"opened", "new commits", "checks failing", "changes requested", "new comment",
	} {
		if !has(got, want) {
			t.Errorf("seed is missing %q; got %v", want, texts(got))
		}
	}
}

// Each comment is dated individually here, because unlike a poll the seed has
// a timestamp and an author for every one of them.
func TestSeedDatesEachCommentSeparately(t *testing.T) {
	got := Seed([]PR{busy()}, ago(time.Hour), "jack")

	var comments int
	for _, e := range got {
		if e.Kind == EventComment {
			comments++
			if e.Actor == "" {
				t.Error("a seeded comment lost its author")
			}
		}
	}
	if comments != 2 {
		t.Errorf("seeded %d comment lines, want one per comment", comments)
	}
}

func TestSeedIsOrderedOldestFirst(t *testing.T) {
	got := Seed([]PR{busy()}, ago(time.Hour), "jack")
	for i := 1; i < len(got); i++ {
		if got[i].At.Before(got[i-1].At) {
			t.Fatalf("seed is out of order at %d: %v then %v", i, got[i-1].Text, got[i].Text)
		}
	}
}

func TestSeedStopsAtTheWindow(t *testing.T) {
	p := busy()
	p.CreatedAt = ago(30 * 24 * time.Hour) // opened a month ago
	p.PushedAt = ago(2 * time.Hour)

	got := Seed([]PR{p}, ago(5*time.Minute), "jack")
	if has(got, "opened") || has(got, "new commits") {
		t.Errorf("seed reached outside its window: %v", texts(got))
	}
	if !has(got, "new comment") {
		t.Errorf("seed dropped something inside its window: %v", texts(got))
	}
}

func TestSeedOfNothingIsNothing(t *testing.T) {
	if got := Seed([]PR{busy()}, seedNow, "jack"); len(got) != 0 {
		t.Errorf("a zero-length window produced %v", texts(got))
	}
	if got := Seed(nil, ago(time.Hour), "jack"); len(got) != 0 {
		t.Errorf("no pull requests produced %v", texts(got))
	}
}

// Same rule as the live feed: the mention is the louder line and stands in for
// the comment that carried it.
func TestSeedLetsAMentionReplaceItsComment(t *testing.T) {
	p := busy()
	p.RecentComments = []Comment{
		{By: "riley-shaw", At: ago(6 * time.Minute)},
		{By: "dana-quill", At: ago(4 * time.Minute), Mention: true},
	}
	p.Mentions = []Mention{{By: "dana-quill", At: ago(4 * time.Minute)}}

	got := Seed([]PR{p}, ago(time.Hour), "jack")
	if !has(got, "mentioned you") {
		t.Errorf("seed dropped the mention: %v", texts(got))
	}
	var comments int
	for _, e := range got {
		if e.Kind == EventComment {
			comments++
		}
	}
	if comments != 1 {
		t.Errorf("seeded %d comment lines, want the mentioning one replaced", comments)
	}
}

func TestSeedSaysNothingAboutYouWithNobodyLoggedIn(t *testing.T) {
	p := busy()
	p.RecentComments = []Comment{{By: "dana-quill", At: ago(4 * time.Minute), Mention: true}}
	p.Mentions = []Mention{{By: "dana-quill", At: ago(4 * time.Minute)}}

	got := Seed([]PR{p}, ago(time.Hour), "")
	if has(got, "mentioned you") {
		t.Error("a mention was seeded with nobody logged in")
	}
	if !has(got, "new comment") {
		t.Error("the comment should stand on its own when there is no mention to replace it")
	}
}

// The seed only claims what a timestamp backs. These have none anywhere in the
// response, so they must not appear at all rather than be dated by guesswork.
func TestSeedInventsNothingItCannotDate(t *testing.T) {
	p := busy()
	p.Mergeable = "CONFLICTING"
	p.ReviewRequests = []Reviewer{{Login: "jack", State: "PENDING"}}
	p.Reviewers = append(p.Reviewers, Reviewer{Login: "kim-rivera", State: "PENDING"})

	got := Seed([]PR{p}, ago(time.Hour), "jack")
	for _, unwanted := range []string{"now conflicting", "review requested"} {
		if has(got, unwanted) {
			t.Errorf("seed invented %q, which nothing in the response dates", unwanted)
		}
	}
}

func TestSeedCarriesTheLinkAndTheActor(t *testing.T) {
	got := Seed([]PR{busy()}, ago(time.Hour), "jack")
	for _, e := range got {
		if e.URL == "" {
			t.Errorf("%q is not clickable", e.Text)
		}
		if e.Key != "acme/starfield#44" {
			t.Errorf("%q has key %q", e.Text, e.Key)
		}
	}
}

func TestSessionEventNamesNoPullRequest(t *testing.T) {
	e := SessionEvent(seedNow)
	if e.Number != 0 || e.Key != "" || e.URL != "" {
		t.Errorf("the session marker points at a pull request: %+v", e)
	}
	if e.Kind != EventSessionStart {
		t.Errorf("kind is %v", e.Kind)
	}
}
