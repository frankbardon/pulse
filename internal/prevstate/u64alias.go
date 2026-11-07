package prevstate

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// U64Aliases is the u64 half of the derived-column view: for attrs in
// order, the label of every ATTR_FORMULA whose expression IS a bare
// read of a u64 field (exprenv.U64Source — `id`, `$env.id`, `$env?.id`,
// a let chain ending in one, or another such alias) mapped to that
// schema field. Its column holds the field's value on every row (null
// where the field is null), so an expression reading the label
// declares it exprenv.DerivedU64Type and binds the field's stored word
// (the runtime's exprProgram), exactly as a bare read of the field: its
// comparisons are exact past 2^53. Predict declares the same type from
// the same map.
//
// A label that names a schema field (refused as a shadow on both sides)
// or an earlier label contributes nothing; nil when no slot does, so a
// request without one is byte-identical. A computed formula (`id + 0`,
// `abs(id)`, `id ?? 0`) is no alias: its column keeps the float64 echo.
// No other attribute or feature writes a u64 word — every one emits a
// float64 transform, a count, a code or a 0 / 1 — so none is an alias.
func U64Aliases(attrs []*types.Attribute, schema *encoding.Schema) map[string]string {
	if schema == nil {
		return nil
	}
	var out map[string]string
	seen := map[string]bool{}
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		label := Label(attr)
		if seen[label] {
			continue
		}
		seen[label] = true
		if attr.Type != types.ATTR_FORMULA || schema.Field(label) != nil {
			continue
		}
		src, ok := exprenv.U64Source(attr.Expression, func(name string) bool {
			if _, ok := out[name]; ok {
				return true
			}
			f := schema.Field(name)
			return f != nil && f.Type == encoding.FieldTypeU64
		})
		if !ok {
			continue
		}
		if root, ok := out[src]; ok {
			src = root
		}
		if out == nil {
			out = map[string]string{}
		}
		out[label] = src
	}
	return out
}
