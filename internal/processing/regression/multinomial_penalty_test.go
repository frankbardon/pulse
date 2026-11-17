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

	"github.com/frankbardon/pulse/types"
)

// multinomial_penalty_test.go pins the penalised REG_MULTINOMIAL fit
// (multinomial_penalty.go, U68 E2-S3) to R 4.6.1's glmnet 5.1 family =
// "multinomial", type.multinomial = "ungrouped", standardize = FALSE —
// Newton-polished on the active set to the stated objective and
// KKT-checked: multinomial_penalty.json
// (scripts/reference/gen_multinomial_penalty.R, make reference). Ridge,
// lasso and elastic net on three categories, with `unpenalized`
// predictors on each kind, a frequency-weighted elastic net, and ridge /
// elastic net on four categories.

// multinomialPenaltyTol is the oracle tolerance: both sides are the
// objective's exact minimiser (R's active-set Newton polish, Pulse's
// quadratically converging proximal Newton with an exact active-set
// finish), so 1e-10 relative holds with room; near-zero figures read an
// absolute floor of 1e-12 (an exact zero is checked exactly).
const multinomialPenaltyTol = 1e-10

type multinomialPenaltyCase struct {
	Name               string      `json:"name"`
	Levels             []string    `json:"levels"`
	Penalty            string      `json:"penalty"`
	Alpha              float64     `json:"alpha"`
	L1Ratio            float64     `json:"l1_ratio"`
	Unpenalized        []string    `json:"unpenalized"`
	Weighted           bool        `json:"weighted"`
	Rows               [][]float64 `json:"rows"`
	Keys               []string    `json:"keys"`
	Coefficients       []float64   `json:"coefficients"`
	Zero               []string    `json:"zero"`
	Deviance           float64     `json:"deviance"`
	NullDeviance       float64     `json:"null_deviance"`
	ScoreRows          [][]float64 `json:"score_rows"`
	ScoreProbabilities []float64   `json:"score_probabilities"`
	ScoreLink          []float64   `json:"score_link"`
}

func loadMultinomialPenalty(t *testing.T) []multinomialPenaltyCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "multinomial_penalty.json"))
	if err != nil {
		t.Fatalf("read multinomial_penalty.json: %v (regenerate with scripts/reference/gen_multinomial_penalty.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("multinomial_penalty.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("multinomial_penalty.json: hash mismatch (hand-edited?)")
	}
	var doc struct {
		RVersion string                   `json:"r_version"`
		Packages map[string]string        `json:"packages"`
		Cases    []multinomialPenaltyCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode multinomial_penalty.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["glmnet"] != "5.1" || len(doc.Cases) != 9 {
		t.Fatalf("multinomial_penalty.json: R %q, glmnet %q, %d cases", doc.RVersion, doc.Packages["glmnet"], len(doc.Cases))
	}
	return doc.Cases
}

var multinomialPenaltyPredictors = []string{"x1", "x2", "x3"}

func (c multinomialPenaltyCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		v := map[string]float64{"x1": r[0], "x2": r[1], "x3": r[2], "y": r[3], "w": r[4]}
		out[i] = labeledRecord{mockRecord: mockRecord{values: v}, labels: map[string]string{"y": c.Levels[int(r[3])]}}
	}
	return out
}

func (c multinomialPenaltyCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{
		Type: types.REG_MULTINOMIAL, Target: "y", Predictors: slices.Clone(multinomialPenaltyPredictors),
		Penalty: c.Penalty, Alpha: c.Alpha, Unpenalized: slices.Clone(c.Unpenalized),
	}
	if c.Penalty == "elasticnet" {
		s.L1Ratio = c.L1Ratio
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	}
	return s
}

