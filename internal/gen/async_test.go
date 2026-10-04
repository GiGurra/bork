package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAsyncRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":   "module asyncrt\n\ngo 1.26\n",
		"scope.go": scopeRuntime, "lazy.go": lazyRuntime, "async.go": asyncRuntime, "async_test.go": asyncRuntimeTests,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("async runtime: %v\n%s", err, out)
	}
}

const asyncRuntimeTests = `package main
import (
 "bytes"
 "context"
 "fmt"
 "log/slog"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
 "time"
)
func panicOf(f func()) (result any) {defer func(){result=recover()}();f();return nil}
func TestStartAndConcurrentReads(t *testing.T) {
 s:=_scopeWith(context.Background());defer s.close()
 var calls atomic.Int32
 started,release:=make(chan struct{}),make(chan struct{})
 value:=_asyncNew(s,func()int{calls.Add(1);close(started);<-release;return 42})
 <-started
 if calls.Load()!=1 {t.Fatal("initializer did not start")}
 var readers sync.WaitGroup
 for i:=0;i<20;i++ {readers.Add(1);go func(){defer readers.Done();if value.get()!=42{t.Error("wrong result")}}()}
 close(release);readers.Wait()
 if value.get()!=42||calls.Load()!=1{t.Fatal("work repeated")}
}
func TestUnreadSuccessAndCancellation(t *testing.T) {
 s:=_scopeWith(context.Background())
 var finished,cleanup atomic.Bool
 _=_asyncNew(s,func()int{<-s.ctx.Done();finished.Store(true);return 7})
 s.Defer(func(){if !finished.Load(){t.Error("cleanup ran before task joined")};cleanup.Store(true)})
 s.close()
 if !finished.Load()||!cleanup.Load(){t.Fatal("scope did not cancel and join unread work")}
}
func TestUnreadPanicCancelsSiblingAndReports(t *testing.T) {
 s:=_scopeWith(context.Background())
 var stopped atomic.Bool
 _=_asyncNew(s,func()int{<-s.ctx.Done();stopped.Store(true);return 1})
 _=_asyncNew(s,func()int{panic("unread failure")})
 failure:=panicOf(s.close)
 if failure==nil||!strings.Contains(fmt.Sprint(failure),"unread failure")||!stopped.Load(){t.Fatalf("failure %v, sibling stopped %v",failure,stopped.Load())}
}
func TestReadsObserveAndCachePanic(t *testing.T) {
 for _,logged:=range []bool{false,true}{
  s:=_scopeWith(context.Background());s.logFailures=logged
  var calls atomic.Int32
  value:=_asyncNew(s,func()int{calls.Add(1);panic("observed failure")})
  for i:=0;i<3;i++ {if got:=panicOf(func(){value.get()});got!="observed failure"{t.Fatalf("lost panic: %v",got)}}
  if !s.tasks[0].reported.Load()||calls.Load()!=1{t.Fatal("read did not report task or work repeated")}
  if got:=panicOf(s.close);got!=nil{t.Fatalf("observed panic reported twice: %v",got)}
 }
}
func TestUnreadPanicLogsAtClose(t *testing.T) {
 var log bytes.Buffer
 original:=slog.Default();slog.SetDefault(slog.New(slog.NewTextHandler(&log,nil)));defer slog.SetDefault(original)
 s:=_scopeWith(context.Background());s.logFailures=true
 _=_asyncNew(s,func()int{panic("logged unread failure")})
 if got:=panicOf(s.close);got!=nil{t.Fatalf("log policy raised panic: %v",got)}
 if !strings.Contains(log.String(),"logged unread failure"){t.Fatal(log.String())}
}
func TestTaskTimeoutRetainsOrphanWork(t *testing.T) {
 s:=_scopeWith(context.Background());s.taskTimeout=time.Millisecond
 release,finished:=make(chan struct{}),make(chan struct{})
 value:=_asyncNew(s,func()int{<-release;close(finished);return 9})
 s.close()
 select{case <-finished:t.Fatal("unfinished task did not remain orphaned");default:}
 close(release)
 if value.get()!=9{t.Fatal("orphan result lost")}
}
func TestCompilerCannotSchedule(t *testing.T) {
 s:=_scopeWith(context.Background());defer s.close()
 _lazyCompileTime=true
 defer func(){_lazyCompileTime=false}()
 calls:=0
 got:=panicOf(func(){_asyncNew(s,func()int{calls++;return 1})})
 if got!="bork: compile-time evaluation cannot schedule an async initializer"||calls!=0||len(s.tasks)!=0{t.Fatalf("scheduled compile-time work: %v",got)}
}
`
