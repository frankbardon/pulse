package winformula

import (
	"reflect"
	"slices"

	"github.com/expr-lang/expr/checker/nature"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Output-row cell types. Over the grouped or aggregated output rows a
// WIN_FORMULA's columns are the group keys and the aggregation labels;
// OutputColumns declares each as the Go type the engine writes into its
// non-null cell, read off the request alone, so predict's
// field-reference walk and the window factory compile the same
// declarations and arithmetic on a label key or a non-number
// aggregation is refused at compile on both sides. A name the rule does
// not decide (an extension or hidden operator's column) stays `any`.

// WelfordCell is the layout of AGG_WELFORD's cell (the engine's
// processing.WelfordTriple, field for field): a formula reads its
// figures as w.Mean, w.Variance and w.N, never the cell as a number.
type WelfordCell struct {
	Mean     float64 `json:"mean"`
	Variance float64 `json:"variance"`
	N        uint64  `json:"n"`
}

var (
	typeString   = reflect.TypeOf("")
	typeFloat    = reflect.TypeOf(float64(0))
	typeLabels   = reflect.TypeOf([]string(nil))
	typeVector   = reflect.TypeOf([]float64(nil))
	typeCountMap = reflect.TypeOf(map[string]int(nil))
	typeSumWMap  = reflect.TypeOf(map[string]float64(nil))
	typeWelford  = reflect.TypeOf(WelfordCell{})
)

// GroupKeyType is the Go type of built-in grouper t's key cell: a
// string (every built-in grouper keys its buckets by label — a bin's
// range label, a date component, a category). nil — `any` — for an
// extension or unknown type.
func GroupKeyType(t types.GroupType) reflect.Type {
	if !slices.Contains(types.AllGroupTypes(), t) {
		return nil
	}
	return typeString
}

// AggregationCellType is the Go type of built-in aggregation slot agg's
// non-null cell on an output row, as a WIN_FORMULA reads it — a decimal
// figure (every decimal-arm type over a decimal field, a decimal
// AGG_CONSTANT) reads as its float64 value:
//
//   - AGG_SET_UNION / AGG_SET_INTERSECTION the label list ([]string);
//   - AGG_SET_FREQUENCY the per-label map: integer counts
//     (map[string]int), or Σw floats (map[string]float64) when the slot
//     carries a weight — agg.Weight as weighting.Stamp resolves it
//     (OutputColumns stamps first, so a request or default weight
//     counts);
//   - AGG_WELFORD the moment triple (WelfordCell);
//   - an array-valued AGG_VEC_* the per-member figures ([]float64);
//   - AGG_CONSTANT by its params.type: a label (string), a label list
//     ([]string), a vector ([]float64), else (numeric family, decimal)
//     a float64; nil when the params do not parse (the reading refuses
//     them first);
//   - every other built-in a float64 (over a decimal field too: the
//     count, the exact decimal or its f64 fallback).
//
// nil — `any` — for an extension or unknown type.
func AggregationCellType(agg *types.Aggregation) reflect.Type {
	if agg == nil || !slices.Contains(types.AllAggregationTypes(), agg.Type) {
		return nil
	}
	switch {
	case agg.Type == types.AGG_SET_UNION || agg.Type == types.AGG_SET_INTERSECTION:
		return typeLabels
	case agg.Type == types.AGG_SET_FREQUENCY:
		if agg.Weight.Spec() != nil {
			return typeSumWMap
		}
		return typeCountMap
	case agg.Type == types.AGG_WELFORD:
		return typeWelford
	case agg.Type.ArrayValued():
		return typeVector
	case agg.Type == types.AGG_CONSTANT:
		spec, ref := aggconst.Parse(agg.Params)
		switch {
		case ref != nil:
			return nil
		case spec.Type.IsCategorical():
			return typeString
		case spec.Type.IsSet():
			return typeLabels
		case spec.Type.IsVector():
			return typeVector
		}
	}
	return typeFloat
}

// OutputColumns is the typed half of a WIN_FORMULA's declarations over
// req's grouped or aggregated output rows: each grouper's key column
// (vectors.GroupKeyField) declared as GroupKeyType, each aggregation's
// label (types.AggregationLabelOf) as AggregationCellType of the slot
// carrying its resolved weight (weighting.Stamp over def, the
// instance's Options.DefaultWeight — the stamp the engine applies), so
// a weighted AGG_SET_FREQUENCY is its Σw map on both sides.
// hidden reports a type the instance hides (nil hides nothing): its
// column stays `any`, as an extension's does — the instance refuses the
// slot before any window runs. A duplicated name keeps its first
// declaration (the field-reference walk refuses the collision). nil
// when req groups and aggregates nothing.
func OutputColumns(req *types.Request, hidden func(string) bool, def *types.WeightSpec) exprtypes.Map {
	if req == nil || len(req.Groups)+len(req.Aggregations) == 0 {
		return nil
	}
	// Built-in weight awareness only: an extension aggregation's column
	// stays `any` whatever its weight.
	req = weighting.Stamp(req, def, nil)
	shown := func(t string) bool { return hidden == nil || !hidden(t) }
	decl := make(exprtypes.Map, len(req.Groups)+len(req.Aggregations))
	add := func(name string, t reflect.Type) {
		if _, dup := decl[name]; dup || t == nil {
			return
		}
		decl[name] = declType(t)
	}
	for _, g := range req.Groups {
		if g == nil {
			continue
		}
		typ := g.Type
		if !shown(string(typ)) {
			typ = ""
		}
		add(vectors.GroupKeyField(typ, g), GroupKeyType(typ))
	}
	for _, a := range req.Aggregations {
		if a == nil || !shown(string(a.Type)) {
			continue
		}
		add(types.AggregationLabelOf(a), AggregationCellType(a))
	}
	return decl
}

// declType is t's declaration. A struct is declared through structType:
// expr's own reflect declaration (exprtypes.TypeOf) carries no field
// table, so the compile of a member read on it faults.
func declType(t reflect.Type) exprtypes.Type {
	if t.Kind() == reflect.Struct {
		return structType{t: t}
	}
	return exprtypes.TypeOf(reflect.Zero(t).Interface())
}

// structType declares a struct-typed column with its field table (the
// nature a cache builds), so w.Mean compiles to a field read by index.
type structType struct{ t reflect.Type }

func (s structType) Nature() nature.Nature {
	var c nature.Cache
	return c.FromType(s.t)
}

func (s structType) Equal(o exprtypes.Type) bool {
	if o == exprtypes.Any {
		return true
	}
	other, ok := o.(structType)
	return ok && other.t == s.t
}

func (s structType) String() string { return s.t.String() }
