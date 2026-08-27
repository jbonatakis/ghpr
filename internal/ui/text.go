package ui

import "github.com/charmbracelet/x/ansi"

// truncateToWidth trims a possibly-styled string to w display cells,
// leaving escape sequences intact.
func truncateToWidth(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// visibleWidth is the rendered width of a styled string.
func visibleWidth(s string) int { return ansi.StringWidth(s) }

// padVisible right-pads an already-styled string to w rendered cells. Use this
// instead of pad when the input may contain escape sequences, which a
// byte-oriented truncate would slice apart.
func padVisible(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if d := w - visibleWidth(s); d > 0 {
		return s + spaces(d)
	}
	return truncateToWidth(s, w)
}
