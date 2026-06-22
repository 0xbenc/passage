package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/termstyle"
)

type Action string

const (
	ActionNone           Action = ""
	ActionCopy           Action = "copy"
	ActionReveal         Action = "reveal"
	ActionTOTP           Action = "totp"
	ActionTogglePin      Action = "toggle_pin"
	ActionClearClipboard Action = "clear_clipboard"
	ActionClearPins      Action = "clear_pins"
	ActionClearRecents   Action = "clear_recents"
	ActionDoctor         Action = "doctor"
	ActionKeys           Action = "keys"
	ActionQuit           Action = "quit"
)

type PickOptions struct {
	Input        io.Reader
	Output       io.Writer
	NoAltScreen  bool
	NoColor      bool
	Theme        termstyle.Theme
	ThemeFile    string
	Title        string
	Version      string
	StoreRoot    string
	Filter       string
	MFAOnly      bool
	Message      string
	RunAction    ActionRunner
	ThemeConfig  termstyle.ThemeConfig
	ThemePath    string
	ThemeWarning string
	SaveTheme    ThemeSaveFunc
}

type PickResult struct {
	Action  Action
	Entry   passstore.Entry
	Filter  string
	MFAOnly bool
}

type ActionRequest struct {
	Action  Action
	Entry   passstore.Entry
	Filter  string
	MFAOnly bool
}

type ActionOutcome struct {
	Message         string
	Entries         []passstore.Entry
	SecretTitle     string
	SecretKind      string
	Secret          string
	SecretRemaining int
	TextTitle       string
	TextLines       []string
	Err             error
}

type ActionRunner func(context.Context, ActionRequest) ActionOutcome

func Pick(ctx context.Context, entries []passstore.Entry, opts PickOptions) (PickResult, error) {
	theme, err := resolveTheme(opts.NoColor, opts.Theme, opts.ThemeFile)
	if err != nil {
		return PickResult{}, err
	}
	model := newPickerModel(entries, opts, theme)
	if ctx == nil {
		ctx = context.Background()
	}
	model.ctx = ctx
	programOptions := []tea.ProgramOption{tea.WithContext(ctx)}
	if opts.Input != nil {
		programOptions = append(programOptions, tea.WithInput(opts.Input))
	}
	if opts.Output != nil {
		programOptions = append(programOptions, tea.WithOutput(opts.Output))
	}
	final, err := tea.NewProgram(model, programOptions...).Run()
	if err != nil {
		return PickResult{}, err
	}
	picker, ok := final.(pickerModel)
	if !ok {
		return PickResult{}, nil
	}
	result := PickResult{
		Action:  picker.action,
		Filter:  picker.query,
		MFAOnly: picker.mfaOnly,
	}
	if picker.selected >= 0 && picker.selected < len(picker.filtered) {
		result.Entry = picker.entries[picker.filtered[picker.selected].Index]
	}
	return result, nil
}

type pickerModel struct {
	entries      []passstore.Entry
	filtered     []passstore.Ranked
	cursor       int
	scroll       int
	query        string
	mfaOnly      bool
	action       Action
	selected     int
	theme        termstyle.Theme
	noColor      bool
	title        string
	version      string
	storeRoot    string
	message      string
	messageErr   bool
	noAltScreen  bool
	width        int
	height       int
	ctx          context.Context
	runAction    ActionRunner
	busy         *pickerBusy
	modal        *pickerModal
	actionSeq    int
	activeID     int
	themeConfig  termstyle.ThemeConfig
	themePath    string
	themeWarning string
	saveTheme    ThemeSaveFunc
	themeEditor  *themeEditorModel
}

type pickerBusy struct {
	id        int
	title     string
	detail    string
	cancel    context.CancelFunc
	canceling bool
}

type pickerModal struct {
	title     string
	lines     []string
	footer    string
	danger    bool
	scroll    bool
	offset    int
	secret    bool
	remaining int
}

type actionDoneMsg struct {
	id      int
	request ActionRequest
	outcome ActionOutcome
}

