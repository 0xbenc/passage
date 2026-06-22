package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/0xbenc/passage/internal/clipboard"
	"github.com/0xbenc/passage/internal/fsutil"
	"github.com/0xbenc/passage/internal/gpgdiag"
	"github.com/0xbenc/passage/internal/passstore"
	"github.com/0xbenc/passage/internal/state"
	"github.com/0xbenc/passage/internal/termstyle"
	"github.com/0xbenc/passage/internal/totp"
	"github.com/0xbenc/passage/internal/ui"
)

const usage = `Usage:
  passage [filter]
  passage mfa [filter]
  passage [command] [flags]

Commands:
  list             List GNU Pass entries
  show             Show one entry's metadata
  copy             Copy an entry's password (first line)
  reveal           Reveal an entry's password until cleared
  totp             Copy/show a TOTP code for an entry
  pin              Pin an entry
  unpin            Unpin an entry
  clear-recents    Clear MRU timestamps
  clear-pins       Clear all pins
  clear-clipboard  Clear the clipboard
  doctor           Check pass, gpg, clipboard, and .gpg-id health
  keys             List local GPG public keys
  version          Print build version information
  help             Show help

Run "passage help COMMAND" for command-specific usage.
`

const listUsage = `Usage:
  passage list [--json] [--filter TEXT] [--mfa] [--store-dir PATH] [--state-dir PATH]
`

const showUsage = `Usage:
  passage show ENTRY [--json] [--store-dir PATH] [--state-dir PATH]
`

const copyUsage = `Usage:
  passage copy ENTRY [--private] [--store-dir PATH] [--state-dir PATH]
`

const revealUsage = `Usage:
  passage reveal ENTRY [--private] [--store-dir PATH] [--state-dir PATH]
`

const totpUsage = `Usage:
  passage totp ENTRY [--json] [--no-copy] [--private] [--wait] [--at UNIX] [--store-dir PATH] [--state-dir PATH]
`

const doctorUsage = `Usage:
  passage doctor [--json] [--store-dir PATH]
`

const keysUsage = `Usage:
  passage keys [--json] [--store-dir PATH]
`

const versionUsage = `Usage:
  passage version
`

const interactiveActionTimeout = 12 * time.Second

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func (b BuildInfo) normalized() BuildInfo {
	return BuildInfo{
		Version: defaultString(b.Version, "dev"),
		Commit:  defaultString(b.Commit, "none"),
		Date:    defaultString(b.Date, "unknown"),
	}
}

func Run(args []string, stdout io.Writer, stderr io.Writer, build BuildInfo) int {
	stdout = writerOrDiscard(stdout)
	stderr = writerOrDiscard(stderr)
	r := runner{
		stdout: stdout,
		stderr: stderr,
		env:    os.Environ(),
		build:  build.normalized(),
	}
	return r.run(args)
}

type runner struct {
	stdout io.Writer
	stderr io.Writer
	env    []string
	build  BuildInfo
}

type commonFlags struct {
	storeDir    string
	stateDir    string
	json        bool
	noColor     bool
	noAltScreen bool
	themeFile   string
}

type runtimeState struct {
	storeDir  string
	stateDir  string
	statePath string
	state     state.State
	store     passstore.Store
	entries   []passstore.Entry
}

func (r runner) run(args []string) int {
	if len(args) == 0 {
		return r.runInteractive(nil, false)
	}
	if hasHelpFlag(args) {
		if len(args) == 1 {
			fmt.Fprint(r.stdout, usage)
			return 0
		}
	}
	switch args[0] {
	case "help", "--help", "-h":
		return r.runHelp(args[1:])
	case "version", "--version", "-v":
		return r.runVersion()
	case "mfa", "MFA":
		return r.runInteractive(args[1:], true)
	case "list":
		return r.runList(args[1:])
	case "show":
		return r.runShow(args[1:])
	case "copy":
		return r.runCopy(args[1:])
	case "reveal":
		return r.runReveal(args[1:])
	case "totp":
		return r.runTOTP(args[1:])
	case "pin":
		return r.runPin(args[1:], true)
	case "unpin":
		return r.runPin(args[1:], false)
	case "clear-recents":
		return r.runClear(args[1:], "recents")
	case "clear-pins":
		return r.runClear(args[1:], "pins")
	case "clear-clipboard":
		return r.runClearClipboard(args[1:])
	case "doctor":
		return r.runDoctor(args[1:])
	case "keys":
		return r.runKeys(args[1:])
	default:
		return r.runInteractive(args, false)
	}
}

func (r runner) runHelp(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(r.stdout, usage)
		return 0
	}
	if len(args) > 1 {
		fmt.Fprintln(r.stderr, "passage: help accepts at most one topic")
		return 1
	}
	switch args[0] {
	case "list":
		fmt.Fprint(r.stdout, listUsage)
	case "show":
		fmt.Fprint(r.stdout, showUsage)
	case "copy":
		fmt.Fprint(r.stdout, copyUsage)
	case "reveal":
		fmt.Fprint(r.stdout, revealUsage)
	case "totp", "mfa":
		fmt.Fprint(r.stdout, totpUsage)
	case "doctor":
		fmt.Fprint(r.stdout, doctorUsage)
	case "keys":
		fmt.Fprint(r.stdout, keysUsage)
	case "version":
		fmt.Fprint(r.stdout, versionUsage)
	default:
		fmt.Fprintf(r.stderr, "passage: unknown help topic %q\n", args[0])
		return 1
	}
	return 0
}

