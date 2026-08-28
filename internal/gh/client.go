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
	switch m {
	case ModeReviewRequested:
		b.WriteString("review-requested:@me ")
	case ModeInvolved:
		b.WriteString("involves:@me ")
	default:
		b.WriteString("author:@me ")
	}
	b.WriteString(strings.TrimSpace(extra))
	return strings.TrimSpace(b.String())
}

func Modes() []Mode { return []Mode{ModeAuthored, ModeReviewRequested, ModeInvolved} }

// Fetch runs the search to completion, following pagination up to max PRs.
func (c *Client) Fetch(ctx context.Context, search string, max int) (Result, error) {
	// Deliberately modest. The nested review threads and check contexts make
	// each row expensive, and asking for too many at once is what pushes the
	// search past GitHub's internal timeout (surfacing as a 502).
	const pageSize = 25
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
		if err := c.do(ctx, searchQuery, vars, &out); err != nil {
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
	}
	p.ThreadsTruncated = p.TotalThreads > len(n.ReviewThreads.Nodes)

	for _, l := range n.Labels.Nodes {
		p.Labels = append(p.Labels, Label{Name: l.Name, Color: l.Color})
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

	// The newest conversation comment, for attributing comment activity.
	for _, c := range n.Comments.Nodes {
		if c.Author.Login == "" {
			continue
		}
		if at := parseTime(c.CreatedAt); at.After(p.LastCommentAt) {
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
		p.HeadOID = n.Commits.Nodes[0].Commit.OID
		p.PushedBy = n.Commits.Nodes[0].Commit.Author.User.Login
		if roll := n.Commits.Nodes[0].Commit.StatusCheckRollup; roll != nil {
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
	note := func(who string, at time.Time, text string) {
		if who == "" || strings.EqualFold(who, viewer) || at.IsZero() {
			return
		}
		if at.After(p.LastMentionAt) && Mentions(text, viewer) {
			p.LastMentionAt, p.LastMentionBy = at, who
		}
	}

	note(p.Author, p.CreatedAt, n.BodyText)
	for _, c := range n.Comments.Nodes {
		note(c.Author.Login, parseTime(c.CreatedAt), c.BodyText)
	}
	for _, r := range n.LatestReviews.Nodes {
		note(r.Author.Login, parseTime(r.SubmittedAt), r.BodyText)
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
