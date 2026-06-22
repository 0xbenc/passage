package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

func manyEntries(n int) []passstore.Entry {
	entries := make([]passstore.Entry, n)
	for i := range entries {
		name := fmt.Sprintf("entry-%03d", i)
		entries[i] = passstore.Entry{Path: name, Display: name}
	}
	return entries
}

// TestPickerPageSizeTracksListHeight pins the C6 invariant: pageSize is the
// real rendered list height. A message (which adds two header rows) shrinks
// the page by exactly two — the drift the old estimate ignored.
func TestPickerPageSizeTracksListHeight(t *testing.T) {
	model := newPickerModel(manyEntries(50), PickOptions{}, termstyle.TerminalTheme())
	model.width = 80
	model.height = 20

	wantPage := 20 - pickerShellStructuralLines(pickerFooterText()) - 2
	if got := model.pageSize(); got != wantPage {
		t.Fatalf("pageSize = %d, want %d", got, wantPage)
	}

	model.message = "copied work/github"
	if got := model.pageSize(); got != wantPage-2 {
		t.Fatalf("pageSize with message = %d, want %d (message must cost two rows)", got, wantPage-2)
	}
}

// TestPickerScrollKeepsCursorVisible drives ensureVisible across the list and
// asserts the cursor stays inside the scroll window and the rendered list
// never exceeds the page budget.
func TestPickerScrollKeepsCursorVisible(t *testing.T) {
	model := newPickerModel(manyEntries(50), PickOptions{}, termstyle.TerminalTheme())
	model.width = 80
	model.height = 20

	theme := pickerTheme{theme: model.theme}
	for _, cursor := range []int{0, 7, 25, 49, 12} {
		model.cursor = cursor
		model.ensureVisible()
		page := model.pageSize()
		if model.cursor < model.scroll || model.cursor >= model.scroll+page {
			t.Fatalf("cursor %d not within scroll window [%d,%d)", model.cursor, model.scroll, model.scroll+page)
		}
		lines := model.listLines(model.computeLayout(theme).bodyWidth, theme, page)
		if len(lines) > page {
			t.Fatalf("cursor %d: rendered %d list lines, exceeds page %d", cursor, len(lines), page)
		}
	}
}

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
	if len(got.filtered) != 1 || got.entries[got.filtered[0].Index].Path != "beta" {
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
		"? keys",
		"╰",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("view = %q, want substring %q", text, want)
		}
	}
}

func TestFilterCursorTracksQueryAndHidesOverModal(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{Glyphs: termstyle.ASCIIGlyphs()}, termstyle.TerminalTheme())
	model.width = 80
	model.height = 20

	// Empty query: beam sits just after the "/" label (X = 3), on the filter
	// row (Y = 2 with no message).
	c := model.View().Cursor
	if c == nil {
		t.Fatal("filter cursor should be present")
	}
	if c.X != 3 || c.Y != 2 {
		t.Fatalf("empty-query cursor = (%d,%d), want (3,2)", c.X, c.Y)
	}
	if c.Shape != tea.CursorBar {
		t.Fatal("filter cursor should be a beam")
	}

	// Typing advances the cursor by the query width.
	for _, ch := range []string{"a", "l"} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: ch}))
		model = updated.(pickerModel)
	}
	if c := model.View().Cursor; c == nil || c.X != 5 {
		t.Fatalf("after typing 'al', cursor X = %v, want 5", c)
	}

	// A message pushes the filter row (and cursor) down by two.
	model.message = "copied"
	if c := model.View().Cursor; c == nil || c.Y != 4 {
		t.Fatalf("cursor Y with message = %v, want 4", c)
	}

	// Over a secret modal the cursor is suppressed.
	model.applyOutcome(0, ActionOutcome{SecretKind: "totp", Secret: "123 456", SecretRemaining: 8, SecretPeriod: 30})
	if c := model.View().Cursor; c != nil {
		t.Fatal("cursor must be nil while a secret modal is open")
	}
}

