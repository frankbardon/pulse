package regression

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// vif_oracle_test.go pins the collinearity block (vif.go) to car 3.1-5's
// vif() on R 4.6.1: vif.json (scripts/reference/gen_vif.R, make
// reference) — lm and the binomial-logit, poisson-log, fixed-θ negative
// binomial and gamma-log glm, ungrouped and with a three-level factor's
// dummies as one term (the generalized VIF), on the model basis (car on
// the fit) and the predictors basis (car on lm over the prior weights),
// unweighted, frequency-weighted (= the rep() expansion) and
// probability-weighted.

type vifCase struct {
	Name       string      `json:"name"`
	Family     string      `json:"family"`
	Link       string      `json:"link"`
	Theta      float64     `json:"theta"`
	Basis      string      `json:"basis"`
	Weight     string      `json:"weight"`
	Grouped    bool        `json:"grouped"`
	Predictors []string    `json:"predictors"`
	Terms      []string    `json:"terms"`
	Rows       [][]float64 `json:"rows"`
	GVIF       []float64   `json:"gvif"`
	DF         []float64   `json:"df"`
	GVIFScaled []float64   `json:"gvif_scaled"`
}

func loadVIF(t *testing.T) []vifCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "vif.json"))
	if err != nil {
		t.Fatalf("read vif.json: %v (regenerate with `make reference`)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("vif.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("vif.json: hash mismatch (hand-edited?); regenerate with `make reference`")
	}
	var doc struct {
		RVersion string            `json:"r_version"`
		Packages map[string]string `json:"packages"`
		Cases    []vifCase         `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode vif.json: %v", err)
	}
	if !strings.HasPrefix(doc.RVersion, "R version 4.6.1") || doc.Packages["car"] != "3.1.5" || len(doc.Cases) != 12 {
		t.Fatalf("vif.json: R %q, car %q, %d cases", doc.RVersion, doc.Packages["car"], len(doc.Cases))
	}
	return doc.Cases
}

func (c vifCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "x3": r[2], "gb": r[3], "gc": r[4], "y": r[5], "w": r[6]}}
	}
	return out
}

func (c vifCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: c.Predictors,
		Collinearity: &types.RegressionCollinearity{Basis: c.Basis}}
	if c.Grouped {
		s.Collinearity.Groups = []types.RegressionTermGroup{{Name: "g", Predictors: []string{"gb", "gc"}}}
	}
	if c.Family != "gaussian" {
		s.Type, s.Family, s.Link, s.Theta, s.Tol, s.MaxIters = types.REG_GLM, c.Family, c.Link, c.Theta, 1e-13, 200
	}
	switch c.Weight {
	case "frequency":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	case "probability":
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
	}
	return s
}

func (c vifCase) fit(t *testing.T) *types.RegressionResult {
	t.Helper()
	var eng BufferedEngine
	if c.Family == "gaussian" {
		eng = newOLS(t, c.spec())
	} else {
		eng = asBuffered(t, newGLM(t, c.spec()))
	}
	res, err := eng.FitBuffered(c.records())
	if err != nil {
		t.Fatalf("%s: FitBuffered: %v", c.Name, err)
	}
	if res.Collinearity == nil {
		t.Fatalf("%s: no collinearity block", c.Name)
	}
	return res
}

// TestReferenceOracle_VIFMatchesCar: every case's VIF (the generalized
// VIF on a grouped term), tolerance (1/VIF), df and — where a group is
// declared — GVIF^(1/(2·df)) agree with car::vif at 1e-10 relative, the
// terms in car's order; the frequency-weighted cases equal the
// rep()-expanded fit (asserted in R), the probability-weighted ones the
// weighted fit (scale-free).
func TestReferenceOracle_VIFMatchesCar(t *testing.T) {
	t.Parallel()

	overall := worst{}
	for _, c := range loadVIF(t) {
		res := c.fit(t)
		col := res.Collinearity
		if col.Basis != c.Basis || strings.Join(col.Terms, ",") != strings.Join(c.Terms, ",") {
			t.Errorf("%s: basis %q terms %v, want %q %v", c.Name, col.Basis, col.Terms, c.Basis, c.Terms)
			continue
		}
		if (col.GVIFScaled != nil) != c.Grouped {
			t.Errorf("%s: gvif_scaled present = %v, grouped = %v", c.Name, col.GVIFScaled != nil, c.Grouped)
		}
		for i, term := range c.Terms {
			if float64(col.DF[term]) != c.DF[i] {
				t.Errorf("%s: df[%s] = %d, car %v", c.Name, term, col.DF[term], c.DF[i])
			}
			for _, f := range []struct {
				what      string
				got, want float64
			}{
				{"vif", col.VIF[term], c.GVIF[i]},
				{"tolerance", col.Tolerance[term], 1 / c.GVIF[i]},
				{"gvif_scaled", col.GVIFScaled[term], c.GVIFScaled[i]},
			} {
				if f.what == "gvif_scaled" && !c.Grouped {
					continue
				}
				e := math.Abs(f.got-f.want) / math.Abs(f.want)
				overall[f.what] = math.Max(overall[f.what], e)
				if !(e <= 1e-10) {
					t.Errorf("%s: %s[%s] = %.17g, car %.17g (rel %.3g)", c.Name, f.what, term, f.got, f.want, e)
				}
			}
		}
	}
	for k, e := range overall {
		t.Logf("max relative error %s: %.3g", k, e)
	}
}
