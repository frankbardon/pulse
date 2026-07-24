package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
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

// buildCompositeLookupFixture writes the compositeIndexTestSchema/
// Records fixture and its "(region,period)" composite sidecar index in
// one step.
func buildCompositeLookupFixture(t *testing.T, keyFields []string) *Service {
	t.Helper()
	schema := compositeIndexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, compositeIndexTestRecords())
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", keyFields); err != nil {
		t.Fatalf("BuildIndex(%v): %v", keyFields, err)
	}
	return svc
}

func TestLookup_CompositeTwoColumn_Hit(t *testing.T) {
	svc := buildCompositeLookupFixture(t, []string{"region", "period"})

	// (region=east, period=2023) matches only row index 3 -> id 4.
	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Keys: []types.LookupKey{
			{Field: "region", Value: "east"},
			{Field: "period", Value: "2023"},
		},
		ReturnColumns: []string{"id"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := res.Rows[0]["id"].(float64); got != 4.0 {
		t.Errorf("id = %v, want 4.0 (region=east,period=2023 -> row index 3)", got)
	}
}

func TestLookup_CompositeThreeColumn_Hit(t *testing.T) {
	svc := buildCompositeLookupFixture(t, []string{"id", "region", "period"})

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Keys: []types.LookupKey{
			{Field: "id", Value: "3"},
			{Field: "region", Value: "north"},
			{Field: "period", Value: "2021"},
		},
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got := res.Rows[0]["score"].(float64); got != 30.0 {
		t.Errorf("score = %v, want 30.0 (id=3,region=north,period=2021 -> row index 2)", got)
	}
}

func TestLookup_CompositeKeyOrderSignificant(t *testing.T) {
	// Build the index on (region, period) only — NOT (period, region).
	svc := buildCompositeLookupFixture(t, []string{"region", "period"})

	// Supplying the same two values in the OPPOSITE order derives a
	// different (non-existent) sidecar path, since key column order is
	// part of the index's identity end to end.
	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Keys: []types.LookupKey{
			{Field: "period", Value: "2023"},
			{Field: "region", Value: "east"},
		},
	})
	if err == nil {
		t.Fatal("expected error: (period,region) order was never built, only (region,period)")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("expected PULSE_INDEX_MISSING for the swapped-order request, got: %v", err)
	}

	// The correctly-ordered request against the same built index still hits.
	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Keys: []types.LookupKey{
			{Field: "region", Value: "east"},
			{Field: "period", Value: "2023"},
		},
		ReturnColumns: []string{"id"},
	})
	if err != nil {
		t.Fatalf("Lookup (correct order): %v", err)
	}
	if got := res.Rows[0]["id"].(float64); got != 4.0 {
		t.Errorf("id = %v, want 4.0", got)
	}
}

func TestLookup_SingleKeyPath_StillWorks(t *testing.T) {
	// E1 single-key shape (Field/Value, no Keys) must still behave
	// unchanged after the composite generalization.
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
	if got := res.Rows[0]["score"].(float64); got != 30.0 {
		t.Errorf("score = %v, want 30.0", got)
	}
}

func TestLookup_WrongArity_MismatchesIndexKeySpec(t *testing.T) {
	// Build a real composite index on (region, period) via BuildIndex,
	// so its on-disk key-spec (idx.Keys) genuinely has 2 entries at the
	// naturally-derived sidecar path. Then overwrite that same path with
	// a manually-constructed Index whose Keys slice has been truncated
	// to 1 entry — simulating a stale/corrupted/hand-placed sidecar
	// whose key-spec no longer matches its own path's field-name-derived
	// arity. A request supplying 2 key components (matching the path)
	// must then be rejected against the index's ACTUAL (loaded) 1-entry
	// key-spec, not the path-implied arity.
	schema := compositeIndexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, compositeIndexTestRecords())
	svc := New(cfg)

	res, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region", "period"})
	if err != nil {
		t.Fatalf("BuildIndex(region,period): %v", err)
	}

	corrupted := &encoding.Index{
		Fingerprint: res.Index.Fingerprint,
		Keys:        res.Index.Keys[:1], // truncated key-spec: 1 entry, not 2
		Buckets:     res.Index.Buckets,
	}
	if err := encoding.WriteIndexFile(cfg.Fs(), res.IndexPath, corrupted); err != nil {
		t.Fatalf("WriteIndexFile (corrupted sidecar): %v", err)
	}

	_, err = svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Keys: []types.LookupKey{
			{Field: "region", Value: "east"},
			{Field: "period", Value: "2023"},
		},
	})
	if err == nil {
		t.Fatal("expected error for a request whose key-component count does not match the loaded index's key-spec")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
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
