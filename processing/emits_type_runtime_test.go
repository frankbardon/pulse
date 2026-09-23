package processing

// EMITS-TYPE HONESTY.
//
// # Why this file exists
//
// `descriptor.Operator.EmitsType` declares the field type an operator's
// output column carries. Nothing in the engine reads it: the attribute
// channel is []float64 end to end, window outputs land in a
// map[string]any, feature outputs land in feature.Output.Values. So a
// wrong declaration is SILENT — it is manifest prose that an MCP client
// sizes a destination column from, and no execution path disagrees with
// it.
//
// That is not hypothetical. ATTR_SET_POPCOUNT declared "u8", correct
// while set_u64 topped the set ladder (popcount 0..64) and wrong the
// moment set_u256 landed, because a fully-selected set_u256 has popcount
// 256 and u8 stops at 255. It was caught by READING, during an unrelated
// effort. This file is the gate that would have caught it.
//
// # What it does
//
// For every registered operator that declares a non-empty EmitsType, it
// constructs the operator through its real runtime factory against a
// synthetic schema, RUNS it, and checks every value it actually emitted
// against the declared type's domain: integral and in range for the
// unsigned integer types, 0/1 for packed_bool, magnitude-representable
// for f32, finite-or-NaN for f64.
//
// Completeness runs both ways. An operator that declares an EmitsType
// with no probe here FAILS — so a new declaration cannot land unprobed —
// and a probe naming an operator that declares no EmitsType fails too, so
// the table cannot rot into a fiction of its own.
//
// # What it does NOT cover, said plainly
//
//   - Aggregators, groupers and filterers declare no EmitsType at all
//     (they emit a scalar, a bucket layout, and nothing respectively).
//     They are checked only for the absence: if one starts declaring an
//     EmitsType, the completeness half demands a probe for it.
//   - The CEILING of a wide integer declaration is unreachable by
//     construction. WIN_ROW_NUMBER's "u32" would need four billion rows
//     to press, so for those the probe presses integrality and
//     non-negativity only — which is still the half that breaks, since a
//     negative or fractional value in a u32 column is the actual failure
//     mode. The ceiling IS pressed wherever it is reachable:
//     ATTR_SET_POPCOUNT at 256 (the exact value that broke "u8"),
//     FEAT_TRAIN_TEST_SPLIT at 2 against "u8", ATTR_SET_HAS against
//     packed_bool.
//   - A row on which the operator emitted NO value (the first row of a
//     WIN_LAG, a null-masked feature output) carries nothing to check and
//     is skipped; every probe must still emit at least one value, so a
//     probe cannot pass by producing nothing.
//   - It says nothing about the Go KIND on the wire (int64 vs float64 in
//     the window row map). That is a separate contract; this gate is
//     about the declared value domain.

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing/feature"
	"github.com/frankbardon/pulse/processing/window"
	"github.com/frankbardon/pulse/types"
)

// emitted is one operator's realised output: the values it actually
// produced, with rows that produced nothing already dropped.
type emitted []float64

// ---------------------------------------------------------------------
// Domain check
// ---------------------------------------------------------------------

