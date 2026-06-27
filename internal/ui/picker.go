package ui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
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
	ActionNew            Action = "new"
	ActionGenerate       Action = "generate"
	ActionEdit           Action = "edit"
	ActionRemove         Action = "remove"
	ActionTrust          Action = "trust"
	ActionImport         Action = "import"
	ActionImportSecret   Action = "import_secret"
	ActionQuit           Action = "quit"
)

// isGapAction reports whether an action must run with the TUI torn down so a
// child process (pinentry for local-signing, $EDITOR) can own the real
// terminal. The picker quits returning the request; the CLI runs it in the gap
// and relaunches the picker (Pattern A).
func isGapAction(action Action) bool {
	return action == ActionEdit || action == ActionTrust || action == ActionImport || action == ActionImportSecret
}

// isWriteAction reports whether an action mutates the store (used to widen the
// action timeout, since encryption + a possible git commit-signing prompt can
// outlast the read timeout).
func IsWriteAction(action Action) bool {
	switch action {
	case ActionNew, ActionGenerate, ActionRemove:
		return true
	default:
		return false
	}
}

type PickOptions struct {
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Theme       termstyle.Theme
	ThemeFile   string
	Title       string
	Version     string
	StoreRoot   string
	Filter      string
	MFAOnly     bool
	Message     string
	MessageErr  bool
	// SelectPath restores the cursor to this entry path after a relaunch, so a
	// Pattern-A gap action does not reset the user's place in the list.
	SelectPath   string
	RunAction    ActionRunner
	ThemeConfig  termstyle.ThemeConfig
	ThemePath    string
	ThemeWarning string
	SaveTheme    ThemeSaveFunc
	Glyphs       termstyle.GlyphSet
	// ClearClipboard quietly clears the clipboard when the armed countdown
	// elapses — distinct from the ActionClearClipboard action so it does not
	// flash a busy box.
	ClearClipboard func(context.Context) error
	// LoadAccess computes each entry path's write verdict
	// (writable/read_only/no_access). It runs asynchronously after launch so the
	// cold start stays instant on a large store; badges appear once it returns.
	LoadAccess func(context.Context) map[string]string
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
	// Composer-supplied fields for ActionNew / ActionGenerate.
	NewPath   string
	Content   []byte
	Generate  bool
	Length    int
	NoSymbols bool
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
	// ClipArmed marks that a value was placed on the clipboard; the picker
	// shows an armed pill counting down ClipRemaining seconds (named by
	// ClipTool) and clears the clipboard at zero while it is open. Never
	// carries the secret itself.
	ClipArmed     bool
	ClipRemaining int
	ClipTool      string
	Err           error
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
	entries        []passstore.Entry
	filtered       []passstore.Ranked
	cursor         int
	scroll         int
	query          string
	mfaOnly        bool
	action         Action
	selected       int
	theme          termstyle.Theme
	noColor        bool
	title          string
	version        string
	storeRoot      string
	message        string
	messageErr     bool
	messageExpires time.Time // when a transient notice fades; zero = persistent
	noAltScreen    bool
	width          int
	height         int
	ctx            context.Context
	clock          func() time.Time
	runAction      ActionRunner
	busy           *pickerBusy
	modal          *pickerModal
	confirm        *pickerConfirm
	actionSeq      int
	activeID       int
	tick           int  // animation frame counter, advanced by tickMsg
	ticking        bool // whether a tick loop is currently in flight
	themeConfig    termstyle.ThemeConfig
	themePath      string
	themeWarning   string
	saveTheme      ThemeSaveFunc
	themeEditor    *themeEditorModel
	glyphs         termstyle.GlyphSet
	secretHidden   bool // revealed secret (modal or composer) blanked because the terminal lost focus
	help           bool // the ? key reference overlay is open
	clip           *clipState
	clearClipboard func(context.Context) error
	composer       *composerModel
	access         map[string]string // entry path -> write verdict (async-loaded)
	loadAccess     func(context.Context) map[string]string
}

// accessLoadedMsg delivers the asynchronously-computed write verdicts.
type accessLoadedMsg struct{ access map[string]string }

// clipState tracks an armed clipboard: which tool holds it and when the
// auto-clear fires. Recomputed from the wall clock, never holds the secret.
type clipState struct {
	tool    string
	expires time.Time
}

type clipClearedMsg struct{ err error }

