package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/termnav"
	"github.com/0xbenc/termnav/source"
	"github.com/0xbenc/termnav/teax"
)

// BrowseKeyDirOptions configures the local-filesystem browser the key-import
// flows use to choose a folder (or, with SelectFiles, a single file).
type BrowseKeyDirOptions struct {
	Input       io.Reader
	Output      io.Writer
	NoAltScreen bool
	NoColor     bool
	Theme       termstyle.Theme
	ThemeFile   string
	Title       string
	Start       string
	// SelectFiles makes file rows selectable (Enter returns the file) instead of
	// reference-only. Used by the secret-key import.
	SelectFiles bool
	// Validate gates a folder/file commit: ok=false keeps the browser open and
	// shows notice (e.g. "no key files here"). path is the chosen path, isFile
	// reports whether a file (vs the current folder) was chosen.
	Validate func(path string, isFile bool) (ok bool, notice string)
}

// BrowseKeyDir lets the user navigate the local filesystem to choose a folder
// (or a file, with SelectFiles) of key material. It is the termnav-backed
// replacement for the old per-directory re-list loop: navigation, listing, and
// filtering all happen inside one program, so the program is no longer torn down
// and rebuilt on every step. It returns the chosen path, whether that path is a
// file (vs the current folder), and ok=false on cancel.
func BrowseKeyDir(ctx context.Context, opts BrowseKeyDirOptions) (path string, isFile bool, ok bool, err error) {
	theme, err := resolveTheme(opts.NoColor, opts.Theme, opts.ThemeFile)
	if err != nil {
		return "", false, false, err
	}
	theme = theme.WithNoColor(theme.NoColor || opts.NoColor)

	src := source.NewLocal(source.LocalOptions{
		SkipHidden:  true,
		UseRow:      true,
		UseTitle:    "Use this folder",
		SelectFiles: opts.SelectFiles,
		DirSuffix:   "/",
		// Kind literals preserved for any downstream consumer; the renderer keys
		// color off NavIntent, never these.
		UseKind: "use", UpKind: "up", DirKind: "dir", FileKind: "file",
		UseBadge: "use", UpBadge: "up", DirBadge: "dir", FileBadge: "file",
	})

	var validate termnav.Validator
	if opts.Validate != nil {
		validate = func(r termnav.Row) (bool, string) {
			return opts.Validate(r.Token, r.Intent == termnav.IntentSelectLeaf)
		}
	}

	navOpts := termnav.Options{
		Matcher:     termnav.Substring{}, // the directory browser filters by plain Contains
		MatchText:   func(r termnav.Row) string { return r.Title },
		ReserveRows: 8, // shell(2) + footer(2) + location/filter/blank(3) + safety(1)
		Validate:    validate,
	}

	title := defaultString(opts.Title, "choose a folder")
	render := func(m termnav.Model) tea.View {
		v := tea.NewView(renderDirBrowse(m, pickerTheme{theme: theme}, title, opts.SelectFiles))
		v.AltScreen = !opts.NoAltScreen
		return v
	}
	input := opts.Input
	if input == nil {
		input = os.Stdin
	}

	out, committed, err := teax.Run(ctx, teax.Config{
		Source: src,
		Start:  opts.Start,
		Render: render,
	}, navOpts, teax.ProgramIO{Input: input, Output: opts.Output})
	if err != nil || !committed {
		return "", false, committed, err
	}
	return out.Token(), out.Intent == termnav.IntentSelectLeaf, true, nil
}