// TestNoticeDecaysAfterTTL pins G4: a transient notice keeps the tick alive
// and fades once its time-to-live elapses.
func TestNoticeDecaysAfterTTL(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme())
	base := time.Unix(9000, 0)
	cur := base
	model.clock = func() time.Time { return cur }

	model.applyOutcome(0, ActionOutcome{Message: "copied"})
	if model.message != "copied" {
		t.Fatal("notice should be set")
	}
	if !model.tickActive() {
		t.Fatal("a live notice should keep the tick alive so it can decay")
	}

	cur = base.Add(3 * time.Second)
	updated, _ := model.Update(tickMsg{})
	model = updated.(pickerModel)
	if model.message != "copied" {
		t.Fatal("notice should persist before its TTL")
	}

	cur = base.Add(7 * time.Second)
	updated, _ = model.Update(tickMsg{})
	model = updated.(pickerModel)
	if model.message != "" {
		t.Fatalf("notice should fade after its TTL, got %q", model.message)
	}
}

func TestMouseWheelMovesCursor(t *testing.T) {
	model := newPickerModel(manyEntries(30), PickOptions{}, termstyle.TerminalTheme())
	model.width = 80
	model.height = 20
	model.cursor = 5

	down, _ := model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if got := down.(pickerModel).cursor; got != 6 {
		t.Fatalf("wheel down cursor = %d, want 6", got)
	}
	up, _ := down.(pickerModel).Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelUp}))
	if got := up.(pickerModel).cursor; got != 5 {
		t.Fatalf("wheel up cursor = %d, want 5", got)
	}
}

func TestMouseClickSelectsRow(t *testing.T) {
	model := newPickerModel(manyEntries(30), PickOptions{}, termstyle.TerminalTheme())
	model.width = 80
	model.height = 24
	model.cursor = 0
	// With no message, the first entry row is at Y = 1 (border) + 2 (header).
	// Clicking three rows down selects entry index 3.
	clicked, _ := model.Update(tea.MouseClickMsg(tea.Mouse{Button: tea.MouseLeft, Y: 1 + 2 + 3}))
	if got := clicked.(pickerModel).cursor; got != 3 {
		t.Fatalf("click selected cursor = %d, want 3", got)
	}
}

func TestMouseSuppressedDuringOverlay(t *testing.T) {
	model := newPickerModel(manyEntries(30), PickOptions{}, termstyle.TerminalTheme())
	model.cursor = 5
	model.confirm = &pickerConfirm{action: ActionClearPins, prompt: "Clear all pins?"}
	moved, _ := model.Update(tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}))
	if got := moved.(pickerModel).cursor; got != 5 {
		t.Fatalf("mouse must be ignored during an overlay; cursor = %d, want 5", got)
	}
}

func TestQuestionMarkOpensHelpOverlay(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 72
	model.height = 24

	// "?" with an empty filter opens the overlay.
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "?"}))
	got := updated.(pickerModel)
	if !got.help {
		t.Fatal("\"?\" with empty filter should open the help overlay")
	}
	text := termstyle.Strip(got.View().Content)
	for _, want := range []string{"NAVIGATE", "ACTIONS", "clear pins", "theme editor"} {
		if !strings.Contains(text, want) {
			t.Fatalf("help overlay missing %q:\n%s", want, text)
		}
	}

	// Any key closes it.
	closed, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	if closed.(pickerModel).help {
		t.Fatal("a key should close the help overlay")
	}
}

func TestQuestionMarkFiltersWhenQueryNonEmpty(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "a?b", Display: "a?b"},
	}, PickOptions{}, termstyle.TerminalTheme())
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "a"}))
	model = updated.(pickerModel)
	updated, _ = model.Update(tea.KeyPressMsg(tea.Key{Text: "?"}))
	got := updated.(pickerModel)
	if got.help {
		t.Fatal("\"?\" with a non-empty filter must type into the filter, not open help")
	}
	if got.query != "a?" {
		t.Fatalf("query = %q, want \"a?\"", got.query)
	}
}

func TestPickerFuzzyFilterCrossesDelimiter(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/token", Display: passstore.Display("work/github/token")},
		{Path: "work/aws/key", Display: passstore.Display("work/aws/key")},
	}, PickOptions{}, termstyle.TerminalTheme())

	for _, ch := range []string{"g", "h"} {
		updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: ch}))
		model = updated.(pickerModel)
	}

	if len(model.filtered) != 1 || model.entries[model.filtered[0].Index].Path != "work/github/token" {
		t.Fatalf("fuzzy gh filtered = %#v, want only github", model.filtered)
	}
}

