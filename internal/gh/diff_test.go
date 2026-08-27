package gh

import (
	"testing"
	"time"
)

func base() PR {
	return PR{
		Repo: "acme/starfield", Number: 96, Title: "t",
		Mergeable: "MERGEABLE", ChecksState: CheckSuccess, HeadOID: "abc",
		CreatedAt: time.Now().Add(-30 * 24 * time.Hour),
	}
}

func kinds(events []Event) []EventKind {
	out := make([]EventKind, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}

func hasKind(events []Event, k EventKind) bool {
	for _, e := range events {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func TestDiffIgnoresFirstLoad(t *testing.T) {
	if got := Diff(nil, []PR{base()}, DiffOpts{Now: time.Now(), PrevComplete: true}); got != nil {
		t.Errorf("first load produced %v, want no events", kinds(got))
	}
}

func TestDiffDetectsQuietSnapshotAsNoChange(t *testing.T) {
	prev := []PR{base()}
	if got := Diff(prev, []PR{base()}, DiffOpts{Now: time.Now(), PrevComplete: true}); len(got) != 0 {
		t.Errorf("unchanged snapshot produced %v", kinds(got))
	}
}

func TestDiffDetectsGenuinelyNewPRs(t *testing.T) {
	old := base()
	fresh := base()
	fresh.Number = 97
	fresh.CreatedAt = time.Now().Add(-2 * time.Minute)

	events := Diff([]PR{old}, []PR{old, fresh}, DiffOpts{Now: time.Now(), PrevComplete: true})
	if !hasKind(events, EventOpened) {
		t.Errorf("a just-created PR should report as opened, got %v", kinds(events))
	}
}

// TestDiffDoesNotCallAnOldPRNewlyOpened is the regression test for bursts of
// "opened" events for pull requests months old, which had simply been missed
// by an earlier poll's pagination.
func TestDiffDoesNotCallAnOldPRNewlyOpened(t *testing.T) {
	old := base()
	appeared := base()
	appeared.Number = 130
	appeared.CreatedAt = time.Now().Add(-110 * 24 * time.Hour)

	events := Diff([]PR{old}, []PR{old, appeared}, DiffOpts{Now: time.Now(), PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventOpened {
			t.Errorf("a %s-old PR was announced as opened: %+v",
				time.Since(appeared.CreatedAt).Round(time.Hour), e)
		}
	}
	if len(events) != 0 {
		t.Errorf("entering the view is not activity, got %v", kinds(events))
	}
}

func TestDiffIgnoresAnUnknownCreationTime(t *testing.T) {
	old := base()
	appeared := base()
	appeared.Number = 131
	appeared.CreatedAt = time.Time{} // never parsed

	events := Diff([]PR{old}, []PR{old, appeared}, DiffOpts{Now: time.Now(), PrevComplete: true})
	if hasKind(events, EventOpened) {
		t.Error("without a creation time we cannot claim a PR is new")
	}
}

func TestNewlyOpenedWindowSeparatesRealFromArtifact(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		age  time.Duration
		want bool
	}{
		{time.Minute, true},
		{30 * time.Minute, true},
		{NewlyOpenedWithin - time.Minute, true},
		{NewlyOpenedWithin + time.Minute, false},
		{4 * time.Hour, false},       // the youngest PR in the real fixture
		{26 * 24 * time.Hour, false}, // the youngest of the falsely-reported burst
	} {
		appeared := base()
		appeared.Number = 500
		appeared.CreatedAt = now.Add(-tc.age)
		got := hasKind(Diff([]PR{base()}, []PR{base(), appeared}, DiffOpts{Now: now, PrevComplete: true}), EventOpened)
		if got != tc.want {
			t.Errorf("age %s: opened=%v, want %v", tc.age, got, tc.want)
		}
	}
}

// TestDiffNeverInfersClosureFromAbsence pins the rule that caused a PR sitting
// on a search page boundary to be announced as "merged or closed" and then
// "opened" again on the next poll.
func TestDiffNeverInfersClosureFromAbsence(t *testing.T) {
	events := Diff([]PR{base()}, nil, DiffOpts{Now: time.Now(), PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventClosed || e.Kind == EventMerged {
			t.Errorf("Diff claimed a PR finished purely because it was absent: %+v", e)
		}
	}
	if len(events) != 0 {
		t.Errorf("a vanished PR should produce no events, got %v", kinds(events))
	}
}

func TestVanishedReportsCandidates(t *testing.T) {
	a, b := base(), base()
	b.Number = 97

	got := Vanished([]PR{a, b}, []PR{a})
	if len(got) != 1 || got[0].Number != 97 {
		t.Fatalf("Vanished = %+v, want just #97", got)
	}
	if len(Vanished([]PR{a, b}, []PR{a, b})) != 0 {
		t.Error("nothing should be vanished when both are still present")
	}
	if len(Vanished(nil, []PR{a})) != 0 {
		t.Error("a newly seen PR is not vanished")
	}
}

func TestClosureEventDistinguishesMergedFromClosed(t *testing.T) {
	now := time.Now()

	merged := ClosureEvent(base(), StateMerged, now)
	if merged.Kind != EventMerged || merged.Text != "merged" {
		t.Errorf("merged event = %+v", merged)
	}
	closed := ClosureEvent(base(), StateClosed, now)
	if closed.Kind != EventClosed || closed.Text != "closed" {
		t.Errorf("closed event = %+v", closed)
	}
	if merged.Number != 96 || merged.Repo != "o/r" && merged.Repo == "" {
		t.Errorf("closure event lost the PR's identity: %+v", merged)
	}
}

func TestDiffDetectsComments(t *testing.T) {
	old := base()
	next := base()
	next.IssueComments = 2
	next.ReviewComments = 1

	events := Diff([]PR{old}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
	if !hasKind(events, EventComment) {
		t.Fatalf("missing EventComment, got %v", kinds(events))
	}
	for _, e := range events {
		if e.Kind == EventComment && e.Text != "3 new comments" {
			t.Errorf("comment text = %q, want %q", e.Text, "3 new comments")
		}
	}
}

func TestDiffIgnoresCommentsGoingDown(t *testing.T) {
	old := base()
	old.IssueComments = 5
	next := base()
	next.IssueComments = 2

	if events := Diff([]PR{old}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true}); hasKind(events, EventComment) {
		t.Errorf("a deleted comment should not report new activity: %v", kinds(events))
	}
}

func TestDiffDetectsChecksPushesAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PR)
		want   EventKind
	}{
		{"checks", func(p *PR) { p.ChecksState = CheckFailure }, EventChecks},
		{"conflict", func(p *PR) { p.Mergeable = "CONFLICTING" }, EventConflict},
		{"push", func(p *PR) { p.HeadOID = "def" }, EventPush},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := base()
			tc.change(&next)
			events := Diff([]PR{base()}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
			if !hasKind(events, tc.want) {
				t.Errorf("got %v, want %v", kinds(events), tc.want)
			}
		})
	}
}

