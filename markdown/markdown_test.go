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

	styles := spanStyles(lines[0])
	if !styles[StyleBold] {
		t.Error("missing bold span")
	}
	if !styles[StyleItalic] {
		t.Error("missing italic span")
	}
}

func TestParseUnderscoreEmphasis(t *testing.T) {
	lines := Parse("This is __bold__ and _italic_ text", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	styles := spanStyles(lines[0])
	if !styles[StyleBold] {
		t.Error("missing bold span from __text__")
	}
	if !styles[StyleItalic] {
		t.Error("missing italic span from _text_")
	}
}

func TestParseInlineCode(t *testing.T) {
	lines := Parse("Use `go fmt` to format", 80)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	found := false
	for _, s := range lines[0].Spans {
		if s.Style == StyleInlineCode && s.Text == "go fmt" {
			found = true
		}
	}
	if !found {
		t.Error("expected inline code span with 'go fmt'")
	}
}

func TestParseCodeBlock(t *testing.T) {
	input := "```go\nfunc main() {\n}\n```"
	lines := Parse(input, 80)

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	if lines[0].Spans[0].Style != StyleCodeBlock || lines[0].Spans[0].Text != "go" {
		t.Errorf("expected code block lang label, got %q style %d", lines[0].Spans[0].Text, lines[0].Spans[0].Style)
	}

	if lines[1].Spans[0].Style != StyleCodeBlock {
		t.Errorf("expected code block style for content line")
	}
}

func TestParseUnorderedList(t *testing.T) {
	input := "- first\n- second\n- third"
	lines := Parse(input, 80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	for i := 0; i < 3; i++ {
		if len(lines[i].Spans) < 2 {
			t.Fatalf("line %d: expected at least 2 spans, got %d", i, len(lines[i].Spans))
		}
		if lines[i].Spans[0].Style != StyleListItem {
			t.Errorf("line %d: first span should be ListItem, got %d", i, lines[i].Spans[0].Style)
		}
	}
}

func TestParseOrderedList(t *testing.T) {
	input := "1. first\n2. second"
	lines := Parse(input, 80)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	if lines[0].Spans[0].Style != StyleListItem {
		t.Errorf("expected ListItem style, got %d", lines[0].Spans[0].Style)
	}
}

func TestParseBlockquote(t *testing.T) {
	lines := Parse("> quoted text", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
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
			if len(lines) == 0 {
				t.Fatal("expected at least 1 line")
			}
			if lines[0].Spans[0].Style != StyleHRule {
				t.Errorf("expected HRule style, got %d", lines[0].Spans[0].Style)
			}
		})
	}
}

func TestParseLink(t *testing.T) {
	lines := Parse("Click [here](https://example.com) for info", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}

	found := false
	for _, s := range lines[0].Spans {
		if s.Style == StyleLink && s.Text == "here" && s.Extra == "https://example.com" {
			found = true
		}
	}
	if !found {
		t.Error("no link span found with text 'here' and correct URL")
	}
}

func TestParseWordWrap(t *testing.T) {
	input := strings.Repeat("word ", 20)
	lines := Parse(input, 40)
	if len(lines) < 2 {
		t.Errorf("expected wrapping, got %d lines", len(lines))
	}
}

func TestParseEmptyInput(t *testing.T) {
	lines := Parse("", 80)
	// Goldmark produces no output for empty input.
	if len(lines) != 0 {
		t.Fatalf("expected 0 lines for empty input, got %d", len(lines))
	}
}

func TestParseMixedInline(t *testing.T) {
	input := "**bold** and `code` and *italic*"
	lines := Parse(input, 80)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}

	styles := spanStyles(lines[0])
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
	lines := Parse("# **Bold Heading**", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}

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

