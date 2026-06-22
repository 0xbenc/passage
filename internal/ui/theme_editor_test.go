package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

func TestThemeEditorViewHonorsNoColor(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{
		NoColor:    true,
		ConfigPath: "/tmp/theme.conf",
	})

	lines := model.view(104, pickerTheme{theme: model.currentTheme()})
	text := strings.Join(lines, "\n")
	for _, want := range []string{"PASSAGE THEME BUILDER", "Config", "Contrast", "SCHEMA", "PREVIEW", "theme.conf", "primary", "s save"} {
		if !strings.Contains(text, want) {
			t.Fatalf("view = %q, want substring %q", text, want)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("view contains ANSI escapes with NoColor: %q", text)
	}
}

// themeCursorForRole returns the editor cursor index for a role, accounting
// for the base-palette selector that occupies cursor 0.
func themeCursorForRole(role termstyle.Role) int {
	for i, meta := range themeRoles {
		if meta.Role == role {
			return i + 1
		}
	}
	return 0
}

func TestThemeEditorAcceptsRawRoleEdit(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{})
	model.cursor = themeCursorForRole(termstyle.RolePrimary)
	model.startEdit()
	model.editBuffer = "bold magenta"

	updated, done := model.updateEdit(themeKeyMsg("enter"))
	if done {
		t.Fatal("edit update returned done")
	}
	model = updated

	if model.editMode {
		t.Fatalf("editMode = true, want false")
	}
	cfg := model.config()
	if got := cfg.Specs[termstyle.RolePrimary]; got != "bold magenta" {
		t.Fatalf("primary spec = %q, want bold magenta", got)
	}
	if got := cfg.Codes[termstyle.RolePrimary]; got != "1;35" {
		t.Fatalf("primary code = %q, want 1;35", got)
	}
}

func TestThemeEditorRejectsInvalidRawRoleEdit(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{})
	model.cursor = themeCursorForRole(termstyle.RolePrimary)
	model.startEdit()
	model.editBuffer = "imaginary"

	updated, _ := model.updateEdit(themeKeyMsg("enter"))
	model = updated

	if !model.editMode {
		t.Fatalf("editMode = false, want to stay editing")
	}
	if !strings.Contains(model.message, "unknown style token") {
		t.Fatalf("message = %q, want parse error", model.message)
	}
}

func TestThemeEditorCyclesRolePresets(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{})
	model.cursor = themeCursorForRole(termstyle.RolePrimary)
	model.cycleCurrent(1)
	if got := model.values[termstyle.RolePrimary]; got != "default" {
		t.Fatalf("primary = %q, want default", got)
	}
	model.clearCurrent()
	if _, ok := model.values[termstyle.RolePrimary]; ok {
		t.Fatalf("primary override still present after clear")
	}
}

// TestThemeEditorBaseSelector covers the theme-*choice* UX: the base row at
// cursor 0 cycles the starting palette, drives the live preview, persists into
// the config, and resets with clear/reset.
func TestThemeEditorBaseSelector(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{})
	if model.base != "terminal" {
		t.Fatalf("default base = %q, want terminal", model.base)
	}
	model.cursor = 0 // base row
	model.cycleCurrent(1)
	if model.base != "vivid" {
		t.Fatalf("after cycle base = %q, want vivid", model.base)
	}
	// The live preview palette follows the base (primary becomes truecolor).
	if got := model.currentTheme().Style(termstyle.RolePrimary, "x"); !strings.Contains(got, "38;2;") {
		t.Fatalf("vivid base primary = %q, want truecolor", got)
	}
	// The choice persists into the saved config.
	if got := model.config().BaseName; got != "vivid" {
		t.Fatalf("config BaseName = %q, want vivid", got)
	}
	// Clear on the base row returns to the terminal default.
	model.clearCurrent()
	if model.base != "terminal" {
		t.Fatalf("after clear base = %q, want terminal", model.base)
	}
}

// TestThemeEditorSeedsBaseFromConfig verifies an opened editor reflects a base
// already chosen in the config file.
func TestThemeEditorSeedsBaseFromConfig(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{
		Config: termstyle.ThemeConfig{BaseName: "vivid"},
	})
	if model.base != "vivid" {
		t.Fatalf("seeded base = %q, want vivid", model.base)
	}
	if got := model.currentTheme().Style(termstyle.RoleTitle, "x"); !strings.Contains(got, "38;2;") {
		t.Fatalf("seeded vivid title = %q, want truecolor", got)
	}
}

func themeKeyMsg(value string) tea.KeyPressMsg {
	switch value {
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
	case "backspace":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})
	default:
		runes := []rune(value)
		if len(runes) == 0 {
			return tea.KeyPressMsg(tea.Key{})
		}
		return tea.KeyPressMsg(tea.Key{Code: runes[0], Text: value})
	}
}
