package gh

import (
	"fmt"
	"strings"
	"time"
)

// CheckState is a normalized CI state, collapsing GitHub's separate
// CheckRun (status+conclusion) and StatusContext (state) vocabularies.
type CheckState int

const (
	CheckNone CheckState = iota
	CheckSkipped
	CheckSuccess
	CheckPending
	CheckFailure
)

func (c CheckState) String() string {
	switch c {
	case CheckSuccess:
		return "passing"
	case CheckPending:
		return "running"
	case CheckFailure:
		return "failing"
	case CheckSkipped:
		return "skipped"
	}
	return "no checks"
}

// Check is a single CI check run or commit status on the head commit.
type Check struct {
	Name  string
	State CheckState
	Raw   string // original conclusion/state, for the detail pane
	URL   string
}

// Reviewer is a person or team either requested for review or who has
// already left one. State is APPROVED, CHANGES_REQUESTED, COMMENTED,
// DISMISSED, or PENDING for a request that hasn't been acted on.
type Reviewer struct {
	Login string
	State string
	Team  bool
	At    time.Time // when the review was submitted; zero for a pending request
}

func (r Reviewer) Pending() bool { return r.State == "PENDING" }

// Comment is one conversation comment the search returned. Only the newest
// few come back, which is enough to attribute activity and to seed the feed,
// and never enough to call it the pull request's history.
type Comment struct {
	By string
	At time.Time

	// Mention marks a comment that named the viewer, so the feed can show the
	// louder line instead of this one rather than both.
	Mention bool
}

// Push is one commit on the branch, dated. The polling query fetches only the
// head; the backfill fetches the last twenty, so a day of work reads as a day
// of work rather than a single line.
type Push struct {
	By string
	At time.Time
}

// Mention is one @mention of the viewer: when it was written, and by whom.
type Mention struct {
	By string
	At time.Time
}

type Label struct {
	Name  string
	Color string // hex, no leading #
}

// Status is the single bucket a PR falls into, ordered by how much it
// wants the author's attention. Lower sorts first.
type Status int

const (
	StatusReadyToMerge Status = iota
	StatusChangesRequested
	StatusChecksFailing
	StatusConflicts
	StatusUnresolved
	StatusAwaitingReview
	StatusDraft
)

func (s Status) Label() string {
	switch s {
	case StatusReadyToMerge:
		return "ready to merge"
	case StatusChangesRequested:
		return "changes requested"
	case StatusChecksFailing:
		return "checks failing"
	case StatusConflicts:
		return "conflicts"
	case StatusUnresolved:
		return "unresolved comments"
	case StatusAwaitingReview:
		return "awaiting review"
	case StatusDraft:
		return "draft"
	}
	return "unknown"
}

// Short is the compact form shown in the PR row.
func (s Status) Short() string {
	switch s {
	case StatusReadyToMerge:
		return "READY"
	case StatusChangesRequested:
		return "CHANGES"
	case StatusChecksFailing:
		return "FAILING"
	case StatusConflicts:
		return "CONFLICT"
	case StatusUnresolved:
		return "COMMENTS"
	case StatusAwaitingReview:
		return "REVIEW"
	case StatusDraft:
		return "DRAFT"
	}
	return "?"
}

