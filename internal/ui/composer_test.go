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
	c = c.update("enter", "") // submit
	if !c.done || c.canceled {
		t.Fatalf("not submitted: done=%v canceled=%v", c.done, c.canceled)
	}
	res := c.result()
	if res.Path != "work/new" || res.Generate || string(res.Content) != "s3cret" {
		t.Fatalf("result = %#v", res)
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
