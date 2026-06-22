package termstyle

import "testing"

// TestASCIIGlyphsAreSevenBit guards the fallback promise: every rune in the
// ASCII glyph set is <= 0x7e so it renders on a legacy codepage terminal.
func TestASCIIGlyphsAreSevenBit(t *testing.T) {
	g := ASCIIGlyphs()
	all := append([]string{g.BarFull, g.BarEmpty}, g.Spinner...)
	for _, s := range all {
		for _, r := range s {
			if r > 0x7e {
				t.Fatalf("ASCII glyph %q contains non-7-bit rune %U", s, r)
			}
		}
	}
}

func TestResolveGlyphsFromLocale(t *testing.T) {
	cases := []struct {
		name      string
		env       []string
		wantASCII bool
	}{
		{"utf8 lang", []string{"LANG=en_US.UTF-8"}, false},
		{"utf8 lc_all wins", []string{"LC_ALL=C.UTF-8", "LANG=C"}, false},
		{"posix", []string{"LANG=C"}, true},
		{"lc_ctype non-utf8 wins over lang", []string{"LC_CTYPE=POSIX", "LANG=en_US.UTF-8"}, true},
		{"empty", []string{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveGlyphs(tc.env)
			if got.ASCII != tc.wantASCII {
				t.Fatalf("ResolveGlyphs(%v).ASCII = %v, want %v", tc.env, got.ASCII, tc.wantASCII)
			}
		})
	}
}

func TestFrameCycles(t *testing.T) {
	g := ASCIIGlyphs()
	if g.Frame(0) != "|" || g.Frame(1) != "/" || g.Frame(4) != "|" {
		t.Fatalf("Frame cycling wrong: %q %q %q", g.Frame(0), g.Frame(1), g.Frame(4))
	}
	if (GlyphSet{}).Frame(3) != "" {
		t.Fatal("empty glyph set Frame should be empty string")
	}
}

func TestTruncateWithMarker(t *testing.T) {
	if got := TruncateWith("hello world", 6, "…"); got != "hello…" {
		t.Fatalf("TruncateWith = %q, want hello…", got)
	}
	if w := VisibleWidth(TruncateWith("hello world", 6, "…")); w != 6 {
		t.Fatalf("TruncateWith width = %d, want 6", w)
	}
	// Empty marker keeps the full budget.
	if got := TruncateWith("hello", 3, ""); got != "hel" {
		t.Fatalf("TruncateWith empty marker = %q, want hel", got)
	}
}