type themeSaveDoneMsg struct {
	result ThemeSaveResult
	err    error
}

func newPickerModel(entries []passstore.Entry, opts PickOptions, theme termstyle.Theme) pickerModel {
	model := pickerModel{
		entries:      append([]passstore.Entry(nil), entries...),
		query:        strings.TrimSpace(opts.Filter),
		mfaOnly:      opts.MFAOnly,
		selected:     -1,
		theme:        theme.WithNoColor(theme.NoColor || opts.NoColor),
		noColor:      opts.NoColor,
		title:        defaultString(opts.Title, "passage"),
		version:      strings.TrimSpace(opts.Version),
		storeRoot:    opts.StoreRoot,
		message:      opts.Message,
		noAltScreen:  opts.NoAltScreen,
		width:        92,
		height:       28,
		ctx:          context.Background(),
		runAction:    opts.RunAction,
		themeConfig:  opts.ThemeConfig,
		themePath:    opts.ThemePath,
		themeWarning: opts.ThemeWarning,
		saveTheme:    opts.SaveTheme,
	}
	model.applyFilter()
	return model
}

func (m pickerModel) Init() tea.Cmd {
	return tea.RequestWindowSize
}

func (m pickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.height = msg.Height
		}
		m.ensureVisible()
	case actionDoneMsg:
		m.applyOutcome(msg.id, msg.outcome)
	case themeSaveDoneMsg:
		m.applyThemeSave(msg)
	case tea.KeyPressMsg:
		key := normalizedKey(msg)
		if m.themeEditor != nil {
			return m.updateThemeEditor(msg, key)
		}
		if m.busy != nil {
			return m.updateBusy(key)
		}
		if m.modal != nil {
			return m.updateModal(key)
		}
		switch key {
		case "ctrl+c", "esc", "ctrl+q":
			m.action = ActionQuit
			return m, tea.Quit
		case "enter":
			return m.trigger(m.primaryAction())
		case "up":
			m.move(-1)
		case "down", "ctrl+n":
			m.move(1)
		case "left", "right":
			// Ignore horizontal arrows. Some terminals report these as
			// literal escape text; never let them leak into the filter.
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
		case "ctrl+y":
			return m.trigger(defaultCopyAction(m.mfaOnly))
		case "ctrl+r":
			return m.trigger(defaultRevealAction(m.mfaOnly))
		case "ctrl+t":
			return m.trigger(m.ctrlTAction())
		case "ctrl+p":
			return m.trigger(ActionTogglePin)
		case "ctrl+f":
			m.mfaOnly = !m.mfaOnly
			m.applyFilter()
		case "ctrl+o":
			m.openThemeEditor()
		case "ctrl+x":
			return m.trigger(ActionClearClipboard)
		case "ctrl+u":
			return m.trigger(ActionClearPins)
		case "ctrl+e":
			return m.trigger(ActionClearRecents)
		case "ctrl+d":
			return m.trigger(ActionDoctor)
		case "ctrl+k":
			return m.trigger(ActionKeys)
		default:
			if safeTextInput(msg.Text) && !isControlKey(key) {
				m.query += msg.Text
				m.applyFilter()
			}
		}
	}
	return m, nil
}

func (m pickerModel) View() tea.View {
	width := max(48, m.width)
	if m.themeEditor != nil {
		editorTheme := pickerTheme{theme: m.themeEditor.currentTheme()}
		view := tea.NewView(strings.Join(m.themeEditor.view(width, editorTheme), "\n") + "\n")
		view.AltScreen = !m.noAltScreen
		return view
	}
	theme := pickerTheme{theme: m.theme}
	spec := m.computeLayout(theme)
	body := append([]string{}, spec.header...)
	body = append(body, m.listLines(spec.bodyWidth, theme, spec.listHeight)...)
	body = append(body, spec.tail...)
	view := tea.NewView(renderWorkflowShell(theme, spec.width, workflowShell{
		Title:  m.titleLine(),
		Body:   body,
		Footer: spec.footer,
	}))
	view.AltScreen = !m.noAltScreen
	return view
}

