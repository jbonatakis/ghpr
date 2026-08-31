package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultEndpoint = "https://api.github.com/graphql"

// Client talks to the GitHub GraphQL API.
type Client struct {
	Endpoint string
	token    string
	http     *http.Client
}

func NewClient(token string) *Client {
	return &Client{
		Endpoint: defaultEndpoint,
		token:    token,
		http:     &http.Client{Timeout: 60 * time.Second},
	}
}

// ResolveToken finds a GitHub token, preferring the environment and
// falling back to the gh CLI's stored credentials.
func ResolveToken() (string, error) {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err == nil {
		if tok := strings.TrimSpace(string(out)); tok != "" {
			return tok, nil
		}
	}
	return "", fmt.Errorf("no GitHub token: set GITHUB_TOKEN or run `gh auth login`")
}

// Mode selects which set of pull requests to watch.
type Mode int

const (
	ModeAuthored Mode = iota
	ModeReviewRequested
	ModeInvolved
)

func (m Mode) String() string {
	switch m {
	case ModeReviewRequested:
		return "review-requested"
	case ModeInvolved:
		return "involved"
	}
	return "authored"
}

// Query builds the GitHub search expression for the mode.
func (m Mode) Query(extra string) string {
	var b strings.Builder
	b.WriteString("is:open is:pr archived:false ")
	b.WriteString(m.qualifier())
	b.WriteString(strings.TrimSpace(extra))
	return strings.TrimSpace(b.String())
}

// BackfillSearches are the searches that fill the activity feed in at startup.
//
// They are deliberately not the dashboard's search. The feed is a session-wide
// record that spans every mode — switching mode does not un-happen what it saw
// — so filling it from whichever mode happens to be selected contradicts the
// thing it is. A dashboard opened on your own pull requests would reconstruct a
// morning with everything you reviewed left out of it.
//
// Two of them because GitHub search cannot express the union: involves:@me
// covers authoring, commenting, assignment and mentions, but not a review
// merely requested of you and not yet acted on. Neither says is:open, so what
// was merged inside the window comes back too.
//
// Both are bounded by updated:>= rather than left open, which is what keeps
// this affordable: a pull request untouched inside the window has nothing to
// contribute to the seed, so fetching it is pure waste. GitHub's search dates
// are days rather than instants, so the bound is rounded down to midnight and
// Seed discards whatever falls outside the real window.
func BackfillSearches(extra string, since, now time.Time, shapes []Shape) []BackfillPlan {
	if len(shapes) == 0 {
		return nil
	}
	var out []BackfillPlan
	extra = strings.TrimSpace(extra)
	for i, w := range backfillWindows(since, now) {
		for _, shape := range shapes {
			q := "is:pr archived:false " + shape.qualifier() + w.qualifier() + " " + extra
			out = append(out, BackfillPlan{
				Query: strings.TrimSpace(q), From: w.from, To: w.to,
				Newest: w.newest, Window: i, Shape: shape, Needs: len(shapes),
			})
		}
	}
	return out
}

// A Shape is one way a pull request can matter to someone. GitHub cannot
// express their union in a single query, and each covers a gap the others
// leave, so the backfill runs one search per shape over every window.
type Shape string

const (
	// ShapeInvolved is author OR assignee OR mentions OR commenter. Reviewing
	// is not on that list, which is the gap the other two exist to close.
	ShapeInvolved Shape = "involved"

	// ShapeRequested is a review asked of you and not yet given — including one
	// asked of a team you are on, which is how CODEOWNERS assigns most reviews.
	// It stops matching the moment you submit the review.
	ShapeRequested Shape = "requested"

	// ShapeReviewed is a pull request you have already reviewed, which is where
	// the answer to your review lands and where requested stops looking.
	ShapeReviewed Shape = "reviewed"
)

// AllShapes is what the backfill covers unless told otherwise.
var AllShapes = []Shape{ShapeInvolved, ShapeRequested, ShapeReviewed}

