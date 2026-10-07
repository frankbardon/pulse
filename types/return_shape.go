package types

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/internal/returnplan"
)

// return_shape.go applies a resolved `return` plan (internal/returnplan)
// to a Response in two layers:
//
//  1. The PRUNE (applyReturnPlan, installed as the returnplan applier):
//     every excluded slot the Go value can represent as absent is
//     zeroed in place — nil pointers / slices / maps, deleted map keys
//     (data row columns included), zero non-nillable leaves.
//  2. The PLANNED ENCODER (planEncoder, reached from
//     Response.MarshalJSON when the response carries a plan): a
//     non-omitempty leaf the prune could only zero (`total_rows`,
//     `tests[*].p_value`, `matrices[*].primary`, …) is dropped from the
//     wire, never written as a zero or null.
//
// Both walk the value by reflection under encoding/json's field rules
// and ask Plan.Visit per concrete node. A subtree the plan selects
// whole is handed to encodeFinite untouched, so its bytes cannot drift
// from the unshaped form. The result types whose MarshalJSON is the
// plain alias → MarshalFinite form (structuralTypes) are walked
// structurally, which is how the plan reaches inside them — the matrix
// marshalers (MatrixValues, MatrixResult, MatrixComponents) included —
// without a plan parameter on their methods. Any other marshaller is an
// opaque leaf.
//
// A node the plan keeps only as the ANCESTOR of a selected path (Keep
// without Whole) that turns out to be a scalar — an include reaching
// below a leaf inside an open map — selected nothing, so it is dropped;
// a nil interface likewise. Both layers apply that rule identically.

// returnedKey is the Response.Returned JSON key: emitted whenever set,
// whatever the plan says.
const returnedKey = "returned"

// structuralTypes are the result types whose MarshalJSON is exactly
// MarshalFinite over a method-free alias: encoding them field by field
// reproduces their bytes, so the planned encoder may descend into them.
var structuralTypes = map[reflect.Type]bool{
	reflect.TypeFor[Response]():                   true,
	reflect.TypeFor[ComposedResponse]():           true,
	reflect.TypeFor[ChainResponse]():              true,
	reflect.TypeFor[FacetResult]():                true,
	reflect.TypeFor[OverlayLayer]():               true,
	reflect.TypeFor[ResponseComponents]():         true,
	reflect.TypeFor[AggregationComponents]():      true,
	reflect.TypeFor[AggregationGroupComponents](): true,
	reflect.TypeFor[GrouperComponents]():          true,
	reflect.TypeFor[CrosstabComponents]():         true,
	reflect.TypeFor[CrosstabResult]():             true,
	reflect.TypeFor[MatrixPayload]():              true,
	reflect.TypeFor[OverlayPayload]():             true,
	reflect.TypeFor[OverlaySummary]():             true,
	reflect.TypeFor[TestResult]():                 true,
	reflect.TypeFor[RegressionResult]():           true,
	reflect.TypeFor[FacetField]():                 true,
	reflect.TypeFor[MatrixValues]():               true,
	reflect.TypeFor[MatrixResult]():               true,
	reflect.TypeFor[MatrixComponents]():           true,
}

func init() {
	returnplan.SetApplier(applyReturnPlan)
	returnplan.SetRowEncoder(encodeReturnRow)
}

// componentsPath / dataRowPath root a standalone components payload and
// a single streamed row at their place in the Response, so the plan's
// Response-rooted paths (and Plan.Exact) apply unchanged.
var (
	componentsPath = []returnplan.Segment{returnplan.Key("components")}
	dataRowPath    = []returnplan.Segment{returnplan.Key("data"), returnplan.Elem()}
)

// encodeReturnRow is the returnplan row encoder: row written exactly as
// the planned Response encoder writes one `data` element (column
// selection + precision, count columns exact), or as MarshalFinite when
// owner carries no non-identity plan. The row is never pruned here — a
// streamed row reaches it already pruned (returnshape.RowIter).
func encodeReturnRow(owner any, row map[string]any) ([]byte, error) {
	var p *returnplan.Plan
	if r, ok := owner.(*Response); ok && r != nil {
		p = r.plan
	}
	if p.Identity() {
		return MarshalFinite(row)
	}
	return marshalPlannedAt(reflect.ValueOf(row), p, dataRowPath)
}

