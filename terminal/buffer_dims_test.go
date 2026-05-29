package terminal

import "testing"

// NewScreenBuffer must never produce a buffer with fewer than 1 row/col — a
// negative dim (e.g. from a bad [window] rows/columns config) would otherwise
// panic in the cell-slice allocations.
func TestNewScreenBuffer_FloorsDimensions(t *testing.T) {
	cases := []struct {
		name       string
		rows, cols int
	}{
		{"negative both", -5, -3},
		{"zero both", 0, 0},
		{"negative rows", -1, 80},
		{"zero cols", 24, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sb := NewScreenBuffer(c.rows, c.cols, 100, 0, testFG, testBG, testPalette)
			if sb.Rows < 1 || sb.Cols < 1 {
				t.Fatalf("NewScreenBuffer(%d,%d) = Rows %d, Cols %d; want both >= 1", c.rows, c.cols, sb.Rows, sb.Cols)
			}
			if len(sb.Cells) != sb.Rows {
				t.Errorf("Cells rows = %d, want %d", len(sb.Cells), sb.Rows)
			}
			if len(sb.dirty) != sb.Rows {
				t.Errorf("dirty len = %d, want %d", len(sb.dirty), sb.Rows)
			}
		})
	}
}
