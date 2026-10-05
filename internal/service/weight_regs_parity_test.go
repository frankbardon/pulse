package service

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Regression / regression-attribute parity harness
// (weighting-inferential E4-S3; the z / t score attributes since E5-S2):
// every weight-aware REG_* and attribute
// through the aggregator harness's arms, stores and weight sources
// (weight_parity_test.go), per kind the manifest's weight_kinds
// advertises:
//
//   - Unity (TestWeightUnityParity/regressions): an all-1.0 weight
//     answers BYTE-identically to no weight once each fit's sum_weights /
//     n_eff are pinned to n_obs and shed.
//   - Expansion (TestWeightFrequencyExpansionParity/regressions): kind
//     frequency — integer weights answer like the physically duplicated
//     rows run unweighted (n_obs stays the raw row count; sum_weights is
//     the expanded n_obs); kind probability — scale invariance, weights
//     × testScaleC answer the same figures with sum_weights × c.
//
// Every arm the unweighted request reaches is run (REG_GLM is
// buffered-only; the plain fits also stream and merge). An attribute's
// per-row value is read back per k group: the fitted value and residual
// through a weight-following AGG_AVERAGE (the weighted mean over rows IS
// the mean over the expanded copies), the leverage through an opted-out
// AGG_SUM (a row's leverage is w × one copy's, so the row sum equals the
// copies' sum). Components are off: the floor keys of those read-back
// slots are the aggregator harness's business.

// regParityOp is one weight-aware regression or regression attribute.
type regParityOp struct {
	name string
	reg  *types.RegressionSpec // nil: an attribute row
	attr types.AttributeType
	// flat reads a score attribute back ungrouped, so the request takes
	// the streaming two-pass arms (a grouped two-pass attribute buffers).
	flat bool
	// tol is the relative tolerance of the expansion / scale arms.
	tol float64
}

var regParityPredictors = []string{"x", "g"}

func regParityOLS(mut func(s *types.RegressionSpec)) *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: regParityPredictors}
	if mut != nil {
		mut(s)
	}
	return s
}

func regParityGLM(family, target string, predictors ...string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: types.REG_GLM, Family: family, Target: target, Predictors: predictors, Tol: 1e-14, MaxIters: 100}
}

var regParityOps = []regParityOp{
	{name: "ols", reg: regParityOLS(nil), tol: 1e-10},
	{name: "ridge", reg: regParityOLS(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.3 }), tol: 1e-10},
	// Coordinate descent stops at its tolerance on both sides.
	{name: "lasso", reg: regParityOLS(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Tol, s.MaxIters = "l1", 0.05, 1e-13, 100000 }), tol: 1e-8},
	{name: "elasticnet", reg: regParityOLS(func(s *types.RegressionSpec) {
		s.Penalty, s.Alpha, s.L1Ratio, s.Tol, s.MaxIters = "elasticnet", 0.05, 0.5, 1e-13, 100000
	}), tol: 1e-8},
	{name: "glm_binomial", reg: regParityGLM("binomial", "b", "x", "y"), tol: 1e-9},
	{name: "glm_poisson", reg: regParityGLM("poisson", "g", "x", "y"), tol: 1e-9},
	{name: "glm_gamma", reg: regParityGLM("gamma", "y", "x", "g"), tol: 1e-9},
	{name: "bayes", reg: &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Target: "y", Predictors: regParityPredictors}, tol: 1e-10},
	{name: "attr_fitted", attr: types.ATTR_REG_FITTED, tol: 1e-10},
	{name: "attr_residual", attr: types.ATTR_REG_RESIDUAL, tol: 1e-10},
	{name: "attr_leverage", attr: types.ATTR_REG_LEVERAGE, tol: 1e-10},
	// The weighted population-sd scores (U12 E5-S2) on y: scale-free, so
	// the scale arm answers the same figures; a row's score is its
	// copies' score, so the weighted per-k mean is the expanded one.
	{name: "attr_zscore", attr: types.ATTR_ZSCORE, tol: 1e-10},
	{name: "attr_tscore", attr: types.ATTR_TSCORE, tol: 1e-10},
	// Ungrouped: the weight-following mean / sd / skewness of the scores
	// (0 / 1 / skew(y) for z) — any weight the attribute missed moves them.
	{name: "attr_zscore_flat", attr: types.ATTR_ZSCORE, flat: true, tol: 1e-10},
	{name: "attr_tscore_flat", attr: types.ATTR_TSCORE, flat: true, tol: 1e-10},
}

func (o regParityOp) surface() (weightSurface, string) {
	if o.reg != nil {
		return weightSurfaceRegressions, string(o.reg.Type)
	}
	return weightSurfaceAttributes, string(o.attr)
}

func (o regParityOp) kinds() []types.WeightKind {
	s, op := o.surface()
	return manifestWeightKinds(s)[op]
}