type pickerBusy struct {
	id        int
	title     string
	detail    string
	cancel    context.CancelFunc
	canceling bool
	started   time.Time
}

// pickerConfirm is a pending destructive action awaiting a y/esc confirmation,
// so a single chord can no longer wipe curated state (pins / recents).
type pickerConfirm struct {
	action Action
	prompt string
	detail string
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
		entries:        append([]passstore.Entry(nil), entries...),
		query:          strings.TrimSpace(opts.Filter),
		mfaOnly:        opts.MFAOnly,
		selected:       -1,
		theme:          theme.WithNoColor(theme.NoColor || opts.NoColor),
		noColor:        opts.NoColor,
		title:          defaultString(opts.Title, "passage"),
		version:        strings.TrimSpace(opts.Version),
		storeRoot:      opts.StoreRoot,
		message:        opts.Message,
		messageErr:     opts.MessageErr,
		noAltScreen:    opts.NoAltScreen,
		width:          92,
		height:         28,
		ctx:            context.Background(),
		clock:          time.Now,
		runAction:      opts.RunAction,
		themeConfig:    opts.ThemeConfig,
		themePath:      opts.ThemePath,
		themeWarning:   opts.ThemeWarning,
		saveTheme:      opts.SaveTheme,
		glyphs:         opts.Glyphs,
		clearClipboard: opts.ClearClipboard,
		loadAccess:     opts.LoadAccess,
	}
	if len(model.glyphs.Spinner) == 0 {
		model.glyphs = termstyle.DefaultGlyphs()
	}
	model.applyFilter()
	if path := strings.TrimSpace(opts.SelectPath); path != "" {
		for i, ranked := range model.filtered {
			if model.entries[ranked.Index].Path == path {
				model.cursor = i
				break
			}
		}
		model.ensureVisible()
	}
	return model
}

func (m pickerModel) Init() tea.Cmd {
	if m.loadAccess != nil {
		return tea.Batch(tea.RequestWindowSize, m.loadAccessCmd())
	}
	return tea.RequestWindowSize
}

