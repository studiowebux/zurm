package markdown

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// SpanStyle identifies visual styling for a text span.
type SpanStyle int

const (
	StyleNormal            SpanStyle = iota
	StyleHeading1                    // #
	StyleHeading2                    // ##
	StyleHeading3                    // ### and deeper
	StyleBold                        // **text** / __text__
	StyleItalic                      // *text* / _text_
	StyleInlineCode                  // `text`
	StyleCodeBlock                   // fenced ``` lines
	StyleLink                        // [text](url)
	StyleBlockquote                  // > text
	StyleListItem                    // - item / 1. item
	StyleHRule                       // ---
	StyleStrikethrough               // ~~text~~
	StyleImage                       // ![alt](url)
	StyleCheckboxChecked             // - [x]
	StyleCheckboxUnchecked           // - [ ]
	StyleTableHeader                 // table header cells
	StyleTableSeparator              // table separator row
	StyleTableCell                   // table data cells
)

// Span is a contiguous run of text with one style.
type Span struct {
	Text  string
	Style SpanStyle
	Extra string // URL for StyleLink / StyleImage
}

// StyledLine is a visual line (post word-wrap) with its spans and indent.
type StyledLine struct {
	Spans  []Span
	Indent int // leading indent in cells (for lists, blockquotes)
}

// Parse converts raw markdown text into styled lines, word-wrapping at maxCols.
func Parse(rawText string, maxCols int) []StyledLine {
	if maxCols < 10 {
		maxCols = 10
	}

	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
	)
	src := []byte(rawText)
	reader := text.NewReader(src)
	doc := md.Parser().Parse(reader)

	w := &walker{
		src:     src,
		maxCols: maxCols,
	}
	w.walkBlock(doc)
	return w.lines
}

type walker struct {
	src     []byte
	maxCols int
	lines   []StyledLine

	// Accumulator for inline spans within a block.
	spans      []Span
	blockStyle SpanStyle
	indent     int
}

// emit flushes accumulated spans as word-wrapped lines.
func (w *walker) emit() {
	if len(w.spans) == 0 {
		return
	}
	wrapped := wrapSpans(w.spans, w.maxCols, w.indent)
	w.lines = append(w.lines, wrapped...)
	w.spans = nil
}

// emitBlank adds an empty line.
func (w *walker) emitBlank() {
	w.lines = append(w.lines, StyledLine{})
}

