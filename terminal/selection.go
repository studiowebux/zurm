package terminal

// Selection represents the current mouse text selection in display coordinates
// (0-indexed row/col as seen on screen, accounting for ViewOffset).
//
// Pattern: value type — all methods operate on copies, no pointer receiver.
type Selection struct {
	Active              bool
	StartRow, StartCol  int
	EndRow, EndCol      int
}

// Normalize returns a Selection where Start is always before End in reading
// order (top-to-bottom, left-to-right). Safe to call on an inactive selection.
func (s Selection) Normalize() Selection {
	if s.StartRow > s.EndRow || (s.StartRow == s.EndRow && s.StartCol > s.EndCol) {
		return Selection{
			Active:   s.Active,
			StartRow: s.EndRow, StartCol: s.EndCol,
			EndRow:   s.StartRow, EndCol: s.StartCol,
		}
	}
	return s
}

// Contains reports whether (row, col) falls within the selection.
// Always returns false when Active is false.
func (s Selection) Contains(row, col int) bool {
	if !s.Active {
		return false
	}
	n := s.Normalize()
	if row < n.StartRow || row > n.EndRow {
		return false
	}
	if row == n.StartRow && col < n.StartCol {
		return false
	}
	if row == n.EndRow && col > n.EndCol {
		return false
	}
	return true
}
