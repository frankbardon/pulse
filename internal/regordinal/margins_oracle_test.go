package regordinal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// margins_oracle_test.go pins REG_ORDINAL's per-level marginal effects
// (U68 E4-S1, #652) to R 4.6.1: the ordinal_cases block of margins.json
// (scripts/reference/gen_margins.R) — the closed forms at the polished
// polr MLE (primary, marginsOracleRel) and marginaleffects 1.0.0 on the
// same fit (cross-check, marginsMERel, its Richardson-extrapolated
// numerical Jacobian).

const (
	marginsOracleRel = 1e-9
	marginsMERel     = 1e-7
)

// marginsOracleCase is one ordinal_cases entry: effects outcome-major
// (for each outcome, for each term).
type marginsOracleCase struct {
	Name        string      `json:"name"`
	Link        string      `json:"link"`
	Type        string      `json:"type"`
	Scale       string      `json:"scale"`
	Covariance  string      `json:"covariance"`
	Weighted    bool        `json:"weighted"`
	Levels      []string    `json:"levels"`
	Outcomes    []string    `json:"outcomes"`
	Terms       []string    `json:"terms"`
	Rows        [][]float64 `json:"rows"`
	Estimates   []float64   `json:"estimates"`
	StdErrors   []float64   `json:"std_errors"`
	Statistics  []float64   `json:"statistics"`
	PValues     []float64   `json:"p_values"`
	CILower     []float64   `json:"ci_lower"`
	CIUpper     []float64   `json:"ci_upper"`
	At          []float64   `json:"at"`
	MEEstimates []float64   `json:"me_estimates"`
	MEStdErrors []float64   `json:"me_std_errors"`
}

