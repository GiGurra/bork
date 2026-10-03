package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFanInRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go race test")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"go.mod":        "module faninrt\n\ngo 1.26\n",
		"scope.go":      scopeRuntime,
		"fanin.go":      fanInRuntime,
		"fanin_test.go": fanInRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fan-in runtime: %v\n%s", err, out)
	}
}

const fanInRuntimeTest = `package main
import (
 "context"
 "errors"
 "reflect"
 "sync/atomic"
 "testing"
 "time"
)
type Cancelled struct { reason string }
type Closed struct {}

func TestFirstKeepsLoserAndReportsPanic(t *testing.T) {
 s := _scopeWith(context.Background()); defer s.close()
 release := make(chan struct{})
 loser := s.Go(func() any { <-release; return 9 })
 winner := s.Go(func() any { return 7 }); <-winner.done
 if _borkFanInFirst(s.ctx, []*_task{loser,winner}) != 1 { t.Fatal("wrong winner") }
 select { case <-loser.done: t.Fatal("loser was stopped"); default: }
 close(release); if loser.Await() != 9 { t.Fatal("lost loser value") }
 failed := s.Go(func() any { panic("failure") }); <-failed.done
 if _borkFanInFirst(s.ctx, []*_task{failed}) != 0 { t.Fatal("panic hidden by cancellation") }
 func() { defer func(){ if recover() != "failure" { t.Error("lost panic") } }(); failed.Await() }()
}

func TestTimeoutJoinsAndStopsDeadline(t *testing.T) {
 s := _scopeWith(context.Background()); defer s.close()
 var finished atomic.Bool
 result := _borkFanInTimeout(s, 100, func(child *_Scope) any {
  child.Go(func() any { <-child.ctx.Done(); finished.Store(true); return nil })
  return 7
 })
 if _, ok := result.(Cancelled); !ok || !finished.Load() { t.Fatal("deadline did not cancel and join spawned task") }
 var retained *_Scope
 if _borkFanInTimeout(s, 100, func(child *_Scope) any { retained=child; return 8 }) != 8 { t.Fatal("success lost") }
 time.Sleep(150*time.Millisecond)
 if retained.ctx.Err() != nil || s.ctx.Err() != nil { t.Fatal("successful callback's resources later cancelled") }
}

func TestJoinIgnoresScopeCloseTimeout(t *testing.T) {
 s := _scopeWith(context.Background()); defer s.close()
 s.taskTimeout = time.Millisecond
 release := make(chan struct{})
 s.Go(func() any { <-release; panic("late failure") })
 done := make(chan any, 1)
 go func() { defer func() { done<-recover() }(); _borkFanInJoin(s) }()
 select { case <-done: t.Fatal("join returned while child task was running"); case <-time.After(20*time.Millisecond): }
 close(release)
 if <-done != "late failure" { t.Fatal("joined task panic was lost") }
}

func TestSynchronousPanicJoins(t *testing.T) {
 s := _scopeWith(context.Background()); defer s.close()
 var finished atomic.Bool
 func() {
  defer func(){ if recover() != "callback" { t.Error("original panic lost") } }()
  _borkFanInTimeout(s, 10000, func(child *_Scope) any {
   child.Go(func() any { <-child.ctx.Done(); finished.Store(true); return nil })
   panic("callback")
  })
 }()
 if !finished.Load() { t.Fatal("panic returned before task joined") }
}

func TestSelectionConsumesOnlyWinner(t *testing.T) {
 for i:=0; i<100; i++ {
  a,b := make(chan int,1),make(chan string,1); a<-7; b<-"eight"
  closed:=make(chan struct{}); close(closed)
  arms:=[]*_borkReceiveChoice{
   {reflect.ValueOf(a),closed,context.Background()},
   {reflect.ValueOf(b),nil,context.Background()},
  }
  index,value := _borkFanInReceive(context.Background(),arms)
  if len(a)+len(b)!=1 { t.Fatal("more than one channel consumed") }
  if index==0 && value!=7 || index==1 && value!="eight" { t.Fatal("wrong decoder input") }
  if index==1 { <-a }
  _,value = _borkFanInReceive(context.Background(),arms[:1])
  if _,ok:=value.(Closed);!ok { t.Fatal("closed buffer not drained") }
 }
 ctx,cancel:=context.WithCancelCause(context.Background());cancel(errors.New("stopped"))
 if index,value:=_borkFanInReceive(ctx,nil);index!=-1 || value.(Cancelled).reason!="stopped" { t.Fatal("selection cancellation lost") }
}
`
