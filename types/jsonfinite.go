package types

import (
	"bytes"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// MarshalFinite is json.Marshal with ONE difference: a non-finite float
// (NaN, +Inf, -Inf) anywhere in v is written as JSON null in place
// instead of failing the whole value with `json: unsupported value`.
//
// It is the single wire rule for an UNDEFINED figure. The engine keeps
// NaN in its Go results wherever that is the documented contract (a
// ratio whose every denominator is zero, the first entry of an
// index-vs-prior series, an unfilled rolling window) so a library caller
// detects it with math.IsNaN; JSON has no NaN, so the wire form says
// null. The key keeps its presence: an omitempty slot that is absent
// still means "not reported for this kind", while null means "reported,
// but undefined at this coordinate".
//
// When v holds no non-finite float the output is byte-identical to
// json.Marshal(v) — the walk only reroutes values json.Marshal would
// have refused. Values that implement json.Marshaler or
// encoding.TextMarshaler encode through their own method. Every result
// type that carries a float or an open `any` slot — the payload roots
// (Response, ComposedResponse, ChainResponse, FacetResult) and the
// fragments a caller plausibly serialises on its own (OverlayLayer and
// its payload / summary, CrosstabResult, MatrixPayload, the Components
// types, TestResult, RegressionResult, FacetField) — marshals through
// this rule by default; call MarshalFinite directly for an untyped
// fragment such as a streamed Row.
func MarshalFinite(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return encodeFinite(reflect.ValueOf(v))
}

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// encodeFinite encodes rv, rerouting non-finite floats to null. A
// subtree with no non-finite float is handed to json.Marshal verbatim
// so its bytes cannot drift from the standard encoder's.
func encodeFinite(rv reflect.Value) ([]byte, error) {
	if !rv.IsValid() {
		return []byte("null"), nil
	}
	if !hasNonFinite(rv, 0) {
		return json.Marshal(marshalTarget(rv))
	}
	if t := marshalTarget(rv); t != nil && implementsMarshaler(reflect.TypeOf(t)) {
		// The type owns its wire form (and, for the result types in
		// this package, applies this same rule itself).
		return json.Marshal(t)
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		return []byte("null"), nil
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return []byte("null"), nil
		}
		return encodeFinite(rv.Elem())
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return []byte("null"), nil
		}
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i := range rv.Len() {
			if i > 0 {
				buf.WriteByte(',')
			}
			b, err := encodeFinite(rv.Index(i))
			if err != nil {
				return nil, err
			}
			buf.Write(b)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil
	case reflect.Map:
		return encodeFiniteMap(rv)
	case reflect.Struct:
		return encodeFiniteStruct(rv)
	}
	return json.Marshal(marshalTarget(rv))
}

// marshalTarget returns the value json.Marshal should see for rv. An
// addressable value whose POINTER type carries the marshaller is handed
// over by address, mirroring encoding/json's own addressable-receiver
// rule so a pointer-receiver MarshalJSON fires exactly when it would
// have under json.Marshal.
func marshalTarget(rv reflect.Value) any {
	if !rv.CanInterface() {
		return nil
	}
	if rv.Kind() != reflect.Pointer && rv.CanAddr() {
		pt := rv.Addr().Type()
		if !implementsMarshaler(rv.Type()) && implementsMarshaler(pt) {
			return rv.Addr().Interface()
		}
	}
	return rv.Interface()
}

func implementsMarshaler(t reflect.Type) bool {
	return t.Implements(jsonMarshalerType) || t.Implements(textMarshalerType)
}

