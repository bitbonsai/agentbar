//go:build !windows

package claude

import (
	"os/exec"
	"syscall"
)

// killProcessGroup kills agy and anything it spawned. creack/pty starts the
// child with Setsid, so its pid doubles as the process group id. Setting
// Setpgid ourselves makes fork/exec fail with EPERM.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