// layoutSpec is the single per-frame geometry budget. The picker computes it
// once in View, and the scroll math (pageSize / ensureVisible) derives the
// list height from the same source — so the height used to place the cursor
// can never drift from the height actually rendered. Previously three
// independent budgeters (View's availableListLines, pageSize, and the raw
// structural-line count) could disagree whenever a message or a busy/modal
// box was on screen, scrolling as if the list were two rows taller than it
// was.
type layoutSpec struct {
	width      int
	bodyWidth  int
	footer     string
	header     []string // lines above the list (message, status, filter)
	tail       []string // lines below the list (busy / modal boxes)
	listHeight int      // rows available for the entry list
}

func (m pickerModel) computeLayout(theme pickerTheme) layoutSpec {
	width := max(48, m.width)
	footer := pickerFooterText()
	bodyWidth := max(20, width-4)
	var header []string
	if m.message != "" {
		line := theme.success(termstyle.Truncate(m.message, bodyWidth))
		if m.messageErr {
			line = theme.warning(termstyle.Truncate(m.message, bodyWidth))
		}
		header = append(header, line, "")
	}
	header = append(header, m.statusLine(bodyWidth, theme), m.filterLine(bodyWidth, theme))
	var tail []string
	if m.busy != nil {
		tail = append(tail, "")
		tail = append(tail, m.busyLines(bodyWidth, theme)...)
	}
	if m.modal != nil {
		tail = append(tail, "")
		tail = append(tail, m.modalLines(bodyWidth, theme)...)
	}
	listHeight := max(1, m.height-pickerShellStructuralLines(footer)-len(header)-len(tail))
	return layoutSpec{
		width:      width,
		bodyWidth:  bodyWidth,
		footer:     footer,
		header:     header,
		tail:       tail,
		listHeight: listHeight,
	}
}

func (m pickerModel) titleLine() string {
	title := strings.ToUpper(defaultString(m.title, "passage"))
	if m.version != "" {
		title += "  " + m.version
	}
	return title
}

func pickerFooterText() string {
	return "type filters | arrows move | enter default | ^C/esc quit\n" +
		"^Y copy ^R reveal ^T totp/secret ^P pin ^F mfa ^O theme ^X clip ^U unpin ^E recent ^D doc ^K keys"
}

func pickerShellStructuralLines(footer string) int {
	lines := 2
	footer = strings.TrimRight(footer, "\n")
	if footer != "" {
		lines += 1 + len(strings.Split(footer, "\n"))
	}
	return lines
}

func (m pickerModel) statusLine(width int, theme pickerTheme) string {
	parts := []string{
		fmt.Sprintf("%d entries", len(m.entries)),
		m.storeRoot,
	}
	if m.mfaOnly {
		parts = append(parts, "MFA-only")
	}
	return theme.muted(termstyle.Truncate(strings.Join(parts, "  ·  "), width))
}

func (m pickerModel) filterLine(width int, theme pickerTheme) string {
	query := m.query
	if query == "" {
		query = "filter"
	}
	counter := theme.counter(len(m.filtered), len(m.entries))
	label := theme.muted("/")
	fieldWidth := max(8, width-termstyle.VisibleWidth(counter)-termstyle.VisibleWidth(label)-4)
	field := label + termstyle.PadRight(termstyle.Truncate(query, fieldWidth), fieldWidth)
	if m.query == "" {
		field = theme.muted(field)
	} else {
		field = theme.primary(field)
	}
	return field + "  " + counter
}

