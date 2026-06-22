package ui

import (
	"context"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

type ThemeEditorOptions struct {
	// Input/Output are only used by the standalone EditTheme program; the
	// picker hosts the editor model directly and leaves them nil.
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Config      termstyle.ThemeConfig
	ConfigPath  string
	Warning     string
}

type ThemeEditorResult struct {
	Config termstyle.ThemeConfig
	Theme  termstyle.Theme
	Path   string
}

type ThemeSaveResult struct {
	Config     termstyle.ThemeConfig
	Theme      termstyle.Theme
	Path       string
	Changed    bool
	BackupPath string
	Message    string
}

type ThemeSaveFunc func(context.Context, ThemeEditorResult) (ThemeSaveResult, error)

// EditTheme runs the theme builder as its own full-screen program, for the
// `passage theme` command. The picker hosts the same editor model inline via
// Ctrl-O; this is the standalone path. It returns ok=false when the user quits
// without saving, so the caller writes nothing.
func EditTheme(ctx context.Context, opts ThemeEditorOptions) (ThemeEditorResult, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	program := themeEditorProgram{
		editor:      newThemeEditorModel(opts),
		noAltScreen: opts.NoAltScreen,
	}
	programOptions := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		programOptions = append(programOptions, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		programOptions = append(programOptions, tea.WithOutput(opts.Output))
	}
	final, err := tea.NewProgram(program, programOptions...).Run()
	if err != nil {
		return ThemeEditorResult{}, false, err
	}
	done, ok := final.(themeEditorProgram)
	if !ok || done.editor.canceled || !done.editor.saved {
		return ThemeEditorResult{}, false, nil
	}
	return done.editor.result(), true, nil
}

// themeEditorProgram adapts the picker-hosted themeEditorModel to the
// tea.Model interface so it can run on its own.
type themeEditorProgram struct {
	editor      themeEditorModel
	noAltScreen bool
}

func (p themeEditorProgram) Init() tea.Cmd { return tea.RequestWindowSize }

func (p themeEditorProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		// Quit shortcuts must win even mid raw-edit, matching the picker host.
		if k := normalizedKey(key); k == "ctrl+c" || k == "ctrl+q" {
			p.editor.canceled = true
			return p, tea.Quit
		}
	}
	updated, done := p.editor.update(msg)
	p.editor = updated
	if done {
		return p, tea.Quit
	}
	return p, nil
}

func (p themeEditorProgram) View() tea.View {
	width := max(48, p.editor.width)
	editorTheme := pickerTheme{theme: p.editor.currentTheme()}
	view := tea.NewView(strings.Join(p.editor.view(width, editorTheme), "\n") + "\n")
	view.AltScreen = !p.noAltScreen
	return view
}

type themeEditorModel struct {
	values     map[termstyle.Role]string
	base       string
	cursor     int
	noColor    bool
	configPath string
	warning    string
	message    string
	editMode   bool
	editBuffer string
	saved      bool
	canceled   bool
	tone       textTone
	width      int
	height     int
}

type textTone int

const (
	toneTheme textTone = iota
	toneBlack
	toneWhite
)

type themeRoleMeta struct {
	Role        termstyle.Role
	Label       string
	Description string
}

type stylePreset struct {
	Label string
	Spec  string
}

func newThemeEditorModel(opts ThemeEditorOptions) themeEditorModel {
	values := make(map[termstyle.Role]string)
	for role, spec := range opts.Config.Specs {
		if strings.TrimSpace(spec) != "" {
			values[role] = strings.TrimSpace(spec)
		}
	}
	for role, code := range opts.Config.Codes {
		if _, ok := values[role]; !ok && strings.TrimSpace(code) != "" {
			values[role] = strings.TrimSpace(code)
		}
	}
	// Seed the base palette from the config, normalizing unknown names to the
	// terminal default (BuiltinTheme("") already yields terminal).
	base := "terminal"
	if t, ok := termstyle.BuiltinTheme(opts.Config.BaseName); ok {
		base = t.Name
	}
	return themeEditorModel{
		values:     values,
		base:       base,
		noColor:    opts.NoColor,
		configPath: opts.ConfigPath,
		warning:    opts.Warning,
		width:      104,
		height:     28,
	}
}

