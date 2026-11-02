//go:build heavy

package regmmm_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/regmmm"
	"github.com/frankbardon/pulse/types"
)

// bayes_conjugate_heavy_test.go pins REG_MMM_BAYES in its conjugate
// limit to REG_BAYES_LINEAR (U64 E4-S2, #620). Hold the adstock decay
// and the Hill curve's k and s fixed and the model is linear in its
// coefficients: y_t = α + γ·price_t + β·h_t + ε_t with h_t the fixed
// curve over the adstocked spend. Make every other prior flat and its
// posterior is the textbook one, which REG_BAYES_LINEAR computes in
// closed form from a flat Normal-Inverse-Gamma prior on the design
// [1, price, h]. The MMM side samples it with NUTS through the whole
// operator (scaling, transforms, Jacobians, MAP start, back-mapping to
// original units); the comparison is within Monte Carlo error.
//
// The limit, as requested of each operator:
//   - REG_MMM_BAYES: decay, k and s each under a truncnormal whose
//     bounds are 1e-9 (relative) either side of the pinned value — a
//     point mass to the likelihood, while the sampler's unconstrained
//     coordinate stays well scaled (the bounded logistic maps it onto
//     the interval); β under HalfNormal(100), the intercept and control
//     under Normal(0, 100) and σ under HalfNormal(100), all on the
//     max-scaled outcome where the posterior SDs are ~1e-2, so each is
//     flat to 1e-8 over the posterior. β is ~40 posterior SDs above 0,
//     so its positivity truncates no mass.
//   - REG_BAYES_LINEAR: prior_precision 0 on every coefficient (flat)
//     and prior_shape = prior_rate = 1e-12.
//
// The two flat limits differ in one known factor. A flat prior on σ
// (the MMM side; σ is sampled on the log scale with its Jacobian, so the
// density is flat in σ) gives the marginal β ~ t_ν(β̂, SSR/ν·(XᵀX)⁻¹) with
// ν = n − P − 1 (P = 3 coefficients), while the NIG limit gives
// t_{2a_n}(β̂, b_n/a_n·(XᵀX)⁻¹) with 2a_n = n and b_n = SSR/2. The
// posterior means are both β̂; the MMM posterior SD is REG_BAYES_LINEAR's
// StdErr (the t scale) times √(DF / (ν − 2)), DF = 2a_n. σ's posterior
// is σ² ~ InvGamma(ν/2, SSR/2), so E[σ] = √(SSR/2)·Γ((ν−1)/2)/Γ(ν/2).
// The small cohort's n = 60 makes the factor 1.054, so a wrong df
// fails the SD check by far more than its Monte Carlo error.
//
// Tolerance: |MMM − reference| ≤ mcZ Monte Carlo standard errors, with
// MCSE = SD/√ESS for a mean and SD·√((κ−1)/(4·ESS)) for an SD (κ the t_ν
// kurtosis), ESS the smaller of the run's bulk and tail ESS. The run is
// seeded (rng_seed), so the figures are the same on every run and
// architecture; mcZ = 4 is a bound the seeded run either meets or not,
// set so an honest sampler passes for any seed with probability ≥ 0.999
// across the eight checks. The worst z of the committed seed is in the
// U64 E4-S2 record.
const mcZ = 4.0

