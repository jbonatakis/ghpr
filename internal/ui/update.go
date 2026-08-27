package ui

import (
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"os/exec"
	"runtime"
	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While the filter box is focused it swallows almost everything.
	if m.filtering {
		switch msg.String() {
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		case "esc":
			m.filtering = false
			m.filter.Blur()
			m.filter.Reset()
			m.rebuild()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuild()
		return m, cmd
	}

	if m.showOrgs {
		return m.handleOrgKey(msg)
	}

	if m.showHelp {
		// Any key dismisses help except an explicit quit.
		if key.Matches(msg, keys.Quit) && msg.String() != "esc" {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit

	case key.Matches(msg, keys.Help):
		m.showHelp = true

	case key.Matches(msg, keys.Up):
		m.move(-1)

	case key.Matches(msg, keys.Down):
		m.move(1)

	case key.Matches(msg, keys.PageUp):
		m.move(-m.listHeight())

	case key.Matches(msg, keys.PageDown):
		m.move(m.listHeight())

	case key.Matches(msg, keys.Home):
		m.cursor = 0
		m.clampScroll()

	case key.Matches(msg, keys.End):
		m.cursor = len(m.rows) - 1
		m.clampScroll()

	case key.Matches(msg, keys.Open):
		if r := m.currentRow(); r != nil {
			if r.isRepo() {
				m.collapsed[r.repo] = !m.collapsed[r.repo]
				m.rebuildKeepCursor()
				m.savePrefs()
			} else {
				return m, openURL(r.pr.URL)
			}
		}

	case key.Matches(msg, keys.Copy):
		if p := m.selected(); p != nil {
			if err := clipboard.WriteAll(p.URL); err != nil {
				m.setToast("clipboard unavailable")
			} else {
				m.setToast("copied " + p.Key())
			}
		}

	case key.Matches(msg, keys.Detail):
		m.showDetail = !m.showDetail
		m.clampScroll()

	case key.Matches(msg, keys.Fold):
		if r := m.currentRow(); r != nil {
			m.collapsed[r.repo] = !m.collapsed[r.repo]
			m.rebuildKeepCursor()
			m.savePrefs()
		}

	case key.Matches(msg, keys.FoldAll):
		m.toggleFoldAll()

	case key.Matches(msg, keys.Group):
		m.grouped = !m.grouped
		m.rebuildKeepCursor()
		m.savePrefs()

	case key.Matches(msg, keys.Sort):
		m.sortBy = sortModes[(int(m.sortBy)+1)%len(sortModes)]
		m.setToast("sort: " + m.sortBy.String())
		m.rebuildKeepCursor()
		m.savePrefs()

	case key.Matches(msg, keys.Mode):
		modes := gh.Modes()
		m.cfg.Mode = modes[(int(m.cfg.Mode)+1)%len(modes)]
		m.setToast("mode: " + m.cfg.Mode.String())
		m.prs = nil
		m.loaded = false
		m.lastComplete = false
		m.rows = nil
		// Freshness markers and pending look-ups describe the list we are
		// leaving behind.
		m.changed = map[string]time.Time{}
		m.absent = map[string]*absence{}
		m.cursor, m.top = 0, 0
		m.savePrefs()
		return m, tea.Batch(m.startFetch(), m.spin.Tick)

	case key.Matches(msg, keys.Hide):
		m.toggleHidden()

	case key.Matches(msg, keys.Peek):
		m.showHidden = !m.showHidden
		if m.showHidden {
			m.setToast("showing hidden — h to unhide, H to conceal again")
		} else {
			m.setToast("hidden items concealed")
		}
		m.rebuildKeepCursor()

	case key.Matches(msg, keys.Orgs):
		m.openOrgPicker()

	case key.Matches(msg, keys.Drafts):
		m.hideDrafts = !m.hideDrafts
		if m.hideDrafts {
			m.setToast("hiding drafts")
		} else {
			m.setToast("showing drafts")
		}
		m.rebuildKeepCursor()
		m.savePrefs()

	case key.Matches(msg, keys.Events):
		m.showEvents = !m.showEvents
		m.clampScroll()

	case key.Matches(msg, keys.Filter):
		m.filtering = true
		return m, m.filter.Focus()

	case key.Matches(msg, keys.Refresh):
		if !m.loading {
			return m, tea.Batch(m.startFetch(), m.spin.Tick)
		}
	}
	return m, nil
}

func (m *Model) currentRow() *row {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return &m.rows[m.cursor]
}

func (m *Model) move(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	m.clampScroll()
}

// rebuildKeepCursor re-lays out the list while keeping the selection anchored.
func (m *Model) rebuildKeepCursor() {
	key := m.selectedKey()
	m.rebuild()
	m.restoreCursor(key)
}

// toggleHidden dismisses whatever the cursor is on, or brings it back. The
// same key does both, so peeking at hidden items with H and pressing h on one
// restores it.
func (m *Model) toggleHidden() {
	r := m.currentRow()
	if r == nil {
		return
	}

	if r.isRepo() {
		repo := r.repo
		if m.hiddenRepos[repo] {
			delete(m.hiddenRepos, repo)
			m.setToast("showing " + repo)
		} else {
			m.hiddenRepos[repo] = true
			m.setToast(hideNotice(repo, m.showHidden))
		}
		m.rebuildKeepCursor()
		m.savePrefs()
		return
	}

	key := r.pr.Key()
	switch {
	case m.hiddenPRs[key]:
		delete(m.hiddenPRs, key)
		m.setToast("showing " + key)
	case m.hiddenRepos[r.pr.Repo]:
		// The PR is only hidden by way of its repository; unhide the repo so
		// the keypress does what the user plainly meant.
		delete(m.hiddenRepos, r.pr.Repo)
		m.setToast("showing " + r.pr.Repo)
	default:
		m.hiddenPRs[key] = true
		m.setToast(hideNotice(key, m.showHidden))
	}
	m.rebuildKeepCursor()
	m.savePrefs()
}

func hideNotice(what string, peeking bool) string {
	if peeking {
		return "hid " + what
	}
	return "hid " + what + " — H to show hidden"
}

func (m *Model) toggleFoldAll() {
	anyOpen := false
	for _, r := range m.rows {
		if r.isRepo() && !m.collapsed[r.repo] {
			anyOpen = true
			break
		}
	}
	for _, r := range m.rows {
		if r.isRepo() {
			m.collapsed[r.repo] = anyOpen
		}
	}
	m.rebuildKeepCursor()
	m.savePrefs()
}

func (m *Model) setToast(s string) {
	m.toast = s
	m.toastExpiry = time.Now().Add(4 * time.Second)
}

// listHeight is how many rows of the PR list fit given the panes in play.
func (m *Model) listHeight() int {
	detail, events := m.panes()
	h := m.height - headerHeight - footerHeight
	if detail {
		h -= detailHeight
	}
	if events {
		h -= eventsHeight
	}
	return max(0, h)
}

func (m *Model) clampScroll() {
	h := m.listHeight()
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = max(0, len(m.rows)-1)
	}
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+h {
		m.top = m.cursor - h + 1
	}
	if maxTop := max(0, len(m.rows)-h); m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

// openURL launches the system browser without blocking the UI.
func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		var c *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			c = exec.Command("open", url)
		case "windows":
			c = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		default:
			c = exec.Command("xdg-open", url)
		}
		if err := c.Start(); err == nil {
			go c.Wait()
		}
		return nil
	}
}