// TestReferenceOracle_MultinomialPenaltyMatchesGlmnet: every case's
// symmetric coefficients (every category, intercepts centred) agree
// with the polished glmnet fit at 1e-10 relative, its zero pattern
// exactly; deviance and null deviance too; the result names no
// reference and reports null standard errors, p-values and intervals;
// and the bound fit's scorer reproduces glmnet's grid probabilities and
// per-category linear predictors (type = "link").
func TestReferenceOracle_MultinomialPenaltyMatchesGlmnet(t *testing.T) {
	t.Parallel()

	maxErr := map[string]float64{}
	for _, c := range loadMultinomialPenalty(t) {
		t.Run(c.Name, func(t *testing.T) {
			spec := c.spec()
			eng := newMultinomial(t, spec, c.Levels)
			res, err := eng.FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			near := func(what string, got, want float64) {
				t.Helper()
				e := math.Abs(got-want) / math.Max(math.Abs(want), 1e-12/multinomialPenaltyTol)
				key := what
				if i := strings.IndexAny(what, "[ "); i > 0 {
					key = what[:i]
				}
				maxErr[key] = math.Max(maxErr[key], e)
				if e > multinomialPenaltyTol {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
				}
			}
			if res.Reference != "" || !slices.Equal(res.Categories, c.Levels) || res.NObs != len(c.Rows) {
				t.Fatalf("reference %q categories %v n_obs %d; want none, %v, %d", res.Reference, res.Categories, res.NObs, c.Levels, len(c.Rows))
			}
			if res.Penalty != c.Penalty || res.Alpha != c.Alpha {
				t.Errorf("penalty echo %q %v", res.Penalty, res.Alpha)
			}
			if len(res.Coefficients) != len(c.Keys) {
				t.Fatalf("%d coefficients, R %d", len(res.Coefficients), len(c.Keys))
			}
			for j, k := range c.Keys {
				got, ok := res.Coefficients[k]
				if !ok {
					t.Fatalf("no coefficient %s", k)
				}
				if zero := slices.Contains(c.Zero, k); zero != (got == 0) {
					t.Errorf("coef[%s] = %.17g; R zero %v", k, got, zero)
				}
				near("coef["+k+"]", got, c.Coefficients[j])
				for _, fig := range []map[string]float64{res.StdErrors, res.PValues, res.CILower, res.CIUpper} {
					if v, ok := fig[k]; !ok || !math.IsNaN(v) {
						t.Errorf("%s inference %v (present %v); want NaN, key kept", k, v, ok)
					}
				}
			}
			near("deviance", res.Deviance, c.Deviance)
			near("null_deviance", res.NullDeviance, c.NullDeviance)
			if res.TwoLogLik != 0 || res.AIC != 0 || res.Vcov != nil {
				t.Errorf("two_log_lik %v aic %v vcov %v; want absent", res.TwoLogLik, res.AIC, res.Vcov)
			}
			f := multinomialFitted(t, spec, res)
			K := len(c.Levels)
			pi := make([]float64, K)
			for i, r := range c.ScoreRows {
				row := mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "x3": r[2]}}
				if !f.Probabilities(row, pi) {
					t.Fatalf("score row %d scored null", i)
				}
				for k := range c.Levels {
					near("score_probability", pi[k], c.ScoreProbabilities[i*K+k])
					eta, ok := f.LogOdds(row, k)
					if !ok {
						t.Fatalf("score row %d link null", i)
					}
					near("score_link", eta, c.ScoreLink[i*K+k])
				}
			}
		})
	}
	t.Logf("max relative error: %v", maxErr)
}

// penaltyCase is the named oracle case.
func penaltyCase(t *testing.T, name string) multinomialPenaltyCase {
	t.Helper()
	for _, c := range loadMultinomialPenalty(t) {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no case %s", name)
	return multinomialPenaltyCase{}
}

// TestMultinomialPenalty_JackknifeIsManualDeletion: under resample
// jackknife a penalised fit keeps the full fit's symmetric coefficients
// (every category, no reference) and each coefficient's SE is the
// leave-one-out jackknife over fresh penalised fits of the deleted
// samples, √((n−1)/n·Σ(β₍ᵢ₎ − β̄)²); p-values are the Wald z on it
// (inference is defined under resample).
func TestMultinomialPenalty_JackknifeIsManualDeletion(t *testing.T) {
	t.Parallel()
	c := penaltyCase(t, "three_elasticnet_unpenalized")
	c.Rows = c.Rows[:60]
	recs := c.records()
	full, err := newMultinomial(t, c.spec(), c.Levels).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	spec := c.spec()
	spec.Resample = "jackknife"
	res, err := fitMultinomialMod(t, spec, c.Levels, recs)
	if err != nil {
		t.Fatal(err)
	}
	if res.Reference != "" || len(res.Coefficients) != len(c.Keys) {
		t.Fatalf("reference %q, %d coefficients; want none, %d", res.Reference, len(res.Coefficients), len(c.Keys))
	}
	n := len(recs)
	reps := make([]map[string]float64, n)
	for i := range recs {
		rest := append(slices.Clone(recs[:i]), recs[i+1:]...)
		r, err := newMultinomial(t, c.spec(), c.Levels).FitBuffered(rest)
		if err != nil {
			t.Fatalf("deleted %d: %v", i, err)
		}
		reps[i] = r.Coefficients
	}
	for _, k := range c.Keys {
		if res.Coefficients[k] != full.Coefficients[k] {
			t.Errorf("coef[%s] %v, full fit %v", k, res.Coefficients[k], full.Coefficients[k])
		}
		mean := 0.0
		for _, r := range reps {
			mean += r[k]
		}
		mean /= float64(n)
		ss := 0.0
		for _, r := range reps {
			ss += (r[k] - mean) * (r[k] - mean)
		}
		want := math.Sqrt(float64(n-1) / float64(n) * ss)
		if e := math.Abs(res.StdErrors[k]-want) / math.Max(want, 1e-12); e > 1e-9 {
			t.Errorf("se[%s] = %.17g, manual %.17g (rel %.3g)", k, res.StdErrors[k], want, e)
		}
		if p := res.PValues[k]; want > 0 && (math.IsNaN(p) || p != waldZTwoSidedP(res.Coefficients[k], res.StdErrors[k])) {
			t.Errorf("p[%s] = %v, want the Wald z", k, p)
		}
	}
}

// TestMultinomialPenalty_BootstrapAndSelectionRun: a seeded bootstrap
// of a penalised fit is bit-for-bit reproducible and reports every
// category's coefficients; a penalised selection refits the symmetric
// form over the chosen terms (K·(terms + 1) coefficients, no reference)
// with null inference (no resample).
func TestMultinomialPenalty_BootstrapAndSelectionRun(t *testing.T) {
	t.Parallel()
	c := penaltyCase(t, "four_l2")
	recs := c.records()
	var first []byte
	for range 2 {
		spec := c.spec()
		spec.Resample, spec.BootstrapIters, spec.RNGSeed = "bootstrap", 15, 7
		res, err := fitMultinomialMod(t, spec, c.Levels, recs)
		if err != nil {
			t.Fatal(err)
		}
		if res.BootstrapSucceeded != 15 || len(res.StdErrors) != len(c.Keys) || res.Reference != "" {
			t.Fatalf("bootstrap %d succeeded, %d SEs, reference %q", res.BootstrapSucceeded, len(res.StdErrors), res.Reference)
		}
		for _, k := range c.Keys {
			if se := res.StdErrors[k]; !(se > 0) {
				t.Errorf("se[%s] = %v", k, se)
			}
		}
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
		} else if string(b) != string(first) {
			t.Errorf("the seeded bootstrap moved")
		}
	}
	spec := c.spec()
	spec.Selection, spec.Criterion = "backward", "bic"
	res, err := fitMultinomialMod(t, spec, c.Levels, recs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SelectedFeatures) == 0 || len(res.Coefficients) != len(c.Levels)*(len(res.SelectedFeatures)+1) || res.Reference != "" {
		t.Fatalf("selected %v, %d coefficients, reference %q", res.SelectedFeatures, len(res.Coefficients), res.Reference)
	}
	for k, se := range res.StdErrors {
		if !math.IsNaN(se) {
			t.Errorf("se[%s] = %v, want NaN without resample", k, se)
		}
	}
}

