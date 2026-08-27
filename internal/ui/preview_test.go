package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/charmbracelet/lipgloss"
	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
	"github.com/muesli/termenv"
)

// TestPreview prints a rendered frame for eyeballing during development.
// Run with: PREVIEW=1 go test ./internal/ui -run TestPreview -v
func TestPreview(t *testing.T) {
	if os.Getenv("PREVIEW") == "" {
		t.Skip("set PREVIEW=1 to render a frame")
	}
	lipgloss.SetColorProfile(termenv.TrueColor)

	w, h := 150, 46
	if v := os.Getenv("PREVIEW_W"); v != "" {
		w, _ = strconv.Atoi(v)
	}
	if v := os.Getenv("PREVIEW_H"); v != "" {
		h, _ = strconv.Atoi(v)
	}

	m := newLoaded(t, w, h)
	if os.Getenv("PREVIEW_README") != "" {
		// Emit the exact blocks the README shows, so its samples cannot drift
		// from what the renderer actually produces.
		lm := newLoaded(t, 104, 24)
		lm.cursor = 1
		fmt.Println("@@LIST@@")
		for _, ln := range strings.Split(lm.View(), "\n")[:9] {
			fmt.Println(strings.TrimRight(ln, " "))
		}
		om := newLoaded(t, 104, 30)
		om = press(om, 'O')
		fmt.Println("@@ORGS@@")
		for _, ln := range strings.Split(om.View(), "\n") {
			fmt.Println(strings.TrimRight(ln, " "))
		}
		fm := newLoaded(t, 104, 40)
		fm.showEvents = true
		fm.events = []gh.Event{
			{At: fm.now, Kind: gh.EventComment, Repo: "acme/sensor-presence-collector", Number: 828, Text: "1 new comment", Actor: "morgan-bell"},
			{At: fm.now, Kind: gh.EventChecks, Repo: "acme/sensor-presence-collector", Number: 828, Text: "checks passing"},
			{At: fm.now, Kind: gh.EventPush, Repo: "acme/starfield", Number: 96, Text: "new commits", Actor: "octo-dev"},
			{At: fm.now, Kind: gh.EventReview, Repo: "acme/retention-policy-enforcer-export-history", Number: 99, Text: "approved", Actor: "priya-shah"},
			{At: fm.now, Kind: gh.EventOpened, Repo: "acme/design-docs", Number: 9, Text: "opened", Actor: "octo-dev"},
			{At: fm.now, Kind: gh.EventArrived, Repo: "acme/data-warehouse", Number: 1312, Text: "review requested", Actor: "kim-rivera"},
		}
		fmt.Println("@@FEED@@")
		for _, ln := range strings.Split(fm.eventsView(), "\n")[1:7] {
			fmt.Println(strings.TrimRight(ln, " "))
		}
		fmt.Println("@@END@@")
		return
	}
	if os.Getenv("PREVIEW_FEED") != "" {
		m.showEvents = true
		m.events = []gh.Event{
			{At: m.now, Kind: gh.EventComment, Repo: "acme/sensor-presence-collector", Number: 828, Text: "1 new comment", Actor: "morgan-bell", URL: "https://github.com/acme/sensor-presence-collector/pull/828"},
			{At: m.now, Kind: gh.EventChecks, Repo: "acme/sensor-presence-collector", Number: 828, Text: "checks running", URL: "https://github.com/acme/sensor-presence-collector/pull/828"},
			{At: m.now, Kind: gh.EventChecks, Repo: "acme/sensor-presence-collector", Number: 828, Text: "checks passing", URL: "https://github.com/acme/sensor-presence-collector/pull/828"},
			{At: m.now, Kind: gh.EventPush, Repo: "acme/starfield", Number: 96, Text: "new commits", Actor: "octo-dev", URL: "https://github.com/acme/starfield/pull/96"},
			{At: m.now, Kind: gh.EventReview, Repo: "acme/retention-policy-enforcer-export-history", Number: 99, Text: "approved", Actor: "priya-shah", URL: "https://github.com/acme/retention-policy-enforcer-export-history/pull/99"},
			{At: m.now, Kind: gh.EventMerged, Repo: "acme/platform-infra", Number: 3075, Text: "merged", URL: "https://github.com/acme/platform-infra/pull/3075"},
			{At: m.now, Kind: gh.EventOpened, Repo: "acme/design-docs", Number: 9, Text: "opened", Actor: "octo-dev", URL: "https://github.com/acme/design-docs/pull/9"},
			{At: m.now, Kind: gh.EventArrived, Repo: "acme/data-warehouse", Number: 1312, Text: "review requested", Actor: "kim-rivera", URL: "https://github.com/acme/data-warehouse/pull/1312"},
		}
		for _, wd := range []int{140, 100, 72} {
			m.width = wd
			fmt.Printf("--- width %d ---\n", wd)
			for _, ln := range strings.Split(m.eventsView(), "\n") {
				if strings.TrimSpace(ln) != "" {
					fmt.Println(ln)
				}
			}
		}
		return
	}
	if os.Getenv("PREVIEW_COUNTS") != "" {
		prefs := config.Defaults()
		prefs.HiddenOrgs = []string{"octo-dev"}
		n := New(Config{Client: m.cfg.Client, Mode: m.cfg.Mode, Interval: m.cfg.Interval, Max: 200, Prefs: prefs})
		n = update(n, tea.WindowSizeMsg{Width: w, Height: h})
		n = update(n, fetchDoneMsg{seq: n.fetchSeq, res: loadFixture(t)})
		fmt.Println("--- your config: one hidden org, nothing dismissed ---")
		fmt.Println(strings.Split(n.View(), "\n")[0])
		n = press(n, 'H')
		fmt.Println("--- after pressing H (nothing to reveal) ---")
		fmt.Println(strings.Split(n.View(), "\n")[0])
		n = press(n, 'H')
		n = cursorToPR(t, n, "acme/starfield#96")
		n = press(n, 'h')
		fmt.Println("--- plus one PR dismissed with h ---")
		fmt.Println(strings.Split(n.View(), "\n")[0])
		n = press(n, 'H')
		fmt.Println("--- peeking at it ---")
		fmt.Println(strings.Split(n.View(), "\n")[0])
		return
	}
	if os.Getenv("PREVIEW_HIDE") != "" {
		m = cursorToPR(t, m, "acme/starfield#96")
		m = press(m, 'h')
		m = cursorToRepo(t, m, "octo-dev/dashboard")
		m = press(m, 'h')
		fmt.Println("--- concealed ---")
		fmt.Println(m.View())
		m = press(m, 'H')
		fmt.Println("--- peeking (H) ---")
		fmt.Println(m.View())
		return
	}
	if os.Getenv("PREVIEW_HELP") != "" {
		m = press(m, '?')
		fmt.Println(m.View())
		return
	}
	if os.Getenv("PREVIEW_ERR") != "" {
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})
		fmt.Println("--- transient (blip) ---")
		fmt.Println(m.View())
		for i := 0; i < 3; i++ {
			m = update(m, fetchDoneMsg{seq: m.fetchSeq, err: transient502()})
		}
		fmt.Println("--- escalated ---")
		fmt.Println(m.View())
		return
	}
	if os.Getenv("PREVIEW_ORGS") != "" {
		m = press(m, 'O')
		fmt.Println(m.View())
		return
	}
	if os.Getenv("PREVIEW_PANES") != "" {
		next := loadFixture(t)
		for i := range next.PRs {
			switch next.PRs[i].Key() {
			case "acme/starfield#96":
				next.PRs[i].IssueComments += 3
			case "acme/design-docs#9":
				next.PRs[i].ChecksState = 2
			}
		}
		m = update(m, fetchDoneMsg{seq: m.fetchSeq, res: next})
		m.showDetail, m.showEvents = true, true
		m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	}
	fmt.Println(m.View())
}
