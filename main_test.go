package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
	"github.com/jbonatakis/ghpr/internal/ui"
)

// The saved seed window has three distinct answers, and "nothing saved" must
// not be confused with "saved as zero" — the first leaves the flag's default
// alone, the second is a deliberate choice to start the feed blank.
func TestParseSeed(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    time.Duration
		wantErr bool
		why     string
	}{
		{"", -1, false, "nothing saved: keep the flag default"},
		{"   ", -1, false, "blank is nothing saved"},
		{"1h", time.Hour, false, "the usual case"},
		{"30m", 30 * time.Minute, false, "a shorter window"},
		{"0", 0, false, "deliberately blank feed"},
		{"0s", 0, false, "the same, spelled out"},
		{"-5m", 0, false, "backwards is not a window; treat it as off"},
		{"soon", -1, true, "not a duration"},
		{"60", -1, true, "a bare number is not a duration"},
	} {
		got, err := parseSeed(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseSeed(%q) accepted it — %s", tc.in, tc.why)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSeed(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSeed(%q) = %v, want %v — %s", tc.in, got, tc.want, tc.why)
		}
	}
}

// fixtureServer replays the captured payload as a single complete page.
func fixtureServer(t *testing.T) *gh.Client {
	t.Helper()
	raw, err := os.ReadFile("internal/gh/testdata/search_authored.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	doc["data"].(map[string]any)["search"].(map[string]any)["pageInfo"] =
		map[string]any{"hasNextPage": false, "endCursor": ""}
	if raw, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
	t.Cleanup(srv.Close)

	c := gh.NewClient("test")
	c.Endpoint = srv.URL
	return c
}

// capture runs fn with stdout redirected, and returns what it printed.
func capture(t *testing.T, fn func() error) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w

	done := make(chan string)
	go func() {
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		done <- b.String()
	}()

	runErr := fn()
	w.Close()
	os.Stdout = saved
	out := <-done

	if runErr != nil {
		t.Fatalf("explainSeed: %v", runErr)
	}
	return out
}

// A diagnostic that lies is worse than none, so this checks it against a
// payload whose contents are known: the fixture is months old, so a one-hour
// window must reach nothing at all and say so.
func TestWhySeedExplainsAQuietWindow(t *testing.T) {
	cfg := ui.Config{
		Client: fixtureServer(t), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: time.Hour,
	}
	out := capture(t, func() error { return explainSeed(cfg) })

	for _, want := range []string{
		"pull requests",
		"seed window 1h —",
		"0 events seeded, from 0 of",
		"Nothing at all landed in the window",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the explanation never says %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  IN     ") {
		t.Error("a months-old fixture reported something inside a one-hour window")
	}
}

// Widened far enough, the same payload has to account for what it does seed.
func TestWhySeedExplainsAWideWindow(t *testing.T) {
	cfg := ui.Config{
		Client: fixtureServer(t), Mode: gh.ModeAuthored, Max: 200,
		Prefs: config.Defaults(), Seed: 100000 * time.Hour,
	}
	out := capture(t, func() error { return explainSeed(cfg) })

	if !strings.Contains(out, "  IN     ") {
		t.Error("a window of eleven years reached nothing")
	}
	if strings.Contains(out, "0 events seeded") {
		t.Errorf("nothing was seeded from a window covering the whole fixture:\n%s", out)
	}
	// The fixture has review threads, which is exactly what cannot be dated.
	if !strings.Contains(out, "review-thread comments") {
		t.Error("the summary never accounts for what is out of reach")
	}
	if i := strings.LastIndex(out, "\n\n"); i >= 0 {
		t.Log("\n" + strings.TrimSpace(out[strings.LastIndex(out[:i], "\n\n"):]))
	}
}
