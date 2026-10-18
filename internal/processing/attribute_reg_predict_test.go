package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/types"
)

// attribute_reg_predict_test.go pins ATTR_REG_PREDICT in the engine:
// bit-equality with ATTR_REG_FITTED across the OLS family (the shared
// scoring kernel plus identical coefficients), null emission on every
// orchestrator arm with a downstream aggregation skipping the row, the
// bind-time refusals, and row-locality (no prepass, streamable).

// predictSchema: three f64 predictors (x2 nullable), an f64 target and
// a categorical, for the non-numeric refusal.
func predictSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	_, _ = d.Add("a")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "x3", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
	}}
}

// predictNullRows are the rows whose x2 is null.
var predictNullRows = map[int]bool{3: true, 17: true, 28: true}

// predictRecords: 40 rows, fresh on every call (attributes write onto
// records). x3 is mostly noise so a selection may drop it.
func predictRecords(schema *encoding.Schema) []*Record {
	out := make([]*Record, 40)
	for i := range out {
		f := float64(i)
		x1 := 0.37*f + math.Sin(f)
		x2 := math.Cos(0.7*f) * 3
		x3 := math.Sin(2.3*f) * 0.01
		y := 4 + 1.7*x1 - 2.2*x2 + 0.9*math.Sin(1.3*f+0.2)
		vals := map[string]float64{"x1": x1, "x2": x2, "x3": x3, "y": y, "g": 0}
		if predictNullRows[i] {
			out[i] = NewRecordWithNulls(schema, vals, map[string]bool{"x2": true})
			continue
		}
		out[i] = NewRecord(schema, vals)
	}
	return out
}

// bindFit runs spec as a standalone regression request — the compose
// fit pre-pass's own Process — and binds its result under "m".
func bindFit(t *testing.T, spec *types.RegressionSpec) (*model.Set, *types.RegressionResult) {
	t.Helper()
	schema := predictSchema()
	resp, err := NewProcessor(schema).Process(context.Background(),
		&types.Request{Regressions: []*types.RegressionSpec{spec}}, NewSliceIterator(predictRecords(schema)))
	if err != nil {
		t.Fatalf("fit: %v", err)
	}
	res := resp.Regressions[0]
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	return set, res
}

func predictAttr(params string) *types.Attribute {
	return &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(params)}
}

// TestRegPredict_EqualsRegFittedBitForBit: scoring a bound REG_OLS fit
// equals ATTR_REG_FITTED's refit on the same rows bit for bit —
// unpenalized, every penalty and under selection (FITTED given the
// selected features) — on the buffered arm, and the rows with a null
// predictor are null.
func TestRegPredict_EqualsRegFittedBitForBit(t *testing.T) {
	preds := []string{"x1", "x2", "x3"}
	cases := []struct {
		name string
		spec types.RegressionSpec
	}{
		{"ols", types.RegressionSpec{}},
		{"ridge", types.RegressionSpec{Penalty: "l2", Alpha: 0.4}},
		{"lasso", types.RegressionSpec{Penalty: "l1", Alpha: 0.05}},
		{"elasticnet", types.RegressionSpec{Penalty: "elasticnet", Alpha: 0.05, L1Ratio: 0.5}},
		{"forward selection", types.RegressionSpec{Selection: "forward", Criterion: "aic"}},
		{"backward selection", types.RegressionSpec{Selection: "backward", Criterion: "bic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := tc.spec
			spec.Type, spec.Name, spec.Target, spec.Predictors = types.REG_OLS, "m", "y", preds
			set, res := bindFit(t, &spec)
			fitted := preds
			if spec.Selection != "" {
				fitted = res.SelectedFeatures
				if len(fitted) == 0 {
					t.Fatal("selection kept no predictor; the fixture no longer exercises scoring")
				}
			}
			schema := predictSchema()
			p := NewProcessor(schema)
			p.SetModels(set)
			recs := predictRecords(schema)
			attrs := []*types.Attribute{
				predictAttr(`{"fit":"m"}`),
				{Type: types.ATTR_REG_FITTED, Label: "f", Target: "y", Predictors: fitted,
					Penalty: spec.Penalty, Alpha: spec.Alpha, L1Ratio: spec.L1Ratio},
			}
			if _, _, err := p.applyAttributes(attrs, recs); err != nil {
				t.Fatal(err)
			}
			compared := 0
			for i, r := range recs {
				got, ok := r.NumericValue("p")
				nullRead := predictNullRows[i] && containsName(fitted, "x2")
				if nullRead {
					if ok || !r.IsNull("p") {
						t.Fatalf("row %d: null predictor scored %v", i, got)
					}
					continue
				}
				want, _ := r.NumericValue("f")
				if !ok || math.Float64bits(got) != math.Float64bits(want) {
					t.Fatalf("row %d: ATTR_REG_PREDICT %v (ok=%v) != ATTR_REG_FITTED %v", i, got, ok, want)
				}
				compared++
			}
			if compared < 30 {
				t.Fatalf("compared only %d rows", compared)
			}
		})
	}
}

