package terminal

import "testing"

func TestTrimTrailingPunct(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no trailing punct", "https://example.com/path", "https://example.com/path"},
		{"trailing dot", "https://example.com.", "https://example.com"},
		{"trailing comma", "https://example.com,", "https://example.com"},
		{"trailing semicolon", "https://example.com;", "https://example.com"},
		{"trailing colon", "https://example.com:", "https://example.com"},
		{"trailing exclamation", "https://example.com!", "https://example.com"},
		{"trailing question", "https://example.com?", "https://example.com"},
		{"trailing single quote", "https://example.com'", "https://example.com"},
		{"trailing double quote", `https://example.com"`, "https://example.com"},
		{"multiple trailing punct", "https://example.com.,;:", "https://example.com"},
		{"balanced parens preserved", "https://en.wikipedia.org/wiki/Go_(programming_language)", "https://en.wikipedia.org/wiki/Go_(programming_language)"},
		{"unbalanced trailing paren", "https://example.com)", "https://example.com"},
		{"unbalanced trailing bracket", "https://example.com]", "https://example.com"},
		{"balanced brackets preserved", "https://example.com/[test]", "https://example.com/[test]"},
		{"paren then dot", "https://en.wikipedia.org/wiki/Go_(lang).", "https://en.wikipedia.org/wiki/Go_(lang)"},
		{"empty string", "", ""},
		{"only punct", ".,;:", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimTrailingPunct(tt.input)
			if got != tt.want {
				t.Errorf("trimTrailingPunct(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestURLAt_SingleRow(t *testing.T) {
	matches := []URLMatch{
		{StartRow: 0, StartCol: 5, EndRow: 0, EndCol: 25, Text: "https://example.com"},
	}

	// Hit inside.
	if m := URLAt(matches, 0, 10); m == nil {
		t.Error("expected match at (0, 10)")
	}
	// Hit at start.
	if m := URLAt(matches, 0, 5); m == nil {
		t.Error("expected match at start col")
	}
	// Hit at end.
	if m := URLAt(matches, 0, 25); m == nil {
		t.Error("expected match at end col")
	}
	// Miss before.
	if m := URLAt(matches, 0, 4); m != nil {
		t.Error("expected no match before start")
	}
	// Miss after.
	if m := URLAt(matches, 0, 26); m != nil {
		t.Error("expected no match after end")
	}
	// Wrong row.
	if m := URLAt(matches, 1, 10); m != nil {
		t.Error("expected no match on different row")
	}
}

func TestURLAt_MultiRow(t *testing.T) {
	matches := []URLMatch{
		{StartRow: 2, StartCol: 60, EndRow: 4, EndCol: 10, Text: "https://long-url.example.com/very/long/path"},
	}

	// Start row, at StartCol.
	if m := URLAt(matches, 2, 60); m == nil {
		t.Error("expected match at start row/col")
	}
	// Start row, before StartCol.
	if m := URLAt(matches, 2, 59); m != nil {
		t.Error("expected no match before StartCol on start row")
	}
	// Middle row.
	if m := URLAt(matches, 3, 0); m == nil {
		t.Error("expected match on middle row")
	}
	// End row, at EndCol.
	if m := URLAt(matches, 4, 10); m == nil {
		t.Error("expected match at end row/col")
	}
	// End row, after EndCol.
	if m := URLAt(matches, 4, 11); m != nil {
		t.Error("expected no match after EndCol on end row")
	}
}

func TestURLAt_Empty(t *testing.T) {
	if m := URLAt(nil, 0, 0); m != nil {
		t.Error("expected nil for empty matches")
	}
}

func TestContainsCell(t *testing.T) {
	m := URLMatch{StartRow: 1, StartCol: 5, EndRow: 3, EndCol: 10}

	tests := []struct {
		name string
		row  int
		col  int
		want bool
	}{
		{"inside", 2, 5, true},
		{"start boundary", 1, 5, true},
		{"end boundary", 3, 10, true},
		{"before start col", 1, 4, false},
		{"after end col", 3, 11, false},
		{"above start row", 0, 5, false},
		{"below end row", 4, 5, false},
		{"start row after start col", 1, 80, true},
		{"end row before end col", 3, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.ContainsCell(tt.row, tt.col)
			if got != tt.want {
				t.Errorf("ContainsCell(%d, %d) = %v, want %v", tt.row, tt.col, got, tt.want)
			}
		})
	}
}
