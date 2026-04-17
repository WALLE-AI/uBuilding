//go:build !windows

package shell

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the child into its own process group so killProcess can
// signal the whole tree with a negative PID.
func setProcAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcess sends SIGKILL to the child's process group. Falls back to
// killing the main pid if the pgid isn't available (e.g. not yet set).
func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}
