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

	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// oracle_test.go pins the fit's arithmetic to R 4.6.1 (MASS 7.3-66 polr,
// brant 0.3.0, sandwich 3.1-3, ordinal 2026.7-26 nominal_test):
// scripts/reference/gen_ordinal.R →
// internal/processing/testdata/reference/ordinal.json. The golden is the
// MLE (polr polished by Newton on the analytic derivatives), so the
// comparison is held at oracleRel = 1e-10 relative; the measured worst
// is reported by the test log (see TestOrdinalOracle_Design).

const oracleRel = 1e-10

// oracleCase is one ordinal.json case.
type oracleCase struct {
	Name                string       `json:"name"`
	Link                string       `json:"link"`
	Weight              string       `json:"weight"`
	Covariance          string       `json:"covariance"`
	Predictors          []string     `json:"predictors"`
	Levels              []string     `json:"levels"`
	LevelsParam         []string     `json:"levels_param"`
	NObs                int          `json:"n_obs"`
	Coefficients        []float64    `json:"coefficients"`
	StdErrors           []float64    `json:"std_errors"`
	Vcov                [][]float64  `json:"vcov"`
	Deviance            float64      `json:"deviance"`
	NullDeviance        float64      `json:"null_deviance"`
	TwoLogLik           *float64     `json:"two_log_lik"`
	AIC                 *float64     `json:"aic"`
	FittedProbabilities []float64    `json:"fitted_probabilities"`
	Brant               *oracleBrant `json:"brant"`
	// NominalTest is the likelihood-ratio test against the partial
	// proportional-odds fit (U68 E6-S1, ordinal::nominal_test).
	NominalTest *oracleNominal `json:"nominal_test"`
	Rows        [][]float64    `json:"rows"`
	// RowEta / RowProbabilities are the per-row scores at the MLE (x'β
	// and predict(polr, type = "probs")), on the classical cases.
	RowEta           []float64   `json:"row_eta"`
	RowProbabilities [][]float64 `json:"row_probabilities"`
	// HoldoutRows / Holdout are new rows [x..., y, w, g] and their
	// weighted proper scores at the MLE (U68 E1-S3), on the classical
	// cases without params.levels.
	HoldoutRows [][]float64    `json:"holdout_rows"`
	Holdout     *oracleHoldout `json:"holdout"`
	// Exponentiated is the predictors' cumulative-odds-ratio block (U68
	// E3-S2) on the case's covariance, in the predictors' order.
	Exponentiated *oracleExpo `json:"exponentiated"`
}

// oracleExpo is exp(beta), exp(beta)·SE and exp(beta ∓ z·SE).
type oracleExpo struct {
	Estimates []float64 `json:"estimates"`
	StdErrors []float64 `json:"std_errors"`
	CILower   []float64 `json:"ci_lower"`
	CIUpper   []float64 `json:"ci_upper"`
}

// oracleHoldout is a case's holdout figures (ord_scores).
type oracleHoldout struct {
	N        int     `json:"n"`
	LogLoss  float64 `json:"log_loss"`
	Brier    float64 `json:"brier"`
	Accuracy float64 `json:"accuracy"`
	RPS      float64 `json:"rps"`
}

type oracleBrant struct {
	Statistic float64   `json:"statistic"`
	DF        float64   `json:"df"`
	PValue    float64   `json:"p_value"`
	Terms     []float64 `json:"terms"`
	TermsDF   float64   `json:"terms_df"`
	TermsP    []float64 `json:"terms_p"`
}

// oracleNominal is a case's nominal test: a null figure (a partial fit
// that did not converge or crosses) is nil, named in Failed.
type oracleNominal struct {
	Statistic *float64   `json:"statistic"`
	DF        float64    `json:"df"`
	PValue    *float64   `json:"p_value"`
	Terms     []*float64 `json:"terms"`
	TermsDF   float64    `json:"terms_df"`
	TermsP    []*float64 `json:"terms_p"`
	Failed    []struct {
		Scope  string `json:"scope"`
		Term   string `json:"term"`
		Reason string `json:"reason"`
	} `json:"failed"`
}

// nominalReasons maps the golden's failure reasons to the warning's.
var nominalReasons = map[string]string{"no_converge": ReasonNominalNoConverge, "crossing": ReasonNominalCrossing}

// nominalRel is the nominal test's tolerance, relative with a floor of
// 1: the statistic is a difference of two log-likelihoods, so its
// absolute error scales with |ℓ|, not with the (often small) statistic.
const nominalRel = 1e-10

// nominalErr is |got − want| / max(|want|, 1), +Inf when exactly one of
// them is undefined (want nil).
func nominalErr(got float64, want *float64) float64 {
	if want == nil {
		if math.IsNaN(got) {
			return 0
		}
		return math.Inf(1)
	}
	if math.IsNaN(got) {
		return math.Inf(1)
	}
	return math.Abs(got-*want) / math.Max(math.Abs(*want), 1)
}

