package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

func driftModel(t *testing.T, opts PickOptions) pickerModel {
	t.Helper()
	return newPickerModel([]passstore.Entry{
		{Path: "alpha", Display: "alpha"},
	}, opts, termstyle.TerminalTheme().WithNoColor(true))
}

// driftStatus is a tiny constructor so the tests read cleanly.
func driftStatus(kind passstore.DriftKind) passstore.DriftStatus {
	return passstore.DriftStatus{Kind: kind, Branch: "main"}
}

func TestDriftBehindOpensSyncConfirm(t *testing.T) {
	model := driftModel(t, PickOptions{})
	updated, _ := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftBehind)})
	got := updated.(pickerModel)
	if got.confirm == nil {
		t.Fatal("behind store should open a confirm")
	}
	if got.confirm.action != ActionPull {
		t.Fatalf("confirm action = %q, want pull", got.confirm.action)
	}
	if got.confirm.danger {
		t.Fatal("sync confirm must not be danger-styled (a pull is not destructive)")
	}
	if got.confirm.title != "sync" {
		t.Fatalf("confirm title = %q, want sync", got.confirm.title)
	}
	// Render and pin the box: header text, footer grammar, and width integrity.
	box := strings.Join(got.confirmLines(60, pickerTheme{theme: got.theme}), "\n")
	if !strings.Contains(box, "upstream changes") {
		t.Fatalf("confirm box missing headline:\n%s", box)
	}
	if !strings.Contains(box, "y confirm") || !strings.Contains(box, "esc cancel") {
		t.Fatalf("confirm box missing canonical footer:\n%s", box)
	}
	if !strings.Contains(box, "sync") {
		t.Fatalf("confirm box missing sync title:\n%s", box)
	}
	assertBorderIntegrity(t, "sync-confirm", box+"\n")
}

func TestDriftStaleShowsNotice(t *testing.T) {
	model := driftModel(t, PickOptions{})
	updated, _ := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftStale)})
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("stale store should not open a confirm")
	}
	if !strings.Contains(got.message, "unreachable") || got.messageErr {
		t.Fatalf("stale notice = %q (err=%v)", got.message, got.messageErr)
	}
}

func TestDriftLocalShowsNotice(t *testing.T) {
	model := driftModel(t, PickOptions{})
	updated, _ := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftLocal)})
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("local-only store should not open a confirm")
	}
	if !strings.Contains(got.message, "not yet pushed") {
		t.Fatalf("local notice = %q", got.message)
	}
}

func TestDriftCleanIsSilent(t *testing.T) {
	for _, kind := range []passstore.DriftKind{passstore.DriftClean, passstore.DriftNoRemote, passstore.DriftUnknown} {
		model := driftModel(t, PickOptions{})
		updated, _ := model.Update(driftCheckedMsg{status: driftStatus(kind)})
		got := updated.(pickerModel)
		if got.confirm != nil || got.message != "" {
			t.Fatalf("kind %v should be silent: confirm=%v message=%q", kind, got.confirm, got.message)
		}
	}
}

func TestDriftDoesNotClobberOpenOverlay(t *testing.T) {
	model := driftModel(t, PickOptions{})
	model.help = true // user already opened the key reference
	updated, _ := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftBehind)})
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("drift must not pop a confirm over an active overlay")
	}
	if !got.help {
		t.Fatal("drift must not close the active overlay")
	}
}

func TestSyncConfirmYStartsPull(t *testing.T) {
	called := make(chan ActionRequest, 1)
	model := driftModel(t, PickOptions{
		RunAction: func(_ context.Context, req ActionRequest) ActionOutcome {
			called <- req
			return ActionOutcome{Message: "Pulled latest from remote."}
		},
	})
	model = model.startConfirm(ActionPull)
	updated, cmd := model.Update(tea.KeyPressMsg(tea.Key{Text: "y"}))
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("y should clear the confirm")
	}
	if got.busy == nil || got.busy.title != "pulling from remote" {
		t.Fatalf("y should start the pull busy box, got %+v", got.busy)
	}
	if got.busy.detail != "" {
		t.Fatalf("pull busy detail = %q, want empty (store-wide action)", got.busy.detail)
	}
	if cmd == nil {
		t.Fatal("y should dispatch the pull command")
	}
}

func TestDriftBehindWhileTypingShowsNoticeNotConfirm(t *testing.T) {
	model := driftModel(t, PickOptions{})
	// The user starts typing into the filter before the async check resolves.
	typed, _ := model.Update(tea.KeyPressMsg(tea.Key{Text: "a"}))
	model = typed.(pickerModel)
	updated, _ := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftBehind)})
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("drift must not grab focus with a confirm while the user is typing")
	}
	if !strings.Contains(got.message, "new entries") {
		t.Fatalf("expected a behind notice while typing, got %q", got.message)
	}
}

func TestDriftNoticeStartsTickToFade(t *testing.T) {
	model := driftModel(t, PickOptions{})
	_, cmd := model.Update(driftCheckedMsg{status: driftStatus(passstore.DriftStale)})
	if cmd == nil {
		t.Fatal("a transient drift notice must start the tick loop so it fades on its TTL")
	}
}

func TestSyncConfirmEscCancels(t *testing.T) {
	model := driftModel(t, PickOptions{})
	model = model.startConfirm(ActionPull)
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	got := updated.(pickerModel)
	if got.confirm != nil {
		t.Fatal("esc should cancel the sync confirm")
	}
	if got.action != ActionNone {
		t.Fatalf("esc must not act, action = %q", got.action)
	}
}
