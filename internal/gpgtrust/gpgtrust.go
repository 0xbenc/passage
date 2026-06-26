// Package gpgtrust makes read-only password-store scopes writable by
// local-signing (and optionally raising ownertrust on) the recipients gpg
// cannot yet encrypt to. It is the Go-native port of bash-zoo's gpgobble,
// adapted to passage: the default is local-sign only (which confers exactly the
// validity pass needs), with ownertrust=full as an explicit opt-in. The engine
// is split into Plan (a dry-run preview that mutates nothing) and Apply (the
// only mutating call), preserving gpgobble's idempotency and never-downgrade
// semantics.
package gpgtrust

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/0xbenc/passage/internal/gpgdiag"
	"github.com/0xbenc/passage/internal/passstore"
)

// Strength selects how much trust Apply confers.
type Strength int

const (
	// Lsign local-signs each fixable recipient — enough to make it a valid
	// encryption target — and is the default.
	Lsign Strength = iota
	// Full additionally raises ownertrust to full (5), enabling web-of-trust
	// propagation passage does not strictly need.
	Full
)

func (s Strength) String() string {
	if s == Full {
		return "lsign+ownertrust-full"
	}
	return "lsign-only"
}

// Action is what Apply will do for one recipient.
type Action string

const (
	ActionWouldLsign   Action = "would-lsign"   // present (or to-be-imported) but not encryptable
	ActionAlreadyValid Action = "already-valid" // already encryptable; nothing to do
	ActionOwnedSkip    Action = "owned-skip"    // you hold the secret key
	ActionMissing      Action = "missing"       // not in keyring and no import source
	ActionUnusable     Action = "unusable"      // expired/revoked/disabled — lsign cannot help
)

type RecipientPlan struct {
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint,omitempty"`
	UID         string `json:"uid,omitempty"`
	Action      Action `json:"action"`
	WillImport  bool   `json:"will_import,omitempty"`
}

// Plan is a dry-run preview; building it mutates nothing.
type Plan struct {
	SchemaVersion int             `json:"schema_version"`
	Scope         string          `json:"scope"`
	GPGIDPath     string          `json:"gpg_id_path,omitempty"`
	Strength      string          `json:"strength"`
	Recipients    []RecipientPlan `json:"recipients"`
	ImportFiles   []string        `json:"import_files,omitempty"`
}

// Actionable reports whether Apply would change anything.
func (p Plan) Actionable() bool {
	for _, r := range p.Recipients {
		if r.Action == ActionWouldLsign {
			return true
		}
	}
	return false
}

type ApplyResult struct {
	Fingerprint string `json:"fingerprint"`
	UID         string `json:"uid,omitempty"`
	Signed      bool   `json:"signed"`
	TrustSet    bool   `json:"trust_set,omitempty"`
	Err         string `json:"error,omitempty"`
}

type ApplyReport struct {
	SchemaVersion     int           `json:"schema_version"`
	Scope             string        `json:"scope"`
	DryRun            bool          `json:"dry_run"`
	Imported          []string      `json:"imported,omitempty"`
	Results           []ApplyResult `json:"results"`
	OwnerTrustApplied []string      `json:"ownertrust_applied,omitempty"`
	NowWritable       bool          `json:"now_writable"`
}

// Truster runs the trust engine against one store.
type Truster struct {
	GPGBinary string
	StoreRoot string
	// Stdin/Stdout/Stderr wire the lsign subprocess to the controlling terminal
	// so pinentry can prompt. Stdout is kept separate from the JSON stdout.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Env    []string
}

func New(storeRoot string) Truster {
	return Truster{GPGBinary: "gpg", StoreRoot: storeRoot}
}

func (t Truster) gpg() string {
	if strings.TrimSpace(t.GPGBinary) == "" {
		return "gpg"
	}
	return t.GPGBinary
}

func (t Truster) diag() gpgdiag.Checker {
	return gpgdiag.Checker{GPGBinary: t.gpg(), StoreRoot: t.StoreRoot}
}

// PlanRecipients builds a preview for the recipients of the .gpg-id governing
// scope (an entry path or directory, "" for the store root).
func (t Truster) PlanRecipients(ctx context.Context, scope string, strength Strength) (Plan, error) {
	startDir := t.scopeDir(scope)
	gpgIDPath, ids, ok, err := passstore.ResolveRecipientsFile(t.StoreRoot, startDir)
	if err != nil {
		return Plan{}, err
	}
	if !ok {
		return Plan{}, fmt.Errorf("no .gpg-id governs %q", scope)
	}
	plan := Plan{
		SchemaVersion: 1,
		Scope:         relScope(t.StoreRoot, filepath.Dir(gpgIDPath)),
		GPGIDPath:     gpgIDPath,
		Strength:      strength.String(),
	}
	d := t.diag()
	for _, token := range ids {
		plan.Recipients = append(plan.Recipients, t.planToken(ctx, d, token))
	}
	return plan, nil
}

