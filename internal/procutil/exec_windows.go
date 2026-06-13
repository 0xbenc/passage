//go:build windows

package procutil

import (
	"os/exec"
	"time"
)

func ConfigureCommandCancellation(cmd *exec.Cmd) {
	cmd.WaitDelay = 2 * time.Second
}
