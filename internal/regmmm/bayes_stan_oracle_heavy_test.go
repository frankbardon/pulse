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

// bayes_stan_oracle_heavy_test.go holds REG_MMM_BAYES's posterior to
// Stan (U70 E1-S2, E1-S3, E1-S4): scripts/reference/gen_mmm_bayes_stan.R
// samples the same model and default priors, written independently in
// scripts/reference/mmm_bayes.stan, with CmdStan 2.40.0 on rows it
// simulates and stores, in every cell of the adstock kernel ×
// saturation curve matrix (4 × 3), and with a lift test in the
// likelihood (params.lift_tests) on two cells. This test fits
// those very rows through REG_MMM_BAYES with each case's stored params
// and seed and compares every coefficient's posterior mean, and each
// channel's ROI, with Stan's.
//
// Tolerance: |Pulse − Stan| ≤ stanZ combined Monte Carlo standard
// errors, √(MCSE_Pulse² + MCSE_Stan²), with MCSE_Pulse = sd / √(bulk
// ESS) from the fit's own diagnostics and MCSE_Stan posterior's
// mcse_mean. Both runs are seeded, so the verdict is fixed; stanZ = 4.5
// keeps an honest pair of samplers inside with probability ≥ 0.999
// across the ~105 comparisons of the fourteen cases. The tolerance is
// stated per case (stanCellZ); every case holds the same 4.5.
const stanZ = 4.5

// stanCellZ is each case's tolerance in combined MCSEs: one per kernel
// × curve cell, and one per lift-test case.
var stanCellZ = map[string]float64{
	"geometric_hill": stanZ, "delayed_hill": stanZ, "weibull_cdf_hill": stanZ, "weibull_pdf_hill": stanZ,
	"geometric_logistic": stanZ, "delayed_logistic": stanZ, "weibull_cdf_logistic": stanZ, "weibull_pdf_logistic": stanZ,
	"geometric_michaelis_menten": stanZ, "delayed_michaelis_menten": stanZ, "weibull_cdf_michaelis_menten": stanZ,
	"weibull_pdf_michaelis_menten": stanZ,
	"geometric_hill_lift":          stanZ, "delayed_logistic_lift": stanZ,
}

// stanFigure is one Stan posterior summary.
type stanFigure struct {
	Mean float64 `json:"mean"`
	SD   float64 `json:"sd"`
	MCSE float64 `json:"mcse"`
}

// stanCase is one mmm_bayes_stan.json case.
type stanCase struct {
	Name    string          `json:"name"`
	Kernel  string          `json:"kernel"`
	Curve   string          `json:"curve"`
	RNGSeed int64           `json:"rng_seed"`
	Params  json.RawMessage `json:"params"`
	Rows    struct {
		Week  []float64 `json:"week"`
		TV    []float64 `json:"tv"`
		Price []float64 `json:"price"`
		Sales []float64 `json:"sales"`
	} `json:"rows"`
	Coefficients map[string]stanFigure `json:"coefficients"`
	ROI          stanFigure            `json:"roi"`
}

// loadStanOracle reads the committed golden, checking its hash footer
// and its recorded toolchain.
func loadStanOracle(t *testing.T) []stanCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "mmm_bayes_stan.json"))
	if err != nil {
		t.Fatalf("read mmm_bayes_stan.json: %v (regenerate with `make reference-stan`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("mmm_bayes_stan.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("mmm_bayes_stan.json hash mismatch (hand-edited? regenerate with `make reference-stan`)")
	}
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []stanCase        `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Packages["cmdstan"] != "2.40.0" || doc.Packages["cmdstanr"] != "0.9.0" {
		t.Fatalf("golden toolchain %v, want CmdStan 2.40.0 / cmdstanr 0.9.0", doc.Packages)
	}
	return doc.Cases
}

// TestMMMBayes_PosteriorMatchesStan: in every kernel × curve cell of
// the golden, and in each lift-test case, REG_MMM_BAYES's posterior
// means match Stan's within the case's stanCellZ combined Monte Carlo
// standard errors.
func TestMMMBayes_PosteriorMatchesStan(t *testing.T) {
	t.Parallel()

	cases := loadStanOracle(t)
	var cells []string
	lifts := 0
	for _, c := range cases {
		cells = append(cells, c.Kernel+"_"+c.Curve)
		if strings.Contains(string(c.Params), `"lift_tests"`) {
			lifts++
		}
	}
	if lifts < 2 {
		t.Errorf("the golden has %d lift-test cases, want 2", lifts)
	}
	for _, k := range kernelNames() {
		for _, cv := range curveNames() {
			if !slices.Contains(cells, k+"_"+cv) {
				t.Errorf("the golden has no %s × %s case", k, cv)
			}
		}
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			z, ok := stanCellZ[c.Name]
			if !ok {
				t.Fatalf("no stated tolerance for case %s", c.Name)
			}
			stanCaseCheck(t, c, z)
		})
	}
}

func stanCaseCheck(t *testing.T, c stanCase, bound float64) {
	rows := make(probeRows, len(c.Rows.Week))
	for i := range rows {
		rows[i] = probeRow{"week": c.Rows.Week[i], "tv": c.Rows.TV[i], "price": c.Rows.Price[i], "sales": c.Rows.Sales[i]}
	}
	spec := &types.RegressionSpec{Type: BayesName, Name: "bayes", Target: "sales", Predictors: []string{"tv", "price"},
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
	mcse := func(summary, conv any) float64 {
		return summary.(map[string]float64)["sd"] / math.Sqrt(conv.(map[string]float64)["ess_bulk"])
	}
	worst := 0.0
	check := func(name string, got, gotMCSE float64, want stanFigure) {
		se := math.Sqrt(float64(gotMCSE*gotMCSE) + float64(want.MCSE*want.MCSE))
		z := math.Abs(got-want.Mean) / se
		worst = max(worst, z)
		t.Logf("%-12s Pulse %.6g, Stan %.6g (sd %.3g), z %.2f", name, got, want.Mean, want.SD, z)
		if !(z <= bound) {
			t.Errorf("%s: REG_MMM_BAYES %.8g, Stan %.8g: %.2f combined MCSEs apart (> %v)", name, got, want.Mean, z, bound)
		}
	}
	if len(c.Coefficients) != len(est) {
		t.Errorf("Stan names %d coefficients, Pulse %d", len(c.Coefficients), len(est))
	}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range c.Coefficients {
			if !yield(k) {
				return
			}
		}
	}) {
		got, ok := est[name]
		if !ok {
			t.Errorf("Pulse reports no coefficient %q", name)
			continue
		}
		check(name, got, mcse(params[name], pconv[name]), c.Coefficients[name])
	}
	roi := out.Outputs["roi"].(map[string]any)["tv"]
	check("roi:tv", roi.(map[string]float64)["mean"], mcse(roi, diag["roi"].(map[string]any)["tv"]), c.ROI)
	t.Logf("%s: worst |z| %.2f (bound %v)", c.Name, worst, bound)
}
