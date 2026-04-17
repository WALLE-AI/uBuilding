//go:build windows

package shell

import (
	"os/exec"
	"syscall"
)

// setProcAttrs puts the child into a new process group (CREATE_NEW_PROCESS_GROUP)
// so we can terminate it cleanly on timeout/cancel.
func setProcAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= 0x00000200 // CREATE_NEW_PROCESS_GROUP
}

// killProcess forcibly terminates the child. Windows has no pgid equivalent,
// so we use taskkill for recursive termination when possible and fall back to
// Process.Kill otherwise.
func killProcess(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	// taskkill /T kills the process tree rooted at pid.
	_ = exec.Command("taskkill", "/F", "/T", "/PID", itoa(pid)).Run()
	return cmd.Process.Kill()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
