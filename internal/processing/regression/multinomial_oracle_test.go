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

	"github.com/frankbardon/pulse/encoding"
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

func (c multinomialCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		v := map[string]float64{"x1": r[0], "x2": r[1]}
		if c.WeightKind != "" {
			v["w"] = r[3]
		}
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
// a probability weight) and every row's fitted category probabilities
// agree with the Newton-polished nnet::multinom at 1e-10 relative; the
// categories, reference and n_obs exactly.
func TestReferenceOracle_MultinomialMatchesR(t *testing.T) {
	t.Parallel()

	maxErr := map[string]float64{}
	for _, c := range loadMultinomial(t) {
		t.Run(c.Name, func(t *testing.T) {
			eng := newMultinomial(t, c.spec(), c.Levels)
			var gotCats []string
			var probs []float64
			eng.fitted = func(cats []string, p []float64) { gotCats, probs = cats, p }
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
			if len(probs) != len(c.Rows)*L {
				t.Fatalf("%d fitted probabilities, want %d", len(probs), len(c.Rows)*L)
			}
			for i := range c.Rows {
				for rc, cat := range c.Categories {
					near("fitted", probs[i*L+slices.Index(gotCats, cat)], c.FittedProbabilities[i*L+rc])
				}
			}
		})
	}
	t.Logf("max relative error: %v", maxErr)
}
