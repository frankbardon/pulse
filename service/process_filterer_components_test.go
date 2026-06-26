package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// nullableScoreCohort writes a 5-row score cohort with a non-nullable
// f64 score column for the small / empty cases. Every row has a valid
// score; the all-null variant below builds its own bitmap for the
// null-input coverage cases. Kept non-nullable here so writePulseFile
// (which doesn't emit the per-record bitmap) produces a well-formed
// record stride; switching this to Nullable: true desynchronises the
// decoder against the writer.
func nullableScoreCohort(t *testing.T, path string) *Service {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
	}
	cfg := setupTestFS(t, path, schema, records)
	return New(cfg)
}

// allNullScoreCohort writes a 5-row cohort whose score column is null
// on every row. The non-nullable id column carries valid u32 ids so
// the cohort still decodes; per-record null bitmap marks score as
// null at every row index.
func allNullScoreCohort(t *testing.T, path string) *Service {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for i := 1; i <= 5; i++ {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(i)); err != nil {
			t.Fatalf("WriteFieldValue id: %v", err)
		}
		// Write a zero payload byte sequence for score (8 bytes of zero);
		// the null bitmap below tells the decoder to skip it.
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, 0); err != nil {
			t.Fatalf("WriteFieldValue score: %v", err)
		}
		if bmSize := schema.BitmapByteSize(); bmSize > 0 {
			bm := make([]byte, bmSize)
			// score lives at field index 1.
			encoding.BitmapSetNull(bm, 1)
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return New(cfg)
}

// nullableSetCohort writes a 5-row cohort with a non-nullable set_u8
// tags column over a 4-entry {VISA, MC, AMEX, DISC} dictionary.
// Drives every FILTER_SET_* small / empty case. Kept non-nullable so
// the writePulseFile record stride lines up with the decoder; the
// all-null variant below emits its own bitmap.
func nullableSetCohort(t *testing.T, path string) *Service {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
		},
	}
	records := [][]uint64{
		{0b0001}, // VISA
		{0b0011}, // VISA, MC
		{0b0100}, // AMEX
		{0b1000}, // DISC
		{0b0110}, // MC, AMEX
	}
	cfg := setupTestFS(t, path, schema, records)
	return New(cfg)
}

// allNullSetCohort writes a 5-row cohort whose tags column is null on
// every row. Used by the FILTER_SET_* all-null coverage cases.
func allNullSetCohort(t *testing.T, path string) *Service {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict, Nullable: true},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeSetU8, 0); err != nil {
			t.Fatalf("WriteFieldValue tags: %v", err)
		}
		if bmSize := schema.BitmapByteSize(); bmSize > 0 {
			bm := make([]byte, bmSize)
			encoding.BitmapSetNull(bm, 0)
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatalf("WriteBitmap: %v", err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return New(cfg)
}

// runProcessForFiltererComponents drives svc.Process and returns the
// FiltererComponents slice. Fails the test if Components is unset.
func runProcessForFiltererComponents(t *testing.T, svc *Service, req *types.Request) []types.FiltererComponents {
	t.Helper()
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated")
	}
	return resp.Components.Filterers
}

// assertFiltererSlot validates a single FiltererComponents entry
// against expected {n_in, n_out, n_null_input} counters. Keeps the
// per-filterer assertions grep-discoverable so a counter drift surfaces
// at the responsible test row, not an aggregate "slice mismatch".
func assertFiltererSlot(t *testing.T, label string, got types.FiltererComponents, wantNIn, wantNOut, wantNNullInput int) {
	t.Helper()
	if got.NIn != wantNIn {
		t.Errorf("%s: NIn = %d, want %d", label, got.NIn, wantNIn)
	}
	if got.NOut != wantNOut {
		t.Errorf("%s: NOut = %d, want %d", label, got.NOut, wantNOut)
	}
	if got.NNullInput != wantNNullInput {
		t.Errorf("%s: NNullInput = %d, want %d", label, got.NNullInput, wantNNullInput)
	}
}

