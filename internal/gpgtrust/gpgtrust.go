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
	"strconv"
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
	ActionWouldLsign    Action = "would-lsign"     // present (or to-be-imported) but not encryptable
	ActionWouldOwnTrust Action = "would-own-trust" // your own key (secret held) but untrusted — set ultimate
	ActionAlreadyValid  Action = "already-valid"   // already encryptable; nothing to do
	ActionOwnedSkip     Action = "owned-skip"      // you hold the secret key and it's already valid
	ActionMissing       Action = "missing"         // not in keyring and no import source
	ActionUnusable      Action = "unusable"        // expired/revoked/disabled — neither lsign nor trust helps
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
		if r.Action == ActionWouldLsign || r.Action == ActionWouldOwnTrust {
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
	// A key you hold the secret for is provably yours. If it isn't a valid
	// encryption target yet (and isn't expired/revoked), the fix is ultimate
	// trust — never a local-signature (you don't lsign your own key).
	if d.HasSecret(ctx, token) {
		switch {
		case d.CanEncryptTo(ctx, token):
			rp.Action = ActionOwnedSkip
		case d.Unusable(ctx, token):
			rp.Action = ActionUnusable
		default:
			rp.Action = ActionWouldOwnTrust
		}
		return rp
	}
	if !present {
		rp.Action = ActionMissing
		return rp
	}
	switch d.RecipientStatus(ctx, token) {
	case gpgdiag.RecipientEncryptable:
		rp.Action = ActionAlreadyValid
	case gpgdiag.RecipientUnusable:
		rp.Action = ActionUnusable
	case gpgdiag.RecipientMissing:
		rp.Action = ActionMissing
	default: // present but invalid → fixable by local-sign
		rp.Action = ActionWouldLsign
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
	HasSecret   bool // the peeked file carried secret-key material for this key
}

// SecretImportKey previews one key found in the chosen files.
type SecretImportKey struct {
	Fingerprint string `json:"fingerprint"`
	UID         string `json:"uid,omitempty"`
	HasSecret   bool   `json:"has_secret"`
}

// SecretImportPlan previews a secret-key import; building it imports nothing.
type SecretImportPlan struct {
	SchemaVersion int               `json:"schema_version"`
	Files         []string          `json:"files"`
	Keys          []SecretImportKey `json:"keys"`
}

// OwnKeyCount returns how many keys the files carry the secret for (the ones
// this flow will set up as yours).
func (p SecretImportPlan) OwnKeyCount() int {
	n := 0
	for _, k := range p.Keys {
		if k.HasSecret {
			n++
		}
	}
	return n
}

type SecretImportReport struct {
	SchemaVersion int      `json:"schema_version"`
	Imported      []string `json:"imported,omitempty"`    // files imported
	Trusted       []string `json:"trusted,omitempty"`     // fingerprints set ultimate
	PublicOnly    []string `json:"public_only,omitempty"` // public-only keys imported but not trusted
}

// PlanImportSecrets peeks the chosen key files (show-only, no mutation) so the
// caller can show which carry secret-key material before importing.
func (t Truster) PlanImportSecrets(ctx context.Context, files []string) (SecretImportPlan, error) {
	if len(files) == 0 {
		return SecretImportPlan{}, fmt.Errorf("no key files selected")
	}
	plan := SecretImportPlan{SchemaVersion: 1, Files: files}
	seen := map[string]bool{}
	for _, f := range files {
		for _, k := range t.peekFile(ctx, f) {
			if k.Fingerprint == "" || seen[k.Fingerprint] {
				continue
			}
			seen[k.Fingerprint] = true
			plan.Keys = append(plan.Keys, SecretImportKey{Fingerprint: k.Fingerprint, UID: k.UID, HasSecret: k.HasSecret})
		}
	}
	return plan, nil
}

// ApplyImportSecrets imports the files and sets ultimate trust on every key you
// now hold the secret for but that isn't a valid encryption target yet — making
// your own keys properly yours on a new machine. Public-only keys are imported
// but left untrusted (trust those via the public-key import / trust flow).
func (t Truster) ApplyImportSecrets(ctx context.Context, plan SecretImportPlan) (SecretImportReport, error) {
	report := SecretImportReport{SchemaVersion: 1}
	for _, f := range plan.Files {
		if err := t.importFile(ctx, f); err == nil {
			report.Imported = append(report.Imported, f)
		}
	}
	d := t.diag()
	var targets []trustTarget
	for _, k := range plan.Keys {
		if !k.HasSecret {
			report.PublicOnly = append(report.PublicOnly, k.Fingerprint)
			continue
		}
		// Imported as yours: ultimate-trust it if it's owned now and not already
		// a valid target (and not expired/revoked).
		if d.HasSecret(ctx, k.Fingerprint) && !d.CanEncryptTo(ctx, k.Fingerprint) && !d.Unusable(ctx, k.Fingerprint) {
			targets = append(targets, trustTarget{fp: k.Fingerprint, level: 6})
		}
	}
	if len(targets) > 0 {
		applied, err := t.applyOwnerTrust(ctx, targets)
		if err != nil {
			return report, err
		}
		report.Trusted = applied
	}
	return report, nil
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
			if r.Action == ActionWouldLsign || r.Action == ActionWouldOwnTrust {
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

	// 3) Ownertrust changes, never downgraded. Your own untrusted keys get
	// ultimate (6) always — that's the fix for them; with --full, the foreign
	// keys we just local-signed additionally get full (5).
	var targets []trustTarget
	for _, r := range plan.Recipients {
		if r.Action == ActionWouldOwnTrust && r.Fingerprint != "" {
			targets = append(targets, trustTarget{fp: r.Fingerprint, level: 6})
			report.Results = append(report.Results, ApplyResult{Fingerprint: r.Fingerprint, UID: r.UID})
		}
	}
	if strength == Full {
		for _, res := range toFull {
			targets = append(targets, trustTarget{fp: res.Fingerprint, level: 5})
		}
	}
	if len(targets) > 0 {
		applied, err := t.applyOwnerTrust(ctx, targets)
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

// trustTarget is a fingerprint and the ownertrust level to raise it to (gpg's
// 4=marginal, 5=full, 6=ultimate).
type trustTarget struct {
	fp    string
	level int
}

func (t Truster) applyOwnerTrust(ctx context.Context, targets []trustTarget) ([]string, error) {
	trust, err := t.diag().OwnerTrust(ctx)
	if err != nil {
		return nil, err
	}
	// Dedup to the highest requested level per fingerprint.
	want := map[string]int{}
	for _, tg := range targets {
		if tg.fp == "" {
			continue
		}
		if tg.level > want[tg.fp] {
			want[tg.fp] = tg.level
		}
	}
	var lines []string
	var applied []string
	for fp, level := range want {
		// Never downgrade: skip when the existing level already meets or exceeds
		// the target.
		if cur := ownerTrustLevel(trust[fp]); cur >= level {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d:", fp, level))
		applied = append(applied, fp)
	}
	if len(applied) == 0 {
		return nil, nil
	}
	sort.Strings(lines)
	sort.Strings(applied)
	cmd := exec.CommandContext(ctx, t.gpg(), "--batch", "--yes", "--quiet", "--import-ownertrust")
	cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("import-ownertrust failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return applied, nil
}

func ownerTrustLevel(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
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
	fprPending := false
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub", "sec": // a primary key block — sec means the file holds the secret
			if cur != nil && cur.Fingerprint != "" {
				keys = append(keys, *cur)
			}
			cur = &peekedKey{HasSecret: fields[0] == "sec"}
			fprPending = true
		case "fpr":
			if cur != nil && fprPending {
				cur.Fingerprint = fields[9]
				fprPending = false
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