// encodeFiniteMap writes a map with its keys in encoding/json's order
// (sorted by the encoded key string). Key kinds encoding/json supports
// beyond strings and integers fall back to json.Marshal.
func encodeFiniteMap(rv reflect.Value) ([]byte, error) {
	if rv.IsNil() {
		return []byte("null"), nil
	}
	kk := rv.Type().Key().Kind()
	type kv struct {
		key string
		val reflect.Value
	}
	entries := make([]kv, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		k := iter.Key()
		var ks string
		switch {
		case kk == reflect.String:
			ks = k.String()
		case kk >= reflect.Int && kk <= reflect.Int64:
			ks = strconv.FormatInt(k.Int(), 10)
		case kk >= reflect.Uint && kk <= reflect.Uintptr:
			ks = strconv.FormatUint(k.Uint(), 10)
		default:
			return json.Marshal(marshalTarget(rv))
		}
		entries = append(entries, kv{key: ks, val: iter.Value()})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, e := range entries {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(e.key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := encodeFinite(e.val)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// encodeFiniteStruct writes a struct's exported fields in declaration
// order under encoding/json's tag rules (name, "-", omitempty,
// omitzero; untagged embedded structs flatten). A non-finite float is
// never "empty", so an omitempty slot holding NaN keeps its key and
// says null.
func encodeFiniteStruct(rv reflect.Value) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	if err := writeFiniteFields(&buf, rv, &first); err != nil {
		return nil, err
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func writeFiniteFields(buf *bytes.Buffer, rv reflect.Value, first *bool) error {
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
				if err := writeFiniteFields(buf, inner, first); err != nil {
					return err
				}
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if hasOpt(opts, "omitempty") && isEmptyJSONValue(fv) {
			continue
		}
		if hasOpt(opts, "omitzero") && isZeroJSONValue(fv) {
			continue
		}
		if !*first {
			buf.WriteByte(',')
		}
		*first = false
		kb, err := json.Marshal(name)
		if err != nil {
			return err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := encodeFinite(fv)
		if err != nil {
			return err
		}
		buf.Write(vb)
	}
	return nil
}

func hasOpt(opts, want string) bool {
	for opts != "" {
		var o string
		o, opts, _ = strings.Cut(opts, ",")
		if o == want {
			return true
		}
	}
	return false
}

// isEmptyJSONValue mirrors encoding/json's omitempty test. A non-finite
// float is non-zero, so it is never empty.
func isEmptyJSONValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

// isZeroJSONValue mirrors encoding/json's omitzero test: an IsZero()
// bool method when the type has one, reflect's zero value otherwise.
func isZeroJSONValue(v reflect.Value) bool {
	if v.CanInterface() {
		if z, ok := v.Interface().(interface{ IsZero() bool }); ok {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return true
			}
			return z.IsZero()
		}
	}
	return v.IsZero()
}

// maxFiniteDepth bounds the scan so a pathological self-referencing
// value cannot recurse without end; encoding/json refuses such a value
// anyway.
const maxFiniteDepth = 512

// hasNonFinite reports whether a non-finite float is reachable from rv
// through anything encoding/json would serialise. Common payload shapes
// take a reflection-free fast path.
func hasNonFinite(rv reflect.Value, depth int) bool {
	if !rv.IsValid() || depth > maxFiniteDepth {
		return false
	}
	if rv.CanInterface() {
		switch x := rv.Interface().(type) {
		case float64:
			return math.IsNaN(x) || math.IsInf(x, 0)
		case string, bool, int, int64, int32, uint64, uint32, uint8, json.RawMessage, []byte:
			return false
		case map[string]any:
			for _, e := range x {
				if anyNonFinite(e, depth+1) {
					return true
				}
			}
			return false
		case []any:
			for _, e := range x {
				if anyNonFinite(e, depth+1) {
					return true
				}
			}
			return false
		case []map[string]any:
			for _, m := range x {
				for _, e := range m {
					if anyNonFinite(e, depth+1) {
						return true
					}
				}
			}
			return false
		case []float64:
			for _, f := range x {
				if math.IsNaN(f) || math.IsInf(f, 0) {
					return true
				}
			}
			return false
		}
	}
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		return math.IsNaN(f) || math.IsInf(f, 0)
	case reflect.Interface, reflect.Pointer:
		if rv.IsNil() {
			return false
		}
		return hasNonFinite(rv.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		et := rv.Type().Elem().Kind()
		if et == reflect.String || et == reflect.Bool || (et >= reflect.Int && et <= reflect.Uintptr) {
			return false
		}
		for i := range rv.Len() {
			if hasNonFinite(rv.Index(i), depth+1) {
				return true
			}
		}
	case reflect.Map:
		iter := rv.MapRange()
		for iter.Next() {
			if hasNonFinite(iter.Value(), depth+1) {
				return true
			}
		}
	case reflect.Struct:
		rt := rv.Type()
		for i := range rt.NumField() {
			sf := rt.Field(i)
			if !sf.IsExported() && !sf.Anonymous {
				continue
			}
			if sf.Tag.Get("json") == "-" {
				continue
			}
			if hasNonFinite(rv.Field(i), depth+1) {
				return true
			}
		}
	}
	return false
}

// anyNonFinite is hasNonFinite for an interface element, skipping the
// reflect.ValueOf allocation for the scalar kinds a row map holds.
func anyNonFinite(v any, depth int) bool {
	switch x := v.(type) {
	case nil, string, bool, int, int64, int32, uint64, uint32, uint8:
		return false
	case float64:
		return math.IsNaN(x) || math.IsInf(x, 0)
	case float32:
		f := float64(x)
		return math.IsNaN(f) || math.IsInf(f, 0)
	}
	return hasNonFinite(reflect.ValueOf(v), depth)
}

// The payload roots below marshal under MarshalFinite's rule so an
// embedder's own json.Marshal of a result — not only the CLI / MCP
// envelope — serialises an undefined figure as null. Each converts to
// a method-free alias first so the walk does not re-enter the method,
// and passes it by address so field marshallers see the same
// addressability json.Marshal gave them through the usual *T root.

// MarshalJSON writes the response with every non-finite float as null;
// see MarshalFinite.
func (r Response) MarshalJSON() ([]byte, error) {
	type alias Response
	return MarshalFinite((*alias)(&r))
}

// MarshalJSON writes the composed response with every non-finite float
// as null; see MarshalFinite.
func (r ComposedResponse) MarshalJSON() ([]byte, error) {
	type alias ComposedResponse
	return MarshalFinite((*alias)(&r))
}

// MarshalJSON writes the chain response with every non-finite float as
// null; see MarshalFinite.
func (r ChainResponse) MarshalJSON() ([]byte, error) {
	type alias ChainResponse
	return MarshalFinite((*alias)(&r))
}

// MarshalJSON writes the facet result with every non-finite float as
// null; see MarshalFinite.
func (r FacetResult) MarshalJSON() ([]byte, error) {
	type alias FacetResult
	return MarshalFinite((*alias)(&r))
}

// MarshalJSON writes the overlay layer with every non-finite float as
// null; see MarshalFinite.
func (l OverlayLayer) MarshalJSON() ([]byte, error) {
	type alias OverlayLayer
	return MarshalFinite((*alias)(&l))
}

// MarshalJSON writes the components with every non-finite float as null;
// see MarshalFinite.
func (v ResponseComponents) MarshalJSON() ([]byte, error) {
	type alias ResponseComponents
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the aggregation components with every non-finite float as null;
// see MarshalFinite.
func (v AggregationComponents) MarshalJSON() ([]byte, error) {
	type alias AggregationComponents
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes one bucket's aggregation components with every
// non-finite float as null; see MarshalFinite.
func (v AggregationGroupComponents) MarshalJSON() ([]byte, error) {
	type alias AggregationGroupComponents
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the grouper components with every non-finite float as null;
// see MarshalFinite.
func (v GrouperComponents) MarshalJSON() ([]byte, error) {
	type alias GrouperComponents
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the crosstab components with every non-finite float as null;
// see MarshalFinite.
func (v CrosstabComponents) MarshalJSON() ([]byte, error) {
	type alias CrosstabComponents
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the crosstab result with every non-finite float as null;
// see MarshalFinite.
func (v CrosstabResult) MarshalJSON() ([]byte, error) {
	type alias CrosstabResult
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the matrix payload with every non-finite float as null;
// see MarshalFinite.
func (v MatrixPayload) MarshalJSON() ([]byte, error) {
	type alias MatrixPayload
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the overlay payload with every non-finite float as null;
// see MarshalFinite.
func (v OverlayPayload) MarshalJSON() ([]byte, error) {
	type alias OverlayPayload
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the overlay summary with every non-finite float as null;
// see MarshalFinite.
func (v OverlaySummary) MarshalJSON() ([]byte, error) {
	type alias OverlaySummary
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the test result with every non-finite float as null;
// see MarshalFinite.
func (v TestResult) MarshalJSON() ([]byte, error) {
	type alias TestResult
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the regression result with every non-finite float as null;
// see MarshalFinite.
func (v RegressionResult) MarshalJSON() ([]byte, error) {
	type alias RegressionResult
	return MarshalFinite((*alias)(&v))
}

// MarshalJSON writes the facet field with every non-finite float as null;
// see MarshalFinite.
func (v FacetField) MarshalJSON() ([]byte, error) {
	type alias FacetField
	return MarshalFinite((*alias)(&v))
}
