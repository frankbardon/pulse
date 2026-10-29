package descriptor

import (
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func layoutNames(l []MaterializeOutputField) []string {
	out := make([]string, len(l))
	for i, f := range l {
		out[i] = f.Field.Name
	}
	return out
}

// TestMaterializeOutputLayout_Resolves: absent output is every source
// field then every derived column; columns pick, order, rename and
// override; exclude keeps source-then-derived order; each column reads
// the source field or derived column its from names.
func TestMaterializeOutputLayout_Resolves(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeU8, Description: "A"},
		{Name: "b", Type: encoding.FieldTypeF64, Nullable: true, Description: "B"},
	}}
	cols := []MaterializeColumn{{Name: "d", Description: "derived d"}}

	full, err := MaterializeOutputLayout(&types.Request{}, schema, cols, nil)
	if err != nil || !slices.Equal(layoutNames(full), []string{"a", "b", "d"}) {
		t.Fatalf("absent output: %v %v", layoutNames(full), err)
	}
	if d := full[2]; d.Derived != 0 || d.Source != -1 || d.Field.Type != encoding.FieldTypeF64 || !d.Field.Nullable || d.Field.Description != "derived d" {
		t.Errorf("derived column = %+v", d)
	}

	desc, null := "renamed b", false
	got, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "d", As: "dd"}, {From: "b", Description: &desc, Nullable: &null}, {From: "a"},
	}}}, schema, cols, nil)
	if err != nil || !slices.Equal(layoutNames(got), []string{"dd", "b", "a"}) {
		t.Fatalf("columns: %v %v", layoutNames(got), err)
	}
	if got[0].Derived != 0 || got[1].Source != 1 || got[2].Source != 0 {
		t.Errorf("reads = %+v", got)
	}
	if got[1].Field.Description != desc || got[1].Field.Nullable || got[1].Field.Type != encoding.FieldTypeF64 {
		t.Errorf("b override = %+v", got[1].Field)
	}
	if schema.Fields[1].Description != "B" || !schema.Fields[1].Nullable {
		t.Error("the layout mutated the source schema")
	}

	ex, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Exclude: []string{"a"}}}, schema, cols, nil)
	if err != nil || !slices.Equal(layoutNames(ex), []string{"b", "d"}) {
		t.Fatalf("exclude: %v %v", layoutNames(ex), err)
	}
	if _, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Exclude: []string{"a", "b", "d"}}}, schema, cols, nil); err == nil || err.Code != errors.PULSE_MATERIALIZE_OUTPUT_INVALID {
		t.Fatalf("exclude all: %v", err)
	}
}

// TestOutputSlotRefusal_EveryRoot: SlotRefusal refuses a set output on
// a Request, a Compose slot and fit, and a chain stage — located — and
// MaterializeSlotGate accepts it.
func TestOutputSlotRefusal_EveryRoot(t *testing.T) {
	t.Parallel()

	withOut := func() *types.Request { return &types.Request{Output: &types.MaterializeOutput{}} }
	cases := []struct {
		name string
		v    any
		key  string
		idx  int
	}{
		{"request", withOut(), "", 0},
		{"compose slot", &types.ComposedRequest{Requests: []*types.Request{{}, withOut()}}, "request", 1},
		{"compose fit", &types.ComposedRequest{Fits: []types.FitSpec{{Name: "f", Request: withOut()}}}, "fit", 0},
		{"chain stage", &types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{}}, {Request: withOut()}}}, "stage", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := SlotRefusal(c.v, nil)
			ce, ok := err.(*errors.CodedError)
			if !ok || ce.Code != errors.PULSE_MATERIALIZE_OUTPUT_ONLY || ce.Details["slot"] != "output" {
				t.Fatalf("err = %v", err)
			}
			if c.key != "" && ce.Details[c.key] != c.idx {
				t.Errorf("details.%s = %v, want %d", c.key, ce.Details[c.key], c.idx)
			}
		})
	}
	if err := MaterializeSlotGate(withOut(), nil); err != nil {
		t.Fatalf("materialize gate refused output: %v", err)
	}
	if err := SlotRefusal(&types.Request{}, nil); err != nil {
		t.Fatalf("no output refused: %v", err)
	}
}

