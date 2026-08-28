package gh

const searchQuery = `
query($q: String!, $n: Int!, $after: String) {
  viewer { login }
  rateLimit { limit remaining cost resetAt }
  search(query: $q, type: ISSUE, first: $n, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      __typename
      ... on PullRequest {
        id
        number
        title
        url
        isDraft
        bodyText
        createdAt
        updatedAt
        additions
        deletions
        changedFiles
        mergeable
        reviewDecision
        headRefName
        baseRefName
        repository { nameWithOwner }
        author { login }
        comments(last: 3) { totalCount nodes { author { login } createdAt bodyText } }
        labels(first: 10) { nodes { name color } }
        reviewRequests(first: 20) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { name }
            }
          }
        }
        latestReviews(first: 20) { nodes { state author { login } submittedAt bodyText } }
        reviewThreads(first: 50) {
          totalCount
          nodes { isResolved isOutdated comments { totalCount } }
        }
        commits(last: 1) {
          nodes {
            commit {
              oid
              committedDate
              author { user { login } }
              statusCheckRollup {
                state
                contexts(first: 20) {
                  totalCount
                  nodes {
                    __typename
                    ... on CheckRun { name status conclusion detailsUrl completedAt }
                    ... on StatusContext { context state targetUrl createdAt }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

// graphQLResponse mirrors the shape of searchQuery's result.
type graphQLResponse struct {
	Data struct {
		Viewer struct {
			Login string `json:"login"`
		} `json:"viewer"`
		RateLimit struct {
			Limit     int    `json:"limit"`
			Remaining int    `json:"remaining"`
			Cost      int    `json:"cost"`
			ResetAt   string `json:"resetAt"`
		} `json:"rateLimit"`
		Search struct {
			IssueCount int `json:"issueCount"`
			PageInfo   struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []prNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"errors"`
}

type prNode struct {
	Typename     string `json:"__typename"`
	ID           string `json:"id"`
	Number       int    `json:"number"`
	Title        string `json:"title"`
	URL          string `json:"url"`
	IsDraft      bool   `json:"isDraft"`
	BodyText     string `json:"bodyText"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Mergeable    string `json:"mergeable"`

	ReviewDecision string `json:"reviewDecision"`
	HeadRefName    string `json:"headRefName"`
	BaseRefName    string `json:"baseRefName"`

	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Comments struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			CreatedAt string `json:"createdAt"`
			BodyText  string `json:"bodyText"`
		} `json:"nodes"`
	} `json:"comments"`
	Labels struct {
		Nodes []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"nodes"`
	} `json:"labels"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer struct {
				Typename string `json:"__typename"`
				Login    string `json:"login"`
				Name     string `json:"name"`
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
			BodyText    string `json:"bodyText"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	ReviewThreads struct {
		TotalCount int `json:"totalCount"`
		Nodes      []struct {
			IsResolved bool `json:"isResolved"`
			IsOutdated bool `json:"isOutdated"`
			Comments   struct {
				TotalCount int `json:"totalCount"`
				// Requested only by the backfill query. The ordinary poll takes
				// the count alone, which is why it can say how many review
				// comments there are and never when any of them was written.
				Nodes []struct {
					CreatedAt string `json:"createdAt"`
					BodyText  string `json:"bodyText"`
					Author    struct {
						Login string `json:"login"`
					} `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`

	// State, MergedAt and ClosedAt are only populated for the backfill's
	// second search, the one that looks at what has already finished.
	State    string `json:"state"`
	MergedAt string `json:"mergedAt"`
	ClosedAt string `json:"closedAt"`

	// Reviews is every review, where LatestReviews is one per reviewer. A
	// reviewer who came back three times is three events, not one.
	Reviews struct {
		Nodes []struct {
			State  string `json:"state"`
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			SubmittedAt string `json:"submittedAt"`
			BodyText    string `json:"bodyText"`
		} `json:"nodes"`
	} `json:"reviews"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				OID           string `json:"oid"`
				CommittedDate string `json:"committedDate"`
				Author        struct {
					User struct {
						Login string `json:"login"`
					} `json:"user"`
				} `json:"author"`
				StatusCheckRollup *struct {
					State    string `json:"state"`
					Contexts struct {
						TotalCount int `json:"totalCount"`
						Nodes      []struct {
							Typename    string `json:"__typename"`
							Name        string `json:"name"`
							Status      string `json:"status"`
							Conclusion  string `json:"conclusion"`
							DetailsURL  string `json:"detailsUrl"`
							Context     string `json:"context"`
							State       string `json:"state"`
							TargetURL   string `json:"targetUrl"`
							CompletedAt string `json:"completedAt"`
							CreatedAt   string `json:"createdAt"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// backfillQuery is the startup-only search behind -seed. It asks for what the
// polling query deliberately does not: every review rather than the latest per
// reviewer, twenty conversation comments rather than three, the comments
// inside review threads with the dates that make them placeable, and twenty
// commits rather than the head alone.
//
// It is expensive, and that is the whole design. It runs once per launch
// instead of every thirty seconds, so the polling query stays cheap while the
// backfill gets to see an actual month.
const backfillQuery = `
query($q: String!, $n: Int!, $after: String) {
  viewer { login }
  rateLimit { limit remaining cost resetAt }
  search(query: $q, type: ISSUE, first: $n, after: $after) {
    issueCount
    pageInfo { hasNextPage endCursor }
    nodes {
      __typename
      ... on PullRequest {
        id
        number
        title
        url
        isDraft
        bodyText
        state
        createdAt
        updatedAt
        mergedAt
        closedAt
        additions
        deletions
        changedFiles
        mergeable
        reviewDecision
        headRefName
        baseRefName
        repository { nameWithOwner }
        author { login }
        comments(last: 20) { totalCount nodes { author { login } createdAt bodyText } }
        labels(first: 10) { nodes { name color } }
        reviewRequests(first: 20) {
          nodes {
            requestedReviewer {
              __typename
              ... on User { login }
              ... on Team { name }
            }
          }
        }
        reviews(last: 20) { nodes { state author { login } submittedAt bodyText } }
        latestReviews(first: 20) { nodes { state author { login } submittedAt bodyText } }
        reviewThreads(first: 50) {
          totalCount
          nodes {
            isResolved
            isOutdated
            comments(last: 20) {
              totalCount
              nodes { createdAt bodyText author { login } }
            }
          }
        }
        commits(last: 20) {
          nodes { commit { oid committedDate author { user { login } } } }
        }
      }
    }
  }
}`
