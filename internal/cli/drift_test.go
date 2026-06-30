package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/ui"
)

// TestPullTimeoutMessageIsStoreWide pins the corrected pull timeout error: it
// reports the real write deadline (60s, not the 12s read timeout) and never
// names an unrelated selected entry or the GPG `pass show` hint.
func TestPullTimeoutMessageIsStoreWide(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	err := interactiveActionError(ctx, ui.ActionRequest{Action: ui.ActionPull}, context.DeadlineExceeded)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "store pull timed out after 1m0s") {
		t.Fatalf("msg = %q, want store-wide 60s deadline", msg)
	}
	if strings.Contains(msg, "pass show") || strings.Contains(msg, "diagnose GPG") {
		t.Fatalf("pull timeout must not carry the entry/GPG hint: %q", msg)
	}
}

// TestSyncCheckDecision is the truth table for the pure sync-check gate (no TTY
// check). It defaults ON; suppression beats force.
func TestSyncCheckDecision(t *testing.T) {
	cases := []struct {
		name  string
		flags commonFlags
		env   []string
		want  bool
	}{
		{"default on", commonFlags{}, nil, true},
		{"flag disable", commonFlags{noSyncCheck: true}, nil, false},
		{"env disable", commonFlags{}, []string{"PASSAGE_NO_SYNC_CHECK=1"}, false},
		{"flag force", commonFlags{syncCheck: true}, nil, true},
		{"env force", commonFlags{}, []string{"PASSAGE_SYNC_CHECK_ALWAYS=1"}, true},
		{"disable beats force (flag)", commonFlags{noSyncCheck: true, syncCheck: true}, nil, false},
		{"disable beats force (env)", commonFlags{}, []string{"PASSAGE_NO_SYNC_CHECK=1", "PASSAGE_SYNC_CHECK_ALWAYS=1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := syncCheckDecision(tc.flags, tc.env); got != tc.want {
				t.Fatalf("syncCheckDecision = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseSyncCheckFlags confirms the flags are consumed (not left in rest).
func TestParseSyncCheckFlags(t *testing.T) {
	flags, rest, err := parseCommon([]string{"--no-sync-check", "alpha"})
	if err != nil {
		t.Fatalf("parseCommon: %v", err)
	}
	if !flags.noSyncCheck {
		t.Fatal("--no-sync-check not parsed")
	}
	if len(rest) != 1 || rest[0] != "alpha" {
		t.Fatalf("rest = %v, want [alpha]", rest)
	}
	flags, _, _ = parseCommon([]string{"--sync-check"})
	if !flags.syncCheck {
		t.Fatal("--sync-check not parsed")
	}
}

func TestActionPullPullsAndReloads(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	// Stub pass: succeed only for `git pull --ff-only`, asserting the store env.
	pass := makeScript2(t, bin, "pass", `if [ "$1" = git ] && [ "$2" = pull ] && [ "$3" = --ff-only ]; then
  [ "$PASSWORD_STORE_DIR" = "$EXPECT_STORE" ] || { echo "bad store $PASSWORD_STORE_DIR" >&2; exit 7; }
  exit 0
fi
echo "unexpected: $*" >&2; exit 9`)
	t.Setenv("PASSAGE_PASS_BINARY", pass)
	t.Setenv("EXPECT_STORE", store)

	r := runner{stdout: io.Discard, stderr: io.Discard, env: os.Environ(), build: BuildInfo{}.normalized()}
	flags := commonFlags{storeDir: store, stateDir: t.TempDir()}
	rt, err := r.load(flags)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := r.runInteractiveActionOnce(t.Context(), &rt, flags, ui.ActionRequest{Action: ui.ActionPull})
	if out.Err != nil {
		t.Fatalf("ActionPull err = %v", out.Err)
	}
	if !strings.Contains(out.Message, "Pulled latest") {
		t.Fatalf("message = %q", out.Message)
	}
	if len(out.Entries) == 0 {
		t.Fatal("ActionPull should reload entries after a pull")
	}
}

func TestActionPullSurfacesPullError(t *testing.T) {
	store := fakeStore(t)
	bin := t.TempDir()
	pass := makeScript2(t, bin, "pass", `echo "fatal: Not possible to fast-forward, aborting." >&2; exit 128`)
	t.Setenv("PASSAGE_PASS_BINARY", pass)

	r := runner{stdout: io.Discard, stderr: io.Discard, env: os.Environ(), build: BuildInfo{}.normalized()}
	flags := commonFlags{storeDir: store, stateDir: t.TempDir()}
	rt, err := r.load(flags)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	out := r.runInteractiveActionOnce(t.Context(), &rt, flags, ui.ActionRequest{Action: ui.ActionPull})
	if out.Err == nil {
		t.Fatal("ActionPull should surface the pull failure")
	}
	if !strings.Contains(out.Err.Error(), "fast-forward") {
		t.Fatalf("err = %v, want fast-forward context", out.Err)
	}
}

// TestDriftCheckClosureClassifiesBehind exercises the runner-built closure end
// to end against a stubbed dangit on PATH, proving the CLI wiring reaches
// passstore.CheckDrift and classifies a behind store.
func TestDriftCheckClosureClassifiesBehind(t *testing.T) {
	store := fakeStore(t)
	if err := os.Mkdir(filepath.Join(store, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	bin := t.TempDir()
	makeScript(t, bin, "dangit", `cat <<'JSON'
{"summary":{"total":1,"flagged":1},"repos":[{"path":".","branch":"main","changes":false,"ahead":"0","behind":"unknown"}]}
JSON
exit 1`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := runner{stdout: io.Discard, stderr: io.Discard, env: os.Environ(), build: BuildInfo{}.normalized()}
	status := r.driftCheckFunc(store)(t.Context())
	if status.Kind != passstore.DriftBehind {
		t.Fatalf("drift kind = %v, want DriftBehind", status.Kind)
	}
}
