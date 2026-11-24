package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/regmargins"
	"github.com/frankbardon/pulse/types"
)

// multinomial_margins_test.go pins REG_MULTINOMIAL's per-category
// marginal effects and joint term tests (multinomial_margins.go, U68
// E4-S2) to R 4.6.1: margins.json's multinomial_cases
// (scripts/reference/gen_margins.R) — the closed forms at the polished
// multinom MLE (1e-9) and marginaleffects 1.0.0 avg_slopes / slopes
// (type = "probs") on the same fit (1e-7) — and multinomial.json's
// term_tests (scripts/reference/gen_multinomial.R): the Wald χ² on the
// classical and every robust covariance, the LR χ² against the refit
// without the term (car::Anova(multinom), type II), at 1e-10.

const (
	mnMarginsRel   = 1e-9
	mnMarginsMERel = 1e-7
)

// mnMarginsCase is one multinomial_cases entry: effects outcome-major
// (for each outcome, for each term).
type mnMarginsCase struct {
	Name               string      `json:"name"`
	Type               string      `json:"type"`
	Scale              string      `json:"scale"`
	Covariance         string      `json:"covariance"`
	WeightKind         string      `json:"weight_kind"`
	Levels             []string    `json:"levels"`
	Reference          string      `json:"reference"`
	ReferenceRequested bool        `json:"reference_requested"`
	Outcomes           []string    `json:"outcomes"`
	Terms              []string    `json:"terms"`
	Rows               [][]float64 `json:"rows"`
	Estimates          []float64   `json:"estimates"`
	StdErrors          []float64   `json:"std_errors"`
	Statistics         []float64   `json:"statistics"`
	PValues            []float64   `json:"p_values"`
	CILower            []float64   `json:"ci_lower"`
	CIUpper            []float64   `json:"ci_upper"`
	At                 []float64   `json:"at"`
	MEEstimates        []float64   `json:"me_estimates"`
	MEStdErrors        []float64   `json:"me_std_errors"`
	// Binary / Contrasts: binary contrast and the 0/1 terms it reports
	// as π_c(1) − π_c(0) (U69 #641).
	Binary    string   `json:"binary"`
	Contrasts []string `json:"contrasts"`
}

