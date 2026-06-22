package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xbenc/passage/internal/termstyle"
)

func TestWrapSecretRuneSafe(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		width  int
	}{
		{"ascii", "correct-horse-battery-staple", 8},
		{"multibyte_cjk", "日本語のパスワード表記です", 4},
		{"emoji", "🔐🔑🛡️🗝️🔓🔒🔐🔑", 3},
		{"mixed", "pass-日本語-word-表記", 5},
		{"narrow_width_one", "abcd日本", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := wrapSecret(tc.secret, tc.width)
			if len(lines) == 0 {
				t.Fatalf("wrapSecret returned no lines")
			}
			var rebuilt strings.Builder
			for i, line := range lines {
				if !utf8.ValidString(line) {
					t.Errorf("line %d is not valid UTF-8: %q", i, line)
				}
				// Every line except a forced single over-wide rune must fit.
				if w := termstyle.VisibleWidth(line); w > tc.width && utf8.RuneCountInString(line) > 1 {
					t.Errorf("line %d width %d exceeds %d: %q", i, w, tc.width, line)
				}
				rebuilt.WriteString(line)
			}
			if got := rebuilt.String(); got != tc.secret {
				t.Errorf("rejoined secret = %q, want %q", got, tc.secret)
			}
		})
	}
}

func TestWrapSecretShortFits(t *testing.T) {
	lines := wrapSecret("hunter2", 20)
	if len(lines) != 1 || lines[0] != "hunter2" {
		t.Fatalf("wrapSecret short = %#v, want single [hunter2]", lines)
	}
}

func TestWrapSecretEmpty(t *testing.T) {
	lines := wrapSecret("", 10)
	if len(lines) != 1 || lines[0] != "" {
		t.Fatalf("wrapSecret empty = %#v, want single empty line", lines)
	}
}
