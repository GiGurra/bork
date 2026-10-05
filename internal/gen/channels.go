package gen

// channelRuntime implements bork channels: a lock per channel, a ring
// buffer (fixed, or growing for unbounded channels), and FIFO queues of
// waiting senders and receivers. A send or receive is a select of one arm.
// A waiting select is completed exactly once, by whichever party first
// claims it: a channel operation, a close, or a cancelled scope.
const channelRuntime = `package main

import (
 "cmp"
 "context"
 "math/rand/v2"
 "slices"
 "sync"
 "sync/atomic"
)

// The outcomes of a channel operation.
const (
 _borkChanValue = iota // a value received, or a send done
 _borkChanClosed
 _borkChanCancelled
 _borkChanNotReady // a non-blocking select found nothing ready
)

var _borkChanIDs atomic.Uint64

type _borkChan struct {
 mu sync.Mutex
 // id orders the locks a select takes.
 id uint64
 // limit is the capacity; -1 is unbounded.
 limit int
 ring []any
 head, n int
 closed bool
 owner context.Context
 recvq, sendq []*_borkWaiter
 // onClose runs once, when the channel closes (stopping a timer, say).
 onClose func()
}

// _borkSel is a waiting select. Whoever claims it first completes it.
type _borkSel struct {
 claimed atomic.Bool
 // ctxs are the scopes whose cancellation stops the select.
 ctxs []context.Context
 wake chan struct{}
 arm int
 status int
 value any
}

func (sel *_borkSel) claim() bool { return sel.claimed.CompareAndSwap(false, true) }

// complete is called by the party that claimed sel.
func (sel *_borkSel) complete(arm, status int, value any) {
 sel.arm, sel.status, sel.value = arm, status, value
 sel.wake <- struct{}{}
}

type _borkWaiter struct {
 sel *_borkSel
 arm int
 // value is what a waiting sender sends.
 value any
}

func _borkNewChan(owner *_Scope, limit int) *_borkChan {
 return &_borkChan{id: _borkChanIDs.Add(1), limit: limit, owner: owner.ctx}
}

// push buffers v. The buffer grows as needed, up to a fixed capacity, so a
// large capacity costs nothing until it is used.
func (c *_borkChan) push(v any) {
 if c.n == len(c.ring) {
  size := max(8, 2*len(c.ring))
  if c.limit > 0 {
   size = min(size, c.limit)
  }
  c.resize(size)
 }
 c.ring[(c.head+c.n)%len(c.ring)] = v
 c.n++
}

func (c *_borkChan) pop() any {
 v := c.ring[c.head]
 c.ring[c.head] = nil
 c.head = (c.head + 1) % len(c.ring)
 c.n--
 // An unbounded buffer shrinks again after a burst.
 if c.limit < 0 && len(c.ring) > 16 && c.n <= len(c.ring)/4 {
  c.resize(len(c.ring) / 2)
 }
 return v
}

func (c *_borkChan) resize(size int) {
 ring := make([]any, size)
 for i := 0; i < c.n; i++ {
  ring[i] = c.ring[(c.head+i)%len(c.ring)]
 }
 c.ring, c.head = ring, 0
}

// dequeue gives the first waiter in q whose select it could claim,
// dropping those that another channel or a cancellation completed. A
// waiter whose scope is cancelled already gets Cancelled, even before
// the cancellation's own callback has run: cancellation wins.
func _borkDequeue(q *[]*_borkWaiter) *_borkWaiter {
 for len(*q) > 0 {
  w := (*q)[0]
  (*q)[0] = nil
  *q = (*q)[1:]
  if w.sel.claim() && !w.sel.cancelled() {
   return w
  }
 }
 return nil
}

// cancelled completes a claimed select with Cancelled if one of its
// scopes is cancelled, and reports whether it did.
func (sel *_borkSel) cancelled() bool {
 if ctx := _borkCancelledIn(sel.ctxs); ctx != nil {
  sel.complete(-1, _borkChanCancelled, _borkCancelledBy(ctx))
  return true
 }
 return false
}

// tryRecv receives without waiting, with c locked.
func (c *_borkChan) tryRecv() (int, any, bool) {
 if c.n > 0 {
  v := c.pop()
  // A slot is free: the first waiting sender fills it.
  if w := _borkDequeue(&c.sendq); w != nil {
   c.push(w.value)
   w.sel.complete(w.arm, _borkChanValue, nil)
  }
  return _borkChanValue, v, true
 }
 if w := _borkDequeue(&c.sendq); w != nil {
  w.sel.complete(w.arm, _borkChanValue, nil)
  return _borkChanValue, w.value, true
 }
 if c.closed {
  return _borkChanClosed, nil, true
 }
 return 0, nil, false
}

// trySend sends without waiting, with c locked.
func (c *_borkChan) trySend(v any) (int, bool) {
 if c.closed {
  return _borkChanClosed, true
 }
 if w := _borkDequeue(&c.recvq); w != nil {
  w.sel.complete(w.arm, _borkChanValue, v)
  return _borkChanValue, true
 }
 if c.limit < 0 || c.n < c.limit {
  c.push(v)
  return _borkChanValue, true
 }
 return 0, false
}

// close makes every later send give Closed, and receives give what is
// buffered, then Closed. Waiting receivers have nothing buffered (a
// value sent while one waits goes to it), so all waiters get Closed.
func (c *_borkChan) close() {
 c.mu.Lock()
 if c.closed {
  c.mu.Unlock()
  return
 }
 c.closed = true
 for _, q := range [][]*_borkWaiter{c.recvq, c.sendq} {
  for _, w := range q {
   if w.sel.claim() && !w.sel.cancelled() {
    w.sel.complete(w.arm, _borkChanClosed, nil)
   }
  }
 }
 c.recvq, c.sendq = nil, nil
 onClose := c.onClose
 c.onClose = nil
 c.mu.Unlock()
 if onClose != nil {
  onClose()
 }
}

// release closes the channel when its scope closes, and drops what is
// still buffered: no one can receive it any more.
func (c *_borkChan) release() {
 c.close()
 c.mu.Lock()
 c.ring, c.head, c.n = nil, 0, 0
 c.mu.Unlock()
}

// offer sends v if that does not wait, and reports whether it did. Timers
// use it, dropping a tick that finds the buffer full.
func (c *_borkChan) offer(v any) bool {
 c.mu.Lock()
 defer c.mu.Unlock()
 status, ok := c.trySend(v)
 return ok && status == _borkChanValue
}

func (c *_borkChan) length() int {
 c.mu.Lock()
 defer c.mu.Unlock()
 return c.n
}

// _borkChanArm is one operation of a select: a send of value, or a
// receive, waiting in scope (nil for one that does not wait).
type _borkChanArm struct {
 ch *_borkChan
 send bool
 value any
 scope context.Context
}

// _borkCancelledIn gives the first of ctxs that is cancelled, or nil.
func _borkCancelledIn(ctxs []context.Context) context.Context {
 for _, ctx := range ctxs {
  if ctx.Err() != nil {
   return ctx
  }
 }
 return nil
}

func _borkCancelledBy(ctx context.Context) Cancelled {
 return Cancelled{reason: context.Cause(ctx).Error()}
}

// _borkChanSelect completes exactly one of arms, chosen at random among
// those ready, waiting until one is unless block is false. It gives the
// arm, an outcome, and the value received (or the Cancelled). A cancelled
// scope of an arm, or of a channel's owner, completes no operation: the
// select gives Cancelled, even when an operation is ready.
func _borkChanSelect(arms []_borkChanArm, block bool) (int, int, any) {
 var ctxs []context.Context
 add := func(ctx context.Context) {
  if ctx != nil && !slices.Contains(ctxs, ctx) {
   ctxs = append(ctxs, ctx)
  }
 }
 // The arms' scopes come first, so the reason is the caller's.
 for _, a := range arms {
  add(a.scope)
 }
 for _, a := range arms {
  if a.scope != nil {
   add(a.ch.owner)
  }
 }
 chans := make([]*_borkChan, 0, len(arms))
 for _, a := range arms {
  if !slices.Contains(chans, a.ch) {
   chans = append(chans, a.ch)
  }
 }
 slices.SortFunc(chans, func(a, b *_borkChan) int { return cmp.Compare(a.id, b.id) })
 for _, c := range chans {
  c.mu.Lock()
 }
 unlock := func() {
  for _, c := range chans {
   c.mu.Unlock()
  }
 }
 if ctx := _borkCancelledIn(ctxs); ctx != nil {
  unlock()
  return -1, _borkChanCancelled, _borkCancelledBy(ctx)
 }
 order := []int{0}
 if len(arms) > 1 {
  order = rand.Perm(len(arms))
 }
 for _, i := range order {
  a := arms[i]
  if a.send {
   if status, ok := a.ch.trySend(a.value); ok {
    unlock()
    return i, status, nil
   }
  } else if status, v, ok := a.ch.tryRecv(); ok {
   unlock()
   return i, status, v
  }
 }
 if !block {
  unlock()
  return -1, _borkChanNotReady, nil
 }
 sel := &_borkSel{wake: make(chan struct{}, 1), ctxs: ctxs}
 for i, a := range arms {
  w := &_borkWaiter{sel: sel, arm: i, value: a.value}
  if a.send {
   a.ch.sendq = append(a.ch.sendq, w)
  } else {
   a.ch.recvq = append(a.ch.recvq, w)
  }
 }
 unlock()
 stops := make([]func() bool, len(ctxs))
 for i, ctx := range ctxs {
  stops[i] = context.AfterFunc(ctx, func() {
   if sel.claim() {
    sel.complete(-1, _borkChanCancelled, _borkCancelledBy(ctx))
   }
  })
 }
 <-sel.wake
 for _, stop := range stops {
  stop()
 }
 // Leave no waiter behind in the queues of the channels that lost.
 for _, c := range chans {
  c.mu.Lock()
  mine := func(w *_borkWaiter) bool { return w.sel == sel }
  c.recvq = slices.DeleteFunc(c.recvq, mine)
  c.sendq = slices.DeleteFunc(c.sendq, mine)
  c.mu.Unlock()
 }
 return sel.arm, sel.status, sel.value
}

// _borkChanSend sends x on c, waiting in scope.
func _borkChanSend(scope *_Scope, c *_borkChan, x any) any {
 _, status, value := _borkChanSelect([]_borkChanArm{{ch: c, send: true, value: x, scope: scope.ctx}}, true)
 switch status {
 case _borkChanClosed:
  return Closed{}
 case _borkChanCancelled:
  return value
 }
 return _borkOk()
}

// _borkChanReceive receives from c, waiting in scope: the value, Closed,
// or Cancelled, and whether it is a value.
func _borkChanReceive(scope *_Scope, c *_borkChan) (any, bool) {
 _, status, value := _borkChanSelect([]_borkChanArm{{ch: c, scope: scope.ctx}}, true)
 switch status {
 case _borkChanClosed:
  return Closed{}, false
 case _borkChanCancelled:
  return value, false
 }
 return value, true
}
`