// TestDiffNamesTheReviewer checks a verdict is attributed to the person who
// gave it, rather than only reporting that the PR's overall state moved.
func TestDiffNamesTheReviewer(t *testing.T) {
	for _, tc := range []struct {
		state string
		text  string
	}{
		{"APPROVED", "approved"},
		{"CHANGES_REQUESTED", "changes requested"},
		{"DISMISSED", "review dismissed"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			next := base()
			next.Reviewers = []Reviewer{{Login: "morgan-bell", State: tc.state, At: time.Now()}}

			events := Diff([]PR{base()}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
			var found *Event
			for i := range events {
				if events[i].Kind == EventReview {
					found = &events[i]
				}
			}
			if found == nil {
				t.Fatalf("no review event, got %v", kinds(events))
			}
			if found.Actor != "morgan-bell" {
				t.Errorf("Actor = %q, want morgan-bell", found.Actor)
			}
			if found.Text != tc.text {
				t.Errorf("Text = %q, want %q", found.Text, tc.text)
			}
		})
	}
}

func TestDiffIgnoresPendingAndCommentedReviewStates(t *testing.T) {
	for _, state := range []string{"PENDING", "COMMENTED"} {
		next := base()
		next.Reviewers = []Reviewer{{Login: "morgan-bell", State: state}}
		if hasKind(Diff([]PR{base()}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true}), EventReview) {
			t.Errorf("%s is not a verdict and should not be reported as one", state)
		}
	}
}

func TestDiffDoesNotRepeatAnUnchangedReview(t *testing.T) {
	prev := base()
	prev.Reviewers = []Reviewer{{Login: "morgan-bell", State: "APPROVED"}}
	next := prev

	if hasKind(Diff([]PR{prev}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true}), EventReview) {
		t.Error("a standing approval should not be re-reported every poll")
	}
}

