package gh

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const lookupQuery = `
query($owner: String!, $name: String!, $number: Int!) {
  viewer { login }
  rateLimit { limit remaining cost resetAt }
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number
      title
      url
      state
      createdAt
      updatedAt
      author { login }
      repository { nameWithOwner }
      reviewRequests(first: 50) {
        nodes {
          requestedReviewer {
            __typename
            ... on User { login }
            ... on Team { name slug organization { login } }
          }
        }
      }
      latestReviews(first: 50) { nodes { state author { login } submittedAt } }
    }
  }
}`

type lookupResponse struct {
	Data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		Repository struct {
			PullRequest *struct {
				Number     int    `json:"number"`
				Title      string `json:"title"`
				URL        string `json:"url"`
				State      string `json:"state"`
				CreatedAt  string `json:"createdAt"`
				UpdatedAt  string `json:"updatedAt"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
				Author struct {
					Login string `json:"login"`
				} `json:"author"`
				ReviewRequests struct {
					Nodes []struct {
						RequestedReviewer struct {
							Typename     string `json:"__typename"`
							Login        string `json:"login"`
							Name         string `json:"name"`
							Slug         string `json:"slug"`
							Organization struct {
								Login string `json:"login"`
							} `json:"organization"`
						} `json:"requestedReviewer"`
					} `json:"nodes"`
				} `json:"reviewRequests"`
				LatestReviews struct {
					Nodes []struct {
						State  string `json:"state"`
						Author struct {
							Login string `json:"login"`
						} `json:"author"`
						SubmittedAt string `json:"submittedAt"`
					} `json:"nodes"`
				} `json:"latestReviews"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Requested is one outstanding review request, and — the part that matters when
// a pull request is not reaching the feed — whether it names a person or a team.
type Requested struct {
	Name string
	Team bool
}

// Lookup is one pull request, fetched by name rather than found by searching.
//
// It exists to answer a question searching cannot: when a pull request is
// missing from the feed, is it because no search reaches it, or because the
// searches reach it and there is nothing in it to date? Asking for it directly
// sidesteps every search and shows what the searches would have had to match —
// above all whether the review was requested of you or of a team you are on,
// which are different qualifiers and, apparently, different answers.
type Lookup struct {
	Viewer    string
	Repo      string
	Number    int
	Title     string
	URL       string
	State     string
	CreatedAt string
	UpdatedAt string
	Author    string
	Requests  []Requested
	Reviewers []Reviewer
}

// ParsePRRef reads "owner/repo#123", with or without the hash.
func ParsePRRef(ref string) (owner, name string, number int, err error) {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "https://github.com/")
	ref = strings.ReplaceAll(ref, "/pull/", "#")

	at := strings.LastIndexAny(ref, "#/")
	if at < 0 {
		return "", "", 0, fmt.Errorf("want owner/repo#123, got %q", ref)
	}
	number, err = strconv.Atoi(strings.TrimSpace(ref[at+1:]))
	if err != nil || number <= 0 {
		return "", "", 0, fmt.Errorf("want owner/repo#123, got %q", ref)
	}
	repo := strings.Trim(ref[:at], "/")
	slash := strings.IndexByte(repo, '/')
	if slash < 0 {
		return "", "", 0, fmt.Errorf("want owner/repo#123, got %q", ref)
	}
	return repo[:slash], repo[slash+1:], number, nil
}

// Lookup fetches one pull request by name.
func (c *Client) Lookup(ctx context.Context, owner, name string, number int) (Lookup, error) {
	var out lookupResponse
	vars := map[string]any{"owner": owner, "name": name, "number": number}
	if err := c.do(ctx, lookupQuery, vars, &out); err != nil {
		return Lookup{}, err
	}
	if len(out.Errors) > 0 {
		msgs := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			msgs = append(msgs, e.Message)
		}
		return Lookup{}, fmt.Errorf("github: %s", CleanMessage(strings.Join(msgs, "; "), 200))
	}
	pr := out.Data.Repository.PullRequest
	if pr == nil {
		return Lookup{}, fmt.Errorf("%s/%s#%d: no such pull request, or the token cannot see it",
			owner, name, number)
	}

	got := Lookup{
		Viewer: out.Data.Viewer.Login, Repo: pr.Repository.NameWithOwner,
		Number: pr.Number, Title: strings.TrimSpace(pr.Title), URL: pr.URL,
		State: pr.State, CreatedAt: pr.CreatedAt, UpdatedAt: pr.UpdatedAt,
		Author: pr.Author.Login,
	}
	for _, r := range pr.ReviewRequests.Nodes {
		rr := r.RequestedReviewer
		if rr.Login != "" {
			got.Requests = append(got.Requests, Requested{Name: rr.Login})
			continue
		}
		team := rr.Slug
		if team == "" {
			team = rr.Name
		}
		if org := rr.Organization.Login; org != "" {
			team = org + "/" + team
		}
		got.Requests = append(got.Requests, Requested{Name: team, Team: true})
	}
	for _, r := range pr.LatestReviews.Nodes {
		got.Reviewers = append(got.Reviewers, Reviewer{
			Login: r.Author.Login, State: r.State, At: parseTime(r.SubmittedAt),
		})
	}
	return got, nil
}
