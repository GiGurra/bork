package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResourceHandleRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a Go test")
	}
	dir := t.TempDir()
	for name, source := range map[string]string{
		"go.mod":         "module resourcehandle\n\ngo 1.26\n",
		"helpers.go":     scopeHelpers,
		"handle_test.go": resourceHandleRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("resource handle runtime: %v\n%s", err, out)
	}
}

const resourceHandleRuntimeTest = `package main
import (
 "context"
 "errors"
 "testing"
 "time"
)

type _task struct{}
func (*_task) Await() any { return nil }
type _Scope struct { ctx context.Context; cancel context.CancelCauseFunc; finalizerTimeout time.Duration }
func _scopeWith(ctx context.Context) *_Scope { c, cancel := context.WithCancelCause(ctx); return &_Scope{ctx: c, cancel: cancel} }
func (s *_Scope) close() { s.cancel(errors.New("the scope ended")) }
func (s *_Scope) abort() { s.close() }
func (s *_Scope) tryGo(func() any) *_task { return nil }

func waitLive(t *testing.T, h *_borkResourceHandle, n int) {
 t.Helper()
 for i := 0; i < 1000; i++ {
  h.mu.Lock()
  live := len(h.live)
  h.mu.Unlock()
  if live == n { return }
  time.Sleep(time.Millisecond)
 }
 t.Fatalf("live sources: want %d", n)
}

func TestAttachedScopesArePruned(t *testing.T) {
 app := _scopeWith(context.Background())
 h := _borkNewResourceHandle(nil, app)
 for i := 0; i < 100; i++ {
  short := _scopeWith(context.Background())
  h._borkRebind(short)
  short.close()
 }
 waitLive(t, h, 1)
 if h.Context().Err() != nil { t.Fatal("cancelled while app owns it") }
 app.cancel(errors.New("app cancelled"))
 if err := context.Cause(h.Context()); err == nil || err.Error() != "app cancelled" {
  t.Fatalf("cause: %v", err)
 }
}

func TestEveryOwnerMustBeCancelled(t *testing.T) {
 a, b := _scopeWith(context.Background()), _scopeWith(context.Background())
 h := _borkNewResourceHandle(nil, a)
 h._borkRebind(b)
 b.close()
 a.cancel(errors.New("a cancelled"))
 if err := context.Cause(h.Context()); err == nil || err.Error() != "a cancelled" {
  t.Fatalf("cause: %v", err)
 }
 // Terminal: attaching to a live scope does not revive it.
 c := _scopeWith(context.Background())
 h._borkRebind(c)
 if h.Context().Err() == nil { t.Fatal("revived") }
}

func TestCloseStopsWatching(t *testing.T) {
 s := _scopeWith(context.Background())
 h := _borkNewResourceHandle(nil, s)
 h.Close()
 if h.Context().Err() == nil { t.Fatal("not closed") }
 h.mu.Lock()
 live := len(h.live)
 h.mu.Unlock()
 if live != 0 { t.Fatalf("live sources after Close: %d", live) }
}
`
