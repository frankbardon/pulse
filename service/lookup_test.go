package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// buildLookupFixture writes the shared cohort fixture and its "id"
// sidecar index in one step, returning the ready-to-use Service.
func buildLookupFixture(t *testing.T) *Service {
	t.Helper()
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex(id): %v", err)
	}
	return svc
}

func TestLookup_Hit_ReturnsRequestedColumns(t *testing.T) {
	svc := buildLookupFixture(t)

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "3",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1", len(res.Rows))
	}
	row := res.Rows[0]
	if len(row) != 1 {
		t.Fatalf("row has %d keys, want exactly 1 (projection): %+v", len(row), row)
	}
	got, ok := row["score"]
	if !ok {
		t.Fatalf("row missing %q: %+v", "score", row)
	}
	if got.(float64) != 30.0 {
		t.Errorf("score = %v, want 30.0 (id=3 -> row index 2)", got)
	}
}

func TestLookup_Hit_NoReturnColumnsProjectsEveryField(t *testing.T) {
	svc := buildLookupFixture(t)

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "1",
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	row := res.Rows[0]
	// indexTestSchema has 3 fields: id, score, region.
	for _, want := range []string{"id", "score", "region"} {
		if _, ok := row[want]; !ok {
			t.Errorf("row missing field %q with empty ReturnColumns: %+v", want, row)
		}
	}
	if got := row["id"].(float64); got != 1.0 {
		t.Errorf("id = %v, want 1.0", got)
	}
	if got := row["region"].(string); got != "north" {
		t.Errorf("region = %v, want \"north\" (dictionary id 0)", got)
	}
}

func TestLookup_CategoricalKey_Hit(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"}); err != nil {
		t.Fatalf("BuildIndex(region): %v", err)
	}

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "region",
		Value:         "east",
		ReturnColumns: []string{"id"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	// "east" (dictionary id 2) matches only row index 3 -> id 4.
	if got := res.Rows[0]["id"].(float64); got != 4.0 {
		t.Errorf("id = %v, want 4.0 (region=east -> row index 3)", got)
	}
}

func TestLookup_Miss_NoMatchingKey(t *testing.T) {
	svc := buildLookupFixture(t)

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "999",
	})
	if err == nil {
		t.Fatal("expected error for a key with no matching record")
	}
	if !errors.HasCode(err, errors.PULSE_LOOKUP_NOT_FOUND) {
		t.Errorf("expected PULSE_LOOKUP_NOT_FOUND, got: %v", err)
	}
}

func TestLookup_MissingIndex(t *testing.T) {
	// Cohort exists but no BuildIndex call was made for "id".
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "1",
	})
	if err == nil {
		t.Fatal("expected error when no sidecar index has been built")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("expected PULSE_INDEX_MISSING, got: %v", err)
	}
}

func TestLookup_UnknownKeyField(t *testing.T) {
	svc := buildLookupFixture(t)

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "does_not_exist",
		Value:  "1",
	})
	if err == nil {
		t.Fatal("expected error for unknown key field")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestLookup_UnknownReturnColumn(t *testing.T) {
	svc := buildLookupFixture(t)

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "1",
		ReturnColumns: []string{"does_not_exist"},
	})
	if err == nil {
		t.Fatal("expected error for unknown return column")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestLookup_NilRequest(t *testing.T) {
	cfg := setupTestFS(t, "cohort.pulse", indexTestSchema(), indexTestRecords())
	svc := New(cfg)

	_, err := svc.Lookup(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestLookup_CategoricalValueNotInDictionary(t *testing.T) {
	svc := buildLookupFixture(t)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"}); err != nil {
		t.Fatalf("BuildIndex(region): %v", err)
	}

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "region",
		Value:  "nowhere",
	})
	if err == nil {
		t.Fatal("expected error for a categorical value absent from the dictionary")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}
