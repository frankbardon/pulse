package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
	"github.com/spf13/afero"
)

// TestSynth_FidelityReportSkipsHelperRelationships: a relationship that
// reads a `"output": false` helper was applied, but the cohort never
// carries the column, so the report names it in a warning instead of
// scoring an empty partition.
func TestSynth_FidelityReportSkipsHelperRelationships(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Synth(context.Background(), fidelitySmallSpec(200), "/source.pulse", SynthOptions{Seed: 1}); err != nil {
		t.Fatal(err)
	}
	hidden := false
	spec := fidelitySmallSpec(300)
	spec.Fields = append(spec.Fields, synth.FieldSpec{
		Name: "hcat", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
		Params: map[string]any{"values": []any{"x", "y"}}, Output: &hidden,
	})
	spec.CategoricalPairs = []synth.CategoricalPairSpec{{A: "country", B: "hcat", Cells: []synth.CategoricalPairCellSpec{
		{AValue: "US", BValue: "x", Count: 5}, {AValue: "UK", BValue: "y", Count: 5},
	}}}
	if _, err := p.Synth(context.Background(), spec, "/aug.pulse",
		SynthOptions{Seed: 2, SourceCohort: "/source.pulse", FidelityReportPath: "/r.json"}); err != nil {
		t.Fatal(err)
	}
	raw, err := afero.ReadFile(fs, "/r.json")
	if err != nil {
		t.Fatal(err)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.CategoricalPairwise) != 0 {
		t.Fatalf("scored %d categorical pairs over a helper", len(report.CategoricalPairwise))
	}
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, `helper field "hcat"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning names the helper: %v", report.Warnings)
	}
}
