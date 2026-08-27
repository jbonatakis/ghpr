package ui

import "github.com/charmbracelet/lipgloss"

// painter draws a row's cells, optionally laying a selection background under
// every one of them. Applying the background per cell — rather than wrapping
// the finished string — is what keeps each column's own color intact: an
// outer style cannot reach past the escape resets the inner cells emit.
type painter struct{ sel bool }

// cell renders text in style, adding the selection background when active.
func (p painter) cell(style lipgloss.Style, text string) string {
	if p.sel {
		style = style.Background(colSelBg)
	}
	return style.Render(text)
}

// sep is the single space between columns, carrying the highlight across gaps.
func (p painter) sep() string { return p.cell(lipgloss.NewStyle(), " ") }

// finish pads a row out to the full terminal width so the highlight runs to
// the right edge instead of stopping at the last column.
func (p painter) finish(row string, width int) string {
	if !p.sel {
		return truncateToWidth(row, width)
	}
	row = truncateToWidth(row, width)
	if gap := width - visibleWidth(row); gap > 0 {
		row += p.cell(lipgloss.NewStyle(), spaces(gap))
	}
	return row
}
