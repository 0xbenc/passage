package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/0xbenc/passage/internal/termstyle"
)

func newDirBrowse(entries []DirEntry) dirBrowseModel {
	m := dirBrowseModel{entries: entries, selected: -1, theme: termstyle.TerminalTheme(), width: 80, height: 24}
	m.applyFilter()
	return m
}

func TestDirBrowseFilterAndSelect(t *testing.T) {
	m := newDirBrowse([]DirEntry{
		{Title: "Use this folder", Path: "/keys", Kind: "use"},
		{Title: "..", Path: "/", Kind: "up"},
		{Title: "work/", Path: "/keys/work", Kind: "dir"},
		{Title: "personal/", Path: "/keys/personal", Kind: "dir"},
	})
	for _, ch := range []string{"w", "o", "r", "k"} {
		u, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: ch}))
		m = u.(dirBrowseModel)
	}
	if len(m.filtered) != 1 || m.entries[m.filtered[0]].Path != "/keys/work" {
		t.Fatalf("filter work = %#v", m.filtered)
	}
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = u.(dirBrowseModel)
	if m.selected < 0 || m.entries[m.filtered[m.selected]].Path != "/keys/work" {
		t.Fatalf("enter did not select work: selected=%d", m.selected)
	}
}

func TestDirBrowseUseFolderIsFirst(t *testing.T) {
	m := newDirBrowse([]DirEntry{
		{Title: "Use this folder", Path: "/keys", Kind: "use"},
		{Title: "sub/", Path: "/keys/sub", Kind: "dir"},
	})
	// Enter on the default cursor (row 0) selects "use".
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = u.(dirBrowseModel)
	if got := m.entries[m.filtered[m.selected]]; got.Kind != "use" {
		t.Fatalf("first row = %q, want use", got.Kind)
	}
}

func TestDirBrowseCursorSkipsFiles(t *testing.T) {
	m := newDirBrowse([]DirEntry{
		{Title: "Use this folder", Path: "/k", Kind: "use"},
		{Title: "sub/", Path: "/k/sub", Kind: "dir"},
		{Title: "a.asc", Path: "/k/a.asc", Kind: "file"},
		{Title: "b.gpg", Path: "/k/b.gpg", Kind: "file"},
	})
	// Cursor starts on the first selectable (use, index 0).
	if m.cursor != 0 {
		t.Fatalf("initial cursor = %d, want 0", m.cursor)
	}
	// Down once lands on the dir (index 1), skipping nothing.
	down, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "\x1b[B"}))
	m = down.(dirBrowseModel)
	if m.cursor != 1 || m.entries[m.filtered[m.cursor]].Kind != "dir" {
		t.Fatalf("after down cursor = %d (kind %s), want dir at 1", m.cursor, m.entries[m.filtered[m.cursor]].Kind)
	}
	// Down again must NOT land on a file row — it stays on the dir (no selectable below).
	down2, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "\x1b[B"}))
	m = down2.(dirBrowseModel)
	if m.entries[m.filtered[m.cursor]].Kind == "file" {
		t.Fatalf("cursor landed on a file row at %d", m.cursor)
	}
	// Enter on a file row would be a no-op: force cursor onto the file and try.
	m.cursor = 2 // a.asc
	en, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if got := en.(dirBrowseModel); got.selected >= 0 {
		t.Fatalf("Enter selected a file row (selected=%d)", got.selected)
	}
}

func TestDirBrowseSelectFilesMode(t *testing.T) {
	m := dirBrowseModel{
		entries: []DirEntry{
			{Title: "Use this folder", Path: "/k", Kind: "use"},
			{Title: "me.sec.asc", Path: "/k/me.sec.asc", Kind: "file"},
		},
		selected:    -1,
		selectFiles: true,
		theme:       termstyle.TerminalTheme(),
		width:       80,
		height:      24,
	}
	m.applyFilter()
	// With SelectFiles, the cursor can land on the file and Enter selects it.
	down, _ := m.Update(tea.KeyPressMsg(tea.Key{Text: "\x1b[B"}))
	m = down.(dirBrowseModel)
	if got := m.entries[m.filtered[m.cursor]]; got.Kind != "file" {
		t.Fatalf("cursor did not reach the file row (kind %s)", got.Kind)
	}
	en, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	got := en.(dirBrowseModel)
	if got.selected < 0 || got.entries[got.filtered[got.selected]].Path != "/k/me.sec.asc" {
		t.Fatalf("Enter did not select the file: selected=%d", got.selected)
	}
}

func TestDirBrowseCancel(t *testing.T) {
	m := newDirBrowse([]DirEntry{{Title: "Use this folder", Kind: "use"}})
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if !u.(dirBrowseModel).canceled {
		t.Fatal("esc did not cancel")
	}
}
