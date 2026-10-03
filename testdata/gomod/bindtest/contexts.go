package bindtest

import (
	"context"
	"errors"
	"sync/atomic"
)

type ContextResource struct{ ctx context.Context }

var contextCloses atomic.Int64

func (r *ContextResource) Close() error    { contextCloses.Add(1); return nil }
func (r *ContextResource) Cancelled() bool { return r.ctx.Err() != nil }
func (r *ContextResource) Value() string {
	value, _ := r.ctx.Value(contextKey{}).(string)
	return value
}
func ContextCloseCount() int64 { return contextCloses.Load() }
func ContextResources(ctx context.Context, shape string) ([]*ContextResource, error) {
	a, b := &ContextResource{ctx}, &ContextResource{ctx}
	switch shape {
	case "error":
		return []*ContextResource{a, b}, errors.New("open failed")
	case "nil":
		return []*ContextResource{a, nil, b}, nil
	case "duplicate":
		return []*ContextResource{a, a}, nil
	default:
		return []*ContextResource{a, b}, nil
	}
}
func ContextMap(ctx context.Context) map[string]*ContextResource {
	return map[string]*ContextResource{"first": {ctx}, "last": {ctx}}
}
func ContextArray(ctx context.Context) [2]*ContextResource { return [2]*ContextResource{{ctx}, {ctx}} }
func OptionalContextResource(ctx context.Context, ok bool) (*ContextResource, bool) {
	return &ContextResource{ctx}, ok
}
func ContextListResources(contexts []context.Context) []*ContextResource {
	return []*ContextResource{{contexts[0]}, {contexts[1]}}
}

type contextKey struct{}

var fixedCancel context.CancelCauseFunc

func FixedContext() context.Context {
	ctx, cancel := context.WithCancelCause(context.WithValue(context.Background(), contextKey{}, "fixed-value"))
	fixedCancel = cancel
	return ctx
}
func CancelFixedContext() { fixedCancel(errors.New("fixed limit")) }

type ContextParams struct{ Context context.Context }

func ContextStructResource(params ContextParams) *ContextResource {
	return &ContextResource{params.Context}
}
