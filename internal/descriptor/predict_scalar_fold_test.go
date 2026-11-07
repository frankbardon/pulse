package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/aggconst"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// predict_scalar_fold_test.go pins predict's fold of a loaded model's
// figures into the window and AGG_CONSTANT scalar slots (#708 part 1):
// the static pass (windowScalarEnv over the request's loaded catalog)
// resolves a coef() / sweep_axis() / response() read of a loaded
// `models` entry to the very value the runtime's pass
// (processing.ResolveWindowScalars / ResolveExtensionScalars over the
// Set the runtime binds) reads, and refuses with the runtime's very
// code, message and details.

// foldTruth is an inline OLS with standard errors and no intervals (a
// lower / upper read is PULSE_MODEL_INTERVALS_UNAVAILABLE).
const foldTruth = `{"model_format_version": 1, "type": "REG_OLS", "target": "y",
	"predictors": ["x"], "terms": ["x"],
	"coefficients": {"(intercept)": 0.1, "x": "params.b"},
	"std_errors": {"x": 0.05}, "n_obs": 40}`

// foldCurve is an inline media-mix fit with a declared adstock and
// sweep axes: response() and sweep_axis() read it.
const foldCurve = `{"model_format_version": 1, "type": "REG_MMM_HILL", "target": "y",
	"predictors": ["x"], "terms": ["x"],
	"coefficients": {"(intercept)": 1, "x": 0.6, "k:x": 9, "s:x": 1.6},
	"features": ["adstock", "sweep_axes"],
	"adstock": {"x": {"kernel": "geometric", "decay": 0.6, "max_lag": 4, "normalize": false}},
	"sweep_axes": {"decay": 0.35}}`

// foldPopulation is a saved population: a vector fit, so coef() of it
// is PULSE_MODEL_KIND_MISMATCH.
const foldPopulation = `{"population_format_version": 1, "name": "brand", "operator": "MAT_COVARIANCE",
	"labels": ["q1", "q2"], "members": ["q1", "q2"], "basis": "unweighted",
	"n": 3, "n_mass": 3, "n_weight_invalid": 0, "sum_weights": 3, "sum_weights_sq": 3,
	"mean": [1, 2], "m2": [[2, 1], [1, 2]], "provenance": {"cohort": "c.pulse"}}`

const foldPopulationPath = "pops/brand.pulse-population.json"

// foldFiles serves the population file to both sides.
type foldFiles struct{}

func (foldFiles) ReadModelFile(path string) ([]byte, error) {
	if path == foldPopulationPath {
		return []byte(foldPopulation), nil
	}
	return nil, stderrors.New("no file " + path)
}

// foldRequest is a request loading the fold fixtures.
func foldRequest() *types.Request {
	return &types.Request{Params: foldParams, Models: foldModels(),
		Populations: []types.PopulationRef{{Name: "brand", Path: foldPopulationPath}}}
}

func foldModels() []types.ModelRef {
	return []types.ModelRef{
		{Name: "truth", Inline: json.RawMessage(foldTruth)},
		{Name: "curve", Inline: json.RawMessage(foldCurve)},
	}
}

var foldParams = types.Params{"b": json.Number("0.25"), "stat": "lower", "fit": "curve"}

// foldPredict is predict's options for req: its loaded catalog
// installed, as Predict installs it.
func foldPredict(t *testing.T, req *types.Request) *PredictOptions {
	t.Helper()
	opts := &PredictOptions{ModelLoader: foldFiles{}}
	cat, _, cerr := predictModels(model.RequestRefs(req), opts.populations(), opts)
	if cerr != nil {
		t.Fatalf("predict load: %v", cerr)
	}
	return opts.withModels(cat)
}