func (t Truster) planToken(ctx context.Context, d gpgdiag.Checker, token string) RecipientPlan {
	rp := RecipientPlan{Token: token}
	fpr, uid, present := d.ResolvePrimary(ctx, token)
	rp.Fingerprint = fpr
	rp.UID = uid
	switch d.RecipientStatus(ctx, token) {
	case gpgdiag.RecipientOwned:
		rp.Action = ActionOwnedSkip
	case gpgdiag.RecipientEncryptable:
		rp.Action = ActionAlreadyValid
	case gpgdiag.RecipientUnusable:
		rp.Action = ActionUnusable
	case gpgdiag.RecipientMissing:
		rp.Action = ActionMissing
	default: // invalid → fixable by local-sign
		if present {
			rp.Action = ActionWouldLsign
		} else {
			rp.Action = ActionMissing
		}
	}
	return rp
}

// PlanImportDir builds a preview for importing a folder of public-key files and
// trusting every key it contains (gpgobble parity). Keys are peeked with
// show-only so the preview imports nothing.
func (t Truster) PlanImportDir(ctx context.Context, dir string, strength Strength) (Plan, error) {
	files, err := keyFiles(dir)
	if err != nil {
		return Plan{}, err
	}
	if len(files) == 0 {
		return Plan{}, fmt.Errorf("no key files found in %s", dir)
	}
	plan := Plan{SchemaVersion: 1, Scope: dir, Strength: strength.String(), ImportFiles: files}
	d := t.diag()
	seen := map[string]bool{}
	for _, f := range files {
		for _, peek := range t.peekFile(ctx, f) {
			if seen[peek.Fingerprint] {
				continue
			}
			seen[peek.Fingerprint] = true
			plan.Recipients = append(plan.Recipients, t.planImportedKey(ctx, d, peek))
		}
	}
	return plan, nil
}

type peekedKey struct {
	Fingerprint string
	UID         string
}

func (t Truster) planImportedKey(ctx context.Context, d gpgdiag.Checker, key peekedKey) RecipientPlan {
	rp := RecipientPlan{Token: key.Fingerprint, Fingerprint: key.Fingerprint, UID: key.UID}
	if d.HasSecret(ctx, key.Fingerprint) {
		rp.Action = ActionOwnedSkip
		return rp
	}
	// If already in the keyring and encryptable, nothing to do.
	if _, _, present := d.ResolvePrimary(ctx, key.Fingerprint); present {
		if d.CanEncryptTo(ctx, key.Fingerprint) {
			rp.Action = ActionAlreadyValid
			return rp
		}
		rp.Action = ActionWouldLsign
		return rp
	}
	// Not present yet: import then local-sign.
	rp.Action = ActionWouldLsign
	rp.WillImport = true
	return rp
}

// Apply performs the mutations described by plan. It is idempotent: re-signing
// is a gpg no-op and ownertrust is never downgraded. dryRun returns the report
// without touching the keyring.
func (t Truster) Apply(ctx context.Context, plan Plan, strength Strength, dryRun bool) (ApplyReport, error) {
	report := ApplyReport{SchemaVersion: 1, Scope: plan.Scope, DryRun: dryRun}
	if dryRun {
		for _, r := range plan.Recipients {
			if r.Action == ActionWouldLsign {
				report.Results = append(report.Results, ApplyResult{Fingerprint: r.Fingerprint, UID: r.UID})
			}
		}
		return report, nil
	}

	// 1) Import any key files first so their fingerprints become resolvable.
	if len(plan.ImportFiles) > 0 {
		for _, f := range plan.ImportFiles {
			if err := t.importFile(ctx, f); err == nil {
				report.Imported = append(report.Imported, f)
			}
		}
	}

	// 2) Local-sign each fixable recipient (pinentry may prompt).
	var toFull []ApplyResult
	for _, r := range plan.Recipients {
		if r.Action != ActionWouldLsign || r.Fingerprint == "" {
			continue
		}
		res := ApplyResult{Fingerprint: r.Fingerprint, UID: r.UID}
		if err := t.lsign(ctx, r.Fingerprint); err != nil {
			res.Err = err.Error()
		} else {
			res.Signed = true
			toFull = append(toFull, res)
		}
		report.Results = append(report.Results, res)
	}

	// 3) Optionally raise ownertrust to full (5), never downgrading 5/6.
	if strength == Full && len(toFull) > 0 {
		applied, err := t.applyOwnerTrust(ctx, toFull)
		if err != nil {
			return report, err
		}
		report.OwnerTrustApplied = applied
		for i := range report.Results {
			for _, fp := range applied {
				if report.Results[i].Fingerprint == fp {
					report.Results[i].TrustSet = true
				}
			}
		}
	}

	// 4) Confirm the flip (recipient-scope plans only; import-dir has no scope).
	if plan.GPGIDPath != "" {
		if data, err := os.ReadFile(plan.GPGIDPath); err == nil {
			report.NowWritable = t.diag().ScopeWritable(ctx, passstore.ParseGPGID(data))
		}
	}
	return report, nil
}

