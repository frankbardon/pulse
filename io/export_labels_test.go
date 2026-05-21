package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// fakeLabelResolver is a hand-rolled implementation of LabelResolver
// for tests. Rows-backed only.
type fakeLabelResolver struct {
	field   string
	mode    types.LabelMode
	mapping map[string]string
	miss    map[string]int
}

func (f *fakeLabelResolver) Has(field string) bool { return field == f.field }

func (f *fakeLabelResolver) Mode(field string) types.LabelMode {
	if field == f.field {
		return f.mode
	}
	return ""
}

func (f *fakeLabelResolver) Apply(field, raw string) (string, string, bool) {
	if field != f.field {
		return raw, "", false
	}
	label, ok := f.mapping[raw]
	if !ok {
		f.miss[raw]++
		return raw, "", false
	}
	if f.mode == types.LabelModeAugment {
		return raw, label, true
	}
	return label, "", true
}

func (f *fakeLabelResolver) FieldsWithAugment() []string {
	if f.mode == types.LabelModeAugment {
		return []string{f.field}
	}
	return nil
}

func (f *fakeLabelResolver) Warnings() []LabelWarning {
	if len(f.miss) == 0 {
		return nil
	}
	return []LabelWarning{{Code: "PULSE_LABEL_LOOKUP_MISS", Message: "misses"}}
}

func newFakeResolver(field string, mode types.LabelMode, mapping map[string]string) *fakeLabelResolver {
	return &fakeLabelResolver{field: field, mode: mode, mapping: mapping, miss: map[string]int{}}
}

// importLabelledCohort writes a small categorical cohort and returns
// the fs path. Reuses the ImportJob machinery so the .pulse file ends
// up byte-faithful.
func importLabelledCohort(t *testing.T, fs afero.Fs) string {
	t.Helper()
	rows := [][]string{{"US", "10"}, {"CA", "20"}, {"USA", "30"}}
	reader := newMockReader([]string{"country", "amount"}, rows)
	job := NewImportJob(reader, "in.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ImportJob: %v", err)
	}
	return "in.pulse"
}

func TestExportJob_Labels_Replace(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := importLabelledCohort(t, fs)

	writer := &collectWriter{}
	job := NewExportJob(path, writer)
	job.FS = fs
	job.LabelResolver = newFakeResolver("country", types.LabelModeReplace, map[string]string{
		"US": "United States", "CA": "Canada", "USA": "United States of America",
	})
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if writer.header[0] != "country" {
		t.Fatalf("expected header[0] = country, got %v", writer.header)
	}
	if writer.rows[0][0] != "United States" {
		t.Fatalf("expected row[0][0]=\"United States\"; got %v", writer.rows[0][0])
	}
	if writer.rows[1][0] != "Canada" {
		t.Fatalf("expected row[1][0]=\"Canada\"; got %v", writer.rows[1][0])
	}
}

func TestExportJob_Labels_Augment(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := importLabelledCohort(t, fs)

	writer := &collectWriter{}
	job := NewExportJob(path, writer)
	job.FS = fs
	job.LabelResolver = newFakeResolver("country", types.LabelModeAugment, map[string]string{
		"US": "United States", "CA": "Canada",
	})
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(writer.header) != 3 {
		t.Fatalf("expected augmented header len 3; got %v", writer.header)
	}
	if writer.header[1] != "country_label" {
		t.Fatalf("expected sibling \"country_label\"; got %v", writer.header)
	}
	if writer.rows[0][1] != "United States" {
		t.Fatalf("expected row[0] sibling label \"United States\"; got %v", writer.rows[0][1])
	}
	if writer.rows[2][1] != "" {
		t.Fatalf("expected USA miss to produce empty sibling; got %v", writer.rows[2][1])
	}
}

func TestExportJob_Labels_Augment_RespectsIncludes(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := importLabelledCohort(t, fs)

	// Project to country only; augment must still emit the sibling.
	writer := &collectWriter{}
	job := NewExportJob(path, writer)
	job.FS = fs
	job.Includes = []string{"country"}
	job.LabelResolver = newFakeResolver("country", types.LabelModeAugment, map[string]string{
		"US": "United States", "CA": "Canada",
	})
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	want := []string{"country", "country_label"}
	for i, h := range want {
		if writer.header[i] != h {
			t.Fatalf("header = %v, want %v", writer.header, want)
		}
	}
	if writer.rows[0][1] != "United States" {
		t.Fatalf("expected augmented label; got %v", writer.rows[0][1])
	}
}

func TestExportJob_Labels_Augment_SkippedWhenSourceFieldExcluded(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := importLabelledCohort(t, fs)

	// Excluding country must also drop its augment sibling — there is
	// no in-band field for the label to attach to.
	writer := &collectWriter{}
	job := NewExportJob(path, writer)
	job.FS = fs
	job.Includes = []string{"amount"}
	job.LabelResolver = newFakeResolver("country", types.LabelModeAugment, map[string]string{
		"US": "United States",
	})
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if len(writer.header) != 1 || writer.header[0] != "amount" {
		t.Fatalf("header = %v, want [amount] only", writer.header)
	}
	if len(writer.rows[0]) != 1 {
		t.Fatalf("row width = %d, want 1", len(writer.rows[0]))
	}
}

func TestExportJob_Labels_NoResolver(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := importLabelledCohort(t, fs)

	writer := &collectWriter{}
	job := NewExportJob(path, writer)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Export: %v", err)
	}
	// No resolver: header + rows mirror raw schema.
	if len(writer.header) != 2 {
		t.Fatalf("expected un-augmented header len 2; got %v", writer.header)
	}
	if writer.rows[0][0] != "US" {
		t.Fatalf("expected raw US; got %v", writer.rows[0][0])
	}
}
