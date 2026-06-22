package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

type RevealOptions struct {
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Theme       termstyle.Theme
	ThemeFile   string
	Title       string
	Secret      string
	Kind        string
	Remaining   int
}

func Reveal(ctx context.Context, opts RevealOptions) error {
	theme, err := resolveTheme(opts.NoColor, opts.Theme, opts.ThemeFile)
	if err != nil {
		return err
	}
	model := revealModel{
		title:       defaultString(opts.Title, "Reveal"),
		secret:      opts.Secret,
		kind:        defaultString(opts.Kind, "secret"),
		remaining:   opts.Remaining,
		theme:       theme.WithNoColor(theme.NoColor || opts.NoColor),
		noAltScreen: opts.NoAltScreen,
		width:       80,
	}
	programOptions := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		programOptions = append(programOptions, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		programOptions = append(programOptions, tea.WithOutput(opts.Output))
	}
	_, err = tea.NewProgram(model, programOptions...).Run()
	return err
}

type revealModel struct {
	title       string
	secret      string
	kind        string
	remaining   int
	theme       termstyle.Theme
	noAltScreen bool
	width       int
}

func (m revealModel) Init() tea.Cmd {
	return tea.RequestWindowSize
}

func (m revealModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
	case tea.KeyPressMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m revealModel) View() tea.View {
	width := clamp(m.width, 54, 120)
	theme := pickerTheme{theme: m.theme}
	var body []string
	body = append(body, theme.muted(strings.ToUpper(m.kind)))
	body = append(body, "")
	for _, line := range wrapSecret(m.secret, width-8) {
		body = append(body, theme.primary(line))
	}
	if m.remaining > 0 {
		body = append(body, "")
		body = append(body, theme.warning(fmt.Sprintf("%ds remaining", m.remaining)))
	}
	view := tea.NewView(renderWorkflowShell(theme, width, workflowShell{
		Title:  m.title,
		Body:   body,
		Footer: "press any key to clear",
		Danger: m.kind == "password",
	}))
	view.AltScreen = !m.noAltScreen
	return view
}

// wrapSecret splits a secret into lines no wider than width, breaking only on
// rune boundaries so a multibyte secret is never corrupted mid-rune (the old
// byte-slice cut split CJK/emoji secrets on narrow terminals). It measures each
// rune with termstyle.VisibleWidth so the wrap stays cell-accurate once width
// math is cell-based, while remaining identical to rune-counting for ASCII.
func wrapSecret(value string, width int) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return []string{""}
	}
	if width < 1 {
		width = 1
	}
	if termstyle.VisibleWidth(value) <= width {
		return []string{value}
	}
	var out []string
	var line strings.Builder
	lineWidth := 0
	for _, r := range value {
		rw := termstyle.VisibleWidth(string(r))
		if rw < 1 {
			rw = 1
		}
		if lineWidth > 0 && lineWidth+rw > width {
			out = append(out, line.String())
			line.Reset()
			lineWidth = 0
		}
		line.WriteRune(r)
		lineWidth += rw
	}
	if line.Len() > 0 {
		out = append(out, line.String())
	}
	if len(out) == 0 {
		out = append(out, "")
	}
	return out
}
