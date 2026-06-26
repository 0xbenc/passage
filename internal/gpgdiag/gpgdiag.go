package gpgdiag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/0xbenc/passage/internal/passstore"
)

type Checker struct {
	GPGBinary string
	StoreRoot string
}

// Verdict is the per-scope answer to "can I write a password here?".
type Verdict string

const (
	// VerdictWritable: gpg can encrypt to every recipient — pass insert/edit
	// will succeed. Verified by an actual probe-encrypt, not by ownertrust.
	VerdictWritable Verdict = "writable"
	// VerdictReadOnly: at least one recipient is not a valid encryption target,
	// but you own a recipient secret key so you can still decrypt.
	VerdictReadOnly Verdict = "read_only"
	// VerdictNoAccess: cannot encrypt to all recipients and you own none, so
	// you can neither write nor decrypt here.
	VerdictNoAccess Verdict = "no_access"
	// VerdictUninitialized: no .gpg-id governs this path.
	VerdictUninitialized Verdict = "uninitialized"
)

// RecipientStatus classifies one .gpg-id recipient by what passage can do with
// it. The encryptable test is an actual probe-encrypt; the invalid/unusable
// split is read from gpg's computed validity only to drive fix messaging.
type RecipientStatus string

const (
	RecipientOwned       RecipientStatus = "owned"       // secret key present (you can decrypt)
	RecipientEncryptable RecipientStatus = "encryptable" // probe-encrypt succeeds
	RecipientInvalid     RecipientStatus = "invalid"     // present but not encryptable — local-sign can fix
	RecipientUnusable    RecipientStatus = "unusable"    // expired/revoked/disabled — local-sign cannot fix
	RecipientMissing     RecipientStatus = "missing"     // not in the keyring — needs import
)

type DoctorReport struct {
	SchemaVersion int           `json:"schema_version"`
	StoreRoot     string        `json:"store_root"`
	PassOK        bool          `json:"pass_ok"`
	GPGOK         bool          `json:"gpg_ok"`
	Clipboard     []string      `json:"clipboard"`
	Stores        []ScopeReport `json:"stores"`
	Warnings      []string      `json:"warnings,omitempty"`
}

// ScopeReport is the verdict for one recipient scope (a directory's governing
// .gpg-id). It is shared by `doctor` and `access`.
type ScopeReport struct {
	Label          string   `json:"label"`
	Scope          string   `json:"scope"`
	Path           string   `json:"path"`
	GPGIDPath      string   `json:"gpg_id_path"`
	Status         string   `json:"status"`
	Verdict        Verdict  `json:"verdict"`
	RecipientCount int      `json:"recipient_count"`
	Owned          []string `json:"owned,omitempty"`
	Encryptable    []string `json:"encryptable,omitempty"`
	Invalid        []string `json:"invalid,omitempty"`
	Unusable       []string `json:"unusable,omitempty"`
	Missing        []string `json:"missing,omitempty"`
	Fixable        string   `json:"fixable,omitempty"`
}

