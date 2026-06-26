package passstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xbenc/passage/internal/fuzzy"
	"github.com/0xbenc/passage/internal/procutil"
	"github.com/0xbenc/passage/internal/state"
)

const DefaultStoreName = ".password-store"

type Entry struct {
	Path      string `json:"path"`
	Display   string `json:"display"`
	Pinned    bool   `json:"pinned"`
	LastUsed  int64  `json:"last_used"`
	HasMFA    bool   `json:"has_mfa"`
	MFATarget string `json:"mfa_target,omitempty"`
}

type Store struct {
	Root       string
	PassBinary string
	Stdin      io.Reader
	Stderr     io.Writer
}

func ResolveStoreDir(env []string) (string, error) {
	values := envMap(env)
	if dir := strings.TrimSpace(values["PASSWORD_STORE_DIR"]); dir != "" {
		return expandHome(filepath.Clean(dir))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	return filepath.Join(home, DefaultStoreName), nil
}

func ResolvePassBinary(env []string) string {
	values := envMap(env)
	if binary := strings.TrimSpace(values["PASSAGE_PASS_BINARY"]); binary != "" {
		return binary
	}
	return "pass"
}

func New(root string, passBinary string) Store {
	return Store{Root: filepath.Clean(root), PassBinary: defaultString(passBinary, "pass")}
}

func (s Store) Discover() ([]string, error) {
	if s.Root == "" {
		return nil, errors.New("password store root is empty")
	}
	info, err := os.Stat(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("password store directory %s does not exist", s.Root)
		}
		return nil, fmt.Errorf("stat password store %s: %w", s.Root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("password store path %s is not a directory", s.Root)
	}

	var entries []string
	err = filepath.WalkDir(s.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == ".gpg" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(name) != ".gpg" {
			return nil
		}
		rel, err := filepath.Rel(s.Root, path)
		if err != nil {
			return err
		}
		rel = strings.TrimSuffix(filepath.ToSlash(rel), ".gpg")
		if rel != "" {
			entries = append(entries, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk password store %s: %w", s.Root, err)
	}
	sort.Strings(entries)
	return compactSorted(entries), nil
}

// ParseGPGID splits a .gpg-id file into recipient tokens, one per non-blank
// line. Recipients may be full fingerprints, key-ids, or email selectors —
// exactly the forms `pass` itself feeds to `gpg -r`.
func ParseGPGID(data []byte) []string {
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			ids = append(ids, line)
		}
	}
	return ids
}

// ResolveRecipientsFile finds the .gpg-id that governs an entry, mirroring
// pass's rule exactly: walk up from startDir to the store root and use the
// first .gpg-id found. startDir and root are absolute paths; startDir must be
// at or below root. It returns the .gpg-id path, its parsed recipients, and ok
// = false when no .gpg-id exists anywhere up to the root (an uninitialized
// scope).
func ResolveRecipientsFile(root, startDir string) (gpgIDPath string, ids []string, ok bool, err error) {
	root = filepath.Clean(root)
	dir := filepath.Clean(startDir)
	for {
		candidate := filepath.Join(dir, ".gpg-id")
		data, readErr := os.ReadFile(candidate)
		if readErr == nil {
			return candidate, ParseGPGID(data), true, nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return "", nil, false, fmt.Errorf("read %s: %w", candidate, readErr)
		}
		if dir == root {
			return "", nil, false, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || len(parent) < len(root) {
			return "", nil, false, nil
		}
		dir = parent
	}
}

// ScopeDirs enumerates every directory in the store that carries its own
// .gpg-id, at any depth, as paths relative to root with the root itself
// represented by "". The result is the set of distinct recipient scopes a
// caller must verify — replacing any assumption that scopes live only one
// level below the root.
func ScopeDirs(root string) ([]string, error) {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("password store directory %s does not exist", root)
		}
		return nil, fmt.Errorf("stat password store %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("password store path %s is not a directory", root)
	}
	var scopes []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != root && (name == ".git" || name == ".gpg") {
			return filepath.SkipDir
		}
		if _, statErr := os.Stat(filepath.Join(path, ".gpg-id")); statErr == nil {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if rel == "." {
				rel = ""
			} else {
				rel = filepath.ToSlash(rel)
			}
			scopes = append(scopes, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk password store %s: %w", root, err)
	}
	sort.Strings(scopes)
	return scopes, nil
}

func BuildEntries(paths []string, st state.State) []Entry {
	known := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		known[path] = struct{}{}
	}

	entries := make([]Entry, 0, len(paths))
	for _, path := range paths {
		record := st.Record(path)
		target, hasMFA := MFATarget(path, known)
		entries = append(entries, Entry{
			Path:      path,
			Display:   Display(path),
			Pinned:    record.Pinned,
			LastUsed:  record.LastUsed,
			HasMFA:    hasMFA,
			MFATarget: target,
		})
	}
	SortEntries(entries)
	return entries
}

func SortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if a.LastUsed != b.LastUsed {
			return a.LastUsed > b.LastUsed
		}
		return a.Path < b.Path
	})
}