func (t Truster) applyOwnerTrust(ctx context.Context, results []ApplyResult) ([]string, error) {
	trust, err := t.diag().OwnerTrust(ctx)
	if err != nil {
		return nil, err
	}
	var lines []string
	var applied []string
	seen := map[string]bool{}
	for _, r := range results {
		fp := r.Fingerprint
		if fp == "" || seen[fp] {
			continue
		}
		seen[fp] = true
		// Never downgrade an existing full (5) or ultimate (6) trust. gpg's
		// ownertrust values are 4=marginal, 5=full, 6=ultimate.
		if lvl := trust[fp]; lvl == "5" || lvl == "6" {
			continue
		}
		lines = append(lines, fp+":5:")
		applied = append(applied, fp)
	}
	if len(applied) == 0 {
		return nil, nil
	}
	sort.Strings(lines)
	cmd := exec.CommandContext(ctx, t.gpg(), "--batch", "--yes", "--quiet", "--import-ownertrust")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("import-ownertrust failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return applied, nil
}

// lsign local-signs a key. It is deliberately NOT run through a cancellation
// process group: pinentry must own the controlling terminal's foreground group,
// and the y/N confirm is auto-answered by --yes while the passphrase prompt
// still reaches the tty.
func (t Truster) lsign(ctx context.Context, fingerprint string) error {
	cmd := exec.CommandContext(ctx, t.gpg(), "--quiet", "--yes", "--quick-lsign-key", fingerprint)
	cmd.Env = t.env()
	cmd.Stdin = t.stdin()
	cmd.Stdout = t.stdout()
	cmd.Stderr = t.stderr()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("local-sign %s failed: %w", fingerprint, err)
	}
	return nil
}

func (t Truster) importFile(ctx context.Context, file string) error {
	cmd := exec.CommandContext(ctx, t.gpg(), "--batch", "--yes", "--quiet", "--import", file)
	cmd.Env = t.env()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("import %s failed: %w: %s", file, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (t Truster) peekFile(ctx context.Context, file string) []peekedKey {
	cmd := exec.CommandContext(ctx, t.gpg(), "--with-colons", "--import-options", "show-only", "--import", file)
	cmd.Env = t.env()
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var keys []peekedKey
	var cur *peekedKey
	pubPending := false
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub":
			if cur != nil && cur.Fingerprint != "" {
				keys = append(keys, *cur)
			}
			cur = &peekedKey{}
			pubPending = true
		case "fpr":
			if cur != nil && pubPending {
				cur.Fingerprint = fields[9]
				pubPending = false
			}
		case "uid":
			if cur != nil && cur.UID == "" {
				cur.UID = fields[9]
			}
		}
	}
	if cur != nil && cur.Fingerprint != "" {
		keys = append(keys, *cur)
	}
	return keys
}

func (t Truster) scopeDir(scope string) string {
	if strings.TrimSpace(scope) == "" {
		return t.StoreRoot
	}
	candidate := filepath.Join(t.StoreRoot, filepath.FromSlash(scope))
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return filepath.Dir(candidate)
}

func (t Truster) env() []string {
	if t.Env != nil {
		return t.Env
	}
	return os.Environ()
}

func (t Truster) stdin() io.Reader {
	if t.Stdin != nil {
		return t.Stdin
	}
	return os.Stdin
}

func (t Truster) stdout() io.Writer {
	if t.Stdout != nil {
		return t.Stdout
	}
	return os.Stderr
}

func (t Truster) stderr() io.Writer {
	if t.Stderr != nil {
		return t.Stderr
	}
	return os.Stderr
}

func keyFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read key dir %s: %w", dir, err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	sort.Strings(files)
	return files, nil
}

func relScope(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}
