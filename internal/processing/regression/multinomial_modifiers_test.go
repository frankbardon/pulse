package regression

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// multinomial_modifiers_test.go pins REG_MULTINOMIAL under the resample
// and selection modifiers (U68 E2-S2, #658) to multinomial.json's
// modifiers block (gen_multinomial.R): the manual-deletion jackknife on
// multinom (leave-one-out and delete-a-cluster) and MASS::stepAIC on
// multinom, a term entering or leaving across every non-reference
// category, edf = (K − 1)·(terms + 1). Every refit keeps the full fit's
// categories and reference (decision D5): a bootstrap replicate that
// lost a category is skipped, a jackknife one fails the fit with reason
// replicate_empty_level.

// mnModTol is the oracle tolerance: every figure is a polished MLE (or
// a jackknife over polished refits), Newton-converged on both sides.
const mnModTol = 1e-8

type mnModifiers struct {
	Jackknife struct {
		Levels       []string    `json:"levels"`
		Predictors   []string    `json:"predictors"`
		Keys         []string    `json:"keys"`
		Coefficients []float64   `json:"coefficients"`
		StdErrors    []float64   `json:"std_errors"`
		ByGroups     int         `json:"by_groups"`
		BySE         []float64   `json:"by_std_errors"`
		Rows         [][]float64 `json:"rows"`
	} `json:"jackknife"`
	Selection struct {
		Levels     []string `json:"levels"`
		Predictors []string `json:"predictors"`
		NObs       int      `json:"n_obs"`
		Runs       []struct {
			Direction    string    `json:"direction"`
			Criterion    string    `json:"criterion"`
			Selected     []string  `json:"selected"`
			Keys         []string  `json:"keys"`
			Coefficients []float64 `json:"coefficients"`
			Deviance     []float64 `json:"deviance"`
		} `json:"runs"`
		Rows [][]float64 `json:"rows"`
	} `json:"selection"`
}

func loadMultinomialModifiers(t *testing.T) mnModifiers {
	t.Helper()
	loadMultinomial(t) // the hash and pin checks
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "multinomial.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	var doc struct {
		Packages  map[string]string `json:"packages"`
		Modifiers mnModifiers       `json:"modifiers"`
	}
	if err := json.Unmarshal([]byte(s[:strings.LastIndex(s, "\n// golden-hash: ")]), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Packages["MASS"] != "7.3.66" || len(doc.Modifiers.Jackknife.Rows) == 0 || len(doc.Modifiers.Selection.Runs) != 6 {
		t.Fatalf("multinomial.json modifiers: MASS %q, %d jackknife rows, %d selection runs",
			doc.Packages["MASS"], len(doc.Modifiers.Jackknife.Rows), len(doc.Modifiers.Selection.Runs))
	}
	return doc.Modifiers
}

// mnModRecords builds rows [x..., y, g] over predictors and levels.
func mnModRecords(rows [][]float64, predictors, levels []string) []Record {
	out := make([]Record, len(rows))
	p := len(predictors)
	for i, r := range rows {
		v := map[string]float64{"y": r[p], "g": r[p+1]}
		for j, name := range predictors {
			v[name] = r[j]
		}
		out[i] = labeledRecord{mockRecord: mockRecord{values: v}, labels: map[string]string{"y": levels[int(r[p])]}}
	}
	return out
}

func fitMultinomialMod(t *testing.T, spec *types.RegressionSpec, levels []string, recs []Record) (*types.RegressionResult, error) {
	t.Helper()
	eng := newMultinomial(t, spec, levels)
	return eng.FitBufferedContext(context.Background(), recs)
}

func mnModNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if e := math.Abs(got-want) / math.Max(math.Abs(want), 1); !(e <= mnModTol) {
		t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
	}
}

// TestMultinomial_JackknifeMatchesR: the leave-one-out and the
// delete-a-cluster (jackknife_by) jackknife standard errors of every
// "<category>:<term>" coefficient agree with R's manual-deletion
// jackknife on multinom at 1e-8; the coefficients stay the full fit's,
// p-values are the Wald z on the replicate SE.
func TestMultinomial_JackknifeMatchesR(t *testing.T) {
	t.Parallel()
	jk := loadMultinomialModifiers(t).Jackknife
	recs := mnModRecords(jk.Rows, jk.Predictors, jk.Levels)
	for _, by := range []bool{false, true} {
		spec := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: jk.Predictors, Resample: "jackknife", Vcov: true}
		want := jk.StdErrors
		if by {
			spec.JackknifeBy, want = "g", jk.BySE
		}
		res, err := fitMultinomialMod(t, spec, jk.Levels, recs)
		if err != nil {
			t.Fatalf("by=%v: %v", by, err)
		}
		if res.Resample != "jackknife" || (by && res.JackknifeGroups != jk.ByGroups) || (!by && res.JackknifeGroups != 0) {
			t.Errorf("by=%v: resample %q jackknife_groups %d", by, res.Resample, res.JackknifeGroups)
		}
		if len(res.StdErrors) != len(jk.Keys) || res.Vcov == nil || !slices.Equal(res.Vcov.RowKeys, jk.Keys) {
			t.Fatalf("by=%v: %d SEs, vcov %v; R keys %v", by, len(res.StdErrors), res.Vcov, jk.Keys)
		}
		for j, k := range jk.Keys {
			mnModNear(t, "coef["+k+"]", res.Coefficients[k], jk.Coefficients[j])
			mnModNear(t, "se["+k+"]", res.StdErrors[k], want[j])
			if p := waldZTwoSidedP(res.Coefficients[k], res.StdErrors[k]); res.PValues[k] != p {
				t.Errorf("p[%s] = %v, want the Wald z %v", k, res.PValues[k], p)
			}
			if v := res.Vcov.Values[j][j]; math.Abs(math.Sqrt(v)-res.StdErrors[k]) > 1e-15*math.Max(1, res.StdErrors[k]) {
				t.Errorf("√vcov[%s] %v ≠ se %v", k, math.Sqrt(v), res.StdErrors[k])
			}
		}
	}
}