// foldRuntime is the runtime's registry and bound Set for req: its
// params installed, its `models` loaded and bound
// (Service.loadRequestModels: model.LoadFiles, then WithLoaded).
func foldRuntime(t *testing.T, req *types.Request) (*processing.ExtensionRegistry, *model.Set) {
	t.Helper()
	params, cerr := exprenv.NormalizeParams(req.Params)
	if cerr != nil {
		t.Fatal(cerr)
	}
	refs := model.RequestRefs(req)
	loaded, cerr := model.LoadFiles(refs, foldFiles{}.ReadModelFile, scalarslot.InlineNumber)
	if cerr != nil {
		t.Fatal(cerr)
	}
	set, err := (*model.Set)(nil).WithLoaded(refs, loaded)
	if err != nil {
		t.Fatal(err)
	}
	return (*processing.ExtensionRegistry)(nil).WithParams(params), set
}

// sameCoded fails unless predict's refusal p and the runtime's error r
// are one coded error: code, message and details.
func sameCoded(t *testing.T, p *errors.CodedError, r error) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(r, &ce) {
		t.Fatalf("runtime accepted what predict refused (%v): %v", p, r)
	}
	if p.Code != ce.Code || p.Message != ce.Message || !reflect.DeepEqual(p.Details, ce.Details) {
		t.Errorf("predict refused %s %q %v; runtime %s %q %v", p.Code, p.Message, p.Details, ce.Code, ce.Message, ce.Details)
	}
}

// TestScalarFold_WindowParamsMatchRuntime: per bound-model reader and
// stat, a window param reading a loaded model folds at predict to the
// literal the runtime writes (nothing left unresolved), and a value out
// of the reading's range — or a reader refusal no literal check sees —
// is refused at predict exactly as the runtime refuses it.
func TestScalarFold_WindowParamsMatchRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, src string
		refused   bool
	}{
		{"coef point", `coef("truth", "x")`, false},
		{"coef se", `coef("truth", "x", "se") * 2`, false},
		{"coef intercept", `coef("truth", "(intercept)") + 0.2`, false},
		{"sweep_axis", `sweep_axis("curve", "decay")`, false},
		{"response", `response("curve", "x", 10) / 10`, false},
		{"coef out of range", `coef("truth", "x") * 8`, true},
		{"response out of range", `response("curve", "x", 10) + 1`, true},
		{"intervals unavailable", `coef("truth", "x", params.stat)`, true},
		{"kind mismatch", `coef("bra" + "nd", "x")`, true},
		{"term unknown", `coef("truth", "z" + "")`, true},
		{"sweep axis unknown", `sweep_axis(params.fit, "shape" + "")`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &types.Window{Type: types.WIN_EWMA, Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}},
				Frame: &types.FrameSpec{Mode: "rows"}, Params: json.RawMessage(`{"alpha": ` + strconv.Quote(tc.src) + `}`)}
			req := foldRequest()
			req.Windows = []*types.Window{w}
			opts := foldPredict(t, req)
			if un := windowUnresolved(req, opts, ""); un != nil {
				t.Fatalf("a loaded-model read left unresolved: %+v", un)
			}
			res, srefs := resolveWindowScalarsStatic(w, windowScalarEnv(req, opts))
			exts, set := foldRuntime(t, req)
			out, rerr := processing.ResolveWindowScalars(exts, set, req.Windows)
			if len(srefs) > 0 {
				if !tc.refused {
					t.Fatalf("predict refused %v", srefs[0].Coded())
				}
				sameCoded(t, srefs[0].Coded(), rerr)
				return
			}
			if rerr != nil {
				t.Fatalf("predict accepted what the runtime refused: %v", rerr)
			}
			if len(res.Deferred) != 0 {
				t.Fatalf("predict deferred %v", res.Deferred)
			}
			if got, want := string(res.Window.Params), string(out[0].Params); got != want {
				t.Errorf("predict folded %s, runtime %s", got, want)
			}
			// The reading's range rule runs on the folded params, as the
			// runtime's factory runs it on the same literal.
			if refs := windowparams.CheckDeferred(res.Window, res.Deferred); (len(refs) > 0) != tc.refused {
				t.Errorf("range refusals %+v, want refused=%v", refs, tc.refused)
			}
		})
	}
}

