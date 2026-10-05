package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMoveRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":       "module moveruntime\n\ngo 1.26\n",
		"scope.go":     scopeRuntime,
		"helpers.go":   scopeHelpers,
		"move_test.go": moveRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("move runtime: %v\n%s", err, out)
	}
}

const moveRuntimeTest = `package main
import (
 "context"
 "strings"
 "sync"
 "sync/atomic"
 "testing"
)

type conn struct { owner *_Owner; handle *_borkResourceHandle }
func (c conn) _ownerOf() *_Owner { return c.owner }
func (c conn) _borkRebind(s *_Scope) { c.handle._borkRebind(s) }
func (c conn) _borkUnbind(s *_Scope) { c.handle._borkUnbind(s) }

func open(s *_Scope, closes *atomic.Int32) conn {
 h := _borkNewResourceHandle(nil, s)
 return conn{owner: s.Own(func() { h.Close(); closes.Add(1) }), handle: h}
}

func mustPanic(t *testing.T, want string, f func()) {
 t.Helper()
 defer func() {
  r := recover()
  if r == nil || !strings.Contains(r.(string), want) { t.Fatalf("panic %v, want %q", r, want) }
 }()
 f()
}

func TestMoveTransfersOwnershipAndCancellation(t *testing.T) {
 var closes atomic.Int32
 from, to := _scopeWith(context.Background()), _scopeWith(context.Background())
 c := _moveResource(open(from, &closes), from, to)
 from.close()
 if closes.Load() != 0 || c.handle.Context().Err() != nil { t.Fatal("the source closed or cancelled a moved resource") }
 to.close()
 if closes.Load() != 1 || c.handle.Context().Err() == nil { t.Fatal("the target did not close it") }
}

func TestFailedMovesChangeNothing(t *testing.T) {
 var closes atomic.Int32
 from, other := _scopeWith(context.Background()), _scopeWith(context.Background())
 c := open(from, &closes)
 mustPanic(t, "not owned by the scope it is moved from", func() { _moveResource(c, other, other) })
 done := _scopeWith(context.Background())
 done.close()
 mustPanic(t, "the target scope has finished closing", func() { _moveResource(c, from, done) })
 other.close()
 if closes.Load() != 0 { t.Fatal("closed by a failed move") }
 from.close()
 if closes.Load() != 1 { t.Fatal("the source no longer closes it") }
 mustPanic(t, "already closed", func() { _moveResource(c, from, other) })
}

func TestMoveAfterAttach(t *testing.T) {
 var closes atomic.Int32
 from, app, to := _scopeWith(context.Background()), _scopeWith(context.Background()), _scopeWith(context.Background())
 c := open(from, &closes)
 if why := c.owner.attach(app); why != "" { t.Fatal(why) }
 c.handle._borkRebind(app)
 _moveResource(c, from, to)
 from.close()
 to.close()
 if closes.Load() != 0 || c.handle.Context().Err() != nil { t.Fatal("closed while app owns it") }
 app.close()
 if closes.Load() != 1 { t.Fatal("not closed with its last owner") }
}

func TestMovesOutOfALongLivedScopeDoNotGrowIt(t *testing.T) {
 var closes atomic.Int32
 app := _scopeWith(context.Background())
 for i := 0; i < 1000; i++ {
  to := _scopeWith(context.Background())
  _moveResource(open(app, &closes), app, to)
  to.close()
 }
 app.mu.Lock()
 n := len(app.finalizers)
 app.mu.Unlock()
 if n > 100 { t.Fatalf("%d finalizers left in the source", n) }
 app.close()
 if closes.Load() != 1000 { t.Fatalf("closed %d", closes.Load()) }
}

func TestMoveRacesTheSourceClosing(t *testing.T) {
 for i := 0; i < 200; i++ {
  var closes atomic.Int32
  from, to := _scopeWith(context.Background()), _scopeWith(context.Background())
  c := open(from, &closes)
  var wg sync.WaitGroup
  wg.Add(2)
  go func() { defer wg.Done(); from.close() }()
  go func() {
   defer wg.Done()
   defer func() { recover() }()
   _moveResource(c, from, to)
  }()
  wg.Wait()
  to.close()
  if closes.Load() != 1 { t.Fatalf("closed %d times", closes.Load()) }
 }
}

func TestAttachWhileTheScopeWaitsForItsTasks(t *testing.T) {
 var closes atomic.Int32
 app, s := _scopeWith(context.Background()), _scopeWith(context.Background())
 c := open(app, &closes)
 started := make(chan struct{})
 s.Go(func() any {
  close(started)
  <-s.ctx.Done()
  if why := c.owner.attach(s); why != "" { panic(why) }
  return nil
 })
 <-started
 s.close() // the task attaches while s waits for it, and s releases it
 if closes.Load() != 0 { t.Fatal("closed while app keeps it") }
 app.close()
 if closes.Load() != 1 { t.Fatal("not closed") }
}

func TestMoveWithinOneScopeKeepsItsCancellation(t *testing.T) {
 var closes atomic.Int32
 s, w := _scopeWith(context.Background()), _scopeWith(context.Background())
 c := open(s, &closes)
 _moveResource(c, s, s)
 if c.handle.Context().Err() != nil { t.Fatal("cancelled by a move to its own scope") }
 // Attached to s twice: moving one registration keeps s a source.
 if why := c.owner.attach(s); why != "" { t.Fatal(why) }
 _moveResource(c, s, w)
 w.close()
 if closes.Load() != 0 || c.handle.Context().Err() != nil { t.Fatal("cancelled or closed while s keeps it") }
 s.close()
 if closes.Load() != 1 { t.Fatal("not closed") }
}
`