func TestParseTable(t *testing.T) {
	input := "| Name | Value |\n| --- | --- |\n| a | b |"
	lines := Parse(input, 80)

	headerFound := false
	sepFound := false
	cellFound := false
	for _, line := range lines {
		for _, s := range line.Spans {
			if s.Style == StyleTableHeader {
				headerFound = true
			}
			if s.Style == StyleTableSeparator {
				sepFound = true
			}
			if s.Style == StyleTableCell {
				cellFound = true
			}
		}
	}
	if !headerFound {
		t.Error("missing table header")
	}
	if !sepFound {
		t.Error("missing table separator")
	}
	if !cellFound {
		t.Error("missing table cell")
	}
}

func TestParseTableColumnAlignment(t *testing.T) {
	input := "| Name | Value |\n| --- | --- |\n| a | long cell |"
	lines := Parse(input, 80)

	// Find header and data rows (skip separator).
	var headerLine, dataLine *StyledLine
	for i := range lines {
		for _, s := range lines[i].Spans {
			if s.Style == StyleTableHeader && headerLine == nil {
				headerLine = &lines[i]
			}
			if s.Style == StyleTableCell && dataLine == nil {
				dataLine = &lines[i]
			}
		}
	}
	if headerLine == nil || dataLine == nil {
		t.Fatal("missing header or data row")
	}

	// Both rows should produce the same total text length (columns are padded).
	headerText := lineText(*headerLine)
	dataText := lineText(*dataLine)
	if len(headerText) != len(dataText) {
		t.Errorf("column misalignment: header %q (%d) vs data %q (%d)",
			headerText, len(headerText), dataText, len(dataText))
	}
}

func lineText(line StyledLine) string {
	var b strings.Builder
	for _, s := range line.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func TestParseStrikethrough(t *testing.T) {
	lines := Parse("This is ~~deleted~~ text", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}
	found := false
	for _, s := range lines[0].Spans {
		if s.Style == StyleStrikethrough {
			found = true
		}
	}
	if !found {
		t.Error("missing strikethrough span")
	}
}

func TestParseImage(t *testing.T) {
	lines := Parse("![alt text](https://example.com/img.png)", 80)
	if len(lines) == 0 {
		t.Fatal("expected at least 1 line")
	}
	found := false
	for _, s := range lines[0].Spans {
		if s.Style == StyleImage && strings.Contains(s.Text, "alt text") {
			found = true
		}
	}
	if !found {
		t.Error("missing image span")
	}
}

func TestParseTaskList(t *testing.T) {
	input := "- [ ] unchecked\n- [x] checked"
	lines := Parse(input, 80)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}

	uncheckedFound := false
	checkedFound := false
	for _, line := range lines {
		for _, s := range line.Spans {
			if s.Style == StyleCheckboxUnchecked {
				uncheckedFound = true
			}
			if s.Style == StyleCheckboxChecked {
				checkedFound = true
			}
		}
	}
	if !uncheckedFound {
		t.Error("missing unchecked checkbox")
	}
	if !checkedFound {
		t.Error("missing checked checkbox")
	}
}

func TestParseOrderedList_Numbering(t *testing.T) {
	input := "1. first\n2. second\n3. third"
	lines := Parse(input, 80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	expected := []string{"1. ", "2. ", "3. "}
	for i, want := range expected {
		if len(lines[i].Spans) == 0 {
			t.Fatalf("line %d: no spans", i)
		}
		got := lines[i].Spans[0].Text
		if got != want {
			t.Errorf("line %d: marker = %q, want %q", i, got, want)
		}
	}
}

func TestParseOrderedList_CustomStart(t *testing.T) {
	input := "3. third\n4. fourth\n5. fifth"
	lines := Parse(input, 80)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines, got %d", len(lines))
	}

	expected := []string{"3. ", "4. ", "5. "}
	for i, want := range expected {
		if len(lines[i].Spans) == 0 {
			t.Fatalf("line %d: no spans", i)
		}
		got := lines[i].Spans[0].Text
		if got != want {
			t.Errorf("line %d: marker = %q, want %q", i, got, want)
		}
	}
}

// spanStyles collects all unique styles from a line's spans.
func spanStyles(line StyledLine) map[SpanStyle]bool {
	m := make(map[SpanStyle]bool)
	for _, s := range line.Spans {
		m[s.Style] = true
	}
	return m
}
