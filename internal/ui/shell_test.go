package ui

import (
	"strings"
	"testing"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

// noColorShellTheme renders the chrome with no SGR escapes, so renderWorkflowShell
// emits stable box-drawing bytes that can be pinned as exact-string goldens.
func noColorShellTheme() pickerTheme {
	return pickerTheme{theme: termstyle.TerminalTheme().WithNoColor(true)}
}

// These goldens encode the CURRENT chrome geometry (corners, divider, body
// padding, and the "~" overflow marker). They must stay byte-identical across
// the P4 termchrome extraction — that is the proof the migration is lossless.
// No -update flag by design (plan K-f): if chrome changes, edit these by hand.

func TestRenderWorkflowShellNormalGolden(t *testing.T) {
	got := renderWorkflowShell(noColorShellTheme(), 48, workflowShell{
		Title:  "ENTRIES",
		Body:   []string{"alpha", "beta"},
		Footer: "type filter   ? keys",
	})
	want := "╭ ENTRIES ─────────────────────────────────────╮\n" +
		"│ alpha                                        │\n" +
		"│ beta                                         │\n" +
		"├──────────────────────────────────────────────┤\n" +
		"│ type filter   ? keys                         │\n" +
		"╰──────────────────────────────────────────────╯\n"
	if got != want {
		t.Errorf("normal shell golden mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestRenderWorkflowShellDangerGolden(t *testing.T) {
	got := renderWorkflowShell(noColorShellTheme(), 48, workflowShell{
		Title:  "REMOVE",
		Body:   []string{"delete work/github?"},
		Footer: "y confirm   esc cancel",
		Danger: true,
	})
	want := "╭ REMOVE ──────────────────────────────────────╮\n" +
		"│ delete work/github?                          │\n" +
		"├──────────────────────────────────────────────┤\n" +
		"│ y confirm   esc cancel                       │\n" +
		"╰──────────────────────────────────────────────╯\n"
	if got != want {
		t.Errorf("danger shell golden mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestRenderWorkflowShellOverflowGolden(t *testing.T) {
	got := renderWorkflowShell(noColorShellTheme(), 48, workflowShell{
		Title: "LONG",
		Body:  []string{"this/is/a/very/long/entry/path/that/exceeds/the/inner/width/by/a/lot"},
	})
	want := "╭ LONG ────────────────────────────────────────╮\n" +
		"│ this/is/a/very/long/entry/path/that/exceeds~ │\n" +
		"╰──────────────────────────────────────────────╯\n"
	if got != want {
		t.Errorf("overflow shell golden mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// In NoColor mode the Danger title is byte-identical to the normal title, so the
// goldens above cannot prove the RoleTitle->RoleDanger flip. This colored check
// gates that the title actually changes role when Danger is set.
func TestRenderWorkflowShellDangerFlipsTitleRole(t *testing.T) {
	theme := pickerTheme{theme: termstyle.TerminalTheme()} // colored
	normal := renderWorkflowShell(theme, 48, workflowShell{Title: "REMOVE", Body: []string{"x"}})
	danger := renderWorkflowShell(theme, 48, workflowShell{Title: "REMOVE", Body: []string{"x"}, Danger: true})
	titleCode := "\x1b[" + termstyle.TerminalTheme().Codes[termstyle.RoleTitle] + "m"
	dangerCode := "\x1b[" + termstyle.TerminalTheme().Codes[termstyle.RoleDanger] + "m"
	if !strings.Contains(normal, titleCode) {
		t.Errorf("normal title not styled with RoleTitle (%q):\n%q", titleCode, normal)
	}
	if strings.Contains(normal, dangerCode) {
		t.Errorf("normal title unexpectedly styled with RoleDanger (%q)", dangerCode)
	}
	if !strings.Contains(danger, dangerCode) {
		t.Errorf("danger title not styled with RoleDanger (%q):\n%q", dangerCode, danger)
	}
}

// assertBorderIntegrity is the S5 invariant ported from ssherpa: every framed
// line of a rendered screen (top/divider/bottom edges and "│ … │" body rows)
// has the same cell width, so a wide rune (CJK/emoji) can never tear the border.
// It derives the frame width from the lines themselves, so it is independent of
// the screen's own width clamp.
func assertBorderIntegrity(t *testing.T, name, content string) {
	t.Helper()
	frameWidth := -1
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		plain := termstyle.Strip(line)
		if plain == "" {
			continue
		}
		switch []rune(plain)[0] {
		case '╭', '╰', '├', '│':
			w := termstyle.VisibleWidth(line)
			if frameWidth == -1 {
				frameWidth = w
			} else if w != frameWidth {
				t.Errorf("%s: framed line width %d != frame width %d: %q", name, w, frameWidth, plain)
			}
		}
	}
	if frameWidth == -1 {
		t.Errorf("%s: no framed lines found", name)
	}
}

// wide-rune entry names that would tear a rune-counted border.
var borderTortureEntries = []passstore.Entry{
	{Path: "日本/tokyo", Display: "日本/tokyo"},
	{Path: "🔐/vault", Display: "🔐/vault"},
	{Path: "work/github", Display: "work/github"},
	{Path: "münchen/café", Display: "münchen/café"},
}

// TestTruncateStyledSanitizesOnOverflow pins the security invariant (ported from
// ssherpa): an overflowing chrome line carrying a raw C1 control (U+009B, a CSI
// introducer) must not leak it into the trusted chrome, and must stay within the
// cell budget. This fails under the old Strip-on-overflow policy.
func TestTruncateStyledSanitizesOnOverflow(t *testing.T) {
	in := "host-" + string(rune(0x9b)) + "31mEVIL-with-enough-text-to-overflow"
	out := truncateStyled(in, 12)
	if strings.ContainsRune(out, 0x9b) {
		t.Fatalf("C1 control leaked through truncateStyled: %q", out)
	}
	if w := termstyle.VisibleWidth(out); w > 12 {
		t.Fatalf("truncateStyled width = %d, exceeds 12: %q", w, out)
	}
}

// TestTruncateStyledEmojiWithinWidth pins that a wide-rune line never exceeds the
// cell budget once truncated.
func TestTruncateStyledEmojiWithinWidth(t *testing.T) {
	out := truncateStyled("🔐🔑🛡️ secret-host-name-that-overflows", 8)
	if w := termstyle.VisibleWidth(out); w > 8 {
		t.Fatalf("emoji line width = %d, exceeds 8: %q", w, out)
	}
}

func TestPickerBorderIntegrity(t *testing.T) {
	// query "" keeps every wide row (incl. the emoji) visible; "o" exercises the
	// highlight path on the CJK + ascii rows. Widths span the single-column and
	// the >=92 detail-pane split. NoColor toggles the SGR path.
	for _, q := range []string{"", "o"} {
		for _, width := range []int{48, 56, 72, 100, 120} {
			for _, nc := range []bool{true, false} {
				m := newPickerModel(borderTortureEntries, PickOptions{NoAltScreen: true, NoColor: nc}, termstyle.TerminalTheme())
				m.width = width
				m.height = 24
				m.query = q
				m.applyFilter()
				assertBorderIntegrity(t, "picker", m.View().Content)
			}
		}
	}
}
