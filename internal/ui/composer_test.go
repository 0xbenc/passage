package ui

import (
	"strings"
	"testing"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

func feed(f textField, s string) textField {
	for _, r := range s {
		f = f.update("", string(r))
	}
	return f
}

func typeComposer(c composerModel, s string) composerModel {
	for _, r := range s {
		c = c.update("", string(r))
	}
	return c
}

func TestTextFieldInsertBackspaceMask(t *testing.T) {
	f := feed(textField{}, "pass")
	if f.String() != "pass" {
		t.Fatalf("value = %q", f.String())
	}
	f = f.update("backspace", "")
	if f.String() != "pas" {
		t.Fatalf("after backspace = %q", f.String())
	}
	f = f.update("ctrl+u", "")
	if f.String() != "" {
		t.Fatalf("after ctrl+u = %q", f.String())
	}
	masked := feed(textField{masked: true}, "ab")
	rendered := masked.render(40)
	if strings.Contains(rendered, "a") || strings.Contains(rendered, "b") {
		t.Fatalf("masked render leaked plaintext: %q", rendered)
	}
	if !strings.Contains(rendered, "•") {
		t.Fatalf("masked render = %q, want bullets", rendered)
	}
}

func TestComposerPasswordFlow(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "") // advance to secret
	if c.step != stepSecret {
		t.Fatalf("step = %v, want secret", c.step)
	}
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "") // advance to confirm (not submit)
	if c.step != stepConfirm || c.done {
		t.Fatalf("first enter should advance to confirm: step=%v done=%v", c.step, c.done)
	}
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "") // submit
	if !c.done || c.canceled {
		t.Fatalf("not submitted: done=%v canceled=%v", c.done, c.canceled)
	}
	res := c.result()
	if res.Path != "work/new" || res.Generate || string(res.Content) != "s3cret" {
		t.Fatalf("result = %#v", res)
	}
}

func TestComposerSecretMustMatch(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "")
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "") // -> confirm
	c = typeComposer(c, "WRONG")
	c = c.update("enter", "") // mismatch
	if c.done {
		t.Fatalf("mismatch should not submit")
	}
	if c.step != stepSecret || !c.mismatch {
		t.Fatalf("mismatch should reset to secret with flag: step=%v mismatch=%v", c.step, c.mismatch)
	}
	if c.secret.String() != "" || c.confirm.String() != "" {
		t.Fatalf("mismatch should clear both fields: secret=%q confirm=%q", c.secret.String(), c.confirm.String())
	}
	if out := termstyle.Strip(strings.Join(c.render(60, pickerTheme{theme: termstyle.TerminalTheme()}), "\n")); !strings.Contains(out, "did not match") {
		t.Fatalf("mismatch warning not rendered:\n%s", out)
	}
	// Re-enter matching secrets; the second attempt succeeds.
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "")
	if c.mismatch {
		t.Fatalf("advancing to confirm should clear the mismatch flag")
	}
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "")
	if !c.done || string(c.result().Content) != "s3cret" {
		t.Fatalf("retry did not submit: done=%v content=%q", c.done, c.result().Content)
	}
}

func TestComposerEmptySecretStaysOnStep(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "") // -> secret
	c = c.update("enter", "") // empty secret: must not advance
	if c.step != stepSecret || c.done {
		t.Fatalf("empty secret advanced: step=%v done=%v", c.step, c.done)
	}
}

func TestComposerRevealToggle(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "") // -> secret
	c = typeComposer(c, "abc")
	if !c.secret.masked || c.revealLabel() != "show" {
		t.Fatalf("secret should start masked: masked=%v label=%q", c.secret.masked, c.revealLabel())
	}
	out := termstyle.Strip(strings.Join(c.render(60, pickerTheme{theme: termstyle.TerminalTheme()}), "\n"))
	if strings.Contains(out, "abc") {
		t.Fatalf("masked render leaked plaintext:\n%s", out)
	}
	c = c.update("ctrl+r", "") // reveal
	if c.secret.masked || c.confirm.masked || c.revealLabel() != "hide" {
		t.Fatalf("ctrl+r should reveal both fields: secret=%v confirm=%v label=%q", c.secret.masked, c.confirm.masked, c.revealLabel())
	}
	out = termstyle.Strip(strings.Join(c.render(60, pickerTheme{theme: termstyle.TerminalTheme()}), "\n"))
	if !strings.Contains(out, "abc") {
		t.Fatalf("revealed render should show plaintext:\n%s", out)
	}
	// Visibility carries into the confirm step.
	c = c.update("enter", "")
	if c.step != stepConfirm || c.confirm.masked {
		t.Fatalf("reveal should persist into confirm: step=%v confirm.masked=%v", c.step, c.confirm.masked)
	}
	// ^R must keep toggling once on the confirm step.
	c = c.update("ctrl+r", "") // hide both again
	if !c.secret.masked || !c.confirm.masked || c.revealLabel() != "show" {
		t.Fatalf("ctrl+r on confirm should re-mask both: secret=%v confirm=%v label=%q", c.secret.masked, c.confirm.masked, c.revealLabel())
	}
	c = c.update("ctrl+r", "") // reveal both again
	if c.secret.masked || c.confirm.masked || c.revealLabel() != "hide" {
		t.Fatalf("ctrl+r on confirm should reveal both: secret=%v confirm=%v label=%q", c.secret.masked, c.confirm.masked, c.revealLabel())
	}
}

