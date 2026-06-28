package ui

import (
	"strings"

	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/termchrome"
	"github.com/0xbenc/termtheme"
)

type workflowShell struct {
	Title  string
	Body   []string
	Footer string
	Danger bool
}

// renderWorkflowShell is passage's local shell composition over the shared
// termchrome box geometry. The geometry (corners, divider, body padding, label
// truncation) lives in termchrome; passage keeps the composition local and feeds
// its own truncateStyled as the Truncator so the Sanitize-on-overflow policy
// stays passage's. Output is byte-identical to the prior inline geometry (gated
// by shell_test.go goldens + assertBorderIntegrity).
func renderWorkflowShell(theme pickerTheme, width int, opts workflowShell) string {
	width = max(48, width)
	tt := termtheme.Theme(theme.theme)
	var lines []string
	titleRole := termstyle.RoleTitle
	if opts.Danger {
		titleRole = termstyle.RoleDanger
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "passage"
	}
	lines = append(lines, termchrome.Top(tt, theme.theme.Style(titleRole, title), width, truncateStyled))
	for _, line := range opts.Body {
		lines = append(lines, termchrome.Line(tt, line, width, truncateStyled))
	}
	if opts.Footer != "" {
		lines = append(lines, termchrome.Divider(tt, width))
		for _, line := range strings.Split(strings.TrimRight(opts.Footer, "\n"), "\n") {
			lines = append(lines, termchrome.Line(tt, theme.muted(line), width, truncateStyled))
		}
	}
	lines = append(lines, termchrome.Bottom(tt, width))
	return strings.Join(lines, "\n") + "\n"
}

func truncateStyled(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if termstyle.VisibleWidth(value) <= width {
		return value
	}
	// Sanitize (not just Strip) on the overflow path: it drops styling AND
	// neutralizes raw C0/C1/DEL (incl. U+009B, the CSI introducer some terminals
	// execute), so an oversized chrome label can never inject control bytes.
	return termstyle.Truncate(termstyle.Sanitize(value), width)
}

func wrapText(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(value, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if termstyle.VisibleWidth(line)+1+termstyle.VisibleWidth(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, line)
	}
	return out
}

// joinColumns lays two pre-rendered, possibly-styled column blocks side by
// side: each output row is left padded/truncated to leftWidth, then sep, then
// right padded/truncated to rightWidth. The caller guarantees
// leftWidth+VisibleWidth(sep)+rightWidth equals the shell's inner width, so the
// joined row fills the row exactly and workflowLine never re-truncates it
// (which would mangle the divider). Rows are capped at height.
func joinColumns(left, right []string, leftWidth, rightWidth int, sep string, height int) []string {
	rows := max(len(left), len(right))
	if height > 0 && rows > height {
		rows = height
	}
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		l = termstyle.PadRight(termstyle.Truncate(l, leftWidth), leftWidth)
		r = termstyle.PadRight(termstyle.Truncate(r, rightWidth), rightWidth)
		out = append(out, l+sep+r)
	}
	return out
}

// hardWrap breaks value into lines no wider than width cells, splitting on rune
// boundaries even mid-"word" (paths have no spaces). Unlike wrapText it never
// overflows the width, so a long entry path wraps cleanly inside a column.
func hardWrap(value string, width int) []string {
	if width < 1 {
		return []string{value}
	}
	var out []string
	var line strings.Builder
	w := 0
	for _, r := range value {
		rw := termstyle.VisibleWidth(string(r))
		if rw < 1 {
			rw = 1
		}
		if w > 0 && w+rw > width {
			out = append(out, line.String())
			line.Reset()
			w = 0
		}
		line.WriteRune(r)
		w += rw
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}

func splitRendered(value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func max(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func clamp(value int, lo int, hi int) int {
	if value < lo {
		return lo
	}
	if value > hi {
		return hi
	}
	return value
}