// build returns the row's request builder over path.
func (o regParityOp) build(path string) func() *types.Request {
	return func() *types.Request {
		off := true
		req := &types.Request{Cohort: &types.Cohort{Filename: path}, DisableComponents: &off,
			Aggregations: []*types.Aggregation{testSteerCount()}}
		if o.reg != nil {
			spec := *o.reg
			spec.Name = "reg_" + o.name
			req.Regressions = []*types.RegressionSpec{&spec}
			return req
		}
		attr := &types.Attribute{Type: o.attr, Label: "attr", Target: "y", Predictors: regParityPredictors}
		if o.attr == types.ATTR_ZSCORE || o.attr == types.ATTR_TSCORE {
			attr = &types.Attribute{Type: o.attr, Label: "attr", Field: "y"}
		}
		req.Attributes = []*types.Attribute{attr}
		if o.flat {
			for _, op := range []types.AggregationType{types.AGG_AVERAGE, types.AGG_STDDEV, types.AGG_SKEWNESS} {
				req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: op, Field: "attr", Label: "v_" + string(op)})
			}
			return req
		}
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "k"}}
		readBack := &types.Aggregation{Type: types.AGG_AVERAGE, Field: "attr", Label: "v"}
		if o.attr == types.ATTR_REG_LEVERAGE {
			readBack = &types.Aggregation{Type: types.AGG_SUM, Field: "attr", Label: "v", Weight: types.NullSlotWeight()}
		}
		req.Aggregations = append(req.Aggregations, readBack)
		return req
	}
}

func assertRegParityCoverage(t *testing.T) {
	t.Helper()
	have := map[weightSurface]map[string][]types.WeightKind{weightSurfaceRegressions: {}, weightSurfaceAttributes: {}}
	for _, r := range regParityOps {
		s, op := r.surface()
		if len(r.kinds()) == 0 {
			t.Errorf("regParityOps row %s: the manifest advertises no weight kind for %s — drop the row or fix the class table", r.name, op)
		}
		for _, k := range r.kinds() {
			if !slices.Contains(have[s][op], k) {
				have[s][op] = append(have[s][op], k)
			}
		}
	}
	for s, h := range have {
		assertWeightKindCoverage(t, s, h, "regParityOps, weight_regs_parity_test.go")
	}
}

// regUnityParity is TestWeightUnityParity's regression half.
func regUnityParity(t *testing.T, store *parityStore) {
	t.Run("coverage", assertRegParityCoverage)
	streamed := 0
	for _, mode := range parityModes() {
		for _, row := range regParityOps {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				path := store.paths[mode.cohort]
				base := runArmRequest(t, store, mode, path, row.build(path), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.name, mode.name)
					return
				}
				if mode.name == "streaming" {
					streamed++
				}
				stripSteer(base)
				want := mustMarshal(t, base)
				for _, src := range sourcesFor(row.kinds()) {
					t.Run(src.name, func(t *testing.T) {
						got := runArmRequest(t, store, mode, path, row.build(path), &src)
						stripSteer(got)
						if row.reg != nil {
							shedRegUnity(t, got, src.kind)
						}
						if g := mustMarshal(t, got); string(g) != string(want) {
							t.Errorf("unity weight changed the answer:\n weighted   %s\n unweighted %s", g, want)
						}
					})
				}
			})
		}
	}
	if streamed == 0 {
		t.Fatal("no regression ran on the streaming arm: the streaming half is vacuous")
	}
}

// shedRegUnity asserts each fit's weight keys carry their unity values
// (sum_weights = n_obs; n_eff = n_obs under probability, absent under
// frequency) and removes them.
func shedRegUnity(t *testing.T, resp *types.Response, kind types.WeightKind) {
	t.Helper()
	if len(resp.Regressions) == 0 {
		t.Fatal("weighted response has no regression results")
	}
	for _, r := range resp.Regressions {
		n := float64(r.NObs)
		if r.SumWeights == 0 {
			t.Fatalf("%s: no sum_weights — the weight did not apply", r.Name)
		}
		if r.SumWeights != n {
			t.Errorf("%s: unity sum_weights %v, want n_obs %v", r.Name, r.SumWeights, n)
		}
		switch {
		case kind == types.WeightKindProbability && r.NEff != n:
			t.Errorf("%s: unity n_eff %v, want n_obs %v", r.Name, r.NEff, n)
		case kind == types.WeightKindFrequency && r.NEff != 0:
			t.Errorf("%s: n_eff %v under kind frequency", r.Name, r.NEff)
		}
		r.SumWeights, r.NEff = 0, 0
	}
}

