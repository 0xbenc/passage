package ui

import (
	"context"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

type TextOptions struct {
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Theme       termstyle.Theme
	ThemeFile   string
	Title       string
	Lines       []string
}

func Text(ctx context.Context, opts TextOptions) error {
	theme, err := resolveTheme(opts.NoColor, opts.Theme, opts.ThemeFile)
	if err != nil {
		return err
	}
	model := textModel{
		title:       defaultString(opts.Title, "passage"),
		lines:       append([]string(nil), opts.Lines...),
		theme:       theme.WithNoColor(theme.NoColor || opts.NoColor),
		noAltScreen: opts.NoAltScreen,
		width:       90,
		height:      26,
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

type textModel struct {
	title       string
	lines       []string
	offset      int
	theme       termstyle.Theme
	noAltScreen bool
	width       int
	height      int
}

func (m textModel) Init() tea.Cmd {
	return tea.RequestWindowSize
}

func (m textModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q", "Q", "enter":
			return m, tea.Quit
		case "up", "ctrl+p":
			if m.offset > 0 {
				m.offset--
			}
		case "down", "ctrl+n":
			if m.offset < max(0, len(m.lines)-m.pageSize()) {
				m.offset++
			}
		case "pgup":
			m.offset = max(0, m.offset-m.pageSize())
		case "pgdown":
			m.offset = min(max(0, len(m.lines)-m.pageSize()), m.offset+m.pageSize())
		}
	}
	return m, nil
}

func (m textModel) View() tea.View {
	width := max(58, m.width)
	theme := pickerTheme{theme: m.theme}
	page := m.pageSize()
	end := min(len(m.lines), m.offset+page)
	body := []string{}
	if m.offset > 0 {
		body = append(body, theme.muted("more above"))
	}
	for _, line := range m.lines[m.offset:end] {
		body = append(body, termstyle.Truncate(line, width-6))
	}
	if end < len(m.lines) {
		body = append(body, theme.muted("more below"))
	}
	if len(body) == 0 {
		body = []string{theme.muted("No output")}
	}
	view := tea.NewView(renderWorkflowShell(theme, width, workflowShell{
		Title:  m.title,
		Body:   body,
		Footer: termstyle.Footer([]termstyle.KeyHint{{"up/down", "scroll"}, {"enter/q", "close"}}, 0),
	}))
	view.AltScreen = !m.noAltScreen
	return view
}

func (m textModel) pageSize() int {
	return max(5, m.height-6)
}

func LinesFromText(text string) []string {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}