// renderDirBrowse paints the directory browser frame from a termnav model,
// preserving passage's look: a location line, a filter line, then the windowed
// list with reference-dimmed file rows.
func renderDirBrowse(m termnav.Model, theme pickerTheme, title string, selectFiles bool) string {
	width := max(48, m.Width())
	inner := width - 4
	body := []string{dirBrowseLocationLine(m.Cwd(), inner, theme), dirBrowseFilterLine(m, inner, theme), ""}
	body = append(body, dirBrowseListLines(m, inner, theme, selectFiles)...)
	footer := termstyle.Footer([]termstyle.KeyHint{{Key: "enter", Label: "open/use folder"}, {Key: "", Label: "files shown for reference"}, {Key: "type", Label: "filter"}, {Key: "esc", Label: "cancel"}}, 0)
	if selectFiles {
		footer = termstyle.Footer([]termstyle.KeyHint{{Key: "enter", Label: "open folder"}, {Key: "", Label: "use folder"}, {Key: "", Label: "select file"}, {Key: "type", Label: "filter"}, {Key: "esc", Label: "cancel"}}, 0)
	}
	return renderWorkflowShell(theme, width, workflowShell{
		Title:  strings.ToUpper(title),
		Body:   body,
		Footer: footer,
	})
}

func dirBrowseLocationLine(loc string, width int, theme pickerTheme) string {
	if loc == "" {
		loc = "."
	}
	return theme.muted(termstyle.Truncate("folder  "+termstyle.Sanitize(loc), width))
}

func dirBrowseFilterLine(m termnav.Model, width int, theme pickerTheme) string {
	query := termstyle.Sanitize(m.Query())
	field := "/" + query
	if m.Query() == "" {
		field = theme.muted("/type to filter")
	} else {
		field = theme.primary(field)
	}
	return field + "  " + theme.counter(len(m.Filtered()), len(m.Rows()))
}

func dirBrowseListLines(m termnav.Model, width int, theme pickerTheme, selectFiles bool) []string {
	filtered := m.Filtered()
	rows := m.Rows()
	if len(filtered) == 0 {
		return []string{theme.warning("No matching folders.")}
	}
	budget := max(1, m.Budget())
	start := m.Scroll()
	if start < 0 {
		start = 0
	}
	var lines []string
	used := 0
	if start > 0 {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more above", start)))
		used++
	}
	i := start
	for ; i < len(filtered); i++ {
		reserve := 0
		if len(filtered)-i-1 > 0 {
			reserve = 1
		}
		if used+1+reserve > budget {
			break
		}
		idx := filtered[i]
		if idx >= 0 && idx < len(rows) {
			lines = append(lines, dirBrowseRow(rows[idx], i == m.Cursor(), selectFiles, width, theme))
		}
		used++
	}
	if i < len(filtered) {
		lines = append(lines, theme.muted(fmt.Sprintf("  ... %d more below", len(filtered)-i)))
	}
	return lines
}

func dirBrowseRow(row termnav.Row, selected, filesSelectable bool, width int, theme pickerTheme) string {
	cursor := "  "
	if selected {
		cursor = ">>"
	}
	badge := "[" + strings.ToUpper(badgeFor(row)) + "]"
	line := cursor + termstyle.PadRight(badge, 6) + " " + termstyle.Sanitize(row.Title)
	line = termstyle.Truncate(line, width)
	switch {
	case selected:
		return theme.selected(termstyle.PadRight(line, width))
	case row.Intent == termnav.IntentUseContainer:
		return theme.accent(line)
	case row.Intent == termnav.IntentReference:
		return theme.subtle(line) // reference-only, dimmed
	case row.Intent == termnav.IntentSelectLeaf:
		if filesSelectable {
			return theme.primary(line)
		}
		return theme.subtle(line)
	case row.Intent == termnav.IntentAscend:
		return theme.muted(line)
	default:
		return theme.primary(line)
	}
}

// badgeFor names a row's badge for display: the app-supplied badge, else a label
// derived from the canonical intent.
func badgeFor(row termnav.Row) string {
	if row.Badge != "" {
		return row.Badge
	}
	switch row.Intent {
	case termnav.IntentUseContainer:
		return "use"
	case termnav.IntentAscend:
		return "up"
	case termnav.IntentDescend:
		return "dir"
	default:
		return "file"
	}
}