// walkBlock recursively walks block-level AST nodes.
func (w *walker) walkBlock(n ast.Node) {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *ast.Heading:
			style := StyleHeading1
			if node.Level == 2 {
				style = StyleHeading2
			} else if node.Level >= 3 {
				style = StyleHeading3
			}
			w.blockStyle = style
			w.walkInline(node)
			w.emit()
			w.blockStyle = StyleNormal

		case *ast.Paragraph:
			// Check if inside a blockquote or list — parent handles style.
			w.walkInline(node)
			w.emit()

		case *ast.ThematicBreak:
			w.lines = append(w.lines, StyledLine{
				Spans: []Span{{Text: "", Style: StyleHRule}},
			})

		case *ast.FencedCodeBlock:
			// Language label.
			lang := string(node.Language(w.src))
			if lang != "" {
				w.lines = append(w.lines, StyledLine{
					Spans: []Span{{Text: lang, Style: StyleCodeBlock}},
				})
			}
			// Code lines.
			lines := node.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				line := string(seg.Value(w.src))
				// Trim trailing newline.
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				for _, wl := range wrapPlain(line, w.maxCols) {
					w.lines = append(w.lines, StyledLine{
						Spans: []Span{{Text: wl, Style: StyleCodeBlock}},
					})
				}
			}

		case *ast.CodeBlock:
			lines := node.Lines()
			for i := 0; i < lines.Len(); i++ {
				seg := lines.At(i)
				line := string(seg.Value(w.src))
				if len(line) > 0 && line[len(line)-1] == '\n' {
					line = line[:len(line)-1]
				}
				for _, wl := range wrapPlain(line, w.maxCols) {
					w.lines = append(w.lines, StyledLine{
						Spans: []Span{{Text: wl, Style: StyleCodeBlock}},
					})
				}
			}

		case *ast.Blockquote:
			saved := w.blockStyle
			savedIndent := w.indent
			w.blockStyle = StyleBlockquote
			w.indent = savedIndent + 2
			w.walkBlock(node)
			w.blockStyle = saved
			w.indent = savedIndent

		case *ast.List:
			w.walkBlock(node)

		case *ast.ListItem:
			savedIndent := w.indent
			// Collect inline text from the list item's paragraph children.
			w.indent = savedIndent + 2

			// Check for task checkbox (first grandchild of any block child).
			hasCheckbox := false
			for ic := node.FirstChild(); ic != nil; ic = ic.NextSibling() {
				if fc := ic.FirstChild(); fc != nil {
					if cb, ok := fc.(*east.TaskCheckBox); ok {
						hasCheckbox = true
						if cb.IsChecked {
							w.spans = append(w.spans, Span{Text: "[x] ", Style: StyleCheckboxChecked})
						} else {
							w.spans = append(w.spans, Span{Text: "[ ] ", Style: StyleCheckboxUnchecked})
						}
					}
				}
			}

			if !hasCheckbox {
				// Determine marker: ordered or unordered.
				if list, ok := node.Parent().(*ast.List); ok && list.IsOrdered() {
					idx := 1
					if list.Start > 0 {
						idx = list.Start
					}
					for sib := node.Parent().FirstChild(); sib != nil && sib != node; sib = sib.NextSibling() {
						idx++
					}
					w.spans = append(w.spans, Span{Text: strconv.Itoa(idx) + ". ", Style: StyleListItem})
				} else {
					w.spans = append(w.spans, Span{Text: "- ", Style: StyleListItem})
				}
			}

			// Walk children: inline content is collected, nested lists recurse.
			for ic := node.FirstChild(); ic != nil; ic = ic.NextSibling() {
				if _, ok := ic.(*ast.List); ok {
					w.emit()
					w.walkBlock(ic)
				} else {
					w.walkInline(ic)
					w.emit()
				}
			}
			w.indent = savedIndent

		case *east.Table:
			w.walkTable(node)

		default:
			// Unknown block — recurse.
			if child.HasChildren() {
				w.walkBlock(child)
			}
			// Blank line between blocks.
			if child.NextSibling() != nil {
				w.emitBlank()
			}
			continue
		}

		// Blank line between top-level blocks (not inside lists/blockquotes).
		if child.NextSibling() != nil && child.Parent() == n && n.Kind() == ast.KindDocument {
			w.emitBlank()
		}
	}
}

// tableRow holds pre-collected cell text and its style for a single row.
type tableRow struct {
	cells []string
	style SpanStyle // StyleTableHeader or StyleTableCell
}

// walkTable handles GFM table nodes with column-aligned output.
// Two passes: collect all cell texts to compute max column widths,
// then emit padded cells.
func (w *walker) walkTable(table *east.Table) {
	var rows []tableRow
	var numCols int

	// Pass 1: collect all rows.
	for child := table.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *east.TableHeader:
			var cells []string
			for cell := node.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, w.collectText(cell))
			}
			if len(cells) > numCols {
				numCols = len(cells)
			}
			rows = append(rows, tableRow{cells: cells, style: StyleTableHeader})
		case *east.TableRow:
			var cells []string
			for cell := node.FirstChild(); cell != nil; cell = cell.NextSibling() {
				cells = append(cells, w.collectText(cell))
			}
			if len(cells) > numCols {
				numCols = len(cells)
			}
			rows = append(rows, tableRow{cells: cells, style: StyleTableCell})
		}
	}

	if numCols == 0 {
		return
	}

	// Compute max width per column.
	colWidths := make([]int, numCols)
	for _, row := range rows {
		for i, cell := range row.cells {
			runeLen := utf8.RuneCountInString(cell)
			if runeLen > colWidths[i] {
				colWidths[i] = runeLen
			}
		}
	}

	// Pass 2: emit padded rows.
	for ri, row := range rows {
		var spans []Span
		for ci := 0; ci < numCols; ci++ {
			if ci > 0 {
				spans = append(spans, Span{Text: " | ", Style: StyleNormal})
			}
			cell := ""
			if ci < len(row.cells) {
				cell = row.cells[ci]
			}
			padded := cell + strings.Repeat(" ", colWidths[ci]-utf8.RuneCountInString(cell))
			spans = append(spans, Span{Text: padded, Style: row.style})
		}
		w.lines = append(w.lines, StyledLine{Spans: spans})

		// Separator line after header row.
		if row.style == StyleTableHeader && ri == 0 {
			w.lines = append(w.lines, StyledLine{
				Spans: []Span{{Text: "", Style: StyleTableSeparator}},
			})
		}
	}
}