// checkEmitsDomain asserts every emitted value fits the declared field
// type's domain. An EmitsType this function has no rule for is a failure,
// not a pass: a new declaration must arrive with its bound.
func checkEmitsDomain(t *testing.T, op, emits string, vals emitted) {
	t.Helper()

	ft, ok := encoding.ParseFieldType(emits)
	if !ok {
		t.Errorf("%s declares EmitsType %q, which is not a registered field type", op, emits)
		return
	}
	if len(vals) == 0 {
		t.Errorf("%s emitted no values at all — the probe is vacuous and proves nothing", op)
		return
	}

	// lo/hi bound the declared domain; integral is true for the types
	// that cannot hold a fraction. Every unsigned type floors at 0 — a
	// negative value in a u* column is as broken as an overflowing one.
	var lo, hi float64
	integral := true
	switch ft {
	case encoding.FieldTypeU4:
		lo, hi = 0, 15
	case encoding.FieldTypeU8:
		lo, hi = 0, 255
	case encoding.FieldTypeU16:
		lo, hi = 0, 65535
	case encoding.FieldTypeU32:
		lo, hi = 0, 4294967295
	case encoding.FieldTypeU64:
		lo, hi = 0, math.MaxUint64
	case encoding.FieldTypePackedBool:
		lo, hi = 0, 1
	case encoding.FieldTypeF32:
		lo, hi, integral = -math.MaxFloat32, math.MaxFloat32, false
	case encoding.FieldTypeF64:
		lo, hi, integral = -math.MaxFloat64, math.MaxFloat64, false
	default:
		t.Errorf("%s declares EmitsType %q and this gate has no domain rule for it; "+
			"add one rather than letting the declaration go unchecked", op, emits)
		return
	}

	for i, v := range vals {
		if math.IsNaN(v) {
			if integral {
				t.Errorf("%s emitted NaN at index %d but declares EmitsType %q, which has no NaN",
					op, i, emits)
			}
			continue
		}
		if math.IsInf(v, 0) {
			t.Errorf("%s emitted %v at index %d but declares EmitsType %q", op, v, i, emits)
			continue
		}
		if integral && v != math.Trunc(v) {
			t.Errorf("%s emitted the fractional value %v at index %d but declares the integer EmitsType %q",
				op, v, i, emits)
		}
		if v < lo {
			t.Errorf("%s emitted %v at index %d, below the %v floor of its declared EmitsType %q",
				op, v, i, lo, emits)
		}
		if v > hi {
			t.Errorf("%s emitted %v at index %d, past the %v ceiling of its declared EmitsType %q — "+
				"a client sizing a destination column from the manifest cannot hold the answer",
				op, v, i, hi, emits)
		}
	}
}

// ---------------------------------------------------------------------
// Category runners — each drives the operator's real runtime path
// ---------------------------------------------------------------------

func attrEmitted(t *testing.T, spec *types.Attribute, schema *encoding.Schema, recs []*Record) emitted {
	t.Helper()
	factory, ok := attributeRegistry[spec.Type]
	if !ok {
		t.Fatalf("attribute %s is not registered", spec.Type)
	}
	c, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("constructing %s: %v", spec.Type, err)
	}
	vals, err := c.Compute(recs, spec.Field)
	if err != nil {
		t.Fatalf("%s.Compute: %v", spec.Type, err)
	}
	return emitted(vals)
}

// winEmitted applies one window operator to rows and collects the values
// it wrote into the label column. Rows the operator left untouched (a
// lag with no lookback row) contribute nothing.
func winEmitted(t *testing.T, w *types.Window, rows []map[string]any) emitted {
	t.Helper()
	if err := window.Apply(context.Background(), rows, []*types.Window{w}); err != nil {
		t.Fatalf("window.Apply(%s): %v", w.Type, err)
	}
	var out emitted
	for _, r := range rows {
		raw, present := r[w.Label]
		if !present || raw == nil {
			continue
		}
		v, ok := numericFromAny(raw)
		if !ok {
			t.Errorf("%s wrote %T into its output column; the value domain cannot be checked", w.Type, raw)
			continue
		}
		out = append(out, v)
	}
	return out
}

// featEmitted applies one feature operator and collects the values it
// wrote into the named output columns. Null-masked rows contribute
// nothing (NumericValue reports them absent).
func featEmitted(t *testing.T, feat *types.Feature, schema *encoding.Schema, recs []*Record, cols ...string) emitted {
	t.Helper()
	view := make([]feature.Record, len(recs))
	for i, r := range recs {
		view[i] = r
	}
	if err := feature.Apply(view, []*types.Feature{feat}, schema); err != nil {
		t.Fatalf("feature.Apply(%s): %v", feat.Type, err)
	}
	var out emitted
	for _, r := range recs {
		for _, col := range cols {
			if v, ok := r.NumericValue(col); ok {
				out = append(out, v)
			}
		}
	}
	return out
}

