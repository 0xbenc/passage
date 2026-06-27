package ui

import (
	"strings"

	"github.com/0xbenc/passage/internal/termstyle"
)

// textField is a minimal single-line input used by the composer. It owns a rune
// buffer and a cursor; every edit returns a fresh value so the surrounding
// (copied-by-value) model never aliases another field's backing array.
type textField struct {
	value  []rune
	cursor int
	masked bool
}

func (f textField) String() string {
	return string(f.value)
}

// displayString returns the field's visible content without a cursor — bullets
// when masked, plaintext otherwise. Used to show a settled field as context.
func (f textField) displayString() string {
	if !f.masked {
		return string(f.value)
	}
	return strings.Repeat("•", len(f.value))
}

// withValue replaces the buffer with value and puts the cursor at the end. Used
// by path completion to splice a completed segment into the field.
func (f textField) withValue(value string) textField {
	f.value = []rune(value)
	f.cursor = len(f.value)
	return f
}

// update applies one keystroke. key is the normalized key name; text is the
// printable text (if any).
func (f textField) update(key, text string) textField {
	switch key {
	case "backspace":
		if f.cursor > 0 {
			v := make([]rune, 0, len(f.value)-1)
			v = append(v, f.value[:f.cursor-1]...)
			v = append(v, f.value[f.cursor:]...)
			f.value = v
			f.cursor--
		}
	case "left":
		if f.cursor > 0 {
			f.cursor--
		}
	case "right":
		if f.cursor < len(f.value) {
			f.cursor++
		}
	case "home":
		f.cursor = 0
	case "end":
		f.cursor = len(f.value)
	case "ctrl+u":
		f.value = nil
		f.cursor = 0
	default:
		if safeTextInput(text) {
			runes := []rune(text)
			v := make([]rune, 0, len(f.value)+len(runes))
			v = append(v, f.value[:f.cursor]...)
			v = append(v, runes...)
			v = append(v, f.value[f.cursor:]...)
			f.value = v
			f.cursor += len(runes)
		}
	}
	return f
}

// render returns the field's visible content with an inline block cursor,
// truncated to width. A masked field shows bullets instead of the runes.
func (f textField) render(width int) string {
	glyphs := make([]string, len(f.value))
	for i := range f.value {
		if f.masked {
			glyphs[i] = "•"
		} else {
			// Completion can splice untrusted store names into the (unmasked)
			// path field, so neutralize escape/control bytes per rune. Sanitize
			// strips the ESC/C1 that starts a sequence, leaving inert literals
			// and one glyph per rune so the cursor split stays aligned.
			glyphs[i] = termstyle.Sanitize(string(f.value[i]))
		}
	}
	cursor := clamp(f.cursor, 0, len(glyphs))
	withCursor := strings.Join(glyphs[:cursor], "") + "▮" + strings.Join(glyphs[cursor:], "")
	if width <= 0 {
		return withCursor
	}
	return termstyle.Truncate(withCursor, width)
}
