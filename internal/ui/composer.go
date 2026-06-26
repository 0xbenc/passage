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
	stepLength
)

const (
	composerDefaultLength = 24
	composerMinLength     = 4
	composerMaxLength     = 128
)

// composerModel is the multi-step "new entry" sub-surface: it collects an entry
// path, then either a typed (masked) secret or generate options, and yields a
// composerResult for the CLI to apply. It is a pure state machine — no I/O — so
// its transitions are unit-tested without a terminal.
type composerModel struct {
	mode      composerMode
	step      composerStep
	path      textField
	secret    textField
	length    int
	noSymbols bool
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
		mode:   mode,
		step:   stepPath,
		secret: textField{masked: true},
		length: composerDefaultLength,
	}
}

func (c composerModel) update(key, text string) composerModel {
	if key == "esc" {
		c.canceled = true
		c.done = true
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
			c.done = true
		case "ctrl+g":
			// Switch to generate instead of typing a secret.
			c.mode = composeGenerate
			c.step = stepLength
		default:
			c.secret = c.secret.update(key, text)
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
		return "enter save  ^G generate instead  esc cancel"
	default:
		return "↑/↓ length  s symbols  enter make  esc cancel"
	}
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
		lines = append(lines, theme.primary("secret  "+c.secret.render(fieldWidth)))
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