func (m themeEditorModel) update(msg tea.Msg) (themeEditorModel, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
	case tea.KeyPressMsg:
		if m.editMode {
			return m.updateEdit(msg)
		}
		switch normalizedKey(msg) {
		case "esc", "Q":
			m.canceled = true
			return m, true
		case "s", "ctrl+s":
			m.saved = true
			return m, true
		case "up", "ctrl+p", "shift+tab":
			m.move(-1)
		case "down", "ctrl+n", "tab":
			m.move(1)
		case "left", "h":
			m.cycleCurrent(-1)
		case "right", "l":
			m.cycleCurrent(1)
		case "e", "enter":
			m.startEdit()
		case "d", "delete":
			m.clearCurrent()
		case "r":
			m.resetAll()
		case "t":
			m.cycleTone()
		}
	}
	return m, false
}

func (m themeEditorModel) updateEdit(msg tea.KeyPressMsg) (themeEditorModel, bool) {
	switch normalizedKey(msg) {
	case "esc":
		m.editMode = false
		m.editBuffer = ""
		m.message = "edit cancelled"
	case "enter":
		if _, err := termstyle.ParseStyleSpec(m.editBuffer); err != nil {
			m.message = err.Error()
			return m, false
		}
		if role, ok := m.selectedRole(); ok {
			m.values[role] = strings.TrimSpace(m.editBuffer)
			m.message = "updated " + string(role)
		}
		m.editMode = false
	case "backspace":
		if m.editBuffer != "" {
			runes := []rune(m.editBuffer)
			m.editBuffer = string(runes[:len(runes)-1])
		}
	case "ctrl+u":
		m.editBuffer = ""
	default:
		if safeTextInput(msg.Text) && !isControlKey(normalizedKey(msg)) {
			m.editBuffer += msg.Text
		}
	}
	return m, false
}

func (m themeEditorModel) view(width int, theme pickerTheme) []string {
	width = clamp(width, 48, 140)
	bodyWidth := max(20, width-4)
	body := m.renderStatusLines(bodyWidth, theme)
	body = append(body, "")
	body = append(body, m.renderBody(bodyWidth, theme)...)
	footer := "s save  /  arrows change  /  e edit raw  /  d inherit  /  r reset  /  t contrast  /  esc close"
	if m.editMode {
		footer = "Enter accept  /  Esc cancel  /  Backspace edit  /  Ctrl-U clear"
	}
	return splitRendered(renderWorkflowShell(theme, width, workflowShell{
		Title:  "PASSAGE THEME BUILDER",
		Body:   body,
		Footer: footer,
	}))
}

func (m themeEditorModel) renderStatusLines(width int, theme pickerTheme) []string {
	path := m.configPath
	if path == "" {
		path = "(default theme path)"
	}
	lines := themeEditorKVLines(theme, width, "Config", path, termstyle.RoleSecondary, 2)
	if m.warning != "" {
		lines = append(lines, themeEditorKVLines(theme, width, "Warning", m.warning, termstyle.RoleWarning, 2)...)
	}
	if m.message != "" {
		lines = append(lines, themeEditorKVLines(theme, width, "Status", m.message, termstyle.RoleSecondary, 2)...)
	}
	lines = append(lines, themeEditorKVLines(theme, width, "Contrast", m.contrastChips(), termstyle.RoleSubtle, 1)...)
	return lines
}

