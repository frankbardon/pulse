package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regcov"
	"github.com/frankbardon/pulse/types"
)

// multinomial_oracle_test.go pins REG_MULTINOMIAL (multinomial.go) to R
// 4.6.1's nnet::multinom 7.3-21, Newton-polished to the exact MLE:
// multinomial.json (scripts/reference/gen_multinomial.R, make
// reference) — three levels (at 0.95 and 0.90 coverage), four levels in
// a non-alphabetical dictionary order with a named reference, a
// frequency-weighted case against the rep() expansion, a
// probability-weighted case (multinom(weights = w*), no likelihood) and
// a dictionary level no row carries (dropped, as multinom drops it).
// Every case's per-row category probabilities — of its training rows
// (fitted(multinom)) and of a grid of new rows (predict(multinom, type =
// "probs")), with each category's log-odds against the reference — are
// the bound fit's production scorer's (MultinomialFitted, U68 E1-S2),
// and the labelled grid's holdout proper scores (log_loss, brier,
// accuracy — mn_scores' closed forms) are HoldoutOutcomeFigures' over
// BoundProbabilities (U68 E1-S3).

// multinomialTol is the oracle tolerance: both sides are the MLE to
// double precision (R's Newton polish, Pulse's quadratic Newton stop),
// so 1e-10 relative holds with room; near-zero figures read an absolute
// floor of 1e-14.
const multinomialTol = 1e-10

type multinomialCase struct {
	Name                string      `json:"name"`
	Levels              []string    `json:"levels"`
	Categories          []string    `json:"categories"`
	Reference           string      `json:"reference"`
	ReferenceRequested  bool        `json:"reference_requested"`
	WeightKind          string      `json:"weight_kind"`
	ConfLevel           float64     `json:"conf_level"`
	Rows                [][]float64 `json:"rows"`
	Keys                []string    `json:"keys"`
	Coefficients        []float64   `json:"coefficients"`
	StdErrors           []float64   `json:"std_errors"`
	PValues             []float64   `json:"p_values"`
	CILower             []float64   `json:"ci_lower"`
	CIUpper             []float64   `json:"ci_upper"`
	Vcov                []float64   `json:"vcov"`
	Deviance            []float64   `json:"deviance"`
	NullDeviance        []float64   `json:"null_deviance"`
	TwoLogLik           []float64   `json:"two_log_lik"`
	AIC                 []float64   `json:"aic"`
	NObs                int         `json:"n_obs"`
	FittedProbabilities []float64   `json:"fitted_probabilities"`
	ScoreRows           [][]float64 `json:"score_rows"`
	ScoreProbabilities  []float64   `json:"score_probabilities"`
	ScoreLogOdds        []float64   `json:"score_log_odds"`
	// HoldoutRows / Holdout are labelled new rows [x1, x2, y(, w)] and
	// their weighted proper scores at the MLE (U68 E1-S3).
	HoldoutRows [][]float64 `json:"holdout_rows"`
	Holdout     struct {
		N        int       `json:"n"`
		LogLoss  []float64 `json:"log_loss"`
		Brier    []float64 `json:"brier"`
		Accuracy []float64 `json:"accuracy"`
	} `json:"holdout"`
	// Robust is the closed-form score sandwich per robust type (U68
	// E3-S1), the cluster CR1 over Clusters (one id per row).
	Robust struct {
		HC0       multinomialRobust `json:"hc0"`
		HC1       multinomialRobust `json:"hc1"`
		Cluster   multinomialRobust `json:"cluster"`
		Clusters  []float64         `json:"clusters"`
		NClusters int               `json:"n_clusters"`
	} `json:"robust"`
	// Exponentiated is the relative-risk-ratio block (U68 E3-S2) on the
	// classical standard errors; each Robust type carries its own.
	Exponentiated multinomialExpo `json:"exponentiated"`
}

// multinomialExpo is one exponentiated block, in the keys' order:
// exp(b), exp(b)·SE, exp(b ∓ z·SE).
type multinomialExpo struct {
	Estimates []float64 `json:"estimates"`
	StdErrors []float64 `json:"std_errors"`
	CILower   []float64 `json:"ci_lower"`
	CIUpper   []float64 `json:"ci_upper"`
}

