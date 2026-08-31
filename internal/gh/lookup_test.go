package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePRRef(t *testing.T) {
	for _, tc := range []struct {
		in          string
		owner, name string
		number      int
		wantErr     bool
	}{
		{in: "acme/hyperspace#86", owner: "acme", name: "hyperspace", number: 86},
		{in: " acme/hyperspace#86 ", owner: "acme", name: "hyperspace", number: 86},
		{in: "acme/hyperspace/86", owner: "acme", name: "hyperspace", number: 86},
		{in: "https://github.com/acme/hyperspace/pull/86", owner: "acme", name: "hyperspace", number: 86},
		{in: "acme/multi-part-name#7", owner: "acme", name: "multi-part-name", number: 7},
		{in: "hyperspace#86", wantErr: true},
		{in: "acme/hyperspace", wantErr: true},
		{in: "acme/hyperspace#0", wantErr: true},
		{in: "acme/hyperspace#abc", wantErr: true},
		{in: "", wantErr: true},
	} {
		owner, name, number, err := ParsePRRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParsePRRef(%q) was accepted as %s/%s#%d", tc.in, owner, name, number)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePRRef(%q): %v", tc.in, err)
			continue
		}
		if owner != tc.owner || name != tc.name || number != tc.number {
			t.Errorf("ParsePRRef(%q) = %s/%s#%d, want %s/%s#%d",
				tc.in, owner, name, number, tc.owner, tc.name, tc.number)
		}
	}
}

// The whole point of looking one up by name is to see who the review is
// actually requested of, because a team and a person are different qualifiers
// and a CODEOWNERS rule usually names a team.
func TestLookupTellsATeamRequestFromAPersonalOne(t *testing.T) {
	body := map[string]any{
		"data": map[string]any{
			"viewer": map[string]any{"login": "jack"},
			"repository": map[string]any{
				"pullRequest": map[string]any{
					"number": 86, "title": "Split the doc",
					"url": "https://example.invalid/86", "state": "OPEN",
					"createdAt":  "2026-08-31T09:00:00Z",
					"updatedAt":  "2026-08-31T10:00:00Z",
					"author":     map[string]any{"login": "brianmuse"},
					"repository": map[string]any{"nameWithOwner": "acme/hyperspace"},
					"reviewRequests": map[string]any{"nodes": []any{
						map[string]any{"requestedReviewer": map[string]any{
							"__typename": "Team", "name": "Platform", "slug": "platform",
							"organization": map[string]any{"login": "acme"},
						}},
						map[string]any{"requestedReviewer": map[string]any{
							"__typename": "User", "login": "dana-quill",
						}},
					}},
					"latestReviews": map[string]any{"nodes": []any{
						map[string]any{
							"state": "APPROVED", "author": map[string]any{"login": "jack"},
							"submittedAt": "2026-08-31T09:30:00Z",
						},
					}},
				},
			},
		},
	}
	raw, _ := json.Marshal(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test")
	c.Endpoint = srv.URL
	got, err := c.Lookup(context.Background(), "acme", "hyperspace", 86)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	if got.Viewer != "jack" || got.Repo != "acme/hyperspace" || got.Number != 86 {
		t.Errorf("looked up %+v", got)
	}
	if len(got.Requests) != 2 {
		t.Fatalf("read %d requests, want the team and the person", len(got.Requests))
	}
	team, person := got.Requests[0], got.Requests[1]
	if !team.Team || team.Name != "acme/platform" {
		t.Errorf("team request read as %+v, want acme/platform", team)
	}
	if person.Team || person.Name != "dana-quill" {
		t.Errorf("personal request read as %+v", person)
	}
	if len(got.Reviewers) != 1 || got.Reviewers[0].Login != "jack" {
		t.Errorf("reviewers read as %+v", got.Reviewers)
	}
}

// A pull request the token cannot see is a clear message rather than an empty
// report that reads like "nothing is wrong".
func TestLookupSaysWhenThereIsNoSuchPullRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"viewer":{"login":"jack"},"repository":{"pullRequest":null}}}`))
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test")
	c.Endpoint = srv.URL
	if _, err := c.Lookup(context.Background(), "acme", "hyperspace", 999); err == nil {
		t.Error("a missing pull request came back as a successful lookup")
	}
}
