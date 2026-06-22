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
	Glyphs       termstyle.GlyphSet
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
	SecretPeriod    int
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
	clock        func() time.Time
	runAction    ActionRunner
	busy         *pickerBusy
	modal        *pickerModal
	actionSeq    int
	activeID     int
	tick         int  // animation frame counter, advanced by tickMsg
	ticking      bool // whether a tick loop is currently in flight
	themeConfig  termstyle.ThemeConfig
	themePath    string
	themeWarning string
	saveTheme    ThemeSaveFunc
	themeEditor  *themeEditorModel
	glyphs       termstyle.GlyphSet
	secretHidden bool // secret modal blanked because the terminal lost focus
}

type pickerBusy struct {
	id        int
	title     string
	detail    string
	cancel    context.CancelFunc
	canceling bool
	started   time.Time
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
	// expires/period drive the live countdown (TOTP only). When expires is
	// non-zero, remaining is recomputed from the wall clock each tick so the
	// bar tracks real OTP validity rather than a blind decrement, and the
	// modal auto-dismisses at zero.
	expires time.Time
	period  int
}

type actionDoneMsg struct {
	id      int
	request ActionRequest
	outcome ActionOutcome
}

// tickMsg drives motion (the busy spinner and the secret countdown). It is a
// distinct message type, not a KeyPressMsg, so it never dismisses the secret
// modal. The clock is gated: it runs only while a busy action or a live secret
// countdown is on screen, re-issued each tick and stopped the instant neither
// is — so an idle picker never redraws, with no goroutines or leaked timers.
type tickMsg struct{ at time.Time }

const tickInterval = 100 * time.Millisecond

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg{at: t} })
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
		clock:        time.Now,
		runAction:    opts.RunAction,
		themeConfig:  opts.ThemeConfig,
		themePath:    opts.ThemePath,
		themeWarning: opts.ThemeWarning,
		saveTheme:    opts.SaveTheme,
		glyphs:       opts.Glyphs,
	}
	if len(model.glyphs.Spinner) == 0 {
		model.glyphs = termstyle.DefaultGlyphs()
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
	case tea.BlurMsg:
		// Alt-tab away: blank a revealed secret so it is not left on screen
		// for an unattended terminal. Defense-in-depth atop the countdown.
		if m.modal != nil && m.modal.secret {
			m.secretHidden = true
		}
	case tea.FocusMsg:
		m.secretHidden = false
	case tickMsg:
		m.tick++
		m.refreshSecretCountdown()
		if !m.tickActive() {
			m.ticking = false
			return m, nil
		}
		return m, tickCmd()
	case actionDoneMsg:
		m.applyOutcome(msg.id, msg.outcome)
		return m, m.maybeStartTick()
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
	if spec.detailWidth > 0 && m.busy == nil && m.modal == nil {
		listLines := m.listLines(spec.listWidth, theme, spec.listHeight)
		detail := m.detailPane(spec.detailWidth, theme)
		sep := " " + theme.style(termstyle.RoleBorder, "│") + " "
		body = append(body, joinColumns(listLines, detail, spec.listWidth, spec.detailWidth, sep, spec.listHeight)...)
	} else {
		body = append(body, m.listLines(spec.bodyWidth, theme, spec.listHeight)...)
	}
	body = append(body, spec.tail...)
	view := tea.NewView(renderWorkflowShell(theme, spec.width, workflowShell{
		Title:  m.titleLine(),
		Body:   body,
		Footer: spec.footer,
	}))
	view.AltScreen = !m.noAltScreen
	// Ask the terminal to report focus so a revealed secret can be blanked
	// when the user alt-tabs away.
	view.ReportFocus = true
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
	width       int
	bodyWidth   int
	footer      string
	header      []string // lines above the list (message, status, filter)
	tail        []string // lines below the list (busy / modal boxes)
	listHeight  int      // rows available for the entry list
	listWidth   int      // width of the list column (== bodyWidth when narrow)
	detailWidth int      // width of the detail pane, or 0 when narrow
}

// detailBreakpoint is the terminal width at and above which the picker shows a
// side detail pane. detailSepWidth is the visible width of the column
// separator (" │ "); listWidth + detailSepWidth + detailWidth == bodyWidth.
const (
	detailBreakpoint = 92
	detailSepWidth   = 3
)