// TestMultinomial_SelectionMatchesStepAIC: forward / backward / stepwise
// selection under AIC and BIC picks MASS::stepAIC's terms on multinom
// (forward and stepwise in entry order), each term entering or leaving
// across every category at once, and reports the final model's MLE and
// deviance at 1e-8.
func TestMultinomial_SelectionMatchesStepAIC(t *testing.T) {
	t.Parallel()
	sel := loadMultinomialModifiers(t).Selection
	recs := mnModRecords(sel.Rows, sel.Predictors, sel.Levels)
	for _, run := range sel.Runs {
		t.Run(run.Direction+"_"+run.Criterion, func(t *testing.T) {
			t.Parallel()
			spec := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: sel.Predictors, Selection: run.Direction, Criterion: run.Criterion}
			res, err := fitMultinomialMod(t, spec, sel.Levels, recs)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(res.SelectedFeatures, run.Selected) || res.Selection != run.Direction || res.Criterion != run.Criterion {
				t.Fatalf("selected %v (%s/%s), stepAIC %v", res.SelectedFeatures, res.Selection, res.Criterion, run.Selected)
			}
			if res.NObs != sel.NObs || len(res.Coefficients) != len(run.Keys) {
				t.Fatalf("n_obs %d, %d coefficients; R %d, %v", res.NObs, len(res.Coefficients), sel.NObs, run.Keys)
			}
			for j, k := range run.Keys {
				mnModNear(t, "coef["+k+"]", res.Coefficients[k], run.Coefficients[j])
			}
			mnModNear(t, "deviance", res.Deviance, run.Deviance[0])
		})
	}
}

// TestMultinomial_SelectionEdfCountsCategories: the criterion charges
// K − 1 parameters per term (D11). Swapping in a per-term charge of 1
// would move the BIC path, which the oracle pins; here the AIC of the
// selected fit is its deviance + 2·(K − 1)·(terms + 1).
func TestMultinomial_SelectionEdfCountsCategories(t *testing.T) {
	t.Parallel()
	sel := loadMultinomialModifiers(t).Selection
	recs := mnModRecords(sel.Rows, sel.Predictors, sel.Levels)
	spec := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: sel.Predictors, Selection: "forward", Criterion: "aic"}
	res, err := fitMultinomialMod(t, spec, sel.Levels, recs)
	if err != nil {
		t.Fatal(err)
	}
	k := float64((len(sel.Levels) - 1) * (len(res.SelectedFeatures) + 1))
	if math.Abs(res.AIC-(res.Deviance+2*k)) > 1e-9*res.AIC {
		t.Errorf("aic %v, want deviance %v + 2·%v", res.AIC, res.Deviance, k)
	}
}

// mnRareRows is the jackknife rows with a rare third category: every c
// row moves to b, then the keep b rows nearest the predictors' centroid
// (interior points, so no fit is separated) move to c, their cluster set
// to g.
func mnRareRows(rows [][]float64, p, keep int, g float64) [][]float64 {
	out := make([][]float64, len(rows))
	centre := make([]float64, p)
	for _, r := range rows {
		for j := 0; j < p; j++ {
			centre[j] += r[j] / float64(len(rows))
		}
	}
	dist := func(r []float64) float64 {
		d := 0.0
		for j := 0; j < p; j++ {
			d += (r[j] - centre[j]) * (r[j] - centre[j])
		}
		return d
	}
	var bs []int
	for i, r := range rows {
		out[i] = slices.Clone(r)
		if r[p] == 2 {
			out[i][p] = 1
		}
		if out[i][p] == 1 {
			bs = append(bs, i)
		}
	}
	slices.SortStableFunc(bs, func(a, b int) int {
		switch da, db := dist(rows[a]), dist(rows[b]); {
		case da < db:
			return -1
		case da > db:
			return 1
		}
		return 0
	})
	for _, i := range bs[:keep] {
		out[i][p], out[i][p+1] = 2, g
	}
	return out
}

