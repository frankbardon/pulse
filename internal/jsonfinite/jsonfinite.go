// Package jsonfinite is the read-side inverse of types.MarshalFinite.
//
// The wire writes an undefined figure — NaN / ±Inf in Go — as JSON null
// in place. encoding/json decodes that null into a non-pointer float
// slot as a no-op, so a result read back from its own JSON would carry
// 0 where the engine reported "undefined", and a reader would take the
// 0 for a real figure. Unmarshal restores NaN there instead.
//
// It is for decoding RESULTS (a Response, ComposedResponse,
// ChainResponse, FacetResult) a caller hands back — the CLI's
// `pulse explain --response` and MCP `pulse_explain`. A request is
// decoded with plain encoding/json: a null request float means "unset",
// never "undefined".
package jsonfinite

import (
	"bytes"
	"encoding"
	"encoding/json"
	"math"
	"reflect"
	"strings"
)

// Unmarshal is json.Unmarshal plus one rule: a JSON null that lands in
// a non-pointer float slot — a struct field, a slice or array element,
// a map value — is NaN, not 0. A pointer slot stays nil (encoding/json
// already keeps that null), an `any` slot stays nil, and a value whose
// type decodes itself (json.Unmarshaler / encoding.TextUnmarshaler) is
// left as its own decoder made it. Input holding no null decodes
// exactly as json.Unmarshal decodes it.
func Unmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	if !bytes.Contains(data, []byte("null")) {
		return nil
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&tree); err != nil {
		return err
	}
	restore(reflect.ValueOf(v), tree)
	return nil
}

var (
	jsonUnmarshalerType = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

func decodesItself(t reflect.Type) bool {
	return t.Implements(jsonUnmarshalerType) || t.Implements(textUnmarshalerType) ||
		(t.Kind() != reflect.Pointer && (reflect.PointerTo(t).Implements(jsonUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType)))
}

// restore walks rv beside node — the same document decoded generically
// — and sets NaN in every float slot whose node is null.
func restore(rv reflect.Value, node any) {
	if !rv.IsValid() {
		return
	}
	if node == nil {
		if (rv.Kind() == reflect.Float32 || rv.Kind() == reflect.Float64) && rv.CanSet() {
			rv.SetFloat(math.NaN())
		}
		return
	}
	if decodesItself(rv.Type()) {
		return
	}
	switch rv.Kind() {
	case reflect.Pointer:
		if !rv.IsNil() {
			restore(rv.Elem(), node)
		}
	case reflect.Struct:
		if m, ok := node.(map[string]any); ok {
			restoreStruct(rv, m)
		}
	case reflect.Slice, reflect.Array:
		arr, ok := node.([]any)
		if !ok {
			return
		}
		for i := 0; i < rv.Len() && i < len(arr); i++ {
			restore(rv.Index(i), arr[i])
		}
	case reflect.Map:
		restoreMap(rv, node)
	}
}

// restoreStruct matches each exported field to its JSON key the way
// encoding/json does (the tag name, else the Go name; an exact match
// first, then a case-insensitive one) and promotes an untagged embedded
// struct's fields into the same object.
func restoreStruct(rv reflect.Value, m map[string]any) {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if strings.Contains(","+opts+",", ",string,") {
			continue
		}
		fv := rv.Field(i)
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				fv, ft = fv.Elem(), ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				restoreStruct(fv, m)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		sub, ok := m[name]
		if !ok {
			for k, v := range m {
				if strings.EqualFold(k, name) {
					sub, ok = v, true
					break
				}
			}
		}
		if ok {
			restore(fv, sub)
		}
	}
}

// restoreMap rewrites each string-keyed entry the node carries. Map
// values are not addressable, so each is copied, restored and stored
// back.
func restoreMap(rv reflect.Value, node any) {
	m, ok := node.(map[string]any)
	if !ok || rv.IsNil() || rv.Type().Key().Kind() != reflect.String || decodesItself(rv.Type().Key()) {
		return
	}
	elem := rv.Type().Elem()
	for k, sub := range m {
		key := reflect.ValueOf(k).Convert(rv.Type().Key())
		cur := rv.MapIndex(key)
		if !cur.IsValid() {
			continue
		}
		cp := reflect.New(elem).Elem()
		cp.Set(cur)
		restore(cp, sub)
		rv.SetMapIndex(key, cp)
	}
}
