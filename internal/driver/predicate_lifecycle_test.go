package driver

import (
	"errors"
	"go/constant"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

func fakePredicateEvaluator(t *testing.T, program *compiledProgram, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fixture requires a shell")
	}
	body = strings.ReplaceAll(body, "'", "'\\''")
	predicateMemoGoCounter(t, program.context, "while [ \"$1\" != '-o' ]; do shift; done\nshift\nprintf '%s\\n' '#!/bin/sh' '"+body+"' > \"$1\"\nchmod 700 \"$1\"")
}

func simplePredicateQuery(program *compiledProgram) []check.Query {
	pred := program.info.Funcs["p"]
	return []check.Query{{Pred: pred, Params: pred.Params, Args: []constant.Value{constant.MakeInt64(1)}}}
}

func TestPredicateDefaultLimitAndCleanup(t *testing.T) {
	t.Parallel()
	program := predicateMemoProgram(t, "pred p(n:Int){n>0}")
	program.context.evalLimit = 150 * time.Millisecond
	marker := filepath.Join(t.TempDir(), "executable")
	fakePredicateEvaluator(t, program, "printf '%s' \"$0\" > '"+strings.ReplaceAll(marker, "'", "'\\''")+"'\nwhile :; do :; done")
	_, err := evaluatorWithContext(program.files, program.info, program.module, program.context)(simplePredicateQuery(program))
	if err == nil || !strings.Contains(err.Error(), "predicate evaluation exceeded 150ms") {
		t.Fatalf("default predicate limit: %v", err)
	}
	path, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(string(path))); !os.IsNotExist(err) {
		t.Fatalf("evaluation directory survived: %v", err)
	}
}

func TestPredicateFailureSummary(t *testing.T) {
	for _, tc := range []struct{ output, want string }{
		{"panic: proof failed\n\ngoroutine 1 [running]:\nmain.p()\n", "panic: proof failed"},
		{"runtime: goroutine stack exceeds limit\nruntime: sp=...\nfatal error: stack overflow\n\nruntime stack:\n", "fatal error: stack overflow"},
		{"", "exit status 2"},
		{"failed\nverbose details\n", "failed"},
	} {
		if got := predicateFailure(tc.output, errors.New("exit status 2")); got != tc.want {
			t.Errorf("summary=%q, want %q", got, tc.want)
		}
	}
	if got := predicateFailure(strings.Repeat("x", 2048), errors.New("exit status 2")); len(got) > 1027 {
		t.Fatalf("unbounded summary: %d", len(got))
	}
}

func TestPredicatePanicDiagnostic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.bork")
	source := `fn fail(n: Int): Bool { panic("predicate exploded") }
pred p(n: Int) { fail(n) }
fn need(n: Int where p): Int { n }
fn main() { println(need(3)) }`
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Check(path)
	if err == nil {
		t.Fatal("expected predicate panic")
	}
	message := err.Error()
	if !strings.Contains(message, path+":4:") || !strings.Contains(message, "a predicate failed: panic: predicate exploded") || strings.Contains(message, "goroutine") || strings.Contains(message, ".go:") {
		t.Fatalf("unexpected diagnostic: %s", message)
	}
}