// regExpansionParity is TestWeightFrequencyExpansionParity's regression
// half (shared stores; scaled holds every weight × testScaleC).
func regExpansionParity(t *testing.T, weighted, expanded, scaled *parityStore) {
	t.Run("coverage", assertRegParityCoverage)
	src := map[types.WeightKind]paritySource{}
	for _, s := range paritySources()[:2] { // request_probability, request_frequency
		src[s.kind] = s
	}
	streamed := 0
	for _, mode := range parityModes() {
		for _, row := range regParityOps {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				for _, kind := range row.kinds() {
					t.Run(string(kind), func(t *testing.T) {
						s := src[kind]
						switch kind {
						case types.WeightKindFrequency:
							base := runArmRequest(t, expanded, mode, expanded.paths[mode.cohort], row.build(expanded.paths[mode.cohort]), nil)
							if base == nil {
								t.Skipf("%s does not run on the %s arm", row.name, mode.name)
							}
							if mode.name == "streaming" {
								streamed++
							}
							stripSteer(base)
							got := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							stripSteer(got)
							assertRegsExpanded(t, row, got, base)
						case types.WeightKindProbability:
							if runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), nil) == nil {
								t.Skipf("%s does not run on the %s arm", row.name, mode.name)
							}
							base := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							stripSteer(base)
							got := runArmRequest(t, scaled, mode, scaled.paths[mode.cohort], row.build(scaled.paths[mode.cohort]), &s)
							stripSteer(got)
							assertRegsScaled(t, row, got, base)
						default:
							t.Fatalf("no parity arm for weight kind %q", kind)
						}
					})
				}
			})
		}
	}
	if streamed == 0 {
		t.Fatal("no regression expanded on the streaming arm: the streaming half is vacuous")
	}
}

// regWire is the part of a response a row compares: the fits, or the
// attribute's read-back data.
func regWire(t *testing.T, row regParityOp, resp *types.Response) []map[string]any {
	t.Helper()
	var v any = resp.Data
	if row.reg != nil {
		v = resp.Regressions
	}
	var out []map[string]any
	if err := json.Unmarshal(mustMarshal(t, v), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("nothing to compare")
	}
	return out
}

// assertRegsExpanded: the frequency-weighted fit matches the expanded
// run within the row's tolerance; n_obs (raw) and converged_iters (an
// iteration count, not a figure) are not compared — sum_weights equals
// the expanded n_obs exactly instead, and no n_eff appears.
func assertRegsExpanded(t *testing.T, row regParityOp, got, want *types.Response) {
	t.Helper()
	g, w := regWire(t, row, got), regWire(t, row, want)
	if len(g) != len(w) {
		t.Fatalf("%d results, expanded %d", len(g), len(w))
	}
	skip := map[string]bool{"n_obs": true, "sum_weights": true, "converged_iters": true}
	for i := range w {
		if row.reg != nil {
			if g[i]["sum_weights"] == nil || g[i]["sum_weights"] != w[i]["n_obs"] {
				t.Errorf("result %d: sum_weights %v, expanded n_obs %v", i, g[i]["sum_weights"], w[i]["n_obs"])
			}
			if g[i]["n_eff"] != nil {
				t.Errorf("result %d: n_eff under kind frequency", i)
			}
		}
		compareRegWire(t, fmt.Sprintf("result %d", i), g[i], w[i], 1, row.tol, skip)
	}
}

// assertRegsScaled: weights × c under kind probability answer the same
// figures; sum_weights is × c.
func assertRegsScaled(t *testing.T, row regParityOp, got, want *types.Response) {
	t.Helper()
	g, w := regWire(t, row, got), regWire(t, row, want)
	if len(g) != len(w) {
		t.Fatalf("%d results, unscaled %d", len(g), len(w))
	}
	for i := range w {
		if row.reg != nil {
			if g[i]["n_eff"] == nil {
				t.Fatalf("result %d: no n_eff — the probability weight did not apply", i)
			}
			gs, _ := g[i]["sum_weights"].(float64)
			ws, _ := w[i]["sum_weights"].(float64)
			if !relClose(gs, ws*testScaleC, 1e-12) {
				t.Errorf("result %d: sum_weights %v, want %v × %v", i, gs, ws, testScaleC)
			}
		}
		compareRegWire(t, fmt.Sprintf("result %d", i), g[i], w[i], 1, row.tol,
			map[string]bool{"sum_weights": true, "converged_iters": true})
	}
}

// compareRegWire walks two wire values: numbers within tol of want ×
// factor (absolute below 1e-12 — a coordinate-descent zero, a residual
// mean), everything else exactly; skipped keys aside, the key sets must
// match.
func compareRegWire(t *testing.T, where string, got, want any, factor, tol float64, skip map[string]bool) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: %T, want an object", where, got)
			return
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		for _, k := range sortedNames(keys) {
			if !skip[k] {
				compareRegWire(t, where+"."+k, g[k], w[k], factor, tol, skip)
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s = %v, want %v", where, got, want)
			return
		}
		for i := range w {
			compareRegWire(t, fmt.Sprintf("%s[%d]", where, i), g[i], w[i], factor, tol, skip)
		}
	case float64:
		g, ok := got.(float64)
		if !ok {
			t.Errorf("%s = %v (%T), want %v", where, got, got, want)
			return
		}
		if !relClose(g, w*factor, tol) && math.Abs(g-w*factor) > 1e-12 {
			t.Errorf("%s = %.17g, want %.17g (rel %.3g)", where, g, w*factor, math.Abs(g-w*factor)/math.Abs(w*factor))
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", where, got, want)
		}
	}
}
