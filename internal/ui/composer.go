package ui

import (
	"fmt"
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

	// composerListFloor is the minimum number of entry-list rows kept visible
	// above an open composer; the candidate window shrinks before this floor.
	composerListFloor = 3
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

	// Path-completion state for stepPath. idx is an immutable snapshot of the
	// store's entry paths (folder-awareness); selIndex is the highlighted
	// candidate (-1 = plain type mode, no selection); notice carries a blocked-
	// enter / no-match reason so feedback is never silent; viewRows caps the
	// candidate window from the terminal height (0 = uncapped, for unit tests).
	idx      pathIndex
	selIndex int
	notice   string
	viewRows int
}

type composerResult struct {
	Path      string
	Generate  bool
	Content   []byte
	Length    int
	NoSymbols bool
}

func newComposer(mode composerMode, entryPaths []string) composerModel {
	return composerModel{
		mode:     mode,
		step:     stepPath,
		secret:   textField{masked: true},
		confirm:  textField{masked: true},
		length:   composerDefaultLength,
		idx:      buildPathIndex(entryPaths),
		selIndex: -1,
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
		return c.updatePath(key, text)
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

// updatePath drives stepPath: shell-style TAB completion over the implicit
// folder tree, arrow selection of the live candidate list, and enter-validation
// against the path index. selIndex == -1 is plain type mode (no highlight); any
// field edit returns to it and clears the notice so feedback is never stale.
func (c composerModel) updatePath(key, text string) composerModel {
	field := c.path.String()
	dir, frag := splitPath(field)
	switch key {
	case "enter":
		// A highlighted folder descends; anything else validates + submits.
		if c.selIndex >= 0 {
			cands := c.idx.completionCandidates(dir, frag)
			if c.selIndex < len(cands) {
				newField, descended := applyNode(dir, cands[c.selIndex].node)
				c.path = c.path.withValue(newField)
				c.selIndex = -1
				c.notice = ""
				if descended {
					return c
				}
				field = newField // an entry was filled; validate it below
			}
		}
		return c.submitPath(field)
	case "tab":
		return c.completePath(dir, frag)
	case "shift+tab":
		c.path = c.path.withValue(ascendPath(field))
		c.selIndex = -1
		c.notice = ""
		return c
	case "down":
		if cands := c.idx.completionCandidates(dir, frag); c.selIndex < len(cands)-1 {
			c.selIndex++
		}
		c.notice = ""
		return c
	case "up":
		if c.selIndex >= 0 {
			c.selIndex--
		}
		c.notice = ""
		return c
	case "ctrl+g":
		// Switch to generate; stay on the path step (enter then goes to length).
		c.mode = composeGenerate
		c.notice = ""
		return c
	default:
		// Field edit: printable text (incl. "/"), backspace, cursor motion, ^U.
		c.path = c.path.update(key, text)
		c.selIndex = -1
		c.notice = ""
		return c
	}
}

// completePath implements shell first-TAB: accept a highlighted candidate, or
// over the current fragment complete to a unique child, or extend to the
// longest common prefix of the matches without guessing a descent.
func (c composerModel) completePath(dir, frag string) composerModel {
	cands := c.idx.completionCandidates(dir, frag)
	if c.selIndex >= 0 && c.selIndex < len(cands) {
		newField, _ := applyNode(dir, cands[c.selIndex].node)
		c.path = c.path.withValue(newField)
		c.selIndex = -1
		c.notice = ""
		return c
	}
	matches := matchingCandidates(cands)
	switch len(matches) {
	case 0:
		if frag == "" {
			c.notice = "no entries in " + termstyle.Sanitize(displayDir(dir))
		} else {
			c.notice = "no match — " + strconv.Quote(frag) + " will be a new entry"
		}
	case 1:
		newField, _ := applyNode(dir, matches[0].node)
		c.path = c.path.withValue(newField)
		c.notice = ""
	default:
		// Extend to the common prefix when it adds characters or corrects the
		// fragment's case to the store's canonical spelling; the live list
		// already shows every match for the next keystroke.
		if prefix := commonCompletionPrefix(matches); prefix != "" && prefix != frag {
			c.path = c.path.withValue(dir + prefix)
		}
		c.notice = ""
	}
	c.selIndex = -1
	return c
}

// submitPath validates the typed path and either advances to the next step or
// sets a notice explaining why it cannot. It mirrors the CLI's exact-match
// overwrite gate, surfacing a duplicate before any round-trip.
func (c composerModel) submitPath(field string) composerModel {
	switch c.idx.classifyLeaf(field) {
	case leafEmpty:
		return c // historical silent no-op for an empty path
	case leafTrailingSlash:
		c.notice = "finish the name after the last /"
	case leafEmptySegment:
		c.notice = "remove the empty path segment"
	case leafPathIsFolder:
		c.notice = termstyle.Sanitize(strings.TrimSpace(field)) + " is a folder — add /name"
	case leafExistingEntry:
		c.notice = "entry exists — esc, then E to edit"
	case leafCaseCollision:
		c.notice = "exists as " + strconv.Quote(c.idx.caseCollisionCanonical(field)) + " (case differs)"
	default: // leafNew
		c.notice = ""
		if c.mode == composeGenerate {
			c.step = stepLength
		} else {
			c.step = stepSecret
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
		// Surface the selection mode so "enter" is never a hidden overload.
		if c.selIndex >= 0 {
			return "tab/enter pick  ↑↓ move  ^G gen  esc cancel"
		}
		return "tab complete  ↑↓ pick  enter next  ^G gen  esc cancel"
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
		pathLine = theme.muted("path  ") + theme.primary(termstyle.Truncate(termstyle.Sanitize(c.path.String()), fieldWidth))
	}
	lines := []string{pathLine}
	switch c.step {
	case stepPath:
		lines = append(lines, c.renderPath(width, theme)...)
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

// renderPath renders the stepPath body below the input line: a read-only
// breadcrumb that colors existing folders (the "confirm it's really there"
// payoff), then the current folder's children — matches bright and
// prefix-highlighted, non-matches dimmed as context — and any blocked-enter
// notice. width is the box's inner content width.
func (c composerModel) renderPath(width int, theme pickerTheme) []string {
	field := c.path.String()
	dir, frag := splitPath(field)
	segs, leaf, kind := c.idx.breadcrumbSegments(field)
	cands := c.idx.completionCandidates(dir, frag)

	var lines []string
	// The breadcrumb earns its line only once a folder is committed — that is
	// exactly when "confirm the folder exists" matters.
	if len(segs) > 0 {
		lines = append(lines, c.breadcrumbLine(segs, leaf, kind, width, theme))
	}
	lines = append(lines, "")
	lines = append(lines, c.candidateHeaderLine(dir, frag, cands, width, theme))
	if len(cands) == 0 {
		lines = append(lines, theme.muted("  (empty — type a name, then enter to create it)"))
	} else {
		lines = append(lines, c.candidateRows(dir, frag, cands, width, theme)...)
	}
	if frag != "" && len(matchingCandidates(cands)) == 0 && len(cands) > 0 {
		lines = append(lines, theme.muted("no existing name starts with "+strconv.Quote(frag)+" — enter creates it"))
	}
	if c.notice != "" {
		lines = append(lines, theme.warning(c.notice))
	}
	return lines
}

// pathViewRows caps the candidate window so the composer box fits within avail
// total lines (its 4 shell lines + body) while keeping every other body line.
// It returns 0 (uncapped) when the full list already fits. width is the box's
// inner content width; the receiver's viewRows is still 0 here, so the measuring
// render is uncapped.
func (c composerModel) pathViewRows(avail int) int {
	field := c.path.String()
	dir, frag := splitPath(field)
	total := len(c.idx.completionCandidates(dir, frag))
	if total == 0 {
		return 0
	}
	// Count the body lines that are NOT candidate rows, structurally (cheaper
	// and clearer than rendering): path line + blank spacer (2), breadcrumb,
	// candidate header, the no-match hint, and any notice.
	nonCandidate := c.pathNonCandidateLines()
	// With no selection the window is anchored at the top, so only a "below"
	// marker can appear; otherwise reserve for both.
	markers := 2
	if c.selIndex < 0 {
		markers = 1
	}
	maxRows := (avail - 4) - nonCandidate - markers // -4 for the box's shell lines
	switch {
	case maxRows >= total:
		return 0 // fits uncapped
	case maxRows < 1:
		return -1 // no room for even one row: render only the overflow marker
	default:
		return maxRows
	}
}

// pathNonCandidateLines counts the stepPath body lines other than candidate
// rows, so pathViewRows can budget the window without a measuring render.
func (c composerModel) pathNonCandidateLines() int {
	field := c.path.String()
	dir, frag := splitPath(field)
	segs, _, _ := c.idx.breadcrumbSegments(field)
	cands := c.idx.completionCandidates(dir, frag)
	n := 3 // path line + blank spacer + candidate header
	if len(segs) > 0 {
		n++ // breadcrumb
	}
	if frag != "" && len(cands) > 0 && len(matchingCandidates(cands)) == 0 {
		n++ // "no existing name starts with …" hint
	}
	if c.notice != "" {
		n++
	}
	return n
}

// breadcrumbLine renders the read-only "in" line: committed folder segments
// (accent when they exist, warning when they do not) and the active leaf (muted
// when new, warning on an overwrite/folder collision). When the path is too deep
// for width it tail-truncates with a leading "…" so the active segment — the one
// you are typing — always stays visible.
func (c composerModel) breadcrumbLine(segs []breadcrumbSeg, leaf string, kind leafKind, width int, theme pickerTheme) string {
	const sep = " / "
	// Each folder piece carries its trailing separator, so concatenation alone
	// yields "pp / alter-ego / " (poised inside) or "pp / alter-ego / gmail".
	type piece struct{ plain, styled string }
	var pieces []piece
	for _, s := range segs {
		name := termstyle.Sanitize(s.Name)
		style := theme.warning
		if s.Exists {
			style = theme.accent
		}
		pieces = append(pieces, piece{name + sep, style(name) + theme.muted(sep)})
	}
	if leaf != "" {
		lf := termstyle.Sanitize(leaf)
		style := theme.warning
		if kind == leafNew {
			style = theme.muted
		}
		pieces = append(pieces, piece{lf, style(lf)})
	}

	const prefix = "in    "
	budget := width - len(prefix)
	full := 0
	for _, p := range pieces {
		full += termstyle.VisibleWidth(p.plain)
	}

	var b strings.Builder
	b.WriteString(theme.muted(prefix))
	if full <= budget || budget <= 0 {
		for _, p := range pieces {
			b.WriteString(p.styled)
		}
		return b.String()
	}
	// Too deep: keep the rightmost pieces that fit after a leading ellipsis, so
	// the active segment you are typing always stays visible.
	const ell = "… "
	kept, used := 0, len(ell)
	for i := len(pieces) - 1; i >= 0; i-- {
		add := termstyle.VisibleWidth(pieces[i].plain)
		if used+add > budget && kept > 0 {
			break
		}
		used += add
		kept++
	}
	b.WriteString(theme.muted(ell))
	for i := len(pieces) - kept; i < len(pieces); i++ {
		b.WriteString(pieces[i].styled)
	}
	return b.String()
}

// candidateHeaderLine is the "under <dir> ... <count>" line above the list.
func (c composerModel) candidateHeaderLine(dir, frag string, cands []pathCandidate, width int, theme pickerTheme) string {
	left := "under " + headerDir(termstyle.Sanitize(dir))
	right := candidateCountLabel(frag, cands)
	left = termstyle.Truncate(left, max(1, width-termstyle.VisibleWidth(right)-1))
	pad := max(1, width-termstyle.VisibleWidth(left)-termstyle.VisibleWidth(right))
	return theme.muted(left) + strings.Repeat(" ", pad) + theme.muted(right)
}

// candidateRows renders the (optionally windowed) child list with overflow
// markers, matches first.
func (c composerModel) candidateRows(dir, frag string, cands []pathCandidate, width int, theme pickerTheme) []string {
	start, end := c.candidateWindow(len(cands))
	tagW := 0
	for i := start; i < end; i++ {
		tag, _ := c.candidateTag(dir, frag, cands[i].node)
		tagW = max(tagW, termstyle.VisibleWidth(tag))
	}
	tagW = clamp(tagW, 0, max(0, width-12))
	var rows []string
	if start > 0 {
		rows = append(rows, theme.muted(fmt.Sprintf("  ... %d more above", start)))
	}
	for i := start; i < end; i++ {
		rows = append(rows, c.candidateRow(dir, frag, cands[i], i, width, tagW, theme))
	}
	if end < len(cands) {
		rows = append(rows, theme.muted(fmt.Sprintf("  ... %d more below", len(cands)-end)))
	}
	return rows
}

// candidateWindow returns the visible [start,end) slice of candidates, capped to
// viewRows (0 = uncapped, for unit tests) and kept around the selection.
func (c composerModel) candidateWindow(n int) (start, end int) {
	rows := c.viewRows
	if rows < 0 {
		return 0, 0 // no room: render only the "more below" marker
	}
	if rows == 0 || n <= rows {
		return 0, n
	}
	sel := max(c.selIndex, 0)
	start = sel - rows/2
	if start < 0 {
		start = 0
	}
	end = start + rows
	if end > n {
		end = n
		start = max(0, end-rows)
	}
	return start, end
}

func (c composerModel) candidateRow(dir, frag string, cand pathCandidate, index, width, tagW int, theme pickerTheme) string {
	selected := index == c.selIndex
	caret, glyph := "  ", "· "
	if cand.node.IsFolder {
		glyph = "▸ "
	}
	if selected {
		caret = "> "
	}
	prefixW := termstyle.VisibleWidth(caret + glyph)
	gap := 1
	nameWidth := max(4, width-prefixW-tagW-gap)
	displayName := termstyle.Sanitize(cand.node.Name)
	if cand.node.IsFolder {
		displayName += "/"
	}
	base, hl := theme.muted, theme.muted
	if cand.match {
		base, hl = theme.primary, theme.search
	}
	if selected {
		base, hl = theme.accent, theme.accent
	}
	styledName := highlightTitle(displayName, cand.positions, nameWidth, base, hl)
	tag, warn := c.candidateTag(dir, frag, cand.node)
	pad := max(1, width-prefixW-termstyle.VisibleWidth(styledName)-termstyle.VisibleWidth(tag))
	caretStyle, glyphStyle, tagStyle := theme.muted, theme.subtle, theme.muted
	if cand.node.IsFolder {
		glyphStyle = theme.secondary
	}
	if selected {
		caretStyle, glyphStyle = theme.accent, theme.accent
	}
	if warn {
		tagStyle = theme.warning
	}
	return caretStyle(caret) + glyphStyle(glyph) + styledName + strings.Repeat(" ", pad) + tagStyle(tag)
}

// candidateTag is the right-aligned tag for a child row: a folder's item count,
// "entry", or a warning "exists" when the typed fragment exactly names this
// entry (an imminent overwrite).
func (c composerModel) candidateTag(dir, frag string, n pathNode) (text string, warn bool) {
	if n.IsFolder {
		return plural(len(c.idx.children[childPath(dir, n.Name)]), "item", "items"), false
	}
	// Case-insensitive so the "exists" warning fires for exactly the fragments
	// the enter-gate blocks (leafExistingEntry and leafCaseCollision).
	if strings.EqualFold(n.Name, frag) {
		return "exists", true
	}
	return "entry", false
}

func candidateCountLabel(frag string, cands []pathCandidate) string {
	if len(cands) == 0 {
		return "empty"
	}
	if frag == "" {
		return plural(len(cands), "item", "items")
	}
	if m := len(matchingCandidates(cands)); m > 0 {
		return plural(m, "match", "matches")
	}
	return "no match"
}

func headerDir(dir string) string {
	if dir == "" {
		return "/"
	}
	return dir
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
