package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// attribute_reg_null_test.go pins the null rule of ATTR_REG_FITTED /
// RESIDUAL / LEVERAGE: a row with a null predictor — or, for RESIDUAL,
// a null target — emits null (never 0) on every orchestrator arm, so a
// downstream aggregation counts it in n_null and skips it.

// regNullSchema: nullable predictor x2 and nullable target y.
func regNullSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

var (
	regNullPredictorRows = map[int]bool{3: true, 17: true, 28: true}
	regNullTargetRows    = map[int]bool{9: true, 22: true}
)

// regNullRecords: 40 rows, fresh per call; x2 null on
// regNullPredictorRows, y null on regNullTargetRows.
func regNullRecords(schema *encoding.Schema) []*Record {
	out := make([]*Record, 40)
	for i := range out {
		f := float64(i)
		x1 := 0.37*f + math.Sin(f)
		x2 := math.Cos(0.7*f) * 3
		y := 4 + 1.7*x1 - 2.2*x2 + 0.9*math.Sin(1.3*f+0.2)
		nulls := map[string]bool{}
		if regNullPredictorRows[i] {
			nulls["x2"] = true
		}
		if regNullTargetRows[i] {
			nulls["y"] = true
		}
		out[i] = NewRecordWithNulls(schema, map[string]float64{"x1": x1, "x2": x2, "y": y}, nulls)
	}
	return out
}

// regNullWant is the set of rows each attribute must emit null for.
func regNullWant(typ types.AttributeType) map[int]bool {
	want := map[int]bool{}
	for i := range regNullPredictorRows {
		want[i] = true
	}
	if typ == types.ATTR_REG_RESIDUAL {
		for i := range regNullTargetRows {
			want[i] = true
		}
	}
	return want
}

var regNullTypes = []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE}

// TestAttrReg_NullInputEmitsNull: the computer's own output is NaN
// (null) exactly on the null-input rows and finite elsewhere — a null
// target alone leaves FITTED / LEVERAGE defined.
func TestAttrReg_NullInputEmitsNull(t *testing.T) {
	schema := regNullSchema()
	for _, typ := range regNullTypes {
		t.Run(string(typ), func(t *testing.T) {
			c := makeRegAttr(t, typ, schema, &types.Attribute{Target: "y", Predictors: []string{"x1", "x2"}})
			if !EmitsNull(c) {
				t.Fatalf("%s does not declare EmitsNull", typ)
			}
			vals, err := c.Compute(regNullRecords(schema), "")
			if err != nil {
				t.Fatal(err)
			}
			want := regNullWant(typ)
			for i, v := range vals {
				if want[i] != math.IsNaN(v) {
					t.Fatalf("row %d = %v, want null=%v", i, v, want[i])
				}
			}
		})
	}
}

// TestAttrReg_NullSkippedDownstream: on the streaming (two-pass) and
// buffered arms a downstream AGG_AVERAGE counts the null-input rows in
// n_null and averages only the defined rows.
func TestAttrReg_NullSkippedDownstream(t *testing.T) {
	schema := regNullSchema()
	for _, typ := range regNullTypes {
		attr := func() *types.Attribute {
			return &types.Attribute{Type: typ, Target: "y", Predictors: []string{"x1", "x2"}, Label: "out"}
		}
		c := makeRegAttr(t, typ, schema, attr())
		vals, err := c.Compute(regNullRecords(schema), "")
		if err != nil {
			t.Fatal(err)
		}
		var sum float64
		var n int
		for _, v := range vals {
			if !math.IsNaN(v) {
				sum += v
				n++
			}
		}
		wantMean := sum / float64(n)
		wantNull := len(regNullWant(typ))
		avg := &types.Aggregation{Type: types.AGG_AVERAGE, Field: "out", Label: "avg"}
		arms := map[string]*types.Request{
			"streaming": {Attributes: []*types.Attribute{attr()}, Aggregations: []*types.Aggregation{avg}},
			"buffered": {Attributes: []*types.Attribute{attr()},
				Aggregations: []*types.Aggregation{avg, {Type: types.AGG_MEDIAN, Field: "out", Label: "med"}}},
		}
		for name, req := range arms {
			t.Run(string(typ)+"/"+name, func(t *testing.T) {
				resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(regNullRecords(schema)))
				if err != nil {
					t.Fatal(err)
				}
				got, _ := resp.Data[0]["avg"].(float64)
				if math.Abs(got-wantMean) > 1e-9*math.Max(1, math.Abs(wantMean)) {
					t.Fatalf("average = %v, want %v over the %d defined rows", got, wantMean, n)
				}
				ac := resp.Components.Aggregations[0]
				if ac.N != n || ac.NNull != wantNull {
					t.Fatalf("components n=%d n_null=%d, want %d / %d", ac.N, ac.NNull, n, wantNull)
				}
			})
		}
	}
}
