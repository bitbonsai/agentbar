//go:build windows

package agy

import "os/exec"

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