func (s Shape) qualifier() string {
	switch s {
	case ShapeRequested:
		return "review-requested:@me "
	case ShapeReviewed:
		return "reviewed-by:@me "
	}
	return "involves:@me "
}

// ParseShapes reads a comma-separated selection, in the order given.
func ParseShapes(list string) ([]Shape, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return nil, nil
	}
	var out []Shape
	seen := map[Shape]bool{}
	for _, name := range strings.Split(list, ",") {
		s := Shape(strings.ToLower(strings.TrimSpace(name)))
		switch s {
		case "":
			continue
		case ShapeInvolved, ShapeRequested, ShapeReviewed:
		default:
			return nil, fmt.Errorf("unknown watch %q: want involved, requested or reviewed", name)
		}
		if !seen[s] {
			seen[s], out = true, append(out, s)
		}
	}
	return out, nil
}

// ShapeNames renders a selection for display and for the coverage scope.
func ShapeNames(shapes []Shape) string {
	names := make([]string, 0, len(shapes))
	for _, s := range shapes {
		names = append(names, string(s))
	}
	return strings.Join(names, ",")
}

// BackfillScope names what the searches reach, so a record of having covered
// some stretch of time can say what it covered it *with*.
//
// Coverage is a claim about a span and a scope together, and only the span was
// ever written down. When reviewed-by:@me was added the searches began reaching
// pull requests they had never reached before, but every stretch already marked
// as covered stayed marked — so the one thing that would have gone and found
// them was skipped, and a wider -seed changed nothing. Recording the scope
// alongside makes a claim expire when it stops being true.
func BackfillScope(extra string, shapes []Shape) string {
	return ShapeNames(shapes) + "|" + strings.TrimSpace(extra)
}

// BackfillPlan is one search the backfill will run.
//
// Window is its place in the sequence, counting back from the present. A caller
// that wants the feed to grow downwards rather than jump about has to release
// whole windows in that order. A window bounds updated, and a pull request's
// newest event is never later than its updated, so window n can contain nothing
// newer than where window n-1 begins — released in order, the top of the feed
// settles as soon as the first window lands and then stays settled. Released as
// they finish, a slow first window drops newer activity in above whatever the
// reader is already looking at.
type BackfillPlan struct {
	Query    string
	From, To time.Time
	Newest   bool
	Window   int
	Shape    Shape

	// Needs is how many searches cover this plan's window, so a caller can
	// tell when the window has been answered in full.
	Needs int
}

type backfillWindow struct {
	from, to time.Time
	newest   bool
}

// qualifier bounds a search to the window. The newest one is left open at the
// top so that anything updated while the backfill itself is running is still
// caught rather than falling into the gap behind it.
func (w backfillWindow) qualifier() string {
	const iso = "2006-01-02T15:04:05+00:00"
	if w.newest {
		return "updated:>=" + w.from.UTC().Format(iso)
	}
	return "updated:" + w.from.UTC().Format(iso) + ".." + w.to.UTC().Format(iso)
}

// The windows are not equal. They start narrow at the present and widen going
// back, because the two things they are asked to do want opposite shapes.
//
// A window's latency is set by how many pages it needs, and pages within one
// search are sequential — the cursor for the next is in the answer to the last.
// A half-hour of activity is almost always a single page; five days of it can
// be four or five round trips of a query heavy enough that each one is slow.
// Since windows are released in order, everything waits on the newest, so
// making that one as cheap as possible is what decides how long the feed sits
// empty after launch.
//
// Widening as they go back keeps the count logarithmic rather than linear: an
// even split fine enough to make the first window half an hour would cut a
// month into 1,440 searches. This gives eight. The old, wide ones are also
// exactly the searches the backlog cap is most likely to abandon before they
// ever run.
const (
	backfillFirstWindow = 30 * time.Minute
	backfillGrowth      = 3
	backfillMaxChunks   = 10
)