func (r runner) runVersion() int {
	fmt.Fprintf(r.stdout, "passage %s\ncommit: %s\nbuilt: %s\n", r.build.Version, r.build.Commit, r.build.Date)
	return 0
}

func (r runner) runInteractive(args []string, mfaOnly bool) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, usage)
		return 0
	}
	filter := strings.TrimSpace(strings.Join(rest, " "))
	ctx := context.Background()
	passstore.SetupGPGTTY(ctx, r.env)
	rt, err := r.loadInteractive(flags)
	if err != nil {
		if r.firstRunGuidance(flags) {
			return 1
		}
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	// When a filter narrows the store to exactly one entry, skip the picker and
	// run the default action directly (copy, or TOTP for MFA), matching what
	// pressing enter on that lone entry would do.
	if filter != "" {
		// Use the same fuzzy Rank the picker uses, so "exactly one match"
		// means the same thing here as on screen — the auto-run can only
		// fire on an entry the picker would have shown alone.
		if matches := passstore.Rank(rt.entries, filter, mfaOnly); len(matches) == 1 {
			rt.store.Stdin = os.Stdin
			rt.store.Stderr = r.stderr
			return r.runAutoAction(ctx, rt, rt.entries[matches[0].Index], mfaOnly, flags)
		}
	}
	themePath, themeConfig, themeWarning, err := r.loadThemeEditorConfig(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	theme, warning := r.resolveInteractiveTheme(flags)
	if warning != "" {
		if themeWarning != "" {
			themeWarning += "; "
		}
		themeWarning += warning
	}
	_, err = ui.Pick(ctx, rt.entries, ui.PickOptions{
		Output:      r.stderr,
		NoColor:     flags.noColor,
		Theme:       theme,
		ThemeFile:   flags.themeFile,
		Title:       "passage",
		Version:     r.build.Version,
		StoreRoot:   rt.storeDir,
		Filter:      filter,
		MFAOnly:     mfaOnly,
		NoAltScreen: flags.noAltScreen,
		Glyphs:      termstyle.ResolveGlyphs(r.env),
		ClearClipboard: func(clearCtx context.Context) error {
			_, err := clipboard.Clear(clearCtx)
			return err
		},
		ThemeConfig:  themeConfig,
		ThemePath:    themePath,
		ThemeWarning: themeWarning,
		SaveTheme: func(saveCtx context.Context, result ui.ThemeEditorResult) (ui.ThemeSaveResult, error) {
			return r.saveThemeConfig(saveCtx, flags, result)
		},
		RunAction: func(actionCtx context.Context, req ui.ActionRequest) ui.ActionOutcome {
			return r.runInteractiveAction(actionCtx, &rt, flags, req)
		},
	})
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	return 0
}

// firstRunGuidance turns the most common first-run failure — no password store
// yet — into guided onboarding with an environment check, instead of a terse
// "directory does not exist". It returns true when it handled the case.
func (r runner) firstRunGuidance(flags commonFlags) bool {
	storeDir, err := r.storeDir(flags)
	if err != nil {
		return false
	}
	if info, statErr := os.Stat(storeDir); statErr == nil && info.IsDir() {
		return false // the store exists; the error is something else
	}
	fmt.Fprintf(r.stderr, "No password store found at %s\n\n", storeDir)
	fmt.Fprintln(r.stderr, "passage reads your real GNU Pass store. To get started:")
	fmt.Fprintln(r.stderr, "")
	fmt.Fprintln(r.stderr, "  1. Install pass and gpg (e.g. brew install pass, apt install pass)")
	fmt.Fprintln(r.stderr, "  2. Create a GPG key:      gpg --full-generate-key")
	fmt.Fprintln(r.stderr, "  3. Initialize the store:  pass init <your-gpg-id>")
	fmt.Fprintln(r.stderr, "  4. Add an entry:          pass insert work/github")
	fmt.Fprintln(r.stderr, "")
	fmt.Fprintln(r.stderr, "Environment check:")
	report := gpgdiag.New(storeDir).Doctor(context.Background())
	for _, line := range doctorLines(report) {
		fmt.Fprintln(r.stderr, "  "+line)
	}
	return true
}

// runAutoAction performs the default action for a single filtered entry without
// opening the picker. It mirrors the picker's primary action: TOTP for MFA-only
// browsing or an MFA secret entry, otherwise copy.
func (r runner) runAutoAction(ctx context.Context, rt runtimeState, entry passstore.Entry, mfaOnly bool, flags commonFlags) int {
	if mfaOnly || isMFASecretEntry(entry) {
		code, msg, err := r.generateTOTP(ctx, rt, entry.Path, totpOptions{copy: true})
		if err != nil {
			fmt.Fprintf(r.stderr, "passage: %v\n", err)
			return 1
		}
		fmt.Fprintln(r.stdout, code.Value)
		if msg != "" {
			fmt.Fprintln(r.stderr, msg)
		}
		return 0
	}
	msg, _, err := r.copyEntry(ctx, rt, entry.Path, false)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	fmt.Fprintln(r.stderr, msg)
	return 0
}

func isMFASecretEntry(entry passstore.Entry) bool {
	return entry.Path == "mfa" || strings.HasSuffix(entry.Path, "/mfa")
}

func (r runner) loadThemeEditorConfig(flags commonFlags) (string, termstyle.ThemeConfig, string, error) {
	path, err := termstyle.ThemeConfigPath(flags.themeFile, r.env)
	if err != nil {
		return "", termstyle.ThemeConfig{}, "", err
	}
	cfg, warning, err := loadThemeConfig(path)
	if err != nil {
		return "", termstyle.ThemeConfig{}, "", err
	}
	return path, cfg, warning, nil
}

func (r runner) resolveInteractiveTheme(flags commonFlags) (termstyle.Theme, string) {
	theme, err := termstyle.ResolveTheme(termstyle.ThemeOptions{
		File:    flags.themeFile,
		NoColor: flags.noColor,
		Env:     r.env,
	})
	if err == nil {
		return theme, ""
	}
	return termstyle.TerminalTheme().WithNoColor(flags.noColor), "active theme did not parse; editor will replace it on save"
}

func (r runner) saveThemeConfig(ctx context.Context, flags commonFlags, result ui.ThemeEditorResult) (ui.ThemeSaveResult, error) {
	if err := ctx.Err(); err != nil {
		return ui.ThemeSaveResult{}, err
	}
	path := result.Path
	if strings.TrimSpace(path) == "" {
		resolved, err := termstyle.ThemeConfigPath(flags.themeFile, r.env)
		if err != nil {
			return ui.ThemeSaveResult{}, err
		}
		path = resolved
	}
	writeResult, err := fsutil.AtomicWriteFile(path, formatThemeConfig(result.Config), fsutil.WriteOptions{
		Backup:       true,
		BackupPrefix: "passage-theme-backup",
		Mode:         0o600,
	})
	if err != nil {
		return ui.ThemeSaveResult{}, err
	}
	theme, err := termstyle.ResolveTheme(termstyle.ThemeOptions{
		File:    path,
		NoColor: flags.noColor,
		Env:     r.env,
	})
	if err != nil {
		return ui.ThemeSaveResult{}, err
	}
	message := "Theme unchanged."
	if writeResult.Changed {
		message = "Theme saved to " + writeResult.Path + "."
		if writeResult.BackupPath != "" {
			message += " Backup: " + writeResult.BackupPath + "."
		}
	}
	return ui.ThemeSaveResult{
		Config:     result.Config,
		Theme:      theme,
		Path:       path,
		Changed:    writeResult.Changed,
		BackupPath: writeResult.BackupPath,
		Message:    message,
	}, nil
}

func loadThemeConfig(path string) (termstyle.ThemeConfig, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return termstyle.ThemeConfig{}, "", nil
		}
		return termstyle.ThemeConfig{}, "", fmt.Errorf("read theme config %s: %w", path, err)
	}
	cfg, err := termstyle.ParseThemeConfig(data)
	if err != nil {
		return termstyle.ThemeConfig{}, "existing theme config did not parse; saving will replace it", nil
	}
	if len(cfg.Warnings) > 0 {
		return cfg, "ignored: " + strings.Join(cfg.Warnings, "; "), nil
	}
	return cfg, "", nil
}

