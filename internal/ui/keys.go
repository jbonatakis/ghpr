package ui

import "github.com/charmbracelet/bubbles/key"

type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Home     key.Binding
	End      key.Binding
	Open     key.Binding
	Copy     key.Binding
	Detail   key.Binding
	Fold     key.Binding
	FoldAll  key.Binding
	Group    key.Binding
	Refresh  key.Binding
	Sort     key.Binding
	Mode     key.Binding
	Drafts   key.Binding
	Orgs     key.Binding
	Hide     key.Binding
	Peek     key.Binding
	Filter   key.Binding
	Events   key.Binding
	Help     key.Binding
	Quit     key.Binding
}

var keys = keyMap{
	Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
	Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
	PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up")),
	PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down")),
	Home:     key.NewBinding(key.WithKeys("home", "g"), key.WithHelp("g", "top")),
	End:      key.NewBinding(key.WithKeys("end", "G"), key.WithHelp("G", "bottom")),
	Open:     key.NewBinding(key.WithKeys("enter", "o"), key.WithHelp("enter", "open in browser")),
	Copy:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy url")),
	Detail:   key.NewBinding(key.WithKeys("d", "tab"), key.WithHelp("d", "detail pane")),
	Fold:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "fold repo")),
	FoldAll:  key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "fold all")),
	Group:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "group by repo")),
	Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Sort:     key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
	Mode:     key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mode")),
	Drafts:   key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "toggle drafts")),
	Orgs:     key.NewBinding(key.WithKeys("O"), key.WithHelp("O", "organizations")),
	Hide:     key.NewBinding(key.WithKeys("h"), key.WithHelp("h", "hide / unhide")),
	Peek:     key.NewBinding(key.WithKeys("H"), key.WithHelp("H", "show hidden")),
	Filter:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Events:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "activity")),
	Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c", "esc"), key.WithHelp("q", "quit")),
}

// shortHelp is the footer hint line.
var shortHelp = []key.Binding{
	keys.Up, keys.Down, keys.Open, keys.Detail, keys.Hide, keys.Filter, keys.Sort, keys.Refresh, keys.Help, keys.Quit,
}

// fullHelp is the ? overlay, laid out in columns.
var fullHelp = [][]key.Binding{
	{keys.Up, keys.Down, keys.PageUp, keys.PageDown, keys.Home, keys.End},
	{keys.Open, keys.Copy, keys.Detail, keys.Fold, keys.FoldAll, keys.Group},
	{keys.Hide, keys.Peek, keys.Drafts, keys.Orgs},
	{keys.Sort, keys.Mode, keys.Filter, keys.Events},
	{keys.Refresh, keys.Help, keys.Quit},
}
