package passstore

import (
	"os"
	"path/filepath"
	"testing"
)

// gitStore makes a temp dir look like a git-backed store (a .git directory) so
// CheckDrift does not short-circuit. Returns the store dir.
func gitStore(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return root
}

func TestClassifyDrift(t *testing.T) {
	mk := func(flagged int, changes bool, ahead, behind string) dangitEnvelope {
		e := dangitEnvelope{}
		e.Summary.Flagged = flagged
		e.Summary.Total = 1
		if flagged > 0 {
			e.Repos = append(e.Repos, struct {
				Path    string `json:"path"`
				Branch  string `json:"branch"`
				Changes bool   `json:"changes"`
				Ahead   string `json:"ahead"`
				Behind  string `json:"behind"`
				Err     string `json:"error,omitempty"`
			}{Path: ".", Branch: "main", Changes: changes, Ahead: ahead, Behind: behind})
		}
		return e
	}
	cases := []struct {
		name string
		env  dangitEnvelope
		want DriftKind
	}{
		{"clean", mk(0, false, "0", "0"), DriftClean},
		{"behind-numeric", mk(1, false, "0", "2"), DriftBehind},
		{"behind-unknown", mk(1, false, "0", "unknown"), DriftBehind},
		{"behind-wins-over-changes", mk(1, true, "0", "unknown"), DriftBehind},
		{"stale", mk(1, false, "0", "stale"), DriftStale},
		{"no-remote", mk(1, false, "no-upstream", "0"), DriftNoRemote},
		{"local-changes", mk(1, true, "0", "0"), DriftLocal},
		{"local-ahead", mk(1, false, "3", "0"), DriftLocal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDrift(tc.env).Kind; got != tc.want {
				t.Fatalf("classifyDrift kind = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckDriftBehind(t *testing.T) {
	store := gitStore(t)
	bin := t.TempDir()
	// dangit exits 1 (flagged) and prints the behind envelope on stdout.
	dangit := makeScript(t, bin, "dangit", `cat <<'JSON'
{"root":".","summary":{"total":1,"flagged":1,"behind":1,"behind_unknown":1},"repos":[{"path":".","branch":"main","changes":false,"ahead":"0","behind":"unknown"}]}
JSON
exit 1`)
	got := CheckDrift(t.Context(), dangit, store, os.Environ())
	if got.Kind != DriftBehind {
		t.Fatalf("Kind = %v, want DriftBehind", got.Kind)
	}
	if got.Branch != "main" || got.Behind != "unknown" {
		t.Fatalf("status = %+v", got)
	}
}

func TestCheckDriftClean(t *testing.T) {
	store := gitStore(t)
	bin := t.TempDir()
	dangit := makeScript(t, bin, "dangit", `printf '{"summary":{"total":1,"flagged":0},"repos":[]}\n'; exit 0`)
	if got := CheckDrift(t.Context(), dangit, store, os.Environ()); got.Kind != DriftClean {
		t.Fatalf("Kind = %v, want DriftClean", got.Kind)
	}
}

func TestCheckDriftMissingBinaryIsUnknown(t *testing.T) {
	store := gitStore(t)
	got := CheckDrift(t.Context(), filepath.Join(t.TempDir(), "no-such-dangit"), store, os.Environ())
	if got.Kind != DriftUnknown {
		t.Fatalf("Kind = %v, want DriftUnknown", got.Kind)
	}
}

func TestCheckDriftNonGitStoreSkipsDangit(t *testing.T) {
	store := t.TempDir() // no .git
	bin := t.TempDir()
	sentinel := filepath.Join(t.TempDir(), "ran")
	dangit := makeScript(t, bin, "dangit", `touch "`+sentinel+`"; exit 0`)
	if got := CheckDrift(t.Context(), dangit, store, os.Environ()); got.Kind != DriftUnknown {
		t.Fatalf("Kind = %v, want DriftUnknown", got.Kind)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("dangit was invoked for a non-git store; it should be skipped")
	}
}

func TestCheckDriftUnparseableIsUnknown(t *testing.T) {
	store := gitStore(t)
	bin := t.TempDir()
	// Mimic an exit-2 usage error: no JSON on stdout.
	dangit := makeScript(t, bin, "dangit", `echo "dangit: bad path" >&2; exit 2`)
	if got := CheckDrift(t.Context(), dangit, store, os.Environ()); got.Kind != DriftUnknown {
		t.Fatalf("Kind = %v, want DriftUnknown", got.Kind)
	}
}

func TestResolveDangitBinary(t *testing.T) {
	if got := ResolveDangitBinary(nil); got != "dangit" {
		t.Fatalf("default = %q, want dangit", got)
	}
	if got := ResolveDangitBinary([]string{"PASSAGE_DANGIT_BINARY=/opt/dangit"}); got != "/opt/dangit" {
		t.Fatalf("override = %q, want /opt/dangit", got)
	}
}

func TestGitPullInvokesPassGitFfOnly(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	pass := makeScript(t, bin, "pass", `if [ "$1" = git ] && [ "$2" = pull ] && [ "$3" = --ff-only ]; then exit 0; fi
echo "unexpected args: $*" >&2; exit 9`)
	store := New(root, pass)
	if err := store.GitPull(t.Context()); err != nil {
		t.Fatalf("GitPull: %v", err)
	}
}

func TestGitPullSurfacesError(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	pass := makeScript(t, bin, "pass", `echo "fatal: Not possible to fast-forward, aborting." >&2; exit 128`)
	store := New(root, pass)
	err := store.GitPull(t.Context())
	if err == nil {
		t.Fatal("GitPull returned nil error on failure")
	}
}
