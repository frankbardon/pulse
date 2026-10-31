package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E3-S1 (FR-19) facade tests: SynthOptions.SyntheticOnly writes the
// generated rows alone, and the fidelity report — marginals, target
// rates and the supplied-model refit included — is unchanged by it.

// syntheticOnlyFixtureSpec is utilityFixtureSpec plus a numeric target
// driven by the categorical through an applied model, so the report's
// target_rates half has something to score.
func syntheticOnlyFixtureSpec(rows int) *synth.Spec {
	spec := utilityFixtureSpec(rows, 100, 0, 0.5)
	spec.Fields = append(spec.Fields, synth.FieldSpec{
		Name: "spend", Type: "f64", Distribution: synth.DistNormal,
		Params: map[string]any{"mean": 10.0, "std": 2.0},
	})
	spec.Models = []synth.FieldModelSpec{{
		Field:     "spend",
		Intercept: 10,
		Predictors: []synth.ModelPredictorSpec{{
			Kind: synth.ModelPredictorCategoricalLevel, Field: "segment", Level: "b", Coefficient: 3,
		}},
		ResidualStd: 1.5,
	}}
	return spec
}

// syntheticOnlyModelRequest refits y on x.
func syntheticOnlyModelRequest(t *testing.T) *types.Request {
	t.Helper()
	var req types.Request
	body := `{"cohort": {"filename": "ignored.pulse"},
	  "regressions": [{"type": "REG_OLS", "name": "y_on_x", "target": "y", "predictors": ["x"]}]}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return &req
}

// runSyntheticOnlyPair tops the same source up twice at the same seed —
// tagged, then synthetic-only — and returns both outputs and reports.
func runSyntheticOnlyPair(t *testing.T, sourceRows, newRows int) (fs afero.Fs, tagged, only, taggedReport, onlyReport []byte) {
	t.Helper()
	fs = afero.NewMemMapFs()
	p, err := New(Options{FS: fs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	if _, err := p.Synth(ctx, syntheticOnlyFixtureSpec(sourceRows), "/source.pulse", SynthOptions{Seed: 31}); err != nil {
		t.Fatalf("source synth: %v", err)
	}
	for _, c := range []struct {
		out, report string
		only        bool
	}{{"/tagged.pulse", "/tagged.json", false}, {"/only.pulse", "/only.json", true}} {
		res, err := p.Synth(ctx, syntheticOnlyFixtureSpec(newRows), c.out, SynthOptions{
			Seed: 32, SourceCohort: "/source.pulse", FidelityReportPath: c.report,
			FidelityModel: syntheticOnlyModelRequest(t), SyntheticOnly: c.only,
		})
		if err != nil {
			t.Fatalf("synth (synthetic-only=%v): %v", c.only, err)
		}
		if res.RowsGenerated != newRows || res.FidelityReportPath != c.report {
			t.Errorf("synthetic-only=%v: result = %+v", c.only, res)
		}
	}
	read := func(path string) []byte {
		raw, err := afero.ReadFile(fs, path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return raw
	}
	return fs, read("/tagged.pulse"), read("/only.pulse"), read("/tagged.json"), read("/only.json")
}

// decodeLabelledRows decodes a cohort into per-row field maps, a
// categorical resolved to its label (the two layouts build their
// dictionaries independently, so IDs need not agree).
func decodeLabelledRows(t *testing.T, data []byte) (*encoding.Schema, []map[string]any) {
	t.Helper()
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	rr := encx.NewRecordReader(r, schema)
	var rows []map[string]any
	for {
		values := make(map[string]float64)
		nulls := make(map[string]bool)
		if err := rr.ReadRecord(values, nulls); err != nil {
			break
		}
		row := make(map[string]any, len(values))
		for name, f := range values {
			switch fld := schema.Field(name); {
			case nulls[name]:
				row[name] = nil
			case fld != nil && fld.Dictionary != nil:
				row[name] = fld.Dictionary.Resolve(uint32(f))
			default:
				row[name] = f
			}
		}
		rows = append(rows, row)
	}
	return schema, rows
}

// TestSynth_SyntheticOnlyWritesGeneratedRowsAlone is FR-19's output
// acceptance: the file carries the source's schema with no _synthetic
// column, exactly the generated rows the tagged run marks
// _synthetic=true, and no source row.
func TestSynth_SyntheticOnlyWritesGeneratedRowsAlone(t *testing.T) {
	t.Parallel()

	const sourceRows, newRows = 300, 500
	fs, tagged, only, _, _ := runSyntheticOnlyPair(t, sourceRows, newRows)
	source, err := afero.ReadFile(fs, "/source.pulse")
	if err != nil {
		t.Fatal(err)
	}

	srcSchema, srcRows := decodeLabelledRows(t, source)
	onlySchema, onlyRows := decodeLabelledRows(t, only)
	_, taggedRows := decodeLabelledRows(t, tagged)

	if onlySchema.Field(synth.SyntheticFieldName) != nil {
		t.Errorf("the synthetic-only output carries a %s column", synth.SyntheticFieldName)
	}
	if len(onlySchema.Fields) != len(srcSchema.Fields) {
		t.Fatalf("fields = %d, want the source's %d", len(onlySchema.Fields), len(srcSchema.Fields))
	}
	for i := range srcSchema.Fields {
		if onlySchema.Fields[i].Name != srcSchema.Fields[i].Name || onlySchema.Fields[i].Type != srcSchema.Fields[i].Type {
			t.Errorf("field %d = %s/%s, want the source's %s/%s", i,
				onlySchema.Fields[i].Name, onlySchema.Fields[i].Type, srcSchema.Fields[i].Name, srcSchema.Fields[i].Type)
		}
	}
	if len(onlyRows) != newRows {
		t.Fatalf("rows = %d, want the %d generated rows alone", len(onlyRows), newRows)
	}

	// The same generated rows as the tagged run's _synthetic=true
	// partition, in the same order.
	var generated []map[string]any
	for _, row := range taggedRows {
		if row[synth.SyntheticFieldName] == 1.0 {
			delete(row, synth.SyntheticFieldName)
			generated = append(generated, row)
		}
	}
	if len(generated) != newRows {
		t.Fatalf("tagged run: %d generated rows, want %d", len(generated), newRows)
	}
	for i := range onlyRows {
		if !sameRow(onlyRows[i], generated[i]) {
			t.Fatalf("row %d = %v, want the tagged run's generated row %v", i, onlyRows[i], generated[i])
		}
	}

	// No source row: a continuous field makes an accidental full-row
	// match impossible, so any match is a copied row.
	for _, s := range srcRows {
		for i, o := range onlyRows {
			if sameRow(s, o) {
				t.Fatalf("output row %d is a source row: %v", i, o)
			}
		}
	}
}

// TestSynth_SyntheticOnlyFidelityReportIsTheTaggedRunsReport is FR-19's
// report acceptance: with the source rows gone from the file, the report
// still compares the generated rows against them — byte for byte the
// report the tagged run writes, the target_rates and the model_fits
// refit included.
func TestSynth_SyntheticOnlyFidelityReportIsTheTaggedRunsReport(t *testing.T) {
	t.Parallel()

	_, _, _, taggedReport, onlyReport := runSyntheticOnlyPair(t, 300, 500)
	if !bytes.Equal(taggedReport, onlyReport) {
		t.Fatalf("synthetic-only report differs from the tagged run's:\n--- tagged\n%s\n--- synthetic-only\n%s", taggedReport, onlyReport)
	}
	var report synth.FidelityReport
	if err := json.Unmarshal(onlyReport, &report); err != nil {
		t.Fatal(err)
	}
	if report.SourceRows != 300 || report.SyntheticRows != 500 {
		t.Errorf("partitions = %d / %d, want 300 / 500", report.SourceRows, report.SyntheticRows)
	}
	if report.Utility == nil || len(report.Utility.TargetRates) == 0 || len(report.Utility.ModelFits) != 1 {
		t.Fatalf("utility = %+v, want target_rates and one model fit", report.Utility)
	}
	fit := report.Utility.ModelFits[0]
	if fit.SourceError != "" || fit.SyntheticError != "" || fit.SourceNObs != 300 || fit.SyntheticNObs != 500 {
		t.Errorf("model fit = %+v, want both fits over 300 / 500 rows", *fit)
	}
}

func sameRow(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
