package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// orgEntry is one line in the organization picker.
type orgEntry struct {
	name    string
	prs     int
	visible bool
}

// orgEntries lists every organization worth offering: those with pull requests
// right now, plus any that are hidden — otherwise a hidden org would vanish
// from the picker and could never be switched back on.
func (m *Model) orgEntries() []orgEntry {
	counts := map[string]int{}
	for _, p := range m.prs {
		counts[p.Org()]++
	}
	for org := range m.orgDraft {
		if _, ok := counts[org]; !ok {
			counts[org] = 0
		}
	}
	for org := range m.hiddenOrgs {
		if _, ok := counts[org]; !ok {
			counts[org] = 0
		}
	}

	out := make([]orgEntry, 0, len(counts))
	for org, n := range counts {
		out = append(out, orgEntry{name: org, prs: n, visible: !m.orgDraft[org]})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	return out
}

// openOrgPicker takes a working copy of the current filter so that escape can
// abandon the edit cleanly.
func (m *Model) openOrgPicker() {
	m.orgDraft = map[string]bool{}
	for org, hidden := range m.hiddenOrgs {
		if hidden {
			m.orgDraft[org] = true
		}
	}
	m.showOrgs = true
	m.orgCursor = 0
}

func (m Model) handleOrgKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.orgEntries()

	switch msg.String() {
	case "esc", "q":
		m.showOrgs = false
		m.orgDraft = nil
		return m, nil

	case "enter":
		m.hiddenOrgs = map[string]bool{}
		for org, hidden := range m.orgDraft {
			if hidden {
				m.hiddenOrgs[org] = true
			}
		}
		m.showOrgs = false
		m.orgDraft = nil
		m.rebuildKeepCursor()
		if n := len(m.hiddenOrgs); n == 0 {
			m.setToast("showing all organizations")
		} else {
			m.setToast(fmt.Sprintf("hiding %s", plural(n, "organization")))
		}
		m.savePrefs()
		return m, nil

	case "up", "k":
		if m.orgCursor > 0 {
			m.orgCursor--
		}
	case "down", "j":
		if m.orgCursor < len(entries)-1 {
			m.orgCursor++
		}
	case " ":
		if m.orgCursor < len(entries) {
			name := entries[m.orgCursor].name
			if m.orgDraft[name] {
				delete(m.orgDraft, name)
			} else {
				m.orgDraft[name] = true
			}
		}
	case "a":
		m.orgDraft = map[string]bool{}
	case "n":
		for _, e := range entries {
			m.orgDraft[e.name] = true
		}
	case "o":
		// Focus a single org: show this one, hide the rest.
		if m.orgCursor < len(entries) {
			keep := entries[m.orgCursor].name
			m.orgDraft = map[string]bool{}
			for _, e := range entries {
				if e.name != keep {
					m.orgDraft[e.name] = true
				}
			}
		}
	}
	return m, nil
}

// orgsView is the full-screen organization picker.
func (m Model) orgsView() string {
	entries := m.orgEntries()

	var b strings.Builder
	b.WriteString(stTitle.Render(" ghpr — organizations"))
	b.WriteString("\n\n")
	b.WriteString("   " + stMuted.Render("Choose which organizations appear in the dashboard. Saved to your config."))
	b.WriteString("\n\n")

	if len(entries) == 0 {
		b.WriteString("   " + stMuted.Render("no organizations seen yet — wait for the first refresh"))
		b.WriteString("\n")
	}

	var shownPRs, hiddenPRs int
	for i, e := range entries {
		cursor := "  "
		if i == m.orgCursor {
			cursor = stCursor.Render("▸ ")
		}
		box := stFaint.Render("[ ]")
		name := stMuted.Render(pad(e.name, 30))
		if e.visible {
			box = stGreen.Render("[✓]")
			name = stBold.Render(pad(e.name, 30))
			shownPRs += e.prs
		} else {
			hiddenPRs += e.prs
		}
		count := stFaint.Render(plural(e.prs, "pull request"))
		b.WriteString(" " + cursor + box + " " + name + count + "\n")
	}

	b.WriteString("\n")
	b.WriteString("   " + stMuted.Render(fmt.Sprintf("%d shown · %d hidden", shownPRs, hiddenPRs)))
	b.WriteString("\n\n")

	for _, h := range [][2]string{
		{"space", "show or hide"},
		{"o", "only this one"},
		{"a", "show all"},
		{"n", "hide all"},
		{"enter", "save"},
		{"esc", "cancel"},
	} {
		b.WriteString("   " + stKey.Render(pad(h[0], 8)) + stMuted.Render(h[1]) + "\n")
	}
	return b.String()
}
