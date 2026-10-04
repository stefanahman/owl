package main

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// ------------------------------------------------------------
// Keymap
// ------------------------------------------------------------

type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Home     key.Binding
	End      key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Enter    key.Binding
	Start    key.Binding
	Browser  key.Binding
	Yank     key.Binding
	Next     key.Binding
	Cleanup  key.Binding
	Pane     key.Binding
	Drill    key.Binding
	Back     key.Binding
	Search   key.Binding
	Cancel   key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Quit     key.Binding
	Bindings []key.Binding // parallel to the list's Config.Bindings
}

// newKeyMap builds the key bindings from `keys` and one list's
// `bindings`.
func newKeyMap(k KeysConfig, bindings []Binding) keyMap {
	bind := func(keys keyNames, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys.label(), desc))
	}
	km := keyMap{
		Up:       bind(k.Up, "up"),
		Down:     bind(k.Down, "down"),
		Home:     bind(k.Top, "top"),
		End:      bind(k.Bottom, "bottom"),
		PageUp:   bind(k.PageUp, "page up"),
		PageDown: bind(k.PageDown, "page down"),
		Enter:    bind(k.Open, "open review"),
		Start:    bind(k.Start, "start (stay)"),
		Browser:  bind(k.Browser, "open PR in browser"),
		Yank:     bind(k.Yank, "yank PR URL"),
		Next:     bind(k.Next, "next attention-needed"),
		Cleanup:  bind(k.Cleanup, "clean up worktree"),
		Pane:     bind(k.Pane, "other pane"),
		Drill:    bind(k.Drill, "the project's issues"),
		Back:     bind(k.Back, "back"),
		Search:   bind(k.Search, "search"),
		Cancel:   bind(k.Cancel, "cancel/clear"),
		Refresh:  bind(k.Refresh, "refresh"),
		Help:     bind(k.Help, "help"),
		Quit:     bind(k.Quit, "quit"),
	}
	for _, b := range bindings {
		km.Bindings = append(km.Bindings, bind(b.Key, b.Name))
	}
	return km
}

var keyGlyphs = map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→", "enter": "↵", "pgdown": "pgdn"}

// label renders key names for the help views: the first key always,
// further keys only when they are single characters (`↑/k`, but `q`
// rather than `q/ctrl+c` — named alternates are noise in a legend).
func (k keyNames) label() string {
	var parts []string
	for i, name := range k {
		if i > 0 && len([]rune(name)) != 1 {
			continue
		}
		if g, ok := keyGlyphs[name]; ok {
			name = g
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, "/")
}

// ShortHelp drives the footer legend: the built-in actions. FullHelp
// is rendered inside the `?` modal (helpModalView) via
// help.FullHelpView, and lists the user's bindings by name.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Next, k.Enter, k.Start, k.Browser, k.Search, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	actions := append([]key.Binding{k.Enter, k.Start, k.Browser, k.Yank, k.Cleanup, k.Pane, k.Drill, k.Back}, k.Bindings...)
	return [][]key.Binding{
		{k.Up, k.Down, k.Next, k.Home, k.End, k.PageUp, k.PageDown},
		actions,
		{k.Search, k.Cancel, k.Refresh, k.Help, k.Quit},
	}
}