// opaqueType reports whether t (or *t) owns its wire form through a
// marshaller the planned walk cannot reproduce field by field.
func opaqueType(t reflect.Type) bool {
	if structuralTypes[t] {
		return false
	}
	return implementsMarshaler(t) || implementsMarshaler(reflect.PointerTo(t))
}

// responseType is the per-slot / per-stage result type.
var responseType = reflect.TypeFor[Response]()

// nestedResponse reports whether a node of type t at path is a Response
// below the root (a Compose slot): it owns its wire form through its
// own plan, so an outer plan never prunes, rounds or re-walks it.
func nestedResponse(t reflect.Type, path []returnplan.Segment) bool {
	return len(path) > 0 && t == responseType
}

func appendSeg(path []returnplan.Segment, s returnplan.Segment) []returnplan.Segment {
	out := make([]returnplan.Segment, len(path), len(path)+1)
	copy(out, path)
	return append(out, s)
}

// jsonField is one serialised struct field under encoding/json's rules.
type jsonField struct {
	value reflect.Value
	name  string
	opts  string
}

// jsonFields lists rv's serialised fields in declaration order: `-`
// and unexported fields skipped, untagged embedded structs flattened
// (a nil embedded pointer contributes nothing) — writeFiniteFields'
// rules.
func jsonFields(rv reflect.Value) []jsonField {
	var out []jsonField
	rt := rv.Type()
	for i := range rt.NumField() {
		sf := rt.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		fv := rv.Field(i)
		if sf.Anonymous && name == "" {
			inner := fv
			if inner.Kind() == reflect.Pointer {
				if inner.IsNil() {
					continue
				}
				inner = inner.Elem()
			}
			if inner.Kind() == reflect.Struct {
				out = append(out, jsonFields(inner)...)
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		out = append(out, jsonField{value: fv, name: name, opts: opts})
	}
	return out
}

// applyReturnPlan is the returnplan applier: prune r under p, attach p,
// report the Open includes nothing matched.
func applyReturnPlan(target any, p *returnplan.Plan) ([]returnplan.Path, bool) {
	var root reflect.Value
	switch r := target.(type) {
	case *Response:
		if r == nil || p.Identity() {
			return nil, true
		}
		root = reflect.ValueOf(r).Elem()
		r.plan = p
	case *ResponseComponents:
		// A standalone components payload (a streamed chunk's), rooted
		// at the Response's `components` key. The caller drops it when
		// the plan excludes `components` outright.
		if r == nil || p.Identity() {
			return nil, true
		}
		r.plan = p
		pr := &returnPruner{plan: p, matched: make([]bool, len(p.Include))}
		if vd := p.Visit(componentsPath); vd.Keep && !vd.Whole {
			pr.pruneFields(reflect.ValueOf(r).Elem(), componentsPath)
		}
		return nil, true
	case *ComposedResponse:
		// The Compose-level plan: rooted at ComposedResponse, it keeps
		// `responses` whole (each slot carries its own plan) and shapes
		// the top-level overlays.
		if r == nil || p.Identity() {
			return nil, true
		}
		root = reflect.ValueOf(r).Elem()
		r.plan = p
	default:
		return nil, false
	}
	pr := &returnPruner{plan: p, matched: make([]bool, len(p.Include))}
	pr.pruneFields(root, nil)
	var unmatched []returnplan.Path
	for i, in := range p.Include {
		if in.Open && !pr.matched[i] {
			unmatched = append(unmatched, in)
		}
	}
	return unmatched, true
}

// returnPruner is layer 1. matched[i] records that Include[i] selected
// at least one concrete node.
type returnPruner struct {
	plan    *returnplan.Plan
	matched []bool
}

func (s *returnPruner) mark(c []returnplan.Segment) {
	for i, in := range s.plan.Include {
		if !s.matched[i] && in.Open && in.Matches(c) {
			s.matched[i] = true
		}
	}
}

// pruneFields prunes the fields of the addressable struct rv at path.
func (s *returnPruner) pruneFields(rv reflect.Value, path []returnplan.Segment) {
	for _, f := range jsonFields(rv) {
		if !f.value.CanSet() {
			continue
		}
		if len(path) == 0 && f.name == returnedKey {
			continue
		}
		child := appendSeg(path, returnplan.Key(f.name))
		vd := s.plan.Visit(child)
		if !vd.Keep {
			f.value.Set(reflect.Zero(f.value.Type()))
			continue
		}
		s.mark(child)
		if vd.Whole {
			continue
		}
		nv, keep := s.prune(f.value, child)
		if !keep {
			f.value.Set(reflect.Zero(f.value.Type()))
			continue
		}
		f.value.Set(nv)
	}
}

// prune shapes v, a node kept without its whole subtree, and returns
// the value to store back (a struct reached through an interface or a
// map value is copied) and whether the node stays at all.
func (s *returnPruner) prune(v reflect.Value, path []returnplan.Segment) (reflect.Value, bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || opaqueType(v.Type().Elem()) || nestedResponse(v.Type().Elem(), path) {
			return v, true
		}
		e := v.Elem()
		nv, keep := s.prune(e, path)
		if !keep {
			return v, false
		}
		e.Set(nv)
		return v, true
	case reflect.Interface:
		if v.IsNil() {
			return v, false
		}
		nv, keep := s.prune(v.Elem(), path)
		if !keep {
			return v, false
		}
		w := reflect.New(v.Type()).Elem()
		w.Set(nv)
		return w, true
	case reflect.Struct:
		if opaqueType(v.Type()) {
			return v, true
		}
		if !v.CanAddr() {
			c := reflect.New(v.Type()).Elem()
			c.Set(v)
			v = c
		}
		s.pruneFields(v, path)
		return v, true
	case reflect.Map:
		if v.IsNil() || v.Type().Key().Kind() != reflect.String || opaqueType(v.Type()) {
			return v, true
		}
		for _, k := range v.MapKeys() {
			child := appendSeg(path, returnplan.Key(k.String()))
			vd := s.plan.Visit(child)
			if !vd.Keep {
				v.SetMapIndex(k, reflect.Value{})
				continue
			}
			s.mark(child)
			if vd.Whole {
				continue
			}
			nv, keep := s.prune(v.MapIndex(k), child)
			if !keep {
				v.SetMapIndex(k, reflect.Value{})
				continue
			}
			v.SetMapIndex(k, nv)
		}
		return v, true
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			// A byte string is a scalar.
			return v, false
		}
		if opaqueType(v.Type()) {
			return v, true
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			return v, true
		}
		if !v.CanAddr() {
			c := reflect.New(v.Type()).Elem()
			c.Set(v)
			v = c
		}
		child := appendSeg(path, returnplan.Elem())
		vd := s.plan.Visit(child)
		if !vd.Keep {
			if v.Kind() == reflect.Slice {
				return v.Slice(0, 0), true
			}
			v.Set(reflect.Zero(v.Type()))
			return v, true
		}
		if v.Len() > 0 {
			s.mark(child)
		}
		if vd.Whole {
			return v, true
		}
		if v.Kind() == reflect.Array {
			for i := range v.Len() {
				nv, keep := s.prune(v.Index(i), child)
				if !keep {
					v.Index(i).Set(reflect.Zero(v.Type().Elem()))
					continue
				}
				v.Index(i).Set(nv)
			}
			return v, true
		}
		kept := 0
		for i := range v.Len() {
			nv, keep := s.prune(v.Index(i), child)
			if !keep {
				continue
			}
			v.Index(kept).Set(nv)
			kept++
		}
		return v.Slice(0, kept), true
	}
	// A scalar reached only as the ancestor of a deeper path selected
	// nothing.
	return v, false
}