// loadMultinomialMargins reads margins.json's multinomial cases,
// checking its golden-hash footer and recorded versions.
func loadMultinomialMargins(t *testing.T) []mnMarginsCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "margins.json"))
	if err != nil {
		t.Fatalf("read margins.json: %v (regenerate with scripts/reference/gen_margins.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("margins.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("margins.json: hash mismatch (hand-edited?); regenerate it")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []mnMarginsCase   `json:"multinomial_cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode margins.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["nnet"] != "7.3.21" ||
		doc.Packages["marginaleffects"] != "1.0.0" || len(doc.Cases) != 16 {
		t.Fatalf("margins.json: R %q, packages %v, %d multinomial cases", doc.RVersion, doc.Packages, len(doc.Cases))
	}
	return doc.Cases
}

func (c mnMarginsCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: []string{"x1", "x2"},
		Margins: &types.RegressionMargins{Type: c.Type, Scale: c.Scale, Binary: c.Binary}}
	if c.ReferenceRequested {
		s.Reference = c.Reference
	}
	switch c.Covariance {
	case "hc1":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceHC1}
	case "cluster":
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}
	}
	if c.WeightKind != "" {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKind(c.WeightKind)})
	}
	return s
}

func (c mnMarginsCase) fit(t *testing.T) *types.RegressionResult {
	t.Helper()
	recs := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		recs[i] = labeledRecord{mockRecord: mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "w": r[3], "g": r[4]}},
			labels: map[string]string{"y": c.Levels[int(r[2])]}}
	}
	eng, err := newMultinomialEngine(c.spec(), categoricalSchema("y", c.Levels, "x1", "x2", "w", "g"))
	if err != nil {
		t.Fatalf("%s: newMultinomialEngine: %v", c.Name, err)
	}
	res, err := eng.(BufferedEngine).FitBuffered(recs)
	if err != nil {
		t.Fatalf("%s: FitBuffered: %v", c.Name, err)
	}
	if res.Margins == nil {
		t.Fatalf("%s: no margins block", c.Name)
	}
	return res
}

func mnRel(got, want float64) float64 {
	return math.Abs(got-want) / math.Max(1, math.Abs(want))
}

// TestMultinomialMarginsOracle_ClosedForms: every case's per-category
// effects, delta-method standard errors, statistics, p-values,
// intervals and mem point agree with R's closed forms at 1e-9 relative,
// outcome-major over the fitted categories (response) or the
// non-reference ones (link), keyed "<category>:<term>".
func TestMultinomialMarginsOracle_ClosedForms(t *testing.T) {
	t.Parallel()
	worst := 0.0
	for _, c := range loadMultinomialMargins(t) {
		mg := c.fit(t).Margins
		if !slices.Equal(mg.Outcomes, c.Outcomes) || !slices.Equal(mg.Terms, c.Terms) || mg.Type != c.Type || mg.Scale != c.Scale || mg.DF != 0 || mg.ConfLevel != 0.95 {
			t.Fatalf("%s: outcomes %v terms %v type %s scale %s df %v, R %v %v", c.Name, mg.Outcomes, mg.Terms, mg.Type, mg.Scale, mg.DF, c.Outcomes, c.Terms)
		}
		if mg.Binary != c.Binary || !slices.Equal(mg.Contrasts, c.Contrasts) {
			t.Fatalf("%s: binary %q contrasts %v, R %q %v", c.Name, mg.Binary, mg.Contrasts, c.Binary, c.Contrasts)
		}
		keys := regmargins.ResultKeys(mg)
		if len(keys) != len(c.Estimates) || len(mg.Estimates) != len(keys) {
			t.Fatalf("%s: %d keys, R %d", c.Name, len(keys), len(c.Estimates))
		}
		for i, k := range keys {
			if want := c.Outcomes[i/len(c.Terms)] + ":" + c.Terms[i%len(c.Terms)]; k != want {
				t.Fatalf("%s: key %d is %s, want %s", c.Name, i, k, want)
			}
			for _, f := range []struct {
				what      string
				got, want float64
			}{
				{"estimate", mg.Estimates[k], c.Estimates[i]},
				{"std_error", mg.StdErrors[k], c.StdErrors[i]},
				{"statistic", mg.Statistics[k], c.Statistics[i]},
				{"p_value", mg.PValues[k], c.PValues[i]},
				{"ci_lower", mg.CILower[k], c.CILower[i]},
				{"ci_upper", mg.CIUpper[k], c.CIUpper[i]},
			} {
				e := mnRel(f.got, f.want)
				worst = math.Max(worst, e)
				if !(e <= mnMarginsRel) {
					t.Errorf("%s %s %s = %.17g, R %.17g (rel %.3g)", c.Name, k, f.what, f.got, f.want, e)
				}
			}
		}
		if c.Type == types.MarginsMEM {
			for j, p := range []string{"x1", "x2"} {
				if e := mnRel(mg.At[p], c.At[j]); e > mnMarginsRel {
					t.Errorf("%s: at[%s] = %v, R %v", c.Name, p, mg.At[p], c.At[j])
				}
			}
		} else if mg.At != nil {
			t.Errorf("%s: ame reports at %v", c.Name, mg.At)
		}
	}
	t.Logf("multinomial margins vs R closed forms: worst rel %.3g", worst)
}

// TestMultinomialMarginsOracle_Marginaleffects: the response-scale
// effects and standard errors agree with marginaleffects (avg_slopes /
// slopes at the weighted means, type = "probs") within its numerical
// accuracy.
func TestMultinomialMarginsOracle_Marginaleffects(t *testing.T) {
	t.Parallel()
	n, worstEst, worstSE := 0, 0.0, 0.0
	for _, c := range loadMultinomialMargins(t) {
		if c.MEEstimates == nil {
			continue
		}
		n++
		mg := c.fit(t).Margins
		for i, k := range regmargins.ResultKeys(mg) {
			re := math.Abs(mg.Estimates[k]-c.MEEstimates[i]) / math.Abs(c.MEEstimates[i])
			rs := math.Abs(mg.StdErrors[k]-c.MEStdErrors[i]) / math.Abs(c.MEStdErrors[i])
			worstEst, worstSE = math.Max(worstEst, re), math.Max(worstSE, rs)
			if re > mnMarginsMERel || rs > mnMarginsMERel {
				t.Errorf("%s %s: estimate rel %.3g, std error rel %.3g vs marginaleffects", c.Name, k, re, rs)
			}
		}
	}
	if n != 12 {
		t.Fatalf("%d cases carry a marginaleffects cross-check, want 12", n)
	}
	t.Logf("multinomial margins vs marginaleffects: estimate %.3g, std_error %.3g", worstEst, worstSE)
}

// TestMultinomialMargins_LinkIsTheCoefficient: on the link scale every
// effect is its "<category>:<term>" coefficient, standard error,
// p-value and interval bit for bit, and the response-scale effects of a
// term sum to zero over the categories (the probabilities sum to one).
func TestMultinomialMargins_LinkIsTheCoefficient(t *testing.T) {
	t.Parallel()
	for _, c := range loadMultinomialMargins(t) {
		res := c.fit(t)
		mg := res.Margins
		if c.Scale == types.MarginsScaleLink {
			for _, k := range regmargins.ResultKeys(mg) {
				if mg.Estimates[k] != res.Coefficients[k] || mg.StdErrors[k] != res.StdErrors[k] ||
					mg.PValues[k] != res.PValues[k] || mg.CILower[k] != res.CILower[k] || mg.CIUpper[k] != res.CIUpper[k] {
					t.Errorf("%s %s: link effect differs from its coefficient", c.Name, k)
				}
			}
			continue
		}
		for _, term := range mg.Terms {
			sum, scale := 0.0, 0.0
			for _, o := range mg.Outcomes {
				e := mg.Estimates[regmargins.Key(o, term)]
				sum += e
				scale = math.Max(scale, math.Abs(e))
			}
			if math.Abs(sum) > 1e-14*math.Max(scale, 1) {
				t.Errorf("%s: effects of %s sum to %g", c.Name, term, sum)
			}
		}
	}
}

// TestReferenceOracle_MultinomialTermTestsMatchR: term_tests on every
// multinomial.json case — the Wald χ² on the classical covariance and on
// each robust type's sandwich, the LR χ² against the refit without the
// term (absent under a probability weight), their χ²(K − 1) p-values
// and df — agree with R (car::linearHypothesis / car::Anova on the
// polished multinom) at 1e-10, and the block leaves every other figure
// of the fit untouched.
func TestReferenceOracle_MultinomialTermTestsMatchR(t *testing.T) {
	t.Parallel()
	// lr compares the LR figures against want; hasLR is whether they
	// must be present at all.
	check := func(t *testing.T, name string, got *types.RegressionTermTests, want multinomialTermTests, lr, hasLR bool) {
		t.Helper()
		if got == nil || !slices.Equal(got.Terms, want.Terms) {
			t.Fatalf("%s: term_tests %+v, R terms %v", name, got, want.Terms)
		}
		for i, term := range want.Terms {
			if got.DF[term] != want.DF {
				t.Errorf("%s %s: df %d, R %d", name, term, got.DF[term], want.DF)
			}
			pairs := [][2]float64{{got.WaldChi2[term], want.WaldChi2[i]}, {got.WaldPValues[term], want.WaldPValues[i]}}
			if lr {
				pairs = append(pairs, [2]float64{got.LRChi2[term], want.LRChi2[i]}, [2]float64{got.LRPValues[term], want.LRPValues[i]})
			}
			for _, p := range pairs {
				if e := mnRel(p[0], p[1]); !(e <= multinomialTol) {
					t.Errorf("%s %s: %.17g, R %.17g (rel %.3g)", name, term, p[0], p[1], e)
				}
			}
		}
		if hasLR != (got.LRChi2 != nil) || hasLR != (got.LRPValues != nil) {
			t.Errorf("%s: LR figures %v, want present = %v (absent only under a probability weight)", name, got.LRChi2, hasLR)
		}
	}
	for _, c := range loadMultinomial(t) {
		lr := c.WeightKind != string(types.WeightKindProbability)
		if lr != (c.TermTests.LRChi2 != nil) {
			t.Fatalf("%s: golden LR block present = %v", c.Name, c.TermTests.LRChi2 != nil)
		}
		recs := c.records()
		for i, rec := range recs {
			rec.(labeledRecord).values["g"] = c.Robust.Clusters[i]
		}
		fit := func(cov *types.RegressionCovariance, tests bool) *types.RegressionResult {
			spec := c.spec()
			spec.Covariance, spec.TermTests = cov, tests
			eng, err := newMultinomialEngine(spec, categoricalSchema("y", c.Levels, "x1", "x2", "w", "g"))
			if err != nil {
				t.Fatalf("newMultinomialEngine: %v", err)
			}
			res, err := eng.(*multinomialEngine).FitBuffered(recs)
			if err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
			return res
		}
		res := fit(nil, true)
		check(t, c.Name, res.TermTests, c.TermTests, lr, lr)
		classical := res.TermTests
		plain := fit(nil, false)
		res.TermTests = nil
		a, _ := json.Marshal(res)
		b, _ := json.Marshal(plain)
		if string(a) != string(b) {
			t.Errorf("%s: term_tests moved another figure:\n%s\nvs\n%s", c.Name, a, b)
		}
		for _, typ := range []struct {
			cov  types.CovarianceType
			want multinomialRobust
		}{
			{types.CovarianceHC0, c.Robust.HC0},
			{types.CovarianceHC1, c.Robust.HC1},
			{types.CovarianceCluster, c.Robust.Cluster},
		} {
			cov := &types.RegressionCovariance{Type: typ.cov}
			if typ.cov == types.CovarianceCluster {
				cov.Cluster = "g"
			}
			want := typ.want.TermTestsWald
			want.Terms, want.DF = c.TermTests.Terms, c.TermTests.DF
			rob := fit(cov, true).TermTests
			check(t, c.Name+"/"+string(typ.cov), rob, want, false, lr)
			// The likelihood ratio reads no covariance: the classical fit's.
			if lr && !maps2Equal(rob.LRChi2, classical.LRChi2) {
				t.Errorf("%s/%s: LR %v moved from the classical fit's %v", c.Name, typ.cov, rob.LRChi2, classical.LRChi2)
			}
		}
	}
}

// maps2Equal reports whether two figure maps agree key for key, bit for
// bit.
func maps2Equal(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || math.Float64bits(v) != math.Float64bits(w) {
			return false
		}
	}
	return true
}