func formatThemeConfig(cfg termstyle.ThemeConfig) []byte {
	var b strings.Builder
	b.WriteString("# passage theme config\n")
	b.WriteString("# Edit with `passage theme` or from the homepage with Ctrl-O.\n\n")
	// Persist the chosen base palette so `theme = vivid` survives a round-trip.
	// Terminal is the implicit default, so it is left out to keep configs lean.
	if base := strings.TrimSpace(cfg.BaseName); base != "" {
		if t, ok := termstyle.BuiltinTheme(base); ok && t.Name != "terminal" {
			b.WriteString("theme = ")
			b.WriteString(t.Name)
			b.WriteString("\n\n")
		}
	}
	for _, role := range termstyle.Roles() {
		spec := strings.TrimSpace(cfg.Specs[role])
		if spec == "" {
			continue
		}
		b.WriteString(string(role))
		b.WriteString(" = ")
		b.WriteString(spec)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func (r runner) runList(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	filter, _ := consumeStringFlag(&rest, "--filter")
	mfaOnly := consumeBoolFlag(&rest, "--mfa")
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, listUsage)
		return 0
	}
	if len(rest) > 0 {
		fmt.Fprintf(r.stderr, "passage: unexpected arguments: %s\n", strings.Join(rest, " "))
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	entries := passstore.FilterEntries(rt.entries, filter, mfaOnly)
	if flags.json {
		return writeJSON(r.stdout, listResponse{SchemaVersion: 1, StoreRoot: rt.storeDir, Entries: entries})
	}
	for _, entry := range entries {
		prefix := "  "
		if entry.Pinned {
			prefix = "* "
		}
		mfa := ""
		if entry.HasMFA {
			mfa = " [mfa]"
		}
		fmt.Fprintf(r.stdout, "%s%s%s\n", prefix, entry.Path, mfa)
	}
	return 0
}

func (r runner) runShow(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, showUsage)
		return 0
	}
	if len(rest) != 1 {
		fmt.Fprint(r.stderr, showUsage)
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	entry, ok := findEntry(rt.entries, rest[0])
	if !ok {
		fmt.Fprintf(r.stderr, "passage: entry %q not found\n", rest[0])
		return 2
	}
	if flags.json {
		return writeJSON(r.stdout, showResponse{SchemaVersion: 1, StoreRoot: rt.storeDir, Entry: entry})
	}
	fmt.Fprintf(r.stdout, "path: %s\npinned: %v\nlast_used: %d\nhas_mfa: %v\n", entry.Path, entry.Pinned, entry.LastUsed, entry.HasMFA)
	if entry.MFATarget != "" {
		fmt.Fprintf(r.stdout, "mfa_target: %s\n", entry.MFATarget)
	}
	return 0
}

