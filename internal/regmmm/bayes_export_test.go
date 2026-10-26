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
const FastParams = `{"channels":["tv"],"time":"week","chains":2,"warmup":150,"draws":100}`

// BayesSpec is a REG_MMM_BAYES slot over the small cohort.
func BayesSpec(params string) *types.RegressionSpec {
	return &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "price"},
		Params: json.RawMessage(params)}
}

// SetBayesSampler swaps the sampler a fit runs for f for the test's
// duration.
func SetBayesSampler(t testing.TB, f func(targetAccept float64) mcmc.Sampler) {
	t.Helper()
	prev := bayesSampler
	bayesSampler = f
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
