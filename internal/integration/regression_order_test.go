package integration_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// regression_order_test.go: RegressionSpec.Order end to end (U71
// E2-S1, #551) — the engine sorts an ordered slot's rows (per
// partition series) before the model sees them, a repeat or a gap fails
// the slot alone, an ordered slot never streams, and every spec refusal
// (the block's shape and types, a model's RequiresOrder) is predict's
// error exactly as it is Process's.

const (
	orderCohort                         = "ordered.pulse"
	regOrderedName types.RegressionType = "REG_ACME_ORDERED"
)

// orderRows are (market, week) in a deliberately shuffled record order:
// market a weeks 1–8, market b weeks 3–8 (a ragged start).
var orderRows = []struct {
	mkt  string
	week uint64
}{
	{"b", 5}, {"a", 3}, {"a", 1}, {"b", 3}, {"a", 8}, {"b", 8}, {"a", 2}, {"b", 4},
	{"a", 5}, {"a", 4}, {"b", 7}, {"a", 7}, {"b", 6}, {"a", 6},
}

func writeOrderCohort(t *testing.T, p *pulse.Pulse) {
	t.Helper()
	mkt := encoding.NewDictionary()
	for _, l := range []string{"a", "b"} {
		if _, err := mkt.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16, Description: "Period index of the ordered regression cohort"},
		{Name: "mkt", Type: encoding.FieldTypeCategoricalU8, Dictionary: mkt, Description: "Market of the ordered regression cohort"},
		{Name: "x", Type: encoding.FieldTypeF64, Description: "Predictor of the ordered regression cohort"},
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Response of the ordered regression cohort"},
	}}
	b, err := p.NewCohortBuilder(context.Background(), orderCohort, schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range orderRows {
		x := float64(r.week) + float64(i%3)
		if err := b.Append(pulse.CohortRow{r.week, r.mkt, x, float64(2*x) + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// orderedModel records the (market, week) sequence its Fit sees.
type orderedModel struct {
	mu   *sync.Mutex
	seen *[]string
}

func (m orderedModel) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	var seq []string
	for i := range rows.Len() {
		r := rows.At(i)
		w, _ := r.NumericValue("week")
		mk, _ := r.StringValue("mkt")
		seq = append(seq, mk+":"+strconv.FormatFloat(w, 'g', -1, 64))
	}
	m.mu.Lock()
	*m.seen = seq
	m.mu.Unlock()
	return extend.RegressionOutput{Coefficients: []extend.RegressionCoefficient{{Name: "rows", Estimate: float64(len(seq))}}}, nil
}

// orderedExtensions registers REG_ACME_ORDERED: buffered, requiring an
// order unless params.free is true, panicking in RequiresOrder when
// params.boom is true.
func orderedExtensions(seen *[]string) pulse.Extensions {
	mu := &sync.Mutex{}
	flag := func(spec *types.RegressionSpec, key string) bool {
		var p map[string]bool
		_ = json.Unmarshal(spec.Params, &p)
		return p[key]
	}
	return pulse.Extensions{Regressions: []pulse.RegressionRegistration{{
		Name: regOrderedName, Description: "a model that reads its rows in order",
		Params: []pulse.ParamMeta{{Name: "free", JSONType: "boolean"}, {Name: "boom", JSONType: "boolean"}},
		RequiresOrder: func(spec *types.RegressionSpec) bool {
			if flag(spec, "boom") {
				panic("boom")
			}
			return !flag(spec, "free")
		},
		Factory: func(*types.RegressionSpec, *encoding.Schema) (extend.Regression, error) {
			return orderedModel{mu: mu, seen: seen}, nil
		},
	}}}
}

func openOrdered(t *testing.T) (*pulse.Pulse, *[]string) {
	t.Helper()
	seen := &[]string{}
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: orderedExtensions(seen)})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	writeOrderCohort(t, p)
	return p, seen
}

func orderReq(specs ...*types.RegressionSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: orderCohort}, Regressions: specs}
}

// TestRegressionOrder_ModelSeesOrderedRows: with order {week, mkt} the
// model reads market a's weeks 1–8 then market b's 3–8, whatever the
// record order; params.free without an order reads record order.
func TestRegressionOrder_ModelSeesOrderedRows(t *testing.T) {
	t.Parallel()

	p, seen := openOrdered(t)
	spec := &types.RegressionSpec{Type: regOrderedName, Name: "m", Target: "y", Predictors: []string{"x"},
		Order: &types.RegressionOrder{Field: "week", Partition: "mkt"}}
	processE2E(t, p, orderReq(spec))
	want := []string{"a:1", "a:2", "a:3", "a:4", "a:5", "a:6", "a:7", "a:8", "b:3", "b:4", "b:5", "b:6", "b:7", "b:8"}
	if !reflect.DeepEqual(*seen, want) {
		t.Errorf("ordered rows = %v, want %v", *seen, want)
	}

	free := &types.RegressionSpec{Type: regOrderedName, Name: "m", Target: "y", Predictors: []string{"x"}, Params: json.RawMessage(`{"free":true}`)}
	processE2E(t, p, orderReq(free))
	if (*seen)[0] != "b:5" {
		t.Errorf("an unordered slot's first row = %s, want record order (b:5)", (*seen)[0])
	}
}

