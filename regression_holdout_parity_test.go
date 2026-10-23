package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// regression_holdout_parity_test.go — RegressionSpec.Holdout composed
// with the shared regression slots (transformed predictors E3-S2), and
// the two parity gates that pin it against the multi-step reference
// route over the materialized cohort:
//
//   - gate A (fit): a holdout fit is bit for bit the fit of the same
//     spec over the materialized rows filtered to NOT the predicate —
//     coefficients, inference, resample replicates (a derived bootstrap
//     seed included), selection path, bounds and penalty. Only the
//     holdout block differs.
//   - gate B (figures): RegressionResult.Holdout's rmse / mae / n equal
//     bit for bit what a Compose fit of that reference, scored by
//     ATTR_REG_PREDICT and folded by AGG_RMSE / AGG_MAE over the
//     materialized rows filtered to the predicate, answer — the holdout
//     fold is AGG_RMSE's own (same per-row loss, same record order, same
//     row rule: both sides present, weight valid and > 0), so n is
//     AGG_RMSE's n_scored and a zero-weight held-out row counts in
//     neither. r2 has no aggregator; its reference is a test-side fold
//     over AGG_RMSE's own loss_sum (bit-identical numerator) and the
//     held-out rows' weighted total sum of squares, compared ≤ 1e-12
//     relative because the test reads those rows back in output order,
//     not record order.
//
// Both routes run live in this process over the same stored bytes, so
// bit identity never depends on FMA (agent-practices).

// holdoutParityPredicate holds out every fourth week.
const holdoutParityPredicate = "week % 4 == 0"

// holdoutParityCase is one composition case: spec (no holdout) fitted
// with holdout pred over windowParityDerive.
type holdoutParityCase struct {
	name string
	spec *types.RegressionSpec
	pred string
	// check runs case-specific assertions on the single-route result.
	check func(t *testing.T, single *types.RegressionResult)
}

// holdoutParitySpec is the base OLS of gate A named name.
func holdoutParitySpec(name string) *types.RegressionSpec { return windowParityOLS(name) }