// backfillWindows divides a span into windows, newest first and narrowest
// first, so the most recent activity is both the first to be asked for and the
// quickest to come back.
//
// Adjacent bounds are inclusive at both ends, so a pull request updated exactly
// on a boundary comes back in both windows; callers de-duplicate by pull
// request anyway, because the two search shapes overlap regardless.
func backfillWindows(since, now time.Time) []backfillWindow {
	span := now.Sub(since)
	if span <= 0 {
		return nil
	}

	var (
		out   []backfillWindow
		to    = now
		width = backfillFirstWindow
	)
	for len(out) < backfillMaxChunks && to.After(since) {
		from := to.Add(-width)
		last := len(out) == backfillMaxChunks-1
		if !from.After(since) || last {
			// The final window absorbs whatever is left, however wide that is:
			// better one oversized search at the far end than a hole.
			from = since
		}
		out = append(out, backfillWindow{from: from, to: to, newest: len(out) == 0})
		to = from
		width *= backfillGrowth
	}
	return out
}

func (m Mode) qualifier() string {
	switch m {
	case ModeReviewRequested:
		return "review-requested:@me "
	case ModeInvolved:
		return "involves:@me "
	}
	return "author:@me "
}

func Modes() []Mode { return []Mode{ModeAuthored, ModeReviewRequested, ModeInvolved} }

// Fetch runs the search to completion, following pagination up to max PRs.
func (c *Client) Fetch(ctx context.Context, search string, max int) (Result, error) {
	// Deliberately modest. The nested review threads and check contexts make
	// each row expensive, and asking for too many at once is what pushes the
	// search past GitHub's internal timeout (surfacing as a 502).
	return c.search(ctx, searchQuery, search, max, 25)
}

// Backfill runs the startup search that fills the activity feed in. It asks
// for everything the polling query cannot afford, so its pages are small: the
// nested thread comments multiply, and a page of twenty-five would routinely
// time out on GitHub's side.
func (c *Client) Backfill(ctx context.Context, search string, max int) (Result, error) {
	return c.search(ctx, backfillQuery, search, max, 10)
}

func (c *Client) search(ctx context.Context, doc, search string, max, pageSize int) (Result, error) {
	var (
		res    Result
		cursor *string
	)
	res.FetchedAt = time.Now()

	for len(res.PRs) < max {
		n := pageSize
		if r := max - len(res.PRs); r < n {
			n = r
		}
		vars := map[string]any{"q": search, "n": n}
		if cursor != nil {
			vars["after"] = *cursor
		}

		var out graphQLResponse
		if err := c.do(ctx, doc, vars, &out); err != nil {
			return res, err
		}
		if len(out.Errors) > 0 {
			msgs := make([]string, 0, len(out.Errors))
			transient := false
			for _, e := range out.Errors {
				msgs = append(msgs, e.Message)
				switch strings.ToUpper(e.Type) {
				case "TIMEDOUT", "RATE_LIMITED", "SERVICE_UNAVAILABLE":
					transient = true
				}
			}
			joined := CleanMessage(strings.Join(msgs, "; "), 200)
			if transient {
				return res, &TransientError{Detail: joined}
			}
			return res, fmt.Errorf("github: %s", joined)
		}

		res.Viewer = out.Data.Viewer.Login
		res.RateLimit = RateLimit{
			Limit:     out.Data.RateLimit.Limit,
			Remaining: out.Data.RateLimit.Remaining,
			Cost:      out.Data.RateLimit.Cost,
			ResetAt:   parseTime(out.Data.RateLimit.ResetAt),
		}
		for _, n := range out.Data.Search.Nodes {
			if n.Typename != "PullRequest" {
				continue
			}
			res.PRs = append(res.PRs, convert(n, res.Viewer))
		}
		if !out.Data.Search.PageInfo.HasNextPage || out.Data.Search.PageInfo.EndCursor == "" {
			res.Complete = true
			break
		}
		end := out.Data.Search.PageInfo.EndCursor
		cursor = &end
	}
	return res, nil
}

