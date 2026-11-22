//go:build windows

package claude

import "os/exec"

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
