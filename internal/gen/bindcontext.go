package gen

import (
	"fmt"
	"github.com/GiGurra/bork/internal/check"
	"go/types"
)

// Generated binding contexts retain the union of scope lifetimes, bounded by
// explicitly passed contexts. Raw result resources close on failed conversion.
const bindContextRuntime = `package main

import (
 "context"
 "reflect"
 "sync"
 "time"
)

// Resources from one call share cancellation, but keep separate close owners.
// Scope sources form a union; explicit contexts are fixed external limits.
type _bindContextGroup struct {
 mu sync.Mutex
 ready bool
 ctx context.Context
 cancel context.CancelCauseFunc
 scopes map[*_Scope]bool
 fixed []context.Context
 stops []func() bool
 paths map[string]*_bindContextResource
 identities map[any]*_bindContextResource
 refs int
}

type _bindContextResource struct {
 group *_bindContextGroup
 once sync.Once
 closeFn func()
 owner *_Owner
}

func _bindNewContextGroup(s *_Scope) *_bindContextGroup {
 ctx, cancel := context.WithCancelCause(context.Background())
 g := &_bindContextGroup{ctx:ctx, cancel:cancel, scopes:map[*_Scope]bool{}, paths:map[string]*_bindContextResource{}, identities:map[any]*_bindContextResource{}, refs:1}
 g.Attach(s)
 return g
}

func (g *_bindContextGroup) Attach(s *_Scope) {
 g.mu.Lock()
 if g.ctx.Err() != nil || g.scopes[s] { g.mu.Unlock(); return }
 g.scopes[s] = true
 g.stops = append(g.stops, context.AfterFunc(s.ctx, g.check))
 g.mu.Unlock()
 g.check()
}

func (g *_bindContextGroup) Start() {
 g.mu.Lock()
 g.ready = true
 g.mu.Unlock()
 g.check()
}

func (g *_bindContextGroup) check() {
 g.mu.Lock()
 defer g.mu.Unlock()
 if !g.ready || g.ctx.Err() != nil { return }
 for _, ctx := range g.fixed {
  if ctx.Err() != nil { g.cancel(context.Cause(ctx)); return }
 }
 cause := error(context.Canceled)
 for s := range g.scopes {
  if s.ctx.Err() == nil { return }
  cause = context.Cause(s.ctx)
 }
 g.cancel(cause)
}

func (g *_bindContextGroup) Context(s *_Scope) context.Context {
 g.Attach(s)
 return &_bindOwnedContext{group:g, values:s.ctx}
}

func (g *_bindContextGroup) External(ctx context.Context) context.Context {
 if ctx == nil { return nil }
 g.mu.Lock()
 if g.ctx.Err() == nil {
  g.fixed = append(g.fixed,ctx)
  g.stops = append(g.stops,context.AfterFunc(ctx,g.check))
 }
 g.mu.Unlock()
 g.check()
 return &_bindOwnedContext{group:g, values:ctx}
}

type _bindOwnedContext struct { group *_bindContextGroup; values context.Context }
func (c *_bindOwnedContext) Done() <-chan struct{} { c.group.check(); return c.group.ctx.Done() }
func (c *_bindOwnedContext) Err() error { c.group.check(); return c.group.ctx.Err() }
func (c *_bindOwnedContext) Value(key any) any {
 // Preserve this context's cancellation identity for context.Cause while
 // retaining the original argument's application values.
 if value := c.group.ctx.Value(key); value != nil { return value }
 return c.values.Value(key)
}
func (c *_bindOwnedContext) Deadline() (time.Time, bool) {
 c.group.mu.Lock()
 defer c.group.mu.Unlock()
 var deadline time.Time
 found := false
 add := func(ctx context.Context) {
  if d, ok := ctx.Deadline(); ok && (!found || d.Before(deadline)) { deadline, found = d, true }
 }
 for s := range c.group.scopes { if s.ctx.Err() == nil { add(s.ctx) } }
 for _, ctx := range c.group.fixed { add(ctx) }
 return deadline, found
}

func (g *_bindContextGroup) Register(path string, value any, closeFn func()) {
 g.mu.Lock()
 defer g.mu.Unlock()
 var resource *_bindContextResource
 comparable := reflect.TypeOf(value).Comparable()
 if comparable { resource = g.identities[value] }
 if resource == nil {
  resource = &_bindContextResource{group:g,closeFn:closeFn}
  g.refs++
  if comparable { g.identities[value] = resource }
 }
 g.paths[path] = resource
}

func (g *_bindContextGroup) Own(path string, s *_Scope) *_Owner {
 g.mu.Lock()
 defer g.mu.Unlock()
 resource := g.paths[path]
 if resource == nil { panic("bork: unregistered Go binding resource") }
 if resource.owner == nil { resource.owner = s.Own(resource.close) }
 return resource.owner
}

func (r *_bindContextResource) close() {
 r.once.Do(func() { defer r.group.release(); r.closeFn() })
}

func (g *_bindContextGroup) release() {
 g.mu.Lock()
 g.refs--
 var stops []func() bool
 if g.refs == 0 {
  g.cancel(context.Canceled)
  stops, g.stops = g.stops, nil
 }
 g.mu.Unlock()
 for _, stop := range stops { stop() }
}

func (g *_bindContextGroup) Finish(success *bool) {
 defer g.release()
 g.mu.Lock()
 if !*success { g.cancel(context.Canceled) }
 resources := map[*_bindContextResource]bool{}
 for _, resource := range g.paths {
  if !*success || resource.owner == nil { resources[resource] = true }
 }
 g.mu.Unlock()
 for resource := range resources { resource.close() }
}
`

