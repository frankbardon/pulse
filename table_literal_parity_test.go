package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// table_literal_parity_test.go pins the static lookup() / range_label()
// literal-call checks (internal/tablecall) at the facade: predict
// refuses each fault with the code, message and details the run raises
// — through the shared field-reference walk (ATTR_FORMULA,
// FILTER_EXPRESSION, total()), the OVERLAY_FORMULA gate and the optimize
// compile — and a literal call that can hit is never refused.

const tableLitCohort = "tl.pulse"

// tableLitPulse is an instance over a three-row cohort (score 5 / 15 /
// 35, ltv 0.5 / 0.9 / 0.95, region north / south / north) with a
// numeric banding score_bands (low / mid / high), a date table fy, a
// rate grid keyed by (score_bands labels, an inline lo / hi axis) and a
// keyless table.
func tableLitPulse(t *testing.T, hide bool) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, tableLitCohort, []string{"score", "ltv", "region"}, [][]string{
		{"5", "0.5", "north"}, {"15", "0.9", "south"}, {"35", "0.95", "north"},
	})
	grid := map[string]float64{}
	for i, s := range []string{"low", "mid", "high"} {
		grid[s+"|lo"] = float64(2*i + 1)
		grid[s+"|hi"] = float64(2*i + 2)
	}
	opts := Options{FS: fsys, Extensions: Extensions{
		RangeTables: map[string]RangeTable{
			"score_bands": {NumericRanges: []NumericRangeSpec{
				{Label: "low", End: rlF64(10)},
				{Label: "mid", Start: rlF64(10), End: rlF64(20)},
				{Label: "high", Start: rlF64(20)},
			}},
			"fy": {Ranges: []DateRangeSpec{
				{Label: "Q1", Start: strp("2024-01-01"), End: strp("2024-03-31")},
			}},
		},
		LookupTables: map[string]LookupTable{
			"rate_grid": {Rows: grid, Keys: []LookupAxis{
				{Name: "score", RangeTable: "score_bands"},
				{Name: "ltv", Values: []string{"lo", "hi"}},
			}},
			"free": {Rows: map[string]float64{"a": 1}},
		},
	}}
	if hide {
		opts.FeatureProfile = &FeatureProfile{Features: []string{"capability:process", "AGG_COUNT", "FILTER_EXPRESSION"}}
	}
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// tableLitCase is one expression and the refusal both sides raise.
type tableLitCase struct {
	name, expr string
	code       perr.Code
	details    map[string]any
}

func tableLitCases() []tableLitCase {
	return []tableLitCase{
		{"lookup unknown table", `lookup("nope", "low", "lo") > 0`, perr.PULSE_LOOKUP_TABLE_UNKNOWN,
			map[string]any{"table": "nope"}},
		{"lookup off-axis literal", `lookup("rate_grid", "low", "zz") > 0`, perr.PULSE_LOOKUP_MISS,
			map[string]any{"table": "rate_grid", "keys": []any{"low", "zz"}}},
		{"lookup off range-table axis", `lookup("rate_grid", "other", "lo") > 0`, perr.PULSE_LOOKUP_MISS,
			map[string]any{"table": "rate_grid", "keys": []any{"other", "lo"}}},
		{"lookup wrong arity", `lookup("rate_grid", "low") > 0`, perr.PULSE_LOOKUP_MISS,
			map[string]any{"table": "rate_grid", "keys": []any{"low"}}},
		{"lookup numeric literal key", `lookup("rate_grid", 1, "lo") > 0`, perr.PULSE_LOOKUP_MISS,
			map[string]any{"table": "rate_grid", "keys": []any{"1", "lo"}}},
		{"lookup null literal key", `lookup("rate_grid", "low", nil) > 0`, perr.PULSE_LOOKUP_MISS,
			map[string]any{"table": "rate_grid", "null_key": float64(1)}},
		{"range_label unknown table", `range_label("nope", score) == "low"`, perr.PULSE_RANGE_TABLE_UNKNOWN,
			map[string]any{"operator": "range_label", "table": "nope"}},
		{"range_label categorical on numeric table", `range_label("score_bands", region) == "low"`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH,
			map[string]any{"operator": "range_label", "table": "score_bands", "want": "numeric", "got": "string"}},
		{"range_label string literal on date table", `range_label("fy", "2024-01-05") == "Q1"`, perr.PULSE_RANGE_TABLE_KIND_MISMATCH,
			map[string]any{"operator": "range_label", "table": "fy", "want": "date", "got": "string"}},
	}
}

