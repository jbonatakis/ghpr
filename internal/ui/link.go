package ui

import "github.com/muesli/termenv"

// link marks text as a clickable hyperlink using the OSC 8 escape sequence,
// so a pull request reference can be opened with a click.
//
// The sequence carries no width: terminals that support it (iTerm2, WezTerm,
// Kitty, Windows Terminal, VS Code, GNOME Terminal) render only the text, and
// those that do not ignore it. Because the wrapper is invisible, a linked
// string must be measured with visibleWidth rather than runewidth — and it
// must be applied after padding, never before.
func link(enabled bool, url, text string) string {
	if !enabled || url == "" {
		return text
	}
	return termenv.Hyperlink(url, text)
}
