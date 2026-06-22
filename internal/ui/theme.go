package ui

import (
	"fmt"
	"strings"

	"github.com/0xbenc/passage/internal/termstyle"
)

type pickerTheme struct {
	theme termstyle.Theme
}

func (p pickerTheme) style(role termstyle.Role, value string) string {
	return p.theme.Style(role, value)
}

func (p pickerTheme) title(value string) string {
	return p.style(termstyle.RoleTitle, value)
}

func (p pickerTheme) selected(value string) string {
	return p.style(termstyle.RoleSelected, value)
}

func (p pickerTheme) primary(value string) string {
	return p.style(termstyle.RolePrimary, value)
}

func (p pickerTheme) secondary(value string) string {
	return p.style(termstyle.RoleSecondary, value)
}

func (p pickerTheme) muted(value string) string {
	return p.style(termstyle.RoleMuted, value)
}

func (p pickerTheme) subtle(value string) string {
	return p.style(termstyle.RoleSubtle, value)
}

func (p pickerTheme) accent(value string) string {
	return p.style(termstyle.RoleAccent, value)
}

func (p pickerTheme) search(value string) string {
	return p.style(termstyle.RoleSearch, value)
}

// onBar renders value with the given foreground role composited over the
// selection-bar background, as a single SGR open+reset. Styling each segment of
// the cursor row this way (including padding) keeps the bar background
// continuous across the whole row while the foreground still varies — so the
// match highlight survives on the selected row. In NoColor mode it is plain
// text (the caret is then the only selection cue).
func (p pickerTheme) onBar(fg termstyle.Role, value string) string {
	bar := p.theme.Codes[termstyle.RoleSelectedBar]
	code := joinSGR(bar, p.theme.Codes[fg])
	return termstyle.Apply(p.theme.NoColor, code, value)
}

func joinSGR(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + ";" + b
	}
}

func (p pickerTheme) danger(value string) string {
	return p.style(termstyle.RoleDanger, value)
}

func (p pickerTheme) success(value string) string {
	return p.style(termstyle.RoleSuccess, value)
}

func (p pickerTheme) warning(value string) string {
	return p.style(termstyle.RoleWarning, value)
}

func (p pickerTheme) pill(value string) string {
	return p.style(termstyle.RolePill, " "+value+" ")
}

func (p pickerTheme) badge(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return p.pill(strings.ToUpper(value))
}

func (p pickerTheme) counter(current int, total int) string {
	return p.muted(fmt.Sprintf("%d/%d", current, total))
}

func resolveTheme(noColor bool, theme termstyle.Theme, themeFile string) (termstyle.Theme, error) {
	if !theme.IsZero() {
		return theme.WithNoColor(theme.NoColor || noColor), nil
	}
	return termstyle.ResolveTheme(termstyle.ThemeOptions{
		File:    themeFile,
		NoColor: noColor,
	})
}
