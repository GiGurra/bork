// Package childproc runs a program in the foreground on behalf of a bork
// command, the way a shell would.
package childproc

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// Run starts cmd and waits for it. While it runs, SIGINT and SIGTERM no
// longer stop the caller, so it cannot exit and orphan the program:
//   - SIGINT from a terminal's Ctrl+C already reaches the whole foreground
//     process group, program included, so it is not forwarded; sending it
//     again would count as a second Ctrl+C.
//   - SIGTERM is aimed at the caller alone, so it is forwarded.
func Run(cmd *exec.Cmd) error {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGTERM {
					_ = cmd.Process.Signal(sig)
				}
			case <-done:
				return
			}
		}
	}()
	return cmd.Wait()
}

// ExitCode is the status to exit with for a program that ended with err:
// its own exit code, or 128 plus the signal number if a signal killed it,
// as shells report it.
func ExitCode(err *exec.ExitError) int {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return err.ExitCode()
}
