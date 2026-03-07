package markdown

import (
	"strings"
	"unicode/utf8"
)

// SpanStyle identifies visual styling for a text span.
type SpanStyle int

const (
	StyleNormal     SpanStyle = iota
	StyleHeading1             // #
	StyleHeading2             // ##
	StyleHeading3             // ### and deeper
	StyleBold                 // **text**
	StyleItalic               // *text*
	StyleInlineCode           // `text`
	StyleCodeBlock            // fenced ``` lines
	StyleLink                 // [text](url)
	StyleBlockquote           // > text
	StyleListItem             // - item / 1. item
	StyleHRule                // ---
)

// Span is a contiguous run of text with one style.
type Span struct {
	Text  string
	Style SpanStyle
	Extra string // URL for StyleLink
}

// StyledLine is a visual line (post word-wrap) with its spans and indent.
type StyledLine struct {
	Spans  []Span
	Indent int // leading indent in cells (for lists, blockquotes)
}

// Parse converts raw markdown text into styled lines, word-wrapping at maxCols.
func Parse(text string, maxCols int) []StyledLine {
	if maxCols < 10 {
		maxCols = 10
	}
	rawLines := strings.Split(text, "\n")

	var result []StyledLine
	inCodeBlock := false
	codeFenceLang := ""

	for _, raw := range rawLines {
		// Code fence toggle.
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				inCodeBlock = false
				codeFenceLang = ""
				continue
			}
			inCodeBlock = true
			codeFenceLang = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			if codeFenceLang != "" {
				result = append(result, StyledLine{
					Spans: []Span{{Text: codeFenceLang, Style: StyleCodeBlock}},
				})
			}
			continue
		}

		if inCodeBlock {
			// Preserve code lines as-is, word-wrap at hard boundary.
			wrapped := wrapPlain(raw, maxCols)
			for _, w := range wrapped {
				result = append(result, StyledLine{
					Spans: []Span{{Text: w, Style: StyleCodeBlock}},
				})
			}
			continue
		}

		// Horizontal rule: ---, ***, ___ (at least 3 chars, nothing else).
		if isHRule(trimmed) {
			result = append(result, StyledLine{
				Spans: []Span{{Text: "", Style: StyleHRule}},
			})
			continue
		}

		// Headings.
		if level, body := parseHeading(trimmed); level > 0 {
			style := StyleHeading1
			if level == 2 {
				style = StyleHeading2
			} else if level >= 3 {
				style = StyleHeading3
			}
			spans := parseInline(body, style)
			wrapped := wrapSpans(spans, maxCols, 0)
			result = append(result, wrapped...)
			continue
		}

		// Blockquote: > text
		if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
			body := strings.TrimPrefix(trimmed, "> ")
			if body == ">" {
				body = ""
			}
			spans := []Span{{Text: body, Style: StyleBlockquote}}
			wrapped := wrapSpans(spans, maxCols, 2)
			result = append(result, wrapped...)
			continue
		}

		// Unordered list: - item, * item, + item
		if indent, body, ok := parseUnorderedList(trimmed); ok {
			spans := parseInline(body, StyleNormal)
			indentCells := indent + 2
			wrapped := wrapSpans(spans, maxCols, indentCells)
			// Prepend bullet to first line.
			if len(wrapped) > 0 && len(wrapped[0].Spans) > 0 {
				wrapped[0].Spans = append([]Span{{Text: "- ", Style: StyleListItem}}, wrapped[0].Spans...)
			}
			result = append(result, wrapped...)
			continue
		}

		// Ordered list: 1. item, 2. item, etc.
		if indent, prefix, body, ok := parseOrderedList(trimmed); ok {
			spans := parseInline(body, StyleNormal)
			indentCells := indent + len(prefix)
			wrapped := wrapSpans(spans, maxCols, indentCells)
			if len(wrapped) > 0 && len(wrapped[0].Spans) > 0 {
				wrapped[0].Spans = append([]Span{{Text: prefix, Style: StyleListItem}}, wrapped[0].Spans...)
			}
			result = append(result, wrapped...)
			continue
		}

		// Empty line.
		if trimmed == "" {
			result = append(result, StyledLine{})
			continue
		}

		// Regular paragraph — parse inline styles and wrap.
		spans := parseInline(trimmed, StyleNormal)
		wrapped := wrapSpans(spans, maxCols, 0)
		result = append(result, wrapped...)
	}

	return result
}

// parseHeading returns (level, body) if the line is a heading, or (0, "") otherwise.
func parseHeading(line string) (int, string) {
	level := 0
	for _, ch := range line {
		if ch == '#' {
			level++
		} else {
			break
		}
	}
	if level == 0 || level > 6 {
		return 0, ""
	}
	body := strings.TrimSpace(line[level:])
	return level, body
}

// isHRule checks if line is a horizontal rule (---, ***, ___).
func isHRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	ch := line[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for i := range line {
		if line[i] != ch {
			return false
		}
	}
	return true
}

// parseUnorderedList checks for "- item", "* item", "+ item" with optional indent.
func parseUnorderedList(line string) (indent int, body string, ok bool) {
	indent = 0
	for _, ch := range line {
		if ch == ' ' || ch == '\t' {
			indent++
		} else {
			break
		}
	}
	rest := line[indent:]
	if len(rest) >= 2 && (rest[0] == '-' || rest[0] == '*' || rest[0] == '+') && rest[1] == ' ' {
		return indent, rest[2:], true
	}
	return 0, "", false
}

