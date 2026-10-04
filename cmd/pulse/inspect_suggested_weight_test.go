package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// `pulse cohort inspect` surfaces the SPSS sidecar's weighting variable
// as suggested_weight on --json and as a "not applied" line in text.
func TestCliCohortInspect_SuggestedWeight(t *testing.T) {
	dir := t.TempDir()
	raw, err := spsstest.Build(spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "ID", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "WT", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}},
		},
		Cases:     [][]spsstest.Value{{spsstest.Num(1), spsstest.Num(2)}},
		WeightVar: "WT",
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	sav := filepath.Join(dir, "in.sav")
	if err := os.WriteFile(sav, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	fs := afero.NewOsFs()
	reader, err := pio.NewReader(pio.FormatSPSS, fs, sav, pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	cohort := filepath.Join(dir, "survey.pulse")
	job := pio.NewImportJob(reader, cohort)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("import: %v", err)
	}

	out, err := runApp(t, "cohort", "inspect", "--json", cohort)
	if err != nil {
		t.Fatalf("cohort inspect --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"suggested_weight": {`) || !strings.Contains(out, `"source": "spss_sidecar"`) {
		t.Errorf("--json lacks suggested_weight: %s", out)
	}
	out, err = runApp(t, "cohort", "inspect", cohort)
	if err != nil {
		t.Fatalf("cohort inspect: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Suggested weight: WT (spss_sidecar, kind probability; not applied)") {
		t.Errorf("text mode lacks the suggestion: %s", out)
	}
}
