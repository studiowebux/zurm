package help

// MenuItem represents one item in a context menu.
// Separator items have Separator: true; no other fields are used.
// Parent items have Children and no Action.
// Leaf items have an Action.
type MenuItem struct {
	Label     string
	Shortcut  string
	Action    func()
	Children  []MenuItem
	Separator bool
}