func themeEditorKVLines(theme pickerTheme, width int, key string, value string, role termstyle.Role, maxLines int) []string {
	labelWidth := 9
	valueWidth := max(0, width-labelWidth-1)
	keyText := theme.accent(termstyle.PadRight(key, labelWidth))
	if valueWidth <= 0 {
		return []string{termstyle.Truncate(keyText, width)}
	}
	if maxLines <= 1 || strings.Contains(value, "\x1b[") {
		line := theme.theme.Style(role, truncateStyled(value, valueWidth))
		return []string{keyText + " " + line}
	}
	wrapped := wrapPlain(termstyle.Strip(value), valueWidth, maxLines)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	out := make([]string, 0, len(wrapped))
	for i, line := range wrapped {
		prefix := strings.Repeat(" ", labelWidth)
		if i == 0 {
			prefix = keyText
		}
		out = append(out, prefix+" "+theme.theme.Style(role, line))
	}
	return out
}

func (m themeEditorModel) renderBody(width int, theme pickerTheme) []string {
	editorWidth := width
	previewWidth := 0
	if width >= 104 {
		editorWidth = 58
		previewWidth = width - editorWidth - 3
	}
	editor := m.renderEditorLines(editorWidth, theme)
	if previewWidth <= 0 {
		return append(editor, append([]string{""}, m.renderPreviewLines(width, theme)...)...)
	}
	preview := m.renderPreviewLines(previewWidth, theme)
	lines := max(len(editor), len(preview))
	out := make([]string, 0, lines)
	divider := theme.muted("|")
	for i := 0; i < lines; i++ {
		left := ""
		if i < len(editor) {
			left = editor[i]
		}
		right := ""
		if i < len(preview) {
			right = preview[i]
		}
		out = append(out, termstyle.PadRight(left, editorWidth)+" "+divider+" "+right)
	}
	return out
}

func (m themeEditorModel) renderEditorLines(width int, theme pickerTheme) []string {
	lines := []string{themeSectionHeader(theme, "Schema", width)}
	lines = append(lines, m.renderBaseRow(width, theme))
	for i, meta := range themeRoles {
		lines = append(lines, m.renderRoleRow(i+1, meta, width, theme))
	}
	if m.editMode {
		lines = append(lines, "")
		prompt := "raw " + m.selectedLabel() + " = " + m.editBuffer
		lines = append(lines, theme.theme.Style(termstyle.RoleSearch, termstyle.Truncate(prompt, width)))
	}
	return lines
}

func (m themeEditorModel) renderBaseRow(width int, theme pickerTheme) string {
	selected := m.cursor == 0
	cursor := "  "
	if selected {
		cursor = ">>"
	}
	live := m.currentTheme()
	line := theme.accent(cursor) + " " +
		termstyle.PadRight(live.Style(termstyle.RoleTitle, "base"), 13) + " " +
		termstyle.PadRight(live.Style(termstyle.RolePrimary, termstyle.Truncate(m.base, 18)), 18)
	if width >= 54 {
		hint := "starting palette (arrows switch)"
		line += " " + m.plain(theme, termstyle.Truncate(hint, max(0, width-termstyle.VisibleWidth(line)-1)))
	}
	return termstyle.PadRight(line, width)
}

func (m themeEditorModel) renderRoleRow(index int, meta themeRoleMeta, width int, theme pickerTheme) string {
	selected := m.cursor == index
	cursor := "  "
	if selected {
		cursor = ">>"
	}
	live := m.currentTheme()
	value := "(inherit)"
	if spec := strings.TrimSpace(m.values[meta.Role]); spec != "" {
		value = spec
	}
	line := theme.accent(cursor) + " " +
		termstyle.PadRight(live.Style(meta.Role, meta.Label), 13) + " " +
		termstyle.PadRight(live.Style(meta.Role, termstyle.Truncate(value, 18)), 18)
	if width >= 54 {
		line += " " + m.plain(theme, termstyle.Truncate(meta.Description, max(0, width-termstyle.VisibleWidth(line)-1)))
	}
	return termstyle.PadRight(line, width)
}

