package ui

import (
	"strconv"
	"strings"

	"github.com/0xbenc/passage/internal/termstyle"
)

type composerMode int

const (
	composePassword composerMode = iota // type a secret
	composeGenerate                     // generate a random password
)

type composerStep int

const (
	stepPath composerStep = iota
	stepSecret
	stepConfirm
	stepLength
)

const (
	composerDefaultLength = 24
	composerMinLength     = 4
	composerMaxLength     = 128
)

// composerModel is the multi-step "new entry" sub-surface: it collects an entry
// path, then either a typed secret (entered twice and required to match) or
// generate options, and yields a composerResult for the CLI to apply. The typed
// secret is masked by default; ^R toggles plaintext on both the secret and
// confirm fields together. It is a pure state machine — no I/O — so its
// transitions are unit-tested without a terminal.
type composerModel struct {
	mode      composerMode
	step      composerStep
	path      textField
	secret    textField
	confirm   textField
	length    int
	noSymbols bool
	mismatch  bool
	done      bool
	canceled  bool

	// Path-completion state for stepPath. idx is an immutable snapshot of the
	// store's entry paths (folder-awareness); selIndex is the highlighted
	// candidate (-1 = plain type mode, no selection); notice carries a blocked-
	// enter / no-match reason so feedback is never silent; viewRows caps the
	// candidate window from the terminal height (0 = uncapped, for unit tests).
	idx      pathIndex
	selIndex int
	notice   string
	viewRows int
}

type composerResult struct {
	Path      string
	Generate  bool
	Content   []byte
	Length    int
	NoSymbols bool
}

func newComposer(mode composerMode, entryPaths []string) composerModel {
	return composerModel{
		mode:     mode,
		step:     stepPath,
		secret:   textField{masked: true},
		confirm:  textField{masked: true},
		length:   composerDefaultLength,
		idx:      buildPathIndex(entryPaths),
		selIndex: -1,
	}
}

func (c composerModel) update(key, text string) composerModel {
	if key == "esc" {
		c.canceled = true
		c.done = true
		return c
	}
	// ^R toggles plaintext for the typed secret. Both entry fields share one
	// visibility so the confirmation is shown the same way as the original.
	if key == "ctrl+r" && (c.step == stepSecret || c.step == stepConfirm) {
		reveal := c.secret.masked
		c.secret.masked = !reveal
		c.confirm.masked = !reveal
		return c
	}
	switch c.step {
	case stepPath:
		return c.updatePath(key, text)
	case stepSecret:
		switch key {
		case "enter":
			// Require a non-empty secret, then confirm it by re-typing.
			if c.secret.String() == "" {
				return c
			}
			c.mismatch = false
			c.step = stepConfirm
		case "ctrl+g":
			// Switch to generate instead of typing a secret.
			c.mode = composeGenerate
			c.step = stepLength
		default:
			c.secret = c.secret.update(key, text)
		}
	case stepConfirm:
		switch key {
		case "enter":
			if c.confirm.String() == c.secret.String() {
				c.done = true
				break
			}
			// Mismatch: drop both entries and start over so a typo in either
			// field can't be silently saved. Visibility is preserved.
			c.mismatch = true
			c.secret = textField{masked: c.secret.masked}
			c.confirm = textField{masked: c.confirm.masked}
			c.step = stepSecret
		default:
			c.confirm = c.confirm.update(key, text)
		}
	case stepLength:
		switch key {
		case "enter":
			c.done = true
		case "up", "right":
			c.length = clamp(c.length+1, composerMinLength, composerMaxLength)
		case "down", "left":
			c.length = clamp(c.length-1, composerMinLength, composerMaxLength)
		default:
			switch text {
			case "+":
				c.length = clamp(c.length+1, composerMinLength, composerMaxLength)
			case "-":
				c.length = clamp(c.length-1, composerMinLength, composerMaxLength)
			case "s":
				c.noSymbols = !c.noSymbols
			}
		}
	}
	return c
}

// updatePath drives stepPath: shell-style TAB completion over the implicit
// folder tree, arrow selection of the live candidate list, and enter-validation
// against the path index. selIndex == -1 is plain type mode (no highlight); any
// field edit returns to it and clears the notice so feedback is never stale.
func (c composerModel) updatePath(key, text string) composerModel {
	field := c.path.String()
	dir, frag := splitPath(field)
	switch key {
	case "enter":
		// A highlighted folder descends; anything else validates + submits.
		if c.selIndex >= 0 {
			cands := c.idx.completionCandidates(dir, frag)
			if c.selIndex < len(cands) {
				newField, descended := applyNode(dir, cands[c.selIndex].node)
				c.path = c.path.withValue(newField)
				c.selIndex = -1
				c.notice = ""
				if descended {
					return c
				}
				field = newField // an entry was filled; validate it below
			}
		}
		return c.submitPath(field)
	case "tab":
		return c.completePath(dir, frag)
	case "shift+tab":
		c.path = c.path.withValue(ascendPath(field))
		c.selIndex = -1
		c.notice = ""
		return c
	case "down":
		if cands := c.idx.completionCandidates(dir, frag); c.selIndex < len(cands)-1 {
			c.selIndex++
		}
		c.notice = ""
		return c
	case "up":
		if c.selIndex >= 0 {
			c.selIndex--
		}
		c.notice = ""
		return c
	case "ctrl+g":
		// Switch to generate; stay on the path step (enter then goes to length).
		c.mode = composeGenerate
		c.notice = ""
		return c
	default:
		// Field edit: printable text (incl. "/"), backspace, cursor motion, ^U.
		c.path = c.path.update(key, text)
		c.selIndex = -1
		c.notice = ""
		return c
	}
}

