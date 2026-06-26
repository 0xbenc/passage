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

func TestDirBrowseCancel(t *testing.T) {
	m := newDirBrowse([]DirEntry{{Title: "Use this folder", Kind: "use"}})
	u, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if !u.(dirBrowseModel).canceled {
		t.Fatal("esc did not cancel")
	}
}