func (m themeEditorModel) renderPreviewLines(width int, theme pickerTheme) []string {
	live := m.currentTheme()
	picker := pickerTheme{theme: live}
	lines := []string{
		themeSectionHeader(theme, "Preview", width),
		picker.title("PASSAGE") + " " + picker.pill("DEV"),
		picker.muted(termstyle.Truncate("92 entries  /home/xbenc/.password-store", width)),
		picker.primary("/prod") + "  " + picker.counter(12, 92),
		picker.accent("> ") + picker.muted("  1 ") + picker.selected("occdev | gitea | token | password") + "  " + picker.accent("*") + " " + picker.muted("06-12 19:57"),
		"  " + picker.muted("  2 ") + picker.primary("pp | github | 0xbenc | mfa") + "  " + picker.accent("mfa") + " " + picker.muted("never"),
		picker.success("Password copied to clipboard (osc52)."),
		picker.warning("Clipboard copy failed: xclip failed"),
		"",
	}
	lines = append(lines, splitRendered(renderWorkflowShell(picker, clamp(width, 54, 100), workflowShell{
		Title:  "working",
		Body:   []string{picker.primary("copying occdev/example/password"), picker.muted("This will return to the picker.")},
		Footer: "esc cancel  ^C/^Q quit",
	}))...)
	lines = append(lines, m.renderPaletteLines(width, theme)...)
	return lines
}

func (m themeEditorModel) renderPaletteLines(width int, theme pickerTheme) []string {
	swatch := func(token string) string {
		if token == "" {
			return ""
		}
		code, err := termstyle.ParseStyleSpec(token)
		if err != nil {
			return theme.muted(token)
		}
		return termstyle.Apply(m.noColor, code, "###") + " " + m.plain(theme, token)
	}
	colWidth := max(12, width/2)
	pair := func(left, right string) string {
		line := termstyle.PadRight(swatch(left), colWidth) + swatch(right)
		return termstyle.PadRight(line, width)
	}
	lines := []string{
		themeSectionHeader(theme, "Palette", width),
		m.plain(theme, termstyle.Truncate("type a name below; swatch shows the result", width)),
		pair("default", ""),
		pair("black", "bright-black"),
		pair("red", "bright-red"),
		pair("green", "bright-green"),
		pair("yellow", "bright-yellow"),
		pair("blue", "bright-blue"),
		pair("magenta", "bright-magenta"),
		pair("cyan", "bright-cyan"),
		pair("white", "bright-white"),
	}
	styleTokens := []string{"bold", "dim", "italic", "underline", "reverse"}
	parts := make([]string, 0, len(styleTokens))
	for _, token := range styleTokens {
		code, err := termstyle.ParseStyleSpec(token)
		if err != nil {
			parts = append(parts, token)
			continue
		}
		parts = append(parts, termstyle.Apply(m.noColor, code, token))
	}
	lines = append(lines,
		m.plain(theme, "styles ")+strings.Join(parts, " "),
		m.plain(theme, termstyle.Truncate(`mix: "bold red"  bg-/fg-blue  raw 1;31`, width)),
	)
	return lines
}

func (m themeEditorModel) plain(theme pickerTheme, value string) string {
	switch m.tone {
	case toneBlack:
		return termstyle.Apply(m.noColor, "30", value)
	case toneWhite:
		return termstyle.Apply(m.noColor, "97", value)
	default:
		return theme.muted(value)
	}
}

func (m themeEditorModel) contrastChips() string {
	blackCode := "47;30"
	whiteCode := "40;97"
	switch m.tone {
	case toneBlack:
		blackCode += ";1;4"
	case toneWhite:
		whiteCode += ";1;4"
	}
	return termstyle.Apply(m.noColor, blackCode, " black ") +
		termstyle.Apply(m.noColor, whiteCode, " white ")
}

func (m *themeEditorModel) cycleTone() {
	m.tone = (m.tone + 1) % 3
	switch m.tone {
	case toneBlack:
		m.message = "basic text -> dark black"
	case toneWhite:
		m.message = "basic text -> bright white"
	default:
		m.message = "basic text -> theme default"
	}
}

func (m themeEditorModel) result() ThemeEditorResult {
	return ThemeEditorResult{
		Config: m.config(),
		Theme:  m.currentTheme(),
		Path:   m.configPath,
	}
}

