//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// The program waits in a scope until it is cancelled.
const runSignalCooperative = `import "bork/time"

fn main() uses io + clock + state {
  scope s {
    println("ready")
    match (time.Sleep(s, time.Nanoseconds(3600000000000))) {
      done: Ok => println("slept")
      stopped: Cancelled => println("cancelled")
    }
  }
}
`

// The program ignores cancellation of its scope.
const runSignalStubborn = `fn block() uses clock unsafe go {
  import "time"
  for { time.Sleep(time.Hour) }
}

fn main() uses io + clock {
  scope s {
    println("ready")
    block()
  }
}
`

// startRunSignal runs bork run on source in a process group of its own, as
// a shell runs a foreground job, and waits until the program is running.
func startRunSignal(t *testing.T, source string, unsafeGo bool) (*exec.Cmd, <-chan string, int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if unsafeGo {
		mod := "module example.com/signals\nunsafe \"example.com/signals\"\n"
		if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, cliExecutable(t, false), "run", dir)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// An orphaned program would hold stdout open forever.
	cmd.WaitDelay = 5 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	group := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-group, syscall.SIGKILL); _ = cmd.Wait() })
	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	if line := nextLine(t, lines); line != "ready" {
		t.Fatalf("program did not start: %q", line)
	}
	return cmd, lines, group
}

func nextLine(t *testing.T, lines <-chan string) string {
	t.Helper()
	select {
	case line := <-lines:
		return line
	case <-time.After(time.Minute):
		t.Fatal("no output")
		return ""
	}
}

// waitRunSignal waits for bork run to exit and checks that it left no
// process of its group behind.
func waitRunSignal(t *testing.T, cmd *exec.Cmd, group int) int {
	t.Helper()
	err := cmd.Wait()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-group, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("bork run exited but left its program running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return code
}

func TestRunInterruptCancelsProgramAndWaits(t *testing.T) {
	t.Parallel()
	cmd, out, group := startRunSignal(t, runSignalCooperative, false)
	// Ctrl+C reaches the whole foreground process group.
	if err := syscall.Kill(-group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if line := nextLine(t, out); line != "cancelled" {
		t.Fatalf("program output %q", line)
	}
	if code := waitRunSignal(t, cmd, group); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunForwardsTerminateToProgram(t *testing.T) {
	t.Parallel()
	cmd, out, group := startRunSignal(t, runSignalCooperative, false)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if line := nextLine(t, out); line != "cancelled" {
		t.Fatalf("program output %q", line)
	}
	if code := waitRunSignal(t, cmd, group); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunSecondInterruptStopsUnresponsiveProgram(t *testing.T) {
	t.Parallel()
	cmd, _, group := startRunSignal(t, runSignalStubborn, true)
	if err := syscall.Kill(-group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	// The first one only cancels, which this program ignores.
	time.Sleep(500 * time.Millisecond)
	if syscall.Kill(-group, 0) != nil {
		t.Fatal("first interrupt stopped the program or bork run")
	}
	if err := syscall.Kill(-group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := waitRunSignal(t, cmd, group); code != 128+int(syscall.SIGINT) {
		t.Fatalf("exit %d, want %d", code, 128+int(syscall.SIGINT))
	}
}
