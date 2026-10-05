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
// longer stop the caller, so it cannot exit and orphan the program; they
// are forwarded to the program instead. A signal sent to the whole process
// group, such as a terminal's Ctrl+C, then reaches the program twice in
// quick succession; generated programs count both as one.
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
				_ = cmd.Process.Signal(sig)
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
