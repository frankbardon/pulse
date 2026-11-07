package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// window_scalar_parity_test.go pins the window scalar-expression params
// (internal/windowscalar): WIN_DISTRIBUTED_LAG decay / shape / scale /
// max_lag and WIN_EWMA alpha / decay take a number or an expression,
// resolved before the window's own reading. Predict and every runtime
// surface (Process, ProcessStream, Materialize) agree on each verdict;
// an accepted expression runs exactly as the literal it yields.

// scalarWindowReq is a request over the series cohort with one window
// of type typ and params, label "w", and the request params.
func scalarWindowReq(typ types.WindowType, params string, reqParams types.Params) *types.Request {
	w := &types.Window{Type: typ, Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}}, Params: json.RawMessage(params)}
	if typ == types.WIN_EWMA {
		w.Frame = &types.FrameSpec{Mode: "rows"}
	}
	return &types.Request{Cohort: &types.Cohort{Filename: "series.pulse"}, Params: reqParams, Windows: []*types.Window{w}}
}

// windowColumn is the "w" column of a Process run, as float bits.
func windowColumn(t *testing.T, p *pulse.Pulse, req *types.Request) []uint64 {
	t.Helper()
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process %s: %v", req.Windows[0].Params, err)
	}
	out := make([]uint64, len(resp.Data))
	for i, r := range resp.Data {
		f, ok := r["w"].(float64)
		if !ok {
			out[i] = math.MaxUint64 // a null cell
			continue
		}
		out[i] = math.Float64bits(f)
	}
	return out
}

var scalarParams = types.Params{"d": json.Number("0.7"), "k": json.Number("3"), "s": json.Number("1.5"), "c": json.Number("2"), "label": "a"}

// TestWindowScalar_AcceptedMatchesLiteral: per scalar param, an
// expression over params predicts valid (resolved — nothing in
// unresolved_scalars) and runs, on Process, ProcessStream and
// Materialize, to the very bits of the literal it yields.
func TestWindowScalar_AcceptedMatchesLiteral(t *testing.T) {
	t.Parallel()
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	cases := []struct {
		name, typ, expr, literal string
	}{
		{"distlag decay", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": "params.d", "max_lag": 4}`, `{"kernel": "geometric", "decay": 0.7, "max_lag": 4}`},
		{"distlag max_lag", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": 0.5, "max_lag": "params.k + 1"}`, `{"kernel": "geometric", "decay": 0.5, "max_lag": 4}`},
		{"distlag shape", "WIN_DISTRIBUTED_LAG", `{"kernel": "weibull_pdf", "shape": "params.s", "scale": 4, "max_lag": 5}`, `{"kernel": "weibull_pdf", "shape": 1.5, "scale": 4, "max_lag": 5}`},
		{"distlag scale", "WIN_DISTRIBUTED_LAG", `{"kernel": "weibull_cdf", "shape": 2, "scale": "params.c * 2", "max_lag": 5}`, `{"kernel": "weibull_cdf", "shape": 2, "scale": 4, "max_lag": 5}`},
		{"ewma alpha", "WIN_EWMA", `{"alpha": "params.d / 2"}`, `{"alpha": 0.35}`},
		{"ewma decay", "WIN_EWMA", `{"decay": "params.d"}`, `{"decay": 0.7}`},
	}
	ctx := context.Background()
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ := types.WindowType(tc.typ)
			req := func() *types.Request { return scalarWindowReq(typ, tc.expr, scalarParams) }
			env := predictEnvelope(t, p, fsys, "series.pulse", req())
			if len(env.Errors) != 0 {
				t.Fatalf("predict refused: %+v", env.Errors)
			}
			if pr, ok := env.Data.(*descriptor.PredictResult); !ok || pr.UnresolvedScalars != nil {
				t.Fatalf("predict listed a params-only expression as unresolved: %+v", env.Data)
			}
			got := windowColumn(t, p, req())
			want := windowColumn(t, p, scalarWindowReq(typ, tc.literal, scalarParams))
			if !reflect.DeepEqual(got, want) {
				t.Errorf("expression column %v, literal %v", got, want)
			}
			if _, err := p.ProcessStream(ctx, req()); err != nil {
				t.Errorf("ProcessStream: %v", err)
			}
			if _, err := p.Materialize(ctx, req(), fmt.Sprintf("scalar_ok%d.pulse", i), pulse.MaterializeOptions{}); err != nil {
				t.Errorf("Materialize: %v", err)
			}
		})
	}
}

