package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

// DirEntry is one row in the directory browser: a folder to open, the parent,
// or the "use this folder" choice. Path is the absolute path the choice
// resolves to; Kind drives the caller's navigation.
type DirEntry struct {
	Title string
	Path  string
	Kind  string // "use" | "up" | "dir"
}

type DirBrowseOptions struct {
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Theme       termstyle.Theme
	ThemeFile   string
	Title       string
	Location    string
	Entries     []DirEntry
}

// BrowseDir shows one directory's contents and returns the chosen entry. It is
// intentionally "dumb" — the caller lists each directory and re-runs BrowseDir
// as the user navigates (open a folder, go up), mirroring ssherpa's transfer
// browser. Returns ok=false on cancel.
func BrowseDir(ctx context.Context, opts DirBrowseOptions) (DirEntry, bool, error) {
	theme, err := resolveTheme(opts.NoColor, opts.Theme, opts.ThemeFile)
	if err != nil {
		return DirEntry{}, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model := dirBrowseModel{
		entries:     append([]DirEntry(nil), opts.Entries...),
		selected:    -1,
		theme:       theme.WithNoColor(theme.NoColor || opts.NoColor),
		title:       defaultString(opts.Title, "choose a folder"),
		location:    opts.Location,
		noAltScreen: opts.NoAltScreen,
		width:       90,
		height:      26,
	}
	model.applyFilter()
	programOptions := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		programOptions = append(programOptions, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		programOptions = append(programOptions, tea.WithOutput(opts.Output))
	}
	final, err := tea.NewProgram(model, programOptions...).Run()
	if err != nil {
		return DirEntry{}, false, err
	}
	browser, ok := final.(dirBrowseModel)
	if !ok || browser.canceled || browser.selected < 0 || browser.selected >= len(browser.filtered) {
		return DirEntry{}, false, nil
	}
	return browser.entries[browser.filtered[browser.selected]], true, nil
}

type dirBrowseModel struct {
	entries     []DirEntry
	filtered    []int
	cursor      int
	scroll      int
	query       string
	selected    int
	canceled    bool
	theme       termstyle.Theme
	title       string
	location    string
	noAltScreen bool
	width       int
	height      int
}

func (m dirBrowseModel) Init() tea.Cmd {
	return tea.RequestWindowSize
}

func (m dirBrowseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.ensureVisible()
	case tea.KeyPressMsg:
		key := normalizedKey(msg)
		switch key {
		case "ctrl+c", "esc", "ctrl+q":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if len(m.filtered) > 0 {
				m.selected = m.cursor
				return m, tea.Quit
			}
		case "up", "ctrl+p":
			m.move(-1)
		case "down", "ctrl+n":
			m.move(1)
		case "pgup":
			m.move(-m.pageSize())
		case "pgdown":
			m.move(m.pageSize())
		case "home":
			m.cursor = 0
			m.ensureVisible()
		case "end":
			m.cursor = max(0, len(m.filtered)-1)
			m.ensureVisible()
		case "backspace":
			if m.query != "" {
				m.query = m.query[:len(m.query)-1]
				m.applyFilter()
			}
		case "left", "right":
			// ignore horizontal arrows
		default:
			if safeTextInput(msg.Text) && !isControlKey(key) {
				m.query += msg.Text
				m.applyFilter()
			}
		}
	}
	return m, nil
}

func (m dirBrowseModel) View() tea.View {
	width := max(48, m.width)
	theme := pickerTheme{theme: m.theme}
	body := []string{m.locationLine(width-4, theme), m.filterLine(width-4, theme), ""}
	body = append(body, m.listLines(width-4, theme)...)
	view := tea.NewView(renderWorkflowShell(theme, width, workflowShell{
		Title:  strings.ToUpper(m.title),
		Body:   body,
		Footer: "enter open/use   type filter   arrows move   esc cancel",
	}))
	view.AltScreen = !m.noAltScreen
	return view
}

func (m dirBrowseModel) locationLine(width int, theme pickerTheme) string {
	loc := m.location
	if loc == "" {
		loc = "."
	}
	return theme.muted(termstyle.Truncate("folder  "+termstyle.Sanitize(loc), width))
}

func (m dirBrowseModel) filterLine(width int, theme pickerTheme) string {
	query := termstyle.Sanitize(m.query)
	if m.query == "" {
		query = "type to filter"
	}
	counter := theme.counter(len(m.filtered), len(m.entries))
	field := "/" + query
	if m.query == "" {
		field = theme.muted(field)
	} else {
		field = theme.primary(field)
	}
	return field + "  " + counter
}

func (m dirBrowseModel) listLines(width int, theme pickerTheme) []string {
	if len(m.filtered) == 0 {
		return []string{theme.warning("No matching folders.")}
	}
	available := max(1, m.pageSize())
	start := clamp(m.scroll, 0, max(0, len(m.filtered)-available))
	slots := available
	if start > 0 && slots > 1 {
		slots--
	}
	end := min(len(m.filtered), start+slots)
	if end < len(m.filtered) && slots > 1 {
		slots--
		end = min(len(m.filtered), start+slots)
	}
	var lines []string
	if start > 0 {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more above", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, dirBrowseRow(m.entries[m.filtered[i]], i == m.cursor, width, theme))
	}
	if end < len(m.filtered) {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more below", len(m.filtered)-end)))
	}
	return lines
}

func dirBrowseRow(entry DirEntry, selected bool, width int, theme pickerTheme) string {
	cursor := "  "
	if selected {
		cursor = "> "
	}
	badge := "[" + strings.ToUpper(entry.Kind) + "]"
	line := cursor + termstyle.PadRight(badge, 6) + " " + termstyle.Sanitize(entry.Title)
	line = termstyle.Truncate(line, width)
	switch {
	case selected:
		return theme.selected(termstyle.PadRight(line, width))
	case entry.Kind == "use":
		return theme.accent(line)
	case entry.Kind == "up":
		return theme.muted(line)
	default:
		return theme.primary(line)
	}
}

func (m dirBrowseModel) pageSize() int {
	// shell chrome (2) + footer (2) + location/filter/blank (3) + safety (1)
	return max(1, m.height-8)
}

func (m *dirBrowseModel) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.query))
	m.filtered = m.filtered[:0]
	for i, entry := range m.entries {
		if q == "" || strings.Contains(strings.ToLower(entry.Title), q) {
			m.filtered = append(m.filtered, i)
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureVisible()
}

func (m *dirBrowseModel) move(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(m.filtered)-1)
	m.ensureVisible()
}

func (m *dirBrowseModel) ensureVisible() {
	if len(m.filtered) == 0 {
		m.scroll = 0
		return
	}
	page := m.pageSize()
	if m.cursor < m.scroll {
		m.scroll = m.cursor
	}
	if m.cursor >= m.scroll+page {
		m.scroll = m.cursor - page + 1
	}
	if m.scroll < 0 {
		m.scroll = 0
	}
}