// collectText extracts plain text from a node and its children.
func (w *walker) collectText(n ast.Node) string {
	var buf bytes.Buffer
	w.collectTextRec(n, &buf)
	return buf.String()
}

func (w *walker) collectTextRec(n ast.Node, buf *bytes.Buffer) {
	if t, ok := n.(*ast.Text); ok {
		buf.Write(t.Segment.Value(w.src))
		if t.SoftLineBreak() {
			buf.WriteByte(' ')
		}
		return
	}
	if cs, ok := n.(*ast.CodeSpan); ok {
		for ic := cs.FirstChild(); ic != nil; ic = ic.NextSibling() {
			w.collectTextRec(ic, buf)
		}
		return
	}
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		w.collectTextRec(child, buf)
	}
}

// walkInline recursively walks inline-level AST nodes, accumulating spans.
func (w *walker) walkInline(n ast.Node) {
	for child := n.FirstChild(); child != nil; child = child.NextSibling() {
		switch node := child.(type) {
		case *ast.Text:
			style := w.blockStyle
			txt := string(node.Segment.Value(w.src))
			w.spans = append(w.spans, Span{Text: txt, Style: style})
			if node.SoftLineBreak() {
				w.spans = append(w.spans, Span{Text: " ", Style: style})
			}

		case *ast.CodeSpan:
			txt := w.collectText(node)
			w.spans = append(w.spans, Span{Text: txt, Style: StyleInlineCode})

		case *ast.Emphasis:
			style := StyleItalic
			if node.Level == 2 {
				style = StyleBold
			}
			saved := w.blockStyle
			w.blockStyle = style
			w.walkInline(node)
			w.blockStyle = saved

		case *ast.Link:
			linkText := w.collectText(node)
			url := string(node.Destination)
			w.spans = append(w.spans, Span{Text: linkText, Style: StyleLink, Extra: url})

		case *ast.Image:
			altText := w.collectText(node)
			url := string(node.Destination)
			label := "[image: " + altText + "]"
			w.spans = append(w.spans, Span{Text: label, Style: StyleImage, Extra: url})

		case *ast.AutoLink:
			url := string(node.URL(w.src))
			w.spans = append(w.spans, Span{Text: url, Style: StyleLink, Extra: url})

		case *east.Strikethrough:
			txt := w.collectText(node)
			w.spans = append(w.spans, Span{Text: txt, Style: StyleStrikethrough})

		case *east.TaskCheckBox:
			// Handled at ListItem level — skip here.

		case *ast.String:
			w.spans = append(w.spans, Span{Text: string(node.Value), Style: w.blockStyle})

		default:
			// Unknown inline — recurse.
			if child.HasChildren() {
				w.walkInline(child)
			}
		}
	}
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

	totalLen := 0
	for _, s := range spans {
		totalLen += utf8.RuneCountInString(s.Text)
	}

	usable := maxCols - indent
	if usable < 5 {
		usable = 5
	}

	if totalLen <= usable {
		return []StyledLine{{Spans: spans, Indent: indent}}
	}

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