// TestWindowScalar_RefusalsPredictMatchesRuntime: per scalar param and
// reason — a static value out of its operator's range (judged by the
// reading itself), max_lag's exact-integer rule, a non-finite, a
// non-numeric, an uncompilable or identifier-reading expression, an
// undeclared param and a coef() / sweep_axis() call no model binds —
// predict refuses with a message every runtime surface raises; and the
// literal-only params (WIN_LAG / WIN_LEAD offset, WIN_PCT_CHANGE /
// WIN_DELTA periods) still refuse an expression string.
func TestWindowScalar_RefusalsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()
	p, fsys := windowParityCohort(t, pulse.Extensions{})
	cases := []struct {
		name, typ, params string
		code              errors.Code
		reason            string
	}{
		{"decay folds out of range", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": "params.d + 0.3", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, ""},
		{"max_lag not an integer", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": 0.5, "max_lag": "params.k / 2"}`, errors.PULSE_WINDOW_INVALID, "param_not_integer"},
		{"max_lag above the ceiling", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": 0.5, "max_lag": "10000 + params.k"}`, errors.PULSE_WINDOW_INVALID, ""},
		{"shape not finite", "WIN_DISTRIBUTED_LAG", `{"kernel": "weibull_cdf", "shape": "params.s / 0", "scale": 2, "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, "param_not_finite"},
		{"scale not positive", "WIN_DISTRIBUTED_LAG", `{"kernel": "weibull_cdf", "shape": 2, "scale": "params.c - 2", "max_lag": 4}`, errors.PULSE_WINDOW_INVALID, ""},
		{"alpha not numeric", "WIN_EWMA", `{"alpha": "params.label"}`, errors.PULSE_WINDOW_INVALID, "param_not_numeric"},
		{"alpha out of range", "WIN_EWMA", `{"alpha": "params.c"}`, errors.PULSE_WINDOW_INVALID, ""},
		{"decay reads a field", "WIN_EWMA", `{"decay": "x / 10"}`, errors.PULSE_WINDOW_INVALID, "param_invalid"},
		{"decay does not compile", "WIN_EWMA", `{"decay": "0.5 +"}`, errors.PULSE_WINDOW_INVALID, "param_invalid"},
		{"undeclared param", "WIN_EWMA", `{"decay": "params.nope"}`, errors.PULSE_PARAMS_UNDECLARED, ""},
		{"coef with no model bound", "WIN_DISTRIBUTED_LAG", `{"kernel": "geometric", "decay": "coef(\"m\", \"x\")", "max_lag": 4}`, errors.PULSE_MODEL_UNKNOWN, ""},
		{"sweep_axis with no model bound", "WIN_EWMA", `{"decay": "sweep_axis(\"best\", \"tv\")"}`, errors.PULSE_MODEL_UNKNOWN, ""},
		{"lag offset stays literal", "WIN_LAG", `{"offset": "params.k"}`, errors.PULSE_WINDOW_INVALID, ""},
		{"lead offset stays literal", "WIN_LEAD", `{"offset": "1"}`, errors.PULSE_WINDOW_INVALID, ""},
		{"pct_change periods stay literal", "WIN_PCT_CHANGE", `{"periods": "params.k"}`, errors.PULSE_WINDOW_INVALID, ""},
		{"delta periods stay literal", "WIN_DELTA", `{"periods": "2"}`, errors.PULSE_WINDOW_INVALID, ""},
	}
	ctx := context.Background()
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			typ := types.WindowType(tc.typ)
			req := func() *types.Request { return scalarWindowReq(typ, tc.params, scalarParams) }
			env := predictEnvelope(t, p, fsys, "series.pulse", req())
			accept := map[string]bool{}
			for _, e := range env.Errors {
				if e.Code != string(tc.code) {
					continue
				}
				if tc.reason != "" && e.Details["reason"] != tc.reason {
					continue
				}
				accept[e.Message] = true
				if rule, ok := strings.CutPrefix(e.Message, "window[0] ("+tc.typ+"): "); ok {
					accept[tc.typ+": "+rule] = true
				}
				if strings.HasPrefix(tc.name, "lag") || strings.HasPrefix(tc.name, "lead") || strings.HasPrefix(tc.name, "pct") || strings.HasPrefix(tc.name, "delta") {
					if !strings.Contains(e.Message, "malformed params") {
						t.Errorf("a literal-only param refused with %q, want malformed params", e.Message)
					}
				}
			}
			if len(accept) == 0 {
				t.Fatalf("predict did not refuse with %s %s: %+v", tc.code, tc.reason, env.Errors)
			}
			_, perr := p.Process(ctx, req())
			_, serr := p.ProcessStream(ctx, req())
			_, merr := p.Materialize(ctx, req(), fmt.Sprintf("scalar_bad%d.pulse", i), pulse.MaterializeOptions{})
			for name, err := range map[string]error{"Process": perr, "ProcessStream": serr, "Materialize": merr} {
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) {
					t.Errorf("%s: accepted what predict refuses: %v", name, err)
					continue
				}
				if ce.Code != tc.code || !accept[ce.Message] {
					t.Errorf("%s raised %s %q, which predict did not report: %+v", name, ce.Code, ce.Message, env.Errors)
				}
			}
		})
	}
}

// TestWindowScalar_LoadedModelsFoldAtPredict: a request carrying
// `models` binds coef() for a window param — predict folds the read of
// the loaded model to the runtime's value (nothing under
// unresolved_scalars, never an error or a warning); Process and
// Materialize evaluate it on the model's coefficient and run exactly as
// the literal it yields; a value out of range is refused by predict
// with the very refusal (code, rule, details — the folded value
// included) every runtime surface raises.
func TestWindowScalar_LoadedModelsFoldAtPredict(t *testing.T) {
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
	coef, ok := fit.Regressions[0].Coefficients["x"]
	if !ok || math.IsNaN(coef) {
		t.Fatalf("no x coefficient: %+v", fit.Regressions[0])
	}
	const decaySrc = `abs(coef("m", "x")) / (1 + abs(coef("m", "x")))`
	decay := math.Abs(coef) / (1 + math.Abs(coef))
	models := []types.ModelRef{{Name: "m", Path: "models/m.pulse-model.json"}}
	req := func(src string) *types.Request {
		r := scalarWindowReq(types.WIN_DISTRIBUTED_LAG, `{"kernel": "geometric", "decay": `+strconv.Quote(src)+`, "max_lag": 4}`, nil)
		r.Models = models
		return r
	}

	env := predictEnvelope(t, p, fsys, "series.pulse", req(decaySrc))
	if len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Fatalf("predict: errors %+v warnings %+v", env.Errors, env.Warnings)
	}
	if pr := env.Data.(*descriptor.PredictResult); pr.UnresolvedScalars != nil {
		t.Fatalf("a loaded model's read left unresolved: %+v", pr.UnresolvedScalars)
	}
	lit := scalarWindowReq(types.WIN_DISTRIBUTED_LAG, `{"kernel": "geometric", "decay": `+strconv.FormatFloat(decay, 'g', -1, 64)+`, "max_lag": 4}`, nil)
	if got, want := windowColumn(t, p, req(decaySrc)), windowColumn(t, p, lit); !reflect.DeepEqual(got, want) {
		t.Errorf("coef() decay column %v, literal %v", got, want)
	}
	if _, err := p.Materialize(ctx, req(decaySrc), "scalar_models.pulse", pulse.MaterializeOptions{}); err != nil {
		t.Errorf("Materialize: %v", err)
	}

	// The same read out of range: predict folds it and refuses it with
	// the reading's own rule and details (the folded decay), which every
	// runtime surface raises.
	out := `1 + abs(coef("m", "x"))`
	env = predictEnvelope(t, p, fsys, "series.pulse", req(out))
	if len(env.Errors) != 1 || env.Errors[0].Code != string(errors.PULSE_WINDOW_INVALID) {
		t.Fatalf("predict did not refuse the folded range: %+v", env.Errors)
	}
	pe := env.Errors[0]
	rule, ok := strings.CutPrefix(pe.Message, "window[0] (WIN_DISTRIBUTED_LAG): ")
	if !ok || !strings.Contains(rule, "params.decay must be in [0, 1)") {
		t.Fatalf("predict refused %q, want the decay range rule", pe.Message)
	}
	if got, want := pe.Details["decay"], 1+math.Abs(coef); got != want {
		t.Errorf("predict folded decay %v, want %v", got, want)
	}
	for name, run := range map[string]func() error{
		"Process":       func() error { _, err := p.Process(ctx, req(out)); return err },
		"ProcessStream": func() error { _, err := p.ProcessStream(ctx, req(out)); return err },
		"Materialize": func() error {
			_, err := p.Materialize(ctx, req(out), "scalar_models_bad.pulse", pulse.MaterializeOptions{})
			return err
		},
	} {
		var ce *errors.CodedError
		if err := run(); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WINDOW_INVALID || ce.Message != "WIN_DISTRIBUTED_LAG: "+rule || ce.Details["decay"] != pe.Details["decay"] {
			t.Errorf("%s: %v, want predict's refusal %q (decay %v)", name, err, pe.Message, pe.Details["decay"])
		}
	}
}