func (m pickerModel) listLines(width int, theme pickerTheme, available int) []string {
	available = max(1, available)
	if len(m.filtered) == 0 {
		if m.mfaOnly {
			return []string{theme.warning("No MFA-capable entries match.")}
		}
		return []string{theme.warning("No entries match.")}
	}
	start := clamp(m.scroll, 0, max(0, len(m.filtered)-available))
	entrySlots := available
	if start > 0 && entrySlots > 1 {
		entrySlots--
	}
	end := min(len(m.filtered), start+entrySlots)
	if end < len(m.filtered) && entrySlots > 1 {
		entrySlots--
		end = min(len(m.filtered), start+entrySlots)
	}
	var lines []string
	if start > 0 {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more above", start)))
	}
	for visibleIndex := start; visibleIndex < end; visibleIndex++ {
		ranked := m.filtered[visibleIndex]
		entry := m.entries[ranked.Index]
		lines = append(lines, m.renderEntryLine(entry, ranked.Positions, visibleIndex, width, theme))
	}
	if end < len(m.filtered) {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more below", len(m.filtered)-end)))
	}
	return lines
}

func (m pickerModel) renderEntryLine(entry passstore.Entry, positions []int, visibleIndex int, width int, theme pickerTheme) string {
	cursor := "  "
	if visibleIndex == m.cursor {
		cursor = theme.accent("> ")
	}
	markers := []string{}
	if entry.Pinned {
		markers = append(markers, "*")
	}
	if entry.HasMFA {
		markers = append(markers, "mfa")
	}
	markerText := strings.Join(markers, " ")
	lastUsed := "never"
	if entry.LastUsed > 0 {
		lastUsed = time.Unix(entry.LastUsed, 0).Format("01-02 15:04")
	}
	if markerText != "" {
		markerText = theme.accent(markerText)
	}
	right := strings.TrimSpace(markerText + " " + theme.muted(lastUsed))
	index := fmt.Sprintf("%3d ", visibleIndex+1)
	leftPrefix := cursor + theme.muted(index)
	leftWidth := max(12, width-termstyle.VisibleWidth(leftPrefix)-termstyle.VisibleWidth(right)-2)
	base := theme.primary
	if visibleIndex == m.cursor {
		base = theme.selected
	}
	title := highlightTitle(entry.Display, positions, leftWidth, base, theme)
	if right == "" {
		return leftPrefix + title
	}
	return leftPrefix + termstyle.PadRight(title, leftWidth) + "  " + right
}

// highlightTitle truncates display to width cells and styles it: matched runes
// (positions are rune indices into the full display) render in RoleSearch, the
// rest in the base role (RolePrimary, or RoleSelected on the cursor row). Each
// run is styled with a full Apply (open+reset), so styling can never bleed
// across a run or past the truncation. With no positions it is byte-identical
// to the previous single-Apply title, keeping the unfiltered view unchanged.
func highlightTitle(display string, positions []int, width int, base func(string) string, theme pickerTheme) string {
	truncated := termstyle.Truncate(display, width)
	if len(positions) == 0 {
		return base(truncated)
	}
	keptStr := truncated
	hasMarker := false
	if termstyle.VisibleWidth(display) > width && strings.HasSuffix(truncated, "~") {
		keptStr = strings.TrimSuffix(truncated, "~")
		hasMarker = true
	}
	runes := []rune(keptStr)
	hl := make([]bool, len(runes))
	for _, p := range positions {
		if p >= 0 && p < len(runes) {
			hl[p] = true
		}
	}
	var b strings.Builder
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && hl[j] == hl[i] {
			j++
		}
		seg := string(runes[i:j])
		if hl[i] {
			b.WriteString(theme.search(seg))
		} else {
			b.WriteString(base(seg))
		}
		i = j
	}
	if hasMarker {
		b.WriteString(base("~"))
	}
	return b.String()
}