func FilterEntries(entries []Entry, filter string, mfaOnly bool) []Entry {
	filter = strings.ToLower(strings.TrimSpace(filter))
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if mfaOnly && !entry.HasMFA {
			continue
		}
		if filter != "" {
			path := strings.ToLower(entry.Path)
			display := strings.ToLower(entry.Display)
			if !strings.Contains(path, filter) && !strings.Contains(display, filter) {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

// Ranked is one entry surviving a fuzzy filter: its index into the input
// slice, the match score, and the matched rune positions within the entry's
// Display string (nil when the entry matched only via its Path). It is the
// single ranking primitive shared by the interactive picker and the
// single-match auto-run path, so the two can never disagree about which
// entries a filter selects — important for a password manager, where a
// divergence could auto-act on an entry the picker would not have surfaced.
type Ranked struct {
	Index     int
	Score     int
	Positions []int
}

// Rank filters entries by a fuzzy query and orders the survivors. An empty
// filter keeps every (mfa-eligible) entry in the input order, preserving the
// store's pin/recency ordering. A non-empty filter keeps only fuzzy matches,
// ordered by: pinned first, then score, then recency, then original order.
// Positions index into Display so a caller can highlight the matched runes of
// the visible title.
func Rank(entries []Entry, filter string, mfaOnly bool) []Ranked {
	filter = strings.TrimSpace(filter)
	out := make([]Ranked, 0, len(entries))
	for i, entry := range entries {
		if mfaOnly && !entry.HasMFA {
			continue
		}
		if filter == "" {
			out = append(out, Ranked{Index: i})
			continue
		}
		// Gate on relevance, not just subsequence membership, so a query does
		// not surface entries whose letters merely appear scattered across the
		// path with large gaps.
		qlen := len([]rune(filter))
		dispRes, dispOK := fuzzy.Match(filter, entry.Display)
		dispOK = dispOK && fuzzy.Relevant(dispRes, qlen)
		pathRes, pathOK := fuzzy.Match(filter, entry.Path)
		pathOK = pathOK && fuzzy.Relevant(pathRes, qlen)
		if !dispOK && !pathOK {
			continue
		}
		ranked := Ranked{Index: i}
		matched := false
		if dispOK {
			matched = true
			ranked.Score = dispRes.Score
			ranked.Positions = dispRes.Positions
		}
		if pathOK && (!matched || pathRes.Score > ranked.Score) {
			ranked.Score = pathRes.Score
			// Highlight only the visible Display; if the entry matched
			// solely through its raw Path there are no display positions.
			if !dispOK {
				ranked.Positions = nil
			}
		}
		out = append(out, ranked)
	}
	if filter != "" {
		sort.SliceStable(out, func(a, b int) bool {
			ea, eb := entries[out[a].Index], entries[out[b].Index]
			if ea.Pinned != eb.Pinned {
				return ea.Pinned
			}
			if out[a].Score != out[b].Score {
				return out[a].Score > out[b].Score
			}
			if ea.LastUsed != eb.LastUsed {
				return ea.LastUsed > eb.LastUsed
			}
			return out[a].Index < out[b].Index
		})
	}
	return out
}

func MFATarget(path string, known map[string]struct{}) (string, bool) {
	if path == "mfa" || strings.HasSuffix(path, "/mfa") {
		return path, true
	}
	sibling := path + "/mfa"
	if _, ok := known[sibling]; ok {
		return sibling, true
	}
	return "", false
}

func Display(path string) string {
	return strings.ReplaceAll(path, "/", " | ")
}

func FirstLine(content []byte) []byte {
	if idx := bytes.IndexByte(content, '\n'); idx >= 0 {
		return append([]byte(nil), content[:idx]...)
	}
	return append([]byte(nil), content...)
}

func (s Store) Show(ctx context.Context, entry string) ([]byte, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, errors.New("entry is empty")
	}
	cmd := exec.CommandContext(ctx, s.PassBinary, "show", "--", entry)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = s.writeEnv()
	cmd.Stdin = s.Stdin
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if s.Stderr != nil {
		cmd.Stderr = io.MultiWriter(s.Stderr, &stderr)
	}
	err := cmd.Run()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("pass show %s canceled: %w", entry, ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("pass show %s failed: %s", entry, msg)
		}
		return nil, fmt.Errorf("pass show %s failed: %w", entry, err)
	}
	return stdout.Bytes(), nil
}

// InsertOptions / GenerateOptions / RemoveOptions tune the write verbs.
type GenerateOptions struct {
	NoSymbols bool
	Length    int
}

type RemoveOptions struct {
	Recursive bool
}

// writeEnv is the env every store-mutating pass invocation needs: the store dir
// plus GPG_TTY so pinentry (encryption / commit signing) can prompt.
func (s Store) writeEnv() []string {
	return withEnv(os.Environ(), append([]string{"PASSWORD_STORE_DIR=" + s.Root}, gpgTTYEnv()...)...)
}

// Insert writes content as the entry, overwriting any existing one (the caller
// gates overwrites). It always uses `pass insert -m`, reading the full content
// from stdin until EOF, which avoids the interactive retype prompt and stores
// the first line as the password exactly like `pass show` reads it.
func (s Store) Insert(ctx context.Context, entry string, content []byte) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("entry is empty")
	}
	body := content
	if len(body) == 0 || body[len(body)-1] != '\n' {
		body = append(append([]byte(nil), body...), '\n')
	}
	cmd := exec.CommandContext(ctx, s.PassBinary, "insert", "--multiline", "--force", "--", entry)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = s.writeEnv()
	cmd.Stdin = bytes.NewReader(body)
	return s.runWrite(ctx, cmd, "insert "+entry)
}