func TestDiffNamesEachReviewerSeparately(t *testing.T) {
	prev := base()
	prev.Reviewers = []Reviewer{{Login: "morgan-bell", State: "APPROVED"}}
	next := base()
	next.Reviewers = []Reviewer{
		{Login: "morgan-bell", State: "APPROVED"},
		{Login: "priya-shah", State: "CHANGES_REQUESTED"},
	}

	events := Diff([]PR{prev}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
	var actors []string
	for _, e := range events {
		if e.Kind == EventReview {
			actors = append(actors, e.Actor)
		}
	}
	if len(actors) != 1 || actors[0] != "priya-shah" {
		t.Errorf("review actors = %v, want just priya-shah", actors)
	}
}

func TestDiffNamesTheCommenter(t *testing.T) {
	now := time.Now()
	prev := base()
	next := base()
	next.IssueComments = 1
	next.LastCommentBy = "morgan-bell"
	next.LastCommentAt = now

	events := Diff([]PR{prev}, []PR{next}, DiffOpts{Now: now, PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventComment {
			if e.Actor != "morgan-bell" {
				t.Errorf("Actor = %q, want morgan-bell", e.Actor)
			}
			return
		}
	}
	t.Fatalf("no comment event, got %v", kinds(events))
}

// TestCommentActorPrefersTheMoreRecentSource covers a review-thread comment,
// which carries no author of its own and must be attributed via the review.
func TestCommentActorPrefersTheMoreRecentSource(t *testing.T) {
	now := time.Now()
	prev := base()
	prev.LastCommentBy, prev.LastCommentAt = "olduser", now.Add(-time.Hour)

	next := prev
	next.ReviewComments = 3 // arrived in a review thread, so no comment author
	next.LastReviewBy, next.LastReviewAt = "priya-shah", now

	events := Diff([]PR{prev}, []PR{next}, DiffOpts{Now: now, PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventComment {
			if e.Actor != "priya-shah" {
				t.Errorf("Actor = %q, want the reviewer who submitted them", e.Actor)
			}
			return
		}
	}
	t.Fatal("no comment event")
}

func TestCommentActorIsEmptyWhenUnknowable(t *testing.T) {
	prev := base()
	next := base()
	next.ReviewComments = 2 // no author information at all

	events := Diff([]PR{prev}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventComment && e.Actor != "" {
			t.Errorf("Actor = %q, want empty rather than a guess", e.Actor)
		}
	}
}

func TestDiffNamesThePusher(t *testing.T) {
	next := base()
	next.HeadOID = "def"
	next.PushedBy = "octo-dev"

	events := Diff([]PR{base()}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true})
	for _, e := range events {
		if e.Kind == EventPush {
			if e.Actor != "octo-dev" {
				t.Errorf("Actor = %q, want octo-dev", e.Actor)
			}
			return
		}
	}
	t.Fatal("no push event")
}

func TestChecksHaveNoActor(t *testing.T) {
	next := base()
	next.ChecksState = CheckFailure

	for _, e := range Diff([]PR{base()}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true}) {
		if e.Kind == EventChecks && e.Actor != "" {
			t.Errorf("checks should have no person attached, got %q", e.Actor)
		}
	}
}

func TestDiffDetectsReadyForReview(t *testing.T) {
	old := base()
	old.IsDraft = true
	events := Diff([]PR{old}, []PR{base()}, DiffOpts{Now: time.Now(), PrevComplete: true})
	if !hasKind(events, EventReadyForReview) {
		t.Errorf("got %v, want EventReadyForReview", kinds(events))
	}
}

func TestDiffIgnoresPushWhenOIDUnknown(t *testing.T) {
	old := base()
	old.HeadOID = ""
	next := base()
	if events := Diff([]PR{old}, []PR{next}, DiffOpts{Now: time.Now(), PrevComplete: true}); hasKind(events, EventPush) {
		t.Error("an unknown previous OID should not be reported as a push")
	}
}

// --- arrivals ------------------------------------------------------------

// TestBeingAddedAsReviewerIsReported covers an existing pull request entering
// the review-requested search: the viewer has just been asked to review it,
// which is the point of watching that search, but the pull request itself is
// not new so no "opened" event applies.
func TestBeingAddedAsReviewerIsReported(t *testing.T) {
	now := time.Now()
	arrived := base()
	arrived.Number = 500
	arrived.Author = "priya-shah"
	arrived.CreatedAt = now.Add(-5 * 24 * time.Hour) // opened days ago
	arrived.UpdatedAt = now.Add(-20 * time.Second)   // the request just landed

	events := Diff([]PR{base()}, []PR{base(), arrived}, DiffOpts{
		Now: now, PrevComplete: true, Mode: ModeReviewRequested,
	})

	var found *Event
	for i := range events {
		if events[i].Kind == EventArrived {
			found = &events[i]
		}
	}
	if found == nil {
		t.Fatalf("no arrival event, got %v", kinds(events))
	}
	if found.Text != "review requested" {
		t.Errorf("Text = %q, want %q", found.Text, "review requested")
	}
	if found.Actor != "priya-shah" {
		t.Errorf("Actor = %q, want the pull request's author", found.Actor)
	}
	if hasKind(events, EventOpened) {
		t.Error("a five-day-old pull request should not also report as opened")
	}
}

func TestArrivalTextFollowsTheMode(t *testing.T) {
	now := time.Now()
	arrived := base()
	arrived.Number = 501
	arrived.CreatedAt = now.Add(-5 * 24 * time.Hour)
	arrived.UpdatedAt = now.Add(-time.Minute)

	for mode, want := range map[Mode]string{
		ModeReviewRequested: "review requested",
		ModeInvolved:        "now involves you",
		ModeAuthored:        "now listed",
	} {
		events := Diff([]PR{base()}, []PR{base(), arrived}, DiffOpts{
			Now: now, PrevComplete: true, Mode: mode,
		})
		var text string
		for _, e := range events {
			if e.Kind == EventArrived {
				text = e.Text
			}
		}
		if text != want {
			t.Errorf("%v: text = %q, want %q", mode, text, want)
		}
	}
}

// TestStalePullRequestAppearingIsStillSilent keeps the earlier fix intact: the
// pull requests that appeared spuriously had not been touched in weeks.
func TestStalePullRequestAppearingIsStillSilent(t *testing.T) {
	now := time.Now()
	for _, age := range []time.Duration{26 * 24 * time.Hour, 111 * 24 * time.Hour} {
		stale := base()
		stale.Number = 130
		stale.CreatedAt = now.Add(-age)
		stale.UpdatedAt = now.Add(-age)

		events := Diff([]PR{base()}, []PR{base(), stale}, DiffOpts{
			Now: now, PrevComplete: true, Mode: ModeReviewRequested,
		})
		if len(events) != 0 {
			t.Errorf("a PR untouched for %s produced %v", age.Round(24*time.Hour), kinds(events))
		}
	}
}

// TestArrivalNeedsACompletePreviousSnapshot stops a partial first poll from
// announcing everything it had missed.
func TestArrivalNeedsACompletePreviousSnapshot(t *testing.T) {
	now := time.Now()
	arrived := base()
	arrived.Number = 502
	arrived.CreatedAt = now.Add(-5 * 24 * time.Hour)
	arrived.UpdatedAt = now.Add(-time.Minute)

	events := Diff([]PR{base()}, []PR{base(), arrived}, DiffOpts{
		Now: now, PrevComplete: false, Mode: ModeReviewRequested,
	})
	if hasKind(events, EventArrived) {
		t.Error("an arrival cannot be concluded from an incomplete previous snapshot")
	}
}

func TestArrivalWindowBoundary(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		since time.Duration
		want  bool
	}{
		{time.Second, true},
		{ArrivedWithin - time.Minute, true},
		{ArrivedWithin + time.Minute, false},
		{24 * time.Hour, false},
	} {
		arrived := base()
		arrived.Number = 503
		arrived.CreatedAt = now.Add(-30 * 24 * time.Hour)
		arrived.UpdatedAt = now.Add(-tc.since)

		got := hasKind(Diff([]PR{base()}, []PR{base(), arrived}, DiffOpts{
			Now: now, PrevComplete: true, Mode: ModeReviewRequested,
		}), EventArrived)
		if got != tc.want {
			t.Errorf("updated %s ago: arrived=%v, want %v", tc.since, got, tc.want)
		}
	}
}

func TestNewPullRequestPrefersOpenedOverArrived(t *testing.T) {
	now := time.Now()
	fresh := base()
	fresh.Number = 504
	fresh.CreatedAt = now.Add(-2 * time.Minute)
	fresh.UpdatedAt = now.Add(-time.Minute)

	events := Diff([]PR{base()}, []PR{base(), fresh}, DiffOpts{
		Now: now, PrevComplete: true, Mode: ModeReviewRequested,
	})
	if !hasKind(events, EventOpened) {
		t.Error("a just-created PR should report as opened")
	}
	if hasKind(events, EventArrived) {
		t.Error("it should not be reported twice")
	}
}
