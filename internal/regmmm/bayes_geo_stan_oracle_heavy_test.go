//go:build heavy

package regmmm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// bayes_geo_stan_oracle_heavy_test.go holds REG_MMM_BAYES's
// geo-hierarchical posterior (params.geo, U70 E2-S3) to Stan:
// scripts/reference/gen_mmm_bayes_geo_stan.R samples the same
// non-centred model and default priors, written independently in
// scripts/reference/mmm_bayes_geo.stan, with CmdStan 2.40.0 on a
// three-geo × two-channel panel it simulates and stores — curves shared
// across the geos, and pooled per geo (params.per_geo_curves). This test
// fits those very rows through REG_MMM_BAYES with each case's stored
// params and seed and compares the posterior means of every coefficient
// (each geo's intercept and β, the population β, the control, the
// population and per-geo decay, the curve — shared, or population and
// per geo — σ and every pooling scale τ), each channel's population ROI
// μ (outputs.hierarchy) and its all-geo and per-geo ROI with Stan's.
//
// Tolerance: |Pulse − Stan| ≤ geoStanZ combined Monte Carlo standard
// errors, √(MCSE_Pulse² + MCSE_Stan²), MCSE_Pulse = sd / √(bulk ESS)
// from the fit's own diagnostics (for the population ROI μ, which the
// diagnostics do not list, the fit's smallest bulk ESS over every
// coefficient and ROI — a larger, so looser, MCSE) and MCSE_Stan
// posterior's mcse_mean. Both runs are seeded, so the verdict is fixed;
// 4.5 keeps an honest pair of samplers inside with probability ≥ 0.999
// across the ~90 comparisons of the two cases.
const geoStanZ = 4.5

// geoStanCase is one mmm_bayes_geo_stan.json case.
type geoStanCase struct {
	Name         string          `json:"name"`
	PerGeoCurves bool            `json:"per_geo_curves"`
	RNGSeed      int64           `json:"rng_seed"`
	Params       json.RawMessage `json:"params"`
	Rows         struct {
		Geo    []float64 `json:"geo"`
		Week   []float64 `json:"week"`
		TV     []float64 `json:"tv"`
		Search []float64 `json:"search"`
		Price  []float64 `json:"price"`
		Sales  []float64 `json:"sales"`
	} `json:"rows"`
	Coefficients map[string]stanFigure `json:"coefficients"`
	ROI          map[string]stanFigure `json:"roi"`
	Mu           map[string]stanFigure `json:"mu"`
}