// conjugateCohort is the small cohort plus h, the pinned curve over the
// adstocked spend, computed here independently of the model (math, not
// detmath; the bits need not match, only the figures).
func conjugateCohort(t *testing.T, p *pulse.Pulse, rows []regmmm.BayesRow, kScaled, s float64) {
	t.Helper()
	fields := []encoding.Field{
		{Name: "week", Type: encoding.FieldTypeU16},
		{Name: "tv", Type: encoding.FieldTypeF64},
		{Name: "price", Type: encoding.FieldTypeF64},
		{Name: "h", Type: encoding.FieldTypeF64},
		{Name: "sales", Type: encoding.FieldTypeF64},
	}
	sch := encoding.Schema{Fields: fields}
	off := 0
	for i := range sch.Fields {
		sch.Fields[i].CsvColumnIdx = i
		sch.Fields[i].ByteOffset = off
		sch.Fields[i].Description = "Conjugate-limit test column " + sch.Fields[i].Name + "."
		off += sch.FieldWidth(i)
	}
	b, err := p.NewCohortBuilder(context.Background(), "conjugate.pulse", sch, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	peak := 0.0
	for _, r := range rows {
		peak = math.Max(peak, r.TV)
	}
	for w, r := range rows {
		ad := 0.0
		for l := 0; l <= regmmm.DefaultMaxLag && l <= w; l++ {
			ad += math.Pow(regmmm.SmallDecay, float64(l)) * rows[w-l].TV
		}
		h := 0.0
		if ad > 0 {
			h = 1 / (1 + math.Pow(kScaled/(ad/peak), s))
		}
		if err := b.Append(pulse.CohortRow{uint64(r.Week), r.TV, r.Price, h, r.Sales}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// pinned is a truncnormal prior holding a parameter within 1e-9
// (relative) of v.
func pinned(v float64) map[string]any {
	return map[string]any{"family": "truncnormal", "mu": v, "sigma": 1, "lower": v * (1 - 1e-9), "upper": v * (1 + 1e-9)}
}

// TestMMMBayes_ConjugateLimitMatchesBayesLinear: REG_MMM_BAYES with the
// adstock and curve pinned and flat priors reproduces REG_BAYES_LINEAR's
// posterior (means, SDs and σ) within Monte Carlo error.
func TestMMMBayes_ConjugateLimitMatchesBayesLinear(t *testing.T) {
	t.Parallel()

	rows := regmmm.BayesRows(11)
	peak := 0.0
	for _, r := range rows {
		peak = math.Max(peak, r.TV)
	}
	const kRaw, s = 120.0, 1.5 // the cohort's own curve
	kScaled := kRaw / peak
	p := instance(t, afero.NewMemMapFs(), nil)
	conjugateCohort(t, p, rows, kScaled, s)

	params, _ := json.Marshal(map[string]any{
		"channels": []string{"tv"}, "time": "week",
		"priors": map[string]any{"tv": map[string]any{
			"beta":  map[string]any{"family": "halfnormal", "sigma": 100},
			"decay": pinned(regmmm.SmallDecay), "k": pinned(kScaled), "s": pinned(s),
		}},
		"intercept_prior": map[string]any{"family": "normal", "mu": 0, "sigma": 100},
		"control_prior":   map[string]any{"family": "normal", "mu": 0, "sigma": 100},
		"sigma_prior":     map[string]any{"family": "halfnormal", "sigma": 100},
		"chains":          4, "warmup": 1000, "draws": 2500,
	})
	mmm := &types.RegressionSpec{Type: types.REG_MMM_BAYES, Name: "mmm", Target: "sales",
		Predictors: []string{"tv", "price"}, Params: params, RNGSeed: 20261102}
	conj := &types.RegressionSpec{Type: types.REG_BAYES_LINEAR, Name: "conj", Target: "sales",
		Predictors: []string{"h", "price"}, PriorPrecision: types.PrecisionVector([]float64{0, 0, 0}),
		PriorShape: 1e-12, PriorRate: 1e-12}
	resp := process(t, p, &types.Request{Cohort: &types.Cohort{Filename: "conjugate.pulse"}, Regressions: []*types.RegressionSpec{mmm, conj}})
	if len(resp.Regressions) != 2 || resp.Regressions[0].Error != nil || resp.Regressions[1].Error != nil {
		t.Fatalf("regressions = %s", mustJSON(t, resp.Regressions))
	}
	m, b := resp.Regressions[0], resp.Regressions[1]
	for _, w := range resp.Warnings {
		t.Logf("warning: %s %s", w.Code, w.Message)
	}

	n := float64(b.NObs)
	nu := n - 3 - 1
	df := b.DF
	sdFactor := math.Sqrt(df / (nu - 2))
	kurt := 3 + 6/(nu-4)
	diag := m.Outputs["diagnostics"].(map[string]any)["parameters"].(map[string]any)
	ess := func(name string) float64 {
		d := diag[name].(map[string]float64)
		return math.Min(d["ess_bulk"], d["ess_tail"])
	}

	worst := 0.0
	check := func(what string, got, want, mcse float64) {
		z := math.Abs(got-want) / mcse
		worst = math.Max(worst, z)
		t.Logf("%-22s MMM %.8g, reference %.8g, z %.2f", what, got, want, z)
		if !(z <= mcZ) {
			t.Errorf("%s: REG_MMM_BAYES %.10g, REG_BAYES_LINEAR limit %.10g: %.2f Monte Carlo SEs apart (> %v)", what, got, want, z, mcZ)
		}
	}
	for mName, bName := range map[string]string{"(intercept)": "(intercept)", "tv": "h", "price": "price"} {
		sd := float64(b.StdErrors[bName] * sdFactor)
		e := ess(mName)
		check("mean "+mName, m.Coefficients[mName], b.Coefficients[bName], sd/math.Sqrt(e))
		check("sd "+mName, m.StdErrors[mName], sd, sd*math.Sqrt((kurt-1)/(4*e)))
	}
	// σ: σ² ~ InvGamma(ν/2, SSR/2) with SSR = 2(b_n − b0) = RSE²·DF − 2b0.
	ssr := b.ResidualStdErr*b.ResidualStdErr*df - 2e-12
	lg1, _ := math.Lgamma((nu - 1) / 2)
	lg2, _ := math.Lgamma(nu / 2)
	eSigma := math.Sqrt(ssr/2) * math.Exp(lg1-lg2)
	sdSigma := math.Sqrt(ssr/(nu-2) - eSigma*eSigma)
	e := ess("sigma")
	check("mean sigma", m.Coefficients["sigma"], eSigma, sdSigma/math.Sqrt(e))
	// σ's kurtosis is near 3 at this ν; the normal-theory SD error is
	// within a few percent of it.
	check("sd sigma", m.StdErrors["sigma"], sdSigma, sdSigma*math.Sqrt(1/(2*e)))
	// The pinned parameters stayed pinned.
	for name, v := range map[string]float64{"decay:tv": regmmm.SmallDecay, "k:tv": kRaw, "s:tv": s} {
		if got := m.Coefficients[name]; math.Abs(got-v) > 2e-9*v {
			t.Errorf("%s = %.12g, pinned at %.12g", name, got, v)
		}
	}
	t.Logf("worst |z| = %.2f (bound %v), SD factor √(DF/(ν−2)) = %.4f", worst, mcZ, sdFactor)
}
