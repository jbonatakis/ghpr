package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/jbonatakis/ghpr/internal/gh"
)

func eventFor(repo string, number int, text string) gh.Event {
	return gh.Event{
		At: time.Now(), Kind: gh.EventComment, Repo: repo, Number: number,
		Key: repoKey(repo, number), Text: text, Actor: "someone",
	}
}

func repoKey(repo string, number int) string {
	return repo + "#" + itoa(number)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func feedText(m Model) string { return ansi.Strip(m.eventsView()) }

// The activity feed is a session-wide record, on purpose. It is not scoped to
// the current mode and not narrowed by the hide filters: switching mode or
// dismissing a pull request does not un-happen what it did. These tests exist
// so that intent is not "fixed" away later.

func TestFeedKeepsActivityFromEveryMode(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = []gh.Event{
		eventFor("acme/starfield", 96, "authored one"),
		eventFor("acme/ci-pipelines", 130, "reviewer one"),
	}

	out := feedText(m)
	for _, want := range []string{"authored one", "reviewer one"} {
		if !strings.Contains(out, want) {
			t.Errorf("feed dropped %q; it should hold the whole session", want)
		}
	}

	// Still all there after switching mode.
	m = press(m, 'm')
	out = feedText(m)
	for _, want := range []string{"authored one", "reviewer one"} {
		if !strings.Contains(out, want) {
			t.Errorf("switching mode lost %q from the feed", want)
		}
	}
	if len(m.events) != 2 {
		t.Errorf("log holds %d events, want both kept", len(m.events))
	}
}

func TestFeedKeepsActivityForHiddenItems(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = []gh.Event{
		eventFor("acme/starfield", 96, "dismissed one"),
		eventFor("octo-dev/dashboard", 4, "hidden org one"),
	}
	m.hiddenPRs["acme/starfield#96"] = true
	m.hiddenOrgs["octo-dev"] = true

	out := feedText(m)
	for _, want := range []string{"dismissed one", "hidden org one"} {
		if !strings.Contains(out, want) {
			t.Errorf("feed dropped %q; hiding affects the list, not the record", want)
		}
	}
}

func TestFeedIgnoresTheTextFilter(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = []gh.Event{eventFor("acme/starfield", 96, "some one")}

	m = press(m, '/')
	for _, r := range "design-docs" {
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if !strings.Contains(feedText(m), "some one") {
		t.Error("the text filter should not empty the activity feed")
	}
}

func TestFeedEmptyStateIsNeutral(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = nil

	if out := feedText(m); !strings.Contains(out, "watching for changes") {
		t.Errorf("expected the initial empty state, got:\n%s", out)
	}
}