// PR is a normalized pull request, flattened from the GraphQL response.
type PR struct {
	ID           string // GraphQL node id, used to confirm a vanished PR's fate
	Repo         string
	Number       int
	Title        string
	URL          string
	Author       string
	IsDraft      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Additions    int
	Deletions    int
	ChangedFiles int
	Mergeable    string // MERGEABLE, CONFLICTING, UNKNOWN
	HeadRef      string
	HeadOID      string
	BaseRef      string

	ReviewDecision    string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	IssueComments     int
	ReviewComments    int
	TotalThreads      int
	UnresolvedThreads int
	// ThreadsTruncated means the PR has more review threads than one page of
	// the query returns, so the comment and unresolved tallies are lower
	// bounds. The UI marks these with a trailing "+" rather than lying.
	ThreadsTruncated bool

	// Attribution for the activity feed, so a change can name who made it.
	LastCommentBy string
	LastCommentAt time.Time
	LastReviewBy  string
	LastReviewAt  time.Time
	PushedBy      string

	// The most recent @mention of the viewer that the query can see, and who
	// wrote it. Only the sources the search already pays for are scanned: the
	// description, the last few conversation comments and each reviewer's
	// latest review body. A mention buried in an older comment, or inside a
	// review thread, is not visible here.
	LastMentionBy string
	LastMentionAt time.Time

	// Dated history from this one snapshot, used to seed the feed at startup
	// with what happened before the dashboard was open. Every one of these is
	// a lower bound: the query returns the newest few comments and one review
	// per reviewer, not everything that happened.
	RecentComments []Comment
	Mentions       []Mention
	PushedAt       time.Time // the head commit's own commit date
	ChecksAt       time.Time // when the newest check on that commit finished

	// Filled in only by Backfill, which asks for far more than a poll can
	// afford. Where these are present they are what Seed reads, because the
	// fields above are the same history seen through a keyhole.
	ThreadComments []Comment  // comments inside review threads, with dates
	AllReviews     []Reviewer // every review, not the latest per reviewer
	Pushes         []Push     // the last twenty commits, dated
	State          State      // MERGED or CLOSED for a finished pull request
	FinishedAt     time.Time  // when it was merged or closed

	Labels    []Label
	Reviewers []Reviewer

	// ReviewRequests is the raw set of outstanding review requests, kept apart
	// from Reviewers because the two answer different questions. Reviewers is
	// a merged view for display, where a review already given hides a later
	// re-request from the same person; ReviewRequests is what "you were asked
	// to review this" has to be read from, re-requests included.
	ReviewRequests []Reviewer

	Checks []Check

	ChecksState   CheckState
	ChecksPassed  int
	ChecksFailed  int
	ChecksPending int
}

// Key uniquely identifies a PR across repos.
func (p PR) Key() string { return fmt.Sprintf("%s#%d", p.Repo, p.Number) }

// Org is the owner half of the repository name.
func (p PR) Org() string {
	if i := strings.IndexByte(p.Repo, '/'); i >= 0 {
		return p.Repo[:i]
	}
	return p.Repo
}

// RepoName is the repository without its owner.
func (p PR) RepoName() string {
	if i := strings.IndexByte(p.Repo, '/'); i >= 0 {
		return p.Repo[i+1:]
	}
	return p.Repo
}

// Comments counts everything a human wrote: conversation comments plus
// every comment inside a review thread.
func (p PR) Comments() int { return p.IssueComments + p.ReviewComments }

func (p PR) Conflicted() bool { return p.Mergeable == "CONFLICTING" }
func (p PR) Approved() bool   { return p.ReviewDecision == "APPROVED" }

func (p PR) Age(now time.Time) time.Duration  { return now.Sub(p.CreatedAt) }
func (p PR) Idle(now time.Time) time.Duration { return now.Sub(p.UpdatedAt) }

// Status buckets the PR by what it needs next.
func (p PR) Status() Status {
	switch {
	case p.IsDraft:
		return StatusDraft
	case p.ReviewDecision == "CHANGES_REQUESTED":
		return StatusChangesRequested
	case p.ChecksState == CheckFailure:
		return StatusChecksFailing
	case p.Conflicted():
		return StatusConflicts
	case p.Approved() && p.ChecksState != CheckPending:
		return StatusReadyToMerge
	case p.UnresolvedThreads > 0:
		return StatusUnresolved
	}
	return StatusAwaitingReview
}

// ReviewRequestedFrom reports whether a review is currently being asked of
// this person. Team requests are excluded: knowing the team was asked says
// nothing about whether this particular person is on it.
func (p PR) ReviewRequestedFrom(login string) bool {
	if login == "" {
		return false
	}
	for _, r := range p.ReviewRequests {
		if !r.Team && strings.EqualFold(r.Login, login) {
			return true
		}
	}
	return false
}

// PendingReviewers are people whose review was requested but not yet given.
func (p PR) PendingReviewers() []string {
	var out []string
	for _, r := range p.Reviewers {
		if r.Pending() {
			out = append(out, r.Login)
		}
	}
	return out
}

// RateLimit reports the GraphQL budget so the footer can show headroom.
type RateLimit struct {
	Limit     int
	Remaining int
	Cost      int
	ResetAt   time.Time
}

// Result is one poll of the API.
type Result struct {
	PRs       []PR
	Viewer    string
	RateLimit RateLimit
	FetchedAt time.Time

	// Complete is true when the search was exhausted rather than cut short by
	// the caller's maximum. When it is false, a pull request missing from PRs
	// proves nothing: it may simply be past the cut-off.
	Complete bool
}
