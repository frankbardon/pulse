package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// constant_aggregator_test.go pins AGG_CONSTANT (internal/aggconst):
// the wire value per field type equals what an existing aggregator
// writes for a field of that type, the same value lands in every group
// row, crosstab cell and margin on every execution arm, and predict and
// the runtime refuse (and resolve) every slot alike.

// constantWireCase is one field type: the stored value of a one-row
// cohort, the params.value literal that names it, and the existing
// aggregator whose row value the constant must equal ("" when no
// aggregator emits the type's value — categorical — and want is the
// JSON the constant writes instead).
type constantWireCase struct {
	ft      encoding.FieldType
	stored  any
	literal string
	cmp     types.AggregationType
	want    string
}

func constantWireCases() []constantWireCase {
	dec, _, err := encoding.ParseDecimal128("-12.50")
	if err != nil {
		panic(err)
	}
	cases := []constantWireCase{
		{ft: encoding.FieldTypeU4, stored: uint64(9), literal: `9`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeU8, stored: uint64(200), literal: `200`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeU16, stored: uint64(60000), literal: `60000`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeU32, stored: uint64(4000000000), literal: `4000000000`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeU64, stored: uint64(1<<52 + 3), literal: strconv.FormatUint(1<<52+3, 10), cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeF32, stored: float32(0.1), literal: `0.1`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeF64, stored: 1234.0625, literal: `1234.0625`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypePackedBool, stored: true, literal: `true`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeDate, stored: int32(19737), literal: `"2024-01-15"`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeDate, stored: int32(-5), literal: `"1969-12-27"`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeDateTime, stored: int64(1705314600), literal: `"2024-01-15T12:30:00+02:00"`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeDecimal128, stored: dec, literal: `"-12.50"`, cmp: types.AGG_MIN},
		{ft: encoding.FieldTypeVecF32, stored: []float32{1.5, -2.25}, literal: `[1.5, -2.25]`, cmp: types.AGG_VEC_SUM},
		{ft: encoding.FieldTypeVecF64, stored: []float64{0.1, 9e300, -1}, literal: `[0.1, 9e300, -1]`, cmp: types.AGG_VEC_SUM},
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU128, encoding.FieldTypeSetU256} {
		cases = append(cases, constantWireCase{ft: ft, stored: []string{"x", "y"}, literal: `["x", "y"]`, cmp: types.AGG_SET_UNION})
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32} {
		cases = append(cases, constantWireCase{ft: ft, stored: "hi", literal: `"hi"`, want: `[{"a":"hi"}]`})
	}
	return cases
}