// tableLitEntry is a coded refusal normalised through JSON (details as
// the wire carries them).
func tableLitEntry(t *testing.T, code, msg string, details map[string]any) descriptor.EnvelopeEntry {
	t.Helper()
	b, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return descriptor.EnvelopeEntry{Code: code, Message: msg, Details: d}
}

// runEntry is err's coded refusal.
func runEntry(t *testing.T, err error) descriptor.EnvelopeEntry {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("run error %v is not coded", err)
	}
	return tableLitEntry(t, string(ce.Code), ce.Message, ce.Details)
}

// predictEntry is the predict result's single error.
func predictEntry(t *testing.T, pred *descriptor.PredictResult, err error) descriptor.EnvelopeEntry {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if pred.Valid || len(pred.Errors) == 0 {
		t.Fatalf("predict valid, want a refusal: %+v", pred)
	}
	e := pred.Errors[0]
	return tableLitEntry(t, e.Code, e.Message, e.Details)
}

// assertTableLitParity checks predict and the run raised c's refusal
// with one code, message and details.
func assertTableLitParity(t *testing.T, c tableLitCase, pe, re descriptor.EnvelopeEntry) {
	t.Helper()
	if pe.Code != string(c.code) || re.Code != string(c.code) {
		t.Fatalf("codes predict %s / run %s, want %s (predict %+v, run %+v)", pe.Code, re.Code, c.code, pe, re)
	}
	if !reflect.DeepEqual(pe, re) {
		t.Errorf("predict and run differ:\npredict %+v\nrun     %+v", pe, re)
	}
	for k, want := range c.details {
		if got := pe.Details[k]; !reflect.DeepEqual(got, want) {
			t.Errorf("details[%s] = %#v, want %#v", k, got, want)
		}
	}
}

// TestTableLiteral_PredictMatchesRuntime_Formula: every literal fault in
// an ATTR_FORMULA is refused by predict with the run's code, message and
// details.
func TestTableLiteral_PredictMatchesRuntime_Formula(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, false)
	ctx := context.Background()
	for _, c := range tableLitCases() {
		t.Run(c.name, func(t *testing.T) {
			req := &Request{
				Cohort:       &types.Cohort{Filename: tableLitCohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: c.expr + " ? 1 : 0"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}},
			}
			pred, perr := p.Predict(ctx, req)
			pe := predictEntry(t, pred, perr)
			_, err := p.Process(ctx, req)
			assertTableLitParity(t, c, pe, runEntry(t, err))
		})
	}
}

// TestTableLiteral_PredictMatchesRuntime_Filter: the same faults in a
// FILTER_EXPRESSION and inside a total() argument.
func TestTableLiteral_PredictMatchesRuntime_Filter(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, false)
	ctx := context.Background()
	for _, c := range tableLitCases() {
		t.Run(c.name, func(t *testing.T) {
			req := &Request{
				Cohort:       &types.Cohort{Filename: tableLitCohort},
				Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: c.expr}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
			}
			pred, perr := p.Predict(ctx, req)
			pe := predictEntry(t, pred, perr)
			_, err := p.Process(ctx, req)
			assertTableLitParity(t, c, pe, runEntry(t, err))

			total := &Request{
				Cohort:       &types.Cohort{Filename: tableLitCohort},
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: "score / total((" + c.expr + ") ? score : 0)"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}},
			}
			pred, perr = p.Predict(ctx, total)
			pe = predictEntry(t, pred, perr)
			_, err = p.Process(ctx, total)
			assertTableLitParity(t, c, pe, runEntry(t, err))
		})
	}
}

// TestTableLiteral_HiddenRangeTables: with capability:range_tables
// hidden, a literal range_label table is unknown on both sides.
func TestTableLiteral_HiddenRangeTables(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, true)
	ctx := context.Background()
	c := tableLitCase{"hidden", `range_label("score_bands", score) == "low"`, perr.PULSE_RANGE_TABLE_UNKNOWN,
		map[string]any{"operator": "range_label", "table": "score_bands"}}
	req := &Request{
		Cohort:       &types.Cohort{Filename: tableLitCohort},
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: c.expr}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
	}
	pred, perr := p.Predict(ctx, req)
	pe := predictEntry(t, pred, perr)
	_, err := p.Process(ctx, req)
	assertTableLitParity(t, c, pe, runEntry(t, err))
}

