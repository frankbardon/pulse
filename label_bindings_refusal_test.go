package pulse

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

func labelRefusalCode(err error) perrors.Code {
	var ce *perrors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	if err != nil {
		return "uncoded: " + perrors.Code(err.Error())
	}
	return ""
}

// TestExport_RowLabelBindingShapeRefusals: a row export refuses a binding
// on a field the cohort lacks (PULSE_LABEL_FIELD_UNKNOWN) and two bindings
// on one field (PULSE_LABEL_DUPLICATE_BINDING) — the codes the `.sav`
// writer raises for the same bindings — instead of labelling nothing or
// letting the last binding win. Export predict raises the same code with
// a row target or none, and a refused export writes nothing.
func TestExport_RowLabelBindingShapeRefusals(t *testing.T) {
	t.Parallel()

	p := rowLabelsPulse(t)
	for _, tc := range []struct {
		name     string
		bindings []types.LabelBinding
		want     perrors.Code
	}{
		{"unknown field", []types.LabelBinding{{Field: "nope", Table: "regions"}}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"unknown after a valid one", []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "nope", Table: "counts", Mode: types.LabelModeAugment}}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"duplicate", []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "region", Table: "regions", Mode: types.LabelModeAugment}}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
		{"duplicate integer coded", []types.LabelBinding{{Field: "n", Table: "counts"}, {Field: "n", Table: "closure"}}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
		{"distinct valid fields", []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "n", Table: "counts"}}, ""},
	} {
		for _, f := range []pio.Format{pio.FormatCSV, pio.FormatParquet, pio.FormatNDJSON, ""} {
			t.Run(tc.name+"/"+string(f), func(t *testing.T) {
				job := func() (*pio.ExportJob, pio.BufferWriter) {
					var w pio.BufferWriter
					if f != "" {
						var err error
						if w, err = pio.NewWriterToBuffer(f, pio.WriterOptions{}); err != nil {
							t.Fatal(err)
						}
					}
					j := pio.NewExportJob("c.pulse", w)
					if w == nil {
						j.Target = nil
					}
					for i := range tc.bindings {
						b := tc.bindings[i]
						j.Labels = append(j.Labels, &b)
					}
					return j, w
				}
				pj, _ := job()
				_, perr := p.PredictExport(context.Background(), pj)
				if got := labelRefusalCode(perr); got != tc.want {
					t.Errorf("PredictExport code = %q, want %q", got, tc.want)
				}
				if f == "" {
					return
				}
				rj, w := job()
				_, rerr := p.Export(context.Background(), rj)
				if got := labelRefusalCode(rerr); got != tc.want {
					t.Errorf("Export code = %q, want %q", got, tc.want)
				}
				if tc.want != "" && f != pio.FormatParquet {
					_ = w.Close()
					if len(w.Bytes()) != 0 {
						t.Errorf("a refused export wrote %q", w.Bytes())
					}
				}
			})
		}
	}
}

// labelConvertSource is a CSV whose inferred schema holds one field of
// each label class: a categorical, an integer-coded field, a float and a
// date.
const labelConvertSource = "region,n,score,day\nNE,1,1.5,2024-01-01\nSO,2,2.5,2024-01-02\nWE,7,3.5,2024-01-03\n"

