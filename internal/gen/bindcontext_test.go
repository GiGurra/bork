package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBindingContextRuntime(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs a Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":          "module bindingcontext\n\ngo 1.26\n",
		"context.go":      bindContextRuntime,
		"context_test.go": bindingContextRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "-race", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("binding context runtime: %v\n%s", err, out)
	}
}

const bindingContextRuntimeTest = `package main
import (
 "context"
 "errors"
 "sync"
 "sync/atomic"
 "testing"
 "time"
)

type _Owner struct{}
type _Scope struct { ctx context.Context; clean []func() }
func (s *_Scope) Own(closeFn func()) *_Owner { s.clean=append(s.clean,closeFn); return &_Owner{} }
func (s *_Scope) close() { for _,close := range s.clean { close() } }

func TestScopeUnion(t *testing.T) {
 type key struct{}
 early := time.Now().Add(time.Hour)
 late := early.Add(time.Hour)
 a,ca := context.WithDeadline(context.WithValue(context.Background(),key{},"original"),early)
 defer ca()
 b,cb := context.WithDeadline(context.Background(),late)
 defer cb()
 sa,sb := &_Scope{ctx:a},&_Scope{ctx:b}
 g := _bindNewContextGroup(sa)
 success:=false
 defer g.Finish(&success)
 ctx:=g.Context(sa)
 g.Attach(sb)
 g.Start()
 if got:=ctx.Value(key{}); got!="original" { t.Fatalf("value: %v",got) }
 if d,ok:=ctx.Deadline(); !ok || !d.Equal(early) { t.Fatalf("deadline: %v %t",d,ok) }
 ca()
 if ctx.Err()!=nil { t.Fatal("one ended scope cancelled union") }
 if d,ok:=ctx.Deadline(); !ok || !d.Equal(late) { t.Fatalf("live deadline: %v %t",d,ok) }
 cb()
 if ctx.Err()==nil { t.Fatal("all scopes ended without cancellation") }
 g.Attach(&_Scope{ctx:context.Background()})
 if ctx.Err()==nil { t.Fatal("terminal cancellation revived") }
}

func TestFixedContext(t *testing.T) {
 type key struct{}
 fixed, cancel := context.WithCancelCause(context.WithValue(context.Background(),key{},"external"))
 defer cancel(context.Canceled)
 g := _bindNewContextGroup(&_Scope{ctx:context.Background()})
 success:=false
 defer g.Finish(&success)
 ctx:=g.External(fixed)
 g.Start()
 g.Attach(&_Scope{ctx:context.Background()})
 if ctx.Value(key{})!="external" { t.Fatal("external value lost") }
 cause:=errors.New("external limit")
 cancel(cause)
 if ctx.Err()==nil || context.Cause(ctx)!=cause { t.Fatalf("fixed cause: %v / %v",ctx.Err(),context.Cause(ctx)) }
}

func TestResourceOwnershipAndFailure(t *testing.T) {
 for _,success:=range []bool{true,false} {
  s:=&_Scope{ctx:context.Background()}
  g:=_bindNewContextGroup(s)
  ctx:=g.Context(s)
  g.Start()
  var closes atomic.Int32
  value:=new(int)
  g.Register("a",value,func(){closes.Add(1)})
  g.Register("b",value,func(){closes.Add(1)})
  if g.Own("a",s)!=g.Own("b",s) { t.Fatal("duplicate result got independent close owners") }
  g.Finish(&success)
  want:=int32(0)
  if !success { want=1 }
  if closes.Load()!=want { t.Fatalf("immediate closes: %d",closes.Load()) }
  s.close()
  if closes.Load()!=1 || ctx.Err()==nil { t.Fatalf("final release: closes=%d err=%v",closes.Load(),ctx.Err()) }
 }
 // Successful None / an unconverted raw value must still release its resource.
 s:=&_Scope{ctx:context.Background()}
 g:=_bindNewContextGroup(s)
 count:=0
 g.Register("unowned",new(int),func(){count++})
 success:=true
 g.Finish(&success)
 if count!=1 || g.ctx.Err()==nil { t.Fatal("unowned raw result leaked") }
}

func TestConcurrentAttachment(t *testing.T) {
 root,cancel:=context.WithCancel(context.Background())
 defer cancel()
 g:=_bindNewContextGroup(&_Scope{ctx:root})
 ctx:=g.Context(&_Scope{ctx:root})
 g.Start()
 var wait sync.WaitGroup
 for i:=0;i<40;i++ {
  wait.Add(1)
  go func(){ defer wait.Done(); source,stop:=context.WithCancel(context.Background()); g.Attach(&_Scope{ctx:source}); stop(); _,_=ctx.Deadline(); _=ctx.Err(); _=ctx.Done() }()
 }
 wait.Wait()
 if ctx.Err()!=nil { t.Fatal("live opening scope cancelled") }
 cancel()
 if ctx.Err()==nil { t.Fatal("union did not cancel after final live scope") }
 success:=false
 g.Finish(&success)
}
`
