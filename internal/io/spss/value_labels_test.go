package spss

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// labelCohortSchema is a native cohort: two categoricals (one whose every
// entry is an integer, one that is text), a numeric and a set.
func labelCohortSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Sales region"},
		{Name: "grade", Type: encoding.FieldTypeCategoricalU8},
		{Name: "score", Type: encoding.FieldTypeF64},
	}}
}

func labelCohortRows() [][]any {
	return [][]any{
		{uint64(1), "NE", "3", 1.5},
		{uint64(2), "SO", "1", 2.5},
		{uint64(3), nil, "10", 3.5},
		{uint64(4), "NE", "3", 4.5},
	}
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// TestValueLabels_UnboundExportsAreByteIdentical pins the two export paths
// that carry no value-label binding to their pre-binding bytes: the sidecar
// path (an SPSS-origin cohort) and the synthesised path (a native cohort,
// categoricals as STRING variables).
func TestValueLabels_UnboundExportsAreByteIdentical(t *testing.T) {
	t.Parallel()

	c := runCycle(t, kitchenSinkSpec(), WriterOptions{})
	if got := digest(c.out); got != "19d88918abc619b618b2e10f47ca6f9843181fcf283fe4ad1a0d843c739bf660" {
		t.Errorf("sidecar-path export digest = %s, changed", got)
	}
	fs, cohort := vecCohort(t, labelCohortSchema(), labelCohortRows())
	out, _, err := exportVecSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := digest(out); got != "f4e5ee281a081c1d7048a8e31d1a2dd805ce18cc6666afb03459281d7736ba09" {
		t.Errorf("synthesised-path export digest = %s, changed", got)
	}
}

// exportLabelled runs a real ExportJob over the native label cohort with
// opts and returns the emitted `.sav`, the report, and the cohort's fs.
func exportLabelled(t *testing.T, opts WriterOptions, job func(*pio.ExportJob)) ([]byte, *pio.ExportReport, afero.Fs, error) {
	t.Helper()
	fs, cohort := vecCohort(t, labelCohortSchema(), labelCohortRows())
	w := NewWriter(fs, "out.sav", opts)
	j := pio.NewExportJob(cohort, w)
	j.FS = fs
	if job != nil {
		job(j)
	}
	rep, err := j.Run(context.Background())
	if err != nil {
		return nil, nil, fs, err
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return w.Bytes(), rep, fs, nil
}

// readBackLabels imports sav through Pulse's own SPSS reader and returns,
// for the named variable, its code -> label map from the sidecar triple and
// the imported cohort field's dictionary entries.
func readBackLabels(t *testing.T, fs afero.Fs, sav []byte, name string) (map[float64]string, []string) {
	t.Helper()
	if err := afero.WriteFile(fs, "back.sav", sav, 0o644); err != nil {
		t.Fatal(err)
	}
	importSav(t, fs, "back.sav", "back.pulse")
	doc := readSidecar(t, fs, "back.pulse")
	labels := map[float64]string{}
	found := false
	for _, v := range doc.Payload.Variables {
		if v.Name != name {
			continue
		}
		found = true
		for _, c := range v.Categories {
			if c.Labelled && c.Code != nil {
				labels[float64(*c.Code)] = c.Label
			}
		}
	}
	if !found {
		t.Fatalf("variable %q not in the re-imported sidecar", name)
	}
	res, err := LoadSidecar(fs, "back.pulse", WriterOptions{})
	if err != nil || res == nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	f, err := fs.Open("back.pulse")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	for _, fld := range s.Fields {
		if fld.Name == name {
			if fld.Dictionary == nil {
				t.Fatalf("%q re-imported without a dictionary (type %s)", name, fld.Type)
			}
			return labels, fld.Dictionary.Values()
		}
	}
	t.Fatalf("field %q not in the re-imported cohort", name)
	return nil, nil
}

// TestValueLabels_RoundTrip is the story's central claim: a native cohort's
// categoricals, bound to label tables, go out as numeric variables with
// value labels, and Pulse's own reader brings back the same codes and the
// same labels. grade's entries are all integers, so they are its codes;
// region's are text, so its codes are positions + 1 and an entry the table
// misses keeps its text as the label.
func TestValueLabels_RoundTrip(t *testing.T) {
	t.Parallel()

	sav, _, fs, err := exportLabelled(t, WriterOptions{ValueLabels: []ValueLabels{
		{Field: "region", Labels: map[string]string{"NE": "Northeast"}},
		{Field: "grade", Labels: map[string]string{"1": "Poor", "3": "Good"}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := savColumn(t, sav, "grade"), []float64{3, 1, 10, 3}; !reflect.DeepEqual(got, want) {
		t.Errorf("grade codes = %v, want the entries' own integers %v", got, want)
	}
	region := savColumn(t, sav, "region")
	if region[0] != 1 || region[1] != 2 || !isSysmis(region[2]) || region[3] != 1 {
		t.Errorf("region codes = %v, want [1 2 sysmis 1] (position + 1, null system-missing)", region)
	}

	labels, dict := readBackLabels(t, fs, sav, "region")
	if want := map[float64]string{1: "Northeast", 2: "SO"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("region labels read back = %v, want %v", labels, want)
	}
	if want := []string{"1", "2"}; !reflect.DeepEqual(dict, want) {
		t.Errorf("region dictionary read back = %v, want the codes %v", dict, want)
	}

	labels, dict = readBackLabels(t, fs, sav, "grade")
	if want := map[float64]string{1: "Poor", 3: "Good"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("grade labels read back = %v, want %v (10 has no label: its text is its code)", labels, want)
	}
	// The value labels go out in code order, and the reader seeds the
	// dictionary from them before the data adds the unlabelled 10.
	if want := []string{"1", "3", "10"}; !reflect.DeepEqual(dict, want) {
		t.Errorf("grade dictionary read back = %v, want %v", dict, want)
	}
}

// TestValueLabels_NilLabelsUseTheDictionaryText: a binding with no table
// labels each positional code with the entry it stands for.
func TestValueLabels_NilLabelsUseTheDictionaryText(t *testing.T) {
	t.Parallel()

	sav, _, fs, err := exportLabelled(t, WriterOptions{ValueLabels: []ValueLabels{{Field: "region"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	labels, _ := readBackLabels(t, fs, sav, "region")
	if want := map[float64]string{1: "NE", 2: "SO"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("region labels = %v, want %v", labels, want)
	}
}

func TestCategoryCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		entries []string
		order   []string
		want    []float64
		own     bool
	}{
		{[]string{"3", "1", "-2"}, nil, []float64{3, 1, -2}, true},
		// The entries' own integers win over a table order.
		{[]string{"3", "1"}, []string{"1", "3"}, []float64{3, 1}, true},
		// No table order: byte-wise sorted dictionary order, never first-seen.
		{[]string{"South", "North", "East"}, nil, []float64{3, 2, 1}, false},
		{[]string{"01", "1"}, nil, []float64{1, 2}, false},
		{[]string{"07", "3"}, nil, []float64{1, 2}, false},
		{[]string{"x", "1"}, nil, []float64{2, 1}, false},
		{[]string{"9007199254740993"}, nil, []float64{1}, false},
		{[]string{""}, nil, []float64{1}, false},
		// A table order: the code is the entry's table position + 1, so a
		// cohort holding only some of the table's entries keeps the codes
		// every other cohort bound to the table gets.
		{[]string{"W3", "W1", "W4"}, []string{"W1", "W2", "W3", "W4"}, []float64{3, 1, 4}, false},
		// Entries the table does not list follow it, in sorted order.
		{[]string{"W3", "X", "A"}, []string{"W1", "W2", "W3"}, []float64{3, 5, 4}, false},
		// A repeated table entry keeps its first position.
		{[]string{"b", "a"}, []string{"a", "b", "a"}, []float64{2, 1}, false},
	} {
		got, own := categoryCodes(tc.entries, tc.order)
		if !reflect.DeepEqual(got, tc.want) || own != tc.own {
			t.Errorf("categoryCodes(%q, %q) = %v,%v want %v,%v", tc.entries, tc.order, got, own, tc.want, tc.own)
		}
	}
}

// TestValueLabels_CodesFollowTheTableOrder: a binding whose table states an
// entry order codes the categorical by it — the cohort's first-seen order
// (SO, NE, WE, MW) is not what SPSS sees — and Pulse's reader brings the
// codes and labels back. Without an order the codes are the sorted
// dictionary order.
func TestValueLabels_CodesFollowTheTableOrder(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}
	rows := [][]any{{"SO"}, {"NE"}, {"WE"}, {"MW"}}
	names := map[string]string{"NE": "Northeast", "MW": "Midwest", "SO": "South", "WE": "West"}
	for _, tc := range []struct {
		name   string
		order  []string
		codes  []float64
		labels map[float64]string
	}{
		{"table order", []string{"NE", "MW", "SO", "WE"}, []float64{3, 1, 4, 2},
			map[float64]string{1: "Northeast", 2: "Midwest", 3: "South", 4: "West"}},
		{"sorted", nil, []float64{3, 2, 4, 1},
			map[float64]string{1: "Midwest", 2: "Northeast", 3: "South", 4: "West"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs, cohort := vecCohort(t, schema, rows)
			sav, _, err := exportVecSav(t, fs, cohort, WriterOptions{ValueLabels: []ValueLabels{
				{Field: "region", Labels: names, Order: tc.order},
			}})
			if err != nil {
				t.Fatal(err)
			}
			if got := savColumn(t, sav, "region"); !reflect.DeepEqual(got, tc.codes) {
				t.Errorf("region codes = %v, want %v", got, tc.codes)
			}
			labels, _ := readBackLabels(t, fs, sav, "region")
			if !reflect.DeepEqual(labels, tc.labels) {
				t.Errorf("labels read back = %v, want %v", labels, tc.labels)
			}
		})
	}
}

// resolverFor is a LabelResolver binding one field to a table in mode.
type resolverFor struct {
	field string
	mode  types.LabelMode
	table map[string]string
	miss  []string
}

func (r *resolverFor) Has(f string) bool             { return f == r.field }
func (r *resolverFor) Mode(f string) types.LabelMode { return r.mode }
func (r *resolverFor) FieldsWithAugment() []string   { return nil }
func (r *resolverFor) Warnings() []pio.LabelWarning  { return nil }
func (r *resolverFor) Apply(f, raw string) (string, string, bool) {
	l, ok := r.table[raw]
	if !ok {
		r.miss = append(r.miss, raw)
		return raw, "", false
	}
	if r.mode == types.LabelModeAugment {
		return raw, l, true
	}
	return l, "", true
}

// TestValueLabels_ExportJobBindingBecomesValueLabels: the export job's own
// label binding (`--labels`, ExportJob.Labels + LabelResolver) is no longer
// refused on a .sav export — it is resolved onto the dictionary and written
// as value labels, in either mode, and the misses reach the resolver.
func TestValueLabels_ExportJobBindingBecomesValueLabels(t *testing.T) {
	t.Parallel()

	for _, mode := range []types.LabelMode{types.LabelModeReplace, types.LabelModeAugment} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			r := &resolverFor{field: "region", mode: mode, table: map[string]string{"NE": "Northeast", "SO": "South"}}
			sav, _, fs, err := exportLabelled(t, WriterOptions{}, func(j *pio.ExportJob) {
				j.Labels = []*types.LabelBinding{{Field: "region", Table: "regions", Mode: mode}}
				j.LabelResolver = r
			})
			if err != nil {
				t.Fatal(err)
			}
			labels, _ := readBackLabels(t, fs, sav, "region")
			if want := map[float64]string{1: "Northeast", 2: "South"}; !reflect.DeepEqual(labels, want) {
				t.Errorf("labels = %v, want %v", labels, want)
			}
		})
	}
}

// TestValueLabels_RefusalsMatchPredict pins every value-label refusal to
// the same code on `pulse export predict` (ExportJob.Predict ->
// ValidateCohort) and on the export itself (ExportJob.Run -> WriteCohort).
func TestValueLabels_RefusalsMatchPredict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts WriterOptions
		job  func(*pio.ExportJob)
		fix  func(t *testing.T) (afero.Fs, string)
		want perr.Code
	}{
		{name: "unknown field option", opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "nope"}}}, want: perr.PULSE_LABEL_FIELD_UNKNOWN},
		{name: "numeric field option", opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "score"}}}, want: perr.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{name: "duplicate option", opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "region"}, {Field: "region"}}}, want: perr.PULSE_LABEL_DUPLICATE_BINDING},
		{name: "unknown field binding", job: func(j *pio.ExportJob) {
			j.Labels = []*types.LabelBinding{{Field: "nope", Table: "t"}}
			j.LabelResolver = &resolverFor{field: "nope"}
		}, want: perr.PULSE_LABEL_FIELD_UNKNOWN},
		{name: "numeric field binding", job: func(j *pio.ExportJob) {
			j.LabelResolver = &resolverFor{field: "score"}
		}, want: perr.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
		{name: "beside a sidecar", opts: WriterOptions{ValueLabels: []ValueLabels{{Field: "REGION"}}},
			fix: func(t *testing.T) (afero.Fs, string) {
				fs, cohort, _ := importFixture(t, plainSpec())
				return fs, cohort
			}, want: perr.PULSE_SPSS_EXPORT_UNSUPPORTED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var fs afero.Fs
			var cohort string
			if tc.fix != nil {
				fs, cohort = tc.fix(t)
			} else {
				fs, cohort = vecCohort(t, labelCohortSchema(), labelCohortRows())
			}
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

// TestValueLabels_IgnoreSidecarLabelsAnSPSSCohort: --ignore-sidecar lifts
// the beside-a-sidecar refusal, and the binding labels the synthesised
// variable.
func TestValueLabels_IgnoreSidecarLabelsAnSPSSCohort(t *testing.T) {
	t.Parallel()

	fs, cohort, _ := importFixture(t, plainSpec())
	w := NewWriter(fs, "out.sav", WriterOptions{IgnoreSidecar: true,
		ValueLabels: []ValueLabels{{Field: "REGION", Labels: map[string]string{"North": "Northern"}}}})
	j := pio.NewExportJob(cohort, w)
	j.FS = fs
	if _, err := j.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	labels, _ := readBackLabels(t, fs, w.Bytes(), "REGION")
	if want := map[float64]string{1: "Northern", 2: "South"}; !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}