// TestMultinomial_ReplicateCategoryLoss: every refit keeps the full
// fit's categories and reference (D5). A bootstrap replicate whose draw
// lost the rare category is skipped (succeeded < requested) and the
// result still names three categories; a jackknife replicate that loses
// it — the leave-one-out deletion of its only row, or a deleted cluster
// holding every row of it — fails with INSUFFICIENT_DATA reason
// replicate_empty_level naming the category (the grouped one also its
// jackknife_group).
func TestMultinomial_ReplicateCategoryLoss(t *testing.T) {
	t.Parallel()
	jk := loadMultinomialModifiers(t).Jackknife
	np := len(jk.Predictors)
	base := func() *types.RegressionSpec {
		return &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: jk.Predictors}
	}
	rare := mnModRecords(mnRareRows(jk.Rows, np, 3, 9), jk.Predictors, jk.Levels)
	one := mnModRecords(mnRareRows(jk.Rows, np, 1, 9), jk.Predictors, jk.Levels)

	t.Run("bootstrap_skips", func(t *testing.T) {
		t.Parallel()
		s := base()
		s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 60, 5
		res, err := fitMultinomialMod(t, s, jk.Levels, rare)
		if err != nil {
			t.Fatal(err)
		}
		if res.BootstrapRequested != 60 || res.BootstrapSucceeded >= 60 || res.BootstrapSucceeded < 10 {
			t.Fatalf("bootstrap %d requested, %d succeeded: want some skipped", res.BootstrapRequested, res.BootstrapSucceeded)
		}
		if !slices.Equal(res.Categories, jk.Levels) || res.Reference != "a" || len(res.StdErrors) != 6 {
			t.Errorf("categories %v reference %q, %d SEs", res.Categories, res.Reference, len(res.StdErrors))
		}
	})
	for _, tc := range []struct {
		name  string
		recs  []Record
		mut   func(*types.RegressionSpec)
		group bool
	}{
		{"jackknife_loo", one, func(s *types.RegressionSpec) { s.Resample = "jackknife" }, false},
		{"jackknife_by", rare, func(s *types.RegressionSpec) { s.Resample, s.JackknifeBy = "jackknife", "g" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := base()
			tc.mut(s)
			_, err := fitMultinomialMod(t, s, jk.Levels, tc.recs)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA ||
				ce.Details["reason"] != model.ReasonReplicateEmptyLevel || ce.Details["level"] != "c" ||
				!slices.Equal(ce.Details["levels"].([]string), jk.Levels) {
				t.Fatalf("err %v (details %v), want INSUFFICIENT_DATA reason %s level c", err, detailsOf(ce), model.ReasonReplicateEmptyLevel)
			}
			if _, has := ce.Details["jackknife_group"]; has != tc.group {
				t.Errorf("jackknife_group present %v, want %v: %v", has, tc.group, ce.Details)
			}
		})
	}
}

func detailsOf(ce *errors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}

// TestMultinomial_BootstrapSeedStable: a seeded bootstrap is bit-for-bit
// reproducible, every replicate refits, p-values are the percentile ones
// and the replicate covariance's root diagonal is the SE.
func TestMultinomial_BootstrapSeedStable(t *testing.T) {
	t.Parallel()
	jk := loadMultinomialModifiers(t).Jackknife
	recs := mnModRecords(jk.Rows, jk.Predictors, jk.Levels)
	var first []byte
	for range 2 {
		s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: jk.Predictors, Resample: "bootstrap", BootstrapIters: 40, RNGSeed: 11, Vcov: true}
		res, err := fitMultinomialMod(t, s, jk.Levels, recs)
		if err != nil {
			t.Fatal(err)
		}
		if res.BootstrapRequested != 40 || res.BootstrapSucceeded != 40 {
			t.Fatalf("bootstrap %d / %d", res.BootstrapRequested, res.BootstrapSucceeded)
		}
		for _, k := range jk.Keys {
			if p := res.PValues[k]; p <= 0 || p > 1 || p == waldZTwoSidedP(res.Coefficients[k], res.StdErrors[k]) {
				t.Errorf("p[%s] = %v: want the percentile p", k, p)
			}
		}
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
		} else if string(b) != string(first) {
			t.Errorf("the seeded bootstrap moved:\n%s\n%s", b, first)
		}
	}
}

// TestMultinomial_NoModifierUnchanged: without a modifier the engine's
// context path is the plain fit, byte for byte.
func TestMultinomial_NoModifierUnchanged(t *testing.T) {
	t.Parallel()
	jk := loadMultinomialModifiers(t).Jackknife
	recs := mnModRecords(jk.Rows, jk.Predictors, jk.Levels)
	s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: jk.Predictors, Vcov: true}
	a, err := newMultinomial(t, s, jk.Levels).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := fitMultinomialMod(t, s, jk.Levels, recs)
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	if string(ja) != string(jb) || a.Statistics != nil {
		t.Errorf("context fit differs:\n%s\n%s", ja, jb)
	}
}