// loadGeoStanOracle reads the committed golden, checking its hash
// footer and its recorded toolchain.
func loadGeoStanOracle(t *testing.T) []geoStanCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mmm_bayes_geo_stan.json"))
	if err != nil {
		t.Fatalf("read mmm_bayes_geo_stan.json: %v (regenerate with `make reference-stan-geo`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("mmm_bayes_geo_stan.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("mmm_bayes_geo_stan.json hash mismatch (hand-edited? regenerate with `make reference-stan-geo`)")
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []geoStanCase     `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Packages["cmdstan"] != "2.40.0" || doc.Packages["cmdstanr"] != "0.9.0" {
		t.Fatalf("golden toolchain %v, want CmdStan 2.40.0 / cmdstanr 0.9.0", doc.Packages)
	}
	return doc.Cases
}

// TestMMMBayes_GeoPosteriorMatchesStan: on the golden's geo panels —
// curves shared, and per_geo_curves — REG_MMM_BAYES's posterior means of
// every coefficient, the population ROI μ and every all-geo and per-geo
// ROI match Stan's within geoStanZ combined Monte Carlo standard errors.
func TestMMMBayes_GeoPosteriorMatchesStan(t *testing.T) {
	t.Parallel()

	cases := loadGeoStanOracle(t)
	var variants []bool
	for _, c := range cases {
		variants = append(variants, c.PerGeoCurves)
	}
	if !slices.Contains(variants, false) || !slices.Contains(variants, true) {
		t.Errorf("the golden's per_geo_curves variants %v, want both", variants)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			geoStanCaseCheck(t, c)
		})
	}
}

func geoStanCaseCheck(t *testing.T, c geoStanCase) {
	rows := make(probeRows, len(c.Rows.Week))
	for i := range rows {
		rows[i] = probeRow{"geo": c.Rows.Geo[i], "week": c.Rows.Week[i], "tv": c.Rows.TV[i], "search": c.Rows.Search[i],
			"price": c.Rows.Price[i], "sales": c.Rows.Sales[i]}
	}
	spec := &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "search", "price"},
		Params: c.Params, RNGSeed: c.RNGSeed}
	reg, err := BayesFactory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reg.(extend.ContextRegression).FitContext(context.Background(), rows)
	if err != nil {
		t.Fatal(err)
	}
	est := map[string]float64{}
	for _, co := range out.Coefficients {
		est[co.Name] = co.Estimate
	}
	diag := out.Outputs["diagnostics"].(map[string]any)
	params := out.Outputs["params"].(map[string]any)
	pconv := diag["parameters"].(map[string]any)
	rconv := diag["roi"].(map[string]any)
	roi := out.Outputs["roi"].(map[string]any)
	pooled := out.Outputs["hierarchy"].(map[string]any)["pooled"].(map[string]any)
	sd := func(summary any) float64 { return summary.(map[string]float64)["sd"] }
	ess := func(conv any) float64 { return conv.(map[string]float64)["ess_bulk"] }
	minESS := diag["min_ess_bulk"].(float64)
	t.Logf("%s: %v divergences, max rhat %v, min ess bulk %.0f", c.Name, diag["divergences"], diag["max_rhat"], minESS)

	worst := 0.0
	check := func(name string, got, gotMCSE float64, want stanFigure) {
		se := math.Sqrt(float64(gotMCSE*gotMCSE) + float64(want.MCSE*want.MCSE))
		z := math.Abs(got-want.Mean) / se
		worst = max(worst, z)
		t.Logf("%-16s Pulse %.6g, Stan %.6g (sd %.3g), z %.2f", name, got, want.Mean, want.SD, z)
		if !(z <= geoStanZ) {
			t.Errorf("%s: REG_MMM_BAYES %.8g, Stan %.8g: %.2f combined MCSEs apart (> %v)", name, got, want.Mean, z, geoStanZ)
		}
	}
	if len(c.Coefficients) != len(est) {
		t.Errorf("Stan names %d coefficients, Pulse %d", len(c.Coefficients), len(est))
	}
	for _, name := range sortedKeys(c.Coefficients) {
		got, ok := est[name]
		if !ok {
			t.Errorf("Pulse reports no coefficient %q", name)
			continue
		}
		check(name, got, sd(params[name])/math.Sqrt(ess(pconv[name])), c.Coefficients[name])
	}
	for _, name := range sortedKeys(c.ROI) {
		s, ok := roi[name]
		if !ok {
			t.Errorf("Pulse reports no ROI %q", name)
			continue
		}
		check("roi:"+name, s.(map[string]float64)["mean"], sd(s)/math.Sqrt(ess(rconv[name])), c.ROI[name])
	}
	if len(c.Mu) != 2 {
		t.Errorf("the case pins %d population ROIs, want 2", len(c.Mu))
	}
	for _, name := range sortedKeys(c.Mu) {
		mu := pooled[name].(map[string]any)["mu"]
		check("mu:"+name, mu.(map[string]float64)["mean"], sd(mu)/math.Sqrt(minESS), c.Mu[name])
	}
	t.Logf("%s: worst |z| %.2f (bound %v)", c.Name, worst, geoStanZ)
}

// sortedKeys is m's keys in order.
func sortedKeys(m map[string]stanFigure) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
