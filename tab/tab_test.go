package tab

import "testing"

func TestDisplayTitle_Default(t *testing.T) {
	tab := &Tab{}
	got := tab.DisplayTitle(0)
	if got != "tab 1" {
		t.Errorf("DisplayTitle(0) = %q, want %q", got, "tab 1")
	}
}

func TestDisplayTitle_IndexOffset(t *testing.T) {
	tab := &Tab{}
	tests := []struct {
		idx  int
		want string
	}{
		{0, "tab 1"},
		{1, "tab 2"},
		{9, "tab 10"},
	}
	for _, tt := range tests {
		got := tab.DisplayTitle(tt.idx)
		if got != tt.want {
			t.Errorf("DisplayTitle(%d) = %q, want %q", tt.idx, got, tt.want)
		}
	}
}

func TestDisplayTitle_CustomTitle(t *testing.T) {
	tab := &Tab{Title: "my-project"}
	got := tab.DisplayTitle(0)
	if got != "my-project" {
		t.Errorf("DisplayTitle with Title = %q, want %q", got, "my-project")
	}
}

func TestDisplayTitle_CustomTitleIgnoresIndex(t *testing.T) {
	tab := &Tab{Title: "dev"}
	got := tab.DisplayTitle(42)
	if got != "dev" {
		t.Errorf("DisplayTitle should return Title regardless of idx, got %q", got)
	}
}