func containsName(names []string, n string) bool {
	for _, s := range names {
		if s == n {
			return true
		}
	}
	return false
}

// TestRegPredict_NullSkippedDownstream: a null predictor yields a null
// score on every orchestrator arm — streaming (row-local), buffered and
// the two-pass drive — and a downstream AGG_AVERAGE skips the row
// (n_null) rather than folding NaN or 0.
func TestRegPredict_NullSkippedDownstream(t *testing.T) {
	set, res := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}})
	schema := predictSchema()
	var sum float64
	var n int
	for i, r := range predictRecords(schema) {
		if predictNullRows[i] {
			continue
		}
		x1, _ := r.NumericValue("x1")
		x2, _ := r.NumericValue("x2")
		sum += res.Coefficients[model.InterceptKey] + res.Coefficients["x1"]*x1 + res.Coefficients["x2"]*x2
		n++
	}
	wantMean := sum / float64(n)
	avg := &types.Aggregation{Type: types.AGG_AVERAGE, Field: "p", Label: "avg"}
	arms := map[string]*types.Request{
		"streaming": {Attributes: []*types.Attribute{predictAttr(`{"fit":"m"}`)}, Aggregations: []*types.Aggregation{avg}},
		"buffered": {Attributes: []*types.Attribute{predictAttr(`{"fit":"m"}`)},
			Aggregations: []*types.Aggregation{avg, {Type: types.AGG_MEDIAN, Field: "p", Label: "med"}}},
		"two-pass": {Attributes: []*types.Attribute{predictAttr(`{"fit":"m"}`), {Type: types.ATTR_ZSCORE, Field: "p", Label: "z"}},
			Aggregations: []*types.Aggregation{avg}},
	}
	for name, req := range arms {
		t.Run(name, func(t *testing.T) {
			p := NewProcessor(schema)
			p.SetModels(set)
			resp, err := p.Process(context.Background(), req, NewSliceIterator(predictRecords(schema)))
			if err != nil {
				t.Fatal(err)
			}
			got, _ := resp.Data[0]["avg"].(float64)
			if math.Abs(got-wantMean) > 1e-12*math.Abs(wantMean) {
				t.Fatalf("average = %v, want %v over the %d non-null rows", got, wantMean, n)
			}
			c := resp.Components.Aggregations[0]
			if c.N != n || c.NNull != len(predictNullRows) {
				t.Fatalf("components n=%d n_null=%d, want %d / %d", c.N, c.NNull, n, len(predictNullRows))
			}
		})
	}
}

