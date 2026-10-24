package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// request_params_test.go — Request.Params (U58 E3-S1) through the
// public facade: params.<name> in FILTER_EXPRESSION, ATTR_FORMULA, a
// total() argument and OVERLAY_FORMULA; exact integers; predict ≡ run
// for every refusal; no stale program across runs; the
// capability:params feature gate.

// paramsRows is the fixture: wave cycles 1..5, x = i, region
// alternates west / east, n = 1.
const paramsRows = 20

// openParams writes params.pulse (wave u16, x f64, region categorical,
// n f64) and, when withField, params_field.pulse whose schema also
// carries a field named params.
func openParams(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	if opts.FS == nil {
		opts.FS = afero.NewMemMapFs()
	}
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	d := encoding.NewDictionary()
	for _, r := range []string{"west", "east"} {
		if _, err := d.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name string, extra bool) {
		fields := []encoding.Field{
			{Name: "wave", Type: encoding.FieldTypeU16},
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
			{Name: "n", Type: encoding.FieldTypeF64},
		}
		if extra {
			fields = append(fields, encoding.Field{Name: "params", Type: encoding.FieldTypeF64})
		}
		b, err := p.NewCohortBuilder(ctx, name, encoding.Schema{Fields: fields}, pulse.CohortBuilderOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < paramsRows; i++ {
			row := pulse.CohortRow{uint64(i%5 + 1), float64(i), []string{"west", "east"}[i%2], 1.0}
			if extra {
				row = append(row, float64(i)*10)
			}
			if err := b.Append(row); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := b.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("params.pulse", false)
	write("params_field.pulse", true)
	return p
}

func paramsCohort() *types.Cohort { return &types.Cohort{Filename: "params.pulse"} }

// countWhere is AGG_COUNT over the rows FILTER_EXPRESSION expr keeps.
func countWhere(expr string, params types.Params) *types.Request {
	return &types.Request{
		Cohort:       paramsCohort(),
		Params:       params,
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "c"}},
	}
}

func paramsFloat(t *testing.T, resp *types.Response, key string) float64 {
	t.Helper()
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %d, want 1: %+v", len(resp.Data), resp.Data)
	}
	switch v := resp.Data[0][key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	t.Fatalf("%s = %T %v", key, resp.Data[0][key], resp.Data[0][key])
	return 0
}

// TestParams_FilterExpression: `wave == params.from_wave` keeps exactly
// the rows of that wave — buffered, streamed and across decode workers.
func TestParams_FilterExpression(t *testing.T) {
	ctx := context.Background()
	for _, workers := range []int{1, 4} {
		p := openParams(t, pulse.Options{DecodeWorkers: workers})
		resp, err := p.Process(ctx, countWhere("wave == params.from_wave", types.Params{"from_wave": json.Number("3")}))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if got := paramsFloat(t, resp, "c"); got != paramsRows/5 {
			t.Fatalf("workers %d: count = %v, want %d", workers, got, paramsRows/5)
		}
		// A string param against a categorical label.
		resp, err = p.Process(ctx, countWhere("region == params.region", types.Params{"region": "east"}))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if got := paramsFloat(t, resp, "c"); got != paramsRows/2 {
			t.Fatalf("region count = %v, want %d", got, paramsRows/2)
		}
	}
}

// TestParams_FormulaInListAndTotal: `x * params.k` in ATTR_FORMULA, a
// list param behind `in`, and a total() argument reading a param.
func TestParams_FormulaInListAndTotal(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	req := &types.Request{
		Cohort:    paramsCohort(),
		Params:    types.Params{"k": json.Number("2.5"), "waves": []any{json.Number("1"), json.Number("2")}},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "wave in params.waves"}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Label: "scaled", Expression: "x * params.k"},
			{Type: types.ATTR_FORMULA, Label: "share", Expression: "x / total(x * params.k)"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "scaled", Label: "s"},
			{Type: types.AGG_SUM, Field: "x", Label: "sx"},
			{Type: types.AGG_SUM, Field: "share", Label: "sh"},
		},
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	// waves 1 and 2 are rows i%5 ∈ {0, 1}.
	var sx float64
	for i := 0; i < paramsRows; i++ {
		if i%5 < 2 {
			sx += float64(i)
		}
	}
	if got := paramsFloat(t, resp, "sx"); got != sx {
		t.Fatalf("sum x = %v, want %v", got, sx)
	}
	if got := paramsFloat(t, resp, "s"); got != sx*2.5 {
		t.Fatalf("sum scaled = %v, want %v", got, sx*2.5)
	}
	if got := paramsFloat(t, resp, "sh"); got < 0.399 || got > 0.401 {
		t.Fatalf("sum share = %v, want 1/2.5", got)
	}
}

