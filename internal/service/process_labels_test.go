package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// setupGroupableCohort writes a categorical "country" + numeric
// "amount" cohort with 6 rows for grouped-aggregation label tests.
func setupGroupableCohort(t *testing.T) (*Service, string) {
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
		{1, math.Float64bits(50.0)},
		{2, math.Float64bits(60.0)},
	}
	cfg := setupTestFS(t, "agg.pulse", schema, records)
	svc := New(cfg)
	return svc, "agg.pulse"
}

func TestProcess_GroupKey_Replace(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{
			"US":  "United States",
			"USA": "United States",
			"CA":  "Canada",
		}},
	})
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "total"},
		},
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeReplace},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	seen := map[string]float64{}
	for _, row := range resp.Data {
		key, _ := row["country"].(string)
		total, _ := row["total"].(float64)
		seen[key] = total
	}
	if v, ok := seen["United States (US)"]; !ok || v != 50.0 {
		t.Fatalf("expected United States (US) total 50.0; got %v in %+v", v, seen)
	}
	if v, ok := seen["United States (USA)"]; !ok || v != 90.0 {
		t.Fatalf("expected United States (USA) total 90.0; got %v in %+v", v, seen)
	}
	if v, ok := seen["Canada"]; !ok || v != 70.0 {
		t.Fatalf("expected Canada total 70.0; got %v in %+v", v, seen)
	}
	sawCollision := false
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_LABEL_COLLISION) {
			sawCollision = true
		}
	}
	if !sawCollision {
		t.Fatalf("expected PULSE_LABEL_COLLISION warning, got %+v", resp.Warnings)
	}
}

func TestProcess_GroupKey_Augment(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States", "CA": "Canada"}},
	})
	resp, err := svc.Process(context.Background(), &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "amount", Label: "total"},
		},
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
		},
	})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	for _, row := range resp.Data {
		raw, _ := row["country"].(string)
		switch raw {
		case "US":
			if row["country_label"] != "United States" {
				t.Fatalf("US row missing sibling label; got %v", row["country_label"])
			}
		case "CA":
			if row["country_label"] != "Canada" {
				t.Fatalf("CA row missing sibling label; got %v", row["country_label"])
			}
		case "USA":
			if row["country_label"] != nil {
				t.Fatalf("USA row should report nil sibling (table miss); got %v", row["country_label"])
			}
		}
	}
}

func TestProcess_Labels_UnknownTable(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	_, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
		Labels:       []*types.LabelBinding{{Field: "country", Table: "missing"}},
	})
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN, got %v", err)
	}
}

func TestProcess_Labels_AugmentCollisionWithAggLabel(t *testing.T) {
	svc, path := setupGroupableCohort(t)
	attachLabelExtensions(t, svc, map[string]processing.LabelTable{
		"country_names": {Rows: map[string]string{"US": "United States"}},
	})
	// Aggregation label is "country_label" — augment sibling clashes.
	_, err := svc.Process(context.Background(), &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "country"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "country_label"}},
		Labels: []*types.LabelBinding{
			{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
		},
	})
	if !errors.HasCode(err, errors.PULSE_LABEL_FIELD_COLLISION) {
		t.Fatalf("expected PULSE_LABEL_FIELD_COLLISION, got %v", err)
	}
}
