package passstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xbenc/passage/internal/state"
)

func TestDiscoverBuildEntriesSortAndMFA(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "work", "github.gpg"))
	touch(t, filepath.Join(root, "work", "github", "mfa.gpg"))
	touch(t, filepath.Join(root, "alpha.gpg"))
	touch(t, filepath.Join(root, ".git", "ignored.gpg"))
	st := state.Empty()
	st.SetPinned("alpha", true)
	st.Touch("work/github", 200)
	st.Touch("alpha", 100)

	paths, err := New(root, "pass").Discover()
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	wantPaths := []string{"alpha", "work/github", "work/github/mfa"}
	if !equal(paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", paths, wantPaths)
	}
	entries := BuildEntries(paths, st)
	if entries[0].Path != "alpha" {
		t.Fatalf("first entry = %s, want pinned alpha", entries[0].Path)
	}
	github, ok := find(entries, "work/github")
	if !ok {
		t.Fatal("missing work/github")
	}
	if !github.HasMFA || github.MFATarget != "work/github/mfa" {
		t.Fatalf("github MFA = %v %q, want sibling target", github.HasMFA, github.MFATarget)
	}
	direct, ok := find(entries, "work/github/mfa")
	if !ok || !direct.HasMFA || direct.MFATarget != "work/github/mfa" {
		t.Fatalf("direct MFA entry = %#v", direct)
	}
}

func TestShowPassesStoreDirToPass(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	pass := makeScript(t, bin, "pass", `if [ "$PASSWORD_STORE_DIR" != "$EXPECTED_STORE" ]; then echo "bad store: $PASSWORD_STORE_DIR" >&2; exit 8; fi
if [ "$1" = show ] && [ "$2" = -- ] && [ "$3" = alpha ]; then printf 'pw\nmeta\n'; exit 0; fi
exit 9`)
	t.Setenv("EXPECTED_STORE", root)
	store := New(root, pass)

	got, err := store.Show(t.Context(), "alpha")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if string(got) != "pw\nmeta\n" {
		t.Fatalf("Show = %q", got)
	}
}

func TestShowMirrorsPassStderr(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	pass := makeScript(t, bin, "pass", `echo "visible gpg prompt" >&2; exit 9`)
	var stderr bytes.Buffer
	store := New(root, pass)
	store.Stderr = &stderr

	_, err := store.Show(t.Context(), "alpha")
	if err == nil {
		t.Fatal("Show returned nil error")
	}
	if !strings.Contains(stderr.String(), "visible gpg prompt") {
		t.Fatalf("mirrored stderr = %q", stderr.String())
	}
	if !strings.Contains(err.Error(), "visible gpg prompt") {
		t.Fatalf("error = %q", err.Error())
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func find(entries []Entry, path string) (Entry, bool) {
	for _, entry := range entries {
		if entry.Path == path {
			return entry, true
		}
	}
	return Entry{}, false
}

func makeScript(t *testing.T, dir string, name string, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	data := []byte("#!/bin/sh\n" + body + "\n")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
	return path
}
