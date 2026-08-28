package gh

import (
	"fmt"
	"time"
)

// EventKind classifies a change observed between two polls.
type EventKind int

const (
	EventOpened EventKind = iota
	EventArrived
	EventMerged
	EventClosed
	EventComment
	EventChecks
	EventReview
	EventReadyForReview
	EventConflict
	EventPush
	EventReviewRequested
	EventMention

	// EventSessionStart divides reconstructed history from the polled record.
	// It names no pull request: it is a line in the feed rather than something
	// that happened to one.
	EventSessionStart
)

func (k EventKind) Icon() string {
	switch k {
	case EventOpened:
		return "+"
	case EventArrived:
		return "→"
	case EventMerged:
		return "✔"
	case EventClosed:
		return "×"
	case EventComment:
		return "»"
	case EventChecks:
		return "~"
	case EventReview:
		return "★"
	case EventReadyForReview:
		return "▲"
	case EventConflict:
		return "!"
	case EventPush:
		return "↑"
	case EventReviewRequested:
		return "◷"
	case EventMention:
		return "@"
	case EventSessionStart:
		return "·"
	}
	return "."
}

// Event is a single human-readable change to a pull request.
type Event struct {
	At     time.Time
	Kind   EventKind
	Key    string
	Repo   string
	Number int
	Text   string

	// Actor is who caused the change, where that is knowable. Checks have no
	// person behind them, and a comment inside a review thread can only be
	// attributed via the review that carried it.
	Actor string

	// URL is the pull request this concerns, so the feed can make the
	// reference clickable.
	URL string
}

// NewlyOpenedWithin bounds how recently a pull request must have been created
// for its first appearance to count as having been opened.
//
// A pull request can turn up in a snapshot for reasons unrelated to being new:
// a paginated search reorders between page fetches, so one that was missed on
// an earlier poll simply shows up later. Announcing those as "opened" produced
// bursts of them for pull requests months old. The window is generous enough
// to absorb clock skew and GitHub's search-index lag, while remaining far
// smaller than the age of anything that appears as a paging artifact.
const NewlyOpenedWithin = time.Hour

// ArrivedWithin bounds how recently a pull request must have been touched for
// its first appearance to count as having genuinely arrived — most often
// because the viewer was just added as a reviewer, which bumps the pull
// request's updated time.
//
// Paging artifacts are old on this axis: the pull requests that once appeared
// spuriously had last been updated between 26 and 111 days earlier, so a short
// window separates a real arrival from one cleanly.
const ArrivedWithin = 15 * time.Minute

// DiffOpts describes the circumstances of a comparison, which decide what can
// honestly be concluded from it.
type DiffOpts struct {
	Now time.Time

	// PrevComplete says the previous snapshot was a full picture of the search.
	// Without it, a pull request appearing now may simply have been missed
	// before, so its arrival is not reported.
	PrevComplete bool

	// Mode phrases an arrival: entering a review-requested search means the
	// viewer has been asked to review something.
	Mode Mode

	// Viewer is the logged-in user. Two kinds of change are only interesting
	// because of who they are aimed at — a review asked of you, and a comment
	// that says your name — and without a login neither can be told apart from
	// the same thing happening to somebody else.
	Viewer string
}

// arrivalText describes a pull request entering the watched set.
func arrivalText(mode Mode) string {
	switch mode {
	case ModeReviewRequested:
		return "review requested"
	case ModeInvolved:
		return "now involves you"
	}
	return "now listed"
}

