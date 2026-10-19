package spss

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// codedCohort is a native cohort holding one integer-coded field of type
// ft (values 1, 2, 3, 2) beside a float.
func codedCohort(t *testing.T, ft encoding.FieldType) (afero.Fs, string) {
	t.Helper()
	return vecCohort(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "dow", Type: ft, Description: "ISO weekday"},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}, [][]any{
		{uint64(1), 1.5},
		{uint64(2), 2.5},
		{uint64(3), 3.5},
		{uint64(2), 4.5},
	})
}

// TestValueLabels_IntegerFieldRoundTrip: a label table bound to an
// integer-coded field (u4 / u8 / u16 / u32) writes value labels on the
// field's NUMERIC variable — the codes are the stored integers, the table
// keys their decimal spellings — and Pulse's own reader brings the labels
// back. The data column is the stored values, untouched. A key no row
// holds (7) is still labelled: the table, not the data, lists the codes.
func TestValueLabels_IntegerFieldRoundTrip(t *testing.T) {
	week := map[string]string{"1": "Monday", "2": "Tuesday", "3": "Wednesday", "7": "Sunday"}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32} {
		t.Run(ft.String(), func(t *testing.T) {
			fs, cohort := codedCohort(t, ft)
			sav, _, err := exportVecSav(t, fs, cohort, WriterOptions{ValueLabels: []ValueLabels{
				{Field: "dow", Labels: week},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := savColumn(t, sav, "dow"), []float64{1, 2, 3, 2}; !reflect.DeepEqual(got, want) {
				t.Errorf("dow column = %v, want the stored integers %v", got, want)
			}
			labels, _ := readBackLabels(t, fs, sav, "dow")
			if want := map[float64]string{1: "Monday", 2: "Tuesday", 3: "Wednesday", 7: "Sunday"}; !reflect.DeepEqual(labels, want) {
				t.Errorf("dow labels read back = %v, want %v", labels, want)
			}
		})
	}
}

// entriesResolver is resolverFor plus the optional Entries method an
// enumerable table's resolver carries.
type entriesResolver struct{ resolverFor }

func (r *entriesResolver) Entries(f string) ([]string, bool) {
	if f != r.field || r.table == nil {
		return nil, false
	}
	keys := make([]string, 0, len(r.table))
	for k := range r.table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, true
}

// TestValueLabels_ExportJobBindingLabelsAnIntegerField: the export job's
// own binding (`--labels`) on an integer-coded field enumerates the bound
// TABLE (there is no dictionary to walk) and writes its entries as value
// labels, in either mode.
func TestValueLabels_ExportJobBindingLabelsAnIntegerField(t *testing.T) {
	for _, mode := range []types.LabelMode{types.LabelModeReplace, types.LabelModeAugment} {
		t.Run(string(mode), func(t *testing.T) {
			r := &entriesResolver{resolverFor{field: "id", mode: mode, table: map[string]string{"1": "One", "4": "Four", "9": "Nine"}}}
			sav, _, fs, err := exportLabelled(t, WriterOptions{}, func(j *pio.ExportJob) {
				j.Labels = []*types.LabelBinding{{Field: "id", Table: "ids", Mode: mode}}
				j.LabelResolver = r
			})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := savColumn(t, sav, "id"), []float64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
				t.Errorf("id column = %v, want %v", got, want)
			}
			labels, _ := readBackLabels(t, fs, sav, "id")
			if want := map[float64]string{1: "One", 4: "Four", 9: "Nine"}; !reflect.DeepEqual(labels, want) {
				t.Errorf("id labels = %v, want %v", labels, want)
			}
		})
	}
}

// TestValueLabels_IntegerFieldRefusalsMatchPredict pins every refusal an
// integer-coded binding can draw to one code on predict and on the export.
func TestValueLabels_IntegerFieldRefusalsMatchPredict(t *testing.T) {
	u64 := func(t *testing.T) (afero.Fs, string) { return codedCohort(t, encoding.FieldTypeU64) }
	u4 := func(t *testing.T) (afero.Fs, string) { return codedCohort(t, encoding.FieldTypeU4) }
	u8 := func(t *testing.T) (afero.Fs, string) { return codedCohort(t, encoding.FieldTypeU8) }
	bind := func(labels map[string]string) WriterOptions {
		return WriterOptions{ValueLabels: []ValueLabels{{Field: "dow", Labels: labels}}}
	}
	for _, tc := range []struct {
		name string
		opts WriterOptions
		job  func(*pio.ExportJob)
		fix  func(t *testing.T) (afero.Fs, string)
		want perr.Code
	}{
		{name: "text key", fix: u8, opts: bind(map[string]string{"1": "Mon", "Tue": "Tuesday"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "leading zero", fix: u8, opts: bind(map[string]string{"01": "Mon"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "fraction", fix: u8, opts: bind(map[string]string{"1.5": "Mon"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "negative", fix: u8, opts: bind(map[string]string{"-1": "Refused"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "past u8", fix: u8, opts: bind(map[string]string{"256": "Big"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "past u4", fix: u4, opts: bind(map[string]string{"16": "Big"}), want: perr.PULSE_LABEL_KEY_INVALID},
		{name: "no entries", fix: u8, opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "dow"}}}, want: perr.PULSE_LABEL_TABLE_NOT_ENUMERABLE},
		{name: "u64 stays refused", fix: u64, opts: bind(map[string]string{"1": "Mon"}), want: perr.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{name: "float stays refused", fix: u8, opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "score", Labels: map[string]string{"1": "x"}}}}, want: perr.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{name: "unenumerable resolver", fix: u8, job: func(j *pio.ExportJob) {
			j.Labels = []*types.LabelBinding{{Field: "dow", Table: "t"}}
			j.LabelResolver = &resolverFor{field: "dow", table: map[string]string{"1": "Mon"}}
		}, want: perr.PULSE_LABEL_TABLE_NOT_ENUMERABLE},
		{name: "bad key through the resolver", fix: u8, job: func(j *pio.ExportJob) {
			j.Labels = []*types.LabelBinding{{Field: "dow", Table: "t"}}
			j.LabelResolver = &entriesResolver{resolverFor{field: "dow", table: map[string]string{"Mon": "Monday"}}}
		}, want: perr.PULSE_LABEL_KEY_INVALID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, cohort := tc.fix(t)
			mk := func() *pio.ExportJob {
				j := pio.NewExportJob(cohort, NewWriter(fs, "out.sav", tc.opts))
				j.FS = fs
				if tc.job != nil {
					tc.job(j)
				}
				return j
			}
			_, perrPredict := mk().Predict(context.Background())
			_, perrRun := mk().Run(context.Background())
			if got := codedErr(t, perrPredict).Code; got != tc.want {
				t.Errorf("predict code = %s, want %s", got, tc.want)
			}
			if got := codedErr(t, perrRun).Code; got != tc.want {
				t.Errorf("runtime code = %s, want %s", got, tc.want)
			}
		})
	}
}
