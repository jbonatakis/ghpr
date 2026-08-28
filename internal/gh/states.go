package gh

import (
	"context"
	"fmt"
)

// State is the lifecycle of a pull request.
type State string

const (
	StateOpen   State = "OPEN"
	StateClosed State = "CLOSED"
	StateMerged State = "MERGED"
)

const statesQuery = `
query($ids: [ID!]!) {
  rateLimit { limit remaining cost resetAt }
  nodes(ids: $ids) {
    ... on PullRequest {
      id
      state
      mergedBy { login }
      timelineItems(last: 1, itemTypes: [CLOSED_EVENT]) {
        nodes { ... on ClosedEvent { actor { login } } }
      }
    }
  }
}`

type statesResponse struct {
	Data struct {
		RateLimit struct {
			Limit     int    `json:"limit"`
			Remaining int    `json:"remaining"`
			Cost      int    `json:"cost"`
			ResetAt   string `json:"resetAt"`
		} `json:"rateLimit"`
		Nodes []struct {
			ID       string `json:"id"`
			State    string `json:"state"`
			MergedBy struct {
				Login string `json:"login"`
			} `json:"mergedBy"`
			TimelineItems struct {
				Nodes []struct {
					Actor struct {
						Login string `json:"login"`
					} `json:"actor"`
				} `json:"nodes"`
			} `json:"timelineItems"`
		} `json:"nodes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"errors"`
}

// Outcome is what became of a pull request that left the search, and who did
// it. GitHub names the merger outright; for a plain close it takes the last
// close on the timeline, which is one extra node per pull request.
type Outcome struct {
	State State
	By    string
}

// States looks up the current state of specific pull requests by node id.
//
// This exists so the dashboard never has to guess. A pull request can drop out
// of a paginated search for reasons that have nothing to do with being closed
// — most often because another PR was updated mid-fetch and shifted the page
// boundary — so absence alone is not evidence. Asking directly is one cheap
// query and gives a definitive answer, including whether it merged or closed.
func (c *Client) States(ctx context.Context, ids []string) (map[string]Outcome, error) {
	out := map[string]Outcome{}
	if len(ids) == 0 {
		return out, nil
	}
	// nodes() accepts up to 100 ids per call.
	const batch = 100
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		var resp statesResponse
		if err := c.do(ctx, statesQuery, map[string]any{"ids": ids[start:end]}, &resp); err != nil {
			return out, err
		}
		if len(resp.Errors) > 0 {
			msgs := make([]string, 0, len(resp.Errors))
			transient := false
			for _, e := range resp.Errors {
				msgs = append(msgs, e.Message)
				switch e.Type {
				case "TIMEDOUT", "RATE_LIMITED", "SERVICE_UNAVAILABLE":
					transient = true
				}
			}
			joined := CleanMessage(joinAll(msgs, "; "), 200)
			if transient {
				return out, &TransientError{Detail: joined}
			}
			return out, fmt.Errorf("github: %s", joined)
		}
		for _, n := range resp.Data.Nodes {
			if n.ID == "" || n.State == "" {
				continue
			}
			who := n.MergedBy.Login
			if who == "" && len(n.TimelineItems.Nodes) > 0 {
				who = n.TimelineItems.Nodes[0].Actor.Login
			}
			out[n.ID] = Outcome{State: State(n.State), By: who}
		}
	}
	return out, nil
}

func joinAll(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}
