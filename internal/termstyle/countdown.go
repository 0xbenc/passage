package termstyle

import "strings"

// Bar renders a width-cell progress bar filled to remaining/total using the
// glyph set's full/empty cells (e.g. "▰▰▰▱▱" or ASCII "###--"). It is plain
// (unstyled); apply UrgencyRole via a theme to color it. Out-of-range inputs
// are clamped; width<1 yields an empty string.
func (g GlyphSet) Bar(remaining, total, width int) string {
	if width < 1 {
		return ""
	}
	full := g.BarFull
	empty := g.BarEmpty
	if full == "" {
		full = "#"
	}
	if empty == "" {
		empty = "-"
	}
	if total <= 0 {
		return strings.Repeat(empty, width)
	}
	if remaining < 0 {
		remaining = 0
	}
	if remaining > total {
		remaining = total
	}
	filled := (remaining*width + total/2) / total // rounded to nearest cell
	if filled > width {
		filled = width
	}
	return strings.Repeat(full, filled) + strings.Repeat(empty, width-filled)
}

// UrgencyRole ramps a countdown's color from success through warning to danger
// as it drains: danger in roughly the last sixth, warning in the last half,
// success otherwise. For a 30s TOTP that lands danger at <=5s and warning at
// <=15s, matching the at-a-glance "is this code about to expire" read.
func UrgencyRole(remaining, total int) Role {
	if total <= 0 {
		return RoleSuccess
	}
	switch {
	case remaining*6 <= total:
		return RoleDanger
	case remaining*2 <= total:
		return RoleWarning
	default:
		return RoleSuccess
	}
}