// oneFieldCohort writes a cohort whose one nullable field "f" of type
// ft holds the given rows.
func oneFieldCohort(t *testing.T, ft encoding.FieldType, rows ...any) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	f := encoding.Field{Name: "f", Type: ft, Nullable: true, Description: "The one typed value the constant is compared with"}
	schema := encoding.Schema{Fields: []encoding.Field{f}}
	switch {
	case ft.IsDecimal():
		schema.Fields[0].Precision, schema.Fields[0].Scale = 10, 2
	case ft.HasDictionary():
		schema.Fields[0].Dictionary = encoding.NewDictionary()
	case ft.IsVector():
		dim := 2
		if ft == encoding.FieldTypeVecF64 {
			dim = 3
		}
		schema.Vectors = []encoding.VectorField{{FieldIndex: 0, Dim: dim}}
	}
	b, err := p.NewCohortBuilder(context.Background(), "one.pulse", schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if err := b.Append(pulse.CohortRow{r}); err != nil {
			t.Fatalf("append %v: %v", r, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

// constRowsJSON is resp.Data as the wire writes it.
func constRowsJSON(t *testing.T, resp *types.Response) string {
	t.Helper()
	b, err := types.MarshalFinite(resp.Data)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func constantSlot(label, params string) *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_CONSTANT, Label: label, Params: json.RawMessage(params)}
}

// TestConstant_WireMatchesExistingAggregators: for every field type, an
// AGG_CONSTANT naming value X writes the very bytes the existing
// aggregator writes over a one-row field holding X — AGG_MIN on the
// numeric family, decimal128, date and datetime, AGG_SET_UNION on a
// set, AGG_VEC_SUM on a vector — and the label on a categorical (no
// aggregator emits a categorical's label). A literal null writes what
// AGG_MIN writes over a null row: null.
func TestConstant_WireMatchesExistingAggregators(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range constantWireCases() {
		t.Run(c.ft.String()+" "+c.literal, func(t *testing.T) {
			t.Parallel()
			p, _ := oneFieldCohort(t, c.ft, c.stored)
			cohort := &types.Cohort{Filename: "one.pulse"}
			got, err := p.Process(ctx, &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{
				constantSlot("a", `{"type":"`+c.ft.String()+`","value":`+c.literal+`}`)}})
			if err != nil {
				t.Fatalf("constant: %v", err)
			}
			want := c.want
			if c.cmp != "" {
				ref := &types.Aggregation{Type: c.cmp, Field: "f", Label: "a"}
				if c.cmp == types.AGG_VEC_SUM {
					ref.Field, ref.Vector = "", "f"
				}
				resp, err := p.Process(ctx, &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{ref}})
				if err != nil {
					t.Fatalf("%s: %v", c.cmp, err)
				}
				want = constRowsJSON(t, resp)
			}
			if g := constRowsJSON(t, got); g != want {
				t.Errorf("AGG_CONSTANT wrote %s, %s writes %s", g, c.cmp, want)
			}
		})
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeU8, encoding.FieldTypeF64, encoding.FieldTypeDate, encoding.FieldTypeDecimal128} {
		t.Run(ft.String()+" null", func(t *testing.T) {
			t.Parallel()
			p, _ := oneFieldCohort(t, ft, nil)
			cohort := &types.Cohort{Filename: "one.pulse"}
			got, err := p.Process(ctx, &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{constantSlot("a", `{"type":"`+ft.String()+`","value":null}`)}})
			if err != nil {
				t.Fatal(err)
			}
			ref, err := p.Process(ctx, &types.Request{Cohort: cohort, Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "f", Label: "a"}}})
			if err != nil {
				t.Fatal(err)
			}
			if g, w := constRowsJSON(t, got), constRowsJSON(t, ref); g != w || g != `[{"a":null}]` {
				t.Errorf("null constant %s, AGG_MIN over a null row %s", g, w)
			}
		})
	}
}

// TestConstant_ExpressionMatchesLiteral: an expr over params (and the
// built-ins) predicts resolved — nothing in unresolved_scalars — and
// writes exactly what the literal it stands for writes, on every
// expression-capable type.
func TestConstant_ExpressionMatchesLiteral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	params := types.Params{"k": json.Number("7"), "share": json.Number("0.25"), "day": json.Number("19737")}
	cases := []struct {
		typ, expr, literal string
	}{
		{"u4", "params.k + 8", `15`},
		{"u64", "params.k * 1000", `7000`},
		{"f32", "params.share / 2", `0.125`},
		{"f64", "params.share * params.k", `1.75`},
		{"packed_bool", "params.k > 5 ? 1 : 0", `true`},
		{"date", "params.day + 1", `"2024-01-16"`},
		{"datetime", "params.day * 86400", `"2024-01-15T00:00:00Z"`},
	}
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			req := func(arg string) *types.Request {
				return &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Params: paramsCopy(params),
					Aggregations: []*types.Aggregation{constantSlot("c", `{"type":"`+c.typ+`",`+arg+`}`)},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}}
			}
			exprReq := req(`"expr":` + strconv.Quote(c.expr))
			env := predictEnvelope(t, p, fsys, "series.pulse", exprReq)
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused: %+v", env.Errors)
			}
			if pr := env.Data.(*descriptor.PredictResult); pr.UnresolvedScalars != nil {
				t.Fatalf("a params-only expression listed unresolved: %+v", pr.UnresolvedScalars)
			}
			got, err := p.Process(ctx, exprReq)
			if err != nil {
				t.Fatalf("expr: %v", err)
			}
			litReq := req(`"value":` + c.literal)
			want, err := p.Process(ctx, litReq)
			if err != nil {
				t.Fatalf("literal: %v", err)
			}
			if g, w := constRowsJSON(t, got), constRowsJSON(t, want); g != w {
				t.Errorf("expr wrote %s, literal %s", g, w)
			}
		})
	}
}

func paramsCopy(p types.Params) types.Params {
	out := types.Params{}
	for k, v := range p {
		out[k] = v
	}
	return out
}

// constantCode is err's code and details.reason ("" when none).
func constantCode(t *testing.T, err error) (string, string) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("uncoded error %v", err)
	}
	r, _ := ce.Details["reason"].(string)
	return string(ce.Code), r
}

