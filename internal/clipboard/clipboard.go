package clipboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/0xbenc/passage/internal/procutil"
)

type Result struct {
	Tool string `json:"tool"`
}

const providerTimeout = 5 * time.Second

var osc52TTYPath = "/dev/tty"

func Copy(ctx context.Context, text []byte) (Result, error) {
	var attempts []provider
	attempts = append(attempts, provider{Name: "pbcopy"})
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		attempts = append(attempts,
			provider{Name: "wl-copy"},
			provider{Name: "xclip", Args: []string{"-selection", "clipboard"}},
		)
	} else {
		attempts = append(attempts,
			provider{Name: "xclip", Args: []string{"-selection", "clipboard"}},
			provider{Name: "wl-copy"},
		)
	}
	attempts = append(attempts, provider{Name: "xsel", Args: []string{"--clipboard", "--input"}})

	var lastErr error
	for _, attempt := range attempts {
		if _, err := exec.LookPath(attempt.Name); err != nil {
			continue
		}
		providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
		cmd := exec.CommandContext(providerCtx, attempt.Name, attempt.Args...)
		procutil.ConfigureCommandCancellation(cmd)
		cmd.Stdin = bytes.NewReader(text)
		out, err := cmd.CombinedOutput()
		ctxErr := providerCtx.Err()
		cancel()
		if err != nil {
			if ctxErr != nil {
				lastErr = fmt.Errorf("%s failed: %w", attempt.Name, ctxErr)
				continue
			}
			lastErr = fmt.Errorf("%s failed: %w: %s", attempt.Name, err, bytes.TrimSpace(out))
			continue
		}
		return Result{Tool: attempt.Name}, nil
	}
	if res, err := copyOSC52(ctx, text); err == nil {
		return res, nil
	}
	if lastErr != nil {
		return Result{}, lastErr
	}
	return Result{}, errors.New("no clipboard tool found (tried pbcopy, wl-copy, xclip, xsel, osc52)")
}

func Clear(ctx context.Context) (Result, error) {
	return Copy(ctx, nil)
}

type provider struct {
	Name string
	Args []string
}

func copyOSC52(ctx context.Context, text []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if os.Getenv("PASSAGE_NO_OSC52") != "" {
		return Result{}, errors.New("osc52 disabled")
	}
	file, err := os.OpenFile(osc52TTYPath, os.O_WRONLY, 0)
	if err != nil {
		return Result{}, fmt.Errorf("osc52 unavailable: %w", err)
	}
	defer file.Close()

	payload := base64.StdEncoding.EncodeToString(text)
	sequence := "\x1b]52;c;" + payload + "\a"
	tool := "osc52"
	if os.Getenv("TMUX") != "" || strings.HasPrefix(os.Getenv("TERM"), "tmux") {
		sequence = "\x1bPtmux;" + strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b") + "\x1b\\"
		tool = "osc52-tmux"
	}
	if _, err := file.WriteString(sequence); err != nil {
		return Result{}, fmt.Errorf("osc52 failed: %w", err)
	}
	return Result{Tool: tool}, nil
}
