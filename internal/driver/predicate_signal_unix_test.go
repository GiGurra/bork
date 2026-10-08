//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestPredicateSignalHelper(t *testing.T) {
	marker := os.Getenv("BORK_PREDICATE_SIGNAL_MARKER")
	if marker == "" {
		t.Skip("subprocess helper")
	}
	program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
	program.context.evalLimit = time.Minute
	quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }
	body := "printf '%s' \"$$\" > " + quote(marker+".pid") + "\nprintf '%s' \"$0\" > " + quote(marker+".path") + "\n(sleep 2; touch " + quote(marker+".survived") + ") &\ntouch " + quote(marker+".ready") + "\nwhile :; do :; done"
	if os.Getenv("BORK_PREDICATE_SIGNAL_BUILD") == "1" {
		body = "while [ \"$1\" != '-o' ]; do shift; done\nshift\nprintf '%s' \"$$\" > " + quote(marker+".pid") + "\nprintf '%s' \"$1\" > " + quote(marker+".path") + "\n(sleep 2; touch " + quote(marker+".survived") + ") &\ntouch " + quote(marker+".ready") + "\nwhile :; do :; done"
		predicateMemoGoCounter(t, program.context, body)
	} else {
		fakePredicateEvaluator(t, program, body)
	}
	if os.Getenv("BORK_PREDICATE_SIGNAL_STAGE") == "1" {
		if err := os.WriteFile(marker+".ready", nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := evaluatorWithContext(program.files, program.info, program.module, program.context)(simplePredicateQuery(program))
	if err == nil || !strings.Contains(err.Error(), "predicate evaluation interrupted") {
		t.Fatalf("signal result: %v", err)
	}
}

func TestPredicateSignalCleanup(t *testing.T) {
	t.Parallel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"stage", "build", "evaluation"} {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
			t.Run(phase+"/"+sig.String(), func(t *testing.T) {
				t.Parallel()
				if phase == "stage" && !cacheTrimSupported() {
					t.Skip("stable staging unsupported")
				}
				marker := filepath.Join(t.TempDir(), "eval")
				cmd := exec.Command(executable, "-test.run=^TestPredicateSignalHelper$", "-test.count=1")
				cmd.Env = append(os.Environ(), "BORK_PREDICATE_SIGNAL_MARKER="+marker)
				if phase == "build" {
					cmd.Env = append(cmd.Env, "BORK_PREDICATE_SIGNAL_BUILD=1")
				}
				stageBase := filepath.Join(t.TempDir(), "cache")
				tempBase := t.TempDir()
				if phase == "stage" {
					if err := os.MkdirAll(stageBase, 0700); err != nil {
						t.Fatal(err)
					}
					root, err := os.OpenRoot(stageBase)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = root.Close() }()
					lock, err := (cacheStore{}).lock(root, "mutation.lock")
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = lock.Close() }()
					cmd.Env = append(cmd.Env, "BORKCACHE="+stageBase, "BORK_CACHE=on", "TMPDIR="+tempBase, "BORK_PREDICATE_SIGNAL_STAGE=1")
				}
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				var evaluatorPID int
				t.Cleanup(func() {
					if evaluatorPID != 0 {
						_ = syscall.Kill(-evaluatorPID, syscall.SIGKILL)
					}
					_ = cmd.Process.Kill()
				})
				deadline := time.Now().Add(30 * time.Second)
				for {
					if _, err := os.Stat(marker + ".ready"); err == nil {
						locks, _ := filepath.Glob(filepath.Join(stageBase, "locks", "stage-v3", "*.lock"))
						if phase != "stage" || len(locks) > 0 {
							break
						}
					}
					select {
					case err := <-done:
						t.Fatalf("helper exited before evaluator: %v\n%s", err, output.String())
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("evaluator did not start")
					}
					time.Sleep(10 * time.Millisecond)
				}
				if phase != "stage" {
					pid, err := os.ReadFile(marker + ".pid")
					if err != nil {
						t.Fatal(err)
					}
					evaluatorPID, err = strconv.Atoi(string(pid))
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := cmd.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("helper failed: %v\n%s", err, output.String())
					}
				case <-time.After(10 * time.Second):
					t.Fatal("compiler did not finish after signal")
				}
				if phase == "stage" {
					leftovers, _ := filepath.Glob(filepath.Join(tempBase, "bork-eval-*"))
					if len(leftovers) > 0 {
						t.Fatalf("staging signal left temp directories: %v", leftovers)
					}
					return
				}
				if err := syscall.Kill(evaluatorPID, 0); err != syscall.ESRCH {
					t.Fatalf("evaluator %d survived: %v", evaluatorPID, err)
				}
				path, err := os.ReadFile(marker + ".path")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Dir(string(path))); !os.IsNotExist(err) {
					t.Fatalf("temp directory survived %s: %v", sig, err)
				}
				time.Sleep(2200 * time.Millisecond)
				if _, err := os.Stat(marker + ".survived"); !os.IsNotExist(err) {
					t.Fatalf("descendant survived %s: %v", sig, err)
				}
			})
		}
	}

}

func TestPredicateExitKillsDescendants(t *testing.T) {
	t.Parallel()
	for _, exit := range []string{"printf true", "echo 'panic: failed' >&2; exit 2"} {
		t.Run(exit, func(t *testing.T) {
			t.Parallel()
			program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
			marker := filepath.Join(t.TempDir(), "child")
			quote := func(text string) string { return "'" + strings.ReplaceAll(text, "'", "'\\''") + "'" }
			body := "printf '%s' \"$0\" > " + quote(marker+".path") + "\n(touch " + quote(marker+".ready") + "; sleep 2; touch " + quote(marker+".survived") + ") </dev/null >/dev/null 2>&1 &\nwhile [ ! -f " + quote(marker+".ready") + " ]; do :; done\n" + exit
			fakePredicateEvaluator(t, program, body)
			_, err := evaluatorWithContext(program.files, program.info, program.module, program.context)(simplePredicateQuery(program))
			if exit == "printf true" && err != nil {
				t.Fatal(err)
			}
			if exit != "printf true" && (err == nil || !strings.Contains(err.Error(), "panic: failed")) {
				t.Fatalf("failure: %v", err)
			}
			path, err := os.ReadFile(marker + ".path")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Dir(string(path))); !os.IsNotExist(err) {
				t.Fatalf("evaluation directory survived: %v", err)
			}
			time.Sleep(2200 * time.Millisecond)
			if _, err := os.Stat(marker + ".survived"); !os.IsNotExist(err) {
				t.Fatalf("descendant survived exit: %v", err)
			}
		})
	}
}