// Generate creates a random password for the entry and returns it. The value is
// parsed from `pass generate` stdout (ANSI-stripped last non-empty line) rather
// than a second decrypt, so generation never triggers a pinentry prompt.
func (s Store) Generate(ctx context.Context, entry string, opts GenerateOptions) ([]byte, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil, errors.New("entry is empty")
	}
	args := []string{"generate", "--force"}
	if opts.NoSymbols {
		args = append(args, "--no-symbols")
	}
	args = append(args, "--", entry)
	if opts.Length > 0 {
		args = append(args, strconv.Itoa(opts.Length))
	}
	cmd := exec.CommandContext(ctx, s.PassBinary, args...)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = s.writeEnv()
	cmd.Stdin = bytes.NewReader(nil)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if s.Stderr != nil {
		cmd.Stderr = io.MultiWriter(s.Stderr, &stderr)
	}
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("pass generate %s canceled: %w", entry, ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("pass generate %s failed: %s", entry, msg)
		}
		return nil, fmt.Errorf("pass generate %s failed: %w", entry, err)
	}
	password := lastNonEmptyLine(stripANSI(stdout.Bytes()))
	if len(password) == 0 {
		return nil, fmt.Errorf("pass generate %s produced no password", entry)
	}
	return password, nil
}

// Remove deletes an entry (or subtree with Recursive).
func (s Store) Remove(ctx context.Context, entry string, opts RemoveOptions) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("entry is empty")
	}
	args := []string{"rm", "--force"}
	if opts.Recursive {
		args = append(args, "--recursive")
	}
	args = append(args, "--", entry)
	cmd := exec.CommandContext(ctx, s.PassBinary, args...)
	procutil.ConfigureCommandCancellation(cmd)
	cmd.Env = s.writeEnv()
	return s.runWrite(ctx, cmd, "rm "+entry)
}

