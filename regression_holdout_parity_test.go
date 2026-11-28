package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
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
	// score is appended to the reference ATTR_REG_PREDICT's params (a
	// carried score's order, or carryover false).
	score string
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
	scorer := &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"` + fitName + `"` + c.score + `}`)}
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
	bitEq("rmse", *h.RMSE, agg.Data[0]["rmse"].(float64))
	bitEq("mae", *h.MAE, agg.Data[0]["mae"].(float64))
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
	if rel := math.Abs(*h.R2-wantR2) / math.Max(1, math.Abs(wantR2)); !(rel <= 1e-12) {
		t.Errorf("holdout r2 = %.17g, reference %.17g (rel %g)", *h.R2, wantR2, rel)
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	// A model regression (extension-shaped): the adstocked tv_ad enters
	// through its Hill curve, the rest are linear controls.
	types.REG_MMM_HILL: func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "reg-mmm-hill", Type: types.REG_MMM_HILL, Target: "sales",
			Predictors: []string{"tv_ad", "radio_lag", "search_ewma", "trend"}, Params: json.RawMessage(`{"channels":["tv_ad"]}`)}
	},
	// The Bayesian model regression, on a two-market geo panel over the
	// raw tv spend (it adstocks in-model), returning its draws (its
	// scorer averages over them) on a tiny sampler run; a pinned seed,
	// since the derived one hashes the spec, holdout included.
	types.REG_MMM_BAYES: func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "reg-mmm-bayes", Type: types.REG_MMM_BAYES, Target: "sales", RNGSeed: 5,
			Predictors: []string{"tv", "trend"},
			Params:     json.RawMessage(`{"channels":["tv"],"time":"week","geo":"market","chains":1,"warmup":20,"draws":8,"return_draws":true}`)}
	},
}

// holdoutBuiltinPreds overrides the holdout predicate per type:
// REG_MMM_BAYES's train rows must stay contiguous per market (a gap is
// geo_time_gap), so it holds out each market's tail.
var holdoutBuiltinPreds = map[types.RegressionType]string{types.REG_MMM_BAYES: "week > 46"}

// holdoutBuiltinScores extends the reference score per type:
// REG_MMM_BAYES adstocks in-model, so its holdout carries the train
// rows' adstock into the held-out rows' raw spend — and the reference
// ATTR_REG_PREDICT carries the same way, ordered by params.order.
var holdoutBuiltinScores = map[types.RegressionType]string{types.REG_MMM_BAYES: `,"order":{"field":"week","partition":"market"}`}

// holdoutOutcomeCases are the categorical-outcome built-ins' parity
// cases over the weekly fixture: REG_ORDINAL on the frequency weight's
// three values (numeric levels "1" < "2" < "3"), REG_MULTINOMIAL on the
// market. outcomes are the levels / categories a slot names.
var holdoutOutcomeCases = map[types.RegressionType]struct {
	spec     func() *types.RegressionSpec
	selector string
	outcomes []string
}{
	types.REG_ORDINAL: {func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "reg-ordinal", Type: types.REG_ORDINAL, Target: "w", Predictors: []string{"tv_ad", "trend"}}
	}, "level", []string{"1", "2", "3"}},
	types.REG_MULTINOMIAL: {func() *types.RegressionSpec {
		return &types.RegressionSpec{Name: "reg-multinomial", Type: types.REG_MULTINOMIAL, Target: "market", Predictors: []string{"tv_ad", "trend"}}
	}, "category", []string{"east", "west"}},
}

// holdoutOutcomeParityRoutes is holdoutParityRoutes for a categorical-
// outcome fit: gate A as there (the fit is the filtered fit, only the
// holdout block differs); gate B folds the proper scores test-side from
// one ATTR_REG_PREDICT probability column per outcome, scored by a
// Compose fit of the reference over the held-out rows — log_loss,
// brier, accuracy and (ordered) rps within 1e-12 relative (the rows
// come back in output order, not record order), n exactly — and holds
// rmse / mae / r2 absent.
func holdoutOutcomeParityRoutes(t *testing.T, p *Pulse, spec *types.RegressionSpec, selector string, outcomes []string) *types.RegressionResult {
	t.Helper()
	ctx := context.Background()
	pred := holdoutParityPredicate

	single := windowParityDerive()
	hs := *spec
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

	mat := "holdout-" + spec.Name + ".pulse"
	if _, err := p.Materialize(ctx, windowParityDerive(), mat, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("reference Materialize: %v", err)
	}
	expr := func(e string) []*types.Filterer {
		return []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: e}}
	}
	trainFit := &Request{Cohort: &types.Cohort{Filename: mat}, Filterers: expr("!(" + pred + ")"),
		Regressions: []*types.RegressionSpec{spec}}
	refResp, err := p.Process(ctx, trainFit)
	if err != nil {
		t.Fatalf("reference fit Process: %v", err)
	}
	fit := *got
	fit.Holdout = nil
	assertRegressionsBitEqual(t, []*types.RegressionResult{&fit}, refResp.Regressions)

	var cols []*types.Attribute
	for i, o := range outcomes {
		cols = append(cols, &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "p" + strconv.Itoa(i),
			Params: json.RawMessage(`{"fit":"` + spec.Name + `","output":"probability","` + selector + `":"` + o + `"}`)})
	}
	rows := &Request{Label: "rows", Cohort: &types.Cohort{Filename: mat}, Filterers: expr(pred), Attributes: cols,
		Windows: []*types.Window{{Type: types.WIN_ROW_NUMBER, Label: "row", PartitionBy: []string{"market"},
			OrderBy: []types.OrderKey{{Field: "week"}}}}}
	ref, err := p.Compose(ctx, &ComposedRequest{
		Fits:     []types.FitSpec{{Name: spec.Name, Request: trainFit}},
		Requests: []*Request{rows},
	})
	if err != nil {
		t.Fatalf("reference Compose: %v", err)
	}
	k := len(outcomes)
	ordered := spec.Type == types.REG_ORDINAL
	var n int
	var logLoss, brier, correct, rps float64
	for _, r := range ref.Responses[0].Data {
		label, ok := r[spec.Target].(string)
		if !ok {
			v, isNum := numberOf(r[spec.Target])
			if !isNum {
				continue
			}
			label = strconv.FormatFloat(v, 'g', -1, 64)
		}
		y := slices.Index(outcomes, label)
		pv := make([]float64, k)
		scored := y >= 0
		for j := range pv {
			v, isNum := r["p"+strconv.Itoa(j)].(float64)
			pv[j], scored = v, scored && isNum
		}
		if !scored {
			continue
		}
		n++
		arg := 0
		var cum float64
		for j, v := range pv {
			o := 0.0
			if j == y {
				o = 1
			}
			brier += (v - o) * (v - o)
			if v > pv[arg] {
				arg = j
			}
			if ordered && j < k-1 {
				cum += v
				c := 0.0
				if y <= j {
					c = 1
				}
				rps += (cum - c) * (cum - c) / float64(k-1)
			}
		}
		logLoss -= math.Log(pv[y])
		if arg == y {
			correct++
		}
	}
	h := got.Holdout
	if h.N != n || h.RMSE != nil || h.MAE != nil || h.R2 != nil || (h.RPS != nil) != ordered {
		t.Fatalf("holdout %+v: reference scored %d rows; want no rmse / mae / r2, rps iff ordered", *h, n)
	}
	near := func(what string, g *float64, w float64) {
		t.Helper()
		if g == nil || !(math.Abs(*g-w)/math.Max(1, math.Abs(w)) <= 1e-12) {
			t.Errorf("holdout %s = %v, reference %.17g", what, g, w)
		}
	}
	nf := float64(n)
	near("log_loss", h.LogLoss, logLoss/nf)
	near("brier", h.Brier, brier/nf)
	near("accuracy", h.Accuracy, correct/nf)
	if ordered {
		near("rps", h.RPS, rps/nf)
	}
	return got
}

// TestHoldout_EveryScorableBuiltin: every built-in regression type with
// a scorer takes a holdout and passes gates A and B (REG_MMM_BAYES
// through its draws, its held-out rows' raw spend adstocked with the
// train rows' adstock carried in — a carried ATTR_REG_PREDICT's figure
// exactly). REG_ORDINAL and REG_MULTINOMIAL score a probability per
// level / category: their holdout reports the proper scores of that
// vector, pinned against ATTR_REG_PREDICT's probability columns
// (holdoutOutcomeParityRoutes). Every built-in has a scorer.
func TestHoldout_EveryScorableBuiltin(t *testing.T) {
	t.Parallel()

	p := windowParityPulse(t, Options{})
	for _, typ := range types.AllRegressionTypes() {
		if oc, outcome := holdoutOutcomeCases[typ]; outcome || model.OutcomeType(typ) {
			if _, listed := holdoutBuiltinCases[typ]; listed || !outcome || !model.OutcomeType(typ) {
				t.Errorf("%s: a categorical-outcome type takes a holdoutOutcomeCases case, never a holdoutBuiltinCases one", typ)
				continue
			}
			if _, scorable := regression.BuiltinScorer(typ); !scorable {
				t.Errorf("%s has no scorer: its ATTR_REG_PREDICT columns need one", typ)
				continue
			}
			t.Run(string(typ), func(t *testing.T) {
				holdoutOutcomeParityRoutes(t, p, oc.spec(), oc.selector, oc.outcomes)
			})
			continue
		}
		if _, scorable := regression.BuiltinScorer(typ); !scorable {
			t.Errorf("%s has no scorer: every built-in regression has one", typ)
			continue
		}
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
			holdoutParityRoutes(t, p, holdoutParityCase{name: spec.Name, spec: spec, pred: holdoutBuiltinPreds[typ], score: holdoutBuiltinScores[typ]})
		})
	}
}

// bayesHoldoutPreCarry are REG_MMM_BAYES's engine-holdout figures on
// the parity case before carry-over existed (its held-out channel
// columns scored as given), recorded on the commit before carry-over
// (db020da99^) over the detmath fixture (windowParityRowsDigest) —
// the same bits on arm64 and linux/amd64: params.carryover false must
// reproduce them.
var bayesHoldoutPreCarry = struct {
	n             int
	rmse, mae, r2 float64
}{12, 55.203258668686779, 47.849859915911452, -11.474089794004604}

// TestHoldout_BayesCarryoverOptOut: REG_MMM_BAYES under params.carryover
// false scores its held-out channel columns as given — the pre-carry-
// over figures (pinned, ≤ 1e-9 relative: recorded on one arch), equal
// bit for bit to an ATTR_REG_PREDICT with carryover false (gates A and
// B) — while the default carries the train rows' adstock in and moves
// them (TestHoldout_EveryScorableBuiltin pins that route).
func TestHoldout_BayesCarryoverOptOut(t *testing.T) {
	t.Parallel()

	p := windowParityPulse(t, Options{})
	spec := holdoutBuiltinCases[types.REG_MMM_BAYES]()
	spec.Params = json.RawMessage(`{"channels":["tv"],"time":"week","geo":"market","chains":1,"warmup":20,"draws":8,"return_draws":true,"carryover":false}`)
	got := holdoutParityRoutes(t, p, holdoutParityCase{name: "bayes-no-carry", spec: spec, pred: "week > 46", score: `,"carryover":false`})
	h, want := got.Holdout, bayesHoldoutPreCarry
	near := func(what string, g, w float64) {
		t.Helper()
		if rel := math.Abs(g-w) / math.Max(1, math.Abs(w)); !(rel <= 1e-9) {
			t.Errorf("carryover false %s = %.17g, pre-carry-over %.17g", what, g, w)
		}
	}
	if h.N != want.n {
		t.Errorf("carryover false n = %d, pre-carry-over %d", h.N, want.n)
	}
	near("rmse", *h.RMSE, want.rmse)
	near("mae", *h.MAE, want.mae)
	near("r2", *h.R2, want.r2)

	carried := holdoutBuiltinCases[types.REG_MMM_BAYES]()
	carried.Holdout = "week > 46"
	req := windowParityDerive()
	req.Regressions = []*types.RegressionSpec{carried}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if c := resp.Regressions[0].Holdout; c == nil || *c.RMSE == *h.RMSE {
		t.Errorf("carried holdout %+v did not move from the as-given %v", c, *h.RMSE)
	}
}

// TestHoldout_ResampleScoresPointEstimate: under bootstrap and jackknife
// the coefficients are the train rows' point estimate, and the holdout
// figures are those of the plain fit's — the replicates never score.
func TestHoldout_ResampleScoresPointEstimate(t *testing.T) {
	t.Parallel()

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
			if !holdoutFiguresEqual(r.Holdout, plain.Holdout) {
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
	t.Parallel()

	p := holdoutExtensionInstance(t, centredScorerFactory)
	spec := &types.RegressionSpec{Name: "centred", Type: regScoredName, Target: "sales", Predictors: []string{"tv_ad"}}
	got := holdoutParityRoutes(t, p, holdoutParityCase{name: "centred", spec: spec})
	if got.Holdout.N != holdoutParityHeldN() || !(*got.Holdout.RMSE > 0) {
		t.Fatalf("extension holdout = %+v", *got.Holdout)
	}
}

// TestHoldout_ExtensionManifestParam: the manifest row of an extension
// regression lists the holdout slot param exactly when its registration
// supplies a Scorer, and predict accepts a holdout on that model with no
// declaration while refusing it (no_scorer) on one without.
func TestHoldout_ExtensionManifestParam(t *testing.T) {
	t.Parallel()

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

// TestBayesCarryover_FittedResidualMatchCarriedPredict (U71 E2-S5): over
// a REG_MMM_BAYES geo panel (posterior-mean kernel, population value) a
// carried params.fit ATTR_REG_FITTED scores each held-out row exactly
// as the carried ATTR_REG_PREDICT does — the same Carrier, the mean
// over the draws — and ATTR_REG_RESIDUAL is the row's target minus it,
// both orchestrators.
func TestBayesCarryover_FittedResidualMatchCarriedPredict(t *testing.T) {
	t.Parallel()

	p := windowParityPulse(t, Options{})
	ctx := context.Background()
	const mat = "bayes-fitted-carry.pulse"
	if _, err := p.Materialize(ctx, windowParityDerive(), mat, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	expr := func(e string) []*types.Filterer {
		return []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: e}}
	}
	const order = `,"order":{"field":"week","partition":"market"}}`
	batch := func() *ComposedRequest {
		return &ComposedRequest{
			Fits: []types.FitSpec{{Name: "b", Request: &Request{Cohort: &types.Cohort{Filename: mat}, Filterers: expr("week <= 46"),
				Regressions: []*types.RegressionSpec{holdoutBuiltinCases[types.REG_MMM_BAYES]()}}}},
			Requests: []*Request{{Label: "held", Cohort: &types.Cohort{Filename: mat}, Filterers: expr("week > 46"),
				Attributes: []*types.Attribute{
					{Type: types.ATTR_REG_PREDICT, Label: "p", Params: json.RawMessage(`{"fit":"b"` + order)},
					{Type: types.ATTR_REG_FITTED, Label: "f", Params: json.RawMessage(`{"fit":"b"` + order)},
					{Type: types.ATTR_REG_RESIDUAL, Label: "r", Params: json.RawMessage(`{"fit":"b"` + order)},
					{Type: types.ATTR_FORMULA, Label: "df", Expression: "abs(f - p)"},
					{Type: types.ATTR_FORMULA, Label: "dr", Expression: "abs(sales - p - r)"},
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_COUNT, Field: "f", Label: "n"},
					{Type: types.AGG_SUM, Field: "p", Label: "p"},
					{Type: types.AGG_MAX, Field: "df", Label: "df"},
					{Type: types.AGG_MAX, Field: "dr", Label: "dr"},
				}}},
		}
	}
	if env, err := p.PredictCompose(ctx, batch()); err != nil || len(env.Errors) > 0 {
		t.Fatalf("predict: %v %+v", err, env.Errors)
	}
	for name, run := range composeRuns(p) {
		out, err := run(batch())
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		row := out.Responses[0].Data[0]
		if n, _ := row["n"].(float64); n == 0 {
			t.Fatalf("%s: no held-out row scored: %v", name, row)
		}
		if df, _ := row["df"].(float64); df != 0 {
			t.Errorf("%s: carried fitted differs from the carried score by up to %v", name, df)
		}
		if dr, _ := row["dr"].(float64); dr > 1e-9 {
			t.Errorf("%s: carried residual differs from sales − score by up to %v", name, dr)
		}
	}
}

// TestBayesInfluence_RefusedByDesignPredictRuntimeParity (U71 E2-S6):
// ATTR_REG_LEVERAGE, ATTR_REG_INFLUENCE and a standardized /
// studentized ATTR_REG_RESIDUAL over a REG_MMM_BAYES fit are refused
// PULSE_MODEL_SCORE_INVALID type_unsupported BY DESIGN (a posterior has
// no hat matrix) — before the carry-over rule, so the slot without
// params.order is told so rather than to add one — identically in
// predict and at run time (Compose; ComposeParallel for one slot).
func TestBayesInfluence_RefusedByDesignPredictRuntimeParity(t *testing.T) {
	t.Parallel()

	p := windowParityPulse(t, Options{})
	ctx := context.Background()
	const mat = "bayes-influence.pulse"
	if _, err := p.Materialize(ctx, windowParityDerive(), mat, MaterializeOptions{Overwrite: true}); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	for _, slot := range []struct {
		typ    types.AttributeType
		params string
	}{
		{types.ATTR_REG_LEVERAGE, `{"fit":"b"}`},
		{types.ATTR_REG_INFLUENCE, `{"fit":"b","order":{"field":"week","partition":"market"}}`},
		{types.ATTR_REG_RESIDUAL, `{"fit":"b","kind":"studentized"}`},
	} {
		t.Run(string(slot.typ), func(t *testing.T) {
			c := &ComposedRequest{
				Fits: []types.FitSpec{{Name: "b", Request: &Request{Cohort: &types.Cohort{Filename: mat},
					Regressions: []*types.RegressionSpec{holdoutBuiltinCases[types.REG_MMM_BAYES]()}}}},
				Requests: []*Request{{Label: "s", Cohort: &types.Cohort{Filename: mat},
					Attributes:   []*types.Attribute{{Type: slot.typ, Label: "v", Params: json.RawMessage(slot.params)}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "v"}}}},
			}
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 {
				t.Fatalf("predict errors = %+v, want exactly one", env.Errors)
			}
			pe := env.Errors[0]
			if perr.Code(pe.Code) != perr.PULSE_MODEL_SCORE_INVALID || pe.Details["reason"] != "type_unsupported" ||
				!strings.Contains(pe.Message, "by design") {
				t.Fatalf("predict = %s %q %v, want PULSE_MODEL_SCORE_INVALID type_unsupported by design", pe.Code, pe.Message, pe.Details)
			}
			// One orchestrator per slot (each run samples the posterior):
			// the refusal is the shared bind rule, so the slots between
			// them cover both.
			run := func() error { _, err := p.Compose(ctx, c); return err }
			if slot.typ == types.ATTR_REG_INFLUENCE {
				run = func() error {
					_, err := p.ComposeParallel(ctx, c, ComposeOptions{MaxWorkers: 2})
					return err
				}
			}
			var ce *perr.CodedError
			if err := run(); !stderrors.As(err, &ce) {
				t.Fatalf("uncoded %v", err)
			}
			if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
				t.Errorf("runtime %s %q %v\n predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
			}
		})
	}
}

// holdoutFiguresEqual reports whether two holdout blocks carry the same
// counts and, figure by figure, the same presence and bits (NaN equal
// to NaN).
func holdoutFiguresEqual(a, b *types.RegressionHoldout) bool {
	if a == nil || b == nil {
		return a == b
	}
	same := func(x, y *float64) bool {
		if x == nil || y == nil {
			return x == y
		}
		return math.Float64bits(*x) == math.Float64bits(*y) || (math.IsNaN(*x) && math.IsNaN(*y))
	}
	return a.N == b.N && a.NNull == b.NNull && same(a.RMSE, b.RMSE) && same(a.MAE, b.MAE) && same(a.R2, b.R2) &&
		same(a.LogLoss, b.LogLoss) && same(a.Brier, b.Brier) && same(a.Accuracy, b.Accuracy) && same(a.RPS, b.RPS)
}