// planEncoder is layer 2: MarshalFinite under a plan.
//
// With Precision set the encoder also owns the float leaves: a wholly
// selected subtree is still walked node by node (no Visit calls — the
// plan already answered "everything") so every float64 / float32 can be
// written as strconv.FormatFloat(v, 'g', Precision, bits), NaN / ±Inf
// staying null. Only the Go float kinds are rounded, so an int — and a
// decimal128 value, which reaches a row as a string or as an opaque
// marshaller — is never touched. A node under a precisionExempt or
// Plan.Exact path is written exact. Opaque marshallers stay leaves. With
// Precision 0 a wholly selected subtree goes through encodeFinite
// untouched, so its bytes cannot drift from the unshaped form.
type planEncoder struct {
	plan *returnplan.Plan
}

// precisionExempt is the DECLARED list of response nodes whose floats
// carry integer semantics on every request — a count held as a float64
// — so Return.precision never rounds them (the subtree under each path
// is written exact). Request-derived exemptions (a count aggregation's
// data column, …) ride Plan.Exact instead. A new count-carried-as-float
// slot goes HERE: TestPrecision_FloatLeavesClassified enumerates every
// float leaf of the Response type and fails on one it cannot classify.
var precisionExempt = []returnplan.Path{
	// The pairwise N beside a matrix: integer counts in a MatrixValues.
	{Segments: []returnplan.Segment{returnplan.Key("matrices"), returnplan.Elem(), returnplan.Key("auxiliary"), returnplan.Key("n")}},
}

