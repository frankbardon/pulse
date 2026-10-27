package regmmm

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_test.go holds REG_MMM_BAYES's fit internals to their contract:
// the posterior moments, Fit ≡ FitContext, the factory's
// coefficient-name collision, a short input that never samples and the
// sampler-error mapping. The registration, the refusals at predict and
// at runtime, the seed and the posterior through the engine are
// bayes_engine_test.go's.

// probeRow is an extend.Record over a name → value map (absent = null).
type probeRow map[string]float64

func (r probeRow) Schema() *encoding.Schema                     { return nil }
func (r probeRow) IsNull(f string) bool                         { _, ok := r[f]; return !ok }
func (r probeRow) NumericValue(f string) (float64, bool)        { v, ok := r[f]; return v, ok }
func (r probeRow) Uint64Value(f string) (uint64, bool)          { v, ok := r[f]; return uint64(v), ok }
func (r probeRow) StringValue(string) (string, bool)            { return "", false }
func (r probeRow) SetMaskValue(string) (encoding.SetMask, bool) { return encoding.SetMask{}, false }
func (r probeRow) DecimalValue(string) (encoding.Decimal128, bool) {
	return encoding.Decimal128{}, false
}
func (r probeRow) VectorValue(string) ([]float64, bool) { return nil, false }
func (r probeRow) Weight() (float64, bool)              { return 0, false }

type probeRows []probeRow

func (r probeRows) Len() int               { return len(r) }
func (r probeRows) At(i int) extend.Record { return r[i] }

// TestMMMBayes_PosteriorMoments: the coefficient table's mean, SD and
// covariance are the pooled draws' (chain order, divisor N − 1).
func TestMMMBayes_PosteriorMoments(t *testing.T) {
	draws := [][][]float64{
		{{1, 2, 3}, {2, 4, 7}},
		{{4, 5, 6}, {1, 0, 2}},
	}
	mean, cov := posteriorMoments(draws)
	if mean[0] != 3.5 || mean[1] != 16.0/6 {
		t.Fatalf("mean = %v", mean)
	}
	var v00, v01 float64
	a, b := []float64{1, 2, 3, 4, 5, 6}, []float64{2, 4, 7, 1, 0, 2}
	for i := range a {
		v00 += float64((a[i] - mean[0]) * (a[i] - mean[0]))
		v01 += float64((a[i] - mean[0]) * (b[i] - mean[1]))
	}
	if math.Abs(cov[0][0]-v00/5) > 1e-12 || math.Abs(cov[0][1]-v01/5) > 1e-12 || cov[0][1] != cov[1][0] {
		t.Errorf("cov = %v, want %v / %v", cov, v00/5, v01/5)
	}
}

// TestMMMBayes_FitMatchesFitContext: Fit is FitContext under a
// background context, byte for byte.
func TestMMMBayes_FitMatchesFitContext(t *testing.T) {
	model, err := BayesFactory(BayesSpec(FastParams), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(probeRows, 0, BayesWeeks)
	for _, r := range BayesRows(11) {
		rows = append(rows, probeRow{"week": r.Week, "tv": r.TV, "price": r.Price, "sales": r.Sales})
	}
	a, errA := model.(extend.BufferedRegression).Fit(rows)
	b, errB := model.(extend.ContextRegression).FitContext(context.Background(), rows)
	if errA != nil || errB != nil {
		t.Fatalf("Fit %v, FitContext %v", errA, errB)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Fit and FitContext differ:\n%+v\n%+v", a, b)
	}
}

// TestMMMBayes_FactoryRefusesCoefficientNameCollision: a predictor
// named like a coefficient the model adds, or listed twice, would
// collide in the table, so the factory refuses it (PROCESSING_CONFIG,
// slot predictors); one merely starting with k builds. The engine refuses an unknown field
// first, so the factory is called directly.
func TestMMMBayes_FactoryRefusesCoefficientNameCollision(t *testing.T) {
	for _, x := range []string{"(intercept)", "sigma", "decay:x", "k:x", "s:x", "tv"} {
		s := BayesSpec(`{"channels":["tv"],"time":"week"}`)
		s.Predictors = []string{"tv", x}
		var ce *perr.CodedError
		if _, err := BayesFactory(s, nil); !stderrors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != "predictors" {
			t.Errorf("predictor %q: %v, want PROCESSING_CONFIG slot predictors", x, err)
		}
	}
	if _, err := BayesFactory(&types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "kx"},
		Params: json.RawMessage(`{"channels":["tv"],"time":"week"}`)}, nil); err != nil {
		t.Errorf("a predictor merely starting with k: %v", err)
	}
}

// TestMMMBayes_ShortInputNeverSamples: a fit over 8 rows without a time
// column (the shape of an extension registration probe) is a cheap
// coded data refusal — PROCESSING_REGRESSION_INSUFFICIENT_DATA before
// any value is read — and the sampler never runs.
func TestMMMBayes_ShortInputNeverSamples(t *testing.T) {
	runs := 0
	SetBayesSampler(t, func(float64) mcmc.Sampler { runs++; return mcmc.NUTS{} })
	s := &types.RegressionSpec{Type: BayesName, Target: "probe_y", Predictors: []string{"probe_x1", "probe_x2"},
		Params: json.RawMessage(`{"channels":["probe_x1","probe_x2"],"time":"week"}`)}
	model, err := BayesFactory(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make(probeRows, 8)
	for i := range rows {
		rows[i] = probeRow{"probe_y": float64(i), "probe_x1": float64(i % 3), "probe_x2": float64(i % 2)}
	}
	_, err = model.(extend.ContextRegression).FitContext(context.Background(), rows)
	if !perr.HasCode(err, perr.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Errorf("short fit: %v, want PROCESSING_REGRESSION_INSUFFICIENT_DATA", err)
	}
	if runs != 0 {
		t.Errorf("the short fit sampled %d times", runs)
	}
}

// TestMMMBayes_SamplerErrorPassThrough: a sampler refusal that is no
// failure to run on the posterior (an options refusal) and a ctx error
// pass through samplerError unchanged.
func TestMMMBayes_SamplerErrorPassThrough(t *testing.T) {
	r := &bayesRegression{cfg: &bayesConfig{name: "bayes"}}
	other := perr.NewCodedErrorWithDetails(perr.PULSE_MCMC_INVALID, "mcmc: chains", map[string]any{"reason": "chains"})
	if got := r.samplerError(other); got != other {
		t.Errorf("an options refusal mapped to %v", got)
	}
	if got := r.samplerError(context.Canceled); got != context.Canceled {
		t.Errorf("a ctx error mapped to %v", got)
	}
}
