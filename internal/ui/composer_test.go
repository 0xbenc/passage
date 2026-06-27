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
	c := newComposer(composePassword, nil)
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
	c := newComposer(composePassword, nil)
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
	c := newComposer(composePassword, nil)
	c = typeComposer(c, "work/new")
	c = c.update("enter", "") // -> secret
	c = c.update("enter", "") // empty secret: must not advance
	if c.step != stepSecret || c.done {
		t.Fatalf("empty secret advanced: step=%v done=%v", c.step, c.done)
	}
}

func TestComposerRevealToggle(t *testing.T) {
	c := newComposer(composePassword, nil)
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
	c := newComposer(composePassword, nil)
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
	c := newComposer(composeGenerate, nil)
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
	c := newComposer(composePassword, nil)
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
	c := newComposer(composePassword, nil)
	c = typeComposer(c, "x")
	c = c.update("esc", "")
	if !c.done || !c.canceled {
		t.Fatalf("esc did not cancel: %#v", c)
	}
}

func TestComposerEmptyPathStaysOnStep(t *testing.T) {
	c := newComposer(composePassword, nil)
	c = c.update("enter", "") // no path yet
	if c.step != stepPath || c.done {
		t.Fatalf("empty path advanced: step=%v done=%v", c.step, c.done)
	}
}

// --- stepPath tab-completion behavior (uses fixturePaths from pathindex_test) ---

// TestComposerPathTabOutWorkflow is the headline flow: p<TAB> a<TAB> gmail to
// build pp/alter-ego/gmail, confirming the folders exist along the way.
func TestComposerPathTabOutWorkflow(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "p")
	c = c.update("tab", "") // unique folder pp -> descend
	if c.path.String() != "pp/" {
		t.Fatalf("after p<TAB> = %q, want pp/", c.path.String())
	}
	c = typeComposer(c, "a")
	c = c.update("tab", "") // unique folder alter-ego -> descend
	if c.path.String() != "pp/alter-ego/" {
		t.Fatalf("after a<TAB> = %q, want pp/alter-ego/", c.path.String())
	}
	c = typeComposer(c, "gmail")
	c = c.update("enter", "") // new leaf -> advance
	if c.step != stepSecret || c.notice != "" {
		t.Fatalf("new leaf should advance silently: step=%v notice=%q", c.step, c.notice)
	}
	if c.result().Path != "pp/alter-ego/gmail" {
		t.Fatalf("result path = %q", c.result().Path)
	}
}

func TestComposerPathTabCommonPrefixNoDescend(t *testing.T) {
	c := newComposer(composePassword, []string{"app/azure-prod/x", "app/azure-dev/y"})
	c = typeComposer(c, "app/a") // dir app/, frag a, matches azure-prod & azure-dev
	c = c.update("tab", "")
	if c.path.String() != "app/azure-" {
		t.Fatalf("ambiguous tab = %q, want app/azure- (common prefix, no descend)", c.path.String())
	}
}

func TestComposerPathTabFillsUniqueEntry(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "pp/backup/k") // unique entry "key"
	c = c.update("tab", "")
	if c.path.String() != "pp/backup/key" {
		t.Fatalf("tab on unique entry = %q, want pp/backup/key", c.path.String())
	}
	c = c.update("enter", "") // it already exists -> blocked
	if c.step != stepPath || !strings.Contains(c.notice, "exists") {
		t.Fatalf("existing entry: step=%v notice=%q", c.step, c.notice)
	}
}

func TestComposerPathTabNoMatchSetsNotice(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "zzz")
	c = c.update("tab", "")
	if c.path.String() != "zzz" {
		t.Fatalf("no-match tab should not edit field: %q", c.path.String())
	}
	if !strings.Contains(c.notice, "new entry") {
		t.Fatalf("no-match notice = %q", c.notice)
	}
}

func TestComposerPathArrowSelectionAndEnterDescends(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = c.update("down", "") // select first root child (pp)
	if c.selIndex != 0 {
		t.Fatalf("down should select 0, got %d", c.selIndex)
	}
	c = c.update("up", "") // back to type mode
	if c.selIndex != -1 {
		t.Fatalf("up from 0 should return to -1, got %d", c.selIndex)
	}
	c = c.update("down", "")  // pp
	c = c.update("enter", "") // selected folder -> descend, stay on path
	if c.path.String() != "pp/" || c.step != stepPath || c.selIndex != -1 {
		t.Fatalf("enter on selected folder: path=%q step=%v sel=%d", c.path.String(), c.step, c.selIndex)
	}
}

func TestComposerPathEditResetsSelection(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = c.update("down", "") // select 0
	c = typeComposer(c, "x") // any edit
	if c.selIndex != -1 {
		t.Fatalf("edit should reset selIndex, got %d", c.selIndex)
	}
}

func TestComposerPathShiftTabAscends(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "pp/alter-ego/gm")
	for _, want := range []string{"pp/alter-ego/", "pp/", ""} {
		c = c.update("shift+tab", "")
		if c.path.String() != want {
			t.Fatalf("shift+tab = %q, want %q", c.path.String(), want)
		}
	}
}

