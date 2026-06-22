package termstyle

import (
	"strings"
	"testing"
)

func TestBarFillsProportionally(t *testing.T) {
	g := ASCIIGlyphs()
	cases := []struct {
		remaining, total, width int
		want                    string
	}{
		{10, 10, 10, "##########"},
		{0, 10, 10, "----------"},
		{5, 10, 10, "#####-----"},
		{30, 30, 10, "##########"},
		{15, 30, 8, "####----"},
		{-3, 10, 4, "----"},   // clamped low
		{99, 10, 4, "####"},   // clamped high
		{5, 0, 4, "----"},     // total<=0 -> empty
		{5, 10, 0, ""},        // width<1 -> empty string
	}
	for _, tc := range cases {
		got := g.Bar(tc.remaining, tc.total, tc.width)
		if got != tc.want {
			t.Errorf("Bar(%d,%d,%d) = %q, want %q", tc.remaining, tc.total, tc.width, got, tc.want)
		}
		if tc.width >= 1 && len([]rune(got)) != tc.width {
			t.Errorf("Bar(%d,%d,%d) width = %d, want %d", tc.remaining, tc.total, tc.width, len([]rune(got)), tc.width)
		}
	}
}

func TestUnicodeBarUsesGlyphCells(t *testing.T) {
	g := UnicodeGlyphs()
	bar := g.Bar(5, 10, 10)
	if c := strings.Count(bar, g.BarFull); c != 5 {
		t.Fatalf("unicode bar full cells = %d, want 5: %q", c, bar)
	}
}

func TestUrgencyRoleRamp(t *testing.T) {
	// 30s TOTP: success > 15s, warning 6..15s, danger <= 5s.
	cases := []struct {
		remaining int
		want      Role
	}{
		{30, RoleSuccess},
		{20, RoleSuccess},
		{15, RoleWarning},
		{6, RoleWarning},
		{5, RoleDanger},
		{1, RoleDanger},
		{0, RoleDanger},
	}
	for _, tc := range cases {
		if got := UrgencyRole(tc.remaining, 30); got != tc.want {
			t.Errorf("UrgencyRole(%d,30) = %q, want %q", tc.remaining, got, tc.want)
		}
	}
}