// multinomialRobust is one robust type's figures, in the keys' order.
type multinomialRobust struct {
	Vcov      []float64 `json:"vcov"`
	StdErrors []float64 `json:"std_errors"`
	PValues   []float64 `json:"p_values"`
	CILower   []float64 `json:"ci_lower"`
	CIUpper   []float64 `json:"ci_upper"`

	Exponentiated multinomialExpo `json:"exponentiated"`
}

func loadMultinomial(t *testing.T) []multinomialCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "multinomial.json"))
	if err != nil {
		t.Fatalf("read multinomial.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("multinomial.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("multinomial.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []multinomialCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode multinomial.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["nnet"] != "7.3.21" || len(doc.Cases) != 6 {
		t.Fatalf("multinomial.json: R %q, nnet %q, %d cases", doc.RVersion, doc.Packages["nnet"], len(doc.Cases))
	}
	return doc.Cases
}

// categoricalSchema is a schema whose target is a categorical over
// levels (in that dictionary order) and whose other fields are f64.
func categoricalSchema(target string, levels []string, others ...string) *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, l := range levels {
		if _, err := dict.Add(l); err != nil {
			panic(err)
		}
	}
	s := schemaWithFields(others...)
	s.Fields = append(s.Fields, encoding.Field{Name: target, Type: encoding.FieldTypeCategoricalU8, Dictionary: dict})
	return s
}

func (c multinomialCase) records() []Record { return c.recordsOf(c.Rows) }

func (c multinomialCase) recordsOf(rows [][]float64) []Record {
	out := make([]Record, len(rows))
	for i, r := range rows {
		v := map[string]float64{"x1": r[0], "x2": r[1]}
		if c.WeightKind != "" {
			v["w"] = r[3]
		}
		v["y"] = r[2]
		out[i] = labeledRecord{mockRecord: mockRecord{values: v}, labels: map[string]string{"y": c.Levels[int(r[2])]}}
	}
	return out
}

func (c multinomialCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_MULTINOMIAL, Target: "y", Predictors: []string{"x1", "x2"}, Vcov: true}
	if c.ReferenceRequested {
		s.Reference = c.Reference
	}
	if c.ConfLevel != 0.95 {
		s.ConfLevel = c.ConfLevel
	}
	if c.WeightKind != "" {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKind(c.WeightKind)})
	}
	return s
}

func newMultinomial(t *testing.T, spec *types.RegressionSpec, levels []string) *multinomialEngine {
	t.Helper()
	eng, err := newMultinomialEngine(spec, categoricalSchema(spec.Target, levels, spec.Predictors...))
	if err != nil {
		t.Fatalf("newMultinomialEngine: %v", err)
	}
	return eng.(*multinomialEngine)
}