func (m pickerModel) loadAccessCmd() tea.Cmd {
	load := m.loadAccess
	ctx := m.ctx
	return func() tea.Msg {
		return accessLoadedMsg{access: load(ctx)}
	}
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
		// Covers both the reveal modal and a composer secret shown via ^R.
		if (m.modal != nil && m.modal.secret) || (m.composer != nil && !m.composer.secret.masked) {
			m.secretHidden = true
		}
	case tea.FocusMsg:
		m.secretHidden = false
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg.Mouse()), nil
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg.Mouse()), nil
	case tickMsg:
		m.tick++
		m.refreshSecretCountdown()
		m.refreshMessage()
		clearCmd := m.refreshClip()
		if !m.tickActive() {
			m.ticking = false
			return m, clearCmd
		}
		return m, tea.Batch(tickCmd(), clearCmd)
	case clipClearedMsg:
		if msg.err == nil {
			m.setNotice("Clipboard auto-cleared.", false)
		}
		return m, m.maybeStartTick()
	case actionDoneMsg:
		m.applyOutcome(msg.id, msg.outcome)
		return m, m.maybeStartTick()
	case accessLoadedMsg:
		m.access = msg.access
		return m, nil
	case themeSaveDoneMsg:
		m.applyThemeSave(msg)
	case tea.KeyPressMsg:
		key := normalizedKey(msg)
		if m.themeEditor != nil {
			return m.updateThemeEditor(msg, key)
		}
		if m.composer != nil {
			return m.updateComposer(msg, key)
		}
		if m.help {
			if key == "ctrl+c" || key == "ctrl+q" {
				m.action = ActionQuit
				return m, tea.Quit
			}
			m.help = false
			return m, nil
		}
		if m.busy != nil {
			return m.updateBusy(key)
		}
		if m.modal != nil {
			return m.updateModal(key)
		}
		if m.confirm != nil {
			return m.updateConfirm(key)
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
			m.clip = nil // manual clear disarms the auto-clear pill
			return m.trigger(ActionClearClipboard)
		case "ctrl+u":
			return m.startConfirm(ActionClearPins), nil
		case "ctrl+e":
			return m.startConfirm(ActionClearRecents), nil
		case "ctrl+d":
			return m.trigger(ActionDoctor)
		case "ctrl+k":
			return m.trigger(ActionKeys)
		default:
			// Uppercase command keys act on the selection regardless of the
			// filter. Fuzzy matching is case-insensitive, so uppercase is never
			// needed to filter — leaving lowercase free to always type, so you
			// can narrow with a lowercase filter then act with a shifted key.
			switch msg.Text {
			case "N":
				return m.openComposer(composePassword), nil
			case "G":
				return m.openComposer(composeGenerate), nil
			case "E":
				return m.trigger(ActionEdit)
			case "T":
				return m.trigger(ActionTrust)
			case "I":
				return m.trigger(ActionImport)
			case "S":
				return m.trigger(ActionImportSecret)
			case "D":
				if _, ok := m.selectedEntry(); ok {
					return m.startConfirm(ActionRemove), nil
				}
				m.setNotice("No entry selected.", true)
				return m, nil
			}
			// "?" opens the key reference only when the filter is empty, so a
			// path containing "?" can still be typed into a non-empty filter.
			if msg.Text == "?" && m.query == "" {
				m.help = true
				return m, nil
			}
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
	if m.help {
		view := tea.NewView(renderWorkflowShell(theme, max(48, m.width), workflowShell{
			Title:  "passage · keys",
			Body:   helpLines(theme),
			Footer: "press any key to return",
		}))
		view.AltScreen = !m.noAltScreen
		view.ReportFocus = true
		return view
	}
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
	// Defensive backstop: never let an oversized overlay push the shell footer or
	// border off the alt-screen. The composer sizes its candidate window to avoid
	// this in normal cases; this only bites on a pathologically short terminal.
	if maxBody := max(1, m.height-pickerShellStructuralLines(spec.footer)); len(body) > maxBody {
		body = body[:maxBody]
	}
	view := tea.NewView(renderWorkflowShell(theme, spec.width, workflowShell{
		Title:  m.titleLine(),
		Body:   body,
		Footer: spec.footer,
	}))
	view.AltScreen = !m.noAltScreen
	// Ask the terminal to report focus so a revealed secret can be blanked
	// when the user alt-tabs away.
	view.ReportFocus = true
	view.Cursor = m.filterCursor(spec, theme)
	// Mouse is a progressive enhancement, enabled only in the alt-screen.
	// In inline mode it would steal the terminal's own selection/scrollback.
	if !m.noAltScreen {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

// overlayActive reports whether a modal-like surface owns the keyboard.
func (m pickerModel) overlayActive() bool {
	return m.busy != nil || m.modal != nil || m.confirm != nil || m.help || m.themeEditor != nil || m.composer != nil
}

func (m pickerModel) openComposer(mode composerMode) pickerModel {
	c := newComposer(mode, m.entryPaths())
	m.composer = &c
	m.message = ""
	m.messageErr = false
	return m
}

// entryPaths snapshots the store's entry paths (sorted, unique) to seed the
// composer's path-completion index. Point-in-time by design: the composer stays
// a pure state machine and never touches the live store.
func (m pickerModel) entryPaths() []string {
	out := make([]string, 0, len(m.entries))
	seen := make(map[string]struct{}, len(m.entries))
	for _, e := range m.entries {
		if _, ok := seen[e.Path]; ok {
			continue
		}
		seen[e.Path] = struct{}{}
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

func (m pickerModel) updateComposer(msg tea.KeyPressMsg, key string) (pickerModel, tea.Cmd) {
	if key == "ctrl+c" || key == "ctrl+q" {
		m.action = ActionQuit
		return m, tea.Quit
	}
	c := m.composer.update(key, msg.Text)
	m.composer = &c
	if !c.done {
		return m, nil
	}
	if c.canceled {
		m.composer = nil
		m.setNotice("Cancelled.", false)
		return m, nil
	}
	result := c.result()
	m.composer = nil
	if result.Path == "" {
		m.setNotice("Entry path is required.", true)
		return m, nil
	}
	action := ActionNew
	if result.Generate {
		action = ActionGenerate
	}
	return m.startRequest(ActionRequest{
		Action:    action,
		Filter:    m.query,
		MFAOnly:   m.mfaOnly,
		NewPath:   result.Path,
		Content:   result.Content,
		Generate:  result.Generate,
		Length:    result.Length,
		NoSymbols: result.NoSymbols,
	}, actionBusyTitle(action), result.Path)
}

func (m pickerModel) handleMouseWheel(mouse tea.Mouse) pickerModel {
	if m.overlayActive() {
		return m
	}
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.move(-1)
	case tea.MouseWheelDown:
		m.move(1)
	}
	return m
}

func (m pickerModel) handleMouseClick(mouse tea.Mouse) pickerModel {
	if m.overlayActive() || mouse.Button != tea.MouseLeft {
		return m
	}
	spec := m.computeLayout(pickerTheme{theme: m.theme})
	start := clamp(m.scroll, 0, max(0, len(m.filtered)-spec.listHeight))
	firstEntryY := 1 + len(spec.header) // top border + header rows
	if start > 0 {
		firstEntryY++ // "... N more above" line
	}
	row := mouse.Y - firstEntryY
	if row < 0 {
		return m
	}
	idx := start + row
	if idx >= len(m.filtered) || idx >= start+spec.listHeight {
		return m
	}
	m.cursor = idx
	m.ensureVisible()
	return m
}

// filterCursor places a real beam cursor at the end of the filter field. It
// returns nil while any overlay owns the keyboard (busy / modal / confirm /
// help / theme editor) so the cursor never blinks on a revealed secret or
// where typing does not go.
func (m pickerModel) filterCursor(spec layoutSpec, theme pickerTheme) *tea.Cursor {
	if m.overlayActive() {
		return nil
	}
	counter := theme.counter(len(m.filtered), len(m.entries))
	fieldWidth := max(8, spec.bodyWidth-termstyle.VisibleWidth(counter)-1-4)
	qWidth := min(termstyle.VisibleWidth(termstyle.Sanitize(m.query)), fieldWidth)
	// X: "│ " (2) + "/" (1) + typed width. Y: the filter is the last header
	// row, one below the top border — i.e. len(header) rows down.
	cursor := tea.NewCursor(3+qWidth, len(spec.header))
	cursor.Shape = tea.CursorBar
	return cursor
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
		// Sanitize: an error message can carry an entry path from an
		// unsanitized filesystem walk, which would otherwise reach the
		// terminal through Truncate (escape-aware but not C1-stripping).
		msg := termstyle.Sanitize(m.message)
		line := theme.success(termstyle.Truncate(msg, bodyWidth))
		if m.messageErr {
			line = theme.warning(termstyle.Truncate(msg, bodyWidth))
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
	if m.confirm != nil {
		tail = append(tail, "")
		tail = append(tail, m.confirmLines(bodyWidth, theme)...)
	}
	if m.composer != nil {
		tail = append(tail, "")
		// Height the composer's candidate window so the entry list keeps a floor
		// and the box can't push the footer off the frame.
		avail := m.height - pickerShellStructuralLines(footer) - len(header) - len(tail) - composerListFloor
		tail = append(tail, m.composerLines(bodyWidth, theme, avail)...)
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

// pickerFooterText is a single contextual hint line. The full, task-grouped
// key reference moved behind "?", so the footer no longer dumps eleven chords.
// helpLines is the task-grouped key reference shown behind "?".
func helpLines(theme pickerTheme) []string {
	row := func(k, d string) string {
		return "  " + theme.primary(termstyle.PadRight(k, 14)) + theme.muted(d)
	}
	var b []string
	b = append(b, theme.accent("NAVIGATE"))
	b = append(b, row("type", "fuzzy filter"))
	b = append(b, row("up / down", "move cursor"))
	b = append(b, row("pgup / pgdn", "page up / down"))
	b = append(b, row("home / end", "jump to first / last"))
	b = append(b, "", theme.accent("ACTIONS"))
	b = append(b, row("enter", "default — copy, or TOTP on an mfa row"))
	b = append(b, row("^Y", "copy password"))
	b = append(b, row("^R", "reveal password"))
	b = append(b, row("^T", "TOTP code, or reveal secret on an mfa row"))
	b = append(b, row("^P", "toggle pin"))
	b = append(b, "", theme.accent("WRITE & TRUST  (shifted — lowercase still filters)"))
	b = append(b, row("N", "new entry (type or generate)"))
	b = append(b, row("G", "generate a new entry"))
	b = append(b, row("E", "edit in $EDITOR"))
	b = append(b, row("D", "delete (confirm)"))
	b = append(b, row("T", "trust — make a read-only folder writable"))
	b = append(b, row("I", "import + trust a folder of public keys"))
	b = append(b, row("S", "import your secret key(s) — set up your identity"))
	b = append(b, "", theme.accent("VIEW & MANAGE"))
	b = append(b, row("^F", "toggle mfa-only"))
	b = append(b, row("^O", "theme editor"))
	b = append(b, row("^X", "clear clipboard"))
	b = append(b, row("^U / ^E", "clear pins / recents (confirm)"))
	b = append(b, row("^D / ^K", "doctor / list keys"))
	b = append(b, "", theme.accent("GENERAL"))
	b = append(b, row("?", "this help"))
	b = append(b, row("^C / esc", "quit"))
	return b
}

func pickerFooterText() string {
	return "type filter   arrows move   enter default   ^T totp   ? keys"
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
	line := theme.muted(strings.Join(parts, "  ·  "))
	if m.clip != nil {
		// Legible, honest clipboard state: it clears while passage is open.
		pill := theme.warning(fmt.Sprintf("clip clears in ~%ds", m.clipRemaining()))
		line += "  " + pill
	}
	return termstyle.Truncate(line, width)
}

func (m pickerModel) filterLine(width int, theme pickerTheme) string {
	// Sanitize defensively: safeTextInput already keeps control bytes out of
	// the query, but the field must never be the path a stray control byte
	// reaches the terminal.
	query := termstyle.Sanitize(m.query)
	if m.query == "" {
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
		return clampLines(m.emptyStateLines(theme), available)
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
	// Size the metadata column once for the visible window so every title
	// starts at the same column and the timestamps align flush right.
	metaWidth := 0
	for vi := start; vi < end; vi++ {
		entry := m.entries[m.filtered[vi].Index]
		metaWidth = max(metaWidth, termstyle.VisibleWidth(m.entryRightPlain(entry)))
	}
	metaWidth = clamp(metaWidth, 0, max(0, width-16))
	var lines []string
	if start > 0 {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more above", start)))
	}
	for visibleIndex := start; visibleIndex < end; visibleIndex++ {
		ranked := m.filtered[visibleIndex]
		entry := m.entries[ranked.Index]
		lines = append(lines, m.renderEntryLine(entry, ranked.Positions, visibleIndex, width, metaWidth, theme))
	}
	if end < len(m.filtered) {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more below", len(m.filtered)-end)))
	}
	return lines
}

// emptyStateLines distinguishes a genuinely empty store (a first-run welcome
// with the next step) from a filter that matched nothing (which echoes the
// sanitized query and how to widen it). The query is Sanitized because it is
// user-influenced text re-entering the render.
func (m pickerModel) emptyStateLines(theme pickerTheme) []string {
	if len(m.entries) == 0 {
		if m.mfaOnly {
			return []string{
				theme.warning("No MFA-capable entries in this store."),
				"",
				theme.muted("MFA entries live at  entry/mfa  in your pass store."),
			}
		}
		return []string{
			theme.primary("Your password store is empty."),
			"",
			theme.muted("Add one with:  pass insert work/github"),
			theme.muted("then reopen passage."),
		}
	}
	query := termstyle.Sanitize(strings.TrimSpace(m.query))
	subject := "entries"
	if m.mfaOnly {
		subject = "MFA-capable entries"
	}
	if query == "" {
		return []string{theme.warning("No " + subject + ".")}
	}
	lines := []string{theme.warning("No " + subject + " match " + strconv.Quote(query) + ".")}
	hint := "Backspace to widen the filter."
	if m.mfaOnly {
		hint = "Backspace to widen, or ^F to show all entries."
	}
	return append(lines, "", theme.muted(hint))
}

func clampLines(lines []string, limit int) []string {
	if len(lines) > limit {
		return lines[:limit]
	}
	return lines
}

func (m pickerModel) renderEntryLine(entry passstore.Entry, positions []int, visibleIndex, width, metaWidth int, theme pickerTheme) string {
	selected := visibleIndex == m.cursor
	caret := "  "
	if selected {
		caret = "> "
	}
	index := fmt.Sprintf("%3d ", visibleIndex+1)
	markers := m.entryMarkers(entry) // "PIN MFA RO" plain, or ""
	lastUsed := humanizeRelative(entry.LastUsed, m.now())
	rightW := min(metaWidth, termstyle.VisibleWidth(strings.TrimSpace(markers+" "+lastUsed)))
	leftPad := metaWidth - rightW
	leftPrefixWidth := termstyle.VisibleWidth(caret) + termstyle.VisibleWidth(index)
	// metaWidth is clamped (in listLines) so this never floors; the metadata
	// column is a stable width across rows, so titles align vertically and the
	// timestamps sit flush right.
	leftWidth := max(8, width-leftPrefixWidth-metaWidth-2)

	if selected {
		// The whole row is a continuous selection bar: every segment —
		// caret, index, title, padding, metadata — is rendered over the
		// bar background, so the fill spans the full inner width including
		// the metadata column, while the match highlight still shows.
		bar := func(s string) string { return theme.style(termstyle.RoleSelectedBar, s) }
		title := highlightTitle(entry.Display, positions, leftWidth,
			func(s string) string { return theme.onBar(termstyle.RoleSelected, s) },
			func(s string) string { return theme.onBar(termstyle.RoleSearch, s) })
		if pad := leftWidth - termstyle.VisibleWidth(title); pad > 0 {
			title += bar(strings.Repeat(" ", pad))
		}
		var right strings.Builder
		right.WriteString(bar(strings.Repeat(" ", leftPad)))
		// On the selection bar the whole row is already highlighted, so the RO/NO
		// badge shares the accent styling rather than fighting the bar.
		onBar := func(s string) string { return theme.onBar(termstyle.RoleAccent, s) }
		if sm := m.styledMarkers(entry, onBar, onBar); sm != "" {
			right.WriteString(sm)
			right.WriteString(bar(" "))
		}
		right.WriteString(theme.onBar(termstyle.RoleMuted, lastUsed))
		return theme.onBar(termstyle.RoleAccent, caret) + theme.onBar(termstyle.RoleMuted, index) + title + bar("  ") + right.String()
	}

	leftPrefix := caret + theme.muted(index)
	title := highlightTitle(entry.Display, positions, leftWidth, theme.primary, theme.search)
	var right strings.Builder
	right.WriteString(strings.Repeat(" ", leftPad))
	if sm := m.styledMarkers(entry, theme.accent, theme.warning); sm != "" {
		right.WriteString(sm)
		right.WriteString(" ")
	}
	right.WriteString(theme.muted(lastUsed))
	return leftPrefix + termstyle.PadRight(title, leftWidth) + "  " + right.String()
}

// accessBadge maps a write verdict to a short row tag. Writable and unknown
// produce no badge — only the can't-write states are flagged.
func accessBadge(verdict string) string {
	switch verdict {
	case "read_only":
		return "RO"
	case "no_access":
		return "NO"
	default:
		return ""
	}
}

// entryMarkers returns the plain marker tags for an entry's pin/MFA/access
// state, used both for rendering and for sizing the metadata column.
func (m pickerModel) entryMarkers(entry passstore.Entry) string {
	tags := make([]string, 0, 3)
	if entry.Pinned {
		tags = append(tags, "PIN")
	}
	if entry.HasMFA {
		tags = append(tags, "MFA")
	}
	if badge := accessBadge(m.access[entry.Path]); badge != "" {
		tags = append(tags, badge)
	}
	return strings.Join(tags, " ")
}

// styledMarkers renders the pin/MFA tags with accentFn and the read-only badge
// with warnFn (so RO/NO reads as a warning, not a positive tag).
func (m pickerModel) styledMarkers(entry passstore.Entry, accentFn, warnFn func(string) string) string {
	var pinMFA []string
	if entry.Pinned {
		pinMFA = append(pinMFA, "PIN")
	}
	if entry.HasMFA {
		pinMFA = append(pinMFA, "MFA")
	}
	var out string
	if len(pinMFA) > 0 {
		out = accentFn(strings.Join(pinMFA, " "))
	}
	if badge := accessBadge(m.access[entry.Path]); badge != "" {
		if out != "" {
			out += " "
		}
		out += warnFn(badge)
	}
	return out
}

// entryRightPlain is the plain (unstyled) metadata column for an entry, used to
// size the stable metadata column across the visible rows.
func (m pickerModel) entryRightPlain(entry passstore.Entry) string {
	return strings.TrimSpace(m.entryMarkers(entry) + " " + humanizeRelative(entry.LastUsed, m.now()))
}

// formatRemaining renders a countdown's seconds, the single place "Ns" is
// formatted so the reveal modal and the picker countdown never drift.
func formatRemaining(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%ds", seconds)
}

// highlightTitle truncates display to width cells and styles it: matched runes
// (positions are rune indices into the full display) render with hl, the rest
// with base. Each run is styled with a full Apply (open+reset), so styling can
// never bleed across a run or past the truncation. With no positions it is
// byte-identical to a single base-styled title, keeping the unfiltered view
// unchanged.
func highlightTitle(display string, positions []int, width int, base, hl func(string) string) string {
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
	matched := make([]bool, len(runes))
	for _, p := range positions {
		if p >= 0 && p < len(runes) {
			matched[p] = true
		}
	}
	var b strings.Builder
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && matched[j] == matched[i] {
			j++
		}
		seg := string(runes[i:j])
		if matched[i] {
			b.WriteString(hl(seg))
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
	if verdict := m.access[entry.Path]; verdict != "" {
		lines = append(lines, "")
		label := "ACCESS  " + accessVerdictLabel(verdict)
		if verdict == "writable" {
			lines = append(lines, theme.accent(termstyle.Truncate(label, width)))
		} else {
			lines = append(lines, theme.warning(termstyle.Truncate(label, width)))
			switch verdict {
			case "read_only":
				lines = append(lines, theme.muted(termstyle.Truncate("T  trust to make writable", width)))
			case "no_access":
				lines = append(lines, theme.muted(termstyle.Truncate("recipient key missing", width)))
			}
		}
	}
	lines = append(lines, "")
	for _, ln := range wrapText("enter copy · ^R reveal · ^T totp · ^P pin", width) {
		lines = append(lines, theme.muted(termstyle.Truncate(ln, width)))
	}
	for _, ln := range wrapText("N new · G generate · E edit · D delete · T trust · I import keys · S import secret", width) {
		lines = append(lines, theme.muted(termstyle.Truncate(ln, width)))
	}
	return lines
}

// accessVerdictLabel renders a write verdict for humans.
func accessVerdictLabel(verdict string) string {
	switch verdict {
	case "writable":
		return "writable"
	case "read_only":
		return "read-only"
	case "no_access":
		return "no access"
	case "uninitialized":
		return "no .gpg-id"
	default:
		return verdict
	}
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
	if m.busy != nil || m.clip != nil {
		return true
	}
	if m.modal != nil && m.modal.secret && m.modal.remaining > 0 {
		return true
	}
	return !m.messageExpires.IsZero() && m.now().Before(m.messageExpires)
}

// noticeTTL is how long a transient action notice ("copied", an error) stays on
// screen before it fades. Config feedback (theme warnings, the opening message)
// is set directly and persists.
const noticeTTL = 6 * time.Second

// setNotice posts a transient, self-fading message — the single notice channel
// the picker shows, so a stale "copied" never lingers under a later action.
func (m *pickerModel) setNotice(text string, isErr bool) {
	m.message = text
	m.messageErr = isErr
	if text == "" {
		m.messageExpires = time.Time{}
		return
	}
	m.messageExpires = m.now().Add(noticeTTL)
}

func (m *pickerModel) refreshMessage() {
	if m.message == "" || m.messageExpires.IsZero() {
		return
	}
	if !m.now().Before(m.messageExpires) {
		m.message = ""
		m.messageErr = false
		m.messageExpires = time.Time{}
	}
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
	if isGapAction(action) {
		// These need the real terminal (pinentry / $EDITOR), so quit and let the
		// CLI run them in the gap, then relaunch us.
		if actionNeedsEntry(action) {
			if _, ok := m.selectedEntry(); !ok {
				m.setNotice("No entry selected.", true)
				return m, nil
			}
		}
		return m.finish(action), tea.Quit
	}
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
	return m.startRequest(ActionRequest{
		Action:  action,
		Entry:   entry,
		Filter:  m.query,
		MFAOnly: m.mfaOnly,
	}, actionBusyTitle(action), entry.Path)
}

// startRequest spins up the busy box and dispatches a request to the action
// runner. Shared by the entry-action path (startAction) and the composer.
func (m pickerModel) startRequest(req ActionRequest, busyTitle, detail string) (pickerModel, tea.Cmd) {
	if m.runAction == nil {
		return m, nil
	}
	actionCtx, cancel := context.WithCancel(m.ctx)
	m.actionSeq++
	id := m.actionSeq
	m.activeID = id
	m.message = ""
	m.messageErr = false
	m.modal = nil
	m.busy = &pickerBusy{
		id:      id,
		title:   busyTitle,
		detail:  detail,
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

func (m pickerModel) startConfirm(action Action) pickerModel {
	prompt, detail := m.confirmText(action)
	m.confirm = &pickerConfirm{action: action, prompt: prompt, detail: detail}
	m.message = ""
	m.messageErr = false
	return m
}

func (m pickerModel) updateConfirm(key string) (pickerModel, tea.Cmd) {
	action := m.confirm.action
	switch key {
	case "ctrl+c", "ctrl+q":
		m.action = ActionQuit
		return m, tea.Quit
	case "y", "Y", "enter":
		m.confirm = nil
		return m.trigger(action)
	default:
		// esc, n, or any other key cancels — curated state is never wiped
		// without an explicit yes.
		m.confirm = nil
		m.message = "Cancelled."
		m.messageErr = false
		return m, nil
	}
}

func (m pickerModel) confirmText(action Action) (string, string) {
	switch action {
	case ActionClearPins:
		n := 0
		for _, e := range m.entries {
			if e.Pinned {
				n++
			}
		}
		suffix := "s"
		if n == 1 {
			suffix = ""
		}
		return "Clear all pins?", fmt.Sprintf("removes %d pin%s", n, suffix)
	case ActionClearRecents:
		return "Clear recents?", "forgets last-used times"
	case ActionRemove:
		entry, _ := m.selectedEntry()
		return "Remove " + entry.Path + "?", "deletes the encrypted entry"
	default:
		return "Proceed?", ""
	}
}

func (m pickerModel) composerLines(width int, theme pickerTheme, avail int) []string {
	boxWidth := clamp(width, 54, 100)
	c := *m.composer
	if m.secretHidden {
		// Terminal lost focus: re-mask a ^R-revealed secret for rendering only,
		// without losing the underlying reveal state. Mirrors the modal
		// blanking and is restored on FocusMsg.
		c.secret.masked = true
		c.confirm.masked = true
	}
	if c.step == stepPath {
		c.viewRows = c.pathViewRows(boxWidth-4, theme, avail)
	}
	body := c.render(boxWidth-4, theme)
	return splitRendered(renderWorkflowShell(theme, boxWidth, workflowShell{
		Title:  c.title(),
		Body:   body,
		Footer: c.footer(),
	}))
}

func (m pickerModel) confirmLines(width int, theme pickerTheme) []string {
	body := []string{theme.warning(m.confirm.prompt)}
	if m.confirm.detail != "" {
		body = append(body, "", theme.muted(m.confirm.detail))
	}
	return splitRendered(renderWorkflowShell(theme, clamp(width, 54, 100), workflowShell{
		Title:  "confirm",
		Body:   body,
		Footer: "y confirm   esc cancel",
		Danger: true,
	}))
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
	if out.Err != nil {
		m.setNotice(out.Err.Error(), true)
	} else {
		m.setNotice(out.Message, false)
	}
	if out.ClipArmed && out.ClipRemaining > 0 {
		m.clip = &clipState{
			tool:    out.ClipTool,
			expires: m.now().Add(time.Duration(out.ClipRemaining) * time.Second),
		}
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

// clipRemaining is the armed clipboard's seconds left, rounded up.
func (m pickerModel) clipRemaining() int {
	if m.clip == nil {
		return 0
	}
	d := m.clip.expires.Sub(m.now())
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

// refreshClip disarms an expired clipboard and returns the quiet clear command
// (or nil). The clear only fires while the picker is alive and foregrounded —
// the pill copy promises no more than that.
func (m *pickerModel) refreshClip() tea.Cmd {
	if m.clip == nil || m.now().Before(m.clip.expires) {
		return nil
	}
	m.clip = nil
	if m.clearClipboard == nil {
		return nil
	}
	clear := m.clearClipboard
	ctx := m.ctx
	return func() tea.Msg {
		return clipClearedMsg{err: clear(ctx)}
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
	case ActionCopy, ActionReveal, ActionTOTP, ActionTogglePin, ActionEdit, ActionTrust, ActionGenerate, ActionRemove:
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
	case ActionNew:
		return "creating"
	case ActionGenerate:
		return "generating"
	case ActionRemove:
		return "removing"
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
