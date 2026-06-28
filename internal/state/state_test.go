package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMigratesLegacyTSV(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateHome := filepath.Join(home, ".local", "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	oldDir := filepath.Join(stateHome, "bash-zoo", "passage")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	oldPath := filepath.Join(oldDir, "state.tsv")
	if err := os.WriteFile(oldPath, []byte("alpha\t1\t10\nignored-empty\t\t\nbeta\t0\t20\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	newDir := filepath.Join(stateHome, "passage")
	result, err := Load(newDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !result.Migrated || result.OldPath != oldPath {
		t.Fatalf("migration = %v %q, want %q", result.Migrated, result.OldPath, oldPath)
	}
	alpha := result.State.Record("alpha")
	if !alpha.Pinned || alpha.LastUsed != 10 {
		t.Fatalf("alpha = %#v", alpha)
	}
	beta := result.State.Record("beta")
	if beta.Pinned || beta.LastUsed != 20 {
		t.Fatalf("beta = %#v", beta)
	}
}

func TestSaveAndLoadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := Empty()
	st.SetPinned("alpha", true)
	st.Touch("alpha", 123)
	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := result.State.Record("alpha")
	if !got.Pinned || got.LastUsed != 123 {
		t.Fatalf("record = %#v", got)
	}
}

// TestLastIntroVersionRoundTrip guards that LastIntroVersion (a top-level field,
// not derived from Records) survives Save→Load and is not dropped by
// normalized(), which runs on both paths.
func TestLastIntroVersionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := Empty()
	st.SetPinned("alpha", true)
	st.SetLastIntroVersion("1.2.3")
	if err := st.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	result, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := result.State.LastIntroVersion; got != "1.2.3" {
		t.Fatalf("LastIntroVersion = %q, want %q", got, "1.2.3")
	}
	// And the records must still survive alongside it.
	if !result.State.Record("alpha").Pinned {
		t.Fatalf("alpha pin lost across round-trip: %#v", result.State.Record("alpha"))
	}
}

// TestNormalizedPreservesLastIntroVersion pins the in-memory invariant directly:
// normalized() (called on Load and Save) must carry the field through.
func TestNormalizedPreservesLastIntroVersion(t *testing.T) {
	st := Empty()
	st.SetLastIntroVersion("v9")
	if got := st.normalized().LastIntroVersion; got != "v9" {
		t.Fatalf("normalized LastIntroVersion = %q, want %q", got, "v9")
	}
}
