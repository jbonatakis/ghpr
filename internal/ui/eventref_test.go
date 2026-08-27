package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// TestEventRefAlwaysKeepsTheNumber pins the rule that made the activity feed
// unhelpful: it showed "sensor-presence-collector#…", naming the repository but
// not the pull request.
func TestEventRefAlwaysKeepsTheNumber(t *testing.T) {
	const repo = "sensor-presence-collector"
	for _, w := range []int{4, 6, 10, 12, 20, 26, 28, 40, 46} {
		got := eventRef(repo, 828, w)

		if runewidth.StringWidth(got) != w {
			t.Errorf("width %d: rendered %d cells: %q", w, runewidth.StringWidth(got), got)
		}
		if !strings.Contains(got, "#828") {
			t.Errorf("width %d: number lost: %q", w, got)
		}
	}
}

func TestEventRefShortensTheNameNotTheNumber(t *testing.T) {
	got := eventRef("sensor-presence-collector", 828, 20)
	if !strings.HasSuffix(strings.TrimRight(got, " "), "#828") {
		t.Errorf("the number should survive at the end: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("the name should be the part elided: %q", got)
	}
	if strings.Contains(got, "#8…") || strings.Contains(got, "#…") {
		t.Errorf("the number was cut: %q", got)
	}
}

func TestEventRefFitsShortNamesWhole(t *testing.T) {
	got := eventRef("design-docs", 9, 26)
	if !strings.HasPrefix(got, "design-docs#9") {
		t.Errorf("a short name should not be touched: %q", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("nothing needed eliding: %q", got)
	}
}

func TestEventRefHandlesVeryLargeNumbers(t *testing.T) {
	got := eventRef("platform-infra", 3075, 12)
	if !strings.Contains(got, "#3075") {
		t.Errorf("number lost: %q", got)
	}
	if runewidth.StringWidth(got) != 12 {
		t.Errorf("width = %d, want 12", runewidth.StringWidth(got))
	}
}

// TestActivityFeedShowsPullRequestNumbers renders the real pane.
func TestActivityFeedShowsPullRequestNumbers(t *testing.T) {
	for _, w := range []int{80, 100, 120, 150} {
		m := newLoaded(t, w, 40)
		m.showEvents = true
		m.events = []gh.Event{
			{At: time.Now(), Kind: gh.EventComment, Repo: "acme/sensor-presence-collector", Number: 828, Text: "1 new comment"},
			{At: time.Now(), Kind: gh.EventChecks, Repo: "acme/sensor-presence-collector", Number: 828, Text: "checks running"},
			{At: time.Now(), Kind: gh.EventChecks, Repo: "acme/data-warehouse-sync", Number: 13, Text: "checks passing"},
		}

		out := ansi.Strip(m.View())
		for _, want := range []string{"#828", "#13"} {
			if !strings.Contains(out, want) {
				t.Errorf("width %d: activity feed omits %s", w, want)
			}
		}
		for _, ln := range strings.Split(m.View(), "\n") {
			if ansi.StringWidth(ln) > w {
				t.Errorf("width %d: activity line overflows: %q", w, ansi.Strip(ln))
			}
		}
	}
}

func TestActivityFeedKeepsNumbersOnANarrowTerminal(t *testing.T) {
	m := newLoaded(t, 60, 30)
	m.showEvents = true
	m.events = []gh.Event{
		{At: time.Now(), Kind: gh.EventComment, Repo: "acme/retention-policy-enforcer-export-history", Number: 99, Text: "1 new comment"},
	}
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "#99") {
		t.Errorf("number dropped on a narrow terminal:\n%s", out)
	}
}

func TestEventRefWidthStaysInBounds(t *testing.T) {
	for _, total := range []int{40, 60, 80, 100, 120, 200, 400} {
		w := eventRefWidth(total)
		if w < 12 || w > 46 {
			t.Errorf("total %d: ref width %d out of bounds", total, w)
		}
		if total >= 120 && w < 40 {
			t.Errorf("total %d: ref width %d is stingy on a wide terminal", total, w)
		}
		// The reference plus its surroundings must never exceed the terminal.
		if used := 1 + 8 + 2 + w + 2; used > total && total >= 60 {
			t.Errorf("total %d: reference column leaves no room (%d used)", total, used)
		}
	}
}

// --- actor column --------------------------------------------------------

func actorFeed() []gh.Event {
	now := time.Now()
	return []gh.Event{
		{At: now, Kind: gh.EventComment, Repo: "acme/sensor-presence-collector", Number: 828, Text: "1 new comment", Actor: "morgan-bell"},
		{At: now, Kind: gh.EventReview, Repo: "acme/sensor-presence-collector", Number: 828, Text: "approved", Actor: "priya-shah"},
		{At: now, Kind: gh.EventPush, Repo: "acme/starfield", Number: 96, Text: "new commits", Actor: "octo-dev"},
		{At: now, Kind: gh.EventChecks, Repo: "acme/starfield", Number: 96, Text: "checks passing"},
	}
}

func TestActivityFeedNamesTheActor(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = actorFeed()

	out := ansi.Strip(m.eventsView())
	for _, who := range []string{"morgan-bell", "priya-shah", "octo-dev"} {
		if !strings.Contains(out, who) {
			t.Errorf("activity feed omits %s:\n%s", who, out)
		}
	}
}

func TestActorsAlignIntoAColumn(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = actorFeed()

	var cols []int
	// Scoped to the pane: these logins also appear in the header and the list.
	for _, ln := range strings.Split(ansi.Strip(m.eventsView()), "\n") {
		for _, who := range []string{"morgan-bell", "priya-shah", "octo-dev"} {
			if i := strings.Index(ln, who); i >= 0 {
				// Display columns, not bytes: the event icons are multi-byte
				// runes, so byte offsets differ even when the text lines up.
				cols = append(cols, ansi.StringWidth(ln[:i]))
			}
		}
	}
	if len(cols) != 3 {
		t.Fatalf("found %d actors, want 3", len(cols))
	}
	for _, c := range cols[1:] {
		if c != cols[0] {
			t.Errorf("actors start at differing columns %v; they should line up", cols)
			break
		}
	}
}

func TestActorColumnDoesNotOverflow(t *testing.T) {
	for _, w := range []int{60, 72, 90, 120, 160} {
		m := newLoaded(t, w, 40)
		m.showEvents = true
		m.events = actorFeed()

		for _, ln := range strings.Split(m.View(), "\n") {
			if got := ansi.StringWidth(ln); got > w {
				t.Errorf("width %d: line is %d cells: %q", w, got, ansi.Strip(ln))
			}
		}
		// The pull request number still takes precedence over the actor.
		if out := ansi.Strip(m.View()); !strings.Contains(out, "#828") {
			t.Errorf("width %d: lost the PR number", w)
		}
	}
}

func TestCheckEventsShowNoActor(t *testing.T) {
	m := newLoaded(t, 140, 40)
	m.showEvents = true
	m.events = []gh.Event{
		{At: time.Now(), Kind: gh.EventChecks, Repo: "acme/starfield", Number: 96, Text: "checks passing"},
	}
	for _, ln := range strings.Split(ansi.Strip(m.eventsView()), "\n") {
		if strings.Contains(ln, "checks passing") {
			if strings.TrimRight(ln, " ") != strings.TrimRight(ln[:strings.Index(ln, "checks passing")+len("checks passing")], " ") {
				t.Errorf("a check event should carry nothing after its text: %q", ln)
			}
		}
	}
}

// TestRealEventTextsAreNotTruncated guards the description column: every text
// the diff can produce must fit, or the feed would elide the very words that
// say what happened.
func TestRealEventTextsAreNotTruncated(t *testing.T) {
	texts := []string{
		"opened", "review requested", "now involves you", "now listed",
		"merged", "closed", "approved", "changes requested", "review dismissed",
		"ready for review", "now conflicting", "new commits",
		"1 new comment", "999 new comments",
		"checks passing", "checks failing", "checks running", "checks no checks",
	}
	for _, text := range texts {
		for _, icon := range []string{"+", "→", "✔", "×", "»", "~", "★", "▲", "!", "↑"} {
			cell := pad(icon+" "+text, evWhatWidth)
			if strings.Contains(cell, "…") {
				t.Errorf("%q with icon %q does not fit in %d cells: %q",
					text, icon, evWhatWidth, cell)
			}
		}
	}
}