// Edit opens `pass edit` ($EDITOR) on the entry. It inherits the real
// controlling terminal and is deliberately NOT placed in its own process group
// (no ConfigureCommandCancellation): the editor must own the foreground tty.
// Run it only with the TUI torn down (the Pattern-A gap) or from the CLI.
func (s Store) Edit(ctx context.Context, entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return errors.New("entry is empty")
	}
	cmd := exec.CommandContext(ctx, s.PassBinary, "edit", "--", entry)
	cmd.Env = s.writeEnv()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pass edit %s failed: %w", entry, err)
	}
	return nil
}

func (s Store) runWrite(ctx context.Context, cmd *exec.Cmd, label string) error {
	var stderr bytes.Buffer
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if s.Stderr != nil {
		cmd.Stderr = io.MultiWriter(s.Stderr, &stderr)
	}
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("pass %s canceled: %w", label, ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("pass %s failed: %s", label, msg)
		}
		return fmt.Errorf("pass %s failed: %w", label, err)
	}
	return nil
}

func stripANSI(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '[' {
			i += 2
			for i < len(b) && !((b[i] >= 'A' && b[i] <= 'Z') || (b[i] >= 'a' && b[i] <= 'z')) {
				i++
			}
			continue
		}
		out = append(out, b[i])
	}
	return out
}

func lastNonEmptyLine(b []byte) []byte {
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if line := bytes.TrimSpace(lines[i]); len(line) > 0 {
			return append([]byte(nil), line...)
		}
	}
	return nil
}

func TouchNow() int64 {
	return time.Now().Unix()
}

func SetupGPGTTY(ctx context.Context, env []string) {
	if runtime.GOOS == "windows" {
		return
	}
	tty := strings.TrimSpace(os.Getenv("GPG_TTY"))
	if tty == "" {
		if out, err := exec.CommandContext(ctx, "tty").Output(); err == nil {
			tty = strings.TrimSpace(string(out))
			if tty != "" && tty != "not a tty" {
				_ = os.Setenv("GPG_TTY", tty)
			}
		}
	}
	if _, err := exec.LookPath("gpg-connect-agent"); err == nil {
		cmd := exec.CommandContext(ctx, "gpg-connect-agent", "updatestartuptty", "/bye")
		cmd.Env = append(os.Environ(), env...)
		_ = cmd.Run()
	}
}

func gpgTTYEnv() []string {
	if tty := strings.TrimSpace(os.Getenv("GPG_TTY")); tty != "" {
		return []string{"GPG_TTY=" + tty}
	}
	return nil
}

func withEnv(env []string, values ...string) []string {
	out := append([]string(nil), env...)
	for _, value := range values {
		key, _, ok := strings.Cut(value, "=")
		if !ok {
			continue
		}
		replaced := false
		prefix := key + "="
		for i, existing := range out {
			if strings.HasPrefix(existing, prefix) {
				out[i] = value
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, value)
		}
	}
	return out
}

func compactSorted(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func envMap(env []string) map[string]string {
	if env == nil {
		env = os.Environ()
	}
	out := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			out[key] = value
		}
	}
	return out
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
