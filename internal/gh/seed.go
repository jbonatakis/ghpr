package gh

import (
	"sort"
	"time"
)

// Seed reconstructs what happened since a point in time from a single snapshot,
// so a dashboard just opened does not start blank.
//
// This is a different kind of claim from the rest of the feed. Everything else
// is something ghpr watched change between two of its own polls; these are
// GitHub's timestamps, read once, and they are a floor rather than a record.
// One search returns the newest few conversation comments and one review per
// reviewer, so a busy hour is under-reported, and comments inside review
// threads carry no dates at all and cannot appear. Nothing here is inferred:
// every line has a timestamp from the API behind it, and anything that cannot
// be dated — a review request, a conflict appearing, a pull request that was
// merged and so is no longer in an is:open search — is simply absent.
func Seed(prs []PR, since time.Time, viewer string) []Event {
	var out []Event

	for _, p := range prs {
		add := func(at time.Time, kind EventKind, actor, text string) {
			if at.IsZero() || at.Before(since) {
				return
			}
			out = append(out, Event{
				At: at, Kind: kind, Key: p.Key(), Repo: p.Repo,
				Number: p.Number, Text: text, Actor: actor, URL: p.URL,
			})
		}

		add(p.CreatedAt, EventOpened, p.Author, "opened")

		for _, r := range p.Reviewers {
			switch r.State {
			case "APPROVED":
				add(r.At, EventReview, r.Login, "approved")
			case "CHANGES_REQUESTED":
				add(r.At, EventReview, r.Login, "changes requested")
			case "DISMISSED":
				add(r.At, EventReview, r.Login, "review dismissed")
			}
		}

		if viewer != "" {
			for _, mn := range p.Mentions {
				add(mn.At, EventMention, mn.By, "mentioned you")
			}
		}
		for _, c := range p.RecentComments {
			if c.Mention && viewer != "" {
				continue // already carried by the louder line above
			}
			// Dated individually rather than tallied: the live feed counts a
			// poll's worth of comments because it only knows the difference
			// between two totals, but here each one has its own timestamp and
			// author, and saying so is strictly more useful.
			add(c.At, EventComment, c.By, "new comment")
		}

		add(p.PushedAt, EventPush, p.PushedBy, "new commits")
		if p.ChecksState != CheckNone {
			add(p.ChecksAt, EventChecks, "", "checks "+p.ChecksState.String())
		}
	}

	sortEvents(out)
	return out
}

// SessionEvent is the line dividing the seeded history from what follows.
// Everything below it in the feed was reconstructed; everything above it was
// watched happening.
func SessionEvent(at time.Time) Event {
	return Event{At: at, Kind: EventSessionStart, Text: "ghpr started"}
}

// sortEvents puts a seeded batch in the order it happened, so the feed reads
// the same way whether a line was reconstructed or observed. Ties break on the
// pull request, so the result does not depend on the order the pages arrived.
func sortEvents(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		return a.Key < b.Key
	})
}
