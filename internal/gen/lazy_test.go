package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLazyRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":       "module lazyrt\n\ngo 1.23\n",
		"lazy.go":      lazyRuntime,
		"lazy_test.go": lazyRuntimeTests,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
}

const lazyRuntimeTests = `package main
import (
 "runtime"
 "sync"
 "sync/atomic"
 "testing"
 "time"
)
func panicOf(f func()) (value any) {
 defer func(){value=recover()}()
 f()
 return
}
func TestDemandAndConcurrentValue(t *testing.T) {
 var calls atomic.Int32
 unused:=_lazyNew(func() int {calls.Add(1000);return 0})
 _=unused
 started,release:=make(chan struct{}),make(chan struct{})
 cell:=_lazyNew(func() int {calls.Add(1);close(started);<-release;return 42})
 var wg sync.WaitGroup
 for range 64 {wg.Add(1);go func(){defer wg.Done();if cell.get()!=42{t.Error("wrong value")}}()}
 <-started
 close(release)
 wg.Wait()
 if calls.Load()!=1{t.Fatal(calls.Load())}
 if cell.get()!=42 || calls.Load()!=1{t.Fatal("recomputed")}
}
func TestConcurrentPanicAndRecursiveAccess(t *testing.T) {
 var calls atomic.Int32
 marker:=new(int)
 cell:=_lazyNew(func() int {calls.Add(1);panic(marker)})
 var wg sync.WaitGroup
 for range 64 {wg.Add(1);go func(){defer wg.Done();if panicOf(func(){cell.get()})!=marker{t.Error("lost panic")}}()}
 wg.Wait()
 if calls.Load()!=1{t.Fatal(calls.Load())}
 var recursive *_lazyCell[int]
 recursive=_lazyNew(func() int{return recursive.get()})
 for range 2{
  if got:=panicOf(func(){recursive.get()});got!="bork: recursive lazy initializer"{t.Fatal(got)}
 }
 var first,second *_lazyCell[int]
 first=_lazyNew(func() int{return second.get()})
 second=_lazyNew(func() int{return first.get()})
 if got:=panicOf(func(){first.get()});got!="bork: recursive lazy initializer"{t.Fatal(got)}
 if got:=panicOf(func(){second.get()});got!="bork: recursive lazy initializer"{t.Fatal(got)}
}
func TestGoexitPublishesFailure(t *testing.T) {
 cell:=_lazyNew(func() int{runtime.Goexit();return 0})
 exited:=make(chan struct{})
 go func(){defer close(exited);cell.get()}()
 select{case <-exited:case <-time.After(time.Second):t.Fatal("first reader stranded")}
 got:=panicOf(func(){cell.get()})
 if got!="bork: lazy initializer exited without a value"{t.Fatal(got)}
}
func TestCompileTimeNeverRunsInitializer(t *testing.T) {
 calls:=0
 cell:=_lazyNew(func()int{calls++;return 1})
 _lazyCompileTime=true
 defer func(){_lazyCompileTime=false}()
 if panicOf(func(){cell.get()})!="bork: compile-time evaluation cannot force a runtime lazy cell"{t.Fatal("force allowed")}
 if calls!=0{t.Fatal("initializer ran")}
 known:=_lazyConstNew(func()int{calls++;return 2})
 if known.get()!=2||known.get()!=2||calls!=1{t.Fatal("known pure candidate did not memoize")}

}
`
