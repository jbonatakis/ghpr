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

	Labels    []Label
	Reviewers []Reviewer
	Checks    []Check

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
