package bindtest

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type Payload struct{ Value string }

func (p *Payload) Text() string { return p.Value }

type PayloadAlias = Payload

func NewPayload(s string) *Payload    { return &Payload{Value: s} }
func NilPayload() *Payload            { return nil }
func PayloadError() (*Payload, error) { return nil, nil }
func PayloadOption(ok bool) *Payload {
	if !ok {
		return nil
	}
	return NewPayload("some")
}
func Payloads() []*Payload { return []*Payload{NewPayload("one"), nil} }
func Stringer(ok bool) fmt.Stringer {
	if !ok {
		return nil
	}
	return time.Second
}
func Duration() time.Duration                 { return time.Second }
func ContextPresent(ctx context.Context) bool { return ctx != nil && ctx.Err() == nil }

type Handle struct{ closed bool }

var closes int

func OpenHandle() *Handle { return &Handle{} }
func (h *Handle) Close() error {
	if !h.closed {
		h.closed = true
		closes++
	}
	return errors.New("ignored close error")
}
func (h *Handle) Closed() bool { return h.closed }
func CloseCount() int          { return closes }
func ContextHandle(ctx context.Context) (*Handle, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return OpenHandle(), nil
}
func NilHandle() (*Handle, error) { return nil, nil }

func StringerError() (fmt.Stringer, error) { return nil, nil }
func NullablePayload(p *Payload) string {
	if p == nil {
		return "none"
	}
	return p.Text()
}
func NullableStringer(s fmt.Stringer) string {
	if s == nil {
		return "none"
	}
	return s.String()
}

type View struct{ handle *Handle }

func NewView(h *Handle) *View { return &View{handle: h} }
func (v *View) Closed() bool  { return v.handle.Closed() }

var keptContext context.Context

func KeepContext(ctx context.Context) { keptContext = ctx }
func ContextWasCancelled() bool       { return keptContext.Err() != nil }

func ContextsHandle(contexts []context.Context) *Handle { return OpenHandle() }

type Formatter struct{}

func (*Formatter) String() string    { return "typed nil" }
func TypedNilStringer() fmt.Stringer { return (*Formatter)(nil) }