// convertWithLabels runs a CSV-to-CSV convert of labelConvertSource with
// the bindings. predict runs ConvertJob.Predict instead: Convert has no
// predict facade, so withResolver hands it the resolver Convert builds;
// without one only the refusals that need no table entries can fire.
func convertWithLabels(t *testing.T, p *Pulse, schema *encoding.Schema, predict, withResolver bool, bindings []types.LabelBinding) (pio.BufferWriter, error) {
	t.Helper()
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(labelConvertSource), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w, err := pio.NewWriterToBuffer(pio.FormatCSV, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	j := iio.NewConvertJob(r, w)
	j.Schema = schema
	for i := range bindings {
		b := bindings[i]
		j.Labels = append(j.Labels, &b)
	}
	if !predict {
		_, err = p.Convert(context.Background(), j)
		return w, err
	}
	if withResolver {
		res, rerr := p.newIOLabelResolver(j.Labels)
		if rerr != nil {
			return w, rerr
		}
		j.LabelResolver = res
	}
	_, err = j.Predict(context.Background())
	return w, err
}

// TestConvert_LabelBindingRefusalsMatchExport: a convert's label bindings
// are checked against the schema convert resolves (inferred or declared)
// with the row export's rules and codes, before a byte is written — in
// Run and in Predict alike. A valid categorical or integer-coded binding
// converts exactly as before.
func TestConvert_LabelBindingRefusalsMatchExport(t *testing.T) {
	t.Parallel()

	p := rowLabelsPulse(t)
	declared := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, CsvColumnIdx: 0},
		{Name: "n", Type: encoding.FieldTypeU8, CsvColumnIdx: 1},
		{Name: "score", Type: encoding.FieldTypeF64, CsvColumnIdx: 2},
		{Name: "day", Type: encoding.FieldTypeDate, CsvColumnIdx: 3},
	}}
	for _, tc := range []struct {
		name     string
		schema   *encoding.Schema
		bindings []types.LabelBinding
		want     perrors.Code
	}{
		{"categorical", nil, []types.LabelBinding{{Field: "region", Table: "regions"}}, ""},
		{"integer coded", nil, []types.LabelBinding{{Field: "n", Table: "counts", Mode: types.LabelModeAugment}}, ""},
		{"float", nil, []types.LabelBinding{{Field: "score", Table: "floats"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"date", nil, []types.LabelBinding{{Field: "day", Table: "counts"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"integer coded text keys", nil, []types.LabelBinding{{Field: "n", Table: "regions"}}, perrors.PULSE_LABEL_KEY_INVALID},
		{"integer coded leading zero", nil, []types.LabelBinding{{Field: "n", Table: "padded"}}, perrors.PULSE_LABEL_KEY_INVALID},
		// An inferred integer may promote past its sampled width in the
		// row pass, so its keys are bounded by u32, the widest coded type.
		{"inferred integer past its sampled width", nil, []types.LabelBinding{{Field: "n", Table: "wide"}}, ""},
		{"declared integer past the type", declared, []types.LabelBinding{{Field: "n", Table: "wide"}}, perrors.PULSE_LABEL_KEY_INVALID},
		{"declared float", declared, []types.LabelBinding{{Field: "score", Table: "floats"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"unknown field", nil, []types.LabelBinding{{Field: "nope", Table: "regions"}}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"duplicate", nil, []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "region", Table: "regions"}}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, err := convertWithLabels(t, p, tc.schema, false, false, tc.bindings)
			if got := labelRefusalCode(err); got != tc.want {
				t.Fatalf("Convert code = %q, want %q", got, tc.want)
			}
			if tc.want != "" {
				_ = w.Close()
				if len(w.Bytes()) != 0 {
					t.Errorf("a refused convert wrote %q", w.Bytes())
				}
			}
			_, err = convertWithLabels(t, p, tc.schema, true, true, tc.bindings)
			if got := labelRefusalCode(err); got != tc.want {
				t.Errorf("ConvertJob.Predict code = %q, want %q", got, tc.want)
			}
			// With no resolver, every refusal but a key's (which needs the
			// table's entries) is still raised from the bindings alone.
			want := tc.want
			if want == perrors.PULSE_LABEL_KEY_INVALID {
				want = ""
			}
			_, err = convertWithLabels(t, p, tc.schema, true, false, tc.bindings)
			if got := labelRefusalCode(err); got != want {
				t.Errorf("ConvertJob.Predict (no resolver) code = %q, want %q", got, want)
			}
		})
	}

	// A valid binding converts byte for byte as before.
	w, err := convertWithLabels(t, p, nil, false, false, []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "n", Table: "counts", Mode: types.LabelModeAugment}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "region,n,n_label,score,day\nNortheast,1,One,1.5,2024-01-01\nSouth,2,Two,2.5,2024-01-02\nWE,7,,3.5,2024-01-03\n"; string(w.Bytes()) != want {
		t.Errorf("valid convert =\n%s\nwant\n%s", w.Bytes(), want)
	}
}
