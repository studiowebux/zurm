package renderer

// MdSpanStyle identifies the visual style of a markdown span.
// Mirrors markdown.SpanStyle so the renderer does not import the markdown package.
type MdSpanStyle int

const (
	MdStyleNormal            MdSpanStyle = iota
	MdStyleHeading1                      // #
	MdStyleHeading2                      // ##
	MdStyleHeading3                      // ### and deeper
	MdStyleBold                          // **text** / __text__
	MdStyleItalic                        // *text* / _text_
	MdStyleInlineCode                    // `text`
	MdStyleCodeBlock                     // fenced ``` lines
	MdStyleLink                          // [text](url)
	MdStyleBlockquote                    // > text
	MdStyleListItem                      // - item / 1. item
	MdStyleHRule                         // ---
	MdStyleStrikethrough                 // ~~text~~
	MdStyleImage                         // ![alt](url)
	MdStyleCheckboxChecked               // - [x]
	MdStyleCheckboxUnchecked             // - [ ]
	MdStyleTableHeader                   // table header cells
	MdStyleTableSeparator                // table separator row
	MdStyleTableCell                     // table data cells
)

// MdSpan is a contiguous run of text with one style.
type MdSpan struct {
	Text  string
	Style MdSpanStyle
	Extra string // URL for links/images
}

// MdStyledLine is a visual line (post word-wrap) with its spans and indent.
type MdStyledLine struct {
	Spans  []MdSpan
	Indent int // leading indent in cells (for lists, blockquotes)
}