func (m *pickerModel) applyFilter() {
	// Rank is the single source of truth shared with the CLI auto-run path,
	// so a filter that the picker treats as one match is the same one the
	// auto-run path would act on.
	m.filtered = passstore.Rank(m.entries, m.query, m.mfaOnly)
	if len(m.filtered) == 0 {
		m.cursor = 0
		m.scroll = 0
		return
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureVisible()
}

func (m *pickerModel) move(delta int) {
	if len(m.filtered) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	m.ensureVisible()
}

func (m *pickerModel) ensureVisible() {
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

// pageSize is the number of entry rows currently visible — the real list
// height from computeLayout, so paging and the scroll window stay in lockstep
// with what View renders even when a message or busy/modal box is shown.
func (m pickerModel) pageSize() int {
	return m.computeLayout(pickerTheme{theme: m.theme}).listHeight
}

func (m pickerModel) trigger(action Action) (pickerModel, tea.Cmd) {
	if m.runAction == nil {
		switch action {
		case ActionClearClipboard, ActionClearPins, ActionClearRecents, ActionDoctor, ActionKeys:
			m.action = action
			return m, tea.Quit
		default:
			return m.finish(action), tea.Quit
		}
	}
	return m.startAction(action)
}

func (m pickerModel) startAction(action Action) (pickerModel, tea.Cmd) {
	entry, ok := m.selectedEntry()
	if actionNeedsEntry(action) && !ok {
		m.message = "No entry selected."
		m.messageErr = true
		return m, nil
	}
	actionCtx, cancel := context.WithCancel(m.ctx)
	m.actionSeq++
	id := m.actionSeq
	m.activeID = id
	req := ActionRequest{
		Action:  action,
		Entry:   entry,
		Filter:  m.query,
		MFAOnly: m.mfaOnly,
	}
	m.message = ""
	m.messageErr = false
	m.modal = nil
	m.busy = &pickerBusy{
		id:     id,
		title:  actionBusyTitle(action),
		detail: entry.Path,
		cancel: cancel,
	}
	return m, func() tea.Msg {
		return actionDoneMsg{id: id, request: req, outcome: m.runAction(actionCtx, req)}
	}
}

func (m pickerModel) updateBusy(key string) (pickerModel, tea.Cmd) {
	switch key {
	case "esc":
		if m.busy.cancel != nil {
			m.busy.cancel()
		}
		detail := m.busy.detail
		m.busy = nil
		m.activeID = 0
		m.message = "Canceled."
		if detail != "" {
			m.message = "Canceled " + detail + "."
		}
		m.messageErr = true
	case "ctrl+c", "ctrl+q":
		if m.busy.cancel != nil {
			m.busy.cancel()
		}
		m.action = ActionQuit
		return m, tea.Quit
	}
	return m, nil
}

func (m pickerModel) updateThemeEditor(msg tea.KeyPressMsg, key string) (pickerModel, tea.Cmd) {
	switch key {
	case "ctrl+c", "ctrl+q":
		m.action = ActionQuit
		return m, tea.Quit
	}
	editor := *m.themeEditor
	updated, done := editor.update(msg)
	m.themeEditor = &updated
	if !done {
		return m, nil
	}
	if updated.canceled {
		m.themeEditor = nil
		m.message = "Theme edit cancelled."
		m.messageErr = false
		return m, nil
	}
	result := updated.result()
	m.themeEditor = nil
	if m.saveTheme == nil {
		m.themeConfig = result.Config
		m.theme = result.Theme.WithNoColor(result.Theme.NoColor || m.noColor)
		m.message = "Theme applied for this session."
		m.messageErr = false
		return m, nil
	}
	return m, func() tea.Msg {
		saved, err := m.saveTheme(m.ctx, result)
		return themeSaveDoneMsg{result: saved, err: err}
	}
}

func (m *pickerModel) openThemeEditor() {
	editor := newThemeEditorModel(ThemeEditorOptions{
		NoAltScreen: m.noAltScreen,
		NoColor:     m.noColor || m.theme.NoColor,
		Config:      m.themeConfig,
		ConfigPath:  m.themePath,
		Warning:     m.themeWarning,
	})
	editor.width = m.width
	editor.height = m.height
	m.themeEditor = &editor
	m.message = ""
	m.messageErr = false
}

func (m *pickerModel) applyThemeSave(msg themeSaveDoneMsg) {
	if msg.err != nil {
		m.message = "Theme save failed: " + msg.err.Error()
		m.messageErr = true
		return
	}
	m.themeConfig = msg.result.Config
	if msg.result.Path != "" {
		m.themePath = msg.result.Path
	}
	if !msg.result.Theme.IsZero() {
		m.theme = msg.result.Theme.WithNoColor(msg.result.Theme.NoColor || m.noColor)
	}
	m.themeWarning = ""
	m.messageErr = false
	if msg.result.Message != "" {
		m.message = msg.result.Message
		return
	}
	if msg.result.Changed {
		m.message = "Theme saved to " + msg.result.Path + "."
		return
	}
	m.message = "Theme unchanged."
}

func (m pickerModel) updateModal(key string) (pickerModel, tea.Cmd) {
	if m.modal.scroll {
		switch key {
		case "up":
			m.modal.offset = max(0, m.modal.offset-1)
			return m, nil
		case "down":
			m.modal.offset = min(m.maxModalOffset(), m.modal.offset+1)
			return m, nil
		case "pgup":
			m.modal.offset = max(0, m.modal.offset-m.modalPageSize())
			return m, nil
		case "pgdown":
			m.modal.offset = min(m.maxModalOffset(), m.modal.offset+m.modalPageSize())
			return m, nil
		case "home":
			m.modal.offset = 0
			return m, nil
		case "end":
			m.modal.offset = m.maxModalOffset()
			return m, nil
		}
	}
	m.modal = nil
	return m, nil
}

func (m *pickerModel) applyOutcome(id int, out ActionOutcome) {
	if id != m.activeID {
		return
	}
	m.activeID = 0
	if m.busy != nil && m.busy.cancel != nil {
		m.busy.cancel()
	}
	m.busy = nil
	if len(out.Entries) > 0 {
		m.entries = append([]passstore.Entry(nil), out.Entries...)
		m.applyFilter()
	}
	m.message = out.Message
	m.messageErr = false
	if out.Err != nil {
		m.message = out.Err.Error()
		m.messageErr = true
	}
	if out.Secret != "" {
		lines := wrapSecret(out.Secret, max(20, m.width-8))
		if out.SecretRemaining > 0 {
			lines = append(lines, "", fmt.Sprintf("%ds remaining", out.SecretRemaining))
		}
		m.modal = &pickerModal{
			title:     defaultString(out.SecretTitle, "Reveal"),
			lines:     lines,
			footer:    "press any key to return",
			danger:    out.SecretKind == "password",
			secret:    true,
			remaining: out.SecretRemaining,
		}
	}
	if len(out.TextLines) > 0 {
		m.modal = &pickerModal{
			title:  defaultString(out.TextTitle, "passage"),
			lines:  append([]string(nil), out.TextLines...),
			footer: "up/down scroll  enter/esc close",
			scroll: true,
		}
	}
}

func (m pickerModel) selectedEntry() (passstore.Entry, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return passstore.Entry{}, false
	}
	return m.entries[m.filtered[m.cursor].Index], true
}

func (m pickerModel) finish(action Action) pickerModel {
	m.action = action
	m.selected = m.cursor
	return m
}

func (m pickerModel) busyLines(width int, theme pickerTheme) []string {
	title := m.busy.title
	if m.busy.detail != "" {
		title += " " + m.busy.detail
	}
	body := []string{theme.primary(title)}
	if m.busy.canceling {
		body = append(body, "", theme.muted("Cancel requested. Waiting for the command to stop."))
	} else {
		body = append(body, "", theme.muted("This will return to the picker."))
	}
	return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
		Title:  "working",
		Body:   body,
		Footer: "esc cancel  ^C/^Q quit",
	}))
}

