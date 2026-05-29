package config

import "testing"

func TestClampFontSize(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{"zero collapses to min", 0, MinFontSize},
		{"negative collapses to min", -10, MinFontSize},
		{"below min", MinFontSize - 0.1, MinFontSize},
		{"at min stays", MinFontSize, MinFontSize},
		{"in range stays", 15, 15},
		{"at max stays", MaxFontSize, MaxFontSize},
		{"above max collapses to max", MaxFontSize + 100, MaxFontSize},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{Font: FontConfig{Size: c.in}}
			clampFontSize(&cfg)
			if cfg.Font.Size != c.want {
				t.Errorf("clampFontSize(%v) = %v, want %v", c.in, cfg.Font.Size, c.want)
			}
		})
	}
}

func TestClampWindow(t *testing.T) {
	cases := []struct {
		name                            string
		cols, rows, pad                 int
		wantCols, wantRows, wantPadding int
	}{
		{"negative cols/rows", -1, -1, 4, 1, 1, 4},
		{"zero cols/rows", 0, 0, 4, 1, 1, 4},
		{"negative padding", 80, 24, -5, 80, 24, 0},
		{"valid values untouched", 120, 35, 4, 120, 35, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{Window: WindowConfig{Columns: c.cols, Rows: c.rows, Padding: c.pad}}
			clampWindow(&cfg)
			if cfg.Window.Columns != c.wantCols || cfg.Window.Rows != c.wantRows || cfg.Window.Padding != c.wantPadding {
				t.Errorf("clampWindow(cols=%d,rows=%d,pad=%d) = (%d,%d,%d), want (%d,%d,%d)",
					c.cols, c.rows, c.pad,
					cfg.Window.Columns, cfg.Window.Rows, cfg.Window.Padding,
					c.wantCols, c.wantRows, c.wantPadding)
			}
		})
	}
}