// holdoutParityRoutes runs one case through both gates: Process the
// single request (derive + spec with the holdout), Materialize derive,
// fit spec on the materialized rows where !pred (gate A), then score
// that fit through a Compose slot over the rows where pred (gate B).
// It returns the single route's result.
func holdoutParityRoutes(t *testing.T, p *Pulse, c holdoutParityCase) *types.RegressionResult {
	t.Helper()
	ctx := context.Background()
	pred := c.pred
	if pred == "" {
		pred = holdoutParityPredicate
	}

	single := windowParityDerive()
	hs := *c.spec
	hs.Holdout = pred
	single.Regressions = []*types.RegressionSpec{&hs}
	resp, err := p.Process(ctx, single)
	if err != nil {
		t.Fatalf("single route Process: %v", err)
	}
	got := resp.Regressions[0]
	if got == nil || got.Error != nil || got.Holdout == nil {
		t.Fatalf("single route holdout fit failed: %+v", got)
	}

	mat := "holdout-" + c.name + ".pulse"
	if _, err := p.Materialize(ctx, windowParityDerive(), mat, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("reference Materialize: %v", err)
	}
	expr := func(e string) []*types.Filterer {
		return []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: e}}
	}
	trainFit := func() *Request {
		return &Request{Cohort: &types.Cohort{Filename: mat}, Filterers: expr("!(" + pred + ")"),
			Regressions: []*types.RegressionSpec{c.spec}}
	}

	// Gate A: the fit is the filtered fit; only the holdout block differs.
	refResp, err := p.Process(ctx, trainFit())
	if err != nil {
		t.Fatalf("reference fit Process: %v", err)
	}
	fit := *got
	fit.Holdout = nil
	assertRegressionsBitEqual(t, []*types.RegressionResult{&fit}, refResp.Regressions)

	// Gate B: the figures are ATTR_REG_PREDICT + AGG_RMSE / AGG_MAE over
	// the held-out rows.
	scoreAgg := func(op types.AggregationType, label string) *types.Aggregation {
		return &types.Aggregation{Type: op, Field: c.spec.Target, Label: label,
			Params: json.RawMessage(`{"prediction_field":"p"}`), Weight: c.spec.Weight}
	}
	fitName := c.spec.Name
	scorer := &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"` + fitName + `"}`)}
	figures := &Request{Label: "held", Cohort: &types.Cohort{Filename: mat}, Filterers: expr(pred),
		Attributes:   []*types.Attribute{scorer},
		Aggregations: []*types.Aggregation{scoreAgg(types.AGG_RMSE, "rmse"), scoreAgg(types.AGG_MAE, "mae")}}
	// The held-out rows themselves, one data row each (WIN_ROW_NUMBER,
	// read by nothing, keeps a row-local slot's rows), for the r2 fold.
	rows := &Request{Label: "rows", Cohort: &types.Cohort{Filename: mat}, Filterers: expr(pred),
		Attributes: []*types.Attribute{scorer},
		Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "row", PartitionBy: []string{"market"},
			OrderBy: []types.OrderKey{{Field: "week"}}}}}
	ref, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: fitName, Request: trainFit()}},
		Requests: []*Request{figures, rows},
	})
	if err != nil {
		t.Fatalf("reference Compose: %v", err)
	}
	if f := ref.Fits[fitName]; f == nil {
		t.Fatalf("reference fit %s missing", fitName)
	} else {
		assertRegressionsBitEqual(t, []*types.RegressionResult{&fit}, []*types.RegressionResult{f.RegressionResult})
	}
	agg := ref.Responses[0]
	if len(agg.Data) != 1 || agg.Components == nil || len(agg.Components.Aggregations) != 2 {
		t.Fatalf("reference figures slot: data %v, components %+v", agg.Data, agg.Components)
	}
	h := got.Holdout
	bitEq := func(what string, g, w float64) {
		t.Helper()
		if !(g == w || (math.IsNaN(g) && math.IsNaN(w))) {
			t.Errorf("holdout %s = %v (%#x), AGG route %v (%#x)", what, g, math.Float64bits(g), w, math.Float64bits(w))
		}
	}
	bitEq("rmse", h.RMSE, agg.Data[0]["rmse"].(float64))
	bitEq("mae", h.MAE, agg.Data[0]["mae"].(float64))
	rmseOp := agg.Components.Aggregations[0].Operator
	if n := rmseOp["n_scored"]; n != h.N {
		t.Errorf("holdout n = %d, AGG_RMSE n_scored = %v", h.N, n)
	}

	// r2 = 1 − loss_sum / Σw·(y − ȳ)² over the scored held-out rows.
	var sw, swy float64
	type yw struct{ y, w float64 }
	var scored []yw
	for _, r := range ref.Responses[1].Data {
		y, okY := numberOf(r[c.spec.Target])
		_, okP := r["p"].(float64)
		if r[c.spec.Target] == nil || !okY || !okP {
			continue
		}
		w := 1.0
		if ws := c.spec.Weight.Spec(); ws != nil {
			w, _ = numberOf(r[ws.Field])
			if !(w > 0) {
				continue
			}
		}
		scored = append(scored, yw{y, w})
		sw += w
		swy += w * y
	}
	if len(scored) != h.N {
		t.Fatalf("reference scored %d held-out rows, holdout n = %d", len(scored), h.N)
	}
	mean := swy / sw
	var sst float64
	for _, s := range scored {
		d := s.y - mean
		sst += s.w * (d * d)
	}
	wantR2 := 1 - rmseOp["loss_sum"].(float64)/sst
	if rel := math.Abs(h.R2-wantR2) / math.Max(1, math.Abs(wantR2)); !(rel <= 1e-12) {
		t.Errorf("holdout r2 = %.17g, reference %.17g (rel %g)", h.R2, wantR2, rel)
	}
	if c.check != nil {
		c.check(t, got)
	}
	return got
}

// holdoutParityTrainN is the train-row n of the base fit: every
// surviving week (3..52) not ≡ 0 mod 4, minus each market's leading
// null lag (week 3, a train week).
func holdoutParityTrainN() int {
	n := 0
	for wk := 3; wk <= windowParityWeeks; wk++ {
		if wk%4 != 0 {
			n++
		}
	}
	return 2*n - 2
}

// holdoutParityHeldN is the held-out row count (every surviving week ≡ 0
// mod 4, both markets; none carries a null lag).
func holdoutParityHeldN() int {
	n := 0
	for wk := 3; wk <= windowParityWeeks; wk++ {
		if wk%4 == 0 {
			n++
		}
	}
	return 2 * n
}

// TestHoldout_ParityCompositions is gates A and B over every shared slot
// a holdout composes with: bounds, penalty (l2, elasticnet), selection,
// bootstrap (explicit and derived seed), jackknife and a weight — each
// fit on the train rows only, each scored by its point-estimate model.
func TestHoldout_ParityCompositions(t *testing.T) {
	p := windowParityPulse(t, Options{})
	trainN := func(t *testing.T, r *types.RegressionResult) {
		t.Helper()
		if r.NObs != holdoutParityTrainN() {
			t.Fatalf("n_obs = %d, want %d (train rows only)", r.NObs, holdoutParityTrainN())
		}
		if r.Holdout.N != holdoutParityHeldN() || r.Holdout.NNull != 0 {
			t.Fatalf("holdout n = %d / n_null %d, want %d / 0", r.Holdout.N, r.Holdout.NNull, holdoutParityHeldN())
		}
	}
	resampled := func(t *testing.T, r *types.RegressionResult) {
		t.Helper()
		trainN(t, r)
		if r.BootstrapRequested != 48 || r.BootstrapSucceeded == 0 {
			t.Fatalf("bootstrap %d of %d replicates succeeded", r.BootstrapSucceeded, r.BootstrapRequested)
		}
	}
	with := func(name string, mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := holdoutParitySpec(name)
		mut(s)
		return s
	}
	cases := []holdoutParityCase{
		{name: "plain", spec: holdoutParitySpec("plain"), check: trainN},
		{name: "bounds", spec: with("bounds", func(s *types.RegressionSpec) {
			s.Bounds = &types.RegressionBounds{Nonnegative: []string{"tv_ad", "radio_lag", "search_ewma"}}
		}), check: func(t *testing.T, r *types.RegressionResult) {
			trainN(t, r)
			if c := r.Coefficients["search_ewma"]; c != 0 {
				t.Fatalf("bounded search_ewma = %v, want 0 (the bound binds on the train rows)", c)
			}
		}},
		{name: "ridge", spec: with("ridge", func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.5 }), check: trainN},
		{name: "elasticnet", spec: with("elasticnet", func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.05, 0.5
		}), check: trainN},
		{name: "selection", spec: with("selection", func(s *types.RegressionSpec) {
			s.Predictors = append(s.Predictors, "radio", "w")
			s.Selection, s.Criterion = "backward", "aic"
		}), check: func(t *testing.T, r *types.RegressionResult) {
			trainN(t, r)
			if len(r.SelectedFeatures) == 0 || len(r.SelectedFeatures) >= 6 {
				t.Fatalf("selected %v: want a strict, non-empty subset of the 6 candidates", r.SelectedFeatures)
			}
		}},
		{name: "bootstrap", spec: with("bootstrap", func(s *types.RegressionSpec) {
			s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 48, 20261023
		}), check: resampled},
		// rng_seed 0: the seed derives from the spec and the fitted rows,
		// so it must be derived from the holdout-free spec over the train
		// rows for the replicates to match the filtered fit.
		{name: "bootstrap-derived-seed", spec: with("bootstrap-derived-seed", func(s *types.RegressionSpec) {
			s.Resample, s.BootstrapIters = "bootstrap", 48
		}), check: resampled},
		{name: "jackknife", spec: with("jackknife", func(s *types.RegressionSpec) { s.Resample = "jackknife" }), check: func(t *testing.T, r *types.RegressionResult) {
			trainN(t, r)
			if r.Resample != "jackknife" || len(r.StdErrors) == 0 {
				t.Fatalf("jackknife resample %q, std errors %v", r.Resample, r.StdErrors)
			}
		}},
		// A delete-a-group jackknife at rng_seed 0 assigns its groups from
		// the derived seed: the train rows' assignment.
		{name: "jackknife-groups", spec: with("jackknife-groups", func(s *types.RegressionSpec) {
			s.Resample, s.JackknifeGroups = "jackknife", 8
		}), check: func(t *testing.T, r *types.RegressionResult) {
			trainN(t, r)
			if r.JackknifeGroups != 8 {
				t.Fatalf("jackknife groups = %d, want 8", r.JackknifeGroups)
			}
		}},
		{name: "weighted", spec: with("weighted", func(s *types.RegressionSpec) {
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		}), check: trainN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { holdoutParityRoutes(t, p, c) })
	}
}

// TestHoldout_ParityZeroWeightRows: held-out (and train) rows of weight
// 0 contribute to neither the fit nor the figures — holdout n is
// AGG_RMSE's n_scored, which skips them too — on both gates.
func TestHoldout_ParityZeroWeightRows(t *testing.T) {
	rows := windowParityRows()
	zeroed := 0
	for i := range rows {
		wk := int(rows[i][1].(uint64))
		if wk%8 == 0 || wk%10 == 5 { // held-out weeks 8, 16, … and train weeks 5, 15, …
			rows[i][6] = 0.0
			if wk%8 == 0 {
				zeroed++
			}
		}
	}
	p := windowParityPulseRows(t, Options{}, rows)
	spec := holdoutParitySpec("zero-weight")
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	holdoutParityRoutes(t, p, holdoutParityCase{name: "zero-weight", spec: spec,
		check: func(t *testing.T, r *types.RegressionResult) {
			if want := holdoutParityHeldN() - zeroed; r.Holdout.N != want {
				t.Fatalf("holdout n = %d, want %d (zero-weight held-out rows excluded, as AGG_RMSE excludes them)", r.Holdout.N, want)
			}
		}})
}

// TestHoldout_ParityNullPredicate: a predicate reading a window label is
// null on each market's leading lag; those rows join neither side
// (n_null), exactly as a FILTER_EXPRESSION on the materialized column
// drops them from both the !pred fit and the pred scoring.
func TestHoldout_ParityNullPredicate(t *testing.T) {
	p := windowParityPulse(t, Options{})
	spec := &types.RegressionSpec{Name: "null-pred", Type: types.REG_OLS, Target: "sales", Predictors: []string{"tv_ad", "trend"}}
	holdoutParityRoutes(t, p, holdoutParityCase{name: "null-pred", spec: spec, pred: "radio_lag > 1.6",
		check: func(t *testing.T, r *types.RegressionResult) {
			if r.Holdout.NNull != 2 {
				t.Fatalf("holdout n_null = %d, want 2 (each market's leading null lag)", r.Holdout.NNull)
			}
			if r.NObs+r.Holdout.N != 2*(windowParityWeeks-2)-2 {
				t.Fatalf("train %d + held %d != every non-null row", r.NObs, r.Holdout.N)
			}
		}})
}

// holdoutBuiltinCases is one holdout case per built-in REG_* type
// (TestHoldout_EveryScorableBuiltin requires the list to cover
// types.AllRegressionTypes()).
var holdoutBuiltinCases = map[types.RegressionType]func() *types.RegressionSpec{
	types.REG_OLS: func() *types.RegressionSpec { return holdoutParitySpec("reg-ols") },
	types.REG_GLM: func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "reg-glm", Type: types.REG_GLM, Family: "gamma", Target: "sales",
			Predictors: []string{"trend", "radio_h"}}
	},
	types.REG_BAYES_LINEAR: func() *types.RegressionSpec {
		s := holdoutParitySpec("reg-bayes-linear")
		s.Type = types.REG_BAYES_LINEAR
		return s
	},
}

// TestHoldout_EveryScorableBuiltin: every built-in regression type has a
// scorer and takes a holdout; each passes gates A and B.
func TestHoldout_EveryScorableBuiltin(t *testing.T) {
	p := windowParityPulse(t, Options{})
	for _, typ := range types.AllRegressionTypes() {
		mk, ok := holdoutBuiltinCases[typ]
		if !ok {
			t.Errorf("%s has no holdout parity case: add one to holdoutBuiltinCases", typ)
			continue
		}
		t.Run(string(typ), func(t *testing.T) {
			spec := mk()
			if spec.Type != typ {
				t.Fatalf("case builds %s, want %s", spec.Type, typ)
			}
			holdoutParityRoutes(t, p, holdoutParityCase{name: spec.Name, spec: spec})
		})
	}
}

// TestHoldout_ResampleScoresPointEstimate: under bootstrap and jackknife
// the coefficients are the train rows' point estimate, and the holdout
// figures are those of the plain fit's — the replicates never score.
func TestHoldout_ResampleScoresPointEstimate(t *testing.T) {
	p := windowParityPulse(t, Options{})
	ctx := context.Background()
	run := func(mut func(*types.RegressionSpec)) *types.RegressionResult {
		t.Helper()
		req := windowParityDerive()
		s := holdoutParitySpec("m")
		s.Holdout = holdoutParityPredicate
		mut(s)
		req.Regressions = []*types.RegressionSpec{s}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if r := resp.Regressions[0]; r.Error != nil || r.Holdout == nil {
			t.Fatalf("fit failed: %+v", r)
		}
		return resp.Regressions[0]
	}
	plain := run(func(*types.RegressionSpec) {})
	for name, mut := range map[string]func(*types.RegressionSpec){
		"bootstrap": func(s *types.RegressionSpec) { s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 32, 5 },
		"jackknife": func(s *types.RegressionSpec) { s.Resample = "jackknife" },
	} {
		t.Run(name, func(t *testing.T) {
			r := run(mut)
			for k, v := range plain.Coefficients {
				if r.Coefficients[k] != v {
					t.Fatalf("%s coefficient %s = %v, point estimate %v", name, k, r.Coefficients[k], v)
				}
			}
			if *r.Holdout != *plain.Holdout && !(math.IsNaN(r.Holdout.R2) && math.IsNaN(plain.Holdout.R2)) {
				t.Fatalf("%s holdout %+v, point-estimate holdout %+v", name, *r.Holdout, *plain.Holdout)
			}
		})
	}
}

// holdoutExtensionInstance is the weekly fixture on an instance
// registering the centred least-squares model, with or without its
// scorer.
func holdoutExtensionInstance(t *testing.T, scorer extend.RegressionScorerFactory) *Pulse {
	t.Helper()
	return windowParityPulse(t, Options{Extensions: Extensions{Regressions: []RegressionRegistration{centredRegistration(scorer)}}})
}

// TestHoldout_ParityExtensionWithScorer: a registered regression with a
// Scorer takes a holdout with no declaration — fit on the train rows,
// scored by its own scorer — and passes gates A and B.
func TestHoldout_ParityExtensionWithScorer(t *testing.T) {
	p := holdoutExtensionInstance(t, centredScorerFactory)
	spec := &types.RegressionSpec{Name: "centred", Type: regScoredName, Target: "sales", Predictors: []string{"tv_ad"}}
	got := holdoutParityRoutes(t, p, holdoutParityCase{name: "centred", spec: spec})
	if got.Holdout.N != holdoutParityHeldN() || !(got.Holdout.RMSE > 0) {
		t.Fatalf("extension holdout = %+v", *got.Holdout)
	}
}

// TestHoldout_ExtensionManifestParam: the manifest row of an extension
// regression lists the holdout slot param exactly when its registration
// supplies a Scorer, and predict accepts a holdout on that model with no
// declaration while refusing it (no_scorer) on one without.
func TestHoldout_ExtensionManifestParam(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		scorer extend.RegressionScorerFactory
	}{{"scorer", centredScorerFactory}, {"no-scorer", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			p := holdoutExtensionInstance(t, tc.scorer)
			var params []string
			for _, m := range p.Manifest(ctx).Extensions.Regressions {
				if m.Name == string(regScoredName) {
					for _, prm := range m.Params {
						params = append(params, prm.Name)
					}
				}
			}
			if len(params) == 0 {
				t.Fatalf("manifest lacks %s", regScoredName)
			}
			if has := slices.Contains(params, "holdout"); has != (tc.scorer != nil) {
				t.Fatalf("manifest holdout param present = %v, want %v (params %v)", has, tc.scorer != nil, params)
			}
			req := windowParityDerive()
			req.Regressions = []*types.RegressionSpec{{Name: "centred", Type: regScoredName, Target: "sales",
				Predictors: []string{"tv_ad"}, Holdout: holdoutParityPredicate}}
			env, err := p.PredictEnvelope(ctx, req)
			if err != nil {
				t.Fatalf("PredictEnvelope: %v", err)
			}
			_, runErr := p.Process(ctx, req)
			if tc.scorer != nil {
				if len(env.Errors) != 0 || runErr != nil {
					t.Fatalf("scorer model: predict errors %+v, run %v; want both accepted", env.Errors, runErr)
				}
				return
			}
			var ce *perr.CodedError
			if !stderrors.As(runErr, &ce) || len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) ||
				ce.Code != perr.PULSE_MODEL_SCORE_INVALID || env.Errors[0].Message != ce.Message || ce.Details["reason"] != "no_scorer" {
				t.Fatalf("no-scorer model: predict errors %+v, run %v; want the same no_scorer refusal", env.Errors, runErr)
			}
		})
	}
}
