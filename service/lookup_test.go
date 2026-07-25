package service

import (
	"context"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
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

func TestLookup_FreshIndex_PassesUnchanged(t *testing.T) {
	// A fresh index (no mutation to the cohort since BuildIndex) must
	// pass Lookup unchanged — the staleness check must never reject a
	// genuinely fresh sidecar.
	svc := buildLookupFixture(t)

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "3",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup on a fresh index must not error: %v", err)
	}
	if got := res.Rows[0]["score"].(float64); got != 30.0 {
		t.Errorf("score = %v, want 30.0", got)
	}
}

func TestLookup_StaleIndex_ErrorsIndexStale(t *testing.T) {
	// Mutating the .pulse file after BuildIndex must make the next
	// Lookup against it hard-error rather than silently returning
	// (possibly wrong) rows scanned via stale row-ids. The read-path
	// staleness check is a cheap size+mtime stat comparison (NOT a full
	// content hash — that would defeat the O(1) lookup); a re-import /
	// rewrite that changes the file size is caught by that check.
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex(id): %v", err)
	}

	// Rewrite the cohort with an extra trailing byte so the file size
	// differs from the sidecar's recorded SourceSize — the stat
	// fast-path catches this without hashing the content.
	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", append(raw, 0x00), 0644); err != nil {
		t.Fatalf("WriteFile (mutated cohort): %v", err)
	}

	_, err = svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "1",
	})
	if err == nil {
		t.Fatal("expected error looking up against a stale index")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_STALE) {
		t.Errorf("expected PULSE_INDEX_STALE, got: %v", err)
	}
}

// TestLookup_StaleIndex_ResidualGap_InPlaceEditSameSizeAndMTime locks
// the documented read-path trade-off: Lookup's staleness check is a
// cheap size+mtime stat comparison (to preserve O(1) reads), so an
// in-place edit that preserves BOTH the file size AND its mtime slips
// past Lookup — while Service.VerifyIndex, the authoritative content
// check, still catches it via a full SHA-256 recompute. Both halves of
// the contract are asserted so the intentional gap can never silently
// widen or close without a test change.
func TestLookup_StaleIndex_ResidualGap_InPlaceEditSameSizeAndMTime(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)
	buildRes, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex(id): %v", err)
	}

	// In-place content edit: same byte length (flip the last byte),
	// then force the mtime back to exactly the sidecar's recorded
	// snapshot so BOTH stat components match despite the content change.
	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 0xFF
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", mutated, 0644); err != nil {
		t.Fatalf("WriteFile (in-place mutation): %v", err)
	}
	origMTime := time.Unix(0, buildRes.Index.SourceModTime)
	if err := cfg.Fs().Chtimes("cohort.pulse", origMTime, origMTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	// Lookup's cheap stat check does NOT catch this by design — the
	// lookup proceeds as if the index were fresh (no PULSE_INDEX_STALE).
	if _, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "1",
	}); errors.HasCode(err, errors.PULSE_INDEX_STALE) {
		t.Fatalf("Lookup should NOT flag PULSE_INDEX_STALE for a size+mtime-preserving in-place edit (residual gap by design); got: %v", err)
	}

	// VerifyIndex IS authoritative — its full content hash catches the
	// divergence the stat fast-path missed.
	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if res.Fresh {
		t.Error("VerifyIndex.Fresh = true, want false — the full hash must catch the in-place content edit")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMismatch {
		t.Errorf("VerifyIndex.Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMismatch)
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
		Fingerprint:   res.Index.Fingerprint,
		Keys:          res.Index.Keys[:1], // truncated key-spec: 1 entry, not 2
		Buckets:       res.Index.Buckets,
		SourceSize:    res.Index.SourceSize,    // preserve stat snapshot so the
		SourceModTime: res.Index.SourceModTime, // read-path staleness check passes
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

// buildRegionIndexFixture builds the "region" sidecar index against
// indexTestRecords, whose region column has deliberate duplicates:
// north -> row ids {1,3}, south -> row ids {2,5}, east -> row id {4}
// (see indexTestRecords' inline comments — id order matches row order).
func buildRegionIndexFixture(t *testing.T) *Service {
	t.Helper()
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)
	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"region"}); err != nil {
		t.Fatalf("BuildIndex(region): %v", err)
	}
	return svc
}