func (r runner) runCopy(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	private := consumeBoolFlag(&rest, "--private")
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, copyUsage)
		return 0
	}
	if len(rest) != 1 {
		fmt.Fprint(r.stderr, copyUsage)
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	msg, _, err := r.copyEntry(context.Background(), rt, rest[0], private)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	fmt.Fprintln(r.stderr, msg)
	return 0
}

func (r runner) runReveal(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	private := consumeBoolFlag(&rest, "--private")
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, revealUsage)
		return 0
	}
	if len(rest) != 1 {
		fmt.Fprint(r.stderr, revealUsage)
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	msg, err := r.revealEntry(context.Background(), rt, rest[0], private, flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	fmt.Fprintln(r.stderr, msg)
	return 0
}

func (r runner) runTOTP(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	opts := totpOptions{
		copy:    !consumeBoolFlag(&rest, "--no-copy"),
		private: consumeBoolFlag(&rest, "--private"),
		wait:    consumeBoolFlag(&rest, "--wait"),
	}
	atString, ok := consumeStringFlag(&rest, "--at")
	if ok {
		at, err := strconv.ParseInt(atString, 10, 64)
		if err != nil {
			fmt.Fprintf(r.stderr, "passage: invalid --at value %q\n", atString)
			return 1
		}
		opts.at = time.Unix(at, 0)
	}
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, totpUsage)
		return 0
	}
	if len(rest) != 1 {
		fmt.Fprint(r.stderr, totpUsage)
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	code, msg, err := r.generateTOTP(context.Background(), rt, rest[0], opts)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if flags.json {
		return writeJSON(r.stdout, totpResponse{SchemaVersion: 1, Entry: rest[0], Code: code})
	}
	fmt.Fprintln(r.stdout, code.Value)
	if msg != "" {
		fmt.Fprintln(r.stderr, msg)
	}
	return 0
}

func (r runner) runPin(args []string, pinned bool) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if len(rest) != 1 {
		fmt.Fprintf(r.stderr, "Usage:\n  passage %s ENTRY [--state-dir PATH]\n", map[bool]string{true: "pin", false: "unpin"}[pinned])
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if _, ok := findEntry(rt.entries, rest[0]); !ok {
		fmt.Fprintf(r.stderr, "passage: entry %q not found\n", rest[0])
		return 2
	}
	rt.state.SetPinned(rest[0], pinned)
	if err := rt.state.Save(rt.statePath); err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if pinned {
		fmt.Fprintf(r.stderr, "Pinned %s.\n", rest[0])
	} else {
		fmt.Fprintf(r.stderr, "Unpinned %s.\n", rest[0])
	}
	return 0
}

func (r runner) runClear(args []string, what string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if len(rest) > 0 {
		fmt.Fprintf(r.stderr, "passage: unexpected arguments: %s\n", strings.Join(rest, " "))
		return 1
	}
	rt, err := r.load(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	switch what {
	case "pins":
		rt.state.ClearPins()
	case "recents":
		rt.state.ClearRecents()
	}
	if err := rt.state.Save(rt.statePath); err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	fmt.Fprintf(r.stderr, "Cleared %s.\n", what)
	return 0
}

func (r runner) runClearClipboard(args []string) int {
	if hasHelpFlag(args) {
		fmt.Fprintln(r.stdout, "Usage:\n  passage clear-clipboard")
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintf(r.stderr, "passage: unexpected arguments: %s\n", strings.Join(args, " "))
		return 1
	}
	res, err := clipboard.Clear(context.Background())
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	fmt.Fprintf(r.stderr, "Clipboard cleared (%s).\n", res.Tool)
	return 0
}

func (r runner) runDoctor(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, doctorUsage)
		return 0
	}
	if len(rest) > 0 {
		fmt.Fprintf(r.stderr, "passage: unexpected arguments: %s\n", strings.Join(rest, " "))
		return 1
	}
	storeDir, err := r.storeDir(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	report := gpgdiag.New(storeDir).Doctor(context.Background())
	if flags.json {
		return writeJSON(r.stdout, report)
	}
	for _, line := range doctorLines(report) {
		fmt.Fprintln(r.stdout, line)
	}
	if len(report.Warnings) > 0 {
		return 2
	}
	return 0
}