func isBindContext(t types.Type) bool {
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "context" && n.Obj().Name() == "Context"
}

// Register every raw resource before inspecting a Go error or converting any
// element, so failures close even resources conversion has not reached yet.
func (w *bindWriter) registerResources(x string, gt types.Type, t check.Type, path string) {
	if w.group == "" {
		return
	}
	if resource, ok := t.(*check.Resource); ok && resource.GoType != nil {
		if goNillable(gt) {
			w.line("if " + x + " != nil {")
		}
		w.line(fmt.Sprintf("%s.Register(%s, %s, func() { %s.Close() })", w.group, path, x, x))
		if goNillable(gt) {
			w.line("}")
		}
		return
	}
	if check.IsOption(t) {
		elem := check.TypeArgs(t)[0]
		if check.GoTypeOf(elem) != nil {
			w.registerResources(x, gt, elem, path)
			return
		}
		if pointer, ok := gt.Underlying().(*types.Pointer); ok {
			w.line("if " + x + " != nil {")
			w.registerResources("*("+x+")", pointer.Elem(), elem, path)
			w.line("}")
		}
		return
	}
	switch u := gt.Underlying().(type) {
	case *types.Pointer:
		w.line("if " + x + " != nil {")
		w.registerResources("*("+x+")", u.Elem(), t, path)
		w.line("}")
	case *types.Slice:
		if list, ok := t.(*check.List); ok {
			i, value := w.newTmp(), w.newTmp()
			w.line(fmt.Sprintf("for %s, %s := range %s {", i, value, x))
			w.registerResources(value, u.Elem(), list.Elem, fmt.Sprintf("_bindIndex(%s, %s)", path, i))
			w.line("}")
		}
	case *types.Array:
		if list, ok := t.(*check.List); ok {
			i, value := w.newTmp(), w.newTmp()
			w.line(fmt.Sprintf("for %s, %s := range %s {", i, value, x))
			w.registerResources(value, u.Elem(), list.Elem, fmt.Sprintf("_bindIndex(%s, %s)", path, i))
			w.line("}")
		}
	case *types.Map:
		if m, ok := t.(*check.Map); ok {
			key, value := w.newTmp(), w.newTmp()
			w.line(fmt.Sprintf("for %s, %s := range %s {", key, value, x))
			w.registerResources(value, u.Elem(), m.Value, fmt.Sprintf("_bindKey(%s, %s)", path, key))
			w.line("}")
		}
	}
}