// TestRegressionOrder_GapFailsSlotAlone: an unpartitioned order over a
// two-market panel repeats periods, and a filter leaves a gap — each
// fails its slot alone (PROCESSING_REGRESSION_ORDER, PULSE_SLOT_FAILED)
// beside a sibling that fits; the partitioned order fits; an ordered
// slot never streams, at predict as at runtime.
func TestRegressionOrder_GapFailsSlotAlone(t *testing.T) {
	t.Parallel()

	p, _ := openOrdered(t)
	ols := func(name string, o *types.RegressionOrder) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: name, Target: "y", Predictors: []string{"x"}, Order: o}
	}
	for _, tc := range []struct {
		name    string
		order   *types.RegressionOrder
		filter  bool
		reason  string
		details map[string]any
	}{
		{"duplicate", &types.RegressionOrder{Field: "week"}, false, "duplicate", map[string]any{"value": float64(3)}},
		{"gap", &types.RegressionOrder{Field: "week", Partition: "mkt"}, true, "gap", map[string]any{"partition": "a", "from": float64(4), "to": float64(6), "step": float64(1)}},
		{"partitioned_fits", &types.RegressionOrder{Field: "week", Partition: "mkt"}, false, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := orderReq(ols("ordered", tc.order), ols("plain", nil))
			if tc.filter {
				req.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "week != 5"}}
			}
			resp := processE2E(t, p, req)
			if resp.Regressions[1].Error != nil {
				t.Fatalf("sibling failed: %+v", resp.Regressions[1].Error)
			}
			got := resp.Regressions[0]
			if tc.reason == "" {
				if got.Error != nil {
					t.Fatalf("partitioned order refused: %+v", got.Error)
				}
			} else {
				if got.Error == nil || got.Error.Code != string(perr.PROCESSING_REGRESSION_ORDER) || got.Error.Details["reason"] != tc.reason {
					t.Fatalf("ordered slot = %+v, want PROCESSING_REGRESSION_ORDER %s", got.Error, tc.reason)
				}
				for k, v := range tc.details {
					if !reflect.DeepEqual(jsonRoundTrip(t, got.Error.Details[k]), v) {
						t.Errorf("details[%s] = %v, want %v", k, got.Error.Details[k], v)
					}
				}
				if !hasWarning(resp.Warnings, "PULSE_SLOT_FAILED") {
					t.Errorf("no PULSE_SLOT_FAILED warning: %+v", resp.Warnings)
				}
			}
			pr, err := p.Predict(context.Background(), req)
			if err != nil || len(pr.Errors) != 0 {
				t.Fatalf("predict: %v %+v", err, pr.Errors)
			}
			if pr.Streamable {
				t.Error("predict calls an ordered request streamable")
			}
		})
	}
}

func jsonRoundTrip(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func hasWarning(ws []*types.ResponseWarning, code string) bool {
	for _, w := range ws {
		if w != nil && w.Code == code {
			return true
		}
	}
	return false
}

// TestRegressionOrder_RefusalsPredictMatchRuntime: every spec refusal of
// an order — a model that requires one without it, a panicking
// RequiresOrder, the block's shape and field types, an unknown field —
// is predict's only error and Process's, code, message and details
// identical.
func TestRegressionOrder_RefusalsPredictMatchRuntime(t *testing.T) {
	t.Parallel()

	p, _ := openOrdered(t)
	ext := func(params string, o *types.RegressionOrder) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: regOrderedName, Name: "m", Target: "y", Predictors: []string{"x"}, Order: o}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		return s
	}
	ols := func(o *types.RegressionOrder) *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"}, Order: o}
	}
	for _, tc := range []struct {
		name   string
		spec   *types.RegressionSpec
		code   perr.Code
		reason string
	}{
		{"order_required", ext("", nil), perr.PROCESSING_CONFIG, "order_required"},
		{"requires_order_panics", ext(`{"boom":true}`, nil), perr.PULSE_EXTENSION_SPEC_REFUSED, ""},
		{"field_missing", ols(&types.RegressionOrder{Partition: "mkt"}), perr.PROCESSING_CONFIG, "field_missing"},
		{"gaps_invalid", ols(&types.RegressionOrder{Field: "week", Gaps: "fill"}), perr.PROCESSING_CONFIG, "gaps_invalid"},
		{"partition_is_field", ols(&types.RegressionOrder{Field: "week", Partition: "week"}), perr.PROCESSING_CONFIG, "partition_is_field"},
		{"field_type", ols(&types.RegressionOrder{Field: "x"}), perr.PROCESSING_CONFIG, "field_type"},
		{"partition_type", ols(&types.RegressionOrder{Field: "week", Partition: "y"}), perr.PROCESSING_CONFIG, "partition_type"},
		{"extension_field_type", ext(`{"free":true}`, &types.RegressionOrder{Field: "x"}), perr.PROCESSING_CONFIG, "field_type"},
		{"unknown_field", ols(&types.RegressionOrder{Field: "nope"}), perr.SERVICE_VALIDATION, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := orderReq(tc.spec)
			_, err := p.Process(context.Background(), req)
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Errorf("runtime reason = %v, want %s", ce.Details["reason"], tc.reason)
			}
			pr, perrv := p.Predict(context.Background(), req)
			if perrv != nil {
				t.Fatalf("Predict: %v", perrv)
			}
			if len(pr.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly the runtime's %s %q", pr.Errors, ce.Code, ce.Message)
			}
			if got := pr.Errors[0]; got.Code != string(ce.Code) || got.Message != ce.Message || !reflect.DeepEqual(got.Details, ce.Details) {
				t.Errorf("predict = %+v, runtime %s %q %v", got, ce.Code, ce.Message, ce.Details)
			}
		})
	}
}
