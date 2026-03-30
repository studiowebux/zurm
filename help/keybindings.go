package help

import "strings"

// KeyBinding describes a single keyboard shortcut.
type KeyBinding struct {
	Category    string
	Key         string
	Description string
}

// AllBindings returns the canonical list of keybindings shown in the overlay.
func AllBindings() []KeyBinding {
	return []KeyBinding{
		{Category: "Navigation", Key: "Cmd+T", Description: "New tab"},
		{Category: "Navigation", Key: "Cmd+Shift+B", Description: "New server tab (Mode B)"},
		{Category: "Navigation", Key: "Cmd+Shift+T", Description: "Tab switcher"},
		{Category: "Navigation", Key: "Cmd+Shift+R", Description: "Rename tab"},
		{Category: "Navigation", Key: "Cmd+Shift+N", Description: "Edit tab note"},
		{Category: "Navigation", Key: "Cmd+Shift+K", Description: "Park tab (hide, keep alive)"},
		{Category: "Navigation", Key: "Cmd+W", Description: "Close tab / pane"},
		{Category: "Navigation", Key: "Cmd+1\u20139", Description: "Switch to tab N"},
		{Category: "Pins", Key: "Cmd+G", Description: "Enter pin mode"},
		{Category: "Pins", Key: "a \u2013 l", Description: "Jump to pinned slot"},
		{Category: "Pins", Key: "\u21e7a \u2013 \u21e7l", Description: "Pin / unpin active tab"},
		{Category: "Navigation", Key: "Cmd+;", Description: "Go back (focus history)"},
		{Category: "Navigation", Key: "Cmd+Shift+[", Description: "Previous tab"},
		{Category: "Navigation", Key: "Cmd+Shift+]", Description: "Next tab"},
		{Category: "Navigation", Key: "Cmd+Shift+←/→", Description: "Move tab (keyboard or drag)"},
		{Category: "Navigation", Key: "Cmd+J", Description: "Search tabs by name"},
		{Category: "Navigation", Key: "Cmd+E", Description: "Toggle file explorer"},
		{Category: "File Explorer", Key: "↑ / ↓", Description: "Navigate entries"},
		{Category: "File Explorer", Key: "Enter / →", Description: "Expand directory or open file"},
		{Category: "File Explorer", Key: "←", Description: "Collapse directory"},
		{Category: "File Explorer", Key: "n", Description: "New file"},
		{Category: "File Explorer", Key: "Shift+N", Description: "New directory"},
		{Category: "File Explorer", Key: "r", Description: "Rename"},
		{Category: "File Explorer", Key: "d", Description: "Delete (with confirmation)"},
		{Category: "File Explorer", Key: "c", Description: "Copy path"},
		{Category: "File Explorer", Key: "o", Description: "Open in Finder"},
		{Category: "File Explorer", Key: "Esc", Description: "Close explorer"},
		{Category: "Panes", Key: "Cmd+P → detach", Description: "Detach pane to new tab"},
		{Category: "Panes", Key: "Cmd+P → move", Description: "Move pane to next/prev tab"},
		{Category: "Panes", Key: "Cmd+D", Description: "Split horizontal"},
		{Category: "Panes", Key: "Cmd+Shift+D", Description: "Split vertical"},
		{Category: "Panes", Key: "Cmd+Shift+H", Description: "Split horizontal (server)"},
		{Category: "Panes", Key: "Cmd+Shift+V", Description: "Split vertical (server)"},
		{Category: "Panes", Key: "Cmd+Arrow", Description: "Focus adjacent pane"},
		{Category: "Panes", Key: "Cmd+Opt+Arrow", Description: "Resize pane split"},
		{Category: "Panes", Key: "Cmd+[ / ]", Description: "Cycle panes"},
		{Category: "Panes", Key: "Cmd+Z", Description: "Zoom pane"},
		{Category: "Panes", Key: "Drag divider", Description: "Resize pane split"},
		{Category: "Panes", Key: "Double-click header", Description: "Rename pane"},
		{Category: "Scroll", Key: "Shift+PgUp", Description: "Scroll up"},
		{Category: "Scroll", Key: "Shift+PgDn", Description: "Scroll down"},
		{Category: "Scroll", Key: "Mouse wheel", Description: "Scroll"},
		{Category: "Scroll", Key: "Shift+Wheel", Description: "Scroll (override PTY mouse mode)"},
		{Category: "Scroll", Key: "Cmd+K", Description: "Clear scrollback"},
		{Category: "Copy / Paste", Key: "Cmd+C", Description: "Copy selection"},
		{Category: "Copy / Paste", Key: "Cmd+V", Description: "Paste"},
		{Category: "Copy / Paste", Key: "Click+drag", Description: "Select text (auto-scrolls)"},
		{Category: "Copy / Paste", Key: "Shift+Click", Description: "Extend selection to click"},
		{Category: "Copy / Paste", Key: "Double-click", Description: "Select word"},
		{Category: "Copy / Paste", Key: "Triple-click", Description: "Select line"},
		{Category: "URLs", Key: "Cmd+Click", Description: "Open URL in browser"},
		{Category: "Search", Key: "Cmd+F", Description: "Open in-buffer search"},
		{Category: "Search", Key: "↓", Description: "Next match"},
		{Category: "Search", Key: "↑", Description: "Previous match"},
		{Category: "Search", Key: "Esc", Description: "Close search"},
		{Category: "Blocks", Key: "Cmd+B", Description: "Toggle command blocks on/off"},
		{Category: "Blocks", Key: "Cmd+P → hooks", Description: "Install shell hooks (OSC 133)"},
		{Category: "Recording", Key: "Cmd+Shift+S", Description: "Take screenshot (PNG)"},
		{Category: "Recording", Key: "Cmd+Shift+.", Description: "Start / stop screen recording (MP4)"},
		{Category: "Help", Key: "Cmd+/", Description: "Toggle keybindings"},
		{Category: "Help", Key: "Cmd+P", Description: "Command palette"},
		{Category: "Help", Key: "Cmd+Shift+M", Description: "Markdown reader mode"},
		{Category: "Help", Key: "Cmd+L", Description: "Open llms.txt browser"},
		{Category: "Browser", Key: "f", Description: "Follow link (hint mode)"},
		{Category: "Browser", Key: "a-z", Description: "Select link badge"},
		{Category: "Browser", Key: "Backspace / H", Description: "Navigate back"},
		{Category: "Browser", Key: "L", Description: "Navigate forward"},
		{Category: "Browser", Key: "Cmd+Enter", Description: "Send to pane"},
		{Category: "Help", Key: "? button", Description: "Status bar shortcut"},
		{Category: "App", Key: "Cmd+I", Description: "Toggle stats overlay"},
		{Category: "App", Key: "Cmd+=", Description: "Increase font size"},
		{Category: "App", Key: "Cmd+-", Description: "Decrease font size"},
		{Category: "App", Key: "Cmd+,", Description: "Reload config"},
		{Category: "App", Key: "Cmd+Q", Description: "Quit"},
	}
}

// FilterBindings returns bindings whose Key or Description contains query
// (case-insensitive).
func FilterBindings(query string) []KeyBinding {
	q := strings.ToLower(query)
	var out []KeyBinding
	for _, b := range AllBindings() {
		if strings.Contains(strings.ToLower(b.Key), q) ||
			strings.Contains(strings.ToLower(b.Description), q) {
			out = append(out, b)
		}
	}
	return out
}