func TestHighlightTitleStylesMatchedRunes(t *testing.T) {
	theme := pickerTheme{theme: termstyle.TerminalTheme()}
	// "work | github": g at rune 7, h at rune 10.
	title := highlightTitle("work | github", []int{7, 10}, 40, theme.primary, theme.search)

	if got := termstyle.Strip(title); got != "work | github" {
		t.Fatalf("Strip(title) = %q, want plain title", got)
	}
	searchOpen := "\x1b[1;39m" // RoleSearch in TerminalTheme
	if !strings.Contains(title, searchOpen+"g") {
		t.Fatalf("title does not highlight 'g' with RoleSearch: %q", title)
	}
	if !strings.Contains(title, searchOpen+"h") {
		t.Fatalf("title does not highlight 'h' with RoleSearch: %q", title)
	}
	if termstyle.VisibleWidth(title) != termstyle.VisibleWidth("work | github") {
		t.Fatalf("highlighted width %d != plain width", termstyle.VisibleWidth(title))
	}
}

func TestHighlightTitleClipsPositionsPastTruncation(t *testing.T) {
	theme := pickerTheme{theme: termstyle.TerminalTheme()}
	// Width 8 keeps "work | " (7 cells) + "~"; the g/h at 7/10 are cut and
	// must not be highlighted (nor index out of range).
	title := highlightTitle("work | github", []int{7, 10}, 8, theme.primary, theme.search)
	if got := termstyle.Strip(title); got != "work | ~" {
		t.Fatalf("Strip(title) = %q, want \"work | ~\"", got)
	}
	if strings.Contains(title, "\x1b[1;39m") {
		t.Fatalf("truncated-away matches must not be highlighted: %q", title)
	}
	if termstyle.VisibleWidth(title) > 8 {
		t.Fatalf("title width %d exceeds 8", termstyle.VisibleWidth(title))
	}
}

func TestEmptyVaultShowsWelcome(t *testing.T) {
	model := newPickerModel(nil, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 70
	model.height = 16
	text := termstyle.Strip(model.View().Content)
	if !strings.Contains(text, "store is empty") {
		t.Fatalf("empty vault should show a welcome:\n%s", text)
	}
	if !strings.Contains(text, "pass insert") {
		t.Fatalf("welcome should show the next step:\n%s", text)
	}
}

func TestZeroMatchEchoesSanitizedQuery(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 70
	model.height = 16
	// A query with an embedded control byte must be sanitized before it is
	// echoed back into the empty-state.
	model.query = "zzz\x1b[31mX"
	model.applyFilter()
	text := model.View().Content
	if strings.Contains(text, "\x1b[31m") {
		t.Fatalf("zero-match echo must sanitize control bytes from the query:\n%q", text)
	}
	if !strings.Contains(text, "No entries match") {
		t.Fatalf("zero-match should report no matches:\n%s", text)
	}
}

func TestEmptyStateClampedToBudget(t *testing.T) {
	model := newPickerModel(nil, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	theme := pickerTheme{theme: model.theme}
	lines := model.listLines(60, theme, 2)
	if len(lines) > 2 {
		t.Fatalf("empty state must clamp to the available budget, got %d lines", len(lines))
	}
}

func TestSelectedRowIsFilledBar(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github", Display: passstore.Display("work/github"), Pinned: true, HasMFA: true, LastUsed: 1},
	}, PickOptions{}, termstyle.VividTheme())
	model.cursor = 0
	theme := pickerTheme{theme: model.theme}

	line := model.renderEntryLine(model.entries[0], model.filtered[0].Positions, 0, 60, 12, theme)
	barCode := "\x1b[48;2;45;55;72" // RoleSelectedBar background in VividTheme

	// The selection bar must cover the metadata column too, not just the
	// title: the last styled segment (the timestamp) still carries the bar.
	if !strings.Contains(line, barCode) {
		t.Fatalf("selected row missing bar background: %q", line)
	}
	last := strings.LastIndex(line, barCode)
	if reset := strings.LastIndex(line, "\x1b[0m"); reset < last {
		t.Fatalf("bar background does not extend to the end of the row: %q", line)
	}
	// Markers render as tags.
	if !strings.Contains(termstyle.Strip(line), "PIN MFA") {
		t.Fatalf("selected row missing PIN/MFA tags: %q", termstyle.Strip(line))
	}
}