func (m pickerModel) modalLines(width int, theme pickerTheme) []string {
	modal := m.modal
	lines := modal.lines
	if modal.scroll {
		page := m.modalPageSize()
		start := clamp(modal.offset, 0, m.maxModalOffset())
		end := min(len(lines), start+page)
		body := make([]string, 0, page+2)
		if start > 0 {
			body = append(body, theme.muted("more above"))
		}
		body = append(body, lines[start:end]...)
		if end < len(lines) {
			body = append(body, theme.muted("more below"))
		}
		lines = body
	}
	return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
		Title:  modal.title,
		Body:   lines,
		Footer: modal.footer,
		Danger: modal.danger,
	}))
}

func (m pickerModel) modalPageSize() int {
	return max(5, m.height/2)
}

func (m pickerModel) maxModalOffset() int {
	if m.modal == nil {
		return 0
	}
	return max(0, len(m.modal.lines)-m.modalPageSize())
}

func actionNeedsEntry(action Action) bool {
	switch action {
	case ActionCopy, ActionReveal, ActionTOTP, ActionTogglePin:
		return true
	default:
		return false
	}
}

func actionBusyTitle(action Action) string {
	switch action {
	case ActionCopy:
		return "copying"
	case ActionReveal:
		return "decrypting"
	case ActionTOTP:
		return "generating TOTP for"
	case ActionTogglePin:
		return "updating pin for"
	case ActionClearClipboard:
		return "clearing clipboard"
	case ActionClearPins:
		return "clearing pins"
	case ActionClearRecents:
		return "clearing recents"
	case ActionDoctor:
		return "running doctor"
	case ActionKeys:
		return "loading keys"
	default:
		return "working"
	}
}

