package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestScopeDeadlineRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go race test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":           "module scopedeadline\n\ngo 1.26\n",
		"scope.go":         scopeRuntime,
		"helpers.go":       scopeHelpers,
		"deadline_test.go": scopeDeadlineRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scope deadline runtime: %v\n%s", err, out)
	}
}

const scopeDeadlineRuntimeTest = `package main
import (
 "context"
 "errors"
 "math"
 "sync"
 "testing"
 "time"
)
func mustDeadline(t *testing.T, s *_Scope) time.Time {
 t.Helper()
 value,ok:=_borkScopeContext(s).Deadline()
 if !ok { t.Fatal("missing deadline") }
 return value
}
func TestLateAncestorAndEarliestDeadline(t *testing.T) {
 parent:=_scopeWith(context.Background())
 defer parent.close()
 child:=_newScope(parent,"lexical")
 defer child.close()
 owned:=_openScope(child)
 if _,ok:=owned.ctx.Deadline();ok {t.Fatal("unexpected deadline")}
 parent.cancelAfter(60000)
 first:=mustDeadline(t,parent)
 if !mustDeadline(t,owned).Equal(first) {t.Fatal("preexisting descendant missed deadline")}
 parent.cancelAfter(120000)
 if !mustDeadline(t,parent).Equal(first) {t.Fatal("deadline extended")}
 parent.cancelAfter(30000)
 earlier:=mustDeadline(t,parent)
 if !earlier.Before(first) || !mustDeadline(t,owned).Equal(earlier) {t.Fatal("shorter ancestor deadline ignored")}
 owned.cancelAfter(10000)
 own:=mustDeadline(t,owned)
 parent.cancelAfter(20000)
 if !mustDeadline(t,owned).Equal(own) {t.Fatal("later ancestor replaced earlier child")}
 parent.cancelAfter(0)
 <-owned.ctx.Done()
 if !errors.Is(context.Cause(owned.ctx),context.DeadlineExceeded) {t.Fatalf("cause: %v",context.Cause(owned.ctx))}
}
func TestExternalDeadlineAndContextIdentity(t *testing.T) {
 type key struct{}
 base:=context.WithValue(context.Background(),key{},"trace")
 ctx,stop:=context.WithDeadline(base,time.Now().Add(time.Hour))
 defer stop()
 scope:=_scopeWith(ctx)
 defer scope.close()
 child:=_newScope(scope,"child")
 defer child.close()
 expected,_:=ctx.Deadline()
 scope.cancelAfter(7200000)
 if !mustDeadline(t,child).Equal(expected) {t.Fatal("external limit lost")}
 if child.ctx.Value(key{})!="trace" {t.Fatal("context value lost")}
 scope.cancelAfter(0)
 if !errors.Is(context.Cause(child.ctx),context.DeadlineExceeded) {t.Fatal("cancel identity lost")}
}
func TestDelayBoundaries(t *testing.T) {
 for _,ms:=range []int64{0,-1,math.MinInt64} {
  s:=_scopeWith(context.Background())
  s.cancelAfter(ms)
  if s.ctx.Err()==nil || !errors.Is(context.Cause(s.ctx),context.DeadlineExceeded) {t.Fatal("nonpositive delay did not cancel immediately")}
  if mustDeadline(t,s).After(time.Now()) {t.Fatal("immediate deadline in future")}
  s.close()
 }
 s:=_scopeWith(context.Background())
 defer s.close()
 s.cancelAfter(math.MaxInt64)
 if s.ctx.Err()!=nil || time.Until(mustDeadline(t,s))<time.Hour {t.Fatal("large delay overflowed")}
 s.cancelAfter(5000)
 if time.Until(mustDeadline(t,s))>6*time.Second {t.Fatal("saturated deadline could not shorten")}
}
func TestConcurrentDeadlineAndClose(t *testing.T) {
 for iteration:=0;iteration<20;iteration++ {
  parent:=_scopeWith(context.Background())
  child:=_openScope(parent)
  var workers sync.WaitGroup
  for index:=0;index<10;index++ {
   workers.Add(1)
   go func(index int) {
    defer workers.Done()
    for repeat:=0;repeat<100;repeat++ {
     parent.cancelAfter(int64(10000+index))
     child.ctx.Deadline()
    }
   }(index)
  }
  workers.Add(1)
  go func(){defer workers.Done();child.close();parent.close()}()
  workers.Wait()
  if parent.ctx.Err()==nil || child.ctx.Err()==nil {t.Fatal("close lost cancellation")}
 }
}
func TestTimerCause(t *testing.T) {
 s:=_scopeWith(context.Background())
 defer s.close()
 s.cancelAfter(1)
 select {
 case <-s.ctx.Done():
  if !errors.Is(context.Cause(s.ctx),context.DeadlineExceeded) || !errors.Is(s.ctx.Err(),context.DeadlineExceeded) {t.Fatal("timer lost typed deadline")}
 case <-time.After(time.Second): t.Fatal("deadline did not cancel")
 }
}
func TestContendedAbsoluteDeadline(t *testing.T) {
 s:=_scopeWith(context.Background())
 defer s.close()
 started:=make(chan struct{})
 finished:=make(chan struct{})
 s.mu.Lock()
 go func(){close(started);s.cancelAfter(200);close(finished)}()
 <-started
 // Keep the setter waiting past its deadline. Scheduling from the original
 // delay after this lock would add another 200ms to the advertised deadline.
 time.Sleep(400*time.Millisecond)
 s.mu.Unlock()
 <-finished
 if !mustDeadline(t,s).Before(time.Now()) {t.Fatal("setter did not enter before lock release")}
 if !errors.Is(s.ctx.Err(),context.DeadlineExceeded) {t.Fatal("expired deadline scheduled another full delay")}
}
`