func TestSelectedRowNoColorUsesCaret(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.cursor = 0
	theme := pickerTheme{theme: model.theme}
	line := model.renderEntryLine(model.entries[0], nil, 0, 60, 12, theme)
	if strings.Contains(line, "\x1b[") {
		t.Fatalf("NoColor selected row must emit no escapes: %q", line)
	}
	if !strings.HasPrefix(line, "> ") {
		t.Fatalf("NoColor selected row must lead with the caret: %q", line)
	}
}

func TestPickerDetailPaneShowsPathWithoutDecrypting(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/token", Display: passstore.Display("work/github/token"), Pinned: true, HasMFA: true},
	}, PickOptions{
		NoColor: true,
		RunAction: func(context.Context, ActionRequest) ActionOutcome {
			t.Fatal("detail pane must never run an action (no decrypt)")
			return ActionOutcome{}
		},
	}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 100
	model.height = 24

	text := model.View().Content
	if !strings.Contains(text, "work/github/token") {
		t.Fatalf("wide view should show the full path in the detail pane:\n%s", text)
	}
	if !strings.Contains(text, "pinned") {
		t.Fatalf("detail pane should show pin state:\n%s", text)
	}
}

func TestPickerDetailPaneHiddenWhenNarrow(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/token", Display: passstore.Display("work/github/token")},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 80 // below the 92-col breakpoint
	model.height = 24

	text := model.View().Content
	if strings.Contains(text, "work/github/token") {
		t.Fatalf("narrow view must not render the slashed full path (no detail pane):\n%s", text)
	}
}