// TestReferenceOracle_MultinomialMatchesR: every case's coefficients,
// standard errors, Wald p-values, intervals, vcov (the inverse
// information), deviance, null deviance, two_log_lik / aic (absent under
// a probability weight) and — through the bound fit's scorer — every
// training row's fitted category probabilities, every new grid row's
// predicted ones and its log-odds against the reference agree with the
// Newton-polished nnet::multinom at 1e-10 relative; the categories,
// reference and n_obs exactly.
func TestReferenceOracle_MultinomialMatchesR(t *testing.T) {
	t.Parallel()

	maxErr := map[string]float64{}
	for _, c := range loadMultinomial(t) {
		t.Run(c.Name, func(t *testing.T) {
			eng := newMultinomial(t, c.spec(), c.Levels)
			res, err := eng.FitBuffered(c.records())
			if err != nil {
				t.Fatalf("FitBuffered: %v", err)
			}
			near := func(what string, got, want float64) {
				t.Helper()
				e := math.Abs(got-want) / math.Max(math.Abs(want), 1e-14/multinomialTol)
				key := what
				if i := strings.IndexAny(what, "[ "); i > 0 {
					key = what[:i]
				}
				maxErr[key] = math.Max(maxErr[key], e)
				if e > multinomialTol {
					t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
				}
			}
			if res.Reference != c.Reference || res.NObs != c.NObs {
				t.Fatalf("reference %q n_obs %d, R %q %d", res.Reference, res.NObs, c.Reference, c.NObs)
			}
			// Pulse lists the fitted levels in dictionary order; R lists
			// the reference first.
			wantCats := make([]string, 0, len(c.Categories))
			for _, l := range c.Levels {
				if slices.Contains(c.Categories, l) {
					wantCats = append(wantCats, l)
				}
			}
			if !slices.Equal(res.Categories, wantCats) {
				t.Fatalf("categories %v, want %v", res.Categories, wantCats)
			}
			if len(res.Coefficients) != len(c.Keys) {
				t.Fatalf("%d coefficients, R %d", len(res.Coefficients), len(c.Keys))
			}
			for j, k := range c.Keys {
				near("coef["+k+"]", res.Coefficients[k], c.Coefficients[j])
				near("se["+k+"]", res.StdErrors[k], c.StdErrors[j])
				near("p["+k+"]", res.PValues[k], c.PValues[j])
				near("ci_lower["+k+"]", res.CILower[k], c.CILower[j])
				near("ci_upper["+k+"]", res.CIUpper[k], c.CIUpper[j])
			}
			if res.ConfLevel != c.ConfLevel || res.DF != 0 {
				t.Errorf("conf_level %v df %v, want %v and Wald normal", res.ConfLevel, res.DF, c.ConfLevel)
			}
			if res.Vcov == nil || !slices.Equal(res.Vcov.RowKeys, c.Keys) {
				t.Fatalf("vcov keys %v, R %v", res.Vcov, c.Keys)
			}
			d := len(c.Keys)
			for i := 0; i < d; i++ {
				for j := 0; j < d; j++ {
					near("vcov", res.Vcov.Values[i][j], c.Vcov[i*d+j])
				}
			}
			near("deviance", res.Deviance, c.Deviance[0])
			near("null_deviance", res.NullDeviance, c.NullDeviance[0])
			near("pseudo_r2", res.PseudoR2, 1-c.Deviance[0]/c.NullDeviance[0])
			if c.WeightKind == "probability" {
				if res.TwoLogLik != 0 || res.AIC != 0 {
					t.Errorf("probability-weighted fit reports two_log_lik %v aic %v; want absent", res.TwoLogLik, res.AIC)
				}
			} else {
				near("two_log_lik", res.TwoLogLik, c.TwoLogLik[0])
				near("aic", res.AIC, c.AIC[0])
			}
			L := len(c.Categories)
			if len(c.FittedProbabilities) != len(c.Rows)*L || len(c.ScoreProbabilities) != len(c.ScoreRows)*L || len(c.ScoreLogOdds) != len(c.ScoreRows)*L || len(c.ScoreRows) == 0 {
				t.Fatalf("golden per-row figures malformed: %d fitted, %d score rows", len(c.FittedProbabilities), len(c.ScoreRows))
			}
			f := multinomialFitted(t, c.spec(), res)
			if !slices.Equal(f.Outcomes(), res.Categories) {
				t.Fatalf("scorer outcomes %v, fit categories %v", f.Outcomes(), res.Categories)
			}
			pi := make([]float64, L)
			for i, rec := range c.records() {
				if !f.Probabilities(rec, pi) {
					t.Fatalf("training row %d scored null", i)
				}
				for rc, cat := range c.Categories {
					near("fitted", pi[slices.Index(res.Categories, cat)], c.FittedProbabilities[i*L+rc])
				}
			}
			for i, r := range c.ScoreRows {
				row := mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1]}}
				if !f.Probabilities(row, pi) {
					t.Fatalf("score row %d scored null", i)
				}
				for rc, cat := range c.Categories {
					k := slices.Index(res.Categories, cat)
					near("score_probability", pi[k], c.ScoreProbabilities[i*L+rc])
					lo, ok := f.LogOdds(row, k)
					if !ok {
						t.Fatalf("score row %d log-odds null", i)
					}
					near("score_log_odds", lo, c.ScoreLogOdds[i*L+rc])
				}
			}
			// Holdout: the held-out rows' proper scores through the
			// engine's probability-vector scorer (BoundProbabilities).
			b, err := model.Bind("m", c.spec(), res)
			if err != nil {
				t.Fatal(err)
			}
			ps, err := BoundProbabilities(b)
			if err != nil {
				t.Fatal(err)
			}
			hs := c.spec()
			hs.Holdout = "held"
			h, err := HoldoutOutcomeFigures(hs, ps, false, c.recordsOf(c.HoldoutRows), 0)
			if err != nil {
				t.Fatalf("HoldoutOutcomeFigures: %v", err)
			}
			if h.N != c.Holdout.N || h.RPS != nil || h.RMSE != nil {
				t.Fatalf("holdout %+v: want n %d (of %d held-out rows), no rps / rmse", *h, c.Holdout.N, len(c.HoldoutRows))
			}
			near("holdout_log_loss", *h.LogLoss, c.Holdout.LogLoss[0])
			near("holdout_brier", *h.Brier, c.Holdout.Brier[0])
			if *h.Accuracy != c.Holdout.Accuracy[0] {
				t.Errorf("holdout accuracy = %v, R %v", *h.Accuracy, c.Holdout.Accuracy[0])
			}
		})
	}
	t.Logf("max relative error: %v", maxErr)
}

