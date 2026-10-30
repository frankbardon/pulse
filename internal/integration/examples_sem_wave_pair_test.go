package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestExamples_SEMWavePairFitsBothWaves runs
// matrices/19_sem_wave_pair.json over the brand_survey fixture and
// holds it to what its description promises: both declared levels
// carry the wave's rows (wave is categorical, so the params must be
// the wave LABELS — an expression sees a categorical as its dictionary
// string, and a number never matches), no group-level warning fires,
// shift := bto - bfrom is finite, and swapping the params flips it.
func TestExamples_SEMWavePairFitsBothWaves(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	osfs := afero.NewOsFs()
	builder, err := pulse.New(pulse.Options{FS: osfs})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := loadFixtureSchema(filepath.Join("internal", "examples", "fixtures", "schemas", "brand_survey.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Import(context.Background(), &pio.ImportJob{
		Source:     csv.NewReader(osfs, filepath.Join("internal", "examples", "fixtures", "brand_survey.csv")),
		Target:     filepath.Join(tmp, "brand_survey.pulse"),
		Schema:     schema,
		SampleRows: 50,
		FS:         osfs,
	}); err != nil {
		t.Fatalf("import brand_survey: %v", err)
	}
	p, err := pulse.New(pulse.Options{DataDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join("internal", "examples", "matrices", "19_sem_wave_pair.json"))
	if err != nil {
		t.Fatal(err)
	}

	run := func(swap bool) float64 {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("parse: %v", err)
		}
		delete(doc, "_meta")
		delete(doc["cohort"].(map[string]any), "data_dir")
		if swap {
			params := doc["params"].(map[string]any)
			params["from_wave"], params["to_wave"] = params["to_wave"], params["from_wave"]
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		var req types.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatalf("Process (swap %v): %v", swap, err)
		}
		if len(resp.Matrices) != 1 {
			t.Fatalf("swap %v: %d matrices, want 1", swap, len(resp.Matrices))
		}
		res := resp.Matrices[0]
		for _, ws := range [][]*types.ResponseWarning{res.Warnings, resp.Warnings} {
			for _, w := range ws {
				if strings.HasPrefix(w.Code, "PULSE_MATRIX_GROUP_LEVEL_") {
					t.Errorf("swap %v: group-level warning %s: %s", swap, w.Code, w.Message)
				}
			}
		}
		if res.Error != nil {
			t.Fatalf("swap %v: slot error %+v", swap, res.Error)
		}
		for _, k := range []string{"n_group_from", "n_group_to"} {
			if n := res.Scalars[k]; !(n > 500) {
				t.Errorf("swap %v: %s = %v, want a wave's ~600 rows", swap, k, n)
			}
		}
		shift := definedRow(t, res, "shift")[0]
		if math.IsNaN(shift) || math.IsInf(shift, 0) {
			t.Fatalf("swap %v: shift = %v, want finite", swap, shift)
		}
		t.Logf("swap %v: n_group_from %v, n_group_to %v, shift %v", swap, res.Scalars["n_group_from"], res.Scalars["n_group_to"], shift)
		return shift
	}

	fwd, back := run(false), run(true)
	if fwd == 0 || math.Abs(fwd+back) > 1e-6*math.Max(math.Abs(fwd), 1) {
		t.Errorf("shift forward %v, swapped %v: want opposite signs, equal size", fwd, back)
	}
}
