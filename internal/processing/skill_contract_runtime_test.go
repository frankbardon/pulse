package processing

// skill_contract_runtime_test.go pins manifest declarations the skills
// repeat against what the engine actually does: the GROUP_RANGE /
// GROUP_ROUNDED / GROUP_QUANTILE `interval` default, the WIN_LAG /
// WIN_LEAD offset floor and output type, and the ATTR_REG_LEVERAGE range
// for a row outside the fit. Each declaration was once wrong in the
// manifest (and so in every skill that copied it); these tests make the
// next drift fail instead of ship.

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/types"
)

// effectiveInterval reads the interval a constructed grouper really
// uses. A grouper declaring an `interval` slot param with no case here
// fails the test, so a new one cannot slip past unpinned.
func effectiveInterval(t *testing.T, g Grouper) (float64, bool) {
	t.Helper()
	switch v := g.(type) {
	case *rangeGrouper:
		return v.interval, true
	case *roundedGrouper:
		return v.interval, true
	case *quantileGrouper:
		return float64(v.buckets), true
	}
	return 0, false
}

func paramDefaultFloat(t *testing.T, d any) float64 {
	t.Helper()
	switch v := d.(type) {
	case int:
		return float64(v)
	case float64:
		return v
	}
	t.Fatalf("interval Default %v (%T) is not numeric", d, d)
	return 0
}

// TestGrouperIntervalDeclarationMatchesRuntime: for every grouper whose
// manifest declares an `interval` slot param, the runtime constructs
// with the slot absent (0) and with a negative value, and both resolve
// to the declared Default — so the param is not Required, and "≤ 0 is
// treated as the default" is the truth the description must state.
func TestGrouperIntervalDeclarationMatchesRuntime(t *testing.T) {
	entries := manifestOperatorEntries()
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	pinned := 0
	for name, op := range entries {
		if op.Category != "grouper" {
			continue
		}
		var param *descriptor.Param
		for i := range op.Params {
			if op.Params[i].Name == "interval" && op.Params[i].Slot {
				param = &op.Params[i]
			}
		}
		if param == nil {
			continue
		}
		pinned++
		t.Run(name, func(t *testing.T) {
			if param.Required {
				t.Errorf("%s declares interval Required, but the runtime constructs without it", name)
			}
			if param.Default == nil {
				t.Fatalf("%s declares no interval Default; the runtime falls back to one", name)
			}
			want := paramDefaultFloat(t, param.Default)
			if !strings.Contains(param.Description, "≤ 0 is treated as") && name != string(types.GROUP_QUANTILE) {
				t.Errorf("%s interval description %q does not say how ≤ 0 is treated", name, param.Description)
			}
			factory, ok := grouperRegistry[types.GroupType(name)]
			if !ok {
				t.Fatalf("%s is not in grouperRegistry", name)
			}
			for _, in := range []float64{0, -3} {
				g, err := factory(&types.Group{Type: types.GroupType(name), Field: "x", Interval: in}, schema)
				if err != nil {
					t.Fatalf("interval %v: runtime refused (%v), so the declaration's default is a fiction", in, err)
				}
				got, ok := effectiveInterval(t, g)
				if !ok {
					t.Fatalf("%s (%T) has no effectiveInterval case; add one", name, g)
				}
				if got != want {
					t.Errorf("interval %v resolves to %v at runtime, declared Default %v", in, got, want)
				}
			}
		})
	}
	if pinned == 0 {
		t.Fatal("no grouper declares an interval slot param; the manifest walk is broken")
	}
}

