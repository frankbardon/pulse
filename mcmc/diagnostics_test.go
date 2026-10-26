package mcmc_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/mcmc"
)

// The ArviZ oracles (mcmc/testdata/gen_diagnostics.py; `make
// reference-mcmc`). CI never runs Python: the JSON is committed with a
// golden-hash footer, chains included, so both sides read the same draws.
//
// Tolerance: 1e-9 relative. The measured disagreement is ≤ 5e-15
// (summation order: NumPy sums pairwise and through an FFT, mcmc
// serially and directly; SciPy's ndtri vs detmath.Erfinv), so 1e-9 is
// far above the evidence and far below any difference a wrong rank,
// split, fold, quantile or Geyer truncation makes.
const oracleRel = 1e-9

type diagCase struct {
	Name    string      `json:"name"`
	Chains  [][]float64 `json:"chains"`
	RHat    *float64    `json:"rhat"`
	ESSBulk *float64    `json:"ess_bulk"`
	ESSTail *float64    `json:"ess_tail"`
}

func splitGoldenHash(content []byte) (body []byte, hash string) {
	const marker = "\n// golden-hash: "
	s := string(content)
	i := strings.LastIndex(s, marker)
	if i < 0 {
		return content, ""
	}
	return []byte(s[:i]), strings.TrimSpace(s[i+len(marker):])
}