// TestMaterializeOutputLayout_Casts: a `type` retypes the written field
// (a decimal with its default or declared shape; a set widen keeps its
// dictionary, any other target drops it), records the field read as
// Cast.From and marks the column Rebuild; the column's own type (and a
// decimal's own shape) is no cast. MaterializePlan stamps the zone on a
// date ↔ datetime cast only: time_zone, else the default, else UTC.
func TestMaterializeOutputLayout_Casts(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	if _, err := dict.Add("x"); err != nil {
		t.Fatal(err)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU32, Description: "Count of the source"},
		{Name: "p", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2, Description: "Price of the source"},
		{Name: "s", Type: encoding.FieldTypeSetU8, Dictionary: dict, Description: "Set of the source"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Description: "Instant of the source"},
	}}
	cols := []MaterializeColumn{{Name: "d", Description: "derived d"}}
	six := 6
	req := &types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "n", Type: "decimal128"},
		{From: "p", As: "p6", Type: "decimal128", Scale: &six},
		{From: "p", As: "p_same", Type: "decimal128"},
		{From: "s", Type: "set_u64"},
		{From: "d", Type: "u8"},
		{From: "n", As: "n_same", Type: "u32"},
		{From: "ts", As: "day", Type: "date"},
	}}}
	layout, ce := MaterializeOutputLayout(req, schema, cols, nil)
	if ce != nil {
		t.Fatal(ce)
	}
	want := []struct {
		typ        encoding.FieldType
		prec, scal uint8
		cast       bool
	}{
		{encoding.FieldTypeDecimal128, 38, 0, true},
		{encoding.FieldTypeDecimal128, 10, 6, true},
		{encoding.FieldTypeDecimal128, 10, 2, false},
		{encoding.FieldTypeSetU64, 0, 0, true},
		{encoding.FieldTypeU8, 0, 0, true},
		{encoding.FieldTypeU32, 0, 0, false},
		{encoding.FieldTypeDate, 0, 0, true},
	}
	for i, w := range want {
		f := layout[i]
		if f.Field.Type != w.typ || f.Field.Precision != w.prec || f.Field.Scale != w.scal || (f.Cast != nil) != w.cast || f.Rebuild != w.cast {
			t.Errorf("column %d = %+v (cast %+v), want %+v", i, f.Field, f.Cast, w)
		}
	}
	if layout[3].Field.Dictionary != dict {
		t.Errorf("a set widen dropped its dictionary")
	}
	if c := layout[4].Cast; c == nil || c.From.Type != encoding.FieldTypeF64 || c.From.Name != "d" {
		t.Errorf("derived cast From = %+v, want the derived f64", c)
	}
	if c := layout[1].Cast; c == nil || c.From.Scale != 2 {
		t.Errorf("decimal cast From = %+v, want the source shape", c)
	}

	for _, z := range []struct{ tz, def, want string }{
		{"Europe/Berlin", "Asia/Tokyo", "Europe/Berlin"},
		{"", "Asia/Tokyo", "Asia/Tokyo"},
		{"", "", "UTC"},
	} {
		// The plan derives its own columns: no derived "d" here.
		r := &types.Request{TimeZone: z.tz, Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "n", Type: "decimal128"}, {From: "ts", As: "day", Type: "date"},
		}}}
		_, planned, _, ce := MaterializePlan(r, schema, nil, "s.pulse", z.def)
		if ce != nil {
			t.Fatal(ce)
		}
		for i, f := range planned {
			wantZone := ""
			if i == 1 {
				wantZone = z.want
			}
			if f.Cast != nil && f.Cast.Zone != wantZone {
				t.Errorf("tz %q default %q: column %d zone = %q, want %q", z.tz, z.def, i, f.Cast.Zone, wantZone)
			}
		}
	}
	if layout[6].Cast.Zone != "" {
		t.Errorf("MaterializeOutputLayout set a zone; only MaterializePlan does")
	}
}

// TestMaterializeOutputLayout_Recode: a recode rewrites the written
// field's dictionary (relabel in place, merge at the first label's
// position, null entries left out, order applied), marks the column
// Rebuild, hands the run its label map (never nil: an order-only recode
// carries an empty one) and feeds the cast the recoded dictionary — a
// merge that brings a 257-label categorical to 256 narrows to
// categorical_u8. The source schema's dictionary is never mutated.
func TestMaterializeOutputLayout_Recode(t *testing.T) {
	t.Parallel()

	small := encoding.NewDictionary()
	for _, l := range []string{"a", "b", "c"} {
		_, _ = small.Add(l)
	}
	wide := encoding.NewDictionary()
	for i := 0; i < 257; i++ {
		_, _ = wide.Add("L" + strconv.Itoa(i))
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: small, Description: "Key of the source"},
		{Name: "w", Type: encoding.FieldTypeCategoricalU16, Dictionary: wide, Description: "Wide key of the source"},
	}}
	str := func(s string) *string { return &s }
	req := &types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "k", As: "relabel", Recode: &types.MaterializeRecode{Map: map[string]*string{"b": str("B")}}},
		{From: "k", As: "merge", Recode: &types.MaterializeRecode{Map: map[string]*string{"c": str("a"), "b": nil}}},
		{From: "k", As: "order", Recode: &types.MaterializeRecode{Order: []string{"c", "a", "b"}}},
		{From: "w", Type: "categorical_u8", Recode: &types.MaterializeRecode{Map: map[string]*string{"L256": str("L0")}}},
		{From: "k", As: "plain"},
	}}}
	layout, ce := MaterializeOutputLayout(req, schema, nil, nil)
	if ce != nil {
		t.Fatal(ce)
	}
	for i, want := range [][]string{{"a", "B", "c"}, {"a"}, {"c", "a", "b"}} {
		if got := layout[i].Field.Dictionary.Values(); !slices.Equal(got, want) {
			t.Errorf("%s dictionary = %v, want %v", layout[i].Field.Name, got, want)
		}
		if !layout[i].Rebuild || layout[i].Recode == nil || layout[i].Cast != nil {
			t.Errorf("%s rebuild=%v recode=%v cast=%v, want a rebuilt uncast recode", layout[i].Field.Name, layout[i].Rebuild, layout[i].Recode, layout[i].Cast)
		}
	}
	if m := layout[1].Recode; len(m) != 2 || m["b"] != nil || m["c"] == nil || *m["c"] != "a" {
		t.Errorf("merge map = %v", m)
	}
	w := layout[3]
	if w.Field.Type != encoding.FieldTypeCategoricalU8 || w.Field.Dictionary.Count() != 256 || w.Cast == nil || w.Cast.From.Dictionary.Count() != 256 {
		t.Errorf("w = %s, %d labels, cast %+v", w.Field.Type, w.Field.Dictionary.Count(), w.Cast)
	}
	if layout[4].Rebuild || layout[4].Recode != nil || layout[4].Field.Dictionary != small {
		t.Errorf("plain column touched: %+v", layout[4])
	}
	if small.Count() != 3 || wide.Count() != 257 {
		t.Errorf("source dictionaries mutated: %d / %d", small.Count(), wide.Count())
	}
}