func (m themeEditorModel) config() termstyle.ThemeConfig {
	cfg := termstyle.ThemeConfig{
		BaseName: m.base,
		Codes:    make(map[termstyle.Role]string),
		Specs:    make(map[termstyle.Role]string),
	}
	for _, meta := range themeRoles {
		spec := strings.TrimSpace(m.values[meta.Role])
		if spec == "" {
			continue
		}
		code, err := termstyle.ParseStyleSpec(spec)
		if err != nil {
			continue
		}
		cfg.Codes[meta.Role] = code
		cfg.Specs[meta.Role] = spec
	}
	return cfg
}

func (m themeEditorModel) currentTheme() termstyle.Theme {
	theme := m.baseTheme()
	theme.NoColor = m.noColor
	for _, meta := range themeRoles {
		spec := strings.TrimSpace(m.values[meta.Role])
		if spec == "" {
			continue
		}
		code, err := termstyle.ParseStyleSpec(spec)
		if err == nil {
			theme.Codes[meta.Role] = code
		}
	}
	return theme
}

// baseTheme is the selected starting palette, before role overrides.
func (m themeEditorModel) baseTheme() termstyle.Theme {
	if t, ok := termstyle.BuiltinTheme(m.base); ok {
		return t.Normalized()
	}
	return termstyle.TerminalTheme().Normalized()
}

// The editor's selectable list is the base-palette selector (cursor 0)
// followed by the role rows (cursor 1..len(themeRoles)).
func (m themeEditorModel) rowCount() int { return len(themeRoles) + 1 }

func (m *themeEditorModel) move(delta int) {
	rows := m.rowCount()
	m.cursor = (m.cursor + delta + rows) % rows
	m.message = ""
}

func (m *themeEditorModel) cycleCurrent(delta int) {
	if m.cursor == 0 {
		m.cycleBase(delta)
		return
	}
	role, ok := m.selectedRole()
	if !ok {
		return
	}
	current := strings.TrimSpace(m.values[role])
	index := presetIndex(current)
	next := (index + delta + len(stylePresets)) % len(stylePresets)
	m.values[role] = stylePresets[next].Spec
	m.message = string(role) + " = " + stylePresets[next].Label
}

// cycleBase steps the starting palette through the builtin themes; the role
// overrides layered on top are untouched, so switching base re-tints only the
// roles the user has not customized.
func (m *themeEditorModel) cycleBase(delta int) {
	names := termstyle.BuiltinThemeNames()
	index := 0
	for i, name := range names {
		if name == m.base {
			index = i
			break
		}
	}
	next := (index + delta + len(names)) % len(names)
	m.base = names[next]
	m.message = "base = " + m.base
}

func (m *themeEditorModel) startEdit() {
	role, ok := m.selectedRole()
	if !ok {
		m.message = "base switches with left/right"
		return
	}
	m.editMode = true
	m.editBuffer = m.values[role]
	m.message = "editing " + string(role)
}

func (m *themeEditorModel) clearCurrent() {
	if m.cursor == 0 {
		m.base = "terminal"
		m.message = "base = terminal (default)"
		return
	}
	if role, ok := m.selectedRole(); ok {
		delete(m.values, role)
		m.message = string(role) + " inherits from base"
	}
}

func (m *themeEditorModel) resetAll() {
	m.values = make(map[termstyle.Role]string)
	m.base = "terminal"
	m.message = "reset to defaults"
}

func (m themeEditorModel) selectedRole() (termstyle.Role, bool) {
	if m.cursor <= 0 {
		return "", false // base-palette row
	}
	index := m.cursor - 1
	if index >= len(themeRoles) {
		return "", false
	}
	return themeRoles[index].Role, true
}

func (m themeEditorModel) selectedLabel() string {
	if m.cursor == 0 {
		return "base"
	}
	index := m.cursor - 1
	if index < 0 || index >= len(themeRoles) {
		return ""
	}
	return themeRoles[index].Label
}