// multinomialFitted is res bound under spec, rebuilt as its scorer.
func multinomialFitted(t *testing.T, spec *types.RegressionSpec, res *types.RegressionResult) *MultinomialFitted {
	t.Helper()
	b, err := model.Bind("m", spec, res)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	f, err := NewMultinomialFitted(b)
	if err != nil {
		t.Fatalf("NewMultinomialFitted: %v", err)
	}
	return f
}

// TestReferenceOracle_MultinomialRobustMatchesR (U68 E3-S1): under
// covariance hc0, hc1 and cluster every case — unweighted, frequency-
// and probability-weighted — keeps its classical coefficients and
// reports the closed-form score sandwich V = B·M·B of gen_multinomial.R
// (B the inverse information, the unit score (y − π) ⊗ x over the
// non-reference blocks, w* in the meat, hc1 by N*/(N* − d), CR1 by
// G/(G − 1)·(N* − 1)/(N* − d)) in its std errors, Wald z p-values,
// normal intervals and vcov at 1e-10 relative, √diag(vcov) == std
// errors bit for bit, and the covariance echo naming the type (and the
// cluster field and its count).
func TestReferenceOracle_MultinomialRobustMatchesR(t *testing.T) {
	t.Parallel()

	maxErr := 0.0
	for _, c := range loadMultinomial(t) {
		if len(c.Robust.Clusters) != len(c.Rows) || c.Robust.NClusters < 2 {
			t.Fatalf("%s: golden robust block malformed (%d clusters for %d rows)", c.Name, len(c.Robust.Clusters), len(c.Rows))
		}
		recs := c.records()
		for i, rec := range recs {
			lr := rec.(labeledRecord)
			lr.values["g"] = c.Robust.Clusters[i]
		}
		for _, typ := range []struct {
			cov  types.CovarianceType
			want multinomialRobust
		}{
			{types.CovarianceHC0, c.Robust.HC0},
			{types.CovarianceHC1, c.Robust.HC1},
			{types.CovarianceCluster, c.Robust.Cluster},
		} {
			t.Run(c.Name+"/"+string(typ.cov), func(t *testing.T) {
				spec := c.spec()
				spec.Covariance = &types.RegressionCovariance{Type: typ.cov}
				if typ.cov == types.CovarianceCluster {
					spec.Covariance.Cluster = "g"
				}
				levels := c.Levels
				eng, err := newMultinomialEngine(spec, categoricalSchema("y", levels, "x1", "x2", "w", "g"))
				if err != nil {
					t.Fatalf("newMultinomialEngine: %v", err)
				}
				res, err := eng.(*multinomialEngine).FitBuffered(recs)
				if err != nil {
					t.Fatalf("FitBuffered: %v", err)
				}
				near := func(what string, got, want float64) {
					t.Helper()
					e := math.Abs(got-want) / math.Max(math.Abs(want), 1e-14/multinomialTol)
					maxErr = math.Max(maxErr, e)
					if e > multinomialTol {
						t.Errorf("%s = %.17g, R %.17g (rel %.3g)", what, got, want, e)
					}
				}
				d := len(c.Keys)
				if res.Vcov == nil || !slices.Equal(res.Vcov.RowKeys, c.Keys) {
					t.Fatalf("vcov keys %v, R %v", res.Vcov, c.Keys)
				}
				for j, k := range c.Keys {
					near("coef["+k+"]", res.Coefficients[k], c.Coefficients[j])
					near("se["+k+"]", res.StdErrors[k], typ.want.StdErrors[j])
					near("p["+k+"]", res.PValues[k], typ.want.PValues[j])
					near("ci_lower["+k+"]", res.CILower[k], typ.want.CILower[j])
					near("ci_upper["+k+"]", res.CIUpper[k], typ.want.CIUpper[j])
					if math.Sqrt(res.Vcov.Values[j][j]) != res.StdErrors[k] {
						t.Errorf("√vcov[%s] %v != std error %v", k, math.Sqrt(res.Vcov.Values[j][j]), res.StdErrors[k])
					}
					for i := 0; i < d; i++ {
						near("vcov", res.Vcov.Values[j][i], typ.want.Vcov[j*d+i])
					}
				}
				if res.DF != 0 || res.ConfLevel != c.ConfLevel {
					t.Errorf("df %v conf_level %v: want Wald normal at %v", res.DF, res.ConfLevel, c.ConfLevel)
				}
				cv := res.Covariance
				if cv == nil || cv.Type != typ.cov {
					t.Fatalf("covariance echo %+v, want type %s", cv, typ.cov)
				}
				if typ.cov == types.CovarianceCluster && (cv.Cluster != "g" || cv.Clusters != c.Robust.NClusters) {
					t.Errorf("cluster echo %+v, want g over %d clusters", cv, c.Robust.NClusters)
				}
			})
		}
	}
	t.Logf("max relative error: %v", maxErr)
}

