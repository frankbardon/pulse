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

// convertMode picks how convertWithLabels drives the job: the Convert
// facade, the PredictConvert facade (which builds Convert's resolver), or a
// bare ConvertJob.Predict with no resolver.
type convertMode int

const (
	convertRun convertMode = iota
	convertPredictFacade
	convertPredictBare
)

// convertWithLabels runs a CSV-to-CSV convert of labelConvertSource with
// the bindings and projection, in the given mode. The report is a
// *pio.ConvertReport for convertRun and a *pio.PredictReport otherwise.
func convertWithLabels(t *testing.T, p *Pulse, schema *encoding.Schema, mode convertMode, includes []string, bindings []types.LabelBinding) (pio.BufferWriter, any, error) {
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
	j.Includes = includes
	for i := range bindings {
		b := bindings[i]
		j.Labels = append(j.Labels, &b)
	}
	switch mode {
	case convertRun:
		rep, err := p.Convert(context.Background(), j)
		return w, rep, err
	case convertPredictFacade:
		rep, err := p.PredictConvert(context.Background(), j)
		return w, rep, err
	default:
		rep, err := j.Predict(context.Background())
		return w, rep, err
	}
}

// TestConvert_LabelBindingRefusalsMatchExport: a convert's label bindings
// are checked against the schema convert resolves (inferred or declared)
// with the row export's rules and codes, before a byte is written — and
// PredictConvert raises every code Convert does, the unregistered table and
// the unknown projection field included, then reports Convert's figures. A valid categorical or integer-coded binding
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
		includes []string
		bindings []types.LabelBinding
		want     perrors.Code
	}{
		{"categorical", nil, nil, []types.LabelBinding{{Field: "region", Table: "regions"}}, ""},
		{"integer coded", nil, nil, []types.LabelBinding{{Field: "n", Table: "counts", Mode: types.LabelModeAugment}}, ""},
		{"float", nil, nil, []types.LabelBinding{{Field: "score", Table: "floats"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"date", nil, nil, []types.LabelBinding{{Field: "day", Table: "counts"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"integer coded text keys", nil, nil, []types.LabelBinding{{Field: "n", Table: "regions"}}, perrors.PULSE_LABEL_KEY_INVALID},
		{"integer coded leading zero", nil, nil, []types.LabelBinding{{Field: "n", Table: "padded"}}, perrors.PULSE_LABEL_KEY_INVALID},
		// An inferred integer may promote past its sampled width in the
		// row pass, so its keys are bounded by u32, the widest coded type.
		{"inferred integer past its sampled width", nil, nil, []types.LabelBinding{{Field: "n", Table: "wide"}}, ""},
		{"declared integer past the type", declared, nil, []types.LabelBinding{{Field: "n", Table: "wide"}}, perrors.PULSE_LABEL_KEY_INVALID},
		{"declared float", declared, nil, []types.LabelBinding{{Field: "score", Table: "floats"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{"unknown field", nil, nil, []types.LabelBinding{{Field: "nope", Table: "regions"}}, perrors.PULSE_LABEL_FIELD_UNKNOWN},
		{"duplicate", nil, nil, []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "region", Table: "regions"}}, perrors.PULSE_LABEL_DUPLICATE_BINDING},
		{"table unknown", nil, nil, []types.LabelBinding{{Field: "region", Table: "no_such_table"}}, perrors.PULSE_LABEL_TABLE_UNKNOWN},
		{"table unknown beside an unknown field", nil, nil, []types.LabelBinding{{Field: "nope", Table: "no_such_table"}}, perrors.PULSE_LABEL_TABLE_UNKNOWN},
		{"include unknown", nil, []string{"nope"}, nil, perrors.PULSE_EXPORT_FIELD_UNKNOWN},
		{"include unknown before a bad binding", nil, []string{"nope"}, []types.LabelBinding{{Field: "score", Table: "floats"}}, perrors.PULSE_EXPORT_FIELD_UNKNOWN},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, err := convertWithLabels(t, p, tc.schema, convertRun, tc.includes, tc.bindings)
			if got := labelRefusalCode(err); got != tc.want {
				t.Fatalf("Convert code = %q, want %q", got, tc.want)
			}
			if tc.want != "" {
				_ = w.Close()
				if len(w.Bytes()) != 0 {
					t.Errorf("a refused convert wrote %q", w.Bytes())
				}
			}
			_, _, err = convertWithLabels(t, p, tc.schema, convertPredictFacade, tc.includes, tc.bindings)
			if got := labelRefusalCode(err); got != tc.want {
				t.Errorf("PredictConvert code = %q, want %q", got, tc.want)
			}
			// With no resolver, every refusal but a key's (which needs the
			// table's entries) or a table's (which needs the resolver
			// build) is still raised from the bindings alone.
			want := tc.want
			switch {
			case want == perrors.PULSE_LABEL_KEY_INVALID:
				want = ""
			case want == perrors.PULSE_LABEL_TABLE_UNKNOWN && tc.bindings[0].Field == "nope":
				want = perrors.PULSE_LABEL_FIELD_UNKNOWN
			case want == perrors.PULSE_LABEL_TABLE_UNKNOWN:
				want = ""
			}
			_, _, err = convertWithLabels(t, p, tc.schema, convertPredictBare, tc.includes, tc.bindings)
			if got := labelRefusalCode(err); got != want {
				t.Errorf("ConvertJob.Predict (no resolver) code = %q, want %q", got, want)
			}
		})
	}

	// A valid binding converts byte for byte as before.
	valid := []types.LabelBinding{{Field: "region", Table: "regions"}, {Field: "n", Table: "counts", Mode: types.LabelModeAugment}}
	w, run, err := convertWithLabels(t, p, nil, convertRun, nil, valid)
	if err != nil {
		t.Fatal(err)
	}
	// PredictConvert reports the figures the run produces: the resolved
	// schema and the row count.
	_, pred, err := convertWithLabels(t, p, nil, convertPredictFacade, nil, valid)
	if err != nil {
		t.Fatal(err)
	}
	rr, pr := run.(*pio.ConvertReport), pred.(*pio.PredictReport)
	if pr.EstimatedRows != rr.RowsConverted {
		t.Errorf("PredictConvert EstimatedRows = %d, Convert RowsConverted = %d", pr.EstimatedRows, rr.RowsConverted)
	}
	if len(pr.Schema.Fields) != len(rr.Schema.Fields) {
		t.Fatalf("PredictConvert schema has %d fields, Convert %d", len(pr.Schema.Fields), len(rr.Schema.Fields))
	}
	for i := range pr.Schema.Fields {
		if pf, rf := pr.Schema.Fields[i], rr.Schema.Fields[i]; pf.Name != rf.Name || pf.Type != rf.Type {
			t.Errorf("field %d: PredictConvert %s %s, Convert %s %s", i, pf.Name, pf.Type, rf.Name, rf.Type)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "region,n,n_label,score,day\nNortheast,1,One,1.5,2024-01-01\nSouth,2,Two,2.5,2024-01-02\nWE,7,,3.5,2024-01-03\n"; string(w.Bytes()) != want {
		t.Errorf("valid convert =\n%s\nwant\n%s", w.Bytes(), want)
	}
}