func loadDiagnostics(t *testing.T) []diagCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "diagnostics.json"))
	if err != nil {
		t.Fatalf("read diagnostics.json: %v (regenerate with `make reference-mcmc`)", err)
	}
	body, _ := splitGoldenHash(raw)
	var doc struct {
		Packages map[string]string `json:"packages"`
		Cases    []diagCase        `json:"cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode diagnostics.json: %v", err)
	}
	if doc.Packages["arviz-stats"] == "" || len(doc.Cases) == 0 {
		t.Fatal("diagnostics.json records no arviz-stats version or no cases")
	}
	return doc.Cases
}

// near compares got with an ArviZ reference; a nil reference is ArviZ's
// NaN (undefined) and requires NaN.
func near(t *testing.T, what string, got float64, want *float64) {
	t.Helper()
	if want == nil {
		if !math.IsNaN(got) {
			t.Errorf("%s = %.17g, ArviZ NaN", what, got)
		}
		return
	}
	scale := math.Max(math.Abs(got), math.Abs(*want))
	if math.IsNaN(got) || math.Abs(got-*want) > oracleRel*scale {
		t.Errorf("%s = %.17g, ArviZ %.17g (rel diff %.3g > %g)", what, got, *want, math.Abs(got-*want)/scale, oracleRel)
	}
}

// TestMCMC_OracleGoldensHashed: every committed oracle JSON carries a
// golden-hash footer matching its body, so a hand edit fails.
func TestMCMC_OracleGoldensHashed(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("want the diagnostics oracle under testdata")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		body, stored := splitGoldenHash(raw)
		if stored == "" {
			t.Errorf("%s: no golden-hash footer (regenerate with `make reference-mcmc`)", f)
			continue
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != stored {
			t.Errorf("%s: hash mismatch (file may have been hand-edited; regenerate with `make reference-mcmc`)", f)
		}
	}
}

// TestMCMC_RHatMatchesArviZ holds RHat to arviz_stats rhat(method="rank").
func TestMCMC_RHatMatchesArviZ(t *testing.T) {
	for _, c := range loadDiagnostics(t) {
		near(t, c.Name+" RHat", mcmc.RHat(c.Chains), c.RHat)
	}
}

// TestMCMC_ESSBulkMatchesArviZ holds ESSBulk to arviz_stats
// ess(method="bulk").
func TestMCMC_ESSBulkMatchesArviZ(t *testing.T) {
	for _, c := range loadDiagnostics(t) {
		near(t, c.Name+" ESSBulk", mcmc.ESSBulk(c.Chains), c.ESSBulk)
	}
}

// TestMCMC_ESSTailMatchesArviZ holds ESSTail to arviz_stats
// ess(method="tail", prob=0.05).
func TestMCMC_ESSTailMatchesArviZ(t *testing.T) {
	for _, c := range loadDiagnostics(t) {
		near(t, c.Name+" ESSTail", mcmc.ESSTail(c.Chains), c.ESSTail)
	}
}

// TestMCMC_DiagnosticsOracleDiscriminates: the fixture set exercises the
// regimes the diagnostics must tell apart, so a degenerate implementation
// cannot match every reference by accident.
func TestMCMC_DiagnosticsOracleDiscriminates(t *testing.T) {
	ref := map[string]diagCase{}
	for _, c := range loadDiagnostics(t) {
		ref[c.Name] = c
	}
	get := func(name string) diagCase {
		c, ok := ref[name]
		if !ok {
			t.Fatalf("fixture %s missing", name)
		}
		return c
	}
	if r := *get("mean_shift_4x500").RHat; r < 1.1 {
		t.Errorf("a shifted chain should give R̂ ≥ 1.1, ArviZ %g", r)
	}
	// Equal locations, unequal scales: only the folded R̂ sees it.
	if r := *get("scale_shift_4x400").RHat; r < 1.1 {
		t.Errorf("a wider chain should give folded R̂ ≥ 1.1, ArviZ %g", r)
	}
	if e := *get("antithetic_2x300").ESSBulk; e <= 600 {
		t.Errorf("antithetic chains should give ESS above the draw count, ArviZ %g", e)
	}
	if get("single_chain_1x250").RHat != nil {
		t.Error("one chain should leave R̂ undefined")
	}
}

// TestMCMC_DiagnosticsRefuseBadShape: ragged chains, a non-finite draw,
// too few draws, or (R̂) a single chain give NaN, never a number.
func TestMCMC_DiagnosticsRefuseBadShape(t *testing.T) {
	ok := []float64{0.1, -0.4, 0.9, 0.3, -1.2, 0.7}
	cases := map[string][][]float64{
		"ragged":   {ok, ok[:5]},
		"nan":      {ok, {0.1, math.NaN(), 0.9, 0.3, -1.2, 0.7}},
		"inf":      {ok, {0.1, math.Inf(1), 0.9, 0.3, -1.2, 0.7}},
		"short":    {ok[:3], ok[:3]},
		"no chain": {},
	}
	for name, chains := range cases {
		for fn, f := range map[string]func([][]float64) float64{
			"RHat": mcmc.RHat, "ESSBulk": mcmc.ESSBulk, "ESSTail": mcmc.ESSTail,
		} {
			if got := f(chains); !math.IsNaN(got) {
				t.Errorf("%s(%s) = %g, want NaN", fn, name, got)
			}
		}
	}
	if got := mcmc.RHat([][]float64{ok}); !math.IsNaN(got) {
		t.Errorf("RHat(one chain) = %g, want NaN", got)
	}
	if got := mcmc.ESSBulk([][]float64{ok}); math.IsNaN(got) {
		t.Error("ESSBulk(one chain) is defined")
	}
}

// TestMCMC_DiagnosticsDeterministic: the diagnostics are pure functions
// of their input, invariant to a reorder of the chains (R̂ and ESS pool
// chains symmetrically) and leave the input untouched.
func TestMCMC_DiagnosticsDeterministic(t *testing.T) {
	cs := loadDiagnostics(t)
	var c diagCase
	for _, x := range cs {
		if x.Name == "ar1_4x1000" {
			c = x
		}
	}
	before := math.Float64bits(c.Chains[0][17])
	r1, b1, t1 := mcmc.RHat(c.Chains), mcmc.ESSBulk(c.Chains), mcmc.ESSTail(c.Chains)
	if math.Float64bits(c.Chains[0][17]) != before {
		t.Fatal("diagnostics mutated their input")
	}
	r2, b2, t2 := mcmc.RHat(c.Chains), mcmc.ESSBulk(c.Chains), mcmc.ESSTail(c.Chains)
	if math.Float64bits(r1) != math.Float64bits(r2) || math.Float64bits(b1) != math.Float64bits(b2) ||
		math.Float64bits(t1) != math.Float64bits(t2) {
		t.Error("repeated calls differ")
	}
	rev := [][]float64{c.Chains[3], c.Chains[2], c.Chains[1], c.Chains[0]}
	if math.Abs(mcmc.RHat(rev)-r1) > 1e-12 || math.Abs(mcmc.ESSBulk(rev)-b1) > 1e-9 {
		t.Error("chain order changed R̂ or ESS")
	}
}

// TestMCMC_ResultParam: Param regroups [chain][param][draw] into one
// parameter's [chain][draw] without copying.
func TestMCMC_ResultParam(t *testing.T) {
	r := mcmc.Result{Draws: [][][]float64{
		{{1, 2}, {3, 4}},
		{{5, 6}, {7, 8}},
	}}
	p := r.Param(1)
	if len(p) != 2 || p[0][0] != 3 || p[1][1] != 8 {
		t.Fatalf("Param(1) = %v", p)
	}
	p[0][0] = 99
	if r.Draws[0][1][0] != 99 {
		t.Error("Param copied instead of aliasing")
	}
}
