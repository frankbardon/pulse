package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// runBrandExample predicts and processes internal/examples/<dir>/<file>
// over the built example fixtures: predict must be clean and the run
// warning-free.
func runBrandExample(t *testing.T, dir, file string) *types.Response {
	t.Helper()
	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, builder, osfs, tmp)
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("internal", "examples", dir, file))
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	req.Cohort.DataDir = ""
	pred, err := p.Predict(context.Background(), &req)
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}
	if pred.Streamable {
		t.Error("predict calls an ordinal request streamable; WLSMV buffers")
	}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("warnings: %+v", resp.Warnings)
	}
	return resp
}

// TestExamples_SEMOrdinalRecipe runs matrices/18_sem_ordinal.json over
// the brand_survey fixture: the ML slot reports a log-likelihood and no
// threshold rows; the WLSMV slot four x | tk rows per 1-5 rating, a null
// unscaled p and log-likelihood beside the scaled test and indices; the
// multi-group WLSMV slot (thresholds, loadings and intercepts equal)
// frees wave2's latent means, which recover the planted shift's signs
// (value down, service up, quality within 2 SE of 0).
func TestExamples_SEMOrdinalRecipe(t *testing.T) {
	t.Parallel()

	resp := runBrandExample(t, "matrices", "18_sem_ordinal.json")
	if len(resp.Matrices) != 3 {
		t.Fatalf("matrices %+v", resp.Matrices)
	}
	for _, m := range resp.Matrices {
		if m.Error != nil {
			t.Fatalf("%s: %+v", m.Name, m.Error)
		}
	}
	ml, wlsmv, waves := resp.Matrices[0], resp.Matrices[1], resp.Matrices[2]
	thresholds := func(m types.MatrixResult) int {
		n := 0
		for _, k := range m.Auxiliary["estimates"].RowKeys {
			if strings.Contains(k, " | t") {
				n++
			}
		}
		return n
	}
	if math.IsNaN(ml.Scalars["loglik"]) || thresholds(ml) != 0 {
		t.Errorf("ml: loglik %v, %d threshold rows", ml.Scalars["loglik"], thresholds(ml))
	}
	if got := thresholds(wlsmv); got != 9*4 {
		t.Errorf("wlsmv: %d threshold rows, want 36 (nine 1-5 ratings)", got)
	}
	if !math.IsNaN(wlsmv.Scalars["chi2_p_value"]) || !math.IsNaN(wlsmv.Scalars["loglik"]) {
		t.Errorf("wlsmv: chi2_p_value %v, loglik %v; want both null", wlsmv.Scalars["chi2_p_value"], wlsmv.Scalars["loglik"])
	}
	for _, k := range []string{"chi2_scaled", "chi2_scaled_p_value", "scaling_factor", "shift_parameter", "cfi_scaled", "tli_scaled", "rmsea_scaled"} {
		if v, ok := wlsmv.Scalars[k]; !ok || math.IsNaN(v) {
			t.Errorf("wlsmv: scalars.%s = %v (present %v)", k, v, ok)
		}
	}
	est := waves.Auxiliary["estimates"]
	mean := func(lat string) (float64, float64) {
		for i, k := range est.RowKeys {
			if k == "[wave2] "+lat+" ~1" {
				return est.Values[i][0], est.Values[i][2]
			}
		}
		t.Fatalf("no [wave2] %s ~1 row in %v", lat, est.RowKeys)
		return 0, 0
	}
	v, _ := mean("value")
	s, _ := mean("service")
	q, qse := mean("quality")
	if !(v < 0) || !(s > 0) || math.Abs(q) > 2*qse {
		t.Errorf("wave2 latent means value %.4f, service %.4f, quality %.4f (SE %.4f): want down, up, within 2 SE of 0", v, s, q, qse)
	}
}

// TestExamples_SEMOrdinalScores runs attributes/26_sem_ordinal_score.json:
// EBM scores of the multi-group WLSMV fit, averaged by wave, sit near 0
// in wave1 (the reference) and move with the planted shift in wave2
// (value down, service up).
func TestExamples_SEMOrdinalScores(t *testing.T) {
	t.Parallel()

	resp := runBrandExample(t, "attributes", "26_sem_ordinal_score.json")
	means := map[string]map[string]float64{}
	for _, row := range resp.Data {
		wave := fmt.Sprint(row["wave"])
		means[wave] = map[string]float64{}
		for _, k := range []string{"mean_value_score", "mean_service_score"} {
			v, ok := row[k].(float64)
			if !ok {
				t.Fatalf("%s %s = %v", wave, k, row[k])
			}
			means[wave][k] = v
		}
	}
	w1, w2 := means["wave1"], means["wave2"]
	if w1 == nil || w2 == nil {
		t.Fatalf("by-wave rows %v", means)
	}
	for k, v := range w1 {
		if math.Abs(v) > 0.1 {
			t.Errorf("wave1 %s = %.4f, want near 0 (the reference)", k, v)
		}
	}
	if !(w2["mean_value_score"] < w1["mean_value_score"]) || !(w2["mean_service_score"] > w1["mean_service_score"]) {
		t.Errorf("wave means %v: want value down and service up in wave2", means)
	}
}