func (m pickerModel) computeLayout(theme pickerTheme) layoutSpec {
	width := max(48, m.width)
	footer := pickerFooterText()
	bodyWidth := max(20, width-4)
	listWidth := bodyWidth
	detailWidth := 0
	if width >= detailBreakpoint {
		dw := clamp(bodyWidth/3, 28, 44)
		lw := bodyWidth - dw - detailSepWidth
		if lw >= 40 {
			listWidth = lw
			detailWidth = dw
		}
	}
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
		width:       width,
		bodyWidth:   bodyWidth,
		footer:      footer,
		header:      header,
		tail:        tail,
		listHeight:  listHeight,
		listWidth:   listWidth,
		detailWidth: detailWidth,
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

// detailPane renders the side panel shown at wide widths: the selected entry's
// full (Sanitized) path, its pin/MFA state, when it was last used, and the
// primary action hints. It reads only already-loaded metadata — it never
// decrypts or runs an action, so opening the picker stays cheap and a revealed
// secret can never reach this pane.
func (m pickerModel) detailPane(width int, theme pickerTheme) []string {
	entry, ok := m.selectedEntry()
	if !ok {
		return []string{theme.muted(termstyle.Truncate("no entry selected", width))}
	}
	var lines []string
	for _, ln := range hardWrap(termstyle.Sanitize(entry.Path), width) {
		lines = append(lines, theme.primary(termstyle.Truncate(ln, width)))
	}
	lines = append(lines, "")
	state := []string{}
	if entry.Pinned {
		state = append(state, "★ pinned")
	}
	if entry.HasMFA {
		state = append(state, "mfa")
	}
	if len(state) > 0 {
		lines = append(lines, theme.accent(termstyle.Truncate(strings.Join(state, "   "), width)))
	}
	lines = append(lines, theme.muted(termstyle.Truncate("used "+humanizeRelative(entry.LastUsed, m.now()), width)))
	lines = append(lines, "")
	for _, ln := range wrapText("enter copy · ^R reveal · ^T totp · ^P pin", width) {
		lines = append(lines, theme.muted(termstyle.Truncate(ln, width)))
	}
	return lines
}

func (m pickerModel) now() time.Time {
	if m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// humanizeRelative renders a coarse "time ago" for the last-used timestamp.
func humanizeRelative(unix int64, now time.Time) string {
	if unix <= 0 {
		return "never"
	}
	d := now.Sub(time.Unix(unix, 0))
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return time.Unix(unix, 0).Format("2006-01-02")
	}
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

// tickActive reports whether motion should keep running: a busy action is in
// flight, or a secret countdown is still on screen with time left.
func (m pickerModel) tickActive() bool {
	if m.busy != nil {
		return true
	}
	return m.modal != nil && m.modal.secret && m.modal.remaining > 0
}

// maybeStartTick starts the tick loop if motion is needed and no loop is
// already running, returning the command to issue (or nil). Returning nil when
// already ticking is what keeps the loop single — never double-speed.
func (m *pickerModel) maybeStartTick() tea.Cmd {
	if m.tickActive() && !m.ticking {
		m.ticking = true
		return tickCmd()
	}
	return nil
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
		id:      id,
		title:   actionBusyTitle(action),
		detail:  entry.Path,
		cancel:  cancel,
		started: m.now(),
	}
	actionCmd := func() tea.Msg {
		return actionDoneMsg{id: id, request: req, outcome: m.runAction(actionCtx, req)}
	}
	return m, tea.Batch(actionCmd, m.maybeStartTick())
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
		m.secretHidden = false
		lines := wrapSecret(out.Secret, max(20, m.width-8))
		modal := &pickerModal{
			title:     defaultString(out.SecretTitle, "Reveal"),
			lines:     lines,
			footer:    "press any key to return",
			danger:    out.SecretKind == "password",
			secret:    true,
			remaining: out.SecretRemaining,
		}
		// Only a TOTP carries a real validity window, so only it gets a live
		// countdown. A standalone password reveal has no meaningful timer.
		if out.SecretKind == "totp" && out.SecretRemaining > 0 {
			period := out.SecretPeriod
			if period <= 0 {
				period = out.SecretRemaining
			}
			modal.period = period
			modal.expires = m.now().Add(time.Duration(out.SecretRemaining) * time.Second)
		}
		m.modal = modal
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

// refreshSecretCountdown recomputes a live secret modal's remaining seconds
// from the wall clock and dismisses the modal when the window has elapsed.
// Recomputing (rather than decrementing a counter) keeps the bar honest across
// a suspend/resume and never disagrees with the real OTP validity.
func (m *pickerModel) refreshSecretCountdown() {
	if m.modal == nil || m.modal.expires.IsZero() {
		return
	}
	d := m.modal.expires.Sub(m.now())
	if d <= 0 {
		m.modal = nil
		return
	}
	m.modal.remaining = int((d + time.Second - 1) / time.Second) // ceil to seconds
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
	// The spinner frame advances with the gated tick, so a slow gpg reads as
	// working rather than hung. Elapsed is derived here (View side) from the
	// start time, keeping the model snapshot itself deterministic.
	spinner := theme.accent(m.glyphs.Frame(m.tick))
	body := []string{strings.TrimSpace(spinner + " " + theme.primary(title))}
	status := "This will return to the picker."
	if m.busy.canceling {
		status = "Cancel requested. Waiting for the command to stop."
	}
	if elapsed := m.busyElapsed(); elapsed != "" {
		status = elapsed + "  ·  " + status
	}
	body = append(body, "", theme.muted(status))
	return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
		Title:  "working",
		Body:   body,
		Footer: "esc cancel  ^C/^Q quit",
	}))
}

func (m pickerModel) busyElapsed() string {
	if m.busy == nil || m.busy.started.IsZero() {
		return ""
	}
	secs := int(m.now().Sub(m.busy.started).Seconds())
	if secs < 0 {
		secs = 0
	}
	return fmt.Sprintf("%ds elapsed", secs)
}

func (m pickerModel) modalLines(width int, theme pickerTheme) []string {
	modal := m.modal
	if modal.secret && m.secretHidden {
		return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
			Title:  modal.title,
			Body:   []string{theme.muted("hidden — focus the terminal to show")},
			Footer: modal.footer,
			Danger: modal.danger,
		}))
	}
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
	if !modal.expires.IsZero() && modal.remaining > 0 {
		lines = append(append([]string(nil), lines...), "", m.countdownLine(theme))
	}
	return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
		Title:  modal.title,
		Body:   lines,
		Footer: modal.footer,
		Danger: modal.danger,
	}))
}

// countdownLine renders the live "Ns ▰▰▰▱▱" bar for a secret modal, colored by
// urgency (success -> warning -> danger) as the window drains.
func (m pickerModel) countdownLine(theme pickerTheme) string {
	remaining := m.modal.remaining
	total := m.modal.period
	if total <= 0 {
		total = remaining
	}
	role := termstyle.UrgencyRole(remaining, total)
	bar := m.glyphs.Bar(remaining, total, 14)
	return theme.style(role, fmt.Sprintf("%2ds ", remaining)+bar)
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