// oracleModifiers is ordinal.json's resample / selection block (U68
// E2-S1): the manual-deletion jackknife on polr and MASS::stepAIC.
type oracleModifiers struct {
	Jackknife struct {
		Predictors   []string    `json:"predictors"`
		Levels       []string    `json:"levels"`
		Coefficients []float64   `json:"coefficients"`
		StdErrors    []float64   `json:"std_errors"`
		ByGroups     int         `json:"by_groups"`
		ByStdErrors  []float64   `json:"by_std_errors"`
		Rows         [][]float64 `json:"rows"`
	} `json:"jackknife"`
	Selection struct {
		Predictors []string    `json:"predictors"`
		Levels     []string    `json:"levels"`
		NObs       int         `json:"n_obs"`
		Rows       [][]float64 `json:"rows"`
		Runs       []struct {
			Direction    string    `json:"direction"`
			Criterion    string    `json:"criterion"`
			Selected     []string  `json:"selected"`
			Steps        []string  `json:"steps"`
			Coefficients []float64 `json:"coefficients"`
			Deviance     float64   `json:"deviance"`
		} `json:"runs"`
	} `json:"selection"`
}

// loadOracle reads the committed golden, checking its hash footer and
// its recorded versions.
func loadOracle(t *testing.T) []oracleCase {
	t.Helper()
	cases, _ := loadOracleDoc(t)
	return cases
}

// loadModifiers reads the golden's modifiers block.
func loadModifiers(t *testing.T) oracleModifiers {
	t.Helper()
	_, m := loadOracleDoc(t)
	if len(m.Jackknife.Rows) == 0 || len(m.Selection.Runs) != 6 {
		t.Fatalf("ordinal.json modifiers: %d jackknife rows, %d selection runs", len(m.Jackknife.Rows), len(m.Selection.Runs))
	}
	return m
}