// TestMultinomial_RobustRows (U68 E3-S1): a row whose cluster is null
// drops listwise (the fit equals the one without it); rows spanning one
// cluster fail with VCOV_UNSUPPORTED reason clusters; a classical
// covariance block is the absent one byte for byte, with no echo.
func TestMultinomial_RobustRows(t *testing.T) {
	t.Parallel()

	c := loadMultinomial(t)[0]
	recs := c.records()
	for i, rec := range recs {
		rec.(labeledRecord).values["g"] = c.Robust.Clusters[i]
	}
	fit := func(cov *types.RegressionCovariance, rows []Record) (*types.RegressionResult, error) {
		spec := c.spec()
		spec.Covariance = cov
		eng, err := newMultinomialEngine(spec, categoricalSchema("y", c.Levels, "x1", "x2", "g"))
		if err != nil {
			t.Fatalf("newMultinomialEngine: %v", err)
		}
		return eng.(*multinomialEngine).FitBuffered(rows)
	}
	cl := &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "g"}
	base, err := fit(cl, recs)
	if err != nil {
		t.Fatal(err)
	}
	extra := labeledRecord{mockRecord: mockRecord{values: map[string]float64{"x1": 9, "x2": -9, "y": 0}}, labels: map[string]string{"y": c.Levels[0]}}
	withNull, err := fit(cl, append(slices.Clone(recs), extra))
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(withNull)
	if string(a) != string(b) || withNull.NObs != len(recs) {
		t.Errorf("a null-cluster row moved the fit (n_obs %d, want %d)", withNull.NObs, len(recs))
	}

	one := make([]Record, len(recs))
	for i, rec := range recs {
		lr := rec.(labeledRecord)
		v := maps.Clone(lr.values)
		v["g"] = 1
		one[i] = labeledRecord{mockRecord: mockRecord{values: v}, labels: lr.labels}
	}
	_, err = fit(cl, one)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_REGRESSION_VCOV_UNSUPPORTED || ce.Details["reason"] != regcov.ReasonClusters {
		t.Errorf("one cluster: %v, want VCOV_UNSUPPORTED reason clusters", err)
	}

	absent, err := fit(nil, recs)
	if err != nil {
		t.Fatal(err)
	}
	classical, err := fit(&types.RegressionCovariance{Type: types.CovarianceClassical}, recs)
	if err != nil {
		t.Fatal(err)
	}
	a, _ = json.Marshal(absent)
	b, _ = json.Marshal(classical)
	if string(a) != string(b) || classical.Covariance != nil {
		t.Errorf("classical differs from absent:\n%s\nvs\n%s", b, a)
	}
}
