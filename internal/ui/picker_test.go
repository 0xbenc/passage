package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

func TestPickerLegacyArrowSequencesDoNotEnterFilter(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
		{Path: "beta", Display: "beta"},
	}, PickOptions{}, termstyle.TerminalTheme())
	model.cursor = 1

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "\x1b[A"}))
	got := updated.(pickerModel)

	if got.query != "" {
		t.Fatalf("query = %q, want empty", got.query)
	}
	if got.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", got.cursor)
	}
}

func TestPickerEscapeTextIsNotFilterInput(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "\x1b[D"}))
	got := updated.(pickerModel)

	if got.query != "" {
		t.Fatalf("query = %q, want empty", got.query)
	}
}

func TestPickerPrintableTextFilters(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
		{Path: "beta", Display: "beta"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "b"}))
	got := updated.(pickerModel)

	if got.query != "b" {
		t.Fatalf("query = %q, want b", got.query)
	}
	if len(got.filtered) != 1 || got.entries[got.filtered[0]].Path != "beta" {
		t.Fatalf("filtered = %#v", got.filtered)
	}
}

func TestPickerCommandLettersFilterInsteadOfActing(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "commandletters", Display: "commandletters"},
	}, PickOptions{}, termstyle.TerminalTheme())

	for _, text := range []string{"c", "r", "t", "p", "m", "x", "u", "R", "d", "k", "Q", "y", "/"} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: text}))
		model = updated.(pickerModel)
		if model.action != ActionNone {
			t.Fatalf("text %q set action %q", text, model.action)
		}
	}

	const want = "crtpmxuRdkQy/"
	if model.query != want {
		t.Fatalf("query = %q, want %q", model.query, want)
	}
}

func TestPickerViewUsesWorkflowShell(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		NoColor:   true,
		Title:     "passage",
		Version:   "dev",
		StoreRoot: "/store",
	}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 72
	model.height = 18

	text := model.View().Content
	for _, want := range []string{
		"╭ PASSAGE  dev",
		"│ 1 entries",
		"│ /filter",
		"├",
		"^O theme",
		"╰",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("view = %q, want substring %q", text, want)
		}
	}
}

func TestPickerCtrlHotkeysRunActions(t *testing.T) {
	tests := []struct {
		name   string
		key    tea.KeyPressMsg
		action Action
	}{
		{name: "copy", key: ctrlKey('y'), action: ActionCopy},
		{name: "reveal", key: ctrlKey('r'), action: ActionReveal},
		{name: "totp", key: ctrlKey('t'), action: ActionTOTP},
		{name: "pin", key: ctrlKey('p'), action: ActionTogglePin},
		{name: "clear clipboard", key: ctrlKey('x'), action: ActionClearClipboard},
		{name: "unpin all", key: ctrlKey('u'), action: ActionClearPins},
		{name: "clear recents", key: ctrlKey('e'), action: ActionClearRecents},
		{name: "doctor", key: ctrlKey('d'), action: ActionDoctor},
		{name: "keys", key: ctrlKey('k'), action: ActionKeys},
		{name: "quit ctrl-c", key: ctrlKey('c'), action: ActionQuit},
		{name: "quit ctrl-q", key: ctrlKey('q'), action: ActionQuit},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newPickerModel([]passstore.Entry{
				{Path: "alpha", Display: "alpha"},
			}, PickOptions{}, termstyle.TerminalTheme())

			updated, _ := model.Update(test.key)
			got := updated.(pickerModel)

			if got.action != test.action {
				t.Fatalf("action = %q, want %q", got.action, test.action)
			}
		})
	}
}

func TestPickerCtrlFTogglesMFAOnly(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha", HasMFA: true},
		{Path: "beta", Display: "beta"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(ctrlKey('f'))
	got := updated.(pickerModel)

	if !got.mfaOnly {
		t.Fatal("mfaOnly = false, want true")
	}
	if len(got.filtered) != 1 || got.entries[got.filtered[0]].Path != "alpha" {
		t.Fatalf("filtered = %#v", got.filtered)
	}
}

func TestPickerCtrlPIsPinNotMoveUp(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
		{Path: "beta", Display: "beta"},
	}, PickOptions{}, termstyle.TerminalTheme())
	model.cursor = 1

	updated, _ := model.Update(ctrlKey('p'))
	got := updated.(pickerModel)

	if got.action != ActionTogglePin {
		t.Fatalf("action = %q, want %q", got.action, ActionTogglePin)
	}
	if got.selected != 1 {
		t.Fatalf("selected = %d, want 1", got.selected)
	}
}

func TestPickerMFASecretEntryEnterGeneratesTOTP(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/mfa", Display: "work | github | mfa", HasMFA: true, MFATarget: "work/github/mfa"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updated.(pickerModel)

	if got.action != ActionTOTP {
		t.Fatalf("action = %q, want %q", got.action, ActionTOTP)
	}
}

func TestPickerMFASecretEntryCtrlTRevealsSecret(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/mfa", Display: "work | github | mfa", HasMFA: true, MFATarget: "work/github/mfa"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(ctrlKey('t'))
	got := updated.(pickerModel)

	if got.action != ActionReveal {
		t.Fatalf("action = %q, want %q", got.action, ActionReveal)
	}
}

func TestPickerParentMFAEntryKeepsDefaultHotkeys(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github", Display: "work | github", HasMFA: true, MFATarget: "work/github/mfa"},
	}, PickOptions{}, termstyle.TerminalTheme())

	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := updated.(pickerModel)
	if got.action != ActionCopy {
		t.Fatalf("enter action = %q, want %q", got.action, ActionCopy)
	}

	model = newPickerModel([]passstore.Entry{
		{Path: "work/github", Display: "work | github", HasMFA: true, MFATarget: "work/github/mfa"},
	}, PickOptions{}, termstyle.TerminalTheme())
	updated, _ = model.Update(ctrlKey('t'))
	got = updated.(pickerModel)
	if got.action != ActionTOTP {
		t.Fatalf("ctrl+t action = %q, want %q", got.action, ActionTOTP)
	}
}