func TestLookup_Multiplicity(t *testing.T) {
	tests := []struct {
		name         string
		multiplicity types.LookupMultiplicity
		field        string
		value        string
		wantIDs      []float64 // expected "id" values in Rows, in order
		wantErrCode  errors.Code
	}{
		{
			name:         "default (unset) assert-unique on duplicate key errors AMBIGUOUS",
			multiplicity: "",
			field:        "region",
			value:        "north",
			wantErrCode:  errors.PULSE_LOOKUP_AMBIGUOUS,
		},
		{
			name:         "explicit assert-unique on duplicate key errors AMBIGUOUS",
			multiplicity: types.LookupMultiplicityAssertUnique,
			field:        "region",
			value:        "south",
			wantErrCode:  errors.PULSE_LOOKUP_AMBIGUOUS,
		},
		{
			name:         "first on duplicate key returns lowest row-id (id=1)",
			multiplicity: types.LookupMultiplicityFirst,
			field:        "region",
			value:        "north",
			wantIDs:      []float64{1},
		},
		{
			name:         "all on duplicate key returns every match ascending row-id (id=1,3)",
			multiplicity: types.LookupMultiplicityAll,
			field:        "region",
			value:        "north",
			wantIDs:      []float64{1, 3},
		},
		{
			name:         "all on the other duplicate key returns id=2,5",
			multiplicity: types.LookupMultiplicityAll,
			field:        "region",
			value:        "south",
			wantIDs:      []float64{2, 5},
		},
		{
			name:         "unique key: default assert-unique returns exactly one row",
			multiplicity: "",
			field:        "region",
			value:        "east",
			wantIDs:      []float64{4},
		},
		{
			name:         "unique key: first returns exactly one row",
			multiplicity: types.LookupMultiplicityFirst,
			field:        "region",
			value:        "east",
			wantIDs:      []float64{4},
		},
		{
			name:         "unique key: all returns exactly one row",
			multiplicity: types.LookupMultiplicityAll,
			field:        "region",
			value:        "east",
			wantIDs:      []float64{4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := buildRegionIndexFixture(t)
			res, err := svc.Lookup(context.Background(), &types.LookupRequest{
				Cohort:        &types.Cohort{Filename: "cohort.pulse"},
				Field:         tt.field,
				Value:         tt.value,
				Multiplicity:  tt.multiplicity,
				ReturnColumns: []string{"id"},
			})

			if tt.wantErrCode != "" {
				if err == nil {
					t.Fatalf("expected error %s, got nil (res=%+v)", tt.wantErrCode, res)
				}
				if !errors.HasCode(err, tt.wantErrCode) {
					t.Fatalf("expected %s, got: %v", tt.wantErrCode, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Lookup: %v", err)
			}
			if len(res.Rows) != len(tt.wantIDs) {
				t.Fatalf("Rows count = %d, want %d (%+v)", len(res.Rows), len(tt.wantIDs), res.Rows)
			}
			for i, want := range tt.wantIDs {
				got, ok := res.Rows[i]["id"].(float64)
				if !ok {
					t.Fatalf("Rows[%d][id] missing or wrong type: %+v", i, res.Rows[i])
				}
				if got != want {
					t.Errorf("Rows[%d][id] = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestLookup_MultiplicityAll_ReturnColumnProjectionApplies(t *testing.T) {
	svc := buildRegionIndexFixture(t)

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "region",
		Value:         "north",
		Multiplicity:  types.LookupMultiplicityAll,
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("Rows count = %d, want 2", len(res.Rows))
	}
	for i, row := range res.Rows {
		if len(row) != 1 {
			t.Fatalf("Rows[%d] has %d keys, want exactly 1 (projection): %+v", i, len(row), row)
		}
		if _, ok := row["score"]; !ok {
			t.Fatalf("Rows[%d] missing projected column %q: %+v", i, "score", row)
		}
		if _, ok := row["id"]; ok {
			t.Fatalf("Rows[%d] carries unprojected column %q: %+v", i, "id", row)
		}
	}
	// id=1 -> score 10.0, id=3 -> score 30.0, ascending row-id order.
	if got := res.Rows[0]["score"].(float64); got != 10.0 {
		t.Errorf("Rows[0][score] = %v, want 10.0", got)
	}
	if got := res.Rows[1]["score"].(float64); got != 30.0 {
		t.Errorf("Rows[1][score] = %v, want 30.0", got)
	}
}

func TestLookup_UnknownMultiplicityMode(t *testing.T) {
	svc := buildRegionIndexFixture(t)

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:       &types.Cohort{Filename: "cohort.pulse"},
		Field:        "region",
		Value:        "east",
		Multiplicity: types.LookupMultiplicity("bogus_mode"),
	})
	if err == nil {
		t.Fatal("expected error for unknown multiplicity mode")
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

// TestLookup_AnchorSyntaxSingleShardWorkaround verifies the workaround
// the PULSE_INDEX_UNSUPPORTED_SHARDED fixup points at: anchoring a
// single shard out of an archive (`archive.pulse#shard.pulse`) opens
// it as a single-file cohort, so BuildIndex + Lookup work against the
// anchor path exactly as they do against any single-file `.pulse`
// cohort — even though the archive itself is rejected.
func TestLookup_AnchorSyntaxSingleShardWorkaround(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	anchorPath := "arch.pulse#a.pulse"
	if _, err := svc.BuildIndex(context.Background(), anchorPath, []string{"id"}); err != nil {
		t.Fatalf("BuildIndex(anchor): %v", err)
	}

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: anchorPath},
		Field:         "id",
		Value:         "2",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup(anchor): %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("Rows = %d, want 1", len(res.Rows))
	}
	if got := res.Rows[0]["score"].(float64); got != 20.0 {
		t.Errorf("score = %v, want 20.0 (id=2 is in shard a.pulse)", got)
	}
}

func TestLookup_ShardArchiveRejected(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	_, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Field:  "id",
		Value:  "3",
	})
	if err == nil {
		t.Fatal("expected error looking up against a shard archive")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED) {
		t.Errorf("expected PULSE_INDEX_UNSUPPORTED_SHARDED, got: %v", err)
	}
}
