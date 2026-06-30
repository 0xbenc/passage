package passstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/0xbenc/passage/internal/procutil"
)

// DriftKind classifies how a git-backed pass store relates to its remote, as
// reported by the external `dangit` CLI. Only DriftBehind warrants an
// interactive prompt; the rest drive a notice or stay silent.
type DriftKind int

const (
	// DriftUnknown means we could not determine the status — dangit is not
	// installed, the store is not a git repo, the call timed out, or the output
	// did not parse. Always silent: a sync feature must never break passage for
	// users without dangit or a git store.
	DriftUnknown DriftKind = iota
	// DriftClean means the store is in sync with its remote (nothing flagged).
	DriftClean
	// DriftBehind means the remote has commits the local store lacks — someone
	// added keys/passwords/MFA elsewhere. This is the prompt-worthy case.
	DriftBehind
	// DriftLocal means there is only local work (uncommitted changes or
	// committed-but-unpushed commits) and no remote drift.
	DriftLocal
	// DriftStale means the remote could not be reached, so drift can't be
	// confirmed either way (offline, unreachable, or --no-network).
	DriftStale
	// DriftNoRemote means the store is a git repo with no upstream configured,
	// so there is nothing to sync against.
	DriftNoRemote
)

// DriftStatus is the classified result of a drift check, plus the raw dangit
// fields for context/messaging.
type DriftStatus struct {
	Kind    DriftKind
	Branch  string
	Behind  string // raw dangit value: "0" | <n> | "unknown" | "stale"
	Ahead   string // raw dangit value: "0" | <n> | "no-upstream"
	Changes bool
}

// dangitEnvelope mirrors the subset of `dangit scan --json` we consume. dangit
// lists only flagged repos in Repos; a clean store yields an empty slice.
type dangitEnvelope struct {
	Summary struct {
		Total   int `json:"total"`
		Flagged int `json:"flagged"`
	} `json:"summary"`
	Repos []struct {
		Path    string `json:"path"`
		Branch  string `json:"branch"`
		Changes bool   `json:"changes"`
		Ahead   string `json:"ahead"`
		Behind  string `json:"behind"`
		Err     string `json:"error,omitempty"`
	} `json:"repos"`
}

// ResolveDangitBinary picks the dangit binary: PASSAGE_DANGIT_BINARY if set,
// else "dangit" on PATH. Mirrors ResolvePassBinary.
func ResolveDangitBinary(env []string) string {
	values := envMap(env)
	if binary := strings.TrimSpace(values["PASSAGE_DANGIT_BINARY"]); binary != "" {
		return binary
	}
	return "dangit"
}

// CheckDrift asks dangit whether the store at storeDir has drifted from its
// remote, and classifies the answer. It never returns an error: anything it
// cannot determine collapses to DriftUnknown (silent), so a missing dangit, a
// non-git store, a timeout, or unparseable output all degrade gracefully.
//
// The caller is responsible for bounding ctx with a timeout — dangit's own
// --timeout-secs does not reliably bound an unreachable HTTPS remote, but the
// process-group cancellation wired here SIGKILLs the subprocess when ctx ends.
func CheckDrift(ctx context.Context, dangitBinary, storeDir string, env []string) DriftStatus {
	storeDir = strings.TrimSpace(storeDir)
	if storeDir == "" {
		return DriftStatus{Kind: DriftUnknown}
	}
	// Short-circuit a non-git store: nothing to sync, and no point forking dangit.
	if info, err := os.Stat(filepath.Join(storeDir, ".git")); err != nil || !info.IsDir() {
		return DriftStatus{Kind: DriftUnknown}
	}

	cmd := exec.CommandContext(ctx, dangitBinary, "scan", "--json", "--timeout-secs", "5", storeDir)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = env
	cmd.Dir = storeDir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// A flagged store exits non-zero (1) by design — that is expected and the
		// JSON is still on stdout. Only a failure to *start* (e.g. dangit not
		// installed) or a non-exit error means we have nothing to parse.
		if _, ok := err.(*exec.ExitError); !ok {
			return DriftStatus{Kind: DriftUnknown}
		}
	}

	var env2 dangitEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env2); err != nil {
		return DriftStatus{Kind: DriftUnknown}
	}
	return classifyDrift(env2)
}

func classifyDrift(env dangitEnvelope) DriftStatus {
	if env.Summary.Flagged == 0 || len(env.Repos) == 0 {
		return DriftStatus{Kind: DriftClean}
	}
	// A single-repo scan (the store) reports exactly one entry, path ".".
	r := env.Repos[0]
	status := DriftStatus{Branch: r.Branch, Behind: r.Behind, Ahead: r.Ahead, Changes: r.Changes}

	switch {
	case isBehind(r.Behind):
		status.Kind = DriftBehind
	case r.Behind == "stale":
		status.Kind = DriftStale
	case r.Ahead == "no-upstream":
		status.Kind = DriftNoRemote
	case r.Changes || isAhead(r.Ahead):
		status.Kind = DriftLocal
	default:
		status.Kind = DriftClean
	}
	return status
}

// isBehind reports whether dangit's behind value means "the remote has commits
// we lack": a positive count or "unknown" (reached the remote, differs, but the
// exact count needs a fetch). "stale" (couldn't reach) and "0" are not behind.
func isBehind(behind string) bool {
	switch behind {
	case "", "0", "stale":
		return false
	default:
		return true
	}
}

// isAhead reports whether dangit's ahead value means "local commits not pushed":
// a positive count. "no-upstream" and "0" are not ahead in that sense.
func isAhead(ahead string) bool {
	switch ahead {
	case "", "0", "no-upstream":
		return false
	default:
		return true
	}
}

// GitPull fast-forwards the store from its remote via `pass git pull --ff-only`.
// ff-only keeps it safe: it never creates a merge commit, and fails cleanly if
// the local store has diverged (the caller surfaces that as a notice).
// GIT_TERMINAL_PROMPT=0 prevents a credential prompt from hanging the TUI.
func (s Store) GitPull(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, s.PassBinary, "git", "pull", "--ff-only")
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = append(s.writeEnv(), "GIT_TERMINAL_PROMPT=0")
	return s.runWrite(ctx, cmd, "git pull")
}
