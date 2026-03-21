package main

import (
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/studiowebux/zurm/fileexplorer"
	"github.com/studiowebux/zurm/renderer"
)

// ExplorerController owns file explorer state (Cmd+E) and navigation logic.
// Game holds a *ExplorerController and delegates explorer operations to it.
type ExplorerController struct {
	State renderer.FileExplorerState

	// Key repeat for arrow navigation.
	repeatKey    ebiten.Key
	repeatActive bool
	repeatStart  time.Time
	repeatLast   time.Time

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

// UpdateRepeat implements OS-style key repeat for explorer arrow keys.
// Returns true when the key should fire (initial press or repeat tick).
func (ec *ExplorerController) UpdateRepeat(key ebiten.Key, pressed, wasPressed bool, now time.Time) bool {
	if !pressed {
		if ec.repeatActive && ec.repeatKey == key {
			ec.repeatActive = false
		}
		return false
	}
	if !wasPressed {
		ec.repeatKey = key
		ec.repeatActive = true
		ec.repeatStart = now
		ec.repeatLast = now
		return true
	}
	if ec.repeatActive && ec.repeatKey == key &&
		now.Sub(ec.repeatStart) >= keyRepeatDelay &&
		now.Sub(ec.repeatLast) >= keyRepeatInterval {
		ec.repeatLast = now
		return true
	}
	return false
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
			// Not on a filtered item — jump to first (down) or last (up).
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

	// Calculate the visual row index based on filtering.
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
// It preserves: scroll position, cursor (by path), and the expanded state of
// every directory that was open before the reload.
func (ec *ExplorerController) ReloadTree() {
	st := &ec.State

	// Snapshot state before rebuild.
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

	// Replay expansions.
	for i := 0; i < len(entries); i++ {
		if entries[i].IsDir && expandedPaths[entries[i].Path] {
			if expanded, err := fileexplorer.ExpandAt(entries, i); err == nil {
				entries = expanded
			}
		}
	}

	// Restore cursor to the same path; fall back to 0 if gone.
	cursor := 0
	if cursorPath != "" {
		if idx := fileexplorer.FindIdx(entries, cursorPath); idx >= 0 {
			cursor = idx
		}
	}

	st.Entries = entries
	st.Cursor = cursor
}
