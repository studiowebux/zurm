package main

import (
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/renderer"
)

// ExplorerController owns file explorer state (Cmd+E) and navigation logic.
// Game holds a *ExplorerController and delegates explorer operations to it.
type ExplorerController struct {
	State renderer.FileExplorerState

	// Key repeat for arrow navigation.
	repeat KeyRepeatHandler

	// Text input repeat for rename/search/new fields.
	inputRepeat TextInputRepeat
}

// NewExplorerController creates a ready-to-use explorer controller.
func NewExplorerController() *ExplorerController {
	return &ExplorerController{}
}

// Close deactivates the explorer and clears state.
func (ec *ExplorerController) Close() {
	ec.State = renderer.FileExplorerState{}
}

// Move moves the file explorer cursor by delta (-1 = up, +1 = down).
func (ec *ExplorerController) Move(delta int) {
	st := &ec.State

	if len(st.SearchResults) > 0 {
		next := st.Cursor + delta
		if next >= 0 && next < len(st.SearchResults) {
			st.Cursor = next
		}
	} else if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		currentFilterIdx := -1
		for i, idx := range st.FilteredIndices {
			if idx == st.Cursor {
				currentFilterIdx = i
				break
			}
		}
		next := currentFilterIdx + delta
		if currentFilterIdx >= 0 && next >= 0 && next < len(st.FilteredIndices) {
			st.Cursor = st.FilteredIndices[next]
		} else if currentFilterIdx == -1 {
			if delta > 0 {
				st.Cursor = st.FilteredIndices[0]
			} else {
				st.Cursor = st.FilteredIndices[len(st.FilteredIndices)-1]
			}
		}
	} else {
		next := st.Cursor + delta
		if next >= 0 && next < len(st.Entries) {
			st.Cursor = next
		}
	}
	ec.EnsureVisible()
}

// EnsureVisible adjusts ScrollOffset so the cursor row is in view.
func (ec *ExplorerController) EnsureVisible() {
	st := &ec.State
	if st.RowH <= 0 {
		return
	}

	visualIdx := st.Cursor
	if len(st.SearchResults) > 0 {
		// cursor is already the visual index for search results
	} else if st.SearchQuery != "" && len(st.FilteredIndices) > 0 {
		for i, idx := range st.FilteredIndices {
			if idx == st.Cursor {
				visualIdx = i
				break
			}
		}
	}

	rowTop := visualIdx * st.RowH
	rowBot := rowTop + st.RowH
	if rowTop < st.ScrollOffset {
		st.ScrollOffset = rowTop
	}
	if rowBot > st.ScrollOffset+st.MaxScroll+st.RowH {
		st.ScrollOffset = rowBot - st.MaxScroll - st.RowH
		if st.ScrollOffset < 0 {
			st.ScrollOffset = 0
		}
	}
}

// ReloadTree rebuilds the entry list from the current root.
// Preserves scroll position, cursor (by path), and expanded directory state.
func (ec *ExplorerController) ReloadTree() {
	st := &ec.State

	var cursorPath string
	if st.Cursor >= 0 && st.Cursor < len(st.Entries) {
		cursorPath = st.Entries[st.Cursor].Path
	}
	expandedPaths := make(map[string]bool)
	for _, e := range st.Entries {
		if e.Expanded {
			expandedPaths[e.Path] = true
		}
	}

	entries, err := fileexplorer.BuildTree(st.Root)
	if err != nil {
		return
	}

	for i := 0; i < len(entries); i++ {
		if entries[i].IsDir && expandedPaths[entries[i].Path] {
			if expanded, err := fileexplorer.ExpandAt(entries, i); err == nil {
				entries = expanded
			}
		}
	}

	cursor := 0
	if cursorPath != "" {
		if idx := fileexplorer.FindIdx(entries, cursorPath); idx >= 0 {
			cursor = idx
		}
	}

	st.Entries = entries
	st.Cursor = cursor
}