func (r runner) runKeys(args []string) int {
	flags, rest, err := parseCommon(args)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if hasHelpFlag(rest) {
		fmt.Fprint(r.stdout, keysUsage)
		return 0
	}
	if len(rest) > 0 {
		fmt.Fprintf(r.stderr, "passage: unexpected arguments: %s\n", strings.Join(rest, " "))
		return 1
	}
	storeDir, err := r.storeDir(flags)
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	keys, err := gpgdiag.New(storeDir).LocalKeys(context.Background())
	if err != nil {
		fmt.Fprintf(r.stderr, "passage: %v\n", err)
		return 1
	}
	if flags.json {
		return writeJSON(r.stdout, keysResponse{SchemaVersion: 1, Keys: keys})
	}
	for _, key := range keys {
		secret := "public-only"
		if key.HasSecret {
			secret = "own-secret"
		}
		fmt.Fprintf(r.stdout, "%s\t%s\t%s\t%s\n", key.Fingerprint, key.UID, secret, key.OwnerTrust)
	}
	return 0
}

func (r runner) load(flags commonFlags) (runtimeState, error) {
	storeDir, err := r.storeDir(flags)
	if err != nil {
		return runtimeState{}, err
	}
	stateDir, err := r.stateDir(flags)
	if err != nil {
		return runtimeState{}, err
	}
	loadResult, err := state.Load(stateDir)
	if err != nil {
		return runtimeState{}, err
	}
	store := passstore.New(storeDir, passstore.ResolvePassBinary(r.env))
	store.Stdin = os.Stdin
	store.Stderr = r.stderr
	paths, err := store.Discover()
	if err != nil {
		return runtimeState{}, err
	}
	entries := passstore.BuildEntries(paths, loadResult.State)
	return runtimeState{
		storeDir:  storeDir,
		stateDir:  stateDir,
		statePath: loadResult.Path,
		state:     loadResult.State,
		store:     store,
		entries:   entries,
	}, nil
}

func (r runner) loadInteractive(flags commonFlags) (runtimeState, error) {
	rt, err := r.load(flags)
	if err != nil {
		return runtimeState{}, err
	}
	rt.store.Stdin = nil
	rt.store.Stderr = nil
	return rt, nil
}

func (r runner) refreshInteractive(rt *runtimeState, flags commonFlags) ([]passstore.Entry, error) {
	refreshed, err := r.loadInteractive(flags)
	if err != nil {
		return nil, err
	}
	*rt = refreshed
	return refreshed.entries, nil
}

func (r runner) runInteractiveAction(parent context.Context, rt *runtimeState, flags commonFlags, req ui.ActionRequest) ui.ActionOutcome {
	ctx, cancel := context.WithTimeout(parent, interactiveActionTimeout)
	defer cancel()
	out := r.runInteractiveActionOnce(ctx, rt, flags, req)
	if out.Err != nil {
		out.Err = interactiveActionError(ctx, req, out.Err)
	}
	return out
}