func TestPickerWideRowsFillInnerWidthExactly(t *testing.T) {
	model := newPickerModel(manyEntries(20), PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 110
	model.height = 24

	text := model.View().Content
	// Every framed row "│ <inner> │" must have inner width == width-4 so the
	// two-column join never desynced the right border.
	inner := model.width - 4
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if !strings.HasPrefix(line, "│ ") || !strings.HasSuffix(line, " │") {
			continue
		}
		content := strings.TrimSuffix(strings.TrimPrefix(line, "│ "), " │")
		if w := termstyle.VisibleWidth(content); w != inner {
			t.Fatalf("framed row inner width = %d, want %d: %q", w, inner, line)
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
	if len(got.filtered) != 1 || got.entries[got.filtered[0].Index].Path != "alpha" {
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

	done, ok := actionDoneFromCmd(cmd)
	if !ok {
		t.Fatalf("cmd did not yield an actionDoneMsg")
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
	done, _ := actionDoneFromCmd(cmd)
	updated, _ = got.Update(done)
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

	lateDone, _ := actionDoneFromCmd(cmd)
	updated, _ = got.Update(lateDone)
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

	updated, _ = got.Update(themeKeyMsg("down")) // base -> title
	got = updated.(pickerModel)
	updated, _ = got.Update(themeKeyMsg("down")) // title -> primary
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

// actionDoneFromCmd extracts the actionDoneMsg from a start-action command,
// which is batched with the (blocking) tick command. The action command is
// always first in the batch, so this returns without waiting on the tick.
func actionDoneFromCmd(cmd tea.Cmd) (actionDoneMsg, bool) {
	if cmd == nil {
		return actionDoneMsg{}, false
	}
	switch msg := cmd().(type) {
	case actionDoneMsg:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if c == nil {
				continue
			}
			if done, ok := c().(actionDoneMsg); ok {
				return done, true
			}
		}
	}
	return actionDoneMsg{}, false
}

// TestBusyBoxShowsSpinnerAndElapsed pins C12: the busy box animates a spinner
// frame and shows elapsed time derived from the start clock.
func TestBusyBoxShowsSpinnerAndElapsed(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{Glyphs: termstyle.ASCIIGlyphs()}, termstyle.TerminalTheme().WithNoColor(true))
	base := time.Unix(1000, 0)
	model.clock = func() time.Time { return base.Add(3 * time.Second) }
	model.busy = &pickerBusy{title: "copying", detail: "alpha", started: base}

	joined := strings.Join(model.busyLines(60, pickerTheme{theme: model.theme}), "\n")
	if !strings.Contains(joined, "copying alpha") {
		t.Fatalf("busy box missing title:\n%s", joined)
	}
	if !strings.Contains(joined, "3s elapsed") {
		t.Fatalf("busy box missing elapsed:\n%s", joined)
	}
	if frame := model.glyphs.Frame(model.tick); !strings.Contains(joined, frame) {
		t.Fatalf("busy box missing spinner frame %q:\n%s", frame, joined)
	}
}

// TestTotpCountdownRecomputesAndAutoDismisses pins C10: a TOTP secret modal
// recomputes remaining from the wall clock on each tick and dismisses itself
// when the window elapses; a password reveal gets no countdown.
func TestTotpCountdownRecomputesAndAutoDismisses(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/mfa", Display: "work | github | mfa", HasMFA: true},
	}, PickOptions{Glyphs: termstyle.ASCIIGlyphs()}, termstyle.TerminalTheme().WithNoColor(true))
	base := time.Unix(2000, 0)
	cur := base
	model.clock = func() time.Time { return cur }

	model.applyOutcome(0, ActionOutcome{
		SecretTitle: "TOTP", SecretKind: "totp", Secret: "123 456",
		SecretRemaining: 8, SecretPeriod: 30,
	})
	if model.modal == nil || model.modal.expires.IsZero() {
		t.Fatal("totp outcome should create a live countdown modal")
	}
	if model.modal.remaining != 8 {
		t.Fatalf("initial remaining = %d, want 8", model.modal.remaining)
	}

	// 5s later, remaining recomputes to ~3.
	cur = base.Add(5 * time.Second)
	model.refreshSecretCountdown()
	if model.modal == nil || model.modal.remaining != 3 {
		t.Fatalf("remaining after 5s = %v, want 3", model.modal)
	}

	// Past expiry, the modal auto-dismisses.
	cur = base.Add(9 * time.Second)
	model.refreshSecretCountdown()
	if model.modal != nil {
		t.Fatal("modal should auto-dismiss when the TOTP window elapses")
	}
}

func TestPasswordRevealHasNoCountdown(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.applyOutcome(0, ActionOutcome{
		SecretKind: "password", Secret: "hunter2", SecretRemaining: 0,
	})
	if model.modal == nil {
		t.Fatal("password reveal should create a modal")
	}
	if !model.modal.expires.IsZero() {
		t.Fatal("password reveal must not have a countdown")
	}
}

// TestDestructiveClearsRequireConfirm pins C15: ^U/^E open a confirm strip
// instead of wiping curated state, 'y' runs the action, esc cancels.
func TestDestructiveClearsRequireConfirm(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    tea.KeyPressMsg
		action Action
	}{
		{"clear pins", ctrlKey('u'), ActionClearPins},
		{"clear recents", ctrlKey('e'), ActionClearRecents},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := newPickerModel([]passstore.Entry{
				{Path: "alpha", Display: "alpha", Pinned: true},
			}, PickOptions{}, termstyle.TerminalTheme())

			updated, _ := model.Update(tc.key)
			got := updated.(pickerModel)
			if got.confirm == nil {
				t.Fatal("destructive clear should open a confirm strip")
			}
			if got.action != ActionNone {
				t.Fatalf("action = %q before confirm, want none", got.action)
			}

			// esc cancels without acting.
			cancelled, _ := got.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
			gotc := cancelled.(pickerModel)
			if gotc.confirm != nil || gotc.action != ActionNone {
				t.Fatalf("esc should cancel: confirm=%v action=%q", gotc.confirm, gotc.action)
			}

			// y confirms and runs the action (headless: sets action + quits).
			updated, _ = got.Update(tea.KeyPressMsg(tea.Key{Text: "y"}))
			gy := updated.(pickerModel)
			if gy.confirm != nil {
				t.Fatal("confirm should be cleared after y")
			}
			if gy.action != tc.action {
				t.Fatalf("action after confirm = %q, want %q", gy.action, tc.action)
			}
		})
	}
}

