package termstyle

import "strings"

// GlyphSet is the set of decorative runes the UI animates with — spinner
// frames and progress-bar cells. It exists so motion never renders as mojibake
// on a terminal without UTF-8: every field has an ASCII fallback, chosen by
// locale, independently of color (a monochrome UTF-8 xterm still gets the
// pretty glyphs; a C/POSIX-locale terminal gets ASCII even in full color).
type GlyphSet struct {
	Name     string
	ASCII    bool
	Spinner  []string // animation frames, cycled per tick
	BarFull  string   // filled progress-bar cell
	BarEmpty string   // empty progress-bar cell
}

// UnicodeGlyphs is the default rich set: a braille spinner and block bar cells.
func UnicodeGlyphs() GlyphSet {
	return GlyphSet{
		Name:     "unicode",
		Spinner:  []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		BarFull:  "▰",
		BarEmpty: "▱",
	}
}

// ASCIIGlyphs is the 7-bit fallback for terminals without UTF-8. Every rune is
// <= 0x7e so it is safe on legacy codepages.
func ASCIIGlyphs() GlyphSet {
	return GlyphSet{
		Name:     "ascii",
		ASCII:    true,
		Spinner:  []string{"|", "/", "-", "\\"},
		BarFull:  "#",
		BarEmpty: "-",
	}
}

// DefaultGlyphs is used when a caller does not resolve a set from the
// environment. Most terminals are UTF-8, so the rich set is the default; the
// resolved set from ResolveGlyphs should be preferred where the env is known.
func DefaultGlyphs() GlyphSet { return UnicodeGlyphs() }

// Frame returns the spinner frame for tick count n, cycling safely.
func (g GlyphSet) Frame(n int) string {
	if len(g.Spinner) == 0 {
		return ""
	}
	if n < 0 {
		n = -n
	}
	return g.Spinner[n%len(g.Spinner)]
}

// ResolveGlyphs picks a glyph set from the environment: Unicode when the active
// locale advertises UTF-8, ASCII otherwise. Glyph choice is deliberately
// decoupled from NoColor — capability, not color, decides.
func ResolveGlyphs(env []string) GlyphSet {
	if localeIsUTF8(env) {
		return UnicodeGlyphs()
	}
	return ASCIIGlyphs()
}

func localeIsUTF8(env []string) bool {
	vals := themeEnv(env)
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(strings.TrimSpace(vals[key]))
		if v == "" {
			continue
		}
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return false
}