// TestConstant_RefusalsMatchPredict: every AGG_CONSTANT refusal —
// the slot shape, the params, a literal that does not fit its type, an
// expression on a type it cannot produce or yielding a number the type
// cannot hold, a slot weight, a non-numeric constant read as one
// number — is refused by Process and by predict with the same code and
// reason; predict's first error is the runtime's.
func TestConstant_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	slot := func(mut func(a *types.Aggregation)) *types.Request {
		a := constantSlot("c", `{"type":"f64","value":1}`)
		mut(a)
		return &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Params: types.Params{"k": json.Number("3")}, Aggregations: []*types.Aggregation{a}}
	}
	params := func(raw string) func(a *types.Aggregation) {
		return func(a *types.Aggregation) { a.Params = json.RawMessage(raw) }
	}
	cfg := string(errors.PROCESSING_CONFIG)
	cases := []struct {
		name         string
		req          *types.Request
		code, reason string
	}{
		{"field named", slot(func(a *types.Aggregation) { a.Field = "x" }), cfg, "field_not_accepted"},
		{"fields named", slot(func(a *types.Aggregation) { a.Fields = []string{"x", "y"} }), cfg, "field_not_accepted"},
		{"no label", slot(func(a *types.Aggregation) { a.Label = "" }), cfg, "label_missing"},
		{"no params", slot(params(``)), cfg, "params_invalid"},
		{"unknown key", slot(params(`{"type":"f64","value":1,"scale":2}`)), string(errors.PULSE_REQUEST_UNKNOWN_FIELD), ""},
		{"no type", slot(params(`{"value":1}`)), cfg, "type_invalid"},
		{"bad type", slot(params(`{"type":"int32","value":1}`)), cfg, "type_invalid"},
		{"both", slot(params(`{"type":"f64","value":1,"expr":"2"}`)), cfg, "value_and_expr"},
		{"neither", slot(params(`{"type":"f64"}`)), cfg, "value_missing"},
		{"u8 over", slot(params(`{"type":"u8","value":256}`)), cfg, "value_invalid"},
		{"u4 fraction", slot(params(`{"type":"u4","value":1.5}`)), cfg, "value_invalid"},
		{"u64 past 2^53", slot(params(`{"type":"u64","value":9007199254740993}`)), cfg, "value_invalid"},
		{"negative u32", slot(params(`{"type":"u32","value":-1}`)), cfg, "value_invalid"},
		{"f32 overflow", slot(params(`{"type":"f32","value":1e39}`)), cfg, "value_invalid"},
		{"f64 string", slot(params(`{"type":"f64","value":"1"}`)), cfg, "value_invalid"},
		{"bool number", slot(params(`{"type":"packed_bool","value":1}`)), cfg, "value_invalid"},
		{"date layout", slot(params(`{"type":"date","value":"01/15/2024"}`)), cfg, "value_invalid"},
		{"datetime no zone", slot(params(`{"type":"datetime","value":"2024-01-15T10:30:00"}`)), cfg, "value_invalid"},
		{"datetime fraction", slot(params(`{"type":"datetime","value":"2024-01-15T10:30:00.5Z"}`)), cfg, "value_invalid"},
		{"decimal exponent", slot(params(`{"type":"decimal128","value":"1e3"}`)), cfg, "value_invalid"},
		{"decimal number", slot(params(`{"type":"decimal128","value":12.5}`)), cfg, "value_invalid"},
		{"categorical empty", slot(params(`{"type":"categorical_u8","value":""}`)), cfg, "value_invalid"},
		{"set duplicate", slot(params(`{"type":"set_u8","value":["a","a"]}`)), cfg, "value_invalid"},
		{"set too wide", slot(params(`{"type":"set_u8","value":["a","b","c","d","e","f","g","h","i"]}`)), cfg, "value_invalid"},
		{"vec one element", slot(params(`{"type":"vec_f64","value":[1]}`)), cfg, "value_invalid"},
		{"vec f32 overflow", slot(params(`{"type":"vec_f32","value":[1,1e39]}`)), cfg, "value_invalid"},
		{"expr on decimal", slot(params(`{"type":"decimal128","expr":"1"}`)), cfg, "expr_type_unsupported"},
		{"expr on set", slot(params(`{"type":"set_u8","expr":"1"}`)), cfg, "expr_type_unsupported"},
		{"expr blank", slot(params(`{"type":"f64","expr":" "}`)), cfg, "expr_invalid"},
		{"expr u8 over", slot(params(`{"type":"u8","expr":"params.k * 100"}`)), cfg, "expr_out_of_range"},
		{"expr bool two", slot(params(`{"type":"packed_bool","expr":"params.k - 1"}`)), cfg, "expr_out_of_range"},
		{"expr not integer", slot(params(`{"type":"u8","expr":"params.k / 2"}`)), cfg, "expr_not_integer"},
		{"expr not finite", slot(params(`{"type":"f64","expr":"log(0)"}`)), cfg, "expr_not_finite"},
		{"expr not numeric", slot(params(`{"type":"f64","expr":"\"a\""}`)), cfg, "expr_not_numeric"},
		{"expr reads a column", slot(params(`{"type":"f64","expr":"x + 1"}`)), cfg, "expr_invalid"},
		{"expr undeclared param", slot(params(`{"type":"f64","expr":"params.nope"}`)), string(errors.PULSE_PARAMS_UNDECLARED), ""},
		{"expr coef unbound", slot(params(`{"type":"f64","expr":"coef(\"m\", \"x\")"}`)), string(errors.PULSE_MODEL_UNKNOWN), ""},
		{"slot weight", slot(func(a *types.Aggregation) { a.Weight = types.SlotWeightField("y") }), cfg, "weight_not_accepted"},
		{"sort by a label constant", &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"},
			Aggregations: []*types.Aggregation{constantSlot("c", `{"type":"categorical_u8","value":"hi"}`)},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Sort: []types.OrderKey{{Field: "c"}}}, cfg, "non_scalar"},
		{"normalize a set constant", &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Normalize: types.CrosstabNormalizeRow,
			Cell: constantSlot("c", `{"type":"set_u8","value":["a"]}`)}}, string(errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED), ""},
		// A row expression filter keeps the crosstab off the fused arm:
		// the buffered arm refuses the same way.
		{"normalize a decimal constant buffered", &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x >= 0"}},
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}, Normalize: types.CrosstabNormalizeRow,
				Cell: constantSlot("c", `{"type":"decimal128","value":"1.5"}`)}}, string(errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req)
			if err == nil {
				t.Fatal("Process accepted")
			}
			code, reason := constantCode(t, err)
			if code != c.code || reason != c.reason {
				t.Errorf("runtime %s / %q, want %s / %q (%v)", code, reason, c.code, c.reason, err)
			}
			env := predictEnvelope(t, p, fsys, "series.pulse", c.req)
			if len(env.Errors) == 0 {
				t.Fatal("predict accepted")
			}
			e := env.Errors[0]
			pr, _ := e.Details["reason"].(string)
			if e.Code != code || pr != reason {
				t.Errorf("predict %s / %q, runtime %s / %q", e.Code, pr, code, reason)
			}
		})
	}
}