// TestScalarFold_ConstantMatchesRuntime: an AGG_CONSTANT expr reading a
// loaded model folds at predict to the literal the runtime writes, and
// its range refusal is the runtime's.
func TestScalarFold_ConstantMatchesRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, typ, src string
		refused        bool
	}{
		{"f64 coef", "f64", `coef("truth", "x") * 3`, false},
		{"f64 response", "f64", `response("curve", "x", 4)`, false},
		{"u8 out of range", "u8", `coef("truth", "x") * 4000`, true},
		{"intervals unavailable", "f64", `coef("truth", "x", "up" + "per")`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agg := &types.Aggregation{Type: types.AGG_CONSTANT, Label: "c",
				Params: json.RawMessage(`{"type": "` + tc.typ + `", "expr": ` + strconv.Quote(tc.src) + `}`)}
			req := foldRequest()
			req.Aggregations = []*types.Aggregation{agg}
			opts := foldPredict(t, req)
			if un := constantUnresolved(req, opts, ""); un != nil {
				t.Fatalf("a loaded-model read left unresolved: %+v", un)
			}
			env := windowScalarEnv(req, opts)
			res := aggconst.ResolveRequest(req, func() (scalarslot.Env, exprenv.Bound) { return env, env.Declared() }, false)
			exts, set := foldRuntime(t, req)
			out, rerr := processing.ResolveExtensionScalars(req, exts, set)
			if len(res.Refusals) > 0 {
				if !tc.refused {
					t.Fatalf("predict refused %v", res.Refusals[0].Refusal.Coded())
				}
				sameCoded(t, res.Refusals[0].Refusal.Coded(), rerr)
				return
			}
			if tc.refused || rerr != nil {
				t.Fatalf("predict accepted; runtime %v", rerr)
			}
			if got, want := string(res.Request.Aggregations[0].Params), string(out.Aggregations[0].Params); got != want {
				t.Errorf("predict folded %s, runtime %s", got, want)
			}
		})
	}
}

// TestScalarFold_UnloadedReadsStayUnresolved: only a request's own
// loaded entries fold. A read of a fit the catalog binds without
// figures (a Compose `fits` entry), a read beside another request-bound
// function, and a coef() an embedder function shadows stay listed under
// unresolved_scalars, never refused.
func TestScalarFold_UnloadedReadsStayUnresolved(t *testing.T) {
	t.Parallel()
	fits := model.CatalogFromFits([]types.FitSpec{{Name: "batch", Request: &types.Request{
		Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}}}}}})
	cases := []struct {
		name, src string
		shadow    bool
	}{
		{"a batch fit", `coef("batch", "x")`, false},
		{"a batch fit beside a loaded one", `coef("truth", "x") * coef("batch", "x")`, false},
		{"a computed batch name", `coef("bat" + "ch", "x")`, false},
		{"beside an embedder function", `coef("truth", "x") * double(1)`, false},
		{"coef shadowed", `coef("truth", "x")`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := &types.Window{Type: types.WIN_EWMA, Field: "x", Label: "w", OrderBy: []types.OrderKey{{Field: "t"}},
				Frame: &types.FrameSpec{Mode: "rows"}, Params: json.RawMessage(`{"alpha": ` + strconv.Quote(tc.src) + `}`)}
			req := foldRequest()
			req.Windows = []*types.Window{w}
			opts := &PredictOptions{ModelLoader: foldFiles{}}
			cat, _, cerr := predictModels(model.RequestRefs(req), fits, opts)
			if cerr != nil {
				t.Fatal(cerr)
			}
			opts = opts.withModels(cat)
			env := windowScalarEnv(req, opts)
			env.Extensions = append(env.Extensions, "double")
			if tc.shadow {
				env.Extensions = append(env.Extensions, "coef")
			}
			res, srefs := resolveWindowScalarsStatic(w, env)
			if len(srefs) > 0 {
				t.Fatalf("refused: %v", srefs[0].Coded())
			}
			if len(res.Unresolved) != 1 || !res.Deferred["alpha"] {
				t.Errorf("unresolved %+v deferred %v, want alpha left to the runtime", res.Unresolved, res.Deferred)
			}
		})
	}
}

