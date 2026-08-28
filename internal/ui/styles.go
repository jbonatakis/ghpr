package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/jbonatakis/ghpr/internal/gh"
)

// Primer-ish palette, adaptive so the dashboard reads on light and dark terminals.
var (
	colGreen  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#3fb950"}
	colRed    = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#f85149"}
	colYellow = lipgloss.AdaptiveColor{Light: "#9a6700", Dark: "#d29922"}
	colBlue   = lipgloss.AdaptiveColor{Light: "#0969da", Dark: "#58a6ff"}
	colPurple = lipgloss.AdaptiveColor{Light: "#8250df", Dark: "#bc8cff"}
	colGray   = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	colFaint  = lipgloss.AdaptiveColor{Light: "#afb8c1", Dark: "#484f58"}
	colText   = lipgloss.AdaptiveColor{Light: "#1f2328", Dark: "#e6edf3"}
	colSelBg  = lipgloss.AdaptiveColor{Light: "#dde3ea", Dark: "#2a3038"}
)

var (
	stTitle    = lipgloss.NewStyle().Bold(true).Foreground(colPurple)
	stMuted    = lipgloss.NewStyle().Foreground(colGray)
	stFaint    = lipgloss.NewStyle().Foreground(colFaint)
	stText     = lipgloss.NewStyle().Foreground(colText)
	stBold     = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stGreen    = lipgloss.NewStyle().Foreground(colGreen)
	stRed      = lipgloss.NewStyle().Foreground(colRed)
	stYellow   = lipgloss.NewStyle().Foreground(colYellow)
	stBlue     = lipgloss.NewStyle().Foreground(colBlue)
	stRepo     = lipgloss.NewStyle().Bold(true).Foreground(colBlue)
	stSelected = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stCursor   = lipgloss.NewStyle().Foreground(colPurple).Bold(true)
	stKey      = lipgloss.NewStyle().Bold(true).Foreground(colText)
	stErr      = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	stDivider  = lipgloss.NewStyle().Foreground(colFaint)
	stHeadBar  = lipgloss.NewStyle().Bold(true).Foreground(colPurple)
	stFresh    = lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	stActor    = lipgloss.NewStyle().Foreground(colPurple)
)

// statusStyle colors the status badge by urgency.
func statusStyle(s gh.Status) lipgloss.Style {
	switch s {
	case gh.StatusReadyToMerge:
		return lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	case gh.StatusChangesRequested, gh.StatusChecksFailing:
		return lipgloss.NewStyle().Foreground(colRed).Bold(true)
	case gh.StatusConflicts, gh.StatusUnresolved:
		return lipgloss.NewStyle().Foreground(colYellow)
	case gh.StatusAwaitingReview:
		return lipgloss.NewStyle().Foreground(colBlue)
	}
	return stFaint
}

// checkGlyph renders the CI rollup as a symbol plus the style that colors it.
// Returning the pair lets the caller layer a selection background underneath
// without having to re-parse escape sequences.
func checkGlyph(p gh.PR) (string, lipgloss.Style) {
	switch p.ChecksState {
	case gh.CheckSuccess:
		return "✓", stGreen
	case gh.CheckFailure:
		return "✗", stRed
	case gh.CheckPending:
		return "•", stYellow
	case gh.CheckSkipped:
		return "–", stFaint
	}
	return " ", stFaint
}

func checkStateStyle(s gh.CheckState) lipgloss.Style {
	switch s {
	case gh.CheckSuccess:
		return stGreen
	case gh.CheckFailure:
		return stRed
	case gh.CheckPending:
		return stYellow
	}
	return stFaint
}

func checkStateGlyph(s gh.CheckState) string {
	switch s {
	case gh.CheckSuccess:
		return "✓"
	case gh.CheckFailure:
		return "✗"
	case gh.CheckPending:
		return "•"
	case gh.CheckSkipped:
		return "–"
	}
	return "?"
}

// reviewGlyph summarizes the review decision as a symbol and its style.
func reviewGlyph(p gh.PR) (string, lipgloss.Style) {
	switch p.ReviewDecision {
	case "APPROVED":
		return "✓", stGreen
	case "CHANGES_REQUESTED":
		return "±", stRed
	}
	if len(p.PendingReviewers()) > 0 {
		return "◷", stBlue
	}
	return "·", stFaint
}

func eventStyle(k gh.EventKind) lipgloss.Style {
	switch k {
	case gh.EventOpened, gh.EventReadyForReview:
		return stBlue
	case gh.EventArrived:
		return lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	case gh.EventMerged:
		return stPurpleText()
	case gh.EventClosed:
		return stRed
	case gh.EventComment:
		return stYellow
	case gh.EventChecks:
		return stGreen
	case gh.EventReview:
		return stGreen
	case gh.EventConflict:
		return stRed
	case gh.EventPush:
		return stBlue
	case gh.EventReviewRequested:
		// Bold, like an arrival: both are the feed saying this one wants you.
		return lipgloss.NewStyle().Foreground(colBlue).Bold(true)
	case gh.EventMention:
		// A louder comment, because that is what it is.
		return lipgloss.NewStyle().Foreground(colYellow).Bold(true)
	}
	return stMuted
}

func stPurpleText() lipgloss.Style { return lipgloss.NewStyle().Foreground(colPurple) }
