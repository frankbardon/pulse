package descriptor

import (
	"slices"
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

	full, err := MaterializeOutputLayout(&types.Request{}, schema, cols)
	if err != nil || !slices.Equal(layoutNames(full), []string{"a", "b", "d"}) {
		t.Fatalf("absent output: %v %v", layoutNames(full), err)
	}
	if d := full[2]; d.Derived != 0 || d.Source != -1 || d.Field.Type != encoding.FieldTypeF64 || !d.Field.Nullable || d.Field.Description != "derived d" {
		t.Errorf("derived column = %+v", d)
	}

	desc, null := "renamed b", false
	got, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "d", As: "dd"}, {From: "b", Description: &desc, Nullable: &null}, {From: "a"},
	}}}, schema, cols)
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

	ex, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Exclude: []string{"a"}}}, schema, cols)
	if err != nil || !slices.Equal(layoutNames(ex), []string{"b", "d"}) {
		t.Fatalf("exclude: %v %v", layoutNames(ex), err)
	}
	if _, err := MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Exclude: []string{"a", "b", "d"}}}, schema, cols); err == nil || err.Code != errors.PULSE_MATERIALIZE_OUTPUT_INVALID {
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
