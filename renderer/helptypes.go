package renderer

// OverlayMenuItem mirrors help.MenuItem for rendering.
// The renderer uses Label, Shortcut, Children, Separator for drawing.
// Action is retained so Game can invoke it after the renderer reports a click.
type OverlayMenuItem struct {
	Label     string
	Shortcut  string
	Action    func()
	Children  []OverlayMenuItem
	Separator bool
}

// OverlayKeyBinding mirrors help.KeyBinding for rendering.
type OverlayKeyBinding struct {
	Category    string
	Key         string
	Description string
}
