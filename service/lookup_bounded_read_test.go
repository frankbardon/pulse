package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
)

// lookupBoundedReadSchema is a small fixed-width schema (id u32, score
// f64, region categorical_u8) — identical shape regardless of record
// count, so any read-byte delta between a small and a much larger
// cohort built from it can only come from record-count-dependent
// reading, never from a wider header/schema/dictionary prefix.
func lookupBoundedReadSchema() *encoding.Schema {
	regionDict := encoding.NewDictionary()
	regionDict.Add("north")
	regionDict.Add("south")
	regionDict.Add("east")

	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 12, CsvColumnIdx: 2, Dictionary: regionDict},
		},
	}
}

// lookupBoundedReadRecords generates n records with unique ascending ids
// (1..n) so a point lookup by id is always assert-unique.
func lookupBoundedReadRecords(n int) [][]uint64 {
	out := make([][]uint64, n)
	for i := 0; i < n; i++ {
		out[i] = []uint64{
			uint64(i + 1),
			math.Float64bits(float64(i+1) * 1.5),
			uint64(i % 3),
		}
	}
	return out
}

// TestLookup_BoundedReadBytes_DoesNotScaleWithCohortSize is the
// load-bearing O(1) proof for E7-S2: it builds two cohorts sharing the
// exact same fixed-width schema — one small (100 records), one ~20x
// larger (2000 records) — indexes both on "id", then measures the total
// bytes a single Lookup call reads off disk (via byteCountingFs, the
// same Read/ReadAt byte-tally wrapper TestCountRecords_HeaderOnly uses
// in count_test.go, which observes every Read/ReadAt on every file
// handle Lookup opens: the sidecar index AND the cohort file). If
// Lookup were still O(n) (a full afero.ReadFile of the cohort, or a
// full encoding.ReadIndexFile of the sidecar), the larger cohort's byte
// count would scale with its ~20x larger record count and file size.
// Because the record region and the bucket-data region are both
// seek-addressed, the byte count instead stays effectively IDENTICAL
// between the two cohorts — bounded by the (record-count-independent)
// header+schema prefix, the bucket-data-free index prefix, one
// bucket's data, and one matched record's bytes.
func TestLookup_BoundedReadBytes_DoesNotScaleWithCohortSize(t *testing.T) {
	ctx := context.Background()
	schema := lookupBoundedReadSchema()

	const smallN = 100
	const largeN = 2000 // 20x smallN

	small := setupTestFS(t, "small.pulse", schema, lookupBoundedReadRecords(smallN))
	large := setupTestFS(t, "large.pulse", schema, lookupBoundedReadRecords(largeN))

	smallSvc := New(small)
	largeSvc := New(large)

	if _, err := smallSvc.BuildIndex(ctx, "small.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex (small): %v", err)
	}
	if _, err := largeSvc.BuildIndex(ctx, "large.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex (large): %v", err)
	}

	smallCounting := &byteCountingFs{Fs: small.Fs()}
	largeCounting := &byteCountingFs{Fs: large.Fs()}

	smallCountingCfg, err := fs.New(fs.WithFs(smallCounting))
	if err != nil {
		t.Fatalf("fs.New (small counting): %v", err)
	}
	largeCountingCfg, err := fs.New(fs.WithFs(largeCounting))
	if err != nil {
		t.Fatalf("fs.New (large counting): %v", err)
	}

	smallLookupSvc := New(smallCountingCfg)
	largeLookupSvc := New(largeCountingCfg)

	smallRes, err := smallLookupSvc.Lookup(ctx, &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "small.pulse"},
		Field:         "id",
		Value:         "50",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup (small): %v", err)
	}
	if got := smallRes.Rows[0]["score"].(float64); got != 75.0 {
		t.Fatalf("small lookup score = %v, want 75.0 (id=50 -> 50*1.5)", got)
	}
	smallBytes := smallCounting.bytes

	largeRes, err := largeLookupSvc.Lookup(ctx, &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "large.pulse"},
		Field:         "id",
		Value:         "1000",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup (large): %v", err)
	}
	if got := largeRes.Rows[0]["score"].(float64); got != 1500.0 {
		t.Fatalf("large lookup score = %v, want 1500.0 (id=1000 -> 1000*1.5)", got)
	}
	largeBytes := largeCounting.bytes

	if smallBytes <= 0 {
		t.Fatalf("positive control failed: small lookup read 0 bytes — byteCountingFs likely not wired in")
	}

	// The load-bearing assertion: bytes read must be BOUNDED, not
	// linear in cohort size. largeN/smallN == 20x; an O(n) read (whole
	// cohort or whole index) would show a ~20x byte delta. Allow a
	// generous 3x band above the small measurement — comfortably wide
	// of any legitimate constant-factor noise (e.g. a differently-sized
	// bucket-offset seek target), while remaining an order of magnitude
	// tighter than the 20x an O(n) regression would produce.
	maxAllowed := smallBytes * 3
	if largeBytes > maxAllowed {
		t.Errorf("large-cohort (%dx records) lookup read %d bytes, small-cohort read %d bytes — "+
			"read volume scales with cohort size (want bounded, e.g. <= %d); "+
			"this indicates a regression back to a full-file/full-index read on the Lookup path",
			largeN/smallN, largeBytes, smallBytes, maxAllowed)
	}
}
