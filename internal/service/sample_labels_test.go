package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

func setupCategoricalCohort(t *testing.T) (*Service, string) {
	t.Helper()
	dict := encoding.NewDictionary()
	dict.Add("US")
	dict.Add("CA")
	dict.Add("USA")

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{0, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
		{0, math.Float64bits(40.0)},
	}
	cfg := setupTestFS(t, "labelled.pulse", schema, records)
	svc := New(cfg)
	return svc, "labelled.pulse"
}

func attachLabelExtensions(t *testing.T, svc *Service, tables map[string]processing.LabelTable) {
	t.Helper()
	r := &processing.ExtensionRegistry{LabelTables: tables}
	svc.SetExtensions(r)
	snap := &descriptor.ExtensionsSnapshot{}
	for name := range tables {
		snap.LabelTables = append(snap.LabelTables, descriptor.LabelTableMeta{Name: name, HasRowsData: true})
	}
	svc.SetExtensionsSnapshot(snap)
}

func TestSampleWithRequest_NoBindingsMatchesSample(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	got, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 rows; got %d", len(got))
	}
	if got[0]["country"] != "US" {
		t.Fatalf("row[0].country = %q; want \"US\"", got[0]["country"])
	}
}

func TestSampleWithRequest_Replace(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{
			"US":  "United States",
			"USA": "United States",
			"CA":  "Canada",
		}},
	})
	rows, warns, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeReplace},
		},
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if rows[0]["country"] != "United States (US)" {
		t.Fatalf("row[0].country = %q; want disambiguated (collision)", rows[0]["country"])
	}
	if rows[1]["country"] != "Canada" {
		t.Fatalf("row[1].country = %q; want \"Canada\"", rows[1]["country"])
	}
	if rows[2]["country"] != "United States (USA)" {
		t.Fatalf("row[2].country = %q; want disambiguated (collision)", rows[2]["country"])
	}
	saw := false
	for _, w := range warns {
		if w.Code == errors.PULSE_LABEL_COLLISION {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected PULSE_LABEL_COLLISION warning; got %+v", warns)
	}
}

func TestSampleWithRequest_Augment(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada"}},
	})
	rows, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
		},
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if rows[0]["country"] != "US" {
		t.Fatalf("expected raw value retained in augment; got %q", rows[0]["country"])
	}
	if rows[0]["country_label"] != "United States" {
		t.Fatalf("expected sibling label \"United States\"; got %q", rows[0]["country_label"])
	}
}

func TestSampleWithRequest_MissEmitsWarning(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States"}}, // USA + CA absent
	})
	rows, warns, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeReplace},
		},
	}, path)
	if err != nil {
		t.Fatalf("SampleWithRequest: %v", err)
	}
	if rows[1]["country"] != "CA" {
		t.Fatalf("expected miss fallback to raw \"CA\"; got %q", rows[1]["country"])
	}
	saw := false
	for _, w := range warns {
		if w.Code == errors.PULSE_LABEL_LOOKUP_MISS {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("expected PULSE_LABEL_LOOKUP_MISS warning; got %+v", warns)
	}
}

func TestSampleWithRequest_UnknownTable(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	_, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{{Field: "country", Table: "missing"}},
	}, path)
	if err == nil {
		t.Fatal("expected PULSE_LABEL_TABLE_UNKNOWN")
	}
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("got %v", err)
	}
}

func TestSampleWithRequest_NonCategoricalField(t *testing.T) {
	svc, path := setupCategoricalCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"x": {Rows: map[string]string{"k": "v"}},
	})
	_, _, err := svc.SampleWithRequest(context.Background(), &types.SampleRequest{
		Cohort: &types.Cohort{Filename: path}, N: 4,
		Labels: []*types.LabelBinding{{Field: "amount", Table: "x"}},
	}, path)
	if !errors.HasCode(err, errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL) {
		t.Fatalf("expected PULSE_LABEL_FIELD_NOT_CATEGORICAL; got %v", err)
	}
}