// Diff compares two snapshots and reports what changed about the pull requests
// present in both, plus any that were genuinely just opened.
//
// It deliberately says nothing about pull requests that have disappeared. A
// paginated search drops items for reasons unrelated to their state — chiefly
// a page boundary shifting when some other PR is updated mid-fetch — so
// absence is not evidence of closure. Use Vanished to collect candidates and
// Client.States to find out what actually happened to them.
func Diff(prev, next []PR, opts DiffOpts) []Event {
	if prev == nil {
		return nil // first load is not "activity"
	}
	now := opts.Now
	old := make(map[string]PR, len(prev))
	for _, p := range prev {
		old[p.Key()] = p
	}

	var events []Event
	add := func(p PR, kind EventKind, actor, format string, args ...any) {
		events = append(events, Event{
			At:     now,
			Kind:   kind,
			Key:    p.Key(),
			Repo:   p.Repo,
			Number: p.Number,
			Text:   fmt.Sprintf(format, args...),
			Actor:  actor,
			URL:    p.URL,
		})
	}

	for _, p := range next {
		o, existed := old[p.Key()]
		if !existed {
			switch {
			case !p.CreatedAt.IsZero() && now.Sub(p.CreatedAt) <= NewlyOpenedWithin:
				// Genuinely new: it was created moments ago.
				add(p, EventOpened, p.Author, "opened")
			case opts.PrevComplete && !p.UpdatedAt.IsZero() && now.Sub(p.UpdatedAt) <= ArrivedWithin:
				// Not new, but freshly touched and absent from a snapshot we
				// know was complete: it has just entered the watched set.
				add(p, EventArrived, p.Author, "%s", arrivalText(opts.Mode))
			}
			// Anything else merely surfaced in our view; that is not activity.
			continue
		}
		// A mention is a comment that named you, so it stands in for the
		// comment line rather than joining it: two rows at the same second on
		// the same pull request would say one thing twice, and the quieter of
		// the two would be the one left underneath.
		mentioned := opts.Viewer != "" && p.LastMentionAt.After(o.LastMentionAt)
		if n := p.Comments() - o.Comments(); n > 0 && !mentioned {
			add(p, EventComment, commentActor(o, p), "%s", plural(n, "new comment", "new comments"))
		}
		if mentioned {
			add(p, EventMention, p.LastMentionBy, "mentioned you")
		}
		if !o.ReviewRequestedFrom(opts.Viewer) && p.ReviewRequestedFrom(opts.Viewer) {
			// GitHub does not say who asked without a second, costlier query,
			// so the actor column is left empty rather than guessed at.
			add(p, EventReviewRequested, "", "review requested")
		}
		if p.ChecksState != o.ChecksState {
			add(p, EventChecks, "", "checks %s", p.ChecksState)
		}
		for _, r := range newReviews(o, p) {
			switch r.State {
			case "APPROVED":
				add(p, EventReview, r.Login, "approved")
			case "CHANGES_REQUESTED":
				add(p, EventReview, r.Login, "changes requested")
			case "DISMISSED":
				add(p, EventReview, r.Login, "review dismissed")
			}
		}
		if o.IsDraft && !p.IsDraft {
			add(p, EventReadyForReview, p.Author, "ready for review")
		}
		if !o.Conflicted() && p.Conflicted() {
			add(p, EventConflict, "", "now conflicting")
		}
		if o.HeadOID != "" && p.HeadOID != "" && o.HeadOID != p.HeadOID {
			add(p, EventPush, p.PushedBy, "new commits")
		}
	}
	return events
}

// Vanished returns the pull requests in prev that are absent from next. They
// are candidates for having been merged or closed, not proof of it.
func Vanished(prev, next []PR) []PR {
	present := make(map[string]bool, len(next))
	for _, p := range next {
		present[p.Key()] = true
	}
	var out []PR
	for _, p := range prev {
		if !present[p.Key()] {
			out = append(out, p)
		}
	}
	return out
}

// ClosureEvent describes a pull request that has genuinely left the list.
func ClosureEvent(p PR, o Outcome, now time.Time) Event {
	kind, text := EventClosed, "closed"
	if o.State == StateMerged {
		kind, text = EventMerged, "merged"
	}
	return Event{
		At: now, Kind: kind, Key: p.Key(), Repo: p.Repo,
		Number: p.Number, Text: text, Actor: o.By, URL: p.URL,
	}
}

// commentActor names who most likely left the new comments. A comment inside a
// review thread carries no author in the cheap form of the query, but the
// review that delivered it does, so whichever of the two is more recent — and
// newer than what the previous snapshot knew — is the best answer available.
func commentActor(o, p PR) string {
	var (
		who string
		at  time.Time
	)
	if p.LastCommentBy != "" && p.LastCommentAt.After(o.LastCommentAt) {
		who, at = p.LastCommentBy, p.LastCommentAt
	}
	if p.LastReviewBy != "" && p.LastReviewAt.After(o.LastReviewAt) && p.LastReviewAt.After(at) {
		who = p.LastReviewBy
	}
	return who
}

// newReviews returns reviews whose verdict changed since the previous snapshot,
// so the feed can name the reviewer rather than just the pull request's overall
// state.
func newReviews(o, p PR) []Reviewer {
	was := make(map[string]string, len(o.Reviewers))
	for _, r := range o.Reviewers {
		was[r.Login] = r.State
	}
	var out []Reviewer
	for _, r := range p.Reviewers {
		if r.State == "PENDING" || r.State == "COMMENTED" {
			continue // a pending request is not a verdict; comments are reported as comments
		}
		if prev, seen := was[r.Login]; !seen || prev != r.State {
			out = append(out, r)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
