package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestChannelRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"go.mod":           "module chanrt\n\ngo 1.26\n",
		"scope.go":         scopeRuntime,
		"channels.go":      channelRuntime,
		"channels_test.go": channelRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("channel runtime: %v\n%s", err, out)
	}
}

const channelRuntimeTest = `package main

import (
 "context"
 "errors"
 "runtime"
 "sync"
 "sync/atomic"
 "testing"
 "time"
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

func scope() *_Scope { return _scopeWith(context.Background()) }

func TestBufferedOrderAndClose(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, 3)
 for i := 0; i < 3; i++ {
  if v := _borkChanSend(s, c, i); v != _borkOk() { t.Fatal("send", v) }
 }
 c.close()
 if v := _borkChanSend(s, c, 9); v != (Closed{}) { t.Fatal("send after close", v) }
 for i := 0; i < 3; i++ {
  if v, ok := _borkChanReceive(s, c); !ok || v != i { t.Fatal("order", v) }
 }
 for i := 0; i < 2; i++ {
  if v, ok := _borkChanReceive(s, c); ok || v != (Closed{}) { t.Fatal("closed is final", v) }
 }
 c.close()
}

func TestUnbufferedHandOff(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, 0)
 sent := make(chan any)
 go func() { sent <- _borkChanSend(s, c, "x") }()
 if v, ok := _borkChanReceive(s, c); !ok || v != "x" { t.Fatal(v) }
 if v := <-sent; v != _borkOk() { t.Fatal(v) }
 if _, status, _ := _borkChanSelect([]_borkChanArm{{ch: c, send: true, value: 1}}, false); status != _borkChanNotReady {
  t.Fatal("unbuffered send with no receiver is not ready")
 }
}

// A sender blocked when the channel closes gets Closed, and its value is
// never received: Closed stays final.
func TestCloseReleasesBlockedSenders(t *testing.T) {
 for round := 0; round < 200; round++ {
  s := scope()
  c := _borkNewChan(s, 1)
  _borkChanSend(s, c, 0)
  results := make(chan any, 4)
  for i := 1; i <= 4; i++ {
   go func() { results <- _borkChanSend(s, c, i) }()
  }
  for c.waiting() < 4 { runtime.Gosched() }
  c.close()
  oks := 0
  for i := 0; i < 4; i++ {
   if v := <-results; v == _borkOk() { oks++ } else if v != (Closed{}) { t.Fatal(v) }
  }
  got := 0
  for {
   if _, ok := _borkChanReceive(s, c); !ok { break }
   got++
  }
  if oks != 0 || got != 1 { t.Fatal("blocked senders delivered after close", oks, got) }
  s.close()
 }
}

func (c *_borkChan) waiting() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.sendq) + len(c.recvq) }

// The scope an operation waits in cancels it, even when the channel
// belongs to an outer scope.
func TestCallerScopeCancels(t *testing.T) {
 outer := scope(); defer outer.close()
 c := _borkNewChan(outer, 0)
 inner := _scopeWith(outer.ctx)
 done := make(chan any)
 go func() { v, _ := _borkChanReceive(inner, c); done <- v }()
 for c.waiting() < 1 { runtime.Gosched() }
 inner.cancel(errors.New("stop"))
 select {
 case v := <-done:
  if v != (Cancelled{reason: "stop"}) { t.Fatal(v) }
 case <-time.After(5 * time.Second):
  t.Fatal("receive ignored its scope")
 }
 if c.waiting() != 0 { t.Fatal("waiter left behind") }
 inner.close()
}

func TestCancellationWinsOverReadyData(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, 1)
 _borkChanSend(s, c, 1)
 op := _scopeWith(s.ctx)
 op.cancel(errors.New("stop"))
 for i := 0; i < 50; i++ {
  if v, ok := _borkChanReceive(op, c); ok || v != (Cancelled{reason: "stop"}) { t.Fatal(v) }
 }
 if c.length() != 1 { t.Fatal("a cancelled receive took a value") }
 owner := scope()
 d := _borkNewChan(owner, 1)
 owner.cancel(errors.New("owner gone"))
 if v, _ := _borkChanReceive(s, d); v != (Cancelled{reason: "owner gone"}) { t.Fatal(v) }
}

func TestUnboundedGrowsAndShrinks(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, -1)
 for i := 0; i < 10000; i++ {
  if v := _borkChanSend(s, c, i); v != _borkOk() { t.Fatal(v) }
 }
 for i := 0; i < 10000; i++ {
  if v, _ := _borkChanReceive(s, c); v != i { t.Fatal("order", i, v) }
 }
 if len(c.ring) > 16 { t.Fatal("did not shrink", len(c.ring)) }
}

// Under contention every value is received exactly once, and a select
// completes exactly one of its arms.
func TestSelectExactlyOnce(t *testing.T) {
 s := scope(); defer s.close()
 a, b := _borkNewChan(s, 0), _borkNewChan(s, 4)
 const n = 2000
 var wg sync.WaitGroup
 for _, c := range []*_borkChan{a, b} {
  wg.Add(1)
  go func() { defer wg.Done(); for i := 0; i < n; i++ { _borkChanSend(s, c, i) } }()
 }
 counts := map[*_borkChan]map[any]int{a: {}, b: {}}
 for i := 0; i < 2*n; i++ {
  arm, status, v := _borkChanSelect([]_borkChanArm{{ch: a, scope: s.ctx}, {ch: b, scope: s.ctx}}, true)
  if status != _borkChanValue { t.Fatal(status) }
  counts[[]*_borkChan{a, b}[arm]][v]++
 }
 wg.Wait()
 for _, m := range counts {
  if len(m) != n { t.Fatal("lost values", len(m)) }
  for _, k := range m { if k != 1 { t.Fatal("duplicated value") } }
 }
 if a.waiting()+b.waiting() != 0 { t.Fatal("waiters left behind") }
}

func TestSelectSendAndReceiveRace(t *testing.T) {
 s := scope(); defer s.close()
 in, out := _borkNewChan(s, 0), _borkNewChan(s, 0)
 var received, sent atomic.Int64
 var wg sync.WaitGroup
 wg.Add(2)
 go func() { defer wg.Done(); for i := 0; i < 1000; i++ { _borkChanSend(s, in, i) } }()
 go func() { defer wg.Done(); for i := 0; i < 1000; i++ { _borkChanReceive(s, out) } }()
 for received.Load() < 1000 || sent.Load() < 1000 {
  var arms []_borkChanArm
  if received.Load() < 1000 { arms = append(arms, _borkChanArm{ch: in, scope: s.ctx}) }
  if sent.Load() < 1000 { arms = append(arms, _borkChanArm{ch: out, send: true, value: 0, scope: s.ctx}) }
  arm, _, _ := _borkChanSelect(arms, true)
  if arms[arm].send { sent.Add(1) } else { received.Add(1) }
 }
 wg.Wait()
}

func TestSelectIsFair(t *testing.T) {
 s := scope(); defer s.close()
 a, b := _borkNewChan(s, -1), _borkNewChan(s, -1)
 wins := [2]int{}
 for i := 0; i < 2000; i++ {
  _borkChanSend(s, a, 1); _borkChanSend(s, b, 2)
  arm, _, _ := _borkChanSelect([]_borkChanArm{{ch: a, scope: s.ctx}, {ch: b, scope: s.ctx}}, true)
  wins[arm]++
 }
 if wins[0] < 800 || wins[1] < 800 { t.Fatal("unfair", wins) }
}

func TestScopeEndReleasesWaiters(t *testing.T) {
 before := runtime.NumGoroutine()
 s := scope()
 c := _borkNewChan(s, 0)
 owner := s.Own(c.release)
 _ = owner
 for i := 0; i < 10; i++ { s.Go(func() any { v, _ := _borkChanReceive(s, c); return v }) }
 for c.waiting() < 10 { runtime.Gosched() }
 s.close()
 if v, _ := _borkChanReceive(scope(), c); v != (Cancelled{reason: "the scope ended"}) { t.Fatal(v) }
 if _, status, _ := _borkChanSelect([]_borkChanArm{{ch: c}}, false); status != _borkChanClosed { t.Fatal("not closed at scope end", status) }
 deadline := time.Now().Add(5 * time.Second)
 for runtime.NumGoroutine() > before && time.Now().Before(deadline) { time.Sleep(time.Millisecond) }
 if runtime.NumGoroutine() > before { t.Fatal("goroutines left", runtime.NumGoroutine(), before) }
}

func TestOfferDropsWhenFull(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, 1)
 if !c.offer(1) || c.offer(2) { t.Fatal("offer") }
 c.close()
 if c.offer(3) { t.Fatal("offer after close") }
}

// Once a waiting operation's scope is cancelled, it gives Cancelled, even
// if a value arrives before the cancellation's callback has run.
func TestCancellationWinsWhileWaiting(t *testing.T) {
 for round := 0; round < 500; round++ {
  s := scope()
  c := _borkNewChan(s, 0)
  inner := _scopeWith(s.ctx)
  got := make(chan any, 1)
  go func() { v, _ := _borkChanReceive(inner, c); got <- v }()
  for c.waiting() < 1 { runtime.Gosched() }
  inner.cancel(errors.New("stop"))
  _, status, _ := _borkChanSelect([]_borkChanArm{{ch: c, send: true, value: 1}}, false)
  if v := <-got; v != (Cancelled{reason: "stop"}) { t.Fatal("receiver got", v) }
  if status == _borkChanValue { t.Fatal("a cancelled receiver took the value") }
  d := _borkNewChan(s, 0)
  sent := make(chan any, 1)
  sender := _scopeWith(s.ctx)
  go func() { sent <- _borkChanSend(sender, d, 2) }()
  for d.waiting() < 1 { runtime.Gosched() }
  sender.cancel(errors.New("stop"))
  if _, status, _ := _borkChanSelect([]_borkChanArm{{ch: d}}, false); status == _borkChanValue { t.Fatal("took a cancelled sender's value") }
  d.close()
  if v := <-sent; v != (Cancelled{reason: "stop"}) { t.Fatal("sender got", v) }
  inner.close(); sender.close(); s.close()
 }
}

func TestFixedBufferGrowsLazily(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkNewChan(s, 1<<40)
 if len(c.ring) != 0 { t.Fatal("allocated up front") }
 for i := 0; i < 20; i++ { _borkChanSend(s, c, i) }
 if len(c.ring) > 32 { t.Fatal("grew too much", len(c.ring)) }
 d := _borkNewChan(s, 3)
 for i := 0; i < 3; i++ { _borkChanSend(s, d, i) }
 if len(d.ring) != 3 { t.Fatal("grew past its capacity", len(d.ring)) }
 if _, status, _ := _borkChanSelect([]_borkChanArm{{ch: d, send: true, value: 9}}, false); status != _borkChanNotReady { t.Fatal("full channel accepted a value") }
}

func TestAfterDeliversOnceThenCloses(t *testing.T) {
 s := scope(); defer s.close()
 c := _borkChanTimer(s, time.Millisecond, 0, func(time.Time) int { return 1 }).native.handle.(*_borkChan)
 if v, ok := _borkChanReceive(s, c); !ok || v != 1 { t.Fatal(v) }
 if v, _ := _borkChanReceive(s, c); v != (Closed{}) { t.Fatal("not closed after firing", v) }
 now := _borkChanTimer(s, -time.Second, 0, func(time.Time) int { return 2 }).native.handle.(*_borkChan)
 if v, _ := _borkChanReceive(s, now); v != 2 { t.Fatal("nonpositive delay", v) }
}

// A ticker stops when its channel is closed, or its scope ends, and
// drops ticks a receiver has not taken.
func TestTickerStops(t *testing.T) {
 var fired atomic.Int64
 count := func(time.Time) int { return int(fired.Add(1)) }
 s := scope()
 c := _borkChanTimer(s, time.Millisecond, time.Millisecond, count).native.handle.(*_borkChan)
 time.Sleep(20 * time.Millisecond)
 if c.length() != 1 { t.Fatal("ticks not dropped", c.length()) }
 c.close()
 after := fired.Load()
 time.Sleep(20 * time.Millisecond)
 if fired.Load() > after+1 { t.Fatal("ticker kept running after close") }
 d := _borkChanTimer(s, time.Millisecond, time.Millisecond, count).native.handle.(*_borkChan)
 _borkChanReceive(s, d)
 s.close()
 after = fired.Load()
 time.Sleep(20 * time.Millisecond)
 if fired.Load() > after+1 { t.Fatal("ticker kept running after its scope ended") }
}
`
