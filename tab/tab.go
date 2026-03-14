package tab

import (
	"fmt"
	"image"

	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/pane"
)

// Tab represents one workspace: a pane layout tree + the currently focused pane.
// Pattern: composite — Tab owns its LayoutNode lifecycle.
type Tab struct {
	Layout  *pane.LayoutNode
	Focused *pane.Pane
	Title   string // set via OSC 0/2; empty → DisplayTitle fallback

	// UserRenamed is true once the user has explicitly set the title via double-click rename.
	// When set, OSC 0/2 title sequences from the shell are ignored for this tab.
	UserRenamed bool

	// Renaming is true while the user is typing a new name inline.
	Renaming   bool
	RenameText string

	// Note is a persistent text annotation attached to this tab.
	// Users can edit it via Cmd+Shift+N or the command palette.
	Note string

	// Noting is true while the user is editing the tab note inline.
	Noting   bool
	NoteText string

	// PinnedSlot is a home-row letter ('a','s','d','f','g','h','j','k','l') if this
	// tab is pinned to a pin slot, or 0 if not pinned.
	PinnedSlot rune

	// HasActivity is true when a background tab has received PTY output since last viewed.
	HasActivity bool

	// HasBell is true when BEL was received while this tab was in the background.
	// Used to render the activity dot in bell color instead of cursor color.
	HasBell bool

	// lastSeenGen stores the last-seen RenderGen sum for activity detection.
	lastSeenGen uint64
}

// SnapshotGen records the current aggregate RenderGen for all panes in this tab.
func (t *Tab) SnapshotGen() {
	var sum uint64
	for _, leaf := range t.Layout.Leaves() {
		sum += leaf.Pane.Term.Buf.RenderGen()
	}
	t.lastSeenGen = sum
	t.HasActivity = false
	t.HasBell = false
}

// CheckActivity compares the current aggregate RenderGen against the snapshot.
// If it changed, sets HasActivity = true.
func (t *Tab) CheckActivity() {
	var sum uint64
	for _, leaf := range t.Layout.Leaves() {
		sum += leaf.Pane.Term.Buf.RenderGen()
	}
	if sum != t.lastSeenGen {
		t.HasActivity = true
	}
}

// New creates a Tab with a single pane covering rect.
// dir is the working directory for the shell; empty string inherits the parent process CWD.
func New(cfg *config.Config, rect image.Rectangle, cellW, cellH int, dir string) (*Tab, error) {
	p, err := pane.New(cfg, rect, cellW, cellH, dir)
	if err != nil {
		return nil, fmt.Errorf("tab new: %w", err)
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(rect, cellW, cellH, cfg.Window.Padding, cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	return &Tab{
		Layout:  layout,
		Focused: p,
	}, nil
}

// DisplayTitle returns the visible label. Defaults to "tab N" (1-indexed).
func (t *Tab) DisplayTitle(idx int) string {
	if t.Title != "" {
		return t.Title
	}
	return fmt.Sprintf("tab %d", idx+1)
}
