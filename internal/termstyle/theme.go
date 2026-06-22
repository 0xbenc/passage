package termstyle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Theme is passage's concrete palette: a role->SGR-code map plus a NoColor
// switch. It is in-memory render state (never serialized — ThemeConfig is), so
// it stays local to passage along with its builtin palettes; the interchange
// surface lives in termtheme (see shim.go).
type Theme struct {
	Name    string
	NoColor bool
	Codes   map[Role]string
}

// ThemeOptions selects and loads a theme. Name is deprecated and ignored; the
// base palette comes from the config's `theme =` line.
type ThemeOptions struct {
	Name            string
	File            string
	NoColor         bool
	Env             []string
	SkipDefaultFile bool
}

func TerminalTheme() Theme {
	return Theme{
		Name: "terminal",
		Codes: map[Role]string{
			RoleTitle:       "32",
			RolePrimary:     "36",
			RoleSecondary:   "34",
			RoleAccent:      "33",
			RoleMuted:       "2",
			RoleSubtle:      "39",
			RoleForeground:  "39",
			RoleSelected:    "39;4",
			RoleSelectedBar: "100",
			RoleBorder:      "1;32",
			RoleSuccess:     "32",
			RoleWarning:     "33",
			RoleDanger:      "31",
			RoleInfo:        "35",
			RoleSearch:      "1;39",
			RolePill:        "1;7",
		},
	}
}

func VividTheme() Theme {
	return Theme{
		Name: "vivid",
		Codes: map[Role]string{
			RoleTitle:       "1;38;2;96;221;255",
			RolePrimary:     "1;38;2;96;221;255",
			RoleSecondary:   "1;38;2;120;183;255",
			RoleAccent:      "1;38;2;255;209;102",
			RoleMuted:       "38;2;132;145;160",
			RoleSubtle:      "38;2;58;69;87",
			RoleForeground:  "38;2;235;239;245",
			RoleSelected:    "1;38;2;255;255;255",
			RoleSelectedBar: "48;2;45;55;72",
			RoleBorder:      "38;2;58;69;87",
			RoleSuccess:     "1;38;2;134;239;172",
			RoleWarning:     "1;38;2;255;209;102",
			RoleDanger:      "1;38;2;255;151;112",
			RoleInfo:        "1;38;2;214;160;255",
			RoleSearch:      "1;38;2;255;255;255",
			RolePill:        "1;38;2;25;30;38;48;2;96;221;255",
		},
	}
}

// BuiltinThemeNames lists the selectable base palettes in display order. It is
// the single source of truth the theme editor cycles through, so adding a
// builtin here surfaces it in the UI automatically.
func BuiltinThemeNames() []string {
	return []string{"terminal", "vivid"}
}

func BuiltinTheme(name string) (Theme, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "terminal", "default", "auto":
		return TerminalTheme(), true
	case "vivid", "rgb", "truecolor":
		return VividTheme(), true
	default:
		return Theme{}, false
	}
}

func ResolveTheme(opts ThemeOptions) (Theme, error) {
	env := themeEnv(opts.Env)
	file, explicitFile := resolveThemeFile(opts.File, env, opts.SkipDefaultFile)

	var cfg ThemeConfig
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			if explicitFile || !errors.Is(err, fs.ErrNotExist) {
				return Theme{}, fmt.Errorf("load theme config %s: %w", file, err)
			}
		} else {
			parsed, err := ParseThemeConfig(data)
			if err != nil {
				return Theme{}, fmt.Errorf("load theme config %s: %w", file, err)
			}
			cfg = parsed
		}
	}

	// Honor the config's base theme (`theme = vivid`). Unknown names fall
	// back to the terminal palette rather than erroring, so a base written by
	// a newer passage never hard-fails an older binary.
	base := TerminalTheme()
	if cfg.BaseName != "" {
		if b, ok := BuiltinTheme(cfg.BaseName); ok {
			base = b
		}
	}
	theme := base.Normalized()
	if len(cfg.Codes) > 0 {
		theme.Name = "custom"
		for role, code := range cfg.Codes {
			theme.Codes[role] = code
		}
	}
	if opts.NoColor || envTruthy(env["PASSAGE_NO_COLOR"]) || env["NO_COLOR"] != "" {
		theme.NoColor = true
	}
	return theme.Normalized(), nil
}

func (t Theme) IsZero() bool {
	return t.Name == "" && len(t.Codes) == 0 && !t.NoColor
}

func (t Theme) Normalized() Theme {
	base, _ := BuiltinTheme(t.Name)
	if base.IsZero() {
		base = TerminalTheme()
	}
	base.NoColor = t.NoColor
	if t.Name != "" {
		base.Name = t.Name
	}
	if base.Codes == nil {
		base.Codes = make(map[Role]string)
	} else {
		base.Codes = copyRoleCodes(base.Codes)
	}
	for role, code := range t.Codes {
		base.Codes[role] = code
	}
	return base
}

func (t Theme) WithNoColor(noColor bool) Theme {
	t = t.Normalized()
	t.NoColor = noColor
	return t
}

func (t Theme) Style(role Role, value string) string {
	code, ok := t.Codes[role]
	if !ok {
		t = t.Normalized()
		code = t.Codes[role]
	}
	return Apply(t.NoColor, code, value)
}

func copyRoleCodes(codes map[Role]string) map[Role]string {
	copied := make(map[Role]string, len(codes))
	for role, code := range codes {
		copied[role] = code
	}
	return copied
}

func ThemeConfigPath(file string, env []string) (string, error) {
	path, _ := resolveThemeFile(file, themeEnv(env), false)
	if path == "" {
		return "", errors.New("could not resolve theme config path")
	}
	return path, nil
}

func resolveThemeFile(file string, env map[string]string, skipDefault bool) (string, bool) {
	if strings.TrimSpace(file) != "" {
		return expandThemePath(file), true
	}
	if value := strings.TrimSpace(env["PASSAGE_THEME_FILE"]); value != "" {
		return expandThemePath(value), true
	}
	if skipDefault {
		return "", false
	}
	configDir, err := os.UserConfigDir()
	if err != nil || configDir == "" {
		return "", false
	}
	return filepath.Join(configDir, "passage", "theme.conf"), false
}

func expandThemePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func themeEnv(env []string) map[string]string {
	if env == nil {
		env = os.Environ()
	}
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	return values
}

func envTruthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}
