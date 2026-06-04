package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/spf13/afero"
)

// stubMemberSet satisfies processing.MemberSet for tests that need only
// the metadata surface (the hot-path predicate is not exercised here —
// fieldFilterForPlan inspects the plan, not the set contents).
type stubMemberSet struct{}

func (stubMemberSet) Len() int     { return 1 }
func (stubMemberSet) Kind() string { return "stub" }

// TestFieldFilterForPlan_NarrowsToExprIdents asserts the projection
// filter built from a FILTER_EXPRESSION predicate keeps only identifiers
// the expression references.
func TestFieldFilterForPlan_NarrowsToExprIdents(t *testing.T) {
	schema := testSchema() // fields: id, score
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	keep := svc.fieldFilterForPlan(schema, filterPlan{filterExpr: "score > 25.0"})
	if keep == nil {
		t.Fatal("keep filter nil; expected narrow set")
	}
	if !keep("score") {
		t.Error("keep(score) = false, want true")
	}
	if keep("id") {
		t.Error("keep(id) = true, want false (not referenced by predicate)")
	}
}

// TestFieldFilterForPlan_MalformedExpressionFallsBack asserts a syntax
// error in the predicate widens the field set so the caller falls back
// to the full-decode path (signalled by a nil FieldFilter).
func TestFieldFilterForPlan_MalformedExpressionFallsBack(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	keep := svc.fieldFilterForPlan(schema, filterPlan{filterExpr: "not (valid expr"})
	if keep != nil {
		t.Fatal("keep filter non-nil; malformed expression should widen + return nil")
	}
}

// TestFieldFilterForPlan_IncludesMemberSetField asserts plan.includeField
// (the field tested for membership in a MemberSet predicate) joins the
// projected set even though it isn't referenced in filterExpr.
func TestFieldFilterForPlan_IncludesMemberSetField(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	keep := svc.fieldFilterForPlan(schema, filterPlan{
		set:          stubMemberSet{},
		includeField: "id",
		filterExpr:   "score > 25.0",
	})
	if keep == nil {
		t.Fatal("keep filter nil; expected narrow set")
	}
	if !keep("id") {
		t.Error("keep(id) = false, want true (member-set include field)")
	}
	if !keep("score") {
		t.Error("keep(score) = false, want true (predicate ident)")
	}
}

// TestFilterToFile_ProjectionPreservesOutput verifies that projecting
// per-record decode to predicate-referenced fields does not change the
// bytes written for surviving records. Same input + predicate must
// yield byte-identical output regardless of projection — projection is
// a decode-time optimisation, not a semantic change.
func TestFilterToFile_ProjectionPreservesOutput(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	// Run twice: once with the natural narrow predicate (projection
	// kicks in), once with a synthetic predicate that the projection
	// extractor must widen (referencing every field) — the surviving
	// rows are the same so the outputs must match.
	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "narrow.pulse", "score > 25.0"); err != nil {
		t.Fatalf("FilterToFile narrow: %v", err)
	}
	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "wide.pulse", "score > 25.0 && id > 0"); err != nil {
		t.Fatalf("FilterToFile wide: %v", err)
	}

	narrowBytes, err := afero.ReadFile(cfg.Fs(), "narrow.pulse")
	if err != nil {
		t.Fatalf("ReadFile narrow: %v", err)
	}
	wideBytes, err := afero.ReadFile(cfg.Fs(), "wide.pulse")
	if err != nil {
		t.Fatalf("ReadFile wide: %v", err)
	}
	if len(narrowBytes) != len(wideBytes) {
		t.Fatalf("output sizes differ: narrow=%d wide=%d", len(narrowBytes), len(wideBytes))
	}
	for i := range narrowBytes {
		if narrowBytes[i] != wideBytes[i] {
			t.Fatalf("output diverges at byte %d: narrow=%#x wide=%#x", i, narrowBytes[i], wideBytes[i])
		}
	}
}

// TestFieldFilterForPlan_EmptyPlanReturnsNil guards the degenerate case
// where no predicate is supplied. fieldFilterForPlan is only invoked
// from paths that have already validated a predicate exists, but the
// helper itself should not produce an empty-set FieldFilter that would
// reject every field at decode.
func TestFieldFilterForPlan_EmptyPlanReturnsNil(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	if keep := svc.fieldFilterForPlan(schema, filterPlan{}); keep != nil {
		t.Fatal("empty plan should return nil keep filter (fall through to full decode)")
	}
}

// TestFieldFilterForPlan_PassesThroughEncodingFieldFilter ensures the
// returned filter satisfies the encoding.FieldFilter type so it slots
// into ReadRecordWithWideProjected without a wrapper.
func TestFieldFilterForPlan_PassesThroughEncodingFieldFilter(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	var keep encoding.FieldFilter = svc.fieldFilterForPlan(schema, filterPlan{filterExpr: "score > 0"})
	if keep == nil {
		t.Fatal("keep nil; expected non-nil for valid predicate")
	}
}

// BenchmarkFilterToFileWideCohort mirrors BenchmarkCrosstabWideCohort —
// 200-field schema (3 referenced + 197 padding), 10K rows, single-field
// predicate. Quantifies the per-record map-write + float-conversion
// savings on the streaming filter path.
//
// Measured on darwin/arm64 (M1 Max), 200 fields × 10K rows, predicate
// `value > 5000`:
//   - baseline (ReadRecordWithWide):           744 ms/op, 554 MB/op, 8.59M allocs/op
//   - projected (ReadRecordWithWideProjected): 149 ms/op, 152 MB/op, 2.58M allocs/op
//   ≈ 5× wall time, 3.6× memory, 3.3× allocations.
//
// Savings scale with the unreferenced-field count — at 628 fields (the
// BERA visa_custom shape that motivated this PR) the unprojected path
// was the difference between an interactive filter and a five-minute
// CPU burn on a single-column predicate.
//
// To re-derive the baseline number, swap the
// ReadRecordWithWideProjected call in streamFilterRecords back to
// ReadRecordWithWide and rerun.
//
// Run with: go test ./service/ -bench BenchmarkFilterToFileWideCohort -benchmem -run=^$
func BenchmarkFilterToFileWideCohort(b *testing.B) {
	const pad = 197
	const rows = 10000

	schema := buildBenchWideSchema(pad)
	recs := buildBenchWideRecords(rows, pad)

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range recs {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				b.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "src.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := svc.FilterToFile(ctx, "src.pulse", "dst.pulse", "value > 5000"); err != nil {
			b.Fatalf("FilterToFile: %v", err)
		}
	}
}