// TestService_Process_FiltererComponents covers every registered
// filterer (11 today) under three input shapes: small (filter admits
// a subset), empty (filter rejects all), all-null (every row's
// filtered field is null on input). Counter expectations are derived
// from the per-row semantics each filterer enforces:
//
//   - FILTER_INCLUDE / FILTER_EXCLUDE: null inputs drop / pass.
//   - FILTER_RANGE: null inputs drop.
//   - FILTER_EXPRESSION: no Field, so NNullInput stays 0.
//   - FILTER_NULL is_null / is_not_null: nulls drive the pass decision.
//   - FILTER_TRUE / FILTER_FALSE strict mode: requires packed_bool;
//     truthy mode coerces — the all-null case uses truthy semantics so
//     the empty record set still flows.
//   - FILTER_SET_*: nulls drop for ANY / ALL / EQUALS and pass for NONE.
func TestService_Process_FiltererComponents(t *testing.T) {
	t.Run("FILTER_INCLUDE/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_include_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_include_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"10", "20", "30"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		if len(got) != 1 {
			t.Fatalf("Filterers slots = %d, want 1", len(got))
		}
		assertFiltererSlot(t, "FILTER_INCLUDE/small", got[0], 5, 3, 0)
	})
	t.Run("FILTER_INCLUDE/empty", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_include_empty.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_include_empty.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"999"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_INCLUDE/empty", got[0], 5, 0, 0)
	})
	t.Run("FILTER_INCLUDE/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_include_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_include_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"10"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Nulls drop FILTER_INCLUDE; n_null_input == n_in.
		assertFiltererSlot(t, "FILTER_INCLUDE/all-null", got[0], 5, 0, 5)
	})

	t.Run("FILTER_EXCLUDE/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_exclude_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_exclude_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "score", Values: []string{"10", "20"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_EXCLUDE/small", got[0], 5, 3, 0)
	})
	t.Run("FILTER_EXCLUDE/empty", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_exclude_empty.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_exclude_empty.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "score", Values: []string{"10", "20", "30", "40", "50"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_EXCLUDE/empty", got[0], 5, 0, 0)
	})
	t.Run("FILTER_EXCLUDE/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_exclude_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_exclude_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXCLUDE, Field: "score", Values: []string{"10"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Nulls pass FILTER_EXCLUDE (the value is not in the rejection
		// list — the filter cannot reject what is not there).
		assertFiltererSlot(t, "FILTER_EXCLUDE/all-null", got[0], 5, 5, 5)
	})

	t.Run("FILTER_RANGE/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_range_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_range_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "score", Values: []string{"20", "40"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_RANGE/small", got[0], 5, 3, 0)
	})
	t.Run("FILTER_RANGE/empty", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_range_empty.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_range_empty.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "score", Values: []string{"100", "200"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_RANGE/empty", got[0], 5, 0, 0)
	})
	t.Run("FILTER_RANGE/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_range_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_range_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "score", Values: []string{"0", "100"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Nulls drop FILTER_RANGE.
		assertFiltererSlot(t, "FILTER_RANGE/all-null", got[0], 5, 0, 5)
	})

	t.Run("FILTER_EXPRESSION/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_expr_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_expr_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXPRESSION, Expression: "score > 20"},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// score > 20 admits {30, 40, 50}.
		// NNullInput is 0 — FILTER_EXPRESSION has no Field.
		assertFiltererSlot(t, "FILTER_EXPRESSION/small", got[0], 5, 3, 0)
	})
	t.Run("FILTER_EXPRESSION/empty", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_expr_empty.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_expr_empty.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_EXPRESSION, Expression: "score > 999"},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_EXPRESSION/empty", got[0], 5, 0, 0)
	})

	t.Run("FILTER_NULL/is_not_null/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_null_isnot_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_null_isnot_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_NULL, Field: "score", Values: []string{"is_not_null"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// No nulls in the cohort; every row passes is_not_null.
		assertFiltererSlot(t, "FILTER_NULL/is_not_null/small", got[0], 5, 5, 0)
	})
	t.Run("FILTER_NULL/is_null/empty", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_null_is_empty.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_null_is_empty.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_NULL, Field: "score", Values: []string{"is_null"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// No nulls in the cohort; every row rejects is_null.
		assertFiltererSlot(t, "FILTER_NULL/is_null/empty", got[0], 5, 0, 0)
	})
	t.Run("FILTER_NULL/is_null/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_null_is_allnull.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_null_is_allnull.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_NULL, Field: "score", Values: []string{"is_null"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Every row is null → every row passes is_null AND every
		// row is null-input.
		assertFiltererSlot(t, "FILTER_NULL/is_null/all-null", got[0], 5, 5, 5)
	})

	// Strict mode requires a packed_bool field; truthy mode coerces any
	// non-null numeric. We exercise truthy so the small/empty/all-null
	// matrix lines up against the score cohort.
	t.Run("FILTER_TRUE/truthy/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_true_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_true_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_TRUE, Field: "score", Values: []string{"truthy"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Every non-null score is truthy → every row passes.
		assertFiltererSlot(t, "FILTER_TRUE/truthy/small", got[0], 5, 5, 0)
	})
	t.Run("FILTER_TRUE/truthy/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_true_allnull.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_true_allnull.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_TRUE, Field: "score", Values: []string{"truthy"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Null coerces to false → every row rejected.
		assertFiltererSlot(t, "FILTER_TRUE/truthy/all-null", got[0], 5, 0, 5)
	})
	t.Run("FILTER_FALSE/truthy/small", func(t *testing.T) {
		svc := nullableScoreCohort(t, "fcomp_false_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_false_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_FALSE, Field: "score", Values: []string{"truthy"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Every non-null score is truthy → FALSE rejects every row.
		assertFiltererSlot(t, "FILTER_FALSE/truthy/small", got[0], 5, 0, 0)
	})
	t.Run("FILTER_FALSE/truthy/all-null", func(t *testing.T) {
		svc := allNullScoreCohort(t, "fcomp_false_allnull.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_false_allnull.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_FALSE, Field: "score", Values: []string{"truthy"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Null coerces to false → FALSE admits every row.
		assertFiltererSlot(t, "FILTER_FALSE/truthy/all-null", got[0], 5, 5, 5)
	})

	t.Run("FILTER_SET_CONTAINS_ANY/small", func(t *testing.T) {
		svc := nullableSetCohort(t, "fcomp_setany_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setany_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_ANY, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// VISA bit is set on rows 0 (0b0001) and 1 (0b0011) → 2 rows pass.
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_ANY/small", got[0], 5, 2, 0)
	})
	t.Run("FILTER_SET_CONTAINS_ANY/all-null", func(t *testing.T) {
		svc := allNullSetCohort(t, "fcomp_setany_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setany_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_ANY, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Nulls drop FILTER_SET_CONTAINS_ANY.
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_ANY/all-null", got[0], 5, 0, 5)
	})

	t.Run("FILTER_SET_CONTAINS_ALL/small", func(t *testing.T) {
		svc := nullableSetCohort(t, "fcomp_setall_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setall_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_ALL, Field: "tags", Values: []string{"VISA", "MC"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Both VISA & MC bits set only on row 1 (0b0011).
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_ALL/small", got[0], 5, 1, 0)
	})
	t.Run("FILTER_SET_CONTAINS_ALL/all-null", func(t *testing.T) {
		svc := allNullSetCohort(t, "fcomp_setall_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setall_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_ALL, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_ALL/all-null", got[0], 5, 0, 5)
	})

	t.Run("FILTER_SET_CONTAINS_NONE/small", func(t *testing.T) {
		svc := nullableSetCohort(t, "fcomp_setnone_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setnone_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_NONE, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// VISA bit absent on rows 2 (0b0100), 3 (0b1000), 4 (0b0110) → 3 pass.
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_NONE/small", got[0], 5, 3, 0)
	})
	t.Run("FILTER_SET_CONTAINS_NONE/all-null", func(t *testing.T) {
		svc := allNullSetCohort(t, "fcomp_setnone_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_setnone_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_CONTAINS_NONE, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Nulls PASS FILTER_SET_CONTAINS_NONE (matches the in-band
		// convention documented at processing/filterer_set.go).
		assertFiltererSlot(t, "FILTER_SET_CONTAINS_NONE/all-null", got[0], 5, 5, 5)
	})

	t.Run("FILTER_SET_EQUALS/small", func(t *testing.T) {
		svc := nullableSetCohort(t, "fcomp_seteq_small.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_seteq_small.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_EQUALS, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		// Only row 0 (0b0001) equals exactly {VISA}.
		assertFiltererSlot(t, "FILTER_SET_EQUALS/small", got[0], 5, 1, 0)
	})
	t.Run("FILTER_SET_EQUALS/all-null", func(t *testing.T) {
		svc := allNullSetCohort(t, "fcomp_seteq_null.pulse")
		req := &types.Request{
			Cohort: &types.Cohort{Filename: "fcomp_seteq_null.pulse"},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_SET_EQUALS, Field: "tags", Values: []string{"VISA"}},
			},
		}
		got := runProcessForFiltererComponents(t, svc, req)
		assertFiltererSlot(t, "FILTER_SET_EQUALS/all-null", got[0], 5, 0, 5)
	})
}

// TestService_Process_FiltererComponents_PipelineNInInvariant chains
// three filterers and asserts the n_in invariant called out in the
// story acceptance criteria: n_in[0] equals the post-feature-pass
// total observed by the first slot, and n_in[i] == n_out[i-1] for
// every i > 0.
//
// Pipeline:
//  1. FILTER_RANGE [10, 40] — admits {10, 20, 30, 40} → NOut = 4.
//  2. FILTER_EXCLUDE {10}   — rejects 10 → NOut = 3.
//  3. FILTER_INCLUDE {20, 40} — admits {20, 40} → NOut = 2.
func TestService_Process_FiltererComponents_PipelineNInInvariant(t *testing.T) {
	svc := nullableScoreCohort(t, "fcomp_pipeline.pulse")
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "fcomp_pipeline.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "score", Values: []string{"10", "40"}},
			{Type: types.FILTER_EXCLUDE, Field: "score", Values: []string{"10"}},
			{Type: types.FILTER_INCLUDE, Field: "score", Values: []string{"20", "40"}},
		},
	}
	got := runProcessForFiltererComponents(t, svc, req)
	if len(got) != 3 {
		t.Fatalf("Filterers slots = %d, want 3", len(got))
	}

	// Slot 0: 5 rows in, 4 admitted by FILTER_RANGE.
	assertFiltererSlot(t, "pipeline[0]/FILTER_RANGE", got[0], 5, 4, 0)
	// Slot 1: 4 rows in (= slot 0 NOut), 3 admitted by FILTER_EXCLUDE.
	assertFiltererSlot(t, "pipeline[1]/FILTER_EXCLUDE", got[1], 4, 3, 0)
	// Slot 2: 3 rows in (= slot 1 NOut), 2 admitted by FILTER_INCLUDE.
	assertFiltererSlot(t, "pipeline[2]/FILTER_INCLUDE", got[2], 3, 2, 0)

	// n_in invariant: n_in[i] == n_out[i-1] for i > 0.
	for i := 1; i < len(got); i++ {
		if got[i].NIn != got[i-1].NOut {
			t.Errorf("n_in invariant violated at slot %d: n_in=%d, prior n_out=%d",
				i, got[i].NIn, got[i-1].NOut)
		}
	}
}

func TestService_Process_FiltererComponents_NoFilterers(t *testing.T) {
	svc, _ := componentsScoreCohort(t)
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "scores.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components == nil {
		t.Fatalf("Response.Components nil; want populated (AggregationComponents still emit)")
	}
	if got := resp.Components.Filterers; got != nil {
		t.Errorf("Filterers = %v, want nil for the no-filterer fast path", got)
	}
}