// TestClipboardArmsAndAutoClears pins C14: a copy arms a countdown pill, the
// pill counts down from the wall clock, and the clipboard auto-clears at zero
// via the quiet clear func.
func TestClipboardArmsAndAutoClears(t *testing.T) {
	cleared := false
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{
		ClearClipboard: func(context.Context) error { cleared = true; return nil },
	}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 80
	model.height = 18
	base := time.Unix(5000, 0)
	cur := base
	model.clock = func() time.Time { return cur }

	model.applyOutcome(0, ActionOutcome{Message: "copied", ClipArmed: true, ClipRemaining: 45, ClipTool: "wl-copy"})
	if model.clip == nil {
		t.Fatal("copy should arm the clipboard pill")
	}
	if !strings.Contains(model.View().Content, "clip clears in ~45s") {
		t.Fatalf("status should show the armed pill:\n%s", model.View().Content)
	}

	// Halfway: pill counts down, no clear yet.
	cur = base.Add(20 * time.Second)
	updated, _ := model.Update(tickMsg{})
	model = updated.(pickerModel)
	if cleared {
		t.Fatal("clipboard cleared too early")
	}
	if model.clip == nil || model.clipRemaining() != 25 {
		t.Fatalf("pill should show ~25s, got %v", model.clipRemaining())
	}

	// Past expiry: auto-clear fires and disarms.
	cur = base.Add(46 * time.Second)
	updated, cmd := model.Update(tickMsg{})
	model = updated.(pickerModel)
	if model.clip != nil {
		t.Fatal("clip should disarm at expiry")
	}
	if cmd == nil {
		t.Fatal("expiry should issue the clear command")
	}
	cmd() // run the clear command
	if !cleared {
		t.Fatal("clipboard should auto-clear at zero")
	}
}

func TestManualClearDisarmsClipPill(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme().WithNoColor(true))
	model.clip = &clipState{tool: "xclip", expires: model.now().Add(time.Minute)}

	updated, _ := model.Update(ctrlKey('x'))
	got := updated.(pickerModel)
	if got.clip != nil {
		t.Fatal("manual ^X clear should disarm the auto-clear pill")
	}
}

// TestSecretModalHidesOnBlur pins C13: losing terminal focus blanks a revealed
// secret in the picker, and regaining focus shows it again.
func TestSecretModalHidesOnBlur(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/github/mfa", Display: "work | github | mfa", HasMFA: true},
	}, PickOptions{Glyphs: termstyle.ASCIIGlyphs()}, termstyle.TerminalTheme().WithNoColor(true))
	model.width = 70
	model.height = 18
	model.applyOutcome(0, ActionOutcome{SecretTitle: "TOTP", SecretKind: "totp", Secret: "482 915", SecretRemaining: 8, SecretPeriod: 30})

	if !strings.Contains(model.View().Content, "482 915") {
		t.Fatal("secret should be visible before blur")
	}

	blurred, _ := model.Update(tea.BlurMsg{})
	model = blurred.(pickerModel)
	if strings.Contains(model.View().Content, "482 915") {
		t.Fatal("secret must be blanked while the terminal is unfocused")
	}
	if !strings.Contains(model.View().Content, "hidden") {
		t.Fatal("blanked secret should show a hidden notice")
	}

	focused, _ := model.Update(tea.FocusMsg{})
	model = focused.(pickerModel)
	if !strings.Contains(model.View().Content, "482 915") {
		t.Fatal("secret should reappear when focus returns")
	}
}

func TestRevealRedactsBeforeQuit(t *testing.T) {
	m := revealModel{title: "Reveal", secret: "hunter2", kind: "password", width: 70}
	if !strings.Contains(m.View().Content, "hunter2") {
		t.Fatal("secret should be visible initially")
	}
	updated, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if cmd == nil {
		t.Fatal("a key should quit the reveal")
	}
	rm := updated.(revealModel)
	if strings.Contains(rm.View().Content, "hunter2") {
		t.Fatal("final frame must not contain the secret (redacted before quit)")
	}
	if !strings.Contains(rm.View().Content, "cleared") {
		t.Fatal("redacted frame should show a cleared notice")
	}
}

// TestPickerTickGatedToLiveState pins the C9 contract: a tickMsg keeps the
// loop alive only while a busy action or live secret countdown is on screen,
// and stops (returns a nil command) the instant neither is.
func TestPickerTickGatedToLiveState(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, PickOptions{}, termstyle.TerminalTheme())

	// Idle: a stray tick must not perpetuate the loop.
	updated, cmd := model.Update(tickMsg{})
	if cmd != nil {
		t.Fatal("idle tick returned a command; loop must stop when nothing is live")
	}
	got := updated.(pickerModel)
	if got.ticking {
		t.Fatal("idle tick left ticking set")
	}

	// Busy: tick re-issues and advances the frame.
	got.busy = &pickerBusy{title: "copying"}
	updated, cmd = got.Update(tickMsg{})
	got = updated.(pickerModel)
	if cmd == nil {
		t.Fatal("busy tick must re-issue the clock")
	}
	if got.tick == 0 {
		t.Fatal("busy tick must advance the frame counter")
	}
}
