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
}

type composerResult struct {
	Path      string
	Generate  bool
	Content   []byte
	Length    int
	NoSymbols bool
}

func newComposer(mode composerMode) composerModel {
	return composerModel{
		mode:    mode,
		step:    stepPath,
		secret:  textField{masked: true},
		confirm: textField{masked: true},
		length:  composerDefaultLength,
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
		switch key {
		case "enter":
			if strings.TrimSpace(c.path.String()) == "" {
				return c
			}
			if c.mode == composeGenerate {
				c.step = stepLength
			} else {
				c.step = stepSecret
			}
		case "ctrl+g":
			c.mode = composeGenerate
		default:
			c.path = c.path.update(key, text)
		}
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