// TestRegPredict_BindRefusals: the bind-time rule (model.CheckScore,
// then the numeric check) refuses before any record is read, with the
// codes the field-reference pass raises.
func TestRegPredict_BindRefusals(t *testing.T) {
	set, _ := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x2"}})
	cases := []struct {
		name    string
		attr    *types.Attribute
		set     *model.Set
		code    perr.Code
		details map[string]any
	}{
		{"unknown fit", predictAttr(`{"fit":"nope"}`), set, perr.PULSE_MODEL_UNKNOWN,
			map[string]any{"name": "nope", "available": []string{"m"}}},
		{"nothing bound", predictAttr(`{"fit":"m"}`), nil, perr.PULSE_MODEL_UNKNOWN,
			map[string]any{"name": "m", "available": []string{}}},
		{"term not a predictor", predictAttr(`{"fit":"m","terms":["x3"]}`), set, perr.PULSE_MODEL_TERM_UNKNOWN,
			map[string]any{"fit": "m", "term": "x3", "available": []string{model.InterceptKey, "x1", "x2"}}},
		{"scale on OLS", predictAttr(`{"fit":"m","scale":"link"}`), set, perr.PULSE_MODEL_SCORE_INVALID,
			map[string]any{"attribute": "ATTR_REG_PREDICT", "fit": "m", "reason": model.ReasonScaleNotGLM, "scale": "link", "type": "REG_OLS"}},
		{"refit fields on the slot", &types.Attribute{Type: types.ATTR_REG_PREDICT, Target: "y", Params: json.RawMessage(`{"fit":"m"}`)}, set,
			perr.PULSE_MODEL_SCORE_INVALID, map[string]any{"attribute": "ATTR_REG_PREDICT", "fit": "m", "reason": model.ReasonSlotFields}},
	}
	schema := predictSchema()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := NewProcessor(schema)
			p.SetModels(tc.set)
			_, err := p.Process(context.Background(),
				&types.Request{Attributes: []*types.Attribute{tc.attr}, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}}},
				NewSliceIterator(predictRecords(schema)))
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
			if !reflect.DeepEqual(ce.Details, tc.details) {
				t.Fatalf("details = %v, want %v", ce.Details, tc.details)
			}
		})
	}
	// A predictor that is a categorical schema field.
	catSet, _ := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1"}})
	b, _ := catSet.Lookup("m")
	b.Terms = []string{"g"}
	c, err := newRegPredictAttribute(predictAttr(`{"fit":"m"}`), schema)
	if err != nil {
		t.Fatal(err)
	}
	var ce *perr.CodedError
	if err := BindAttribute(c, nil, catSet); !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_PREDICTOR_NOT_NUMERIC {
		t.Fatalf("categorical predictor: err = %v", err)
	}
	// Params that are not the documented object fail at construction.
	if _, err := newRegPredictAttribute(predictAttr(`{"fit":"m","bogus":1}`), schema); !stderrors.As(err, &ce) || ce.Code != perr.PULSE_MODEL_SCORE_INVALID {
		t.Fatalf("unknown params key: err = %v", err)
	}
}