// TestConstant_RequestWeightSkipped: a request weight passes a constant
// by — the constant is written unchanged, the weighted slot beside it
// is weighted — and predict reports the constant's weight skipped.
func TestConstant_RequestWeightSkipped(t *testing.T) {
	t.Parallel()
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	req := &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Weight: &types.WeightSpec{Field: "y"},
		Aggregations: []*types.Aggregation{constantSlot("c", `{"type":"f64","value":2.5}`), {Type: types.AGG_SUM, Field: "x", Label: "s"}}}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if v := resp.Data[0]["c"]; v != 2.5 {
		t.Errorf("constant under a request weight = %v, want 2.5", v)
	}
	env := predictEnvelope(t, p, fsys, "series.pulse", req)
	if len(env.Errors) != 0 {
		t.Fatalf("predict: %+v", env.Errors)
	}
	for _, w := range env.Data.(*descriptor.PredictResult).Weights {
		if w.Slot == "aggregations[0]" && w.Status != descriptor.WeightStatusSkippedNotWeightAware {
			t.Errorf("constant weight status %q, want skipped", w.Status)
		}
	}
	// A slot `weight: null` opts out: no refusal.
	req.Aggregations[0].Weight = types.NullSlotWeight()
	if _, err := p.Process(context.Background(), req); err != nil {
		t.Errorf("weight null refused: %v", err)
	}
}