// foldOptimize is optimizeBatch whose block loads foldTruth (as "truth",
// the batch's params binding b) and reads it through bound — the
// variable's upper bound — and start.
func foldOptimize(upper, start string) *types.ComposedRequest {
	c := optimizeBatch(func(s *types.OptimizeSpec) {
		s.Models = []types.ModelRef{{Name: "truth", Inline: json.RawMessage(foldTruth)}}
		s.Variables[0].Upper = types.BoundExpr(upper)
		if start != "" {
			s.Variables[0].Start = types.BoundExpr(start)
		}
	})
	c.Params["b"] = json.Number("0.25")
	return c
}

// foldOptimizeRuntime is the runtime's Compile of c's block: over the
// Set Service.runComposeOptimize binds (the block's loaded files) and
// the engine's bound readers.
func foldOptimizeRuntime(t *testing.T, c *types.ComposedRequest, loaded []*model.Loaded) *errors.CodedError {
	t.Helper()
	spec := &c.Optimize[0]
	set, err := (*model.Set)(nil).WithLoaded(model.Refs{Models: spec.Models}, loaded[0])
	if err != nil {
		t.Fatal(err)
	}
	params, _ := exprenv.NormalizeParams(c.Params)
	fits := model.CatalogFromFits(c.Fits)
	env := optblock.Env{Params: params, Fits: fits, Outer: model.WithinCatalog(nil, fits), Loaded: loaded,
		Results: ResultCatalog(c.Requests, vectors.AggregationKinds{})}
	_, cerr := optblock.Compile(spec, 0, env, (*processing.ExtensionRegistry)(nil).ExprBoundWithModels(set))
	return cerr
}

// TestScalarFold_OptimizeBoundsMatchRuntime: predict's OptimizeCheck
// folds a bound or start reading the block's own loaded model — nothing
// left unresolved, never the placeholder's PROCESSING_INTERNAL — and
// refuses a folded value out of range (or a reader refusal) with the
// runtime Compile's very code, message and details. A read of a batch
// fit stays unresolved.
func TestScalarFold_OptimizeBoundsMatchRuntime(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, upper, start string
		refused            bool
		unresolved         int
	}{
		{"coef upper", `coef("truth", "x") * 200`, "", false, 0},
		{"coef upper and start", `coef("truth", "x") * 200`, `coef("truth", "(intercept)") * 20`, false, 0},
		{"folded bounds crossed", `coef("truth", "x") * 2`, "", true, 0},
		{"folded start out of bounds", `coef("truth", "x") * 200`, `coef("truth", "x") * 400`, true, 0},
		{"intervals unavailable", `coef("truth", "x", "up" + "per")`, "", true, 0},
		{"a batch fit", `coef("demand", "price") * 200`, "", false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := foldOptimize(tc.upper, tc.start)
			checked, pcerr := OptimizeCheck(c, nil, nil, nil, nil, nil, vectors.AggregationKinds{})
			if pcerr != nil && pcerr.Code == errors.PROCESSING_INTERNAL {
				t.Fatalf("a foldable read ran a placeholder: %v", pcerr)
			}
			if tc.refused != (pcerr != nil) {
				t.Fatalf("predict refusal %v, want refused=%v", pcerr, tc.refused)
			}
			// The runtime's own pre-fit check never folds: it passes the
			// block and hands its files to the Compile that solves.
			loaded, rcerr := OptimizeRefusal(c, nil, nil, nil, nil, nil, vectors.AggregationKinds{})
			if rcerr != nil {
				t.Fatalf("the runtime's pre-fit check refused %v", rcerr)
			}
			if tc.unresolved > 0 {
				if len(checked.Unresolved) != tc.unresolved {
					t.Errorf("unresolved %+v, want %d", checked.Unresolved, tc.unresolved)
				}
				return
			}
			rerr := foldOptimizeRuntime(t, c, loaded)
			if pcerr != nil {
				sameCoded(t, pcerr, rerr)
				return
			}
			if rerr != nil {
				t.Fatalf("predict accepted what the runtime refused: %v", rerr)
			}
			if len(checked.Unresolved) != 0 {
				t.Errorf("a loaded-model read left unresolved: %+v", checked.Unresolved)
			}
		})
	}
}