// TestMultinomialPenalty_Centre: an unpenalised term is centred to mean
// zero; under pure l1 a penalised term's median interval is centred on
// zero (an odd count moves to its median, which is already 0 at the
// optimum); under l2 / elasticnet a penalised term is left as fitted.
func TestMultinomialPenalty_Centre(t *testing.T) {
	t.Parallel()
	p := &multinomialPenalty{pen: []bool{false, true}}
	// K = 4 blocks of q = 2: (intercept, x).
	beta := []float64{1, -0.5, 2, 0, 3, 0.2, 6, 0.7}
	p.centre(beta, 2, 4, 0)
	want := []float64{-2, -0.6, -1, -0.1, 0, 0.1, 3, 0.6}
	for i := range want {
		if math.Abs(beta[i]-want[i]) > 1e-15 {
			t.Fatalf("l1 centre = %v, want %v", beta, want)
		}
	}
	beta = []float64{0, -0.5, 0, 0.2, 0, 0.7}
	p.centre(beta, 2, 3, 0)
	if math.Abs(beta[1]+0.7) > 1e-15 || beta[3] != 0 || math.Abs(beta[5]-0.5) > 1e-15 {
		t.Fatalf("odd-K l1 centre = %v", beta)
	}
	beta = []float64{0, -0.5, 0, 0.2, 0, 0.7}
	p.centre(beta, 2, 3, 0.1)
	if beta[1] != -0.5 || beta[3] != 0.2 || beta[5] != 0.7 {
		t.Fatalf("l2 centre moved a penalised term: %v", beta)
	}
}

// TestMultinomialPenalty_EvenLassoCentred: a four-category lasso fit —
// whose penalised terms are pinned only up to a shift inside their
// median interval — converges and reports every penalised term with its
// two middle coefficients symmetric about zero (glmnet's centring), the
// intercepts summing to zero.
func TestMultinomialPenalty_EvenLassoCentred(t *testing.T) {
	t.Parallel()
	c := penaltyCase(t, "four_l2")
	c.Penalty, c.Alpha = "l1", 0.004
	res, err := newMultinomial(t, c.spec(), c.Levels).FitBuffered(c.records())
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range append([]string{InterceptKey}, multinomialPenaltyPredictors...) {
		vals := make([]float64, 0, len(c.Levels))
		for _, l := range c.Levels {
			vals = append(vals, res.Coefficients[l+":"+term])
		}
		slices.Sort(vals)
		sum := vals[0] + vals[1] + vals[2] + vals[3]
		if term == InterceptKey && math.Abs(sum) > 1e-12 {
			t.Errorf("intercepts %v sum %v", vals, sum)
		}
		if term != InterceptKey && math.Abs(vals[1]+vals[2]) > 1e-12 {
			t.Errorf("%s: middle coefficients %v not centred", term, vals)
		}
	}
}