// TestWinLagLead_DeclarationMatchesRuntime: the offset floor is 0 (0
// reads the row itself, -1 is refused), the declared default matches
// the runtime's, and the output is a verbatim copy of the source cell —
// so the manifest declares no fixed EmitsType.
func TestWinLagLead_DeclarationMatchesRuntime(t *testing.T) {
	entries := manifestOperatorEntries()
	big := uint64(1)<<53 + 1 // not representable as float64
	for _, typ := range []types.WindowType{types.WIN_LAG, types.WIN_LEAD} {
		t.Run(string(typ), func(t *testing.T) {
			op := entries[string(typ)]
			if op.EmitsType != "" {
				t.Errorf("%s declares EmitsType %q, but it emits the source cell verbatim", typ, op.EmitsType)
			}
			if !strings.Contains(op.EmitsTypeNote, "source field type") {
				t.Errorf("%s EmitsTypeNote %q does not say it matches the source field type", typ, op.EmitsTypeNote)
			}
			var offset *descriptor.Param
			for i := range op.Params {
				if op.Params[i].Name == "offset" {
					offset = &op.Params[i]
				}
			}
			if offset == nil {
				t.Fatalf("%s declares no offset param", typ)
			}
			if !strings.Contains(offset.Description, "≥ 0") {
				t.Errorf("%s offset description %q does not state the ≥ 0 floor", typ, offset.Description)
			}

			rows := func() []map[string]any {
				return []map[string]any{
					{"ts": 1.0, "x": big, "d": "2024-01-01"},
					{"ts": 2.0, "x": big + 2, "d": "2024-01-02"},
				}
			}
			apply := func(field, params string) ([]map[string]any, error) {
				r := rows()
				err := window.Apply(context.Background(), r, []*types.Window{{
					Type: typ, Field: field, Label: "out",
					OrderBy: []types.OrderKey{{Field: "ts"}}, Params: json.RawMessage(params),
				}})
				return r, err
			}

			// Floor: -1 refused, 0 admitted and reads the row itself.
			if _, err := apply("x", `{"offset":-1}`); err == nil {
				t.Errorf("offset -1 accepted; the floor is 0")
			}
			r, err := apply("x", `{"offset":0}`)
			if err != nil {
				t.Fatalf("offset 0 refused (%v), but the declaration admits it", err)
			}
			for i, row := range r {
				if !reflect.DeepEqual(row["out"], row["x"]) {
					t.Errorf("offset 0 row %d: out %v (%T), want its own cell %v", i, row["out"], row["out"], row["x"])
				}
			}

			// Default offset matches the declaration.
			def := int(paramDefaultFloat(t, offset.Default))
			r, err = apply("x", `{"default":"none"}`)
			if err != nil {
				t.Fatal(err)
			}
			if def != 1 {
				t.Fatalf("declared offset Default %d; this probe is written for 1", def)
			}
			// src is the row that receives a copy, edge the row whose
			// offset falls outside the partition. Lead: row 0 reads row 1;
			// lag: row 1 reads row 0.
			src, edge := 0, 1
			if typ == types.WIN_LAG {
				src, edge = 1, 0
			}
			// Verbatim copy: a u64 past 2^53 keeps its exact word and type.
			from := r[1-src]
			if got := r[src]["out"]; !reflect.DeepEqual(got, from["x"]) {
				t.Errorf("copied cell %v (%T), want the source cell %v (%T) verbatim", got, got, from["x"], from["x"])
			}
			// params.default is emitted as given, whatever its JSON type.
			if got := r[edge]["out"]; got != "none" {
				t.Errorf("edge row out = %v (%T), want params.default \"none\" verbatim", got, got)
			}

			// A non-numeric source cell is copied as-is too.
			r, err = apply("d", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if got := r[src]["out"]; got != r[1-src]["d"] {
				t.Errorf("date cell copied as %v (%T), want %v", got, got, r[1-src]["d"])
			}
		})
	}
}

// TestAttrRegLeverage_OutOfFitRowCanExceedOne: a row with a null target
// is outside the fit but is still scored from its predictors; far from
// the fit rows its leverage exceeds 1, so the declaration must not claim
// [0, 1]. The in-fit rows stay within [1/n, 1].
func TestAttrRegLeverage_OutOfFitRowCanExceedOne(t *testing.T) {
	op := manifestOperatorEntries()[string(types.ATTR_REG_LEVERAGE)]
	if strings.Contains(op.Description, "[0, 1]") || strings.Contains(op.EmitsTypeNote, "[0, 1]") {
		t.Errorf("ATTR_REG_LEVERAGE still declares a [0, 1] range: %q / %q", op.Description, op.EmitsTypeNote)
	}
	if !strings.Contains(op.Description, "can exceed 1") {
		t.Errorf("ATTR_REG_LEVERAGE description %q does not say an out-of-fit row can exceed 1", op.Description)
	}

	schema := regNullSchema()
	const n = 20
	recs := make([]*Record, 0, n+1)
	for i := range n {
		f := float64(i)
		x1, x2 := f, math.Cos(f)
		recs = append(recs, NewRecordWithNulls(schema, map[string]float64{"x1": x1, "x2": x2, "y": 1 + x1 - x2 + 0.1*math.Sin(3*f)}, nil))
	}
	// Outside the fit: null target, predictors far beyond the fit rows.
	recs = append(recs, NewRecordWithNulls(schema, map[string]float64{"x1": 1000, "x2": 50}, map[string]bool{"y": true}))

	c := makeRegAttr(t, types.ATTR_REG_LEVERAGE, schema, &types.Attribute{Target: "y", Predictors: []string{"x1", "x2"}})
	vals, err := c.Compute(recs, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if vals[i] < 1.0/n-1e-12 || vals[i] > 1+1e-12 {
			t.Errorf("in-fit row %d leverage %v outside [1/n, 1]", i, vals[i])
		}
	}
	if out := vals[n]; !(out > 1) {
		t.Errorf("out-of-fit row leverage = %v, want > 1", out)
	}
}