// marshalPlanned encodes v (the response root) under p.
func marshalPlanned(v reflect.Value, p *returnplan.Plan) ([]byte, error) {
	e := &planEncoder{plan: p}
	vd := p.Visit(nil)
	b, _, err := e.encode(v, nil, vd.Whole)
	return b, err
}

// marshalPlannedAt encodes v, the node at path below the response
// root, under p — a standalone components payload or one streamed row.
func marshalPlannedAt(v reflect.Value, p *returnplan.Plan, path []returnplan.Segment) ([]byte, error) {
	e := &planEncoder{plan: p}
	vd := p.Visit(path)
	b, present, err := e.encode(v, path, vd.Whole)
	if err == nil && !present {
		b = []byte("null")
	}
	return b, err
}

// exact reports whether the node at path keeps full float precision.
func (e *planEncoder) exact(path []returnplan.Segment) bool {
	for _, x := range precisionExempt {
		if x.Selects(path) {
			return true
		}
	}
	for _, x := range e.plan.Exact {
		if x.Selects(path) {
			return true
		}
	}
	return false
}

// encodeFloat writes one float leaf at the plan's precision.
func (e *planEncoder) encodeFloat(v reflect.Value) []byte {
	f := v.Float()
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null")
	}
	bits := 64
	if v.Kind() == reflect.Float32 {
		bits = 32
	}
	return strconv.AppendFloat(nil, f, 'g', e.plan.Precision, bits)
}

