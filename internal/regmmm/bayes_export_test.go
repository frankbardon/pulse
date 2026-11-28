package regmmm

import (
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/mcmc"
	"github.com/frankbardon/pulse/types"
)

// bayes_export_test.go is REG_MMM_BAYES's shared test fixture and the
// internals its end-to-end tests (package regmmm_test, which imports the
// root package and so cannot live in this package) read: the small
// one-market cohort, the fast slot, the sampler hook and the model's
// constants.

// The small cohort: one market, BayesWeeks weeks, one channel (tv) with
// geometric adstock and a Hill curve, a price control, Gaussian noise.
// Every product feeding an add is barriered and the normal draws are
// detmath's, so the cohort's bits are the seed's alone on every
// architecture.
const (
	BayesWeeks  = 60
	BayesCohort = "bayes.pulse"
	SmallDecay  = 0.5
	smallK      = 120.0
	smallS      = 1.5
	smallBeta   = 40.0
)

// BayesRow is one week of the small cohort.
type BayesRow struct {
	Geo       string
	Week      float64
	TV, Price float64
	Sales     float64
}

// BayesRows generates one market's series.
func BayesRows(seed uint64) []BayesRow {
	rng := rand.New(rand.NewPCG(seed, 0x6261796573))
	spend, price := make([]float64, BayesWeeks), make([]float64, BayesWeeks)
	for w := range spend {
		if rng.Float64() < 0.6 {
			spend[w] = float64(rng.Float64()*150) + 20
		}
		price[w] = 2 * (float64(rng.Float64()) - 0.5)
	}
	rows := make([]BayesRow, BayesWeeks)
	for w := range spend {
		ad := 0.0
		for l := 0; l <= defaultMaxLag && l <= w; l++ {
			ad += float64(detmath.Pow(SmallDecay, float64(l)) * spend[w-l])
		}
		y := 200 + float64(smallBeta*mmmfixture.JointHill(ad, smallK, smallS)) + float64(-5*price[w]) + float64(1*detmath.NormFloat64(rng))
		rows[w] = BayesRow{Geo: "east", Week: float64(w + 1), TV: spend[w], Price: price[w], Sales: y}
	}
	return rows
}

// FastParams is a fit small enough for a unit test.
const FastParams = `{"channels":["tv"],"chains":2,"warmup":150,"draws":100}`

// BayesSpec is a REG_MMM_BAYES slot over the small cohort, ordered by
// BayesOrder(params).
func BayesSpec(params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "price"},
		Order: BayesOrder(params), Params: json.RawMessage(params)}
}

// oracleParams splits an R-written oracle case's params into the slot's
// params and order: the R generators (scripts/reference/gen_mmm_bayes*_stan.R)
// still write the time field as params.time, which REG_MMM_BAYES no
// longer reads (it is order.field, partition params.geo on a panel).
// The lift keeps the derived seed, and so the oracle comparison, as it
// was (orderAsSeedTime).
func oracleParams(t testing.TB, raw json.RawMessage) (json.RawMessage, *types.RegressionOrder) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	var field, geo string
	if v, ok := obj["time"]; ok {
		if err := json.Unmarshal(v, &field); err != nil {
			t.Fatal(err)
		}
		delete(obj, "time")
	}
	if v, ok := obj["geo"]; ok {
		_ = json.Unmarshal(v, &geo)
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return out, &types.RegressionOrder{Field: field, Partition: geo}
}

// BayesOrder is the test cohorts' time order: order.field "week" and, on
// a geo panel, partition params.geo (the model's panel rule).
func BayesOrder(params string) *types.RegressionOrder {
	var p struct {
		Geo string `json:"geo"`
	}
	_ = json.Unmarshal([]byte(params), &p)
	if p.Geo == "week" {
		// A geo naming the time field is the factory's geo_conflict
		// refusal; a partition naming it too would be the engine's.
		p.Geo = ""
	}
	return &types.RegressionOrder{Field: "week", Partition: p.Geo}
}

// SetBayesSampler swaps the sampler a fit runs for f for the test's
// duration.
func SetBayesSampler(t testing.TB, f func(targetAccept float64) mcmc.Sampler) {
	t.Helper()
	prev := bayesSampler
	bayesSampler = func(s samplerSettings) mcmc.Sampler { return f(s.targetAccept) }
	t.Cleanup(func() { bayesSampler = prev })
}

// SamplerSettings is the sampler a fit asked for (samplerSettings).
type SamplerSettings struct {
	HMC                                bool
	TargetAccept, PathLength, StepSize float64
}

// WrapBayesSampler wraps the sampler a fit builds for the test's
// duration: f sees the settings the fit asked for and the sampler the
// stock hook built from them, and returns the one to run.
func WrapBayesSampler(t testing.TB, f func(s SamplerSettings, built mcmc.Sampler) mcmc.Sampler) {
	t.Helper()
	prev := bayesSampler
	bayesSampler = func(s samplerSettings) mcmc.Sampler {
		return f(SamplerSettings{HMC: s.hmc, TargetAccept: s.targetAccept, PathLength: s.pathLength, StepSize: s.stepSize}, prev(s))
	}
	t.Cleanup(func() { bayesSampler = prev })
}

// BayesEffectiveSeed is the seed a slot samples with (bayesEffectiveSeed).
var BayesEffectiveSeed = bayesEffectiveSeed

// The model's constants the end-to-end tests read.
const (
	DefaultMaxLag            = defaultMaxLag
	BayesRHatMax             = bayesRHatMax
	BayesESSMin              = bayesESSMin
	ReasonRHatHigh           = reasonRHatHigh
	ReasonESSLow             = reasonESSLow
	ReasonDivergences        = reasonDivergences
	ReasonTreeDepthSaturated = reasonTreeDepthSaturated
	InterceptName            = interceptName
)

// GeometricWeights and Adstock are the model's adstock (geometricWeights,
// adstock).
var (
	GeometricWeights = geometricWeights
	Adstock          = adstock
)

// KernelWeights is the named adstock kernel's weights at params p
// (adstockKernel.Weights, no gradient).
func KernelWeights(kernel string, p []float64, maxLag int) []float64 {
	w, _ := kernelByName(kernel).Weights(p, maxLag, false)
	return w
}