func presetIndex(spec string) int {
	spec = normalizeSpec(spec)
	for i, preset := range stylePresets {
		if normalizeSpec(preset.Spec) == spec {
			return i
		}
	}
	return 0
}

func normalizeSpec(spec string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(spec)), " "))
}

func themeSectionHeader(theme pickerTheme, label string, width int) string {
	label = strings.ToUpper(strings.TrimSpace(label))
	if label == "" {
		return theme.muted(strings.Repeat("-", min(width, 40)))
	}
	line := label
	if width > termstyle.VisibleWidth(label)+1 {
		line += " " + strings.Repeat("-", max(0, width-termstyle.VisibleWidth(label)-1))
	}
	return theme.accent(termstyle.Truncate(line, width))
}

func wrapPlain(value string, width int, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	lines := wrapText(value, width)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	return lines
}

var themeRoles = []themeRoleMeta{
	{Role: termstyle.RoleTitle, Label: "title", Description: "app name and modal titles"},
	{Role: termstyle.RolePrimary, Label: "primary", Description: "entry names and active filter"},
	{Role: termstyle.RoleSecondary, Label: "secondary", Description: "supporting emphasis"},
	{Role: termstyle.RoleAccent, Label: "accent", Description: "cursor, pins, badges"},
	{Role: termstyle.RoleMuted, Label: "muted", Description: "metadata, timestamps, help"},
	{Role: termstyle.RoleSubtle, Label: "subtle", Description: "low-contrast text"},
	{Role: termstyle.RoleForeground, Label: "foreground", Description: "normal text"},
	{Role: termstyle.RoleSelected, Label: "selected", Description: "selected entry text"},
	{Role: termstyle.RoleBorder, Label: "border", Description: "modal borders and rules"},
	{Role: termstyle.RoleSuccess, Label: "success", Description: "successful copy/status"},
	{Role: termstyle.RoleWarning, Label: "warning", Description: "warnings and copy failures"},
	{Role: termstyle.RoleDanger, Label: "danger", Description: "revealed secrets"},
	{Role: termstyle.RoleInfo, Label: "info", Description: "informational badges"},
	{Role: termstyle.RoleSearch, Label: "search", Description: "raw edit and search text"},
	{Role: termstyle.RolePill, Label: "pill", Description: "compact badges"},
}

var stylePresets = []stylePreset{
	{Label: "inherit", Spec: ""},
	{Label: "default", Spec: "default"},
	{Label: "black", Spec: "black"},
	{Label: "red", Spec: "red"},
	{Label: "green", Spec: "green"},
	{Label: "yellow", Spec: "yellow"},
	{Label: "blue", Spec: "blue"},
	{Label: "magenta", Spec: "magenta"},
	{Label: "cyan", Spec: "cyan"},
	{Label: "white", Spec: "white"},
	{Label: "bright black", Spec: "bright-black"},
	{Label: "bright red", Spec: "bright-red"},
	{Label: "bright green", Spec: "bright-green"},
	{Label: "bright yellow", Spec: "bright-yellow"},
	{Label: "bright blue", Spec: "bright-blue"},
	{Label: "bright magenta", Spec: "bright-magenta"},
	{Label: "bright cyan", Spec: "bright-cyan"},
	{Label: "bright white", Spec: "bright-white"},
	{Label: "bold default", Spec: "bold default"},
	{Label: "bold red", Spec: "bold red"},
	{Label: "bold green", Spec: "bold green"},
	{Label: "bold yellow", Spec: "bold yellow"},
	{Label: "bold blue", Spec: "bold blue"},
	{Label: "bold magenta", Spec: "bold magenta"},
	{Label: "bold cyan", Spec: "bold cyan"},
	{Label: "bold white", Spec: "bold white"},
	{Label: "dim", Spec: "dim"},
	{Label: "underline", Spec: "underline"},
	{Label: "reverse", Spec: "reverse"},
	{Label: "bold reverse", Spec: "bold reverse"},
	{Label: "plain", Spec: "plain"},
}
