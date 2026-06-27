package passstore

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGPGID(t *testing.T, dir string, ids ...string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	body := ""
	for _, id := range ids {
		body += id + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, ".gpg-id"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile .gpg-id: %v", err)
	}
}

func TestParseGPGID(t *testing.T) {
	got := ParseGPGID([]byte("alice@corp\n\n  bob@corp  \n# a comment\nFPR123\n"))
	want := []string{"alice@corp", "bob@corp", "FPR123"}
	if !equal(got, want) {
		t.Fatalf("ParseGPGID = %#v, want %#v", got, want)
	}
}

func TestResolveRecipientsFileNearestAncestor(t *testing.T) {
	root := t.TempDir()
	writeGPGID(t, root, "root@id")
	writeGPGID(t, filepath.Join(root, "work", "aws"), "owner@id", "alice@corp")

	// An entry deep under work/aws resolves to the work/aws/.gpg-id, not root.
	path, ids, ok, err := ResolveRecipientsFile(root, filepath.Join(root, "work", "aws", "prod"))
	if err != nil || !ok {
		t.Fatalf("resolve work/aws/prod: ok=%v err=%v", ok, err)
	}
	if path != filepath.Join(root, "work", "aws", ".gpg-id") {
		t.Fatalf("resolved path = %s", path)
	}
	if !equal(ids, []string{"owner@id", "alice@corp"}) {
		t.Fatalf("ids = %#v", ids)
	}

	// A sibling without its own .gpg-id inherits the root one.
	path, ids, ok, err = ResolveRecipientsFile(root, filepath.Join(root, "personal"))
	if err != nil || !ok {
		t.Fatalf("resolve personal: ok=%v err=%v", ok, err)
	}
	if path != filepath.Join(root, ".gpg-id") || !equal(ids, []string{"root@id"}) {
		t.Fatalf("personal resolved to %s ids=%#v", path, ids)
	}
}

func TestResolveRecipientsFileUninitialized(t *testing.T) {
	root := t.TempDir()
	// No .gpg-id anywhere up to root.
	_, _, ok, err := ResolveRecipientsFile(root, filepath.Join(root, "work"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false when no .gpg-id exists")
	}
}

func TestScopeDirsAnyDepth(t *testing.T) {
	root := t.TempDir()
	writeGPGID(t, root, "root@id")
	writeGPGID(t, filepath.Join(root, "work"), "work@id")
	writeGPGID(t, filepath.Join(root, "work", "aws", "prod"), "prod@id") // deeply nested
	// .git should be skipped even if it somehow holds a .gpg-id.
	writeGPGID(t, filepath.Join(root, ".git"), "noise@id")

	scopes, err := ScopeDirs(root)
	if err != nil {
		t.Fatalf("ScopeDirs: %v", err)
	}
	want := []string{"", "work", "work/aws/prod"}
	if !equal(scopes, want) {
		t.Fatalf("ScopeDirs = %#v, want %#v", scopes, want)
	}
}

func TestScopeDirsEmptyStore(t *testing.T) {
	root := t.TempDir()
	scopes, err := ScopeDirs(root)
	if err != nil {
		t.Fatalf("ScopeDirs: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("ScopeDirs = %#v, want empty", scopes)
	}
}
