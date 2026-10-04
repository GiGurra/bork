//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package driver

import (
	"os/exec"
	"syscall"
)

// Let Delve stop its debuggee before it exits; Delve owns a separate process
// group for the debuggee, so killing only the adapter group is insufficient.
func configureDebugProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
}
