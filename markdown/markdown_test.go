package markdown

import (
	"strings"
	"testing"
)

func TestParseHeadings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		style SpanStyle
	}{
		{"h1", "# Title", StyleHeading1},
		{"h2", "## Subtitle", StyleHeading2},
		{"h3", "### Section", StyleHeading3},
		{"h4", "#### Deep", StyleHeading3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := Parse(tt.input, 80)
			if len(lines) == 0 {
				t.Fatal("expected at least one line")
			}
			if len(lines[0].Spans) == 0 {
				t.Fatal("expected at least one span")
			}
			if lines[0].Spans[0].Style != tt.style {
				t.Errorf("got style %d, want %d", lines[0].Spans[0].Style, tt.style)
			}
		})
	}
}

func TestParseBoldItalic(t *testing.T) {
	lines := Parse("This is **bold** and *italic* text", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	spans := lines[0].Spans
	if len(spans) != 5 {
		t.Fatalf("expected 5 spans, got %d", len(spans))
	}

	if spans[0].Style != StyleNormal || spans[0].Text != "This is " {
		t.Errorf("span 0: got %q style %d", spans[0].Text, spans[0].Style)
	}
	if spans[1].Style != StyleBold || spans[1].Text != "bold" {
		t.Errorf("span 1: got %q style %d", spans[1].Text, spans[1].Style)
	}
	if spans[2].Style != StyleNormal || spans[2].Text != " and " {
		t.Errorf("span 2: got %q style %d", spans[2].Text, spans[2].Style)
	}
	if spans[3].Style != StyleItalic || spans[3].Text != "italic" {
		t.Errorf("span 3: got %q style %d", spans[3].Text, spans[3].Style)
	}
	if spans[4].Style != StyleNormal || spans[4].Text != " text" {
		t.Errorf("span 4: got %q style %d", spans[4].Text, spans[4].Style)
	}
}

func TestParseInlineCode(t *testing.T) {
	lines := Parse("Use `go fmt` to format", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	spans := lines[0].Spans
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(spans))
	}
	if spans[1].Style != StyleInlineCode || spans[1].Text != "go fmt" {
		t.Errorf("span 1: got %q style %d", spans[1].Text, spans[1].Style)
	}
}

func TestParseCodeBlock(t *testing.T) {
	input := "```go\nfunc main() {\n}\n```"
	lines := Parse(input, 80)

	// Should have: lang label + 2 code lines (fence lines are consumed).
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	// First line is the language label.
	if lines[0].Spans[0].Style != StyleCodeBlock || lines[0].Spans[0].Text != "go" {
		t.Errorf("expected code block lang label, got %q style %d", lines[0].Spans[0].Text, lines[0].Spans[0].Style)
	}

	// Content lines are code block styled.
	if lines[1].Spans[0].Style != StyleCodeBlock {
		t.Errorf("expected code block style for content line")
	}
}

func TestParseUnorderedList(t *testing.T) {
	input := "- first\n- second\n- third"
	lines := Parse(input, 80)
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	for i, line := range lines {
		if len(line.Spans) < 2 {
			t.Fatalf("line %d: expected at least 2 spans, got %d", i, len(line.Spans))
		}
		if line.Spans[0].Style != StyleListItem {
			t.Errorf("line %d: first span should be ListItem, got %d", i, line.Spans[0].Style)
		}
	}
}

func TestParseOrderedList(t *testing.T) {
	input := "1. first\n2. second"
	lines := Parse(input, 80)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if lines[0].Spans[0].Style != StyleListItem {
		t.Errorf("expected ListItem style, got %d", lines[0].Spans[0].Style)
	}
	if lines[0].Spans[0].Text != "1. " {
		t.Errorf("expected prefix '1. ', got %q", lines[0].Spans[0].Text)
	}
}

func TestParseBlockquote(t *testing.T) {
	lines := Parse("> quoted text", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0].Spans[0].Style != StyleBlockquote {
		t.Errorf("expected blockquote style, got %d", lines[0].Spans[0].Style)
	}
	if lines[0].Indent != 2 {
		t.Errorf("expected indent 2, got %d", lines[0].Indent)
	}
}

func TestParseHRule(t *testing.T) {
	tests := []string{"---", "***", "___", "-----"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			lines := Parse(input, 80)
			if len(lines) != 1 {
				t.Fatalf("expected 1 line, got %d", len(lines))
			}
			if lines[0].Spans[0].Style != StyleHRule {
				t.Errorf("expected HRule style, got %d", lines[0].Spans[0].Style)
			}
		})
	}
}

func TestParseLink(t *testing.T) {
	lines := Parse("Click [here](https://example.com) for info", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	spans := lines[0].Spans
	found := false
	for _, s := range spans {
		if s.Style == StyleLink {
			found = true
			if s.Text != "here" {
				t.Errorf("expected link text 'here', got %q", s.Text)
			}
			if s.Extra != "https://example.com" {
				t.Errorf("expected URL 'https://example.com', got %q", s.Extra)
			}
		}
	}
	if !found {
		t.Error("no link span found")
	}
}

func TestParseWordWrap(t *testing.T) {
	input := strings.Repeat("word ", 20) // 100 chars
	lines := Parse(input, 40)
	if len(lines) < 2 {
		t.Errorf("expected wrapping, got %d lines", len(lines))
	}
}

func TestParseEmptyInput(t *testing.T) {
	lines := Parse("", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 empty line, got %d", len(lines))
	}
}

func TestParseMixedInline(t *testing.T) {
	input := "**bold** and `code` and *italic*"
	lines := Parse(input, 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	styles := make(map[SpanStyle]bool)
	for _, s := range lines[0].Spans {
		styles[s.Style] = true
	}

	if !styles[StyleBold] {
		t.Error("missing bold span")
	}
	if !styles[StyleInlineCode] {
		t.Error("missing inline code span")
	}
	if !styles[StyleItalic] {
		t.Error("missing italic span")
	}
}

func TestParseCodeBlockPreservesContent(t *testing.T) {
	input := "```\n  indented\n    more\n```"
	lines := Parse(input, 80)

	// Should preserve indentation inside code blocks.
	found := false
	for _, line := range lines {
		for _, s := range line.Spans {
			if s.Style == StyleCodeBlock && strings.Contains(s.Text, "  indented") {
				found = true
			}
		}
	}
	if !found {
		t.Error("code block should preserve indentation")
	}
}

func TestParseNestedFormatting(t *testing.T) {
	// Bold inside a heading — heading style takes precedence for default.
	lines := Parse("# **Bold Heading**", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

	// The bold markers should be parsed, producing a bold span.
	found := false
	for _, s := range lines[0].Spans {
		if s.Style == StyleBold {
			found = true
		}
	}
	if !found {
		t.Error("expected bold span inside heading")
	}
}