func TestPickerRunnerActionStaysInPickerAndShowsMessage(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		RunAction: func(ctx context.Context, req ActionRequest) ActionOutcome {
			if req.Action != ActionCopy {
				t.Fatalf("action = %q, want %q", req.Action, ActionCopy)
			}
			if req.Entry.Path != "alpha" {
				t.Fatalf("entry = %q, want alpha", req.Entry.Path)
			}
			return ActionOutcome{Message: "copied"}
		},
	}, termstyle.TerminalTheme())

	updated, cmd := model.Update(ctrlKey('y'))
	got := updated.(pickerModel)
	if got.busy == nil {
		t.Fatal("busy = nil, want modal")
	}
	if got.action != ActionNone {
		t.Fatalf("action = %q, want none", got.action)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want action command")
	}

	msg := cmd()
	done, ok := msg.(actionDoneMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want actionDoneMsg", msg)
	}
	updated, _ = got.Update(done)
	got = updated.(pickerModel)

	if got.busy != nil {
		t.Fatal("busy still set after action completed")
	}
	if got.message != "copied" || got.messageErr {
		t.Fatalf("message = %q err=%v, want copied/false", got.message, got.messageErr)
	}
}

func TestPickerRunnerActionErrorReturnsToPicker(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		RunAction: func(context.Context, ActionRequest) ActionOutcome {
			return ActionOutcome{Err: errors.New("copy failed")}
		},
	}, termstyle.TerminalTheme())

	updated, cmd := model.Update(ctrlKey('y'))
	got := updated.(pickerModel)
	updated, _ = got.Update(cmd())
	got = updated.(pickerModel)

	if got.busy != nil {
		t.Fatal("busy still set after action failed")
	}
	if got.message != "copy failed" || !got.messageErr {
		t.Fatalf("message = %q err=%v, want failure/true", got.message, got.messageErr)
	}
}

func TestPickerBusyCancelReturnsImmediatelyAndIgnoresLateResult(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		RunAction: func(context.Context, ActionRequest) ActionOutcome {
			return ActionOutcome{Message: "late success"}
		},
	}, termstyle.TerminalTheme())

	updated, cmd := model.Update(ctrlKey('y'))
	got := updated.(pickerModel)
	if got.busy == nil || got.activeID == 0 {
		t.Fatal("action did not enter busy state")
	}

	updated, _ = got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	got = updated.(pickerModel)
	if got.busy != nil {
		t.Fatal("busy still set after cancel")
	}
	if got.activeID != 0 {
		t.Fatalf("activeID = %d, want 0", got.activeID)
	}
	if got.message != "Canceled alpha." || !got.messageErr {
		t.Fatalf("message = %q err=%v, want canceled/true", got.message, got.messageErr)
	}

	updated, _ = got.Update(cmd())
	got = updated.(pickerModel)
	if got.message != "Canceled alpha." {
		t.Fatalf("late result changed message to %q", got.message)
	}
}

func TestPickerBusyCtrlCQuitsInsteadOfCanceling(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		RunAction: func(context.Context, ActionRequest) ActionOutcome {
			return ActionOutcome{Message: "late success"}
		},
	}, termstyle.TerminalTheme())

	updated, _ := model.Update(ctrlKey('y'))
	got := updated.(pickerModel)
	updated, cmd := got.Update(ctrlKey('c'))
	got = updated.(pickerModel)

	if got.action != ActionQuit {
		t.Fatalf("action = %q, want %q", got.action, ActionQuit)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want quit command")
	}
}

func TestPickerCtrlOOpensThemeEditorAndSavesTheme(t *testing.T) {
	var saved ThemeEditorResult
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		ThemePath: "/tmp/theme.conf",
		SaveTheme: func(ctx context.Context, result ThemeEditorResult) (ThemeSaveResult, error) {
			saved = result
			return ThemeSaveResult{
				Config:  result.Config,
				Theme:   result.Theme,
				Path:    result.Path,
				Changed: true,
				Message: "saved theme",
			}, nil
		},
	}, termstyle.TerminalTheme())

	updated, _ := model.Update(ctrlKey('o'))
	got := updated.(pickerModel)
	if got.themeEditor == nil {
		t.Fatal("themeEditor = nil, want editor")
	}

	updated, _ = got.Update(themeKeyMsg("down")) // primary
	got = updated.(pickerModel)
	updated, _ = got.Update(themeKeyMsg("right")) // primary = default
	got = updated.(pickerModel)
	updated, cmd := got.Update(themeKeyMsg("s"))
	got = updated.(pickerModel)
	if got.themeEditor != nil {
		t.Fatal("themeEditor still open after save")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want save command")
	}

	updated, _ = got.Update(cmd())
	got = updated.(pickerModel)

	if saved.Path != "/tmp/theme.conf" {
		t.Fatalf("saved path = %q", saved.Path)
	}
	if got.message != "saved theme" || got.messageErr {
		t.Fatalf("message = %q err=%v, want saved theme/false", got.message, got.messageErr)
	}
	if got.themeConfig.Specs[termstyle.RolePrimary] != "default" {
		t.Fatalf("saved primary = %q, want default", got.themeConfig.Specs[termstyle.RolePrimary])
	}
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: r, Mod: tea.ModCtrl})
}
