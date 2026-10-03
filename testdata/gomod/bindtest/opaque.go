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

type MirrorItem struct {
	ID    int64
	Count int64
	Name  string
}
type MirrorEnvelope struct {
	Item     MirrorItem
	Items    []MirrorItem
	Optional *MirrorItem
}

func MirrorItems() MirrorEnvelope {
	return MirrorEnvelope{Item: MirrorItem{1, 3, "first"}, Items: []MirrorItem{{2, 300, "second"}, {3, -1, "third"}}, Optional: &MirrorItem{4, 2, "last"}}
}
func EchoMirror(v MirrorEnvelope) MirrorEnvelope { return v }

type MirrorNode struct {
	Value int64
	Next  *MirrorNode
}

func CyclicMirror() *MirrorNode   { n := &MirrorNode{Value: 1}; n.Next = n; return n }
func SharedMirror() []*MirrorNode { n := &MirrorNode{Value: 1}; return []*MirrorNode{n, n} }

type MirrorEmbedded struct {
	MirrorItem
	Label string
}

func EmbeddedMirror() MirrorEmbedded {
	return MirrorEmbedded{MirrorItem: MirrorItem{ID: 9}, Label: "embedded"}
}

type MirrorPointerEmbedded struct{ *MirrorItem }
type MirrorAmbiguous struct {
	ID int
	Id int
}
type MirrorArray struct{ Values [2]int }

func ArrayMirror() MirrorArray            { return MirrorArray{Values: [2]int{1, 2}} }
func EchoArray(v MirrorArray) MirrorArray { return v }
func MirrorBounded(value, bound int) int  { return value }

type mirrorHidden struct{ Promoted int }
type MirrorHidden struct{ mirrorHidden }

func HiddenMirror() MirrorHidden { return MirrorHidden{mirrorHidden: mirrorHidden{Promoted: 7}} }

type MirrorMethod struct{ TITLE string }

func (MirrorMethod) Title() string { return "method" }
func MethodMirror() MirrorMethod   { return MirrorMethod{TITLE: "field"} }

type MirrorFold struct {
	HTTP string
	Http string
}
type SliceNode struct{ Children []SliceNode }

func SliceCycle() SliceNode {
	s := make([]SliceNode, 1)
	s[0].Children = s
	return SliceNode{Children: s}
}
func SliceSharing() []SliceNode { s := make([]SliceNode, 2); s[1].Children = s[:1]; return s }

type MapNode struct{ Children map[string]MapNode }

func MapCycle() MapNode {
	m := map[string]MapNode{}
	m["self"] = MapNode{Children: m}
	return MapNode{Children: m}
}
func MapSharing() []MapNode {
	m := map[string]MapNode{"leaf": {}}
	return []MapNode{{Children: m}, {Children: m}}
}

type FactList struct{ Values []int64 }

func BadFactList() FactList      { return FactList{Values: []int64{300, -1}} }
func NegativeFactList() FactList { return FactList{Values: []int64{-2, -1}} }

type OpaqueNode struct {
	Next *OpaqueNode
	View *View
}

func MakeOpaqueNode(h *Handle) *OpaqueNode {
	v := NewView(h)
	return &OpaqueNode{Next: &OpaqueNode{View: v}, View: v}
}
