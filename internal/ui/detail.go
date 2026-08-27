package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// detailView is the expandable pane describing the selected pull request.
func (m Model) detailView() string {
	var b strings.Builder
	b.WriteString(m.divider())
	b.WriteString("\n")

	p := m.selected()
	if p == nil {
		b.WriteString(" " + stMuted.Render("select a pull request to see details") + "\n")
		for i := 2; i < detailHeight; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	lines := make([]string, 0, detailHeight)
	lines = append(lines, stBold.Render(fmt.Sprintf("#%d ", p.Number))+stText.Render(p.Title))

	meta := []string{stRepo.Render(p.Repo), stMuted.Render(p.Author)}
	if p.HeadRef != "" {
		meta = append(meta, stFaint.Render(p.HeadRef+" → "+p.BaseRef))
	}
	lines = append(lines, strings.Join(meta, stFaint.Render("  ·  ")))

	lines = append(lines, stMuted.Render(fmt.Sprintf(
		"opened %s · updated %s · %s across %s",
		longAge(p.Age(m.now)), longAge(p.Idle(m.now)),
		diffStat(p.Additions, p.Deletions), plural(p.ChangedFiles, "file"),
	)))

	st := p.Status()
	state := []string{statusStyle(st).Render(st.Label())}
	switch p.Mergeable {
	case "CONFLICTING":
		state = append(state, stRed.Render("conflicts with "+p.BaseRef))
	case "MERGEABLE":
		state = append(state, stGreen.Render("no conflicts"))
	}
	if p.IsDraft {
		state = append(state, stFaint.Render("draft"))
	}
	lines = append(lines, stFaint.Render("state    ")+strings.Join(state, stFaint.Render(" · ")))

	lines = append(lines, stFaint.Render("review   ")+m.reviewersLine(*p))
	lines = append(lines, stFaint.Render("comments ")+m.commentsLine(*p))
	lines = append(lines, stFaint.Render("checks   ")+m.checksLine(*p))
	if len(p.Labels) > 0 {
		names := make([]string, 0, len(p.Labels))
		for _, l := range p.Labels {
			names = append(names, labelStyle(l).Render(l.Name))
		}
		lines = append(lines, stFaint.Render("labels   ")+strings.Join(names, " "))
	}
	lines = append(lines, stFaint.Render("url      ")+stBlue.Render(link(m.cfg.Links, p.URL, p.URL)))

	for i, ln := range lines {
		if i >= detailHeight-1 {
			break
		}
		b.WriteString(" " + truncateToWidth(ln, m.width-1) + "\n")
	}
	for i := len(lines) + 1; i < detailHeight; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) reviewersLine(p gh.PR) string {
	if len(p.Reviewers) == 0 {
		return stMuted.Render("no reviewers requested")
	}
	parts := make([]string, 0, len(p.Reviewers))
	for _, r := range p.Reviewers {
		name := r.Login
		if r.Team {
			name = "@" + name
		}
		switch r.State {
		case "APPROVED":
			parts = append(parts, stGreen.Render("✓ "+name))
		case "CHANGES_REQUESTED":
			parts = append(parts, stRed.Render("± "+name))
		case "COMMENTED":
			parts = append(parts, stYellow.Render("· "+name))
		case "DISMISSED":
			parts = append(parts, stFaint.Render("· "+name))
		default:
			parts = append(parts, stBlue.Render("◷ "+name))
		}
	}
	return strings.Join(parts, stFaint.Render("  "))
}

func (m Model) commentsLine(p gh.PR) string {
	if p.Comments() == 0 {
		return stMuted.Render("none")
	}
	counted := "%d total (%d conversation, %d in %s)"
	if p.ThreadsTruncated {
		counted = "%d+ total (%d conversation, %d in the first of %s)"
	}
	s := fmt.Sprintf(counted,
		p.Comments(), p.IssueComments, p.ReviewComments, plural(p.TotalThreads, "thread"))
	out := stMuted.Render(s)
	if p.UnresolvedThreads > 0 {
		out += stYellow.Render(fmt.Sprintf("  %s unresolved", plural(p.UnresolvedThreads, "thread")))
	}
	return out
}

func (m Model) checksLine(p gh.PR) string {
	if len(p.Checks) == 0 {
		return stMuted.Render("no checks reported")
	}
	// Failures first, then anything still running: that is what the eye wants.
	ordered := make([]gh.Check, 0, len(p.Checks))
	for _, want := range []gh.CheckState{gh.CheckFailure, gh.CheckPending, gh.CheckSuccess, gh.CheckSkipped, gh.CheckNone} {
		for _, c := range p.Checks {
			if c.State == want {
				ordered = append(ordered, c)
			}
		}
	}
	parts := make([]string, 0, len(ordered))
	for _, c := range ordered {
		parts = append(parts, checkStateStyle(c.State).Render(checkStateGlyph(c.State)+" "+c.Name))
	}
	return strings.Join(parts, stFaint.Render("  "))
}

func labelStyle(l gh.Label) lipgloss.Style {
	if l.Color == "" {
		return stFaint
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#" + l.Color))
}

// eventsView is the rolling feed of everything that changed while open.
func (m Model) eventsView() string {
	var b strings.Builder
	b.WriteString(stDivider.Render(strings.Repeat("─", max(0, min(m.width, 10)))))
	b.WriteString(stFaint.Render(" activity "))
	rest := m.width - 10 - len(" activity ")
	if rest > 0 {
		b.WriteString(stDivider.Render(strings.Repeat("─", rest)))
	}
	b.WriteString("\n")

	rows := eventsHeight - 1
	// Deliberately the whole log: activity is a session-wide record, not a
	// view of the current mode. Switching mode or hiding a pull request does
	// not un-happen what it did.
	if len(m.events) == 0 {
		b.WriteString(" " + stMuted.Render("watching for changes…") + "\n")
		for i := 2; i <= rows; i++ {
			b.WriteString("\n")
		}
		return b.String()
	}

	start := max(0, len(m.events)-rows)
	shown := m.events[start:]
	refWidth := eventRefWidth(m.width)
	// The actor sits in its own column so a run of activity can be scanned by
	// who caused it, not just by what happened.
	actorWidth := max(0, m.width-(1+evTimeWidth+2+refWidth+2+evWhatWidth+2))
	if actorWidth > evActorWidth {
		actorWidth = evActorWidth
	}

	for i := len(shown) - 1; i >= 0; i-- {
		e := shown[i]
		ts := stFaint.Render(e.At.Local().Format("15:04:05"))
		// Padded after linking: the escape sequence has no width, so the cell
		// must be measured with visibleWidth.
		refText := link(m.cfg.Links, e.URL, eventRefText(shortRepo(e.Repo), e.Number, refWidth))
		ref := stMuted.Render(padVisible(refText, refWidth))
		what := eventStyle(e.Kind).Render(pad(e.Kind.Icon()+" "+e.Text, evWhatWidth))

		line := fmt.Sprintf(" %s  %s  %s", ts, ref, what)
		if actorWidth > 0 && e.Actor != "" {
			line += "  " + stActor.Render(pad(e.Actor, actorWidth))
		}
		b.WriteString(truncateToWidth(strings.TrimRight(line, " "), m.width) + "\n")
	}
	for i := len(shown); i < rows; i++ {
		b.WriteString("\n")
	}
	return b.String()
}

func shortRepo(repo string) string {
	if i := strings.IndexByte(repo, '/'); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// footerView shows errors and toasts above the key hints.
func (m Model) footerView() string {
	var b strings.Builder
	b.WriteString(m.divider())
	b.WriteString("\n")

	// Everything here is forced onto a single line: an upstream message can be
	// multi-line HTML, and a footer that grows pushes the list off the screen.
	var line string
	switch {
	case m.filtering:
		line = m.filter.View()
	case m.err != nil:
		line = stErr.Render("error: ") + stText.Render(oneLine(m.err.Error()))
	case m.warn != "":
		line = stYellow.Render("! ") + stMuted.Render(oneLine(m.warn)+" · retrying")
	case m.toast != "":
		line = stYellow.Render(oneLine(m.toast))
	default:
		line = m.hintsView()
	}
	b.WriteString(" " + truncateToWidth(line, max(1, m.width-2)))
	return b.String()
}

// oneLine flattens anything destined for the single-row footer.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (m Model) hintsView() string {
	parts := make([]string, 0, len(shortHelp))
	for _, k := range shortHelp {
		h := k.Help()
		parts = append(parts, stKey.Render(h.Key)+stFaint.Render(" "+h.Desc))
	}
	line := strings.Join(parts, stFaint.Render(" · "))
	return truncateToWidth(line, max(10, m.width-2))
}

// helpView takes over the screen with the full keymap.
func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(stTitle.Render(" ghpr — keys"))
	b.WriteString("\n\n")

	for _, col := range fullHelp {
		for _, k := range col {
			h := k.Help()
			b.WriteString("   " + stKey.Render(pad(h.Key, 10)) + stMuted.Render(h.Desc) + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(stFaint.Render("   markers"))
	b.WriteString("\n")
	for _, l := range [][2]string{
		{stFresh.Render("●"), "changed in the last minute"},
		{stGreen.Render("✓") + "/" + stRed.Render("✗") + "/" + stYellow.Render("•"), "checks passing / failing / running"},
		{stGreen.Render("✓") + "/" + stRed.Render("±") + "/" + stBlue.Render("◷"), "approved / changes requested / review pending"},
		{stFaint.Render("HIDDEN"), "dismissed with h; only listed while H is on"},
		{stYellow.Render("!"), "unresolved review threads"},
		{stMuted.Render("+"), "more review threads than one page shows"},
	} {
		b.WriteString("   " + padVisible(l[0], 10) + stMuted.Render(l[1]) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(stFaint.Render("   sort cycles: "))
	names := make([]string, 0, len(sortModes))
	for _, s := range sortModes {
		names = append(names, s.String())
	}
	b.WriteString(stMuted.Render(strings.Join(names, " → ")))
	b.WriteString("\n")
	b.WriteString(stFaint.Render("   mode cycles: "))
	mnames := make([]string, 0)
	for _, md := range gh.Modes() {
		mnames = append(mnames, md.String())
	}
	b.WriteString(stMuted.Render(strings.Join(mnames, " → ")))
	b.WriteString("\n\n")
	b.WriteString(stFaint.Render(fmt.Sprintf("   polling every %s · about %d of 5000 rate-limit points per hour",
		m.cfg.Interval, m.pointsPerHour())))
	b.WriteString("\n")
	if path, err := config.Path(); err == nil {
		b.WriteString(stFaint.Render("   settings saved to " + path))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(stFaint.Render("   press any key to return"))
	return b.String()
}

var _ = key.Binding{}
