package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// processExample runs one example request against the shared fixture
// cohorts in dataDir and fails on any envelope-level warning or error.
func processExample(t *testing.T, p *pulse.Pulse, rel, dataDir string) *pulse.Response {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("internal", "examples", rel))
	if err != nil {
		t.Fatal(err)
	}
	var req pulse.Request
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	req.Cohort.DataDir = dataDir
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s: %v", rel, err)
	}
	if len(resp.Warnings) != 0 {
		t.Fatalf("%s: warnings %+v", rel, resp.Warnings)
	}
	return resp
}

// floatVector reads a matrix result vector as []float64.
func floatVector(t *testing.T, m *types.MatrixResult, key string) []float64 {
	t.Helper()
	switch v := m.Vectors[key].(type) {
	case []float64:
		return v
	case []any:
		out := make([]float64, len(v))
		for i, x := range v {
			f, ok := x.(float64)
			if !ok {
				t.Fatalf("vectors.%s[%d] = %T, want float64", key, i, x)
			}
			out[i] = f
		}
		return out
	default:
		t.Fatalf("vectors.%s = %T, want a float vector", key, m.Vectors[key])
		return nil
	}
}

// TestExamples_BatteryMeasuresOneTrait pins what the measure_construct
// examples on battery.pulse teach: the reliability example's omega is
// DEFINED (no Heywood fit, the regression the battery replaced), the
// PCA example has one dominant component, and every item survives
// alpha-if-deleted. The test also pins the trend example's planted
// signal: Mann-Kendall rejects on the weekly revenue lift.
func TestExamples_BatteryMeasuresOneTrait(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	buildExampleFixtures(t, p, osfs, tmp)

	t.Run("reliability omega defined", func(t *testing.T) {
		resp := processExample(t, p, "matrices/08_reliability.json", tmp)
		if len(resp.Matrices) != 1 {
			t.Fatalf("got %d matrix results, want 1", len(resp.Matrices))
		}
		m := resp.Matrices[0]
		if m.Error != nil {
			t.Fatalf("slot error %+v", m.Error)
		}
		for _, w := range m.Warnings {
			t.Errorf("matrix warning %s: %s", w.Code, w.Message)
		}
		omega, ok := m.Scalars["omega"]
		if !ok || math.IsNaN(omega) || omega < 0.7 || omega > 0.95 {
			t.Fatalf("omega = %v (present %v), want a defined value in [0.7, 0.95]", omega, ok)
		}
		alpha := m.Scalars["alpha"]
		if alpha < 0.7 {
			t.Fatalf("alpha = %v, want >= 0.7 on a one-trait battery", alpha)
		}
		for i, a := range floatVector(t, &m, "alpha_if_deleted") {
			if a >= alpha {
				t.Errorf("alpha_if_deleted[%d] = %v >= alpha %v: an item weakens the scale", i, a, alpha)
			}
		}
	})

	t.Run("pca one dominant component", func(t *testing.T) {
		resp := processExample(t, p, "matrices/09_pca.json", tmp)
		m := resp.Matrices[0]
		if m.Error != nil {
			t.Fatalf("slot error %+v", m.Error)
		}
		if got := m.Scalars["components_retained"]; got != 1 {
			t.Fatalf("components_retained = %v, want 1", got)
		}
		ev := floatVector(t, &m, "eigenvalues")
		if len(ev) != 6 || ev[0] < 2*ev[1] {
			t.Fatalf("eigenvalues = %v, want six with the first at least twice the second", ev)
		}
		if kmo := m.Scalars["kmo"]; kmo < 0.8 {
			t.Fatalf("kmo = %v, want >= 0.8", kmo)
		}
	})

	t.Run("trend rejects", func(t *testing.T) {
		resp := processExample(t, p, "tests/09_post_trend_on_dates.json", tmp)
		if len(resp.PostTests) != 1 {
			t.Fatalf("got %d post tests, want 1", len(resp.PostTests))
		}
		if tr := resp.PostTests[0]; !tr.RejectNull {
			t.Fatalf("Mann-Kendall p = %v, want a rejection of no trend", tr.PValue)
		}
	})
}