func numericFromAny(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

// ---------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------

// emitsNumericRecords returns records over numericSchema() with a spread
// that includes zero and a negative value, so an operator that mishandles
// sign shows it.
func emitsNumericRecords(schema *encoding.Schema) []*Record {
	return makeRecords(schema, "score", []float64{-4, 0, 2, 4, 4, 5, 7, 9})
}

// emitsWindowRows returns ordered rows whose measure goes NEGATIVE, so a
// rank-family operator declaring an unsigned EmitsType cannot pass by
// echoing its input.
func emitsWindowRows() []map[string]any {
	return []map[string]any{
		{"ts": 1.0, "x": -30.0},
		{"ts": 2.0, "x": -10.0},
		{"ts": 3.0, "x": -10.0},
		{"ts": 4.0, "x": 20.0},
		{"ts": 5.0, "x": 40.0},
	}
}

func emitsIntPtr(v int) *int { return &v }

// widestSetRung returns the widest registered set field type, found by
// walking the type-byte space rather than naming a rung. A new, wider
// rung therefore presses the ATTR_SET_POPCOUNT declaration through this
// gate the day it lands, exactly as set_u256 did to the old "u8".
func widestEmitsSetRung(t *testing.T) encoding.FieldType {
	t.Helper()
	widest := encoding.FieldType(0)
	var members uint32
	for i := range 256 {
		ft := encoding.FieldType(i)
		if !ft.IsKnown() {
			break
		}
		if ft.IsSet() && ft.MaxSetEntries() > members {
			widest, members = ft, ft.MaxSetEntries()
		}
	}
	if members == 0 {
		t.Fatal("no registered set field type found")
	}
	return widest
}

// emitsSetSchema returns a one-field set schema at the widest registered
// rung with a full dictionary. Paired with emitsSetRecords it produces
// popcount 256 today — the value that broke ATTR_SET_POPCOUNT's old
// "u8" declaration.
func emitsSetSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	ft := widestEmitsSetRung(t)
	d := encoding.NewDictionary()
	for i := range int(ft.MaxSetEntries()) {
		if _, err := d.Add("m" + strconv.Itoa(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: ft, Nullable: true, Dictionary: d},
	}}
}

func emitsSetRecords(t *testing.T, schema *encoding.Schema) []*Record {
	t.Helper()
	var full encoding.SetMask
	for i := range int(widestEmitsSetRung(t).MaxSetEntries()) {
		full = full.WithBit(i)
	}
	var one encoding.SetMask
	one = one.WithBit(0)
	return []*Record{
		NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": full}),
		NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": one}),
		NewRecordWithWide(schema, map[string]float64{}, nil, map[string]any{"tags": encoding.SetMask{}}),
	}
}

// emitsRegRecords builds a noise-free linear fixture for the ATTR_REG_*
// family.
func emitsRegRecords(schema *encoding.Schema) []*Record {
	x1 := []float64{1, 2, 3, 4, 5, 6}
	x2 := []float64{0, 1, 1, 2, 3, 5}
	y := make([]float64, len(x1))
	for i := range x1 {
		y[i] = 2 + 3*x1[i] + 5*x2[i]
	}
	return makeRegRecords(schema, x1, x2, y)
}

// ---------------------------------------------------------------------
// The probe table — one entry per EmitsType declaration
// ---------------------------------------------------------------------

