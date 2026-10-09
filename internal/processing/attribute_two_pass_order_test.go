package processing

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// twoPassOrderSchema is the fixture for the declared-order parity
// suite: two numerics, a date and a set column, so every built-in
// row-local attribute kind has an input it accepts.
func twoPassOrderSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, Description: "input x"},
			{Name: "y", Type: encoding.FieldTypeF64, Description: "input y"},
			{Name: "d", Type: encoding.FieldTypeDate, Description: "epoch days"},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict},
			{Name: "c", Type: encoding.FieldTypeU8, Description: "integer code"},
		},
	}
}

// twoPassOrderRecords builds a FRESH record slice on every call so the
// streaming and buffered runs never share injected attribute labels.
func twoPassOrderRecords(schema *encoding.Schema) []*Record {
	const n = 40
	out := make([]*Record, n)
	for i := 0; i < n; i++ {
		x := float64(i%7) + 0.5*float64(i)
		y := 3 + 1.7*x + math.Sin(float64(i))*4
		d := float64(i * 23) // spans several months
		mask := uint64((i*5 + 3) % 16)
		out[i] = NewRecordWithWide(schema,
			map[string]float64{"x": x, "y": y, "d": d, "c": float64(i % 5)}, nil,
			map[string]any{"tags": mask})
	}
	return out
}

// runDeclaredOrderParity drives req through the streaming orchestrator
// (asserting it really streamed) and the buffered path, on independent
// record sets, and compares every aggregate in data[0].
func runDeclaredOrderParity(t *testing.T, req *types.Request) {
	t.Helper()
	schema := twoPassOrderSchema(t)

	sp := NewProcessor(schema)
	sResp, err := sp.Process(context.Background(), req, NewSliceIterator(twoPassOrderRecords(schema)))
	if err != nil {
		t.Fatalf("streaming Process: %v", err)
	}
	if sp.LastPath() != PathStreaming {
		t.Fatalf("expected the streaming path, got %s", sp.LastPath())
	}
	bp := NewProcessor(schema)
	bResp, err := bp.processRecords(context.Background(), req, twoPassOrderRecords(schema))
	if err != nil {
		t.Fatalf("buffered processRecords: %v", err)
	}
	if len(sResp.Data) != 1 || len(bResp.Data) != 1 {
		t.Fatalf("data rows: streaming=%d buffered=%d", len(sResp.Data), len(bResp.Data))
	}
	nonZero := false
	for label, bv := range bResp.Data[0] {
		b, _ := bv.(float64)
		s, _ := sResp.Data[0][label].(float64)
		if math.Abs(s-b) > 1e-9*math.Max(1, math.Abs(b)) {
			t.Errorf("%s: streaming=%v buffered=%v", label, s, b)
		}
		if b != 0 {
			nonZero = true
		}
	}
	if !nonZero {
		t.Fatalf("degenerate fixture: every buffered aggregate is zero: %v", bResp.Data[0])
	}
}

func outAggs(label string) []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_MAX, Field: label, Label: "max"},
		{Type: types.AGG_MIN, Field: label, Label: "min"},
		{Type: types.AGG_SUM, Field: label, Label: "sum"},
	}
}

// TestTwoPass_DeclaredOrder_UpstreamRowLocalFeedsTwoPass is the E4-S8
// repro generalised: every built-in two-pass attribute × every built-in
// row-local attribute kind feeding it. The two-pass PrePass must see the
// row-local output exactly as the buffered arm's Compute does.
func TestTwoPass_DeclaredOrder_UpstreamRowLocalFeedsTwoPass(t *testing.T) {
	upstreams := map[string]*types.Attribute{
		"formula":      {Type: types.ATTR_FORMULA, Expression: "x * x + y", Label: "u"},
		"date_part":    {Type: types.ATTR_DATE_PART, Field: "d", Params: []byte(`{"part":"month"}`), Label: "u"},
		"set_popcount": {Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "u"},
		"set_has":      {Type: types.ATTR_SET_HAS, Field: "tags", Params: []byte(`{"label":"AMEX"}`), Label: "u"},
		"code_in":      {Type: types.ATTR_CODE_IN, Field: "c", Params: []byte(`{"codes":[1, 3]}`), Label: "u"},
	}
	twoPass := map[string]*types.Attribute{
		"zscore":       {Type: types.ATTR_ZSCORE, Field: "u", Label: "out"},
		"tscore":       {Type: types.ATTR_TSCORE, Field: "u", Label: "out"},
		"normalized":   {Type: types.ATTR_NORMALIZED, Field: "u", Label: "out"},
		"reg_fitted":   {Type: types.ATTR_REG_FITTED, Target: "y", Predictors: []string{"u", "x"}, Label: "out"},
		"reg_residual": {Type: types.ATTR_REG_RESIDUAL, Target: "y", Predictors: []string{"u", "x"}, Label: "out"},
		"reg_leverage": {Type: types.ATTR_REG_LEVERAGE, Target: "y", Predictors: []string{"u", "x"}, Label: "out"},
		"reg_target":   {Type: types.ATTR_REG_FITTED, Target: "u", Predictors: []string{"x"}, Label: "out"},
	}
	for un, up := range upstreams {
		for tn, tp := range twoPass {
			t.Run(fmt.Sprintf("%s_into_%s", un, tn), func(t *testing.T) {
				runDeclaredOrderParity(t, &types.Request{
					Attributes:   []*types.Attribute{up, tp},
					Aggregations: outAggs("out"),
				})
			})
		}
	}
}

