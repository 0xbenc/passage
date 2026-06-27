package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/termnav"
	"github.com/0xbenc/termnav/source"
)

// dirBrowseSource mirrors the LocalSource configuration BrowseKeyDir builds, so
// these tests pin passage's directory-browser behavior (the navigation itself
// is covered by termnav's reducer tests).
func dirBrowseSource(selectFiles bool) *source.LocalSource {
	return source.NewLocal(source.LocalOptions{
		SkipHidden: true, UseRow: true, UseTitle: "Use this folder",
		SelectFiles: selectFiles, DirSuffix: "/",
		UseKind: "use", UpKind: "up", DirKind: "dir", FileKind: "file",
	})
}

func TestBrowseKeyDirRows(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "work"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "key.asc"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("h"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Folder mode: "Use this folder" first, dirs selectable, files reference-only,
	// dotfiles hidden.
	l, err := dirBrowseSource(false).ListSync(root)
	if err != nil {
		t.Fatal(err)
	}
	if l.Rows[0].Intent != termnav.IntentUseContainer {
		t.Fatalf("first row = %v, want UseContainer", l.Rows[0].Intent)
	}
	var sawDir, sawFileRef, sawHidden bool
	for _, r := range l.Rows {
		switch {
		case r.Title == "work/" && r.Intent == termnav.IntentDescend && r.Selectable:
			sawDir = true
		case r.Title == "key.asc" && r.Intent == termnav.IntentReference && !r.Selectable:
			sawFileRef = true
		case strings.HasPrefix(r.Title, ".hidden"):
			sawHidden = true
		}
	}
	if !sawDir || !sawFileRef {
		t.Fatalf("folder mode rows wrong: dir=%v fileRef=%v\n%#v", sawDir, sawFileRef, l.Rows)
	}
	if sawHidden {
		t.Fatal("dotfile leaked into the listing")
	}

	// SelectFiles mode: the file becomes a selectable leaf.
	l2, _ := dirBrowseSource(true).ListSync(root)
	var sawFileLeaf bool
	for _, r := range l2.Rows {
		if r.Title == "key.asc" && r.Intent == termnav.IntentSelectLeaf && r.Selectable {
			sawFileLeaf = true
		}
	}
	if !sawFileLeaf {
		t.Fatalf("SelectFiles mode did not make the file selectable:\n%#v", l2.Rows)
	}
}

func TestRenderDirBrowseSmoke(t *testing.T) {
	m := termnav.New(termnav.Options{ReserveRows: 8, Matcher: termnav.Substring{}})
	m, _ = m.Load("/keys")
	m, _ = termnav.Update(m, termnav.ResizeEvent{W: 80, H: 24})
	m, _ = termnav.Update(m, termnav.ListLoadedEvent{Gen: 1, Listing: termnav.Listing{
		Dir: "/keys", Parent: "/", Rows: []termnav.Row{
			{Token: "/keys", Title: "Use this folder", Intent: termnav.IntentUseContainer, Selectable: true, Badge: "use"},
			{Token: "/keys/work", Title: "work/", Intent: termnav.IntentDescend, Selectable: true, Badge: "dir"},
			{Token: "/keys/evil\x1b[31m", Title: "evil\x1b[31mx\x07", Intent: termnav.IntentReference, Badge: "file"},
		},
	}})
	out := renderDirBrowse(m, pickerTheme{theme: termstyle.TerminalTheme().WithNoColor(true)}, "choose a folder", false)
	for _, want := range []string{"CHOOSE A FOLDER", "Use this folder", "work/", "folder  /keys"} {
		if !strings.Contains(out, want) {
			t.Errorf("frame missing %q\n%s", want, out)
		}
	}
	// The NoColor theme emits no escapes of its own, so any escape/control byte
	// in the frame would be a leak from the hostile filename.
	if strings.ContainsAny(out, "\x1b\x07") {
		t.Fatalf("hostile filename leaked an escape byte into the frame:\n%q", out)
	}
}