// constantGroupedCohort is 4000 rows over two regions and three kinds.
func constantGroupedCohort(t *testing.T, opts pulse.Options, shards int) *pulse.Pulse {
	t.Helper()
	if opts.FS == nil {
		opts.FS = afero.NewMemMapFs()
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeU8, Description: "Sales region the order was booked in"},
		{Name: "kind", Type: encoding.FieldTypeU8, Description: "Product line the order belongs to"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Order value in the booking currency"},
	}}
	bo := pulse.CohortBuilderOptions{}
	if shards > 0 {
		bo.Shards = &pulse.ShardSplit{MaxRecords: 4000 / shards}
	}
	b, err := p.NewCohortBuilder(context.Background(), "orders.pulse", schema, bo)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4000 {
		if err := b.Append(pulse.CohortRow{uint64(i % 2), uint64(i % 3), float64(i%17) + 0.5}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

var constantSlots = []*types.Aggregation{
	constantSlot("target", `{"type":"f64","expr":"params.target * 2"}`),
	constantSlot("cut", `{"type":"date","value":"2024-06-30"}`),
	constantSlot("tags", `{"type":"set_u16","value":["b","a"]}`),
	constantSlot("dec", `{"type":"decimal128","value":"3.10"}`),
	constantSlot("vec", `{"type":"vec_f32","value":[0.5,1]}`),
	constantSlot("none", `{"type":"u8","value":null}`),
}

func constantGroupedReq() *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: "orders.pulse"}, Params: types.Params{"target": json.Number("1.25")},
		Aggregations: append([]*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "sum"}}, constantSlots...),
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_CATEGORY, Field: "kind"}}}
}

// constantWant is each constant's row value as the wire writes it.
var constantWant = map[string]string{
	"target": `2.5`, "cut": `19904`, "tags": `["b","a"]`, "dec": `"3.10"`, "vec": `[0.5,1]`, "none": `null`,
}

// checkConstantRows asserts every row carries every constant.
func checkConstantRows(t *testing.T, where string, rows []map[string]any, wantRows int) {
	t.Helper()
	if len(rows) != wantRows {
		t.Fatalf("%s: %d rows, want %d", where, len(rows), wantRows)
	}
	for i, r := range rows {
		for label, want := range constantWant {
			b, err := types.MarshalFinite(r[label])
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != want {
				t.Errorf("%s row %d %s = %s, want %s", where, i, label, b, want)
			}
		}
	}
}

// TestConstant_SameValueInEveryRowAndArm: the constants land unchanged
// in every group row on the streaming, buffered, parallel-decode and
// shard-archive arms (a stream too); the floor counts every record that
// reached the slot; the rows are bit-identical across DecodeWorkers and
// ShardWorkers.
func TestConstant_SameValueInEveryRowAndArm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var ref string
	for _, arm := range []struct {
		name   string
		opts   pulse.Options
		shards int
	}{
		{"serial", pulse.Options{DecodeWorkers: 1}, 0},
		{"parallel decode", pulse.Options{DecodeWorkers: 4}, 0},
		{"archive serial", pulse.Options{ShardWorkers: 1}, 4},
		{"archive parallel", pulse.Options{ShardWorkers: 4}, 4},
	} {
		p := constantGroupedCohort(t, arm.opts, arm.shards)
		resp, err := p.Process(ctx, constantGroupedReq())
		if err != nil {
			t.Fatalf("%s: %v", arm.name, err)
		}
		checkConstantRows(t, arm.name, resp.Data, 6)
		for _, c := range resp.Components.Aggregations[1:] {
			if c.N != 4000 || c.NNull != 0 {
				t.Errorf("%s %s floor n %d n_null %d, want 4000 / 0", arm.name, c.Label, c.N, c.NNull)
			}
		}
		got := constRowsJSON(t, resp)
		if ref == "" {
			ref = got
		} else if got != ref {
			t.Errorf("%s rows differ from serial:\n%s\n%s", arm.name, got, ref)
		}

		buffered := constantGroupedReq()
		buffered.Groups = []*types.Group{{Type: types.GROUP_QUANTILE, Field: "x", Interval: 2}}
		if resp, err := p.Process(ctx, buffered); err != nil {
			t.Fatalf("%s buffered: %v", arm.name, err)
		} else {
			checkConstantRows(t, arm.name+" buffered", resp.Data, len(resp.Data))
		}

		it, err := p.ProcessStream(ctx, constantGroupedReq())
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		for {
			r, ok, err := it.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			rows = append(rows, r)
		}
		_ = it.Close()
		checkConstantRows(t, arm.name+" stream", rows, 6)
	}
}