func (r runner) runInteractiveActionOnce(ctx context.Context, rt *runtimeState, flags commonFlags, req ui.ActionRequest) ui.ActionOutcome {
	switch req.Action {
	case ui.ActionClearClipboard:
		res, err := clipboard.Clear(ctx)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		return ui.ActionOutcome{Message: "Clipboard cleared (" + res.Tool + ")."}
	case ui.ActionClearPins:
		rt.state.ClearPins()
		if err := rt.state.Save(rt.statePath); err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, err := r.refreshInteractive(rt, flags)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		return ui.ActionOutcome{Message: "All pins cleared.", Entries: entries}
	case ui.ActionClearRecents:
		rt.state.ClearRecents()
		if err := rt.state.Save(rt.statePath); err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, err := r.refreshInteractive(rt, flags)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		return ui.ActionOutcome{Message: "Recents cleared.", Entries: entries}
	case ui.ActionDoctor:
		report := gpgdiag.New(rt.storeDir).Doctor(ctx)
		return ui.ActionOutcome{TextTitle: "passage doctor", TextLines: doctorLines(report)}
	case ui.ActionKeys:
		keys, err := gpgdiag.New(rt.storeDir).LocalKeys(ctx)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		lines := keyLines(keys)
		if len(lines) == 0 {
			lines = []string{"No local public keys found."}
		}
		return ui.ActionOutcome{TextTitle: "passage keys", TextLines: lines}
	case ui.ActionTogglePin:
		if req.Entry.Path == "" {
			return ui.ActionOutcome{Err: errors.New("no entry selected")}
		}
		pinned := rt.state.TogglePinned(req.Entry.Path)
		if err := rt.state.Save(rt.statePath); err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, err := r.refreshInteractive(rt, flags)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		if pinned {
			return ui.ActionOutcome{Message: "Pinned " + req.Entry.Path + ".", Entries: entries}
		}
		return ui.ActionOutcome{Message: "Unpinned " + req.Entry.Path + ".", Entries: entries}
	case ui.ActionCopy:
		msg, tool, err := r.copyEntry(ctx, *rt, req.Entry.Path, false)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, refreshErr := r.refreshInteractive(rt, flags)
		if refreshErr != nil {
			return ui.ActionOutcome{Err: refreshErr}
		}
		out := ui.ActionOutcome{Message: msg, Entries: entries}
		if tool != "" {
			out.ClipArmed = true
			out.ClipRemaining = clipboardArmSeconds
			out.ClipTool = tool
		}
		return out
	case ui.ActionReveal:
		secret, msg, err := r.revealEntryInteractive(ctx, *rt, req.Entry.Path)
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, refreshErr := r.refreshInteractive(rt, flags)
		if refreshErr != nil {
			return ui.ActionOutcome{Err: refreshErr}
		}
		return ui.ActionOutcome{
			Message:     msg,
			Entries:     entries,
			SecretTitle: req.Entry.Display,
			SecretKind:  "password",
			Secret:      secret,
		}
	case ui.ActionTOTP:
		code, msg, err := r.generateTOTP(ctx, *rt, req.Entry.Path, totpOptions{copy: true})
		if err != nil {
			return ui.ActionOutcome{Err: err}
		}
		entries, refreshErr := r.refreshInteractive(rt, flags)
		if refreshErr != nil {
			return ui.ActionOutcome{Err: refreshErr}
		}
		return ui.ActionOutcome{
			Message:         defaultString(msg, "TOTP ready."),
			Entries:         entries,
			SecretTitle:     defaultString(req.Entry.Display, req.Entry.Path),
			SecretKind:      "totp",
			Secret:          code.Pretty,
			SecretRemaining: code.Remaining,
			SecretPeriod:    code.Period,
		}
	default:
		return ui.ActionOutcome{Err: fmt.Errorf("unsupported action %q", req.Action)}
	}
}

func interactiveActionError(ctx context.Context, req ui.ActionRequest, err error) error {
	verb := interactiveActionVerb(req.Action)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		if req.Entry.Path == "" {
			return fmt.Errorf("%s timed out after %s", verb, interactiveActionTimeout)
		}
		return fmt.Errorf("%s %s timed out after %s; try `pass show -- %s` once outside passage to unlock or diagnose GPG", verb, req.Entry.Path, interactiveActionTimeout, req.Entry.Path)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		if req.Entry.Path == "" {
			return fmt.Errorf("%s canceled", verb)
		}
		return fmt.Errorf("%s %s canceled", verb, req.Entry.Path)
	}
	return err
}

func interactiveActionVerb(action ui.Action) string {
	switch action {
	case ui.ActionCopy:
		return "copy"
	case ui.ActionReveal:
		return "reveal"
	case ui.ActionTOTP:
		return "TOTP"
	case ui.ActionTogglePin:
		return "pin update"
	case ui.ActionClearClipboard:
		return "clipboard clear"
	case ui.ActionClearPins:
		return "pin clear"
	case ui.ActionClearRecents:
		return "recent clear"
	case ui.ActionDoctor:
		return "doctor"
	case ui.ActionKeys:
		return "keys"
	default:
		return "action"
	}
}

func (r runner) storeDir(flags commonFlags) (string, error) {
	if flags.storeDir != "" {
		return flags.storeDir, nil
	}
	return passstore.ResolveStoreDir(r.env)
}

func (r runner) stateDir(flags commonFlags) (string, error) {
	if flags.stateDir != "" {
		return flags.stateDir, nil
	}
	return state.ResolveDir(r.env)
}

// clipboardArmSeconds is how long the interactive picker keeps a copied secret
// on the clipboard before auto-clearing it while passage stays open.
const clipboardArmSeconds = 45

func (r runner) copyEntry(ctx context.Context, rt runtimeState, entryPath string, private bool) (string, string, error) {
	entry, ok := findEntry(rt.entries, entryPath)
	if !ok {
		return "", "", fmt.Errorf("entry %q not found", entryPath)
	}
	content, err := rt.store.Show(ctx, entry.Path)
	if err != nil {
		return "", "", err
	}
	password := passstore.FirstLine(content)
	res, copyErr := clipboard.Copy(ctx, password)
	zero(password)
	zero(content)
	if !private {
		rt.state.Touch(entry.Path, passstore.TouchNow())
		if err := rt.state.Save(rt.statePath); err != nil {
			return "", "", err
		}
	}
	if copyErr != nil {
		return "Clipboard copy failed: " + copyErr.Error(), "", nil
	}
	return "Password copied to clipboard (" + res.Tool + ").", res.Tool, nil
}