func defaultPrimaryAction(mfaOnly bool) Action {
	if mfaOnly {
		return ActionTOTP
	}
	return ActionCopy
}

func (m pickerModel) primaryAction() Action {
	entry, ok := m.selectedEntry()
	if ok && isMFASecretEntry(entry) {
		return ActionTOTP
	}
	return defaultPrimaryAction(m.mfaOnly)
}

func (m pickerModel) ctrlTAction() Action {
	entry, ok := m.selectedEntry()
	if ok && isMFASecretEntry(entry) {
		return ActionReveal
	}
	return ActionTOTP
}

func isMFASecretEntry(entry passstore.Entry) bool {
	return entry.Path == "mfa" || strings.HasSuffix(entry.Path, "/mfa")
}

func defaultCopyAction(mfaOnly bool) Action {
	if mfaOnly {
		return ActionTOTP
	}
	return ActionCopy
}

func defaultRevealAction(mfaOnly bool) Action {
	if mfaOnly {
		return ActionTOTP
	}
	return ActionReveal
}

func isControlKey(key string) bool {
	return strings.HasPrefix(key, "ctrl+") ||
		key == "esc" ||
		key == "tab" ||
		key == "shift+tab" ||
		key == "enter" ||
		key == "backspace"
}

func normalizedKey(msg tea.KeyPressMsg) string {
	key := msg.String()
	if key == "" {
		key = msg.Keystroke()
	}
	switch key {
	case "\x1b[A", "\x1bOA":
		return "up"
	case "\x1b[B", "\x1bOB":
		return "down"
	case "\x1b[C", "\x1bOC":
		return "right"
	case "\x1b[D", "\x1bOD":
		return "left"
	case "\x1b[5~":
		return "pgup"
	case "\x1b[6~":
		return "pgdown"
	case "\x1b[H", "\x1b[1~", "\x1bOH":
		return "home"
	case "\x1b[F", "\x1b[4~", "\x1bOF":
		return "end"
	}
	text := msg.Text
	switch text {
	case "\x1b[A", "\x1bOA":
		return "up"
	case "\x1b[B", "\x1bOB":
		return "down"
	case "\x1b[C", "\x1bOC":
		return "right"
	case "\x1b[D", "\x1bOD":
		return "left"
	case "\x1b[5~":
		return "pgup"
	case "\x1b[6~":
		return "pgdown"
	case "\x1b[H", "\x1b[1~", "\x1bOH":
		return "home"
	case "\x1b[F", "\x1b[4~", "\x1bOF":
		return "end"
	}
	return key
}

func safeTextInput(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return false
		}
	}
	return true
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