// TestConstant_CrosstabCellsAndMargins: a constant cell and a constant
// auxiliary margin aggregation hold the same value in every populated
// cell, row and column margin and the grand total, on the fused and the
// buffered crosstab arms alike.
func TestConstant_CrosstabCellsAndMargins(t *testing.T) {
	t.Parallel()
	fsys := afero.NewMemMapFs()
	p := constantGroupedCohort(t, pulse.Options{FS: fsys}, 0)
	ctx := context.Background()
	spec := func() *types.CrosstabSpec {
		return &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "kind"}},
			Margins:            types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Cell:               constantSlot("tags", `{"type":"set_u8","value":["x","y"]}`),
			MarginAggregations: []*types.Aggregation{constantSlot("goal", `{"type":"f64","expr":"params.target"}`)},
		}
	}
	for _, arm := range []string{"fused", "buffered"} {
		req := &types.Request{Cohort: &types.Cohort{Filename: "orders.pulse"}, Params: types.Params{"target": json.Number("4.5")}, Crosstab: spec()}
		if arm == "buffered" {
			// A row expression filter keeps the crosstab off the fused arm.
			req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x >= 0"}}
		}
		pr := predictEnvelope(t, p, fsys, "orders.pulse", req).Data.(*descriptor.PredictResult)
		if fused := pr.CrosstabFusable != nil && *pr.CrosstabFusable; fused != (arm == "fused") {
			t.Fatalf("%s arm: predict crosstab_fusable %v (%v)", arm, fused, pr.CrosstabFusionReasons)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", arm, err)
		}
		m := resp.Crosstab.Matrix
		var values []types.MatrixCell
		for _, row := range m.Cells {
			values = append(values, row...)
		}
		values = append(values, m.RowMargins...)
		values = append(values, m.ColumnMargins...)
		values = append(values, m.GrandTotal)
		if len(values) != 6+2+3+1 {
			t.Fatalf("%s: %d cells and margins", arm, len(values))
		}
		for i, c := range values {
			if !c.Present || !reflect.DeepEqual(c.Value, []string{"x", "y"}) {
				t.Errorf("%s value %d = %+v, want [x y]", arm, i, c)
			}
		}
		cc := resp.Components.Crosstab
		figs := append(append([]map[string]types.MarginAggregationFigure{}, cc.RowMarginAggregations...), cc.ColumnMarginAggregations...)
		figs = append(figs, cc.GrandTotalAggregations)
		for i, f := range figs {
			if g := f["goal"]; !g.Present || g.Value != 4.5 {
				t.Errorf("%s margin aggregation %d = %+v, want 4.5", arm, i, g)
			}
		}
	}
}

// TestConstant_UnresolvedUntilRuntime: an expr calling a model function
// is checked but not run by predict — listed under unresolved_scalars
// with its path and reads, never an error — and Process evaluates it on
// the loaded model.
func TestConstant_UnresolvedUntilRuntime(t *testing.T) {
	t.Parallel()
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	ctx := context.Background()
	fit, err := p.Process(ctx, &types.Request{
		Cohort:      &types.Cohort{Filename: "series.pulse"},
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}, Save: "models/m.pulse-model.json"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	coef := fit.Regressions[0].Coefficients["x"]
	const src = `coef("m", "x") * 2`
	req := &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"},
		Models:       []types.ModelRef{{Name: "m", Path: "models/m.pulse-model.json"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}, constantSlot("c", `{"type":"f64","expr":`+strconv.Quote(src)+`}`)}}
	env := predictEnvelope(t, p, fsys, "series.pulse", req)
	if len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Fatalf("predict: errors %+v warnings %+v", env.Errors, env.Warnings)
	}
	want := []descriptor.UnresolvedScalar{{Path: "aggregations[1].params.expr", Expression: src, Reads: []string{"coef"}}}
	if got := env.Data.(*descriptor.PredictResult).UnresolvedScalars; !reflect.DeepEqual(got, want) {
		t.Fatalf("unresolved_scalars %+v, want %+v", got, want)
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := resp.Data[0]["c"].(float64); !ok || v != float64(coef*2) || math.IsNaN(v) {
		t.Errorf("c = %v, want coef * 2 = %v", resp.Data[0]["c"], coef*2)
	}
}