func (r runner) revealEntry(ctx context.Context, rt runtimeState, entryPath string, private bool, flags commonFlags) (string, error) {
	entry, ok := findEntry(rt.entries, entryPath)
	if !ok {
		return "", fmt.Errorf("entry %q not found", entryPath)
	}
	content, err := rt.store.Show(ctx, entry.Path)
	if err != nil {
		return "", err
	}
	password := passstore.FirstLine(content)
	secret := string(password)
	res, copyErr := clipboard.Copy(ctx, password)
	zero(password)
	zero(content)
	if !private {
		rt.state.Touch(entry.Path, passstore.TouchNow())
		if err := rt.state.Save(rt.statePath); err != nil {
			return "", err
		}
	}
	if err := ui.Reveal(ctx, ui.RevealOptions{
		Output:      r.stderr,
		NoColor:     flags.noColor,
		ThemeFile:   flags.themeFile,
		Title:       entry.Display,
		Secret:      secret,
		Kind:        "password",
		NoAltScreen: flags.noAltScreen,
	}); err != nil {
		return "", err
	}
	if copyErr != nil {
		return "Clipboard copy failed; reveal only: " + copyErr.Error(), nil
	}
	return "Also copied to clipboard (" + res.Tool + ").", nil
}

func (r runner) revealEntryInteractive(ctx context.Context, rt runtimeState, entryPath string) (string, string, error) {
	entry, ok := findEntry(rt.entries, entryPath)
	if !ok {
		return "", "", fmt.Errorf("entry %q not found", entryPath)
	}
	content, err := rt.store.Show(ctx, entry.Path)
	if err != nil {
		return "", "", err
	}
	password := passstore.FirstLine(content)
	secret := string(password)
	res, copyErr := clipboard.Copy(ctx, password)
	zero(password)
	zero(content)
	rt.state.Touch(entry.Path, passstore.TouchNow())
	if err := rt.state.Save(rt.statePath); err != nil {
		return "", "", err
	}
	if copyErr != nil {
		return secret, "Clipboard copy failed; reveal only: " + copyErr.Error(), nil
	}
	return secret, "Also copied to clipboard (" + res.Tool + ").", nil
}

type totpOptions struct {
	copy    bool
	private bool
	wait    bool
	at      time.Time
}

func (r runner) totpEntry(ctx context.Context, rt runtimeState, entryPath string, opts totpOptions, flags commonFlags) (string, error) {
	code, msg, err := r.generateTOTP(ctx, rt, entryPath, opts)
	if err != nil {
		return "", err
	}
	entry, _ := findEntry(rt.entries, entryPath)
	title := defaultString(entry.Display, entryPath)
	if err := ui.Reveal(ctx, ui.RevealOptions{
		Output:      r.stderr,
		NoColor:     flags.noColor,
		ThemeFile:   flags.themeFile,
		Title:       title,
		Secret:      code.Pretty,
		Kind:        "totp",
		Remaining:   code.Remaining,
		NoAltScreen: flags.noAltScreen,
	}); err != nil {
		return "", err
	}
	return defaultString(msg, "TOTP ready."), nil
}

func (r runner) generateTOTP(ctx context.Context, rt runtimeState, entryPath string, opts totpOptions) (totp.Code, string, error) {
	entry, ok := findEntry(rt.entries, entryPath)
	if !ok {
		return totp.Code{}, "", fmt.Errorf("entry %q not found", entryPath)
	}
	target := entry.MFATarget
	if target == "" {
		return totp.Code{}, "", fmt.Errorf("no MFA entry found for %q", entry.Path)
	}
	content, err := rt.store.Show(ctx, target)
	if err != nil {
		return totp.Code{}, "", err
	}
	now := opts.at
	if now.IsZero() {
		now = time.Now()
	}
	code, err := totp.FromPassContent(content, now)
	zero(content)
	if err != nil {
		return totp.Code{}, "", err
	}
	if opts.wait && code.Remaining <= 3 && opts.at.IsZero() {
		time.Sleep(time.Duration(code.Remaining+1) * time.Second)
		code, err = r.generateTOTPCodeOnly(ctx, rt.store, target, time.Now())
		if err != nil {
			return totp.Code{}, "", err
		}
	}
	msg := ""
	if opts.copy {
		res, err := clipboard.Copy(ctx, []byte(code.Value))
		if err != nil {
			msg = "Clipboard copy failed: " + err.Error()
		} else {
			msg = "TOTP copied to clipboard (" + res.Tool + ")."
		}
	}
	if !opts.private {
		rt.state.Touch(entry.Path, passstore.TouchNow())
		if err := rt.state.Save(rt.statePath); err != nil {
			return totp.Code{}, "", err
		}
	}
	return code, msg, nil
}

func (r runner) generateTOTPCodeOnly(ctx context.Context, store passstore.Store, target string, now time.Time) (totp.Code, error) {
	content, err := store.Show(ctx, target)
	if err != nil {
		return totp.Code{}, err
	}
	code, err := totp.FromPassContent(content, now)
	zero(content)
	return code, err
}

