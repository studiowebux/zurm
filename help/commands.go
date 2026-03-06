package help

// Command is a named app action with an optional keyboard shortcut.
// Actions are not stored here to avoid circular imports — main.go holds
// a parallel []func() slice keyed by index.
type Command struct {
	Name     string
	Shortcut string
}

// AllCommands returns the canonical list of app commands shown in the palette.
// Order determines display order when no query is active.
func AllCommands() []Command {
	return []Command{
		// Tabs
		{Name: "New Tab", Shortcut: "Cmd+T"},
		{Name: "Close Tab", Shortcut: "Cmd+W"},
		{Name: "Next Tab", Shortcut: "Cmd+Shift+]"},
		{Name: "Previous Tab", Shortcut: "Cmd+Shift+["},
		{Name: "Tab 1", Shortcut: "Cmd+1"},
		{Name: "Tab 2", Shortcut: "Cmd+2"},
		{Name: "Tab 3", Shortcut: "Cmd+3"},
		// Panes
		{Name: "Split Horizontal", Shortcut: "Cmd+D"},
		{Name: "Split Vertical", Shortcut: "Cmd+Shift+D"},
		{Name: "Focus Left", Shortcut: "Cmd+←"},
		{Name: "Focus Right", Shortcut: "Cmd+→"},
		{Name: "Focus Up", Shortcut: "Cmd+↑"},
		{Name: "Focus Down", Shortcut: "Cmd+↓"},
		{Name: "Zoom Pane", Shortcut: "Cmd+Z"},
		{Name: "Resize Left", Shortcut: "Cmd+Opt+←"},
		{Name: "Resize Right", Shortcut: "Cmd+Opt+→"},
		{Name: "Resize Up", Shortcut: "Cmd+Opt+↑"},
		{Name: "Resize Down", Shortcut: "Cmd+Opt+↓"},
		{Name: "Rename Pane", Shortcut: ""},
		// Scroll
		{Name: "Scroll Up", Shortcut: "Shift+PgUp"},
		{Name: "Scroll Down", Shortcut: "Shift+PgDn"},
		{Name: "Clear Scrollback", Shortcut: "Cmd+K"},
		// Copy / Paste
		{Name: "Copy Selection", Shortcut: "Cmd+C"},
		{Name: "Paste", Shortcut: "Cmd+V"},
		// Search
		{Name: "Search Buffer", Shortcut: "Cmd+F"},
		// File Explorer
		{Name: "File Explorer", Shortcut: "Cmd+E"},
		// Pins
		{Name: "Pin Mode", Shortcut: "Cmd+G"},
		// Tab Switcher
		{Name: "Tab Switcher", Shortcut: "Cmd+Shift+T"},
		// Tab Search
		{Name: "Tab Search", Shortcut: "Cmd+J"},
		// Blocks
		{Name: "Toggle Command Blocks", Shortcut: "Cmd+B"},
		{Name: "Install Shell Hooks", Shortcut: ""},
		// Session
		{Name: "Save Session", Shortcut: ""},
		// Recording
		{Name: "Take Screenshot", Shortcut: "Cmd+Shift+S"},
		{Name: "Toggle Recording", Shortcut: "Cmd+Shift+."},
		// Help
		{Name: "Show Keybindings", Shortcut: "Cmd+/"},
		{Name: "Command Palette", Shortcut: "Cmd+P"},
		// Config
		{Name: "Reload Config", Shortcut: "Cmd+,"},
		// App
		{Name: "Quit", Shortcut: "Cmd+Q"},
	}
}