// parseOrderedList checks for "1. item" style with optional indent.
func parseOrderedList(line string) (indent int, prefix, body string, ok bool) {
	indent = 0
	for _, ch := range line {
		if ch == ' ' || ch == '\t' {
			indent++
		} else {
			break
		}
	}
	rest := line[indent:]
	dotIdx := strings.Index(rest, ". ")
	if dotIdx < 1 || dotIdx > 4 {
		return 0, "", "", false
	}
	num := rest[:dotIdx]
	for _, ch := range num {
		if ch < '0' || ch > '9' {
			return 0, "", "", false
		}
	}
	return indent, rest[:dotIdx+2], rest[dotIdx+2:], true
}

// parseInline parses inline markdown (bold, italic, code, links) within text.
// defaultStyle is applied to unstyled text.
func parseInline(text string, defaultStyle SpanStyle) []Span {
	var spans []Span
	i := 0
	runes := []rune(text)
	n := len(runes)

	flush := func(start, end int) {
		if end > start {
			spans = append(spans, Span{Text: string(runes[start:end]), Style: defaultStyle})
		}
	}

	segStart := 0
	for i < n {
		// Inline code: `text`
		if runes[i] == '`' {
			end := indexRune(runes, '`', i+1)
			if end > i+1 {
				flush(segStart, i)
				spans = append(spans, Span{Text: string(runes[i+1 : end]), Style: StyleInlineCode})
				i = end + 1
				segStart = i
				continue
			}
		}

		// Bold: **text**
		if i+1 < n && runes[i] == '*' && runes[i+1] == '*' {
			end := indexRuneDouble(runes, '*', i+2)
			if end > i+2 {
				flush(segStart, i)
				spans = append(spans, Span{Text: string(runes[i+2 : end]), Style: StyleBold})
				i = end + 2
				segStart = i
				continue
			}
		}

		// Italic: *text* (single asterisk, not preceded by another *)
		if runes[i] == '*' && (i+1 < n && runes[i+1] != '*') {
			end := indexRune(runes, '*', i+1)
			if end > i+1 {
				flush(segStart, i)
				spans = append(spans, Span{Text: string(runes[i+1 : end]), Style: StyleItalic})
				i = end + 1
				segStart = i
				continue
			}
		}

		// Link: [text](url)
		if runes[i] == '[' {
			closeBracket := indexRune(runes, ']', i+1)
			if closeBracket > i+1 && closeBracket+1 < n && runes[closeBracket+1] == '(' {
				closeParen := indexRune(runes, ')', closeBracket+2)
				if closeParen > closeBracket+2 {
					flush(segStart, i)
					linkText := string(runes[i+1 : closeBracket])
					linkURL := string(runes[closeBracket+2 : closeParen])
					spans = append(spans, Span{Text: linkText, Style: StyleLink, Extra: linkURL})
					i = closeParen + 1
					segStart = i
					continue
				}
			}
		}

		i++
	}
	flush(segStart, n)
	return spans
}

// indexRune finds the next occurrence of ch in runes starting at from.
func indexRune(runes []rune, ch rune, from int) int {
	for j := from; j < len(runes); j++ {
		if runes[j] == ch {
			return j
		}
	}
	return -1
}

// indexRuneDouble finds the next occurrence of chch (double char) in runes starting at from.
func indexRuneDouble(runes []rune, ch rune, from int) int {
	for j := from; j+1 < len(runes); j++ {
		if runes[j] == ch && runes[j+1] == ch {
			return j
		}
	}
	return -1
}

// wrapPlain splits a plain string into lines of at most maxCols runes.
func wrapPlain(s string, maxCols int) []string {
	runes := []rune(s)
	if len(runes) <= maxCols {
		return []string{s}
	}
	var lines []string
	for len(runes) > maxCols {
		lines = append(lines, string(runes[:maxCols]))
		runes = runes[maxCols:]
	}
	lines = append(lines, string(runes))
	return lines
}

// wrapSpans wraps styled spans into visual lines respecting maxCols.
// indent is the leading indent for continuation lines.
func wrapSpans(spans []Span, maxCols, indent int) []StyledLine {
	if len(spans) == 0 {
		return []StyledLine{{Indent: indent}}
	}

	// Flatten all text to measure total width.
	totalLen := 0
	for _, s := range spans {
		totalLen += utf8.RuneCountInString(s.Text)
	}

	usable := maxCols - indent
	if usable < 5 {
		usable = 5
	}

	// Fast path: fits on one line.
	if totalLen <= usable {
		return []StyledLine{{Spans: spans, Indent: indent}}
	}

	// Slow path: split spans across lines.
	var lines []StyledLine
	var curSpans []Span
	col := 0

	for _, span := range spans {
		runes := []rune(span.Text)
		ri := 0
		for ri < len(runes) {
			remaining := usable - col
			if remaining <= 0 {
				lines = append(lines, StyledLine{Spans: curSpans, Indent: indent})
				curSpans = nil
				col = 0
				remaining = usable
			}
			chunk := len(runes) - ri
			if chunk > remaining {
				chunk = remaining
			}
			curSpans = append(curSpans, Span{
				Text:  string(runes[ri : ri+chunk]),
				Style: span.Style,
				Extra: span.Extra,
			})
			col += chunk
			ri += chunk
		}
	}
	if len(curSpans) > 0 {
		lines = append(lines, StyledLine{Spans: curSpans, Indent: indent})
	}

	return lines
}