func (c *Client) do(ctx context.Context, query string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ghpr")

	resp, err := c.http.Do(req)
	if err != nil {
		// Timeouts, resets and DNS hiccups are all worth retrying quietly.
		return &TransientError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("token rejected (401) — re-run `gh auth login`")
	}
	if resp.StatusCode == http.StatusForbidden {
		var snippet bytes.Buffer
		snippet.ReadFrom(io.LimitReader(resp.Body, 4096))
		msg := CleanMessage(snippet.String(), 160)
		if strings.Contains(strings.ToLower(msg), "rate limit") {
			return &TransientError{Status: http.StatusTooManyRequests, Detail: msg}
		}
		return fmt.Errorf("forbidden (403): %s", msg)
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		var snippet bytes.Buffer
		snippet.ReadFrom(io.LimitReader(resp.Body, 4096))
		return &TransientError{Status: resp.StatusCode, Detail: CleanMessage(snippet.String(), 160)}
	}
	if resp.StatusCode != http.StatusOK {
		var snippet bytes.Buffer
		snippet.ReadFrom(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, CleanMessage(snippet.String(), 160))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("could not read GitHub's response: %w", err)
	}
	return nil
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// convert flattens a GraphQL node into the normalized PR model. The viewer's
// login is needed to spot @mentions of them as the bodies go past; it is the
// only thing here that depends on who is looking.
func convert(n prNode, viewer string) PR {
	p := PR{
		ID:             n.ID,
		Repo:           n.Repository.NameWithOwner,
		Number:         n.Number,
		Title:          strings.TrimSpace(n.Title),
		URL:            n.URL,
		Author:         n.Author.Login,
		IsDraft:        n.IsDraft,
		CreatedAt:      parseTime(n.CreatedAt),
		UpdatedAt:      parseTime(n.UpdatedAt),
		Additions:      n.Additions,
		Deletions:      n.Deletions,
		ChangedFiles:   n.ChangedFiles,
		Mergeable:      n.Mergeable,
		HeadRef:        n.HeadRefName,
		BaseRef:        n.BaseRefName,
		ReviewDecision: n.ReviewDecision,
		IssueComments:  n.Comments.TotalCount,
		TotalThreads:   n.ReviewThreads.TotalCount,
	}

	for _, t := range n.ReviewThreads.Nodes {
		p.ReviewComments += t.Comments.TotalCount
		if !t.IsResolved && !t.IsOutdated {
			p.UnresolvedThreads++
		}
		for _, c := range t.Comments.Nodes {
			if c.Author.Login == "" {
				continue
			}
			p.ThreadComments = append(p.ThreadComments, Comment{
				By: c.Author.Login, At: parseTime(c.CreatedAt),
			})
		}
	}
	p.ThreadsTruncated = p.TotalThreads > len(n.ReviewThreads.Nodes)

	for _, l := range n.Labels.Nodes {
		p.Labels = append(p.Labels, Label{Name: l.Name, Color: l.Color})
	}

	for _, r := range n.Reviews.Nodes {
		if r.Author.Login == "" {
			continue
		}
		p.AllReviews = append(p.AllReviews, Reviewer{
			Login: r.Author.Login, State: r.State, At: parseTime(r.SubmittedAt),
		})
	}
	switch n.State {
	case "MERGED":
		p.State, p.FinishedAt = StateMerged, parseTime(n.MergedAt)
		p.FinishedBy = n.MergedBy.Login
	case "CLOSED":
		p.State, p.FinishedAt = StateClosed, parseTime(n.ClosedAt)
		// GitHub names the merger outright but not the closer, so a plain
		// close is read off the last close on the timeline.
		if len(n.TimelineItems.Nodes) > 0 {
			p.FinishedBy = n.TimelineItems.Nodes[0].Actor.Login
		}
	}

	// Reviews already given win over a still-open request from the same person.
	seen := map[string]bool{}
	for _, r := range n.LatestReviews.Nodes {
		login := r.Author.Login
		if login == "" || seen[login] {
			continue
		}
		seen[login] = true
		at := parseTime(r.SubmittedAt)
		p.Reviewers = append(p.Reviewers, Reviewer{Login: login, State: r.State, At: at})
		if at.After(p.LastReviewAt) {
			p.LastReviewAt, p.LastReviewBy = at, login
		}
	}

	// The newest conversation comments, for attributing comment activity and
	// for seeding the feed with the ones that predate the dashboard.
	for _, c := range n.Comments.Nodes {
		if c.Author.Login == "" {
			continue
		}
		at := parseTime(c.CreatedAt)
		p.RecentComments = append(p.RecentComments, Comment{By: c.Author.Login, At: at})
		if at.After(p.LastCommentAt) {
			p.LastCommentAt, p.LastCommentBy = at, c.Author.Login
		}
	}
	for _, r := range n.ReviewRequests.Nodes {
		rr := r.RequestedReviewer
		login, team := rr.Login, false
		if login == "" {
			login, team = rr.Name, true
		}
		if login == "" {
			continue
		}
		// Recorded before the dedupe below, because a request that follows a
		// review from the same person is exactly the case Reviewers hides.
		p.ReviewRequests = append(p.ReviewRequests, Reviewer{Login: login, State: "PENDING", Team: team})
		if seen[login] {
			continue
		}
		seen[login] = true
		p.Reviewers = append(p.Reviewers, Reviewer{Login: login, State: "PENDING", Team: team})
	}
	noteMentions(&p, n, viewer)

	if len(n.Commits.Nodes) > 0 {
		for _, c := range n.Commits.Nodes {
			if at := parseTime(c.Commit.CommittedDate); !at.IsZero() {
				p.Pushes = append(p.Pushes, Push{By: c.Commit.Author.User.Login, At: at})
			}
		}
		// commits(last: n) returns oldest first, so the head is the last one.
		// With the polling query's last: 1 the two are the same element; with
		// the backfill's last: 20 they are emphatically not.
		head := n.Commits.Nodes[len(n.Commits.Nodes)-1].Commit
		p.HeadOID = head.OID
		p.PushedBy = head.Author.User.Login
		// The commit's own date, not when it was pushed. GitHub's pushedDate is
		// deprecated and frequently null, and this errs the safe way: a commit
		// written days ago and pushed a minute ago is left out of the seed
		// rather than announced as something that never happened.
		p.PushedAt = parseTime(head.CommittedDate)
		if roll := n.Commits.Nodes[len(n.Commits.Nodes)-1].Commit.StatusCheckRollup; roll != nil {
			for _, c := range roll.Contexts.Nodes {
				chk := Check{Name: c.Name, URL: c.DetailsURL, Raw: c.Conclusion}
				if c.Typename == "StatusContext" {
					chk.Name, chk.URL, chk.Raw = c.Context, c.TargetURL, c.State
					chk.State = statusContextState(c.State)
				} else {
					chk.State = checkRunState(c.Status, c.Conclusion)
					if c.Conclusion == "" {
						chk.Raw = c.Status
					}
				}
				p.Checks = append(p.Checks, chk)
				// A check run reports when it finished; a status context only
				// when it was posted. Either dates the rollup well enough to
				// place one line in the seeded feed.
				if at := parseTime(c.CompletedAt); at.After(p.ChecksAt) {
					p.ChecksAt = at
				}
				if at := parseTime(c.CreatedAt); at.After(p.ChecksAt) {
					p.ChecksAt = at
				}
				switch chk.State {
				case CheckSuccess:
					p.ChecksPassed++
				case CheckFailure:
					p.ChecksFailed++
				case CheckPending:
					p.ChecksPending++
				}
			}
			p.ChecksState = rollupState(roll.State, p)
		}
	}
	return p
}

// noteMentions records the most recent @mention of the viewer among the text
// this query already carries.
//
// The description is dated by the pull request's creation rather than its last
// update, deliberately. Dating it by UpdatedAt would re-announce a standing
// mention in the description on every push and every comment, because that
// timestamp moves whenever anything at all happens.
//
// Mentions the viewer wrote themselves are skipped: quoting your own handle,
// or being quoted back, is not someone asking for you.
func noteMentions(p *PR, n prNode, viewer string) {
	if viewer == "" {
		return
	}
	note := func(who string, at time.Time, text string) bool {
		if who == "" || strings.EqualFold(who, viewer) || at.IsZero() {
			return false
		}
		if !Mentions(text, viewer) {
			return false
		}
		p.Mentions = append(p.Mentions, Mention{By: who, At: at})
		if at.After(p.LastMentionAt) {
			p.LastMentionAt, p.LastMentionBy = at, who
		}
		return true
	}

	note(p.Author, p.CreatedAt, n.BodyText)
	for _, c := range n.Comments.Nodes {
		at := parseTime(c.CreatedAt)
		if !note(c.Author.Login, at, c.BodyText) {
			continue
		}
		// Matched by author and time rather than by position: the comment list
		// skips authorless entries, so the two are not the same length.
		for i := range p.RecentComments {
			if p.RecentComments[i].By == c.Author.Login && p.RecentComments[i].At.Equal(at) {
				p.RecentComments[i].Mention = true
			}
		}
	}
	for _, r := range n.LatestReviews.Nodes {
		note(r.Author.Login, parseTime(r.SubmittedAt), r.BodyText)
	}
	// Only the backfill fills these, and they are where most review talk is.
	for _, r := range n.Reviews.Nodes {
		note(r.Author.Login, parseTime(r.SubmittedAt), r.BodyText)
	}
	for _, t := range n.ReviewThreads.Nodes {
		for _, c := range t.Comments.Nodes {
			at := parseTime(c.CreatedAt)
			if !note(c.Author.Login, at, c.BodyText) {
				continue
			}
			for i := range p.ThreadComments {
				if p.ThreadComments[i].By == c.Author.Login && p.ThreadComments[i].At.Equal(at) {
					p.ThreadComments[i].Mention = true
				}
			}
		}
	}
}

// checkRunState collapses a CheckRun's status+conclusion pair.
func checkRunState(status, conclusion string) CheckState {
	switch status {
	case "QUEUED", "IN_PROGRESS", "WAITING", "PENDING", "REQUESTED":
		return CheckPending
	}
	switch conclusion {
	case "SUCCESS":
		return CheckSuccess
	case "SKIPPED", "NEUTRAL":
		return CheckSkipped
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "CANCELLED":
		return CheckFailure
	case "":
		return CheckPending
	}
	return CheckNone
}

func statusContextState(state string) CheckState {
	switch state {
	case "SUCCESS":
		return CheckSuccess
	case "PENDING", "EXPECTED":
		return CheckPending
	case "FAILURE", "ERROR":
		return CheckFailure
	}
	return CheckNone
}

// rollupState prefers GitHub's own rollup, falling back to our tallies when
// it reports something we don't recognize.
func rollupState(state string, p PR) CheckState {
	switch state {
	case "SUCCESS":
		return CheckSuccess
	case "PENDING", "EXPECTED":
		return CheckPending
	case "FAILURE", "ERROR":
		return CheckFailure
	}
	switch {
	case p.ChecksFailed > 0:
		return CheckFailure
	case p.ChecksPending > 0:
		return CheckPending
	case p.ChecksPassed > 0:
		return CheckSuccess
	}
	return CheckNone
}