// completePath implements shell first-TAB: accept a highlighted candidate, or
// over the current fragment complete to a unique child, or extend to the
// longest common prefix of the matches without guessing a descent.
func (c composerModel) completePath(dir, frag string) composerModel {
	cands := c.idx.completionCandidates(dir, frag)
	if c.selIndex >= 0 && c.selIndex < len(cands) {
		newField, _ := applyNode(dir, cands[c.selIndex].node)
		c.path = c.path.withValue(newField)
		c.selIndex = -1
		c.notice = ""
		return c
	}
	matches := matchingCandidates(cands)
	switch len(matches) {
	case 0:
		if frag == "" {
			c.notice = "no entries in " + displayDir(dir)
		} else {
			c.notice = "no match — " + strconv.Quote(frag) + " will be a new entry"
		}
	case 1:
		newField, _ := applyNode(dir, matches[0].node)
		c.path = c.path.withValue(newField)
		c.notice = ""
	default:
		// Extend to the common prefix only when it makes progress; the live
		// list already shows every match for the next keystroke.
		if prefix := commonCompletionPrefix(matches); len([]rune(prefix)) > len([]rune(frag)) {
			c.path = c.path.withValue(dir + prefix)
		}
		c.notice = ""
	}
	c.selIndex = -1
	return c
}

// submitPath validates the typed path and either advances to the next step or
// sets a notice explaining why it cannot. It mirrors the CLI's exact-match
// overwrite gate, surfacing a duplicate before any round-trip.
func (c composerModel) submitPath(field string) composerModel {
	switch c.idx.classifyLeaf(field) {
	case leafEmpty:
		return c // historical silent no-op for an empty path
	case leafTrailingSlash:
		c.notice = "finish the name after the last /"
	case leafEmptySegment:
		c.notice = "remove the empty path segment"
	case leafPathIsFolder:
		c.notice = strings.TrimSpace(field) + " is a folder — add /name"
	case leafExistingEntry:
		c.notice = "entry exists — esc, then E to edit"
	case leafCaseCollision:
		c.notice = "exists as " + strconv.Quote(c.idx.caseCollisionCanonical(field)) + " (case differs)"
	default: // leafNew
		c.notice = ""
		if c.mode == composeGenerate {
			c.step = stepLength
		} else {
			c.step = stepSecret
		}
	}
	return c
}

func (c composerModel) result() composerResult {
	path := strings.TrimSpace(c.path.String())
	if c.mode == composeGenerate {
		return composerResult{Path: path, Generate: true, Length: c.length, NoSymbols: c.noSymbols}
	}
	return composerResult{Path: path, Content: []byte(c.secret.String())}
}

func (c composerModel) title() string {
	if c.mode == composeGenerate {
		return "new entry · generate"
	}
	return "new entry"
}

func (c composerModel) footer() string {
	switch c.step {
	case stepPath:
		return "enter next  ^G generate  esc cancel"
	case stepSecret:
		return "enter next  ^R " + c.revealLabel() + "  ^G generate  esc cancel"
	case stepConfirm:
		return "enter save  ^R " + c.revealLabel() + "  esc cancel"
	default:
		return "↑/↓ length  s symbols  enter make  esc cancel"
	}
}

// revealLabel names the action ^R would perform next, given the current
// visibility of the secret fields.
func (c composerModel) revealLabel() string {
	if c.secret.masked {
		return "show"
	}
	return "hide"
}

func (c composerModel) render(width int, theme pickerTheme) []string {
	fieldWidth := max(8, width-10)
	pathLine := theme.muted("path")
	if c.step == stepPath {
		pathLine = theme.primary("path  " + c.path.render(fieldWidth))
	} else {
		pathLine = theme.muted("path  ") + theme.primary(termstyle.Truncate(c.path.String(), fieldWidth))
	}
	lines := []string{pathLine}
	switch c.step {
	case stepPath:
		// nothing else yet
	case stepSecret:
		lines = append(lines, theme.primary("secret   "+c.secret.render(fieldWidth)))
		if c.mismatch {
			lines = append(lines, theme.warning("secrets did not match — type it again"))
		}
	case stepConfirm:
		// The first entry is settled context (no cursor); the confirm field is
		// active. Aligned so a revealed secret lines up for visual comparison.
		lines = append(lines,
			theme.muted("secret   ")+theme.secondary(termstyle.Truncate(c.secret.displayString(), fieldWidth)),
			theme.primary("confirm  "+c.confirm.render(fieldWidth)),
		)
	case stepLength:
		lines = append(lines, theme.primary("length  "+strconv.Itoa(c.length)))
		symbols := "on"
		if c.noSymbols {
			symbols = "off"
		}
		lines = append(lines, theme.muted("symbols  "+symbols))
	}
	return lines
}
