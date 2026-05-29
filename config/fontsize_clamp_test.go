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
