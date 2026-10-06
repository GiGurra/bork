package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestHandOverRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":          "module handoffruntime\n\ngo 1.26\n",
		"scope.go":        scopeRuntime,
		"helpers.go":      scopeHelpers,
		"channels.go":     channelRuntime,
		"handoff_test.go": handOverRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hand-over runtime: %v\n%s", err, out)
	}
}

const handOverRuntimeTest = `package main
import (
 "context"
 "runtime"
 "slices"
 "sync"
 "sync/atomic"
 "testing"
)

type Cancelled struct{ reason string }
type Closed struct{}
type _Ok struct{}
type ChannelHandle struct {
 handle any
 owner *_Owner
}
type Channel[T any] struct{ native ChannelHandle }

func _borkOk() _Ok { return _Ok{} }

type conn struct { owner *_Owner; handle *_borkResourceHandle }
func (c conn) _ownerOf() *_Owner { return c.owner }
func (c conn) _borkRebind(s *_Scope) { c.handle._borkRebind(s) }
func (c conn) _borkUnbind(s *_Scope) { c.handle._borkUnbind(s) }

func open(s *_Scope, closes *atomic.Int32) conn {
 h := _borkNewResourceHandle(nil, s)
 return conn{owner: s.Own(func() { h.Close(); closes.Add(1) }), handle: h}
}

func scope() *_Scope { return _scopeWith(context.Background()) }

// The handoff's scope owns what is handed over, and a receiver can move
// it on at once: the source closing does not close it.
func TestHandOverMovesToTheHandoffScope(t *testing.T) {
 var closes atomic.Int32
 app, src, session := scope(), scope(), scope()
 defer app.close()
 h := _borkNewChan(app, 1)
 c := open(src, &closes)
 if _, ok := _borkHandOver(src, h, c, src).(_Ok); !ok { t.Fatal("the hand-over failed") }
 src.close()
 if closes.Load() != 0 || c.handle.Context().Err() != nil { t.Fatal("the source closed or cancelled what it handed over") }
 got, ok := _borkChanReceive(app, h)
 if !ok { t.Fatalf("received %v", got) }
 moved := _moveResource(got.(conn), h.scope, session)
 session.close()
 if closes.Load() != 1 || moved.handle.Context().Err() == nil { t.Fatal("the receiver's session did not close it") }
}

// A failed hand-over releases the resource at once.
func TestFailedHandOverReleases(t *testing.T) {
 var closes atomic.Int32
 app, src := scope(), scope()
 defer app.close()
 defer src.close()
 h := _borkNewChan(app, 1)
 h.close()
 c := open(src, &closes)
 if _, ok := _borkHandOver(src, h, c, src).(Closed); !ok { t.Fatal("handed over to a closed handoff") }
 if closes.Load() != 1 || c.handle.Context().Err() == nil { t.Fatal("a failed hand-over did not release it") }
}

// A cancelled hand-over releases it too, unless another scope keeps it.
func TestCancelledHandOverKeepsAttached(t *testing.T) {
 var closes atomic.Int32
 app, src, other := scope(), scope(), scope()
 defer app.close()
 h := _borkNewChan(app, 0)
 c := open(src, &closes)
 if why := c.owner.attach(other); why != "" { t.Fatal(why) }
 c._borkRebind(other) // as attach does
 waiting := scope()
 waiting.cancel(context.Canceled)
 if _, ok := _borkHandOver(waiting, h, c, src).(_Ok); ok { t.Fatal("handed over in a cancelled scope") }
 src.close()
 if closes.Load() != 0 || c.handle.Context().Err() != nil { t.Fatal("released although attached elsewhere") }
 other.close()
 if closes.Load() != 1 { t.Fatal("not closed with the last scope") }
}

// What nobody received closes with the handoff's scope.
func TestUnreceivedClosesWithTheHandoff(t *testing.T) {
 var closes atomic.Int32
 app, src := scope(), scope()
 h := _borkNewChan(app, 1)
 _borkHandOver(src, h, open(src, &closes), src)
 src.close()
 if closes.Load() != 0 { t.Fatal("closed with its source") }
 app.close()
 if closes.Load() != 1 { t.Fatal("not closed with the handoff's scope") }
}

// A hand-over to a handoff whose scope has finished closing (by an
// orphaned task) gives Closed and releases the resource.
func TestHandOverAfterTheHandoffClosed(t *testing.T) {
 var closes atomic.Int32
 app, src := scope(), scope()
 defer src.close()
 h := _borkNewChan(app, 1)
 app.close()
 c := open(src, &closes)
 if _, ok := _borkHandOver(src, h, c, src).(Closed); !ok { t.Fatal("handed over to a closed scope") }
 if closes.Load() != 1 { t.Fatal("not released") }
}

// Receivers move what they receive on while senders wait on an
// unbuffered handoff: every resource closes exactly once, with the
// session that received it.
func TestConcurrentHandOvers(t *testing.T) {
 var opens, closes atomic.Int32
 app := scope()
 h := _borkNewChan(app, 0)
 var workers sync.WaitGroup
 for range 4 {
  workers.Add(1)
  go func() {
   defer workers.Done()
   for {
    got, ok := _borkChanReceive(app, h)
    if !ok { return }
    session := scope()
    _moveResource(got.(conn), h.scope, session)
    session.close()
   }
  }()
 }
 var senders sync.WaitGroup
 for range 4 {
  senders.Add(1)
  go func() {
   defer senders.Done()
   for range 500 {
    src := scope()
    opens.Add(1)
    _borkHandOver(src, h, open(src, &closes), src)
    src.close()
   }
  }()
 }
 senders.Wait()
 h.close()
 workers.Wait()
 app.close()
 if opens.Load() != closes.Load() { t.Fatalf("opened %d, closed %d", opens.Load(), closes.Load()) }
}

// owners gives the scopes that keep c open.
func owners(c conn) []*_Scope {
 c.owner.mu.Lock()
 defer c.owner.mu.Unlock()
 var out []*_Scope
 for _, r := range c.owner.regs { out = append(out, r.scope) }
 return out
}

func handOverArm(h *_borkChan, c conn, from, wait *_Scope) _borkChanArm {
 return _borkChanArm{ch: h, send: true, value: c, scope: wait.ctx, from: from}
}

// A select's hand-over arm moves its resource only if it is the arm
// completed. With receivers racing on an unbuffered and a buffered
// handoff, a channel arm and cancellations, every losing arm's resource
// stays its source's, open and uncancelled, and every resource closes
// exactly once.
func TestSelectHandOverMovesOnlyTheChosenArm(t *testing.T) {
 app := scope()
 handoffs := []*_borkChan{_borkNewChan(app, 0), _borkNewChan(app, 1)}
 feed := _borkNewChan(app, 0)
 var workers sync.WaitGroup
 for _, h := range handoffs {
  for range 2 {
   workers.Add(1)
   go func() {
    defer workers.Done()
    for {
     got, ok := _borkChanReceive(app, h)
     if !ok { return }
     if !slices.Contains(owners(got.(conn)), h.scope) { t.Error("received what the handoff does not own") }
     session := scope()
     _moveResource(got.(conn), h.scope, session)
     session.close()
    }
   }()
  }
 }
 workers.Add(1)
 go func() {
  defer workers.Done()
  for i := 0; ; i++ {
   if _, ok := _borkChanSend(app, feed, i).(_Ok); !ok { return }
  }
 }()
 var mu sync.Mutex
 var counts []*atomic.Int32
 var senders sync.WaitGroup
 for g := range 4 {
  senders.Add(1)
  go func() {
   defer senders.Done()
   for i := range 500 {
    src, wait := scope(), scope()
    var cs []conn
    for range handoffs {
     n := new(atomic.Int32)
     mu.Lock()
     counts = append(counts, n)
     mu.Unlock()
     cs = append(cs, open(src, n))
    }
    if (g+i)%3 == 0 { go wait.cancel(context.Canceled) }
    arms := []_borkChanArm{{ch: feed, scope: wait.ctx}}
    for k, h := range handoffs { arms = append(arms, handOverArm(h, cs[k], src, wait)) }
    arm, status, _ := _borkChanSelect(arms, true)
    for k, c := range cs {
     if arm == k+1 && status == _borkChanValue {
      if slices.Contains(owners(c), src) { t.Error("the chosen arm's resource is still its source's") }
     } else if !slices.Equal(owners(c), []*_Scope{src}) || c.handle.Context().Err() != nil {
      t.Errorf("a losing arm moved or cancelled its resource (arm %d won with %d)", arm, status)
     }
    }
    src.close()
    wait.close()
   }
  }()
 }
 senders.Wait()
 for _, h := range handoffs { h.close() }
 feed.close()
 workers.Wait()
 app.close()
 for _, n := range counts {
  if n.Load() != 1 { t.Fatalf("a resource closed %d times", n.Load()) }
 }
}

// A hand-over whose resource cannot move (its source does not own it)
// fails in its sender, whether it waited or not, and nobody receives it.
func TestSelectHandOverFailsInTheSender(t *testing.T) {
 var closes atomic.Int32
 app, src, other := scope(), scope(), scope()
 defer app.close()
 defer src.close()
 defer other.close()
 h := _borkNewChan(app, 0)
 c := open(src, &closes)
 done := make(chan int)
 go func() {
  _, status, _ := _borkChanSelect([]_borkChanArm{handOverArm(h, c, other, app)}, true)
  done <- status
 }()
 for waiting := 0; waiting == 0; runtime.Gosched() {
  h.mu.Lock()
  waiting = len(h.sendq)
  h.mu.Unlock()
 }
 if _, status, _ := _borkChanSelect([]_borkChanArm{{ch: h}}, false); status != _borkChanNotReady { t.Fatal("received what could not be handed over", status) }
 if status := <-done; status != _borkChanFailed { t.Fatal("the waiting sender did not fail", status) }
 b := _borkNewChan(app, 1)
 if _, status, why := _borkChanSelect([]_borkChanArm{handOverArm(b, c, other, app)}, false); status != _borkChanFailed || why == nil || b.length() != 0 { t.Fatal("the sender did not fail at once", status) }
 if !slices.Equal(owners(c), []*_Scope{src}) || closes.Load() != 0 { t.Fatal("a failed hand-over moved or closed it") }
}

// A hand-over that finds only receivers whose scopes are cancelled (before
// their cancellations claim them) moves nothing.
func TestSelectHandOverToCancelledReceivers(t *testing.T) {
 var closes atomic.Int32
 app, src, gone := scope(), scope(), scope()
 defer app.close()
 gone.cancel(context.Canceled)
 h := _borkNewChan(app, 0)
 h.recvq = append(h.recvq, &_borkWaiter{sel: &_borkSel{wake: make(chan struct{}, 1), ctxs: []context.Context{gone.ctx}}})
 c := open(src, &closes)
 if _, status, _ := _borkChanSelect([]_borkChanArm{handOverArm(h, c, src, app)}, false); status != _borkChanNotReady { t.Fatal(status) }
 if !slices.Equal(owners(c), []*_Scope{src}) || c.handle.Context().Err() != nil { t.Fatal("moved or cancelled, although nobody received it") }
 src.close()
 if closes.Load() != 1 { t.Fatal("not closed with its source") }
}
`
