package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// extensions_regression_scorer_test.go pins custom-regressions E2-S2:
// a registration's Scorer scores its fits through ATTR_REG_PREDICT, a
// fit saves as a version-3 model file carrying its outputs and reloads
// on a fresh instance with identical predictions; without a Scorer
// scoring is refused (no_scorer) while the fit runs; a saved custom
// model on an instance that does not register — or hides — its type is
// PULSE_MODEL_REGRESSION_UNREGISTERED. Every refusal is predict's too.

const regScoredName types.RegressionType = "REG_ACME_CENTRED"

// centredFit is a buffered least-squares fit of target on one predictor
// reported in the model's own parameterisation: `level` and `gain`
// around the predictor's mean, which it returns as the output `shift`
// (no intercept, no coefficient named after the predictor).
type centredFit struct{ spec *types.RegressionSpec }

func (c centredFit) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	x := c.spec.Predictors[0]
	n := float64(rows.Len())
	if n < 3 {
		return extend.RegressionOutput{}, perr.NewCodedError(perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA, "too few rows")
	}
	var sx, sy float64
	for i := 0; i < rows.Len(); i++ {
		xv, _ := rows.At(i).NumericValue(x)
		yv, _ := rows.At(i).NumericValue(c.spec.Target)
		sx += xv
		sy += yv
	}
	mx, my := sx/n, sy/n
	var sxx, sxy float64
	for i := 0; i < rows.Len(); i++ {
		xv, _ := rows.At(i).NumericValue(x)
		yv, _ := rows.At(i).NumericValue(c.spec.Target)
		sxx += (xv - mx) * (xv - mx)
		sxy += (xv - mx) * (yv - my)
	}
	return extend.RegressionOutput{
		Coefficients: []extend.RegressionCoefficient{{Name: "level", Estimate: my}, {Name: "gain", Estimate: sxy / sxx}},
		Outputs:      map[string]any{"shift": mx, "spread": []float64{sxx, math.NaN()}},
	}, nil
}

// centredScorer scores level + gain·(x − shift) from the fit alone.
type centredScorer struct {
	x                  string
	level, gain, shift float64
}

func (s centredScorer) Columns() []string { return []string{s.x} }

func (s centredScorer) Score(row extend.ScoreRow) (float64, bool) {
	v, ok := row.NumericValue(s.x)
	if !ok {
		return 0, false
	}
	return centredScore(s.level, s.gain, s.shift, v), true
}

func centredScore(level, gain, shift, x float64) float64 { return level + gain*(x-shift) }

func centredScorerFactory(fit extend.FittedRegression) (extend.RowScorer, error) {
	shift, ok := fit.Outputs["shift"].(float64)
	if !ok {
		return nil, stderrors.New("no shift output")
	}
	return centredScorer{x: fit.Predictors[0], level: fit.Coefficients["level"], gain: fit.Coefficients["gain"], shift: shift}, nil
}

func centredRegistration(scorer extend.RegressionScorerFactory) RegressionRegistration {
	return RegressionRegistration{
		Name:        regScoredName,
		Description: "Least squares around the predictor's mean (test model).",
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return centredFit{spec: spec}, nil
		},
		OutputSchema: []RegressionOutputMeta{
			{Key: "shift", JSONType: "number", Description: "The predictor's mean."},
			{Key: "spread", JSONType: "array", Description: "The predictor's centred sum of squares, and an undefined figure."},
		},
		Scorer: scorer,
	}
}

func centredInstance(t *testing.T, fsys afero.Fs, regs ...RegressionRegistration) *Pulse {
	t.Helper()
	p, err := New(Options{FS: fsys, Extensions: Extensions{Regressions: regs}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func centredFitRequest() *Request {
	return &Request{
		Cohort:      &types.Cohort{Filename: fitsFacadeCohort},
		Filterers:   []*types.Filterer{{Type: types.FILTER_RANGE, Field: "week", Values: []string{"0", "79"}}},
		Regressions: []*types.RegressionSpec{{Type: regScoredName, Name: "m", Target: "y", Predictors: []string{"x"}}},
	}
}

// centredSlot scores fit m on every week (one row per week).
func centredSlot(params string, models ...types.ModelRef) *Request {
	return &Request{
		Label:  "score",
		Cohort: &types.Cohort{Filename: fitsFacadeCohort},
		Models: models,
		Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "week", Interval: 1}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(params)},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_MIN, Field: "x", Label: "x"},
			{Type: types.AGG_MIN, Field: "p", Label: "p"},
		},
	}
}

func centredJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestExtensions_RegressionScorerScoresSavesReloads: the registration's
// scorer scores every row of a Compose fit exactly as the model's own
// formula over the reported fit; the fit's save writes a version-3
// model file carrying its outputs; a fresh instance with the same
// registration reloads it and scores identically, and predict accepts
// the load.
func TestExtensions_RegressionScorerScoresSavesReloads(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	p := centredInstance(t, fsys, centredRegistration(centredScorerFactory))
	ctx := context.Background()
	const save = "models/centred" + model.FileSuffix
	out, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: centredFitRequest(), Save: save}},
		Requests: []*Request{centredSlot(`{"fit":"m"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := out.Fits["m"].RegressionResult
	shift := res.Outputs["shift"].(float64)
	data := out.Responses[0].Data
	if len(data) != 104 {
		t.Fatalf("%d rows", len(data))
	}
	for _, row := range data {
		want := centredScore(res.Coefficients["level"], res.Coefficients["gain"], shift, row["x"].(float64))
		if got := row["p"].(float64); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("week %v: score %v, want %v", row["week"], got, want)
		}
	}

	raw, err := afero.ReadFile(fsys, save)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["model_format_version"] != float64(3) || doc["type"] != string(regScoredName) {
		t.Fatalf("saved file: version %v type %v", doc["model_format_version"], doc["type"])
	}
	if outs, _ := doc["outputs"].(map[string]any); outs["shift"] != shift || !reflect.DeepEqual(outs["spread"], []any{res.Outputs["spread"].([]float64)[0], nil}) {
		t.Fatalf("saved outputs %v", doc["outputs"])
	}

	fresh := centredInstance(t, fsys, centredRegistration(centredScorerFactory))
	loadReq := centredSlot(`{"fit":"m"}`, types.ModelRef{Name: "m", Path: save})
	loaded, err := fresh.Process(ctx, loadReq)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := centredJSON(t, loaded.Data), centredJSON(t, data); a != b {
		t.Fatalf("the reloaded model scores differently:\n%s\n%s", a, b)
	}
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := fresh.PredictBytes(ctx, cohort, loadReq)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict on the load: %v %+v", err, env.Errors)
	}
}

// TestExtensions_RegressionScoreRefusals: scoring a fit whose
// registration has no Scorer (no_scorer), or a scored fit under
// params.terms (terms_unsupported) or a lower bound
// (bound_unsupported), is refused by PredictCompose exactly as Compose
// refuses it; the regression slot itself still runs without a scorer,
// and its save is refused at both.
func TestExtensions_RegressionScoreRefusals(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	ctx := context.Background()
	plain := centredInstance(t, fsys, centredRegistration(nil))
	scored := centredInstance(t, fsys, centredRegistration(centredScorerFactory))
	cases := []struct {
		name   string
		p      *Pulse
		params string
		reason string
	}{
		{"no scorer", plain, `{"fit":"m"}`, model.ReasonNoScorer},
		{"terms", scored, `{"fit":"m","terms":["x"]}`, model.ReasonTermsUnsupported},
		{"bound", scored, `{"fit":"m","bound":"upper"}`, model.ReasonBoundUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &ComposedRequest{
				Fits:     []types.FitSpec{{Name: "m", Request: centredFitRequest()}},
				Requests: []*Request{centredSlot(tc.params)},
			}
			env, err := tc.p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, perr.PULSE_MODEL_SCORE_INVALID)
			if pe.details["reason"] != tc.reason {
				t.Fatalf("predict reason %v, want %s", pe.details["reason"], tc.reason)
			}
			_, rerr := tc.p.Compose(ctx, c)
			assertSameRefusal(t, tc.name, rerr, pe)
		})
	}

	// The fit runs without a scorer: its regression result is reported.
	resp, err := plain.Process(ctx, centredFitRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Regressions) != 1 || resp.Regressions[0].Error != nil || resp.Regressions[0].Coefficients["gain"] == 0 {
		t.Fatalf("the unscored fit did not run: %+v", resp.Regressions)
	}

	// Its save is refused (E1-S4's declare-or-refuse rule), at both.
	saving := centredFitRequest()
	saving.Regressions[0].Save = "models/plain" + model.FileSuffix
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	env, err := plain.PredictBytes(ctx, cohort, saving)
	if err != nil {
		t.Fatal(err)
	}
	pe := findEnvError(t, env, perr.PROCESSING_CONFIG)
	if pe.details["slot"] != "save" {
		t.Fatalf("save refusal details %v", pe.details)
	}
	_, rerr := plain.Process(ctx, saving)
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != "save" {
		t.Fatalf("runtime save: %v", rerr)
	}
}

// TestExtensions_RegressionModelUnregistered: a saved custom model
// loaded on an instance that does not register its regression, or
// whose feature profile hides it, is refused
// PULSE_MODEL_REGRESSION_UNREGISTERED — by predict exactly as by
// Process.
func TestExtensions_RegressionModelUnregistered(t *testing.T) {
	t.Parallel()

	fsys := fitsFacadeFS(t)
	ctx := context.Background()
	const save = "models/centred" + model.FileSuffix
	saver := centredInstance(t, fsys, centredRegistration(centredScorerFactory))
	if _, err := saver.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "m", Request: centredFitRequest(), Save: save}},
		Requests: []*Request{centredSlot(`{"fit":"m"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	cohort, err := afero.ReadFile(fsys, fitsFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	unregistered := centredInstance(t, fsys)
	hidden, err := New(Options{FS: fsys, Extensions: Extensions{Regressions: []RegressionRegistration{centredRegistration(centredScorerFactory)}},
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "capability:models", string(types.ATTR_REG_PREDICT),
			string(types.REG_OLS), string(types.GROUP_RANGE), string(types.AGG_MIN)}}})
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*Pulse{"unregistered": unregistered, "hidden": hidden} {
		t.Run(name, func(t *testing.T) {
			req := centredSlot(`{"fit":"m"}`, types.ModelRef{Name: "m", Path: save})
			env, err := p.PredictBytes(ctx, cohort, req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, perr.PULSE_MODEL_REGRESSION_UNREGISTERED)
			want := map[string]any{"model": 0, "name": "m", "path": save, "type": string(regScoredName)}
			if !reflect.DeepEqual(pe.details, want) {
				t.Fatalf("details %v, want %v", pe.details, want)
			}
			for _, e := range env.Errors {
				if e.Code == string(perr.PULSE_MODEL_UNKNOWN) {
					t.Fatalf("an unloaded model is also reported unknown: %+v", env.Errors)
				}
			}
			_, rerr := p.Process(ctx, req)
			assertSameRefusal(t, name, rerr, pe)
		})
	}
}

// strayScorer reads a column that is none of the fit's predictors.
type strayScorer struct{}

func (strayScorer) Columns() []string                     { return []string{"elsewhere"} }
func (strayScorer) Score(extend.ScoreRow) (float64, bool) { return 0, false }

// panicScorer panics on every row.
type panicScorer struct{ centredScorer }

func (panicScorer) Score(extend.ScoreRow) (float64, bool) { panic("score boom") }

// TestExtensions_RegressionScorerProbed: pulse.New builds the Scorer
// over the probe fit and scores the probe rows — a Scorer error or a
// panic in it or in Score is PULSE_EXTENSION_FACTORY_PANIC (stage
// scorer), a nil scorer or one reading a column outside the predictors
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH.
func TestExtensions_RegressionScorerProbed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		scorer extend.RegressionScorerFactory
		code   perr.Code
		reason string
	}{
		{"error", func(extend.FittedRegression) (extend.RowScorer, error) { return nil, stderrors.New("nope") }, perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
		{"factory panic", func(extend.FittedRegression) (extend.RowScorer, error) { panic("boom") }, perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
		{"score panic", func(f extend.FittedRegression) (extend.RowScorer, error) {
			return panicScorer{centredScorer{x: f.Predictors[0]}}, nil
		}, perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
		{"nil scorer", func(extend.FittedRegression) (extend.RowScorer, error) { return nil, nil }, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "nil_scorer"},
		{"stray column", func(extend.FittedRegression) (extend.RowScorer, error) { return strayScorer{}, nil }, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "scorer_columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{Regressions: []RegressionRegistration{centredRegistration(tc.scorer)}}})
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code || ce.Details["stage"] != "scorer" {
				t.Fatalf("New: %v (%+v), want %s stage scorer", err, ce, tc.code)
			}
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("reason %v, want %s", ce.Details["reason"], tc.reason)
			}
		})
	}
	if _, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{Regressions: []RegressionRegistration{centredRegistration(centredScorerFactory)}}}); err != nil {
		t.Fatalf("a well-formed scorer probes clean: %v", err)
	}
}