// TestParams_LargeIntegerExact: a u64 param beyond 2^53 survives the
// wire decode and compares exactly — 2^53+1 is not 2^53.
func TestParams_LargeIntegerExact(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	var req types.Request
	body := `{"cohort":{"filename":"params.pulse"},"params":{"big":9007199254740993,"top":18446744073709551615},` +
		`"attributes":[{"type":"ATTR_FORMULA","label":"hit","expression":"params.big == 9007199254740993 ? 1 : 0"},` +
		`{"type":"ATTR_FORMULA","label":"miss","expression":"params.big == 9007199254740992 ? 1 : 0"},` +
		`{"type":"ATTR_FORMULA","label":"top","expression":"params.top == params.top && params.top != params.big ? 1 : 0"}],` +
		`"aggregations":[{"type":"AGG_SUM","field":"hit","label":"hit"},{"type":"AGG_SUM","field":"miss","label":"miss"},{"type":"AGG_SUM","field":"top","label":"top"}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if n, ok := req.Params["big"].(json.Number); !ok || n != "9007199254740993" {
		t.Fatalf("decoded big = %T %v, want the exact json.Number", req.Params["big"], req.Params["big"])
	}
	resp, err := p.Process(ctx, &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if hit, miss := paramsFloat(t, resp, "hit"), paramsFloat(t, resp, "miss"); hit != paramsRows || miss != 0 {
		t.Fatalf("hit = %v, miss = %v; want %d, 0", hit, miss, paramsRows)
	}
	if got := paramsFloat(t, resp, "top"); got != paramsRows {
		t.Fatalf("top = %v, want %d", got, paramsRows)
	}
}

// TestParams_NoStaleProgram: the same request run again with a changed
// value — sequentially and concurrently on one instance — always reads
// its own value, never a program compiled for another run.
func TestParams_NoStaleProgram(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	for w := 1; w <= 5; w++ {
		resp, err := p.Process(ctx, countWhere("wave <= params.w", types.Params{"w": w}))
		if err != nil {
			t.Fatal(err)
		}
		if got := paramsFloat(t, resp, "c"); got != float64(w*paramsRows/5) {
			t.Fatalf("w=%d: count = %v, want %d", w, got, w*paramsRows/5)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		w := i%5 + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := p.Process(ctx, countWhere("wave <= params.w", types.Params{"w": w}))
			if err != nil {
				errs <- err
				return
			}
			if got := resp.Data[0]["c"]; fmt.Sprint(got) != fmt.Sprint(w*paramsRows/5) {
				errs <- fmt.Errorf("w=%d: count = %v", w, got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestParams_OverlayFormula: OVERLAY_FORMULA reads a param on a
// crosstab host.
func TestParams_OverlayFormula(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	req := &types.Request{
		Cohort: paramsCohort(),
		Params: types.Params{"k": 10},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
		},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula, Params: json.RawMessage(`{"formula":"cell * params.k"}`)}},
	}
	resp, err := p.Process(ctx, req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
		t.Fatalf("overlays = %+v", resp.Overlays)
	}
	cell := resp.Crosstab.Matrix.Cells[0][0].Value
	got := resp.Overlays[0].Payload.Matrix.Cells[0][0].Value
	if fmt.Sprint(got) != fmt.Sprint(toF(cell)*10) {
		t.Fatalf("overlay cell = %v, want 10 × %v", got, cell)
	}
}

func toF(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return -1
}

// TestParams_PredictMatchesRuntime: every params refusal — a null value,
// a mixed list, a non-identifier name, an undeclared read (filter,
// formula, total() argument, holdout, overlay formula) and a read over
// a cohort carrying a field named params — is the same code and
// message at predict and at run.
func TestParams_PredictMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	formula := func(expr string, params types.Params) *types.Request {
		return &types.Request{
			Cohort:       paramsCohort(),
			Params:       params,
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}},
		}
	}
	collision := countWhere("wave == params.w", types.Params{"w": 1})
	collision.Cohort = &types.Cohort{Filename: "params_field.pulse"}
	holdout := &types.Request{
		Cohort: paramsCohort(),
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "x", Predictors: []string{"wave"},
			Holdout: "wave == params.h"}},
	}
	overlay := &types.Request{
		Cohort: paramsCohort(),
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
		},
		Overlays: []types.OverlaySpec{{Kind: types.OverlayKindFormula, Params: json.RawMessage(`{"formula":"cell * params.k"}`)}},
	}
	cases := []struct {
		name string
		req  *types.Request
		code perr.Code
	}{
		{"null value", countWhere("wave == params.w", types.Params{"w": nil}), perr.PULSE_PARAMS_INVALID},
		{"mixed list", countWhere("wave in params.w", types.Params{"w": []any{1, "a"}}), perr.PULSE_PARAMS_INVALID},
		{"invalid name", countWhere("wave == 1", types.Params{"a-b": 1}), perr.PULSE_PARAMS_INVALID},
		{"undeclared filter", countWhere("wave == params.w", types.Params{"v": 1}), perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared no params", countWhere("wave == params.w", nil), perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared formula", formula("x * params.k", nil), perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared total", formula("x / total(x * params.k)", types.Params{"j": 1}), perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared holdout", holdout, perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared overlay", overlay, perr.PULSE_PARAMS_UNDECLARED},
		{"field collision", collision, perr.PULSE_PARAMS_FIELD_COLLISION},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, err := p.PredictEnvelope(ctx, c.req)
			if err != nil {
				t.Fatalf("PredictEnvelope: %v", err)
			}
			_, runErr := p.Process(ctx, c.req)
			ce := requireCodedError(t, runErr, c.code)
			found := false
			for _, e := range env.Errors {
				found = found || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if !found {
				t.Fatalf("predict errors %+v lack the run's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
}

// TestParams_FieldNamedParams: a cohort field named params still reads
// as a bare identifier (and through $env) — only a params.<name> read
// collides.
func TestParams_FieldNamedParams(t *testing.T) {
	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	for _, expr := range []string{"params * 2", `$env["params"] * 2`} {
		resp, err := p.Process(ctx, &types.Request{
			Cohort:       &types.Cohort{Filename: "params_field.pulse"},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "f", Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "s"}},
		})
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		want := 0.0
		for i := 0; i < paramsRows; i++ {
			want += float64(i) * 20
		}
		if got := paramsFloat(t, resp, "s"); got != want {
			t.Fatalf("%s: sum = %v, want %v", expr, got, want)
		}
	}
}

// TestParams_FeatureProfileGate: a profile without capability:params
// refuses a set params as an unknown field, and its payload schema has
// no Request params key; enabling the feature runs it.
func TestParams_FeatureProfileGate(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	openParams(t, pulse.Options{FS: fsys})
	open := func(features ...string) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	req := countWhere("wave == params.w", types.Params{"w": 2})
	hidden := open("capability:process", "FILTER_EXPRESSION", "AGG_COUNT")
	_, err := hidden.Process(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if fmt.Sprint(ce.Details["unknown_keys"]) != "[params]" {
		t.Fatalf("details = %v", ce.Details)
	}
	enabled := open("capability:process", "FILTER_EXPRESSION", "AGG_COUNT", "capability:params")
	resp, err := enabled.Process(ctx, req)
	if err != nil || paramsFloat(t, resp, "c") != paramsRows/5 {
		t.Fatalf("enabled: %v %+v", err, resp)
	}
	for _, c := range []struct {
		p    *pulse.Pulse
		want bool
	}{{hidden, false}, {enabled, true}} {
		raw, err := c.p.PayloadSchema()
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		_, has := doc.Defs["Request"].Properties["params"]
		if has != c.want {
			t.Fatalf("payload schema Request.params present = %v, want %v", has, c.want)
		}
	}
	if _, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: []string{"capability:process", "capability:params"}}}); err == nil ||
		!strings.Contains(err.Error(), string(perr.PULSE_FEATURE_PROFILE_DEPENDENCY)) {
		t.Fatalf("params without an expression operator: %v", err)
	}
}