// encode writes v at path. whole means the plan selects the subtree
// untouched; present false means the node is absent.
func (e *planEncoder) encode(v reflect.Value, path []returnplan.Segment, whole bool) ([]byte, bool, error) {
	if whole && (e.plan.Precision == 0 || e.exact(path)) {
		b, err := encodeFinite(v)
		return b, true, err
	}
	if !v.IsValid() {
		if whole {
			return []byte("null"), true, nil
		}
		return nil, false, nil
	}
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		if whole {
			return e.encodeFloat(v), true, nil
		}
	case reflect.Pointer:
		if v.IsNil() {
			return []byte("null"), true, nil
		}
		if opaqueType(v.Type().Elem()) || nestedResponse(v.Type().Elem(), path) {
			b, err := encodeFinite(v)
			return b, true, err
		}
		return e.encode(v.Elem(), path, whole)
	case reflect.Interface:
		if v.IsNil() {
			if whole {
				return []byte("null"), true, nil
			}
			return nil, false, nil
		}
		return e.encode(v.Elem(), path, whole)
	case reflect.Struct:
		if opaqueType(v.Type()) {
			b, err := encodeFinite(v)
			return b, true, err
		}
		b, err := e.encodeStruct(v, path, whole)
		return b, true, err
	case reflect.Map:
		if v.IsNil() {
			return []byte("null"), true, nil
		}
		if opaqueType(v.Type()) || v.Type().Key().Kind() != reflect.String {
			b, err := encodeFinite(v)
			return b, true, err
		}
		return e.encodeMap(v, path, whole)
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 && v.Kind() == reflect.Slice {
			if whole {
				b, err := encodeFinite(v)
				return b, true, err
			}
			return nil, false, nil
		}
		if v.Kind() == reflect.Slice && v.IsNil() {
			return []byte("null"), true, nil
		}
		if opaqueType(v.Type()) {
			b, err := encodeFinite(v)
			return b, true, err
		}
		child := appendSeg(path, returnplan.Elem())
		vd := returnplan.Verdict{Keep: true, Whole: true}
		if !whole {
			vd = e.plan.Visit(child)
		}
		if !vd.Keep {
			return []byte("[]"), true, nil
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		n := 0
		for i := range v.Len() {
			b, present, err := e.encode(v.Index(i), child, vd.Whole)
			if err != nil {
				return nil, false, err
			}
			if !present {
				continue
			}
			if n > 0 {
				buf.WriteByte(',')
			}
			buf.Write(b)
			n++
		}
		buf.WriteByte(']')
		return buf.Bytes(), true, nil
	}
	if whole {
		// Any other scalar of a wholly selected subtree.
		b, err := encodeFinite(v)
		return b, true, err
	}
	// A scalar reached only as the ancestor of a deeper path selected
	// nothing.
	return nil, false, nil
}

func (e *planEncoder) encodeStruct(v reflect.Value, path []returnplan.Segment, whole bool) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, f := range jsonFields(v) {
		var vd returnplan.Verdict
		if whole || (len(path) == 0 && f.name == returnedKey) {
			vd = returnplan.Verdict{Keep: true, Whole: true}
		} else {
			vd = e.plan.Visit(appendSeg(path, returnplan.Key(f.name)))
		}
		if !vd.Keep {
			continue
		}
		if hasOpt(f.opts, "omitempty") && isEmptyJSONValue(f.value) {
			continue
		}
		if hasOpt(f.opts, "omitzero") && isZeroJSONValue(f.value) {
			continue
		}
		b, present, err := e.encode(f.value, appendSeg(path, returnplan.Key(f.name)), vd.Whole)
		if err != nil {
			return nil, err
		}
		if !present {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		kb, err := json.Marshal(f.name)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(b)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// encodeMap writes a string-keyed map in encoding/json's key order,
// asking the plan per key unless the whole map is selected.
func (e *planEncoder) encodeMap(v reflect.Value, path []returnplan.Segment, whole bool) ([]byte, bool, error) {
	type kv struct {
		key string
		val reflect.Value
	}
	entries := make([]kv, 0, v.Len())
	iter := v.MapRange()
	for iter.Next() {
		entries = append(entries, kv{key: iter.Key().String(), val: iter.Value()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	var buf bytes.Buffer
	buf.WriteByte('{')
	n := 0
	for _, en := range entries {
		child := appendSeg(path, returnplan.Key(en.key))
		vd := returnplan.Verdict{Keep: true, Whole: true}
		if !whole {
			vd = e.plan.Visit(child)
		}
		if !vd.Keep {
			continue
		}
		b, present, err := e.encode(en.val, child, vd.Whole)
		if err != nil {
			return nil, false, err
		}
		if !present {
			continue
		}
		if n > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(en.key)
		if err != nil {
			return nil, false, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(b)
		n++
	}
	buf.WriteByte('}')
	return buf.Bytes(), true, nil
}
