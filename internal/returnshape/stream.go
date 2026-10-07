package returnshape

import (
	"context"
	"reflect"

	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// Source is the method set of service.RowIter, restated so this
// package need not import the service (a service.RowIter satisfies it,
// and the shaped RowIter below satisfies service.RowIter in turn).
type Source interface {
	Next(ctx context.Context) (map[string]any, bool, error)
	Close() error
	Metadata() *types.ResponseMetadata
	Components() *types.ResponseComponents
}

// RowIter shapes a streamed Process under a resolved, non-identity
// plan — the streaming arm of Apply:
//
//   - every row is a deep CLONE of the inner iterator's row (a
//     streaming iterator may reuse its maps) pruned exactly as a
//     buffered shaped response prunes one `data` element; when the
//     plan excludes `data` the stream yields no rows (the inner
//     iterator is still drained, so its terminal parts complete);
//   - Components and Metadata are shaped COPIES — the inner iterator's
//     running state is never touched, so an excluded mergeable
//     operator keeps accumulating; an excluded slot is nil. The shaped
//     Components carries the plan, so its JSON equals the buffered
//     shaped response's `components` (precision included);
//   - Returned stamps the selection, and only once the stream is
//     exhausted (terminal flush).
//
// Precision is wire-only: rows stay full float64 in Go, and MarshalRow
// writes one at the plan's precision.
type RowIter struct {
	inner    Source
	plan     *returnplan.Plan
	carrier  *types.Response // holds the plan for the row encoder
	dataKept bool
	done     bool

	compIn, compOut *types.ResponseComponents
	metaIn, metaOut *types.ResponseMetadata
}

// NewRowIter wraps inner under plan. A nil or identity plan returns
// inner unchanged (the byte-identity fast path) — the result is then
// whatever inner is.
func NewRowIter(inner Source, plan *returnplan.Plan) Source {
	if plan.Identity() {
		return inner
	}
	carrier := &types.Response{}
	_, _ = returnplan.Apply(carrier, plan)
	return &RowIter{
		inner:    inner,
		plan:     plan,
		carrier:  carrier,
		dataKept: plan.Visit([]returnplan.Segment{returnplan.Key("data")}).Keep,
	}
}

// Next returns the next shaped row.
func (it *RowIter) Next(ctx context.Context) (map[string]any, bool, error) {
	for {
		row, ok, err := it.inner.Next(ctx)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			it.done = true
			return nil, false, nil
		}
		if !it.dataKept {
			continue
		}
		clone, _ := deepCopy(reflect.ValueOf(row)).Interface().(map[string]any)
		probe := &types.Response{Data: []map[string]any{clone}}
		_, _ = returnplan.Apply(probe, it.plan)
		if len(probe.Data) == 0 || probe.Data[0] == nil {
			continue
		}
		return probe.Data[0], true, nil
	}
}

// Close closes the inner iterator.
func (it *RowIter) Close() error { return it.inner.Close() }

// Metadata returns the inner metadata shaped under the plan (nil when
// excluded).
func (it *RowIter) Metadata() *types.ResponseMetadata {
	in := it.inner.Metadata()
	if in != it.metaIn {
		it.metaIn = in
		it.metaOut = nil
		if in != nil {
			cp := *in
			probe := &types.Response{Metadata: &cp}
			_, _ = returnplan.Apply(probe, it.plan)
			it.metaOut = probe.Metadata
		}
	}
	return it.metaOut
}

// Components returns the inner components shaped under the plan (nil
// when excluded); see RowIter. Cached per inner pointer, so a buffered
// inner is shaped once.
func (it *RowIter) Components() *types.ResponseComponents {
	in := it.inner.Components()
	if in == it.compIn {
		return it.compOut
	}
	it.compIn = in
	it.compOut = nil
	if in == nil || !it.plan.Visit([]returnplan.Segment{returnplan.Key("components")}).Keep {
		return nil
	}
	cp, _ := deepCopy(reflect.ValueOf(in)).Interface().(*types.ResponseComponents)
	_, _ = returnplan.Apply(cp, it.plan)
	it.compOut = cp
	return cp
}

// Returned is the selection marker, nil until the stream is exhausted.
func (it *RowIter) Returned() *types.ReturnedMarker {
	if !it.done {
		return nil
	}
	return &types.ReturnedMarker{
		Preset:    it.plan.Preset,
		Digest:    it.plan.Digest,
		Precision: it.plan.Precision,
	}
}

// MarshalRow writes one row this iterator yielded at the plan's
// precision (count columns exact, NaN / ±Inf null).
func (it *RowIter) MarshalRow(row map[string]any) ([]byte, error) {
	return MarshalRow(it.carrier, row)
}

// MarshalRow writes row, one element of resp.Data (or a row streamed
// under resp's plan), as its wire form: under resp's plan when it was
// shaped (precision applied; the row is assumed already pruned), else
// exactly types.MarshalFinite(row).
func MarshalRow(resp *types.Response, row map[string]any) ([]byte, error) {
	b, ok, err := returnplan.EncodeRow(resp, row)
	if !ok {
		return types.MarshalFinite(row)
	}
	return b, err
}

// MarshalStreamRow writes a row drawn from iter: through the shaped
// iterator's MarshalRow when iter is one, else exactly
// types.MarshalFinite(row) — so an unshaped stream is byte-identical.
func MarshalStreamRow(iter any, row map[string]any) ([]byte, error) {
	if s, ok := iter.(*RowIter); ok {
		return s.MarshalRow(row)
	}
	return types.MarshalFinite(row)
}

// deepCopy returns a copy of v sharing no mutable memory reachable
// through exported fields, pointers, slices, maps or interfaces, so
// pruning the copy never reaches the original. A struct is copied by
// value first (unexported fields come along), then its exported fields
// are copied deeply.
func deepCopy(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(deepCopy(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(deepCopy(v.Elem()))
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := range v.NumField() {
			if f := out.Field(i); f.CanSet() {
				f.Set(deepCopy(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := range v.Len() {
			out.Index(i).Set(deepCopy(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}
		return out
	}
	return v
}