// TestTwoPass_DeclaredOrder_Layers covers chains where a two-pass
// attribute reads (directly or through a row-local) another two-pass
// attribute's output, plus the unrelated-attribute and filter mixes.
func TestTwoPass_DeclaredOrder_Layers(t *testing.T) {
	cases := map[string]*types.Request{
		// row-local → two-pass → row-local → two-pass.
		"rl_tp_rl_tp": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "x * x + y", Label: "u"},
				{Type: types.ATTR_ZSCORE, Field: "u", Label: "z1"},
				{Type: types.ATTR_FORMULA, Expression: "z1 * z1 + x", Label: "w"},
				{Type: types.ATTR_NORMALIZED, Field: "w", Label: "out"},
			},
			Aggregations: outAggs("out"),
		},
		// two-pass reading a two-pass directly.
		"tp_of_tp": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_NORMALIZED, Field: "y", Label: "n"},
				{Type: types.ATTR_TSCORE, Field: "n", Label: "out"},
			},
			Aggregations: outAggs("out"),
		},
		// three layers, with an independent two-pass sharing layer 0.
		"three_layers": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "x + y", Label: "u"},
				{Type: types.ATTR_ZSCORE, Field: "u", Label: "z1"},
				{Type: types.ATTR_NORMALIZED, Field: "x", Label: "nx"},
				{Type: types.ATTR_FORMULA, Expression: "z1 * nx", Label: "w"},
				{Type: types.ATTR_REG_RESIDUAL, Target: "w", Predictors: []string{"x"}, Label: "r"},
				{Type: types.ATTR_ZSCORE, Field: "r", Label: "out"},
			},
			Aggregations: append(outAggs("out"),
				&types.Aggregation{Type: types.AGG_SUM, Field: "w", Label: "sum_w"},
				&types.Aggregation{Type: types.AGG_MAX, Field: "z1", Label: "max_z1"}),
		},
		// a row-local reading a two-pass output feeds only the aggregate.
		"rl_after_tp": {
			Attributes: []*types.Attribute{
				{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
				{Type: types.ATTR_FORMULA, Expression: "z * 2 + 1", Label: "out"},
			},
			Aggregations: outAggs("out"),
		},
		// filter gates every pass, including the layer passes.
		"filtered_layers": {
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "x", Values: []string{"2", "15"}},
			},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_FORMULA, Expression: "x * y", Label: "u"},
				{Type: types.ATTR_ZSCORE, Field: "u", Label: "z1"},
				{Type: types.ATTR_TSCORE, Field: "z1", Label: "out"},
			},
			Aggregations: outAggs("out"),
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) { runDeclaredOrderParity(t, req) })
	}
}

// TestTwoPass_DeclaredOrder_PassCount pins the pass plan: a request
// whose two-pass attributes read only source fields (or row-locals of
// source fields) keeps the historical two scans; each dependent two-pass
// layer adds exactly one.
func TestTwoPass_DeclaredOrder_PassCount(t *testing.T) {
	cases := []struct {
		name  string
		attrs []*types.Attribute
		want  int
	}{
		{"single", []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
		}, 1},
		{"independent_pair", []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
			{Type: types.ATTR_NORMALIZED, Field: "y", Label: "n"},
		}, 1},
		{"rowlocal_feeds", []*types.Attribute{
			{Type: types.ATTR_FORMULA, Expression: "x * 2", Label: "u"},
			{Type: types.ATTR_ZSCORE, Field: "u", Label: "z"},
		}, 1},
		{"tp_of_tp", []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
			{Type: types.ATTR_NORMALIZED, Field: "z", Label: "n"},
		}, 2},
		{"tp_via_rowlocal", []*types.Attribute{
			{Type: types.ATTR_ZSCORE, Field: "x", Label: "z"},
			{Type: types.ATTR_FORMULA, Expression: "z + 1", Label: "w"},
			{Type: types.ATTR_REG_FITTED, Target: "y", Predictors: []string{"w"}, Label: "f"},
		}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planTwoPassLayers(tc.attrs, nil)
			if got := plan.layerCount(); got != tc.want {
				t.Errorf("prepass layers = %d, want %d", got, tc.want)
			}
		})
	}
}
