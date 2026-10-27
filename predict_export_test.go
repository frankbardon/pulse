package pulse

import (
	"context"
	stderrors "errors"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestPredictExport_LabelRefusalsMatchExport: PredictExport resolves the
// job's label bindings against the instance's tables exactly as Export
// does, so every label refusal on a `.sav` target carries one code on both
// — and a clean binding predicts clean. Without the facade, ExportJob.Predict
// has no resolver and these bindings would pass unchecked.
func TestPredictExport_LabelRefusalsMatchExport(t *testing.T) {
	t.Parallel()

	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, "c.pulse", []string{"id", "region", "amount"}, [][]string{
		{"1", "north", "10.5"}, {"2", "south", "20.25"},
	})
	p, err := New(Options{FS: fsys, Extensions: Extensions{LabelTables: map[string]LabelTable{
		"regions": {Rows: map[string]string{"north": "North", "south": "South"}},
		"ids":     {Rows: map[string]string{"1": "One", "2": "Two", "9": "Nine"}},
		"closure": {Lookup: func(k string) (string, bool, error) { return "L" + k, true, nil }},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	code := func(err error) perrors.Code {
		var ce *perrors.CodedError
		if stderrors.As(err, &ce) {
			return ce.Code
		}
		if err != nil {
			return "uncoded: " + perrors.Code(err.Error())
		}
		return ""
	}
	for _, tc := range []struct {
		name    string
		binding types.LabelBinding
		want    perrors.Code
	}{
		{"clean", types.LabelBinding{Field: "region", Table: "regions"}, ""},
		{"unknown table", types.LabelBinding{Field: "region", Table: "nope"}, perrors.PULSE_LABEL_TABLE_UNKNOWN},
		{"unknown field", types.LabelBinding{Field: "nope", Table: "regions"}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"numeric field", types.LabelBinding{Field: "amount", Table: "regions"}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"integer field", types.LabelBinding{Field: "id", Table: "ids"}, ""},
		{"integer field augment", types.LabelBinding{Field: "id", Table: "ids", Mode: types.LabelModeAugment}, ""},
		{"integer field text keys", types.LabelBinding{Field: "id", Table: "regions"}, perrors.PULSE_LABEL_KEY_INVALID},
		{"integer field lookup-only table", types.LabelBinding{Field: "id", Table: "closure"}, perrors.PULSE_LABEL_TABLE_NOT_ENUMERABLE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := func() *pio.ExportJob {
				w, err := pio.NewWriterToBuffer(pio.FormatSPSS, pio.WriterOptions{})
				if err != nil {
					t.Fatal(err)
				}
				j := pio.NewExportJob("c.pulse", w)
				b := tc.binding
				j.Labels = []*types.LabelBinding{&b}
				return j
			}
			_, perr := p.PredictExport(context.Background(), job())
			_, rerr := p.Export(context.Background(), job())
			if got := code(perr); got != tc.want {
				t.Errorf("PredictExport code = %q, want %q", got, tc.want)
			}
			if got := code(rerr); got != tc.want {
				t.Errorf("Export code = %q, want %q", got, tc.want)
			}
		})
	}
}
