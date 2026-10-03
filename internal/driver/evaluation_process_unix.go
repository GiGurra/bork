//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package driver

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Keep evaluator descendants in the process group so deadline cancellation
// kills and reaps the compiler-started evaluator without leaving its children.
func configureEvaluationProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