func (r runner) showDoctorUI(ctx context.Context, storeDir string, flags commonFlags) error {
	report := gpgdiag.New(storeDir).Doctor(ctx)
	return ui.Text(ctx, ui.TextOptions{
		Output:      r.stderr,
		NoColor:     flags.noColor,
		ThemeFile:   flags.themeFile,
		Title:       "passage doctor",
		Lines:       doctorLines(report),
		NoAltScreen: flags.noAltScreen,
	})
}

func (r runner) showKeysUI(ctx context.Context, storeDir string, flags commonFlags) error {
	keys, err := gpgdiag.New(storeDir).LocalKeys(ctx)
	if err != nil {
		return err
	}
	return ui.Text(ctx, ui.TextOptions{
		Output:      r.stderr,
		NoColor:     flags.noColor,
		ThemeFile:   flags.themeFile,
		Title:       "passage keys",
		Lines:       keyLines(keys),
		NoAltScreen: flags.noAltScreen,
	})
}

func keyLines(keys []gpgdiag.LocalKey) []string {
	var lines []string
	for _, key := range keys {
		secret := "public-only"
		if key.HasSecret {
			secret = "own-secret"
		}
		lines = append(lines, fmt.Sprintf("%s  %s  %s  %s", key.UID, key.Fingerprint, secret, key.OwnerTrust))
	}
	return lines
}

func parseCommon(args []string) (commonFlags, []string, error) {
	flags := commonFlags{}
	rest := append([]string(nil), args...)
	var ok bool
	if flags.storeDir, ok = consumeStringFlag(&rest, "--store-dir"); !ok {
		flags.storeDir, _ = consumeStringFlag(&rest, "--store")
	}
	flags.stateDir, _ = consumeStringFlag(&rest, "--state-dir")
	flags.themeFile, _ = consumeStringFlag(&rest, "--theme-file")
	flags.json = consumeBoolFlag(&rest, "--json")
	flags.noColor = consumeBoolFlag(&rest, "--no-color")
	flags.noAltScreen = consumeBoolFlag(&rest, "--no-alt-screen")
	return flags, rest, nil
}

func consumeBoolFlag(args *[]string, name string) bool {
	out := (*args)[:0]
	found := false
	for _, arg := range *args {
		if arg == name {
			found = true
			continue
		}
		out = append(out, arg)
	}
	*args = out
	return found
}

func consumeStringFlag(args *[]string, name string) (string, bool) {
	out := (*args)[:0]
	value := ""
	found := false
	skip := false
	for i, arg := range *args {
		if skip {
			skip = false
			continue
		}
		if arg == name {
			if i+1 >= len(*args) {
				out = append(out, arg)
				continue
			}
			value = (*args)[i+1]
			found = true
			skip = true
			continue
		}
		if strings.HasPrefix(arg, name+"=") {
			value = strings.TrimPrefix(arg, name+"=")
			found = true
			continue
		}
		out = append(out, arg)
	}
	*args = out
	return value, found
}

func findEntry(entries []passstore.Entry, path string) (passstore.Entry, bool) {
	for _, entry := range entries {
		if entry.Path == path {
			return entry, true
		}
	}
	return passstore.Entry{}, false
}

func doctorLines(report gpgdiag.DoctorReport) []string {
	lines := []string{
		"store: " + report.StoreRoot,
		fmt.Sprintf("pass: %s", okLabel(report.PassOK)),
		fmt.Sprintf("gpg: %s", okLabel(report.GPGOK)),
		"clipboard: " + defaultString(strings.Join(report.Clipboard, ", "), "none"),
		"",
		"stores:",
	}
	for _, store := range report.Stores {
		lines = append(lines, fmt.Sprintf("  %s  %s  recipients=%d", store.Label, store.Status, store.RecipientCount))
		if len(store.Missing) > 0 {
			lines = append(lines, "    missing: "+strings.Join(store.Missing, ", "))
		}
		if len(store.Untrusted) > 0 {
			lines = append(lines, "    untrusted: "+strings.Join(store.Untrusted, ", "))
		}
	}
	if len(report.Warnings) > 0 {
		lines = append(lines, "", "warnings:")
		lines = append(lines, prefixLines(report.Warnings, "  ")...)
	}
	return lines
}

func okLabel(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
}

func prefixLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = prefix + line
	}
	return out
}

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func writeJSON(w io.Writer, value any) int {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "json error: %v\n", err)
		return 1
	}
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n"))
	return 0
}

func zero(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func writerOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

type listResponse struct {
	SchemaVersion int               `json:"schema_version"`
	StoreRoot     string            `json:"store_root"`
	Entries       []passstore.Entry `json:"entries"`
}

type showResponse struct {
	SchemaVersion int             `json:"schema_version"`
	StoreRoot     string          `json:"store_root"`
	Entry         passstore.Entry `json:"entry"`
}

type totpResponse struct {
	SchemaVersion int       `json:"schema_version"`
	Entry         string    `json:"entry"`
	Code          totp.Code `json:"code"`
}

type keysResponse struct {
	SchemaVersion int                `json:"schema_version"`
	Keys          []gpgdiag.LocalKey `json:"keys"`
}
