package sweep

import (
	"reflect"
	"strings"

	"github.com/frankbardon/pulse/types"
)

// A rank path is resolved against the slot's wire document, where an
// `omitempty` figure that is exactly 0 has no key at all. A missing
// final segment is therefore re-read off the typed Response (#300): when
// the path names a plain numeric struct field whose value is 0 AND the
// field's applicability rule says the operator that produced the
// enclosing result computes it, the slot ranks with 0. Everything else
// that is absent on the wire — a nil pointer or entry, an absent map
// key, a figure the operator does not compute, or a figure with no
// applicability rule — stays missing. The wire keeps `omitempty`
// unchanged; only the rank's reading of a vanished zero moves.

// computedZeroFigures lists, per result struct type, the `omitempty`
// numeric figures whose exact 0 is a computed value, each with the rule
// that says whether the enclosing result computes it. An unlisted
// figure's zero is indistinguishable from "not computed" and stays
// missing — the conservative reading: a rank must never place an
// uncomputed 0 first.
var computedZeroFigures = map[reflect.Type]map[string]func(parent reflect.Value) bool{
	reflect.TypeFor[types.RegressionResult](): {
		"residual_std_err": regressionTypeIn(types.REG_OLS, types.REG_BAYES_LINEAR),
		"r2":               regressionTypeIn(types.REG_OLS, types.REG_BAYES_LINEAR),
		"adj_r2":           regressionTypeIn(types.REG_OLS, types.REG_BAYES_LINEAR),
		"deviance":         regressionTypeIn(types.REG_GLM),
		"null_deviance":    regressionTypeIn(types.REG_GLM),
		"pseudo_r2":        regressionTypeIn(types.REG_GLM),
	},
}

// regressionTypeIn is the applicability rule for a figure only the
// listed regression operators compute.
func regressionTypeIn(in ...types.RegressionType) func(reflect.Value) bool {
	return func(parent reflect.Value) bool {
		rt := parent.FieldByName("Type").Interface().(types.RegressionType)
		for _, t := range in {
			if rt == t {
				return true
			}
		}
		return false
	}
}

// typedZero reports whether resp carries, at segs, a computed figure
// that is exactly 0 — the case the wire document cannot show. It walks
// the typed value with the wire rules: a struct segment is a json key, a
// map segment a key, a list segment the one element whose `name` or
// `label` equals it.
func typedZero(resp *types.Response, segs []string) bool {
	cur := reflect.ValueOf(resp)
	for k, seg := range segs {
		cur = deref(cur)
		if !cur.IsValid() {
			return false
		}
		last := k == len(segs)-1
		switch cur.Kind() {
		case reflect.Struct:
			f, omitEmpty, ok := fieldByJSONKey(cur, seg)
			if !ok {
				return false
			}
			if last {
				return omitEmpty && isNumericKind(f.Kind()) && f.IsZero() && figureComputed(cur, seg)
			}
			cur = f
		case reflect.Map:
			if cur.Type().Key().Kind() != reflect.String {
				return false
			}
			v := cur.MapIndex(reflect.ValueOf(seg).Convert(cur.Type().Key()))
			if !v.IsValid() || last {
				return false // a present map key is on the wire already
			}
			cur = v
		case reflect.Slice, reflect.Array:
			el, ok := elementByNameOrLabel(cur, seg)
			if !ok || last {
				return false
			}
			cur = el
		default:
			return false
		}
	}
	return false
}

// figureComputed applies the figure's applicability rule; an unlisted
// figure is never treated as computed.
func figureComputed(parent reflect.Value, key string) bool {
	rule, ok := computedZeroFigures[parent.Type()][key]
	return ok && rule(parent)
}

// deref unwraps pointers and interfaces; a nil one is the invalid Value.
func deref(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

// fieldByJSONKey finds the exported struct field whose json key is key,
// and whether it is tagged omitempty. Embedded structs are not followed:
// no rankable result type embeds one.
func fieldByJSONKey(v reflect.Value, key string) (reflect.Value, bool, bool) {
	t := v.Type()
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}
		if name != key {
			continue
		}
		omit := false
		for o := range strings.SplitSeq(opts, ",") {
			if o == "omitempty" {
				omit = true
			}
		}
		return v.Field(i), omit, true
	}
	return reflect.Value{}, false, false
}

// elementByNameOrLabel selects the one list element whose `name` or
// `label` (a json key on a struct, a key on a map) equals seg — the wire
// rule. Zero or several matches select nothing: the wire walk has
// already judged those cases.
func elementByNameOrLabel(list reflect.Value, seg string) (reflect.Value, bool) {
	var match reflect.Value
	n := 0
	for i := range list.Len() {
		el := list.Index(i)
		d := deref(el)
		if !d.IsValid() {
			continue
		}
		if elementKey(d, "name") == seg || elementKey(d, "label") == seg {
			n++
			match = el
		}
	}
	return match, n == 1
}

// elementKey is the string value at key on a struct or string-keyed map,
// else "".
func elementKey(v reflect.Value, key string) string {
	switch v.Kind() {
	case reflect.Struct:
		f, _, ok := fieldByJSONKey(v, key)
		if ok && f.Kind() == reflect.String {
			return f.String()
		}
	case reflect.Map:
		if v.Type().Key().Kind() == reflect.String {
			if x := deref(v.MapIndex(reflect.ValueOf(key).Convert(v.Type().Key()))); x.IsValid() && x.Kind() == reflect.String {
				return x.String()
			}
		}
	}
	return ""
}

// isNumericKind reports whether k is an integer or float kind.
func isNumericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
