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

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/regcompare"
	"github.com/frankbardon/pulse/types"
)

// compareResampleCase is one compare_resample.json case
// (scripts/reference/gen_compare_resample.R): a manual base-R replicate
// loop of the full model and car::linearHypothesis over its replicate
// covariance.
type compareResampleCase struct {
	Name       string   `json:"name"`
	Family     string   `json:"family"`
	Link       string   `json:"link"`
	Resample   string   `json:"resample"`
	By         bool     `json:"jackknife_by"`
	Restricted []string `json:"restricted"`
	Full       []string `json:"full"`
	Terms      []string `json:"terms"`
	// Samples are a bootstrap case's index sets (0-based rows).
	Samples      [][]int     `json:"samples"`
	Rows         [][]float64 `json:"rows"`
	Coefficients []float64   `json:"coefficients"`
	ReplicateCov [][]float64 `json:"replicate_cov"`
	Statistic    float64     `json:"statistic"`
	DF           float64     `json:"df"`
	PValue       float64     `json:"p_value"`
}

// loadCompareResample reads compare_resample.json, checking its
// golden-hash footer.
func loadCompareResample(t *testing.T) []compareResampleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "reference", "compare_resample.json"))
	if err != nil {
		t.Fatalf("read compare_resample.json: %v (regenerate with scripts/reference/gen_compare_resample.R)", err)
	}
	s := string(raw)
	i := strings.LastIndex(s, "\n// golden-hash: ")
	if i < 0 {
		t.Fatal("compare_resample.json has no golden-hash footer")
	}
	body, stored := s[:i], strings.TrimSpace(s[i+len("\n// golden-hash: "):])
	if sum := sha256.Sum256([]byte(body)); hex.EncodeToString(sum[:]) != stored {
		t.Fatal("compare_resample.json: hash mismatch (hand-edited?); regenerate it")
	}
	var doc struct {
		Packages map[string]string     `json:"packages"`
		Cases    []compareResampleCase `json:"cases"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode compare_resample.json: %v", err)
	}
	if !strings.HasPrefix(doc.Packages["R"], "R version 4.6.1") || doc.Packages["car"] != "3.1.5" || len(doc.Cases) != 6 {
		t.Fatalf("compare_resample.json: R %q, car %q, %d cases", doc.Packages["R"], doc.Packages["car"], len(doc.Cases))
	}
	return doc.Cases
}

func (c compareResampleCase) records() []Record {
	out := make([]Record, len(c.Rows))
	for i, r := range c.Rows {
		out[i] = mockRecord{values: map[string]float64{"x1": r[0], "x2": r[1], "x3": r[2], "y": r[3], "g": r[4]}}
	}
	return out
}

// spec is the case's full fit under its resample.
func (c compareResampleCase) spec() *types.RegressionSpec {
	s := &types.RegressionSpec{Name: c.Name, Type: types.REG_OLS, Target: "y", Predictors: slices.Clone(c.Full),
		Tol: 1e-13, MaxIters: 200, Resample: c.Resample}
	if c.Family != "gaussian" {
		s.Type, s.Family, s.Link = types.REG_GLM, c.Family, c.Link
	}
	if c.By {
		s.JackknifeBy = "g"
	}
	return s
}

// relErr is |got − want| / |want|.
func relErr(got, want float64) float64 {
	return math.Abs(got-want) / math.Abs(want)
}

// TestReferenceOracle_CompareResampleMatchesR: the comparison figures a
// resampled REG_OLS / REG_GLM fit offers carry its replicate covariance
// (R's manual jackknife / bootstrap covariance of the full model's
// coefficients), and the Wald comparison over them (regcompare.Compute,
// χ² on q) reproduces car::linearHypothesis(vcov. = V, test = "Chisq") —
// the leave-one-out and delete-a-cluster jackknife through the modifier
// engine (vcov not requested: the full covariance is formed for the
// comparison alone), the bootstrap over R's own index sets through the
// same replicate hook, replicate summary and figures relay the engine's
// bootstrap runs. 1e-9 relative on the canonical links (worst seen
// 1.6e-13), 1e-6 on the gamma log link.
func TestReferenceOracle_CompareResampleMatchesR(t *testing.T) {
	t.Parallel()

	worst := 0.0
	for _, c := range loadCompareResample(t) {
		t.Run(c.Name, func(t *testing.T) {
			spec, recs := c.spec(), c.records()
			keys := coefficientKeys(spec.Predictors)
			// A non-canonical link's IRLS converges linearly: R's glm
			// stops on the relative deviance change, so its replicate
			// coefficients sit ~1e-9 off the fixed point Pulse reaches.
			tol := 1e-9
			if c.Family == "gamma" {
				tol = 1e-6
			}
			var (
				res  *types.RegressionResult
				figs *model.RegressionFigures
			)
			offer := func(g *model.RegressionFigures) {
				if figs != nil {
					t.Fatal("figures offered twice")
				}
				figs = g
			}
			eng, err := newModifierEngine(spec, nil)
			if err != nil {
				t.Fatal(err)
			}
			me := eng.(*modifierEngine)
			if c.Resample == "jackknife" {
				me.SetFiguresOffer(offer)
				if res, err = me.FitBuffered(recs); err != nil {
					t.Fatalf("fit: %v", err)
				}
			} else {
				// The base fit offers into the hold; the run over R's
				// index sets releases it.
				hold, relay := HoldFigures(spec, offer)
				inner := *spec
				inner.Resample = ""
				var base Engine
				if inner.Type == types.REG_GLM {
					base, err = newGLMEngine(&inner, schemaWithFields("y", "x1", "x2", "x3", "g"))
				} else {
					base, err = newOLSEngine(&inner, schemaWithFields("y", "x1", "x2", "x3", "g"))
				}
				if err != nil {
					t.Fatal(err)
				}
				base.(FiguresOfferer).SetFiguresOffer(hold)
				if res, err = base.(BufferedEngine).FitBuffered(recs); err != nil {
					t.Fatal(err)
				}
				if figs != nil {
					t.Fatal("the hold leaked the base fit's analytical figures")
				}
				fitter, _ := me.makeResampleFitter(spec.Predictors)
				rep := refitReplicate(len(spec.Predictors), fitter, nil)
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
				relay.release(keys, newReplicateRun(reps, len(keys), false, true, func(s float64) float64 { return s / div }))
			}
			if figs == nil {
				t.Fatal("no figures offered")
			}
			if figs.WaldDF != 0 || !slices.Equal(figs.Terms, keys) {
				t.Fatalf("figures terms %v wald df %v", figs.Terms, figs.WaldDF)
			}
			for j := range keys {
				if e := relErr(figs.Coefficients[j], c.Coefficients[j]); e > 1e-8 {
					t.Errorf("coefficient %s = %.15g, R %.15g", keys[j], figs.Coefficients[j], c.Coefficients[j])
				}
				for k := range keys {
					if e := relErr(figs.Covariance[j][k], c.ReplicateCov[j][k]); e > 10*tol {
						t.Errorf("cov[%s][%s] = %.15g, R %.15g", keys[j], keys[k], figs.Covariance[j][k], c.ReplicateCov[j][k])
					}
				}
			}
			plan := regcompare.Plan{Name: c.Name, Restricted: "m0", Full: "m1", Test: types.ComparisonTestWald,
				Distribution: types.ComparisonDistributionChiSquare, Type: spec.Type, Family: spec.Family, Terms: c.Terms}
			got, err := regcompare.Compute(plan, res, res, figs, figs)
			if err != nil || got.Error != nil {
				t.Fatalf("compute: %v %+v", err, got.Error)
			}
			if got.DF != c.DF || got.DFResidual != 0 {
				t.Fatalf("df %v df_residual %v, want %v / 0", got.DF, got.DFResidual, c.DF)
			}
			for _, f := range []struct {
				what      string
				got, want float64
			}{{"statistic", got.Statistic, c.Statistic}, {"p_value", got.PValue, c.PValue}} {
				e := relErr(f.got, f.want)
				worst = math.Max(worst, e)
				if !(e <= tol) {
					t.Errorf("%s = %.15g, R %.15g (rel %.2g)", f.what, f.got, f.want, e)
				}
			}
		})
	}
	t.Logf("worst statistic / p relative error: %.2g", worst)
}
