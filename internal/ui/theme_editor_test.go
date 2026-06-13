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

func TestThemeEditorAcceptsRawRoleEdit(t *testing.T) {
	model := newThemeEditorModel(ThemeEditorOptions{})
	model.cursor = 1 // primary
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
	model.cursor = 1 // primary
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
	model.cursor = 1 // primary
	model.cycleCurrent(1)
	if got := model.values[termstyle.RolePrimary]; got != "default" {
		t.Fatalf("primary = %q, want default", got)
	}
	model.clearCurrent()
	if _, ok := model.values[termstyle.RolePrimary]; ok {
		t.Fatalf("primary override still present after clear")
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