var emitsTypeProbes = map[string]func(t *testing.T) emitted{
	// --- attributes -------------------------------------------------
	string(types.ATTR_ZSCORE): func(t *testing.T) emitted {
		s := numericSchema()
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_ZSCORE, Field: "score", Label: "out"}, s, emitsNumericRecords(s))
	},
	string(types.ATTR_TSCORE): func(t *testing.T) emitted {
		s := numericSchema()
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_TSCORE, Field: "score", Label: "out"}, s, emitsNumericRecords(s))
	},
	string(types.ATTR_NORMALIZED): func(t *testing.T) emitted {
		s := numericSchema()
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_NORMALIZED, Field: "score", Label: "out"}, s, emitsNumericRecords(s))
	},
	string(types.ATTR_FORMULA): func(t *testing.T) emitted {
		s := numericSchema()
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_FORMULA, Field: "score", Label: "out", Expression: "score * 2.5"}, s, emitsNumericRecords(s))
	},
	string(types.ATTR_PERCENTILE): func(t *testing.T) emitted {
		s := numericSchema()
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "score", Label: "out"}, s, emitsNumericRecords(s))
	},
	string(types.ATTR_DATE_PART): func(t *testing.T) emitted {
		s := dateSchema()
		// 19797 = 2024-03-15; year_month_day is the widest encoding the
		// operator emits (YYYYMMDD), so it presses the declaration hardest.
		recs := makeRecords(s, "enrolled", []float64{19797, 19692})
		return attrEmitted(t, &types.Attribute{
			Type: types.ATTR_DATE_PART, Field: "enrolled", Label: "out",
			Params: json.RawMessage(`{"part":"year_month_day"}`),
		}, s, recs)
	},
	string(types.ATTR_REG_FITTED): func(t *testing.T) emitted {
		s := regSchema()
		return attrEmitted(t, &types.Attribute{
			Type: types.ATTR_REG_FITTED, Label: "out", Target: "y", Predictors: []string{"x1", "x2"},
		}, s, emitsRegRecords(s))
	},
	string(types.ATTR_REG_RESIDUAL): func(t *testing.T) emitted {
		s := regSchema()
		return attrEmitted(t, &types.Attribute{
			Type: types.ATTR_REG_RESIDUAL, Label: "out", Target: "y", Predictors: []string{"x1", "x2"},
		}, s, emitsRegRecords(s))
	},
	string(types.ATTR_REG_LEVERAGE): func(t *testing.T) emitted {
		s := regSchema()
		return attrEmitted(t, &types.Attribute{
			Type: types.ATTR_REG_LEVERAGE, Label: "out", Target: "y", Predictors: []string{"x1", "x2"},
		}, s, emitsRegRecords(s))
	},
	string(types.ATTR_SET_POPCOUNT): func(t *testing.T) emitted {
		s := emitsSetSchema(t)
		return attrEmitted(t, &types.Attribute{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "out"}, s, emitsSetRecords(t, s))
	},
	string(types.ATTR_SET_HAS): func(t *testing.T) emitted {
		s := emitsSetSchema(t)
		return attrEmitted(t, &types.Attribute{
			Type: types.ATTR_SET_HAS, Field: "tags", Label: "out",
			Params: json.RawMessage(`{"label":"m0"}`),
		}, s, emitsSetRecords(t, s))
	},

	// --- window operators -------------------------------------------
	string(types.WIN_LAG): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_LAG, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"offset":1}`),
		}, emitsWindowRows())
	},
	string(types.WIN_LEAD): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_LEAD, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"offset":1}`),
		}, emitsWindowRows())
	},
	string(types.WIN_ROW_NUMBER): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_ROW_NUMBER, Label: "out",
			OrderBy: []types.OrderKey{{Field: "x"}},
		}, emitsWindowRows())
	},
	string(types.WIN_RANK): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_RANK, Label: "out",
			OrderBy: []types.OrderKey{{Field: "x"}},
		}, emitsWindowRows())
	},
	string(types.WIN_DENSE_RANK): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_DENSE_RANK, Label: "out",
			OrderBy: []types.OrderKey{{Field: "x"}},
		}, emitsWindowRows())
	},
	string(types.WIN_RUNNING_SUM): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_RUNNING_SUM, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: emitsIntPtr(0)},
		}, emitsWindowRows())
	},
	string(types.WIN_RUNNING_AVG): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_RUNNING_AVG, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Following: emitsIntPtr(0)},
		}, emitsWindowRows())
	},
	string(types.WIN_MOVING_AVG): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_MOVING_AVG, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows", Preceding: emitsIntPtr(2), Following: emitsIntPtr(0)},
		}, emitsWindowRows())
	},
	string(types.WIN_EWMA): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_EWMA, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Frame:   &types.FrameSpec{Mode: "rows"},
			Params:  json.RawMessage(`{"alpha":0.5}`),
		}, emitsWindowRows())
	},
	string(types.WIN_PCT_CHANGE): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_PCT_CHANGE, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"periods":1}`),
		}, emitsWindowRows())
	},
	string(types.WIN_DELTA): func(t *testing.T) emitted {
		return winEmitted(t, &types.Window{
			Type: types.WIN_DELTA, Field: "x", Label: "out",
			OrderBy: []types.OrderKey{{Field: "ts"}},
			Params:  json.RawMessage(`{"periods":1}`),
		}, emitsWindowRows())
	},

	// --- feature operators ------------------------------------------
	string(types.FEAT_LOG): func(t *testing.T) emitted {
		s := numericSchema()
		recs := makeRecords(s, "score", []float64{0, 2, 4, 9})
		return featEmitted(t, &types.Feature{Type: types.FEAT_LOG, Field: "score", Label: "out"}, s, recs, "out")
	},
	string(types.FEAT_SQRT): func(t *testing.T) emitted {
		s := numericSchema()
		recs := makeRecords(s, "score", []float64{0, 2, 4, 9})
		return featEmitted(t, &types.Feature{Type: types.FEAT_SQRT, Field: "score", Label: "out"}, s, recs, "out")
	},
	string(types.FEAT_BUCKETIZE): func(t *testing.T) emitted {
		s := numericSchema()
		recs := emitsNumericRecords(s)
		return featEmitted(t, &types.Feature{
			Type: types.FEAT_BUCKETIZE, Field: "score", Label: "out",
			Params: json.RawMessage(`{"boundaries":[0,4,8]}`),
		}, s, recs, "out")
	},
	string(types.FEAT_POLY): func(t *testing.T) emitted {
		s := numericSchema()
		recs := emitsNumericRecords(s)
		return featEmitted(t, &types.Feature{
			Type: types.FEAT_POLY, Field: "score", Label: "out",
			Params: json.RawMessage(`{"degree":3}`),
		}, s, recs, "out_2", "out_3")
	},
	string(types.FEAT_FREQUENCY_ENCODE): func(t *testing.T) emitted {
		s := categoricalSchema()
		recs := makeRecords(s, "brand", []float64{0, 1, 1, 2, 2, 2})
		return featEmitted(t, &types.Feature{
			Type: types.FEAT_FREQUENCY_ENCODE, Field: "brand", Label: "out",
		}, s, recs, "out")
	},
	string(types.FEAT_TARGET_ENCODE): func(t *testing.T) emitted {
		s := categoricalSchema()
		recs := make([]*Record, 6)
		brands := []float64{0, 1, 1, 2, 2, 2}
		scores := []float64{-4, 0, 10, 20, 30, 40}
		for i := range recs {
			recs[i] = NewRecord(s, map[string]float64{"brand": brands[i], "score": scores[i]})
		}
		return featEmitted(t, &types.Feature{
			Type: types.FEAT_TARGET_ENCODE, Field: "brand", Label: "out",
			Params: json.RawMessage(`{"target":"score","smoothing":1.0}`),
		}, s, recs, "out")
	},
	string(types.FEAT_TRAIN_TEST_SPLIT): func(t *testing.T) emitted {
		s := numericSchema()
		recs := emitsNumericRecords(s)
		return featEmitted(t, &types.Feature{
			Type: types.FEAT_TRAIN_TEST_SPLIT, Label: "out",
			Params: json.RawMessage(`{"ratios":[0.6,0.2,0.2],"seed":7}`),
		}, s, recs, "out")
	},
}

// ---------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------

// manifestOperatorsWithEmitsType returns every manifest operator entry,
// across all six component categories, keyed by name.
func manifestOperatorEntries() map[string]descriptor.Operator {
	m := descriptor.BuildManifest()
	out := make(map[string]descriptor.Operator)
	groups := [][]descriptor.Operator{
		m.Components.Aggregators,
		m.Components.Attributes,
		m.Components.Filterers,
		m.Components.Groupers,
		m.Components.Windows,
		m.Components.Features,
	}
	for _, g := range groups {
		for _, op := range g {
			out[op.Name] = op
		}
	}
	return out
}

// TestManifestEmitsTypeHoldsAtRuntime is the runtime consumer EmitsType
// never had. See the file header for the exact scope and its limits.
//
// This gate is load-bearing: it is the only thing anywhere that compares
// a declared EmitsType against a produced value.
func TestManifestEmitsTypeHoldsAtRuntime(t *testing.T) {
	entries := manifestOperatorEntries()

	declared := make([]string, 0, len(entries))
	for name, op := range entries {
		if op.EmitsType != "" {
			declared = append(declared, name)
		}
	}
	sort.Strings(declared)
	if len(declared) == 0 {
		t.Fatal("no operator declares an EmitsType; the manifest walk is broken")
	}

	t.Run("every_declaration_is_probed", func(t *testing.T) {
		for _, name := range declared {
			if _, ok := emitsTypeProbes[name]; !ok {
				t.Errorf("%s declares EmitsType %q with no probe in emitsTypeProbes — "+
					"an unprobed declaration is manifest prose nothing can contradict; add a probe",
					name, entries[name].EmitsType)
			}
		}
	})

	t.Run("no_probe_outlives_its_declaration", func(t *testing.T) {
		for name := range emitsTypeProbes {
			op, ok := entries[name]
			if !ok {
				t.Errorf("emitsTypeProbes has a probe for %q, which is not a registered operator", name)
				continue
			}
			if op.EmitsType == "" {
				t.Errorf("emitsTypeProbes has a probe for %s, which declares no EmitsType; "+
					"drop the probe or restore the declaration", name)
			}
		}
	})

	for _, name := range declared {
		probe, ok := emitsTypeProbes[name]
		if !ok {
			continue
		}
		t.Run(name, func(t *testing.T) {
			checkEmitsDomain(t, name, entries[name].EmitsType, probe(t))
		})
	}
}
