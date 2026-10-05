//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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

// runSignalJob is bork run in a process group of its own, as a shell runs
// a foreground job.
type runSignalJob struct {
	cmd   *exec.Cmd
	lines <-chan string
	group int
}

// writeRunSignal writes a package with source and returns its directory.
func writeRunSignal(t *testing.T, source string, unsafeGo bool) string {
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
	return dir
}

// startRunSignal starts bork run with args and waits for an output line
// (stdout or stderr) containing ready.
func startRunSignal(t *testing.T, ready string, args ...string) *runSignalJob {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, cliExecutable(t, false), append([]string{"run"}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// An orphaned program would hold the output open forever.
	cmd.WaitDelay = 5 * time.Second
	reader, writer := io.Pipe()
	cmd.Stdout, cmd.Stderr = writer, writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	job := &runSignalJob{cmd: cmd, group: cmd.Process.Pid}
	t.Cleanup(func() { _ = syscall.Kill(-job.group, syscall.SIGKILL); _ = cmd.Wait(); _ = writer.Close() })
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	job.lines = lines
	for {
		if line := job.next(t); strings.Contains(line, ready) {
			return job
		}
	}
}

func (job *runSignalJob) next(t *testing.T) string {
	t.Helper()
	select {
	case line, ok := <-job.lines:
		if !ok {
			t.Fatal("output ended")
		}
		return line
	case <-time.After(time.Minute):
		t.Fatal("no output")
		return ""
	}
}

// program is the pid of the program bork run runs: the other member of
// its process group.
func (job *runSignalJob) program(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("pgrep", "-g", strconv.Itoa(job.group)).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil && pid != job.group {
			return pid
		}
	}
	t.Fatal("no program process")
	return 0
}

// wait waits for bork run to exit and checks that it left no process of
// its group behind.
func (job *runSignalJob) wait(t *testing.T) int {
	t.Helper()
	err := job.cmd.Wait()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-job.group, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatal("bork run exited but left its program running")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return code
}

// expectCancelled checks that the program saw one cancellation and ended
// normally, and bork run with it.
func (job *runSignalJob) expectCancelled(t *testing.T) {
	t.Helper()
	if line := job.next(t); line != "cancelled" {
		t.Fatalf("program output %q", line)
	}
	if code := job.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestRunInterruptCancelsProgramAndWaits(t *testing.T) {
	t.Parallel()
	job := startRunSignal(t, "ready", writeRunSignal(t, runSignalCooperative, false))
	// Ctrl+C reaches the whole foreground process group, and bork run also
	// forwards its copy: the program must count the two as one.
	if err := syscall.Kill(-job.group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	job.expectCancelled(t)
}

func TestRunTerminateToGroupCountsOnce(t *testing.T) {
	t.Parallel()
	job := startRunSignal(t, "ready", writeRunSignal(t, runSignalCooperative, false))
	if err := syscall.Kill(-job.group, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	job.expectCancelled(t)
}

func TestRunForwardsSignalsToProgram(t *testing.T) {
	t.Parallel()
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Parallel()
			job := startRunSignal(t, "ready", writeRunSignal(t, runSignalCooperative, false))
			if err := job.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			job.expectCancelled(t)
		})
	}
}

func TestRunSecondInterruptStopsUnresponsiveProgram(t *testing.T) {
	t.Parallel()
	job := startRunSignal(t, "ready", writeRunSignal(t, runSignalStubborn, true))
	program := job.program(t)
	if err := syscall.Kill(-job.group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	// The first interrupt only cancels, which this program ignores. Wait
	// out the window in which copies of it count as the same one.
	time.Sleep(time.Second)
	if err := syscall.Kill(program, 0); err != nil {
		t.Fatalf("first interrupt stopped the program: %v", err)
	}
	if err := syscall.Kill(-job.group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := job.wait(t); code != 128+int(syscall.SIGINT) {
		t.Fatalf("exit %d, want %d", code, 128+int(syscall.SIGINT))
	}
}

func TestRunInterruptStopsHTTPServer(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "http_server"))
	if err != nil {
		t.Fatal(err)
	}
	job := startRunSignal(t, "listening", root, "--", "serve", "127.0.0.1:0")
	if err := syscall.Kill(-job.group, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if code := job.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
