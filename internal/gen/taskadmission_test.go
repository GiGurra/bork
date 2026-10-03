package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTaskAdmissionRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go race test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":       "module taskadmission\n\ngo 1.26\n",
		"scope.go":     scopeRuntime,
		"helpers.go":   scopeHelpers,
		"task_test.go": taskAdmissionRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("task admission runtime: %v\n%s", err, out)
	}
}

const taskAdmissionRuntimeTest = `package main
import (
 "context"
 "sync"
 "sync/atomic"
 "testing"
)

func TestRejectCancelledAndClosed(t *testing.T) {
 for _,closed:=range []bool{false,true} {
  s:=_scopeWith(context.Background())
  if closed {s.close()} else {s.cancel(context.Canceled)}
  calls:=0
  wait,ok:=_borkTryScopeTask(s,func()any{calls++;return 1})
  if ok || wait!=nil || calls!=0 { t.Fatal("rejected work ran or registered") }
  s.close()
 }
 // Ordinary task submission retains its previous cancelled-scope behavior.
 s:=_scopeWith(context.Background())
 s.cancel(context.Canceled)
 if got:=s.Go(func()any{return 7}).Await();got!=7 {t.Fatalf("ordinary task: %v",got)}
 s.close()
}

func TestAdmissionRacesScopeClose(t *testing.T) {
 for attempt:=0;attempt<100;attempt++ {
  s:=_scopeWith(context.Background())
  var calls atomic.Int32
  var wait sync.WaitGroup
  wait.Add(2)
  var admitted bool
  var result func()any
  go func(){defer wait.Done();result,admitted=_borkTryScopeTask(s,func()any{calls.Add(1);return 42})}()
  go func(){defer wait.Done();s.close()}()
  wait.Wait()
  expected:=int32(0)
  if admitted {expected=1;if result()!=42 {t.Fatal("task result lost")}}
  if calls.Load()!=expected {t.Fatalf("admission %t, calls %d",admitted,calls.Load())}
 }
}

func TestTaskWaitReportsPanic(t *testing.T) {
 s:=_scopeWith(context.Background())
 wait,ok:=_borkTryScopeTask(s,func()any{panic("task failed")})
 if !ok {t.Fatal("task rejected")}
 func(){defer func(){if recover()!="task failed" {t.Error("task panic lost")}}();wait()}()
 s.close()
}
`
