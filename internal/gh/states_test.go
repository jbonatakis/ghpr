package gh

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// statesServer answers a nodes(ids:) look-up from a fixed id -> state table.
func statesServer(t *testing.T, table map[string]string, calls *int, batches *[][]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if calls != nil {
			*calls++
		}
		if batches != nil {
			*batches = append(*batches, body.Variables.IDs)
		}

		type node struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		var nodes []node
		for _, id := range body.Variables.IDs {
			if st, ok := table[id]; ok {
				nodes = append(nodes, node{ID: id, State: st})
			} else {
				nodes = append(nodes, node{}) // an id we cannot resolve
			}
		}
		out := map[string]any{"data": map[string]any{"nodes": nodes}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	c := NewClient("test-token")
	c.Endpoint = srv.URL
	return c
}

func TestStatesReportsEachLifecycle(t *testing.T) {
	c := statesServer(t, map[string]string{
		"open": "OPEN", "merged": "MERGED", "closed": "CLOSED",
	}, nil, nil)

	got, err := c.States(context.Background(), []string{"open", "merged", "closed"})
	if err != nil {
		t.Fatalf("States: %v", err)
	}
	for id, want := range map[string]State{"open": StateOpen, "merged": StateMerged, "closed": StateClosed} {
		if got[id].State != want {
			t.Errorf("%s = %q, want %q", id, got[id].State, want)
		}
	}
}

func TestStatesWithNoIDsMakesNoRequest(t *testing.T) {
	calls := 0
	c := statesServer(t, nil, &calls, nil)

	got, err := c.States(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || calls != 0 {
		t.Errorf("empty input should be free: %d results, %d calls", len(got), calls)
	}
}

func TestStatesBatchesLargeInputs(t *testing.T) {
	table := map[string]string{}
	ids := make([]string, 250)
	for i := range ids {
		ids[i] = string(rune('a'+i%26)) + string(rune('a'+i/26))
		table[ids[i]] = "MERGED"
	}
	var batches [][]string
	c := statesServer(t, table, nil, &batches)

	if _, err := c.States(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	if len(batches) != 3 {
		t.Errorf("made %d requests for 250 ids, want 3 batches of <=100", len(batches))
	}
	for i, b := range batches {
		if len(b) > 100 {
			t.Errorf("batch %d carried %d ids, over the nodes() limit", i, len(b))
		}
	}
}

func TestStatesOmitsUnresolvableIDs(t *testing.T) {
	c := statesServer(t, map[string]string{"known": "MERGED"}, nil, nil)

	got, err := c.States(context.Background(), []string{"known", "mystery"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["mystery"]; ok {
		t.Error("an unresolvable id should be absent, not guessed at")
	}
	if got["known"].State != StateMerged {
		t.Errorf("known = %q", got["known"].State)
	}
}

func TestStatesSurfacesTransientFailures(t *testing.T) {
	c := clientFor(t, http.StatusBadGateway, html502)
	_, err := c.States(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsTransient(err) {
		t.Errorf("a 502 during verification should be transient, got %v", err)
	}
}
