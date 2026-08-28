package ui

import (
	"fmt"
	"strings"

	"time"

	"github.com/jbonatakis/ghpr/internal/gh"
)

const (
	headerHeight = 3
	footerHeight = 2
	detailHeight = 11
	eventsHeight = 9
	minList      = 3
	// eventRows is how many activity lines fit under the pane's title bar.
	eventRows = eventsHeight - 1
)

// column widths for the PR table, excluding the flexible title.
const (
	wGutter = 2
	wNum    = 6
	wStatus = 9
	wChecks = 7
	wRev    = 4
	wCmt    = 5
	wDiff   = 12
	wAge    = 5
	wIdle   = 5
)

// layout decides which optional columns fit at the current width.
type layout struct {
	title                                int
	showDiff, showIdle, showCmt, showRev bool
}

func (m *Model) layout() layout {
	l := layout{showDiff: true, showIdle: true, showCmt: true, showRev: true}
	switch {
	case m.width < 72:
		l.showRev, l.showCmt, l.showDiff, l.showIdle = false, false, false, false
	case m.width < 84:
		l.showRev, l.showDiff, l.showIdle = false, false, false
	case m.width < 100:
		l.showDiff, l.showIdle = false, false
	case m.width < 116:
		l.showIdle = false
	}
	// Mirrors renderPRRow exactly: gutter, #, title, status, checks and age are
	// always drawn, separated by single spaces; the rest are optional.
	used := wGutter + wNum + wStatus + wChecks + wAge + 4
	if l.showRev {
		used += 1 + wRev
	}
	if l.showCmt {
		used += 1 + wCmt
	}
	if l.showDiff {
		used += 1 + wDiff
	}
	if l.showIdle {
		used += 1 + wIdle
	}
	l.title = m.width - used
	if l.title < 12 {
		l.title = 12
	}
	return l
}

// panes reports which optional panes actually fit in the current height.
func (m *Model) panes() (detail, events bool) {
	detail, events = m.showDetail, m.showEvents
	avail := m.height - headerHeight - footerHeight
	if detail && events && avail < detailHeight+eventsHeight+minList {
		events = false
	}
	if detail && avail < detailHeight+minList {
		detail = false
	}
	if events && avail < eventsHeight+minList {
		events = false
	}
	return detail, events
}

func (m Model) View() string {
	if m.showOrgs {
		return m.orgsView()
	}
	if m.showHelp {
		return m.helpView()
	}

	detail, events := m.panes()

	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.listView())
	if detail {
		b.WriteString(m.detailView())
	}
	if events {
		b.WriteString(m.eventsView())
	}
	b.WriteString(m.footerView())
	return clampFrame(b.String(), m.width, m.height)
}