// loadOracleDoc reads the committed golden's cases and modifiers.
func loadOracleDoc(t *testing.T) ([]oracleCase, oracleModifiers) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "processing", "testdata", "reference", "ordinal.json"))
	if err != nil {
		t.Fatalf("read ordinal.json: %v (regenerate with scripts/reference/gen_ordinal.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("ordinal.json has no golden-hash footer")
	}
	body := raw[:i]
	sum := sha256.Sum256(body)
	if got := strings.TrimSpace(s[i+len("\n// golden-hash: "):]); got != hex.EncodeToString(sum[:]) {
		t.Fatal("ordinal.json hash mismatch (hand-edited? regenerate it)")
	}
	var doc struct {
		RVersion  string            `json:"r_version"`
		Packages  map[string]string `json:"packages"`
		Cases     []oracleCase      `json:"cases"`
		Modifiers oracleModifiers   `json:"modifiers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.RVersion != "R version 4.6.1 (2026-06-24)" || doc.Packages["MASS"] != "7.3.66" || doc.Packages["brant"] != "0.3.0" || doc.Packages["sandwich"] != "3.1.3" || doc.Packages["ordinal"] != "2026.7.26" {
		t.Fatalf("ordinal.json records R %q, packages %v", doc.RVersion, doc.Packages)
	}
	if len(doc.Cases) < 10 {
		t.Fatalf("ordinal.json has %d cases", len(doc.Cases))
	}
	return doc.Cases, doc.Modifiers
}

// caseDesign builds the design the golden's rows describe (rows
// [x..., y, w, g], y 1-based), with the case's prior weights w*.
func caseDesign(c oracleCase) (*design, []string) {
	p := len(c.Predictors)
	d := &design{p: p, k: len(c.Levels)}
	clusters := make([]string, len(c.Rows))
	sumW, sumW2 := 0.0, 0.0
	for i, r := range c.Rows {
		d.x = append(d.x, append([]float64(nil), r[:p]...))
		d.y = append(d.y, int(r[p])-1)
		d.w = append(d.w, r[p+1])
		sumW += r[p+1]
		sumW2 += r[p+1] * r[p+1]
		clusters[i] = string(rune('a' + int(r[p+2])))
	}
	if c.Weight == "probability" {
		nStar := sumW * sumW / sumW2
		for i := range d.w {
			d.w[i] = float64(d.w[i] * (nStar / sumW))
		}
	}
	return d, clusters
}

func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Max(math.Abs(want), 1e-12)
}

// TestOrdinalOracle_Design fits every golden case through the design
// (no engine): coefficients, standard errors, vcov, deviances, 2ℓ, AIC,
// fitted probabilities and the Brant statistics against R.
func TestOrdinalOracle_Design(t *testing.T) {
	t.Parallel()
	worst := map[string]float64{}
	note := func(what string, e float64) {
		if e > worst[what] {
			worst[what] = e
		}
	}
	for _, c := range loadOracle(t) {
		t.Run(c.Name, func(t *testing.T) {
			d, clusters := caseDesign(c)
			l := links[c.Link]
			sol, fail := d.newton(l)
			if fail != nil {
				t.Fatalf("fit failed: %+v", fail)
			}
			inv, err := linalg.InverseSPD(sol.info)
			if err != nil {
				t.Fatal(err)
			}
			vcov := inv.ToRows()
			if c.Covariance != "classical" {
				nStar := 0.0
				for _, w := range d.w {
					nStar += w
				}
				m := &model{spec: &types.RegressionSpec{Type: types.REG_ORDINAL}, link: l, cov: types.CovarianceType(c.Covariance)}
				if vcov, err = m.robust(d, sol.theta, vcov, clusters, nStar); err != nil {
					t.Fatal(err)
				}
			}
			check := func(what string, got, want float64) {
				t.Helper()
				e := relErr(got, want)
				note(what, e)
				if e > oracleRel {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
				}
			}
			for j, want := range c.Coefficients {
				check("coefficient", sol.theta[j], want)
				check("std_error", math.Sqrt(vcov[j][j]), c.StdErrors[j])
				for k := range c.Vcov[j] {
					check("vcov", vcov[j][k], c.Vcov[j][k])
				}
			}
			check("deviance", -2*sol.ll, c.Deviance)
			check("null_deviance", -2*d.nullLoglik(), c.NullDeviance)
			if c.TwoLogLik != nil {
				check("two_log_lik", 2*sol.ll, *c.TwoLogLik)
				check("aic", -2*sol.ll+2*float64(d.q()), *c.AIC)
			}
			for k, p := range d.fittedProbabilities(l, sol.theta) {
				check("fitted_probability", p, c.FittedProbabilities[k])
			}
			if c.Brant != nil {
				b := d.brant()
				check("brant_statistic", b.stat, c.Brant.Statistic)
				check("brant_p", b.p, c.Brant.PValue)
				if b.df != c.Brant.DF {
					t.Errorf("brant df = %v, R %v", b.df, c.Brant.DF)
				}
				for a, tm := range b.terms {
					check("brant_term_statistic", tm.stat, c.Brant.Terms[a])
					check("brant_term_p", tm.p, c.Brant.TermsP[a])
					if tm.df != c.Brant.TermsDF {
						t.Errorf("brant term df = %v, R %v", tm.df, c.Brant.TermsDF)
					}
				}
			}
			if c.NominalTest != nil {
				nt := d.nominalTest(l, sol)
				checkNominal(t, nt, *c.NominalTest, c.Predictors, note)
			}
		})
	}
	t.Logf("max relative error vs R: %v", worst)
}

// checkNominal compares a nominal test with its golden: every figure
// (null where R's partial fit failed) and the failures, terms first.
func checkNominal(t *testing.T, nt nominalResult, want oracleNominal, preds []string, note func(string, float64)) {
	t.Helper()
	check := func(what string, got float64, want *float64) {
		t.Helper()
		e := nominalErr(got, want)
		if !math.IsInf(e, 1) {
			note(strings.Fields(what)[0], e)
		}
		if !(e <= nominalRel) {
			t.Errorf("%s = %.17g, R %v (err %.3g)", what, got, fmtPtr(want), e)
		}
	}
	check("nominal_statistic", nt.omnibus.stat, want.Statistic)
	check("nominal_p", nt.omnibus.p, want.PValue)
	if nt.omnibus.df != want.DF {
		t.Errorf("nominal df = %v, R %v", nt.omnibus.df, want.DF)
	}
	if len(nt.terms) != len(want.Terms) {
		t.Fatalf("nominal terms %d, R %d", len(nt.terms), len(want.Terms))
	}
	for a, tm := range nt.terms {
		check("nominal_term_statistic "+preds[a], tm.stat, want.Terms[a])
		check("nominal_term_p "+preds[a], tm.p, want.TermsP[a])
		if tm.df != want.TermsDF {
			t.Errorf("nominal term df = %v, R %v", tm.df, want.TermsDF)
		}
	}
	if len(nt.failures) != len(want.Failed) {
		t.Fatalf("nominal failures %+v, R %+v", nt.failures, want.Failed)
	}
	for i, f := range nt.failures {
		w := want.Failed[i]
		term := ""
		if f.term >= 0 {
			term = preds[f.term]
		}
		scope := map[bool]string{true: "term", false: "omnibus"}[f.term >= 0]
		if scope != w.Scope || term != w.Term || f.reason != nominalReasons[w.Reason] {
			t.Errorf("nominal failure %d = %s %q %s, R %s %q %s", i, scope, term, f.reason, w.Scope, w.Term, w.Reason)
		}
	}
}

func fmtPtr(v *float64) any {
	if v == nil {
		return "null"
	}
	return *v
}