// AccessReport is the envelope for the `passage access` command.
type AccessReport struct {
	SchemaVersion int           `json:"schema_version"`
	StoreRoot     string        `json:"store_root"`
	Entry         string        `json:"entry,omitempty"`
	Scopes        []ScopeReport `json:"scopes"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type LocalKey struct {
	Fingerprint string `json:"fingerprint"`
	UID         string `json:"uid"`
	HasSecret   bool   `json:"has_secret"`
	OwnerTrust  string `json:"ownertrust"`
}

func New(storeRoot string) Checker {
	return Checker{GPGBinary: "gpg", StoreRoot: storeRoot}
}

func (c Checker) Doctor(ctx context.Context) DoctorReport {
	report := DoctorReport{
		SchemaVersion: 1,
		StoreRoot:     c.StoreRoot,
		PassOK:        commandExists("pass"),
		GPGOK:         commandExists(c.gpg()),
		Clipboard:     availableClipboardTools(),
	}
	if !report.PassOK {
		report.Warnings = append(report.Warnings, "pass is not available in PATH")
	}
	if !report.GPGOK {
		report.Warnings = append(report.Warnings, "gpg is not available in PATH")
		return report
	}
	stores, err := c.VerifyStores(ctx)
	if err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	} else {
		report.Stores = stores
		for _, store := range stores {
			if store.Verdict != VerdictWritable {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", store.Label, store.Status))
			}
		}
	}
	return report
}

// VerifyStores classifies every recipient scope in the store. Scopes are the
// directories that carry a .gpg-id at any depth (pass's real layout), not just
// the root and its immediate children.
func (c Checker) VerifyStores(ctx context.Context) ([]ScopeReport, error) {
	root := c.StoreRoot
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("password store root is empty")
	}
	scopes, err := passstore.ScopeDirs(root)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 {
		// No .gpg-id anywhere: report the root as uninitialized so doctor still
		// tells the user the store needs `pass init`.
		return []ScopeReport{{
			Label:     "default",
			Path:      root,
			GPGIDPath: filepath.Join(root, ".gpg-id"),
			Status:    "no .gpg-id",
			Verdict:   VerdictUninitialized,
		}}, nil
	}
	reports := make([]ScopeReport, 0, len(scopes))
	for _, rel := range scopes {
		dir := root
		if rel != "" {
			dir = filepath.Join(root, filepath.FromSlash(rel))
		}
		report, err := c.verifyScope(ctx, rel, dir)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	return reports, nil
}

// Access returns the verdict governing one entry, resolving the nearest
// ancestor .gpg-id exactly as pass would.
func (c Checker) Access(ctx context.Context, entry string) (ScopeReport, error) {
	root := c.StoreRoot
	if strings.TrimSpace(root) == "" {
		return ScopeReport{}, errors.New("password store root is empty")
	}
	entryDir := filepath.Dir(filepath.Join(root, filepath.FromSlash(entry)))
	gpgIDPath, ids, ok, err := passstore.ResolveRecipientsFile(root, entryDir)
	if err != nil {
		return ScopeReport{}, err
	}
	if !ok {
		return ScopeReport{
			Label:   "default",
			Scope:   "",
			Path:    entryDir,
			Status:  "no .gpg-id",
			Verdict: VerdictUninitialized,
		}, nil
	}
	report := c.reportForRecipients(ctx, ids)
	scopeDir := filepath.Dir(gpgIDPath)
	report.Scope = relScope(root, scopeDir)
	report.Label = labelFor(report.Scope)
	report.Path = scopeDir
	report.GPGIDPath = gpgIDPath
	return report, nil
}

// AccessAll returns a verdict for every scope in the store.
func (c Checker) AccessAll(ctx context.Context) (AccessReport, error) {
	scopes, err := c.VerifyStores(ctx)
	if err != nil {
		return AccessReport{}, err
	}
	report := AccessReport{SchemaVersion: 1, StoreRoot: c.StoreRoot, Scopes: scopes}
	for _, s := range scopes {
		if s.Verdict != VerdictWritable {
			report.Warnings = append(report.Warnings, fmt.Sprintf("%s: %s", s.Label, s.Status))
		}
	}
	return report, nil
}

func (c Checker) verifyScope(ctx context.Context, rel string, dir string) (ScopeReport, error) {
	gpgIDPath := filepath.Join(dir, ".gpg-id")
	data, err := os.ReadFile(gpgIDPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ScopeReport{
				Label:     labelFor(rel),
				Scope:     rel,
				Path:      dir,
				GPGIDPath: gpgIDPath,
				Status:    "no .gpg-id",
				Verdict:   VerdictUninitialized,
			}, nil
		}
		return ScopeReport{}, fmt.Errorf("read %s: %w", gpgIDPath, err)
	}
	report := c.reportForRecipients(ctx, passstore.ParseGPGID(data))
	report.Label = labelFor(rel)
	report.Scope = rel
	report.Path = dir
	report.GPGIDPath = gpgIDPath
	return report, nil
}

func (c Checker) reportForRecipients(ctx context.Context, ids []string) ScopeReport {
	report := ScopeReport{RecipientCount: len(ids)}
	if len(ids) == 0 {
		report.Status = "empty .gpg-id"
		report.Verdict = VerdictUninitialized
		return report
	}
	owned := false
	blocked := false
	for _, id := range ids {
		switch c.recipientStatus(ctx, id) {
		case RecipientOwned:
			report.Owned = append(report.Owned, id)
			owned = true
		case RecipientEncryptable:
			report.Encryptable = append(report.Encryptable, id)
		case RecipientInvalid:
			report.Invalid = append(report.Invalid, id)
			blocked = true
		case RecipientUnusable:
			report.Unusable = append(report.Unusable, id)
			blocked = true
		default:
			report.Missing = append(report.Missing, id)
			blocked = true
		}
	}
	switch {
	case !blocked:
		report.Verdict = VerdictWritable
		report.Status = "writable"
	case owned:
		report.Verdict = VerdictReadOnly
		report.Status = "read-only"
	default:
		report.Verdict = VerdictNoAccess
		report.Status = "no access"
	}
	report.Fixable = fixHint(report)
	return report
}

// recipientStatus is the load-bearing classifier. The encryptable test is a
// real probe-encrypt — the ground truth of whether `pass` can encrypt to this
// recipient — because gpg's gate is computed validity, not ownertrust: a
// locally-signed key encrypts with no ownertrust record, while an ownertrust=4
// key with no certification still fails.
func (c Checker) recipientStatus(ctx context.Context, id string) RecipientStatus {
	if c.hasSecret(ctx, id) {
		return RecipientOwned
	}
	out, err := c.runGPG(ctx, "--batch", "--with-colons", "--list-keys", id)
	if err != nil || strings.TrimSpace(out) == "" {
		return RecipientMissing
	}
	if c.canEncryptTo(ctx, id) {
		return RecipientEncryptable
	}
	if keyUnusable(out) {
		return RecipientUnusable
	}
	return RecipientInvalid
}

// canEncryptTo asks gpg to do exactly what `pass` will do — encrypt to the
// literal recipient selectors — with no secret and no tty. rc==0 means every
// listed recipient is a valid encryption target.
func (c Checker) canEncryptTo(ctx context.Context, ids ...string) bool {
	args := []string{"--batch", "--no-tty", "--yes", "--encrypt", "--output", os.DevNull}
	for _, id := range ids {
		args = append(args, "--recipient", id)
	}
	cmd := exec.CommandContext(ctx, c.gpg(), args...)
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// scopeWritable is the single authoritative probe for a whole scope: encrypt to
// all recipients at once. It mirrors pass byte-for-byte.
func (c Checker) scopeWritable(ctx context.Context, ids []string) bool {
	if len(ids) == 0 {
		return false
	}
	return c.canEncryptTo(ctx, ids...)
}

func (c Checker) LocalKeys(ctx context.Context) ([]LocalKey, error) {
	trust, err := c.ownerTrust(ctx)
	if err != nil {
		return nil, err
	}
	out, err := c.runGPG(ctx, "--with-colons", "--list-keys")
	if err != nil {
		return nil, err
	}
	var keys []LocalKey
	var current *LocalKey
	pubPending := false
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub":
			if current != nil && current.Fingerprint != "" {
				keys = append(keys, *current)
			}
			current = &LocalKey{}
			pubPending = true
		case "uid":
			if current != nil && current.UID == "" {
				current.UID = fields[9]
			}
		case "fpr":
			if current != nil && pubPending {
				current.Fingerprint = fields[9]
				current.OwnerTrust = OwnerTrustLabel(trust[current.Fingerprint])
				current.HasSecret = c.hasSecret(ctx, current.Fingerprint)
				pubPending = false
			}
		}
	}
	if current != nil && current.Fingerprint != "" {
		keys = append(keys, *current)
	}
	return keys, nil
}

// RecipientStatus classifies a single recipient selector. Exported for the
// trust engine so it shares one definition of "encryptable" with the verdict.
func (c Checker) RecipientStatus(ctx context.Context, id string) RecipientStatus {
	return c.recipientStatus(ctx, id)
}

// CanEncryptTo reports whether gpg can encrypt to every listed selector
// non-interactively (the authoritative writable probe).
func (c Checker) CanEncryptTo(ctx context.Context, ids ...string) bool {
	return c.canEncryptTo(ctx, ids...)
}

// ScopeWritable reports whether a whole recipient set is encryptable in one
// probe.
func (c Checker) ScopeWritable(ctx context.Context, ids []string) bool {
	return c.scopeWritable(ctx, ids)
}

// HasSecret reports whether a secret key is present for the selector.
func (c Checker) HasSecret(ctx context.Context, id string) bool {
	return c.hasSecret(ctx, id)
}

// OwnerTrust returns the fingerprint→ownertrust-level map (diagnostic only; it
// does not drive the writable verdict).
func (c Checker) OwnerTrust(ctx context.Context) (map[string]string, error) {
	return c.ownerTrust(ctx)
}

// ResolvePrimary returns the primary fingerprint and primary UID for a
// recipient selector, or ok=false when it is not in the keyring.
func (c Checker) ResolvePrimary(ctx context.Context, id string) (fingerprint string, uid string, ok bool) {
	out, err := c.runGPG(ctx, "--batch", "--with-colons", "--list-keys", id)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", "", false
	}
	pubPending := false
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 10 {
			continue
		}
		switch fields[0] {
		case "pub":
			pubPending = true
		case "fpr":
			if pubPending && fingerprint == "" {
				fingerprint = fields[9]
				pubPending = false
			}
		case "uid":
			if uid == "" {
				uid = fields[9]
			}
		}
	}
	return fingerprint, uid, fingerprint != ""
}

func (c Checker) hasSecret(ctx context.Context, id string) bool {
	_, err := c.runGPG(ctx, "--batch", "--quiet", "--list-secret-keys", id)
	return err == nil
}

func (c Checker) ownerTrust(ctx context.Context) (map[string]string, error) {
	out, err := c.runGPG(ctx, "--export-ownertrust")
	if err != nil {
		return nil, err
	}
	trust := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) >= 2 && parts[0] != "" {
			trust[parts[0]] = parts[1]
		}
	}
	return trust, nil
}

func (c Checker) runGPG(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.gpg(), args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gpg %s failed: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (c Checker) gpg() string {
	if strings.TrimSpace(c.GPGBinary) == "" {
		return "gpg"
	}
	return c.GPGBinary
}

// keyUnusable reports whether a key's computed validity is terminal — expired,
// revoked, invalid, or disabled — so the caller can say "local-sign won't help"
// instead of offering a fix that can't work.
func keyUnusable(colons string) bool {
	for _, line := range strings.Split(colons, "\n") {
		if !strings.HasPrefix(line, "pub:") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 2 || fields[1] == "" {
			return false
		}
		switch fields[1][0] {
		case 'e', 'r', 'i', 'd':
			return true
		default:
			return false
		}
	}
	return false
}

func fixHint(report ScopeReport) string {
	if report.Verdict == VerdictWritable || report.Verdict == VerdictUninitialized {
		return ""
	}
	if len(report.Missing) > 0 {
		return "import"
	}
	if len(report.Invalid) > 0 {
		return "trust"
	}
	if len(report.Unusable) > 0 {
		return "unfixable"
	}
	return ""
}

func labelFor(rel string) string {
	if rel == "" {
		return "default"
	}
	return rel
}

func relScope(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func OwnerTrustLabel(level string) string {
	switch level {
	case "5":
		return "ultimate"
	case "4":
		return "full"
	case "3":
		return "marginal"
	case "2":
		return "never"
	case "1":
		return "unknown"
	default:
		return "unset"
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func availableClipboardTools() []string {
	var tools []string
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		if commandExists(name) {
			tools = append(tools, name)
		}
	}
	return tools
}
