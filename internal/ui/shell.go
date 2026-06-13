package ui

import (
	"strings"

	"github.com/0xbenc/passage/internal/termstyle"
)

type workflowShell struct {
	Title  string
	Body   []string
	Footer string
	Danger bool
}

func renderWorkflowShell(theme pickerTheme, width int, opts workflowShell) string {
	width = max(48, width)
	var lines []string
	titleRole := termstyle.RoleTitle
	if opts.Danger {
		titleRole = termstyle.RoleDanger
	}
	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = "passage"
	}
	lines = append(lines, workflowEdge(theme, "╭", "╮", theme.theme.Style(titleRole, title), width))
	for _, line := range opts.Body {
		lines = append(lines, workflowLine(theme, line, width))
	}
	if opts.Footer != "" {
		lines = append(lines, workflowDivider(theme, width))
		for _, line := range strings.Split(strings.TrimRight(opts.Footer, "\n"), "\n") {
			lines = append(lines, workflowLine(theme, theme.muted(line), width))
		}
	}
	lines = append(lines, workflowEdge(theme, "╰", "╯", "", width))
	return strings.Join(lines, "\n") + "\n"
}

func renderCompactPicker(theme pickerTheme, width int, title string, body []string, footer string) string {
	width = max(48, width)
	var lines []string
	header := theme.title(strings.TrimSpace(title))
	if header == "" {
		header = theme.title("PASSAGE")
	}
	lines = append(lines, termstyle.Truncate(header, width))
	for _, line := range body {
		lines = append(lines, termstyle.Truncate(line, width))
	}
	if footer != "" {
		lines = append(lines, theme.muted(strings.Repeat("─", min(width, 80))))
		for _, line := range strings.Split(footer, "\n") {
			lines = append(lines, theme.muted(termstyle.Truncate(line, width)))
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

func workflowEdge(theme pickerTheme, left string, right string, label string, width int) string {
	inner := max(0, width-2)
	if inner == 0 {
		return theme.theme.Style(termstyle.RoleBorder, left+right)
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return theme.theme.Style(termstyle.RoleBorder, left+strings.Repeat("─", inner)+right)
	}
	label = " " + truncateStyled(label, max(0, inner-2)) + " "
	remaining := inner - termstyle.VisibleWidth(label)
	if remaining < 0 {
		remaining = 0
	}
	return theme.theme.Style(termstyle.RoleBorder, left) + label + theme.theme.Style(termstyle.RoleBorder, strings.Repeat("─", remaining)+right)
}

func workflowDivider(theme pickerTheme, width int) string {
	inner := max(0, width-2)
	return theme.theme.Style(termstyle.RoleBorder, "├"+strings.Repeat("─", inner)+"┤")
}

func workflowLine(theme pickerTheme, line string, width int) string {
	inner := max(0, width-4)
	content := termstyle.PadRight(truncateStyled(line, inner), inner)
	return theme.theme.Style(termstyle.RoleBorder, "│ ") + content + theme.theme.Style(termstyle.RoleBorder, " │")
}

func truncateStyled(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if termstyle.VisibleWidth(value) <= width {
		return value
	}
	return termstyle.Truncate(termstyle.Strip(value), width)
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