// clampFrame is the last line of defence against a rendering bug or an
// unexpectedly long upstream message corrupting the alt-screen: no frame may
// be wider or taller than the terminal it is drawn into.
func clampFrame(frame string, width, height int) string {
	lines := strings.Split(frame, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	for i, ln := range lines {
		if visibleWidth(ln) > width {
			lines[i] = truncateToWidth(ln, width)
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) divider() string {
	return stDivider.Render(strings.Repeat("─", max(0, m.width)))
}

// headerView is the brand line, the live status, and the column headings.
func (m Model) headerView() string {
	who := m.viewer
	if who == "" {
		who = "…"
	}
	left := stHeadBar.Render("ghpr") + stMuted.Render(fmt.Sprintf(" %s · %s · %s", who, m.cfg.Mode, m.summary()))
	right := m.refreshStatus(m.width >= 90)

	line1 := fitBar(left, right, m.width)

	l := m.layout()
	var h strings.Builder
	h.WriteString(spaces(wGutter))
	h.WriteString(padLeft("#", wNum))
	h.WriteString(" ")
	h.WriteString(pad("TITLE", l.title))
	h.WriteString(" ")
	h.WriteString(pad("STATUS", wStatus))
	h.WriteString(" ")
	h.WriteString(pad("CHECKS", wChecks))
	if l.showRev {
		h.WriteString(" ")
		h.WriteString(pad("REV", wRev))
	}
	if l.showCmt {
		h.WriteString(" ")
		h.WriteString(padLeft("CMT", wCmt))
	}
	if l.showDiff {
		h.WriteString(" ")
		h.WriteString(padLeft("DIFF", wDiff))
	}
	h.WriteString(" ")
	h.WriteString(padLeft("AGE", wAge))
	if l.showIdle {
		h.WriteString(" ")
		h.WriteString(padLeft("IDLE", wIdle))
	}
	return line1 + "\n" + stFaint.Render(truncateToWidth(h.String(), m.width)) + "\n" + m.divider()
}

// fitBar lays left and right against the edges of a w-wide line, shrinking
// the left side rather than ever overflowing.
func fitBar(left, right string, w int) string {
	if w <= 0 {
		return ""
	}
	rw := visibleWidth(right)
	if rw+2 > w {
		return truncateToWidth(left, w)
	}
	left = truncateToWidth(left, w-rw-1)
	gap := w - visibleWidth(left) - rw
	return left + spaces(gap) + right
}

// summary counts what the dashboard most wants to shout about.
func (m Model) summary() string {
	if !m.loaded {
		return "loading"
	}
	total := len(m.prs)
	var attention, ready int
	for _, p := range m.prs {
		if m.hiddenOrgs[p.Org()] || m.isHidden(p) {
			continue
		}
		switch p.Status() {
		case gh.StatusReadyToMerge:
			ready++
		case gh.StatusChangesRequested, gh.StatusChecksFailing, gh.StatusConflicts:
			attention++
		}
	}
	byOrg := m.orgHiddenCount()
	dismissed := m.hiddenCount()

	parts := []string{fmt.Sprintf("%d open", total-byOrg-dismissed)}
	if byOrg > 0 {
		// Named after the control that governs it, so it is never mistaken for
		// something H would reveal.
		parts = append(parts, fmt.Sprintf("%d in hidden orgs", byOrg))
	}
	if dismissed > 0 {
		label := fmt.Sprintf("%d hidden", dismissed)
		if m.showHidden {
			label += " (shown)"
		}
		parts = append(parts, label)
	}
	if ready > 0 {
		parts = append(parts, fmt.Sprintf("%d ready", ready))
	}
	if attention > 0 {
		parts = append(parts, fmt.Sprintf("%d need work", attention))
	}
	return strings.Join(parts, " · ")
}

// refreshStatus is the right-hand clock: spinner while polling, countdown otherwise.
func (m Model) refreshStatus(showAPI bool) string {
	var live string
	switch {
	case m.loading:
		live = m.spin.View() + stMuted.Render(" syncing")
	case m.err != nil:
		live = stRed.Render("● offline")
	case m.warn != "":
		d := m.nextFetch.Sub(m.now)
		if d < 0 {
			d = 0
		}
		live = stYellow.Render("●") + stMuted.Render(fmt.Sprintf(" retry %ds", int(d.Seconds())+1))
	default:
		d := m.nextFetch.Sub(m.now)
		if d < 0 {
			d = 0
		}
		live = stGreen.Render("●") + stMuted.Render(fmt.Sprintf(" %ds", int(d.Seconds())+1))
	}
	rate := ""
	if showAPI && m.rate.Limit > 0 {
		rate = stFaint.Render(fmt.Sprintf("  %d/%d pts/hr", m.rate.Remaining, m.rate.Limit))
	}
	return live + rate
}

// listView renders the scrolled window of repo headers and PR rows. It always
// emits exactly listHeight lines so the panes below it stay put.
func (m Model) listView() string {
	h := m.listHeight()
	if h <= 0 {
		return ""
	}
	out := make([]string, 0, h)

	if len(m.rows) == 0 {
		msg := "no open pull requests"
		if !m.loaded {
			msg = "loading pull requests…"
		} else if strings.TrimSpace(m.filter.Value()) != "" {
			msg = "nothing matches this filter"
		}
		out = append(out, "")
		out = append(out, "  "+stMuted.Render(msg))
	} else {
		l := m.layout()
		end := min(len(m.rows), m.top+h)
		for i := m.top; i < end; i++ {
			out = append(out, truncateToWidth(m.renderRow(m.rows[i], i == m.cursor, l), m.width))
		}
	}

	var b strings.Builder
	for i := 0; i < h; i++ {
		if i < len(out) {
			b.WriteString(out[i])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) renderRow(r row, sel bool, l layout) string {
	if r.isRepo() {
		return m.renderRepoRow(r, sel)
	}
	return m.renderPRRow(*r.pr, sel, r.hidden, l)
}

func (m Model) renderRepoRow(r row, sel bool) string {
	// rowPainter carries the selection background through every cell so the
	// highlight reads as one continuous band across a wide terminal.
	p := painter{sel: sel}

	arrow := "▾"
	if m.collapsed[r.repo] {
		arrow = "▸"
	}
	var b strings.Builder
	if sel {
		b.WriteString(p.cell(stCursor, "▸ "))
	} else {
		b.WriteString(p.cell(stText, spaces(wGutter)))
	}
	b.WriteString(p.cell(stMuted, arrow+" "))
	nameStyle := stRepo
	if r.hidden {
		nameStyle = stFaint
	}
	b.WriteString(p.cell(nameStyle, r.repo))
	b.WriteString(p.cell(stFaint, fmt.Sprintf(" (%d)", r.count)))
	switch {
	case r.hidden:
		b.WriteString(p.cell(stText, "  "))
		b.WriteString(p.cell(stFaint, "HIDDEN"))
	case r.urgent <= gh.StatusConflicts:
		b.WriteString(p.cell(stText, "  "))
		b.WriteString(p.cell(statusStyle(r.urgent), r.urgent.Short()))
	}
	if r.fresh {
		b.WriteString(p.cell(stText, " "))
		b.WriteString(p.cell(stFresh, "●"))
	}
	return p.finish(b.String(), m.width)
}

func (m Model) renderPRRow(pr gh.PR, sel, hidden bool, l layout) string {
	p := painter{sel: sel}
	var b strings.Builder

	switch {
	case sel:
		b.WriteString(p.cell(stCursor, "▸ "))
	case m.isFresh(pr.Key()):
		b.WriteString(p.cell(stFresh, "● "))
	default:
		b.WriteString(p.cell(stText, spaces(wGutter)))
	}

	b.WriteString(link(m.cfg.Links, pr.URL, p.cell(stMuted, padLeft(fmt.Sprintf("#%d", pr.Number), wNum))))
	b.WriteString(p.sep())

	title := pr.Title
	if !m.grouped {
		title = pr.RepoName() + "  " + title
	}
	titleStyle := stText
	switch {
	case hidden:
		titleStyle = stFaint
	case sel:
		titleStyle = stSelected
	case pr.IsDraft:
		titleStyle = stMuted
	}
	b.WriteString(p.cell(titleStyle, pad(title, l.title)))
	b.WriteString(p.sep())

	st := pr.Status()
	if hidden {
		b.WriteString(p.cell(stFaint, pad("HIDDEN", wStatus)))
	} else {
		b.WriteString(p.cell(statusStyle(st), pad(st.Short(), wStatus)))
	}
	b.WriteString(p.sep())

	b.WriteString(m.checksCell(pr, wChecks, p))

	if l.showRev {
		b.WriteString(p.sep())
		glyph, style := reviewGlyph(pr)
		b.WriteString(p.cell(style, pad(glyph, wRev)))
	}
	if l.showCmt {
		b.WriteString(p.sep())
		cmt := ""
		style := stMuted
		if n := pr.Comments(); n > 0 {
			cmt = fmt.Sprintf("%d", n)
		}
		if pr.UnresolvedThreads > 0 {
			style = stYellow
			cmt = fmt.Sprintf("%d!", pr.Comments())
		}
		if pr.ThreadsTruncated && cmt != "" {
			cmt += "+"
		}
		b.WriteString(p.cell(style, padLeft(cmt, wCmt)))
	}
	if l.showDiff {
		b.WriteString(p.sep())
		b.WriteString(p.cell(stFaint, padLeft(diffStat(pr.Additions, pr.Deletions), wDiff)))
	}
	b.WriteString(p.sep())
	b.WriteString(p.cell(stMuted, padLeft(compactAge(pr.Age(m.now)), wAge)))
	if l.showIdle {
		b.WriteString(p.sep())
		idle := pr.Idle(m.now)
		style := stFaint
		if idle > 7*24*time.Hour {
			style = stYellow
		}
		b.WriteString(p.cell(style, padLeft(compactAge(idle), wIdle)))
	}
	return p.finish(b.String(), m.width)
}

// checksCell shows the rollup glyph plus a passed/total tally.
func (m Model) checksCell(pr gh.PR, w int, p painter) string {
	total := pr.ChecksPassed + pr.ChecksFailed + pr.ChecksPending
	if total == 0 {
		return p.cell(stFaint, pad("—", w))
	}
	glyph, glyphStyle := checkGlyph(pr)
	body := pad(fmt.Sprintf("%d/%d", pr.ChecksPassed, total), w-2)
	return p.cell(glyphStyle, glyph) + p.sep() + p.cell(checkStateStyle(pr.ChecksState), body)
}