// TestTableLiteral_HitsAreNotRefused: literal calls that can hit — an
// on-axis key, a key whose "|" spells both parts, a keyless table, a
// computed key, a numeric field on a numeric table — pass predict and
// run.
func TestTableLiteral_HitsAreNotRefused(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, false)
	ctx := context.Background()
	for _, expr := range []string{
		`lookup("rate_grid", "low", "lo") > 0`,
		`lookup("rate_grid", "mid|hi") > 0`,
		`lookup("free", "a") > 0`,
		`lookup("rate_grid", range_label("score_bands", score), ltv > 0.8 ? "hi" : "lo") > 0`,
		`range_label("score_bands", score) != "zz"`,
		`range_label("fy", 19727) == "Q1" || true`,
	} {
		req := &Request{
			Cohort:       &types.Cohort{Filename: tableLitCohort},
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}
		pred, err := p.Predict(ctx, req)
		if err != nil || !pred.Valid {
			t.Errorf("%s: predict %v %+v", expr, err, pred)
			continue
		}
		if _, err := p.Process(ctx, req); err != nil {
			t.Errorf("%s: run %v", expr, err)
		}
	}
}

// TestTableLiteral_PredictMatchesRuntime_Overlay: an OVERLAY_FORMULA's
// literal lookup() fault is refused by predict and the run alike, the
// spec located (index, kind).
func TestTableLiteral_PredictMatchesRuntime_Overlay(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, false)
	ctx := context.Background()
	c := tableLitCase{"overlay", `value * lookup("nope", "low", "lo")`, perr.PULSE_LOOKUP_TABLE_UNKNOWN,
		map[string]any{"table": "nope", "index": float64(0), "kind": string(types.OverlayKindFormula)}}
	req := &Request{
		Cohort:       &types.Cohort{Filename: tableLitCohort},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
		Overlays: []types.OverlaySpec{{
			Name:   "rated",
			Kind:   types.OverlayKindFormula,
			Scope:  types.OverlayScopeGroup,
			Params: json.RawMessage(`{"formula":` + string(tableLitJSON(t, c.expr)) + `}`),
		}},
	}
	pred, perr := p.Predict(ctx, req)
	pe := predictEntry(t, pred, perr)
	_, err := p.Process(ctx, req)
	assertTableLitParity(t, c, pe, runEntry(t, err))

	// An on-axis literal call (its first axis a range table's labels)
	// passes both sides.
	req.Overlays[0].Params = json.RawMessage(`{"formula":"value * lookup(\"rate_grid\", \"low\", \"lo\")"}`)
	if pred, err := p.Predict(ctx, req); err != nil || !pred.Valid {
		t.Fatalf("on-axis overlay: predict %v %+v", err, pred)
	}
	if _, err := p.Process(ctx, req); err != nil {
		t.Fatalf("on-axis overlay: run %v", err)
	}
}

// TestTableLiteral_PredictMatchesRuntime_Optimize: an optimize
// objective's literal range_label() table is refused by predict compose
// and the run alike, the block located.
func TestTableLiteral_PredictMatchesRuntime_Optimize(t *testing.T) {
	t.Parallel()

	p := tableLitPulse(t, false)
	ctx := context.Background()
	c := &ComposedRequest{
		Requests: []*Request{{
			Cohort:       &types.Cohort{Filename: tableLitCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}},
		}},
		Optimize: []types.OptimizeSpec{{
			Name:      "band",
			Variables: []types.OptimizeVariable{{Name: "v", Lower: types.BoundValue(0), Upper: types.BoundValue(9)}},
			Objective: `v * (range_label("nope", v) == "low" ? 1 : 0)`,
			Direction: types.OptimizeMaximize,
		}},
	}
	env, err := p.PredictCompose(ctx, c)
	if err != nil || len(env.Errors) == 0 {
		t.Fatalf("predict compose: %v %+v, want a refusal", err, env)
	}
	pe := tableLitEntry(t, env.Errors[0].Code, env.Errors[0].Message, env.Errors[0].Details)
	_, runErr := p.Compose(ctx, c)
	tc := tableLitCase{"optimize", "", perr.PULSE_RANGE_TABLE_UNKNOWN,
		map[string]any{"operator": "range_label", "table": "nope", "optimize": float64(0), "field": "optimize[0].objective"}}
	assertTableLitParity(t, tc, pe, runEntry(t, runErr))
}

func tableLitJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
