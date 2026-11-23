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

// marginsResampleCase is one margins_resample.json case
// (scripts/reference/gen_margins_resample.R): a manual base-R replicate
// loop of the effects themselves.
type marginsResampleCase struct {
	Name     string `json:"name"`
	Model    string `json:"model"`
	Family   string `json:"family"`
	Link     string `json:"link"`
	Type     string `json:"type"`
	Binary   string `json:"binary"`
	Resample string `json:"resample"`
	By       bool   `json:"jackknife_by"`
	Weighted bool   `json:"weighted"`
	// Samples are a bootstrap case's index sets (0-based rows).
	Samples       [][]int     `json:"samples"`
	Rows          [][]float64 `json:"rows"`
	Coefficients  []float64   `json:"coefficients"`
	CoefStdErrors []float64   `json:"coef_std_errors"`
	Estimates     []float64   `json:"estimates"`
	StdErrors     []float64   `json:"std_errors"`
	PValues       []float64   `json:"p_values"`
	CILower       []float64   `json:"ci_lower"`
	CIUpper       []float64   `json:"ci_upper"`
}

// loadMarginsResample reads margins_resample.json, checking its
// golden-hash footer.
func loadMarginsResample(t *testing.T) []marginsResampleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "margins_resample.json"))
	if err != nil {
		t.Fatalf("read margins_resample.json: %v (regenerate with scripts/reference/gen_margins_resample.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("margins_resample.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("margins_resample.json: hash mismatch (hand-edited?); regenerate it")
	}
	var doc struct {
		Packages map[string]string     `json:"packages"`
		Cases    []marginsResampleCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode margins_resample.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || len(doc.Cases) != 13 {
		t.Fatalf("margins_resample.json: R %q, %d cases", doc.Packages["R"], len(doc.Cases))
	}
	return doc.Cases
}

func (c marginsResampleCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "y": r[2], "g": r[3], "f": r[4]}}
	}
	return out
}

// spec is the case's regression: margins beside its resample (the
// bootstrap's index sets are driven by hand, so its spec is unresampled).
func (c marginsResampleCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Name: c.Name, Type: types.REG_OLS, Target: "y", Predictors: []string{"x1", "x2"},
		Tol: 1e-13, MaxIters: 200, Margins: &types.RegressionMargins{Type: c.Type, Binary: c.Binary}}
	if c.Model == "glm" {
		s.Type, s.Family, s.Link = types.REG_GLM, c.Family, c.Link
	}
	if c.Resample == "jackknife" {
		s.Resample = "jackknife"
		if c.By {
			s.JackknifeBy = "g"
		}
	}
	if c.Weighted {
		s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "f", Kind: types.WeightKindFrequency})
	}
	return s
}

// checkMarginsAgainstR holds a resampled block to the case's figures.
func checkMarginsAgainstR(t *testing.T, c marginsResampleCase, mg *types.RegressionMarginsResult) {
	t.Helper()
	if mg == nil {
		t.Fatal("no margins block")
	}
	if mg.Inference != types.MarginsInferenceResample || mg.DF != 0 || mg.ConfLevel != 0.95 {
		t.Fatalf("inference %q df %v conf %v, want resample / 0 / 0.95", mg.Inference, mg.DF, mg.ConfLevel)
	}
	for j, term := range []string{"x1", "x2"} {
		for _, f := range []struct {
			what      string
			got, want float64
			tol       float64
		}{
			{"estimate", mg.Estimates[term], c.Estimates[j], 1e-8},
			{"std_error", mg.StdErrors[term], c.StdErrors[j], 1e-7},
			{"p_value", mg.PValues[term], c.PValues[j], 1e-6},
			{"ci_lower", mg.CILower[term], c.CILower[j], 1e-7},
			{"ci_upper", mg.CIUpper[term], c.CIUpper[j], 1e-7},
		} {
			if !relNear(f.got, f.want, f.tol) {
				t.Errorf("%s[%s] = %.12g, R %.12g", f.what, term, f.got, f.want)
			}
		}
	}
}

// TestReferenceOracle_MarginsResampleMatchesR: REG_OLS and REG_GLM
// margins under resample reproduce base R's manual replicate loops of the
// effect itself — the leave-one-out, delete-a-cluster (jackknife_by) and
// frequency-weighted jackknife through the modifier engine, and the
// bootstrap over R's own index sets through the same replicate hook and
// replicate summary the engine's bootstrap runs (Pulse's PCG draws are
// pinned seed-stable apart) — ame, mem and the binary contrast, with the
// coefficients' replicate SE alongside.
func TestReferenceOracle_MarginsResampleMatchesR(t *testing.T) {
	t.Parallel()

	for _, c := range loadMarginsResample(t) {
		t.Run(c.Name, func(t *testing.T) {
			spec, recs := c.spec(), c.records()
			keys := []string{InterceptKey, "x1", "x2"}
			if c.Resample == "jackknife" {
				res, err := fitModifier(t, spec, recs)
				if err != nil {
					t.Fatalf("fit: %v", err)
				}
				for j, k := range keys {
					if !relNear(res.StdErrors[k], c.CoefStdErrors[j], 1e-7) {
						t.Errorf("coefficient se %s = %.12g, R %.12g", k, res.StdErrors[k], c.CoefStdErrors[j])
					}
				}
				checkMarginsAgainstR(t, c, res.Margins)
				return
			}
			base := fitPlain(t, spec, recs)
			eng, err := newModifierEngine(spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			me := eng.(*modifierEngine)
			fitter, _ := me.makeResampleFitter(spec.Predictors)
			rep := refitReplicate(2, fitter, marginsDerive(spec, me.family == "GLM", me.glmFam, spec.Predictors, base.Margins))
			reps := make([]replicate, 0, len(c.Samples))
			for _, idx := range c.Samples {
				sample := make([]Record, len(idx))
				for i, r := range idx {
					sample[i] = recs[r]
				}
				r, err := rep(sample)
				if err != nil {
					t.Fatalf("replicate: %v", err)
				}
				reps = append(reps, r)
			}
			div := float64(len(reps) - 1)
			run := newReplicateRun(reps, 3, false, true, func(s float64) float64 { return s / div })
			for j := range keys {
				if !relNear(run.se[j], c.CoefStdErrors[j], 1e-7) {
					t.Errorf("coefficient se %s = %.12g, R %.12g", keys[j], run.se[j], c.CoefStdErrors[j])
				}
			}
			checkMarginsAgainstR(t, c, resampleMargins(base.Margins, run, 0.95))
		})
	}
}

// fitPlain is spec's unmodified fit (resample cleared) through its
// inner engine.
func fitPlain(t *testing.T, spec *types.RegressionSpec, recs []Record) *types.RegressionResult {
	t.Helper()
	inner := *spec
	inner.Resample, inner.JackknifeBy = "", ""
	fields := []string{"y", "x1", "x2", "g", "f"}
	var (
		eng Engine
		err error
	)
	if inner.Type == types.REG_GLM {
		eng, err = newGLMEngine(&inner, schemaWithFields(fields...))
	} else {
		eng, err = newOLSEngine(&inner, schemaWithFields(fields...))
	}
	if err != nil {
		t.Fatal(err)
	}
	res, err := eng.(BufferedEngine).FitBuffered(recs)
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(res.Coefficients["x1"]) {
		t.Fatal("plain fit undefined")
	}
	return res
}