// loadMarginsOracle reads margins.json's ordinal cases, checking its
// golden-hash footer and recorded versions.
func loadMarginsOracle(t *testing.T) []marginsOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "margins.json"))
	if err != nil {
		t.Fatalf("read margins.json: %v (regenerate with scripts/reference/gen_margins.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("margins.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("margins.json hash mismatch (hand-edited? regenerate it)")
	}
	var doc struct {
		RVersion string              `json:"r_version"`
		Packages map[string]string   `json:"packages"`
		Cases    []marginsOracleCase `json:"ordinal_cases"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RVersion != "R version 4.6.1 (2026-06-24)" || doc.Packages["MASS"] != "7.3.66" || doc.Packages["marginaleffects"] != "1.0.0" || len(doc.Cases) != 10 {
		t.Fatalf("margins.json records R %q, packages %v, %d ordinal cases", doc.RVersion, doc.Packages, len(doc.Cases))
	}
	return doc.Cases
}

// marginsRows is the case's rows ([x1, x2, y, w, g], y 1-based) as the
// model reads them.
type marginsRows struct {
	c marginsOracleCase
}

func (r marginsRows) Len() int { return len(r.c.Rows) }
func (r marginsRows) At(i int) extend.Record {
	row := r.c.Rows[i]
	return marginsRecord{vals: map[string]float64{"x1": row[0], "x2": row[1], "y": row[2], "g": row[4]}, w: row[3], weighted: r.c.Weighted}
}

type marginsRecord struct {
	fakeRecord
	vals     map[string]float64
	w        float64
	weighted bool
}

func (r marginsRecord) NumericValue(f string) (float64, bool) {
	v, ok := r.vals[f]
	return v, ok
}
func (r marginsRecord) IsNull(f string) bool { _, ok := r.vals[f]; return !ok }
func (r marginsRecord) Weight() (float64, bool) {
	return r.w, r.weighted
}

// fitMargins fits the case through the real factory and asks for its
// effects (extend.MarginsRegression), as the engine's driver does.
func fitMargins(t *testing.T, c marginsOracleCase) extend.MarginsOutput {
	t.Helper()
	spec := &types.RegressionSpec{Type: Name, Target: "y", Predictors: c.Terms,
		Margins: &types.RegressionMargins{Type: c.Type, Scale: c.Scale}}
	if c.Link != LinkLogit {
		spec.Params = json.RawMessage(`{"link":"` + c.Link + `"}`)
	}
	switch c.Covariance {
	case "hc1":
		spec.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
	case "cluster":
		spec.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}
	}
	if c.Weighted {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	reg, err := Factory(spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := reg.(*model)
	if _, err := m.Fit(marginsRows{c}); err != nil {
		t.Fatalf("%s: Fit: %v", c.Name, err)
	}
	out, err := m.Margins(extend.MarginsRequest{Type: spec.Margins.EffectiveType(), Scale: spec.Margins.EffectiveScale(), Terms: c.Terms})
	if err != nil {
		t.Fatalf("%s: Margins: %v", c.Name, err)
	}
	return out
}

// TestOrdinalMarginsOracle_ClosedForms: every case's per-level effects
// and their delta-method standard errors (and the mem point) agree with
// R's closed forms at 1e-9 relative, outcome-major in the level order.
func TestOrdinalMarginsOracle_ClosedForms(t *testing.T) {
	t.Parallel()
	worstEst, worstSE := 0.0, 0.0
	for _, c := range loadMarginsOracle(t) {
		out := fitMargins(t, c)
		if strings.Join(out.Outcomes, ",") != strings.Join(c.Outcomes, ",") || len(out.Effects) != len(c.Estimates) {
			t.Fatalf("%s: outcomes %v (%d effects), R %v (%d)", c.Name, out.Outcomes, len(out.Effects), c.Outcomes, len(c.Estimates))
		}
		for i, e := range out.Effects {
			if e.Outcome != c.Outcomes[i/len(c.Terms)] || e.Term != c.Terms[i%len(c.Terms)] {
				t.Fatalf("%s: effect %d is %s:%s, want outcome-major order", c.Name, i, e.Outcome, e.Term)
			}
			re, rs := relErr(e.Estimate, c.Estimates[i]), relErr(*e.StdErr, c.StdErrors[i])
			worstEst, worstSE = math.Max(worstEst, re), math.Max(worstSE, rs)
			if re > marginsOracleRel || rs > marginsOracleRel {
				t.Errorf("%s %s:%s: estimate %.17g (R %.17g, rel %.3g), std error %.17g (R %.17g, rel %.3g)",
					c.Name, e.Outcome, e.Term, e.Estimate, c.Estimates[i], re, *e.StdErr, c.StdErrors[i], rs)
			}
		}
		if c.Type == types.MarginsMEM {
			for j, term := range c.Terms {
				if e := relErr(out.At[term], c.At[j]); e > marginsOracleRel {
					t.Errorf("%s: at[%s] = %v, R %v", c.Name, term, out.At[term], c.At[j])
				}
			}
		} else if out.At != nil {
			t.Errorf("%s: ame reports at %v", c.Name, out.At)
		}
	}
	t.Logf("ordinal margins vs R closed forms: estimate %.3g, std_error %.3g", worstEst, worstSE)
}

// TestOrdinalMarginsOracle_Marginaleffects: the response-scale effects
// and standard errors agree with marginaleffects (avg_slopes / slopes at
// the weighted means on polr) within its numerical accuracy.
func TestOrdinalMarginsOracle_Marginaleffects(t *testing.T) {
	t.Parallel()
	worstEst, worstSE, n := 0.0, 0.0, 0
	for _, c := range loadMarginsOracle(t) {
		if c.MEEstimates == nil {
			continue
		}
		n++
		for i, e := range fitMargins(t, c).Effects {
			re := math.Abs(e.Estimate-c.MEEstimates[i]) / math.Abs(c.MEEstimates[i])
			rs := math.Abs(*e.StdErr-c.MEStdErrors[i]) / math.Abs(c.MEStdErrors[i])
			worstEst, worstSE = math.Max(worstEst, re), math.Max(worstSE, rs)
			if re > marginsMERel || rs > marginsMERel {
				t.Errorf("%s %s:%s: estimate rel %.3g, std error rel %.3g vs marginaleffects", c.Name, e.Outcome, e.Term, re, rs)
			}
		}
	}
	if n != 8 {
		t.Fatalf("%d cases carry a marginaleffects cross-check, want 8", n)
	}
	t.Logf("ordinal margins vs marginaleffects: estimate %.3g, std_error %.3g", worstEst, worstSE)
}

// TestOrdinalMargins_LevelsSumToZero: the response-scale effects of a
// term over the levels sum to zero (the probabilities sum to one), and
// the link-scale effects are −β with the coefficient's standard error.
func TestOrdinalMargins_LevelsSumToZero(t *testing.T) {
	t.Parallel()
	for _, c := range loadMarginsOracle(t) {
		out := fitMargins(t, c)
		if c.Scale == types.MarginsScaleLink {
			continue
		}
		for j, term := range c.Terms {
			sum, scale := 0.0, 0.0
			for k := range out.Outcomes {
				e := out.Effects[k*len(c.Terms)+j].Estimate
				sum += e
				scale = math.Max(scale, math.Abs(e))
			}
			if math.Abs(sum) > 1e-14*math.Max(scale, 1) {
				t.Errorf("%s: effects of %s sum to %g", c.Name, term, sum)
			}
		}
	}
}
