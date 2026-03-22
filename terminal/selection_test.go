package terminal

import "testing"

func TestNormalize_AlreadyOrdered(t *testing.T) {
	s := Selection{Active: true, StartRow: 1, StartCol: 5, EndRow: 3, EndCol: 10}
	n := s.Normalize()
	if n.StartRow != 1 || n.StartCol != 5 || n.EndRow != 3 || n.EndCol != 10 {
		t.Errorf("Normalize changed already-ordered selection: %+v", n)
	}
}

func TestNormalize_Reversed(t *testing.T) {
	s := Selection{Active: true, StartRow: 5, StartCol: 20, EndRow: 2, EndCol: 3}
	n := s.Normalize()
	if n.StartRow != 2 || n.StartCol != 3 || n.EndRow != 5 || n.EndCol != 20 {
		t.Errorf("Normalize failed to swap: %+v", n)
	}
	if !n.Active {
		t.Error("Normalize should preserve Active flag")
	}
}

func TestNormalize_SameRowReversed(t *testing.T) {
	s := Selection{Active: true, StartRow: 3, StartCol: 15, EndRow: 3, EndCol: 5}
	n := s.Normalize()
	if n.StartCol != 5 || n.EndCol != 15 {
		t.Errorf("Normalize same-row swap: StartCol=%d EndCol=%d, want 5 and 15", n.StartCol, n.EndCol)
	}
}

func TestNormalize_Inactive(t *testing.T) {
	s := Selection{Active: false, StartRow: 5, StartCol: 20, EndRow: 2, EndCol: 3}
	n := s.Normalize()
	if n.Active {
		t.Error("Normalize should preserve Active=false")
	}
}

func TestContains_InactiveAlwaysFalse(t *testing.T) {
	s := Selection{Active: false, StartRow: 0, StartCol: 0, EndRow: 100, EndCol: 100}
	if s.Contains(50, 50) {
		t.Error("Contains should return false when selection is inactive")
	}
}

func TestContains_SingleRow(t *testing.T) {
	s := Selection{Active: true, StartRow: 5, StartCol: 10, EndRow: 5, EndCol: 20}

	if !s.Contains(5, 10) {
		t.Error("should contain start boundary")
	}
	if !s.Contains(5, 15) {
		t.Error("should contain middle")
	}
	if !s.Contains(5, 20) {
		t.Error("should contain end boundary")
	}
	if s.Contains(5, 9) {
		t.Error("should not contain before start")
	}
	if s.Contains(5, 21) {
		t.Error("should not contain after end")
	}
	if s.Contains(4, 15) {
		t.Error("should not contain above")
	}
	if s.Contains(6, 15) {
		t.Error("should not contain below")
	}
}

func TestContains_MultiRow(t *testing.T) {
	s := Selection{Active: true, StartRow: 2, StartCol: 30, EndRow: 4, EndCol: 10}

	// Middle row — any column should be in selection.
	if !s.Contains(3, 0) {
		t.Error("middle row col 0 should be in selection")
	}
	if !s.Contains(3, 80) {
		t.Error("middle row col 80 should be in selection")
	}
	// Start row before StartCol.
	if s.Contains(2, 29) {
		t.Error("start row before StartCol should not be in selection")
	}
	// End row after EndCol.
	if s.Contains(4, 11) {
		t.Error("end row after EndCol should not be in selection")
	}
}

func TestContains_ReversedSelection(t *testing.T) {
	// Selection where Start is after End (user dragged upward).
	s := Selection{Active: true, StartRow: 10, StartCol: 5, EndRow: 8, EndCol: 20}

	// Contains should normalize internally.
	if !s.Contains(9, 0) {
		t.Error("reversed selection should still contain middle row")
	}
	if !s.Contains(8, 20) {
		t.Error("reversed selection should contain normalized start")
	}
	if !s.Contains(10, 5) {
		t.Error("reversed selection should contain normalized end")
	}
}