// TestComposerConfirmRendersSettledSecret pins the confirm-step rendering: the
// first entry is shown as settled context via displayString (the only caller),
// masked by default and plaintext only when revealed. A distinct confirm value
// proves the context line reflects the secret, not the confirm field.
func TestComposerConfirmRendersSettledSecret(t *testing.T) {
	theme := pickerTheme{theme: termstyle.TerminalTheme()}
	c := newComposer(composePassword)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "") // -> secret
	c = typeComposer(c, "s3cret")
	c = c.update("enter", "") // -> confirm
	if c.step != stepConfirm {
		t.Fatalf("step = %v, want confirm", c.step)
	}
	c = typeComposer(c, "zzz") // a different confirm value

	masked := termstyle.Strip(strings.Join(c.render(60, theme), "\n"))
	if strings.Contains(masked, "s3cret") || strings.Contains(masked, "zzz") {
		t.Fatalf("confirm screen leaked plaintext while masked:\n%s", masked)
	}
	if !strings.Contains(masked, "•") {
		t.Fatalf("confirm screen should show masked bullets:\n%s", masked)
	}

	c = c.update("ctrl+r", "") // reveal
	shown := termstyle.Strip(strings.Join(c.render(60, theme), "\n"))
	if !strings.Contains(shown, "s3cret") {
		t.Fatalf("revealed confirm screen should show the settled secret:\n%s", shown)
	}
	if !strings.Contains(shown, "zzz") {
		t.Fatalf("revealed confirm screen should show the confirm field:\n%s", shown)
	}
}

// TestComposerGenerateFromPath locks the stepPath->stepLength jump for a
// composer opened directly in generate mode (the picker's "G" action), so the
// stepConfirm enum insertion can't reroute it through the typed-secret steps.
func TestComposerGenerateFromPath(t *testing.T) {
	c := newComposer(composeGenerate)
	c = typeComposer(c, "work/gen")
	c = c.update("enter", "") // path -> length, skipping secret/confirm
	if c.step != stepLength {
		t.Fatalf("generate path enter: step=%v, want stepLength", c.step)
	}
	c = c.update("enter", "") // make
	res := c.result()
	if !res.Generate || res.Path != "work/gen" || res.Length != composerDefaultLength {
		t.Fatalf("generate result = %#v", res)
	}
}

func TestComposerGenerateFlowAndSwitch(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "work/gen")
	c = c.update("enter", "")  // to secret
	c = c.update("ctrl+g", "") // switch to generate
	if c.mode != composeGenerate || c.step != stepLength {
		t.Fatalf("after ctrl+g: mode=%v step=%v", c.mode, c.step)
	}
	c = c.update("up", "") // length++
	c = c.update("", "s")  // toggle symbols off
	c = c.update("enter", "")
	res := c.result()
	if !res.Generate || res.Path != "work/gen" || res.Length != composerDefaultLength+1 || !res.NoSymbols {
		t.Fatalf("generate result = %#v", res)
	}
}

func TestComposerCancel(t *testing.T) {
	c := newComposer(composePassword)
	c = typeComposer(c, "x")
	c = c.update("esc", "")
	if !c.done || !c.canceled {
		t.Fatalf("esc did not cancel: %#v", c)
	}
}

func TestComposerEmptyPathStaysOnStep(t *testing.T) {
	c := newComposer(composePassword)
	c = c.update("enter", "") // no path yet
	if c.step != stepPath || c.done {
		t.Fatalf("empty path advanced: step=%v done=%v", c.step, c.done)
	}
}

func TestPickerRendersReadOnlyBadge(t *testing.T) {
	model := newPickerModel([]passstore.Entry{
		{Path: "work/aws/db", Display: passstore.Display("work/aws/db")},
	}, PickOptions{}, termstyle.TerminalTheme())
	model.width = 100
	model.height = 24
	model.access = map[string]string{"work/aws/db": "read_only"}
	out := termstyle.Strip(model.View().Content)
	if !strings.Contains(out, "RO") {
		t.Fatalf("read-only badge not rendered:\n%s", out)
	}
}