// TestRegPredict_RowLocal: once bound the attribute is row-local — no
// prepass, not in the two-pass set — so a request scoring with it
// takes the streaming path.
func TestRegPredict_RowLocal(t *testing.T) {
	c, err := newRegPredictAttribute(predictAttr(`{"fit":"m"}`), predictSchema())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(TwoPassAttribute); ok {
		t.Fatal("ATTR_REG_PREDICT must not be a TwoPassAttribute")
	}
	if _, ok := c.(RowLocalAttribute); !ok {
		t.Fatal("ATTR_REG_PREDICT must be a RowLocalAttribute")
	}
	if requiresTwoPass(types.ATTR_REG_PREDICT) {
		t.Fatal("ATTR_REG_PREDICT is in requiresTwoPass")
	}
	set, _ := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1"}})
	p := NewProcessor(predictSchema())
	p.SetModels(set)
	req := &types.Request{Attributes: []*types.Attribute{predictAttr(`{"fit":"m"}`)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p"}}}
	if !p.canStream(req) {
		t.Fatal("canStream refused a request scoring a bound model")
	}
	if _, err := p.Process(context.Background(), req, NewSliceIterator(predictRecords(predictSchema()))); err != nil {
		t.Fatal(err)
	}
	if p.LastPath() != PathStreaming {
		t.Fatalf("path = %v, want streaming", p.LastPath())
	}
}

// TestRegPredict_ProjectionReadsBoundPredictors: the projected decode
// keeps the bound model's predictor columns, which the request never
// spells.
func TestRegPredict_ProjectionReadsBoundPredictors(t *testing.T) {
	set, _ := bindFit(t, &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x1", "x3"}})
	req := &types.Request{Attributes: []*types.Attribute{predictAttr(`{"fit":"m"}`)},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "p"}}}
	got := NeededFieldsWithModels(req, predictSchema(), nil, set)
	if got.IsWide() || !got.Has("x1") || !got.Has("x3") || got.Has("x2") {
		t.Fatalf("needed fields = %v, want x1 and x3", got)
	}
}

// TestRegPredict_ScoresPenalizedGLM: a penalised REG_GLM fit (l2, and
// l1 with a predictor dropped to exactly 0) scores like any other —
// the link scale is the shared kernel over the penalised coefficients
// bit for bit, the response scale its inverse logit — and a dropped
// predictor contributes nothing.
func TestRegPredict_ScoresPenalizedGLM(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64},
		{Name: "b", Type: encoding.FieldTypeF64},
	}}
	rows := func() []*Record {
		out := make([]*Record, 60)
		for i := range out {
			f := float64(i)
			x1, x2 := math.Sin(0.9*f)*2, math.Cos(1.7*f)*0.05
			b := 0.0
			if x1+0.8*math.Sin(2.1*f+0.4) > 0 {
				b = 1
			}
			out[i] = NewRecord(schema, map[string]float64{"x1": x1, "x2": x2, "b": b})
		}
		return out
	}
	for _, tc := range []struct {
		name, penalty string
		alpha         float64
		dropped       string
	}{
		{"l2", "l2", 0.05, ""},
		{"l1", "l1", 0.08, "x2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "b",
				Predictors: []string{"x1", "x2"}, Penalty: tc.penalty, Alpha: tc.alpha}
			resp, err := NewProcessor(schema).Process(context.Background(),
				&types.Request{Regressions: []*types.RegressionSpec{spec}}, NewSliceIterator(rows()))
			if err != nil {
				t.Fatalf("fit: %v", err)
			}
			res := resp.Regressions[0]
			if res.Error != nil || res.Penalty != tc.penalty {
				t.Fatalf("fit error %+v, penalty echo %q", res.Error, res.Penalty)
			}
			if tc.dropped != "" && res.Coefficients[tc.dropped] != 0 {
				t.Fatalf("%s = %v, want dropped to 0 (fixture no longer exercises l1)", tc.dropped, res.Coefficients[tc.dropped])
			}
			b, err := model.Bind("m", spec, res)
			if err != nil {
				t.Fatalf("Bind: %v", err)
			}
			set := &model.Set{}
			if err := set.Add(b); err != nil {
				t.Fatal(err)
			}
			p := NewProcessor(schema)
			p.SetModels(set)
			recs := rows()
			if _, _, err := p.applyAttributes([]*types.Attribute{
				predictAttr(`{"fit":"m","scale":"link"}`),
				{Type: types.ATTR_REG_PREDICT, Label: "r", Params: json.RawMessage(`{"fit":"m"}`)},
			}, recs); err != nil {
				t.Fatal(err)
			}
			beta := []float64{res.Coefficients["x1"], res.Coefficients["x2"]}
			for i, r := range recs {
				x1, _ := r.NumericValue("x1")
				x2, _ := r.NumericValue("x2")
				eta := regression.LinearPredictor(res.Coefficients[regression.InterceptKey], beta, []float64{x1, x2})
				gotEta, _ := r.NumericValue("p")
				if math.Float64bits(gotEta) != math.Float64bits(eta) {
					t.Fatalf("row %d: link score %v, kernel %v", i, gotEta, eta)
				}
				gotMu, _ := r.NumericValue("r")
				if mu := 1 / (1 + math.Exp(-eta)); math.Abs(gotMu-mu) > 1e-15 {
					t.Fatalf("row %d: response score %v, inverse logit %v", i, gotMu, mu)
				}
			}
		})
	}
}