func TestComposerPathEnterBlocksFolderAndTrailingSlash(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "pp") // an existing folder, no /name
	c = c.update("enter", "")
	if c.step != stepPath || !strings.Contains(c.notice, "folder") {
		t.Fatalf("folder enter: step=%v notice=%q", c.step, c.notice)
	}
	c = typeComposer(c, "/") // now "pp/"
	c = c.update("enter", "")
	if c.step != stepPath || !strings.Contains(c.notice, "finish the name") {
		t.Fatalf("trailing-slash enter: step=%v notice=%q", c.step, c.notice)
	}
}

func TestComposerPathEnterBlocksCaseCollision(t *testing.T) {
	c := newComposer(composePassword, []string{"Work/github"})
	c = typeComposer(c, "work/github") // same entry, different case
	c = c.update("enter", "")
	if c.step != stepPath || !strings.Contains(c.notice, "case differs") {
		t.Fatalf("case collision: step=%v notice=%q", c.step, c.notice)
	}
}

// --- stepPath rendering ---

func renderPathStrip(c composerModel) string {
	return termstyle.Strip(strings.Join(c.render(56, pickerTheme{theme: termstyle.TerminalTheme()}), "\n"))
}

func TestComposerPathRenderRootChildren(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	out := renderPathStrip(c)
	for _, want := range []string{"under /", "pp/", "work/", "items"} {
		if !strings.Contains(out, want) {
			t.Fatalf("root render missing %q:\n%s", want, out)
		}
	}
}

func TestComposerPathRenderBreadcrumbAfterDescend(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "pp/alter-ego/")
	out := renderPathStrip(c)
	if !strings.Contains(out, "in    pp / alter-ego /") {
		t.Fatalf("breadcrumb missing:\n%s", out)
	}
	if !strings.Contains(out, "proton") {
		t.Fatalf("folder children missing:\n%s", out)
	}
	// No breadcrumb at the root (nothing committed yet).
	if strings.Contains(renderPathStrip(newComposer(composePassword, fixturePaths)), "in    ") {
		t.Fatalf("breadcrumb should be hidden before any folder is committed")
	}
}

func TestComposerPathRenderOverwrite(t *testing.T) {
	c := newComposer(composePassword, []string{"work/aws/db", "work/github"})
	c = typeComposer(c, "work/github")
	c = c.update("enter", "")
	out := renderPathStrip(c)
	if !strings.Contains(out, "exists") {
		t.Fatalf("overwrite row tag missing:\n%s", out)
	}
	if !strings.Contains(out, "esc, then E to edit") {
		t.Fatalf("overwrite notice missing:\n%s", out)
	}
}

func TestComposerPathRenderNoMatchHint(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	c = typeComposer(c, "pp/zzz")
	out := renderPathStrip(c)
	if !strings.Contains(out, "no existing name starts with") {
		t.Fatalf("no-match hint missing:\n%s", out)
	}
	// The folder's real children still show as dimmed context.
	if !strings.Contains(out, "alter-ego") || !strings.Contains(out, "backup") {
		t.Fatalf("dimmed context children missing:\n%s", out)
	}
}

func TestComposerPathRenderSelectionCaret(t *testing.T) {
	c := newComposer(composePassword, fixturePaths)
	out := renderPathStrip(c.update("down", ""))
	if !strings.Contains(out, "> ") {
		t.Fatalf("selection caret missing:\n%s", out)
	}
}

func TestComposerPathRenderEmptyStore(t *testing.T) {
	c := newComposer(composePassword, nil)
	c = typeComposer(c, "newthing")
	out := renderPathStrip(c)
	if !strings.Contains(out, "empty") {
		t.Fatalf("empty-store render should note emptiness:\n%s", out)
	}
}

func TestComposerPathRenderSanitizesNames(t *testing.T) {
	// A hostile entry name must not reach the terminal raw (BEL would ring it).
	c := newComposer(composePassword, []string{"danger/ev\x07il"})
	c = typeComposer(c, "danger/")
	raw := strings.Join(c.render(56, pickerTheme{theme: termstyle.TerminalTheme()}), "\n")
	if strings.ContainsRune(raw, '\x07') {
		t.Fatalf("BEL control char leaked into render")
	}
}

func TestComposerPathBreadcrumbTailTruncates(t *testing.T) {
	c := newComposer(composePassword, []string{"alpha/bravo/charlie/delta/echo/foxtrot/other"})
	c = typeComposer(c, "alpha/bravo/charlie/delta/echo/foxtrot/active")
	var in string
	for _, ln := range strings.Split(renderPathStrip(c), "\n") {
		if strings.HasPrefix(ln, "in    ") {
			in = ln
		}
	}
	if in == "" {
		t.Fatal("no breadcrumb line rendered")
	}
	if !strings.Contains(in, "…") {
		t.Fatalf("deep breadcrumb should ellipsize: %q", in)
	}
	if !strings.Contains(in, "active") {
		t.Fatalf("the active (typed) segment must stay visible: %q", in)
	}
	if w := termstyle.VisibleWidth(in); w > 56 {
		t.Fatalf("breadcrumb width %d exceeds box: %q", w, in)
	}
}

func TestComposerPathRenderViewRowsWindow(t *testing.T) {
	paths := make([]string, 0, 12)
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		paths = append(paths, "root/"+n)
	}
	c := newComposer(composePassword, paths)
	c = typeComposer(c, "root/")
	c.viewRows = 4 // cap the candidate window
	out := renderPathStrip(c)
	if !strings.Contains(out, "more below") {
		t.Fatalf("windowed list should show an overflow marker:\n%s", out)
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
