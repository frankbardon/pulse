package service

import (
	"context"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// --- Sample union semantics ---

func TestShardArchiveSampleGlobalOffsetLimit(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	// Helper: read scores out of a row slice in collection order.
	scores := func(rows []map[string]any) []float64 {
		out := make([]float64, len(rows))
		for i, row := range rows {
			v, _ := row["score"].(float64)
			out[i] = v
		}
		return out
	}

	// 12 rows: shard a = 10/20/30/40, shard b = 50/60/70/80, shard c = 90/100/110/120.
	cases := []struct {
		label      string
		offset     int
		limit      int
		wantScores []float64
	}{
		{
			label:      "offset=0 limit=3 inside first shard",
			offset:     0,
			limit:      3,
			wantScores: []float64{10, 20, 30},
		},
		{
			label:      "offset=4 spans into shard 2",
			offset:     4,
			limit:      3,
			wantScores: []float64{50, 60, 70},
		},
		{
			label:      "offset=2 limit spans two shards",
			offset:     2,
			limit:      4,
			wantScores: []float64{30, 40, 50, 60},
		},
		{
			label:      "limit spans all three shards",
			offset:     3,
			limit:      6,
			wantScores: []float64{40, 50, 60, 70, 80, 90},
		},
		{
			label:      "offset past end returns nothing",
			offset:     20,
			limit:      3,
			wantScores: nil,
		},
		{
			label:      "limit larger than remaining truncates",
			offset:     10,
			limit:      10,
			wantScores: []float64{110, 120},
		},
	}

	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			rows, err := svc.sampleOffsetLimit(context.Background(), "arch.pulse", c.offset, c.limit)
			if err != nil {
				t.Fatalf("sampleOffsetLimit: %v", err)
			}
			got := scores(rows)
			if len(got) != len(c.wantScores) {
				t.Fatalf("len(rows) = %d, want %d (got=%v)", len(got), len(c.wantScores), got)
			}
			for i, want := range c.wantScores {
				if got[i] != want {
					t.Errorf("row[%d] score = %v, want %v", i, got[i], want)
				}
			}
		})
	}

	// And verify the public Sample(path, n) is offset=0 limit=n —
	// matches the first n rows of the first shard for an archive.
	t.Run("public Sample matches offset=0 path", func(t *testing.T) {
		rows, err := svc.Sample(context.Background(), "arch.pulse", 5)
		if err != nil {
			t.Fatalf("Sample: %v", err)
		}
		got := scores(rows)
		want := []float64{10, 20, 30, 40, 50}
		if len(got) != 5 {
			t.Fatalf("Sample len = %d, want 5", len(got))
		}
		for i, w := range want {
			if got[i] != w {
				t.Errorf("Sample row[%d] = %v, want %v", i, got[i], w)
			}
		}
	})
}

// TestShardArchiveSample_SingleFileUnchanged confirms the single-file
// Sample path still returns the first n rows in record order — the
// scanIterator dispatch leaves single-file cohorts on the
// streamingIterator branch.
func TestShardArchiveSample_SingleFileUnchanged(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "test.pulse", schema, testRecords())
	svc := New(cfg)

	rows, err := svc.Sample(context.Background(), "test.pulse", 3)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	for i, row := range rows {
		score, _ := row["score"].(float64)
		want := float64((i + 1) * 10)
		if score != want {
			t.Errorf("row[%d] score = %v, want %v", i, score, want)
		}
	}
}

// --- Facet (simple distinct-values) union semantics ---

// TestShardArchiveFacet_CategoricalUsesCanonicalDict confirms the
// categorical fast path reads from the canonical schema's dictionary
// (which is the union of every shard's dict thanks to the append-only
// prefix rule enforced at insert time). No record streaming is needed.
func TestShardArchiveFacet_CategoricalUsesCanonicalDict(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "green", "blue", "yellow"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	concat := [][]uint64{
		{0, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
		{3, math.Float64bits(40.0)},
		{0, math.Float64bits(50.0)},
		{1, math.Float64bits(60.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:2]},
		{Name: "b.pulse", Records: concat[2:4]},
		{Name: "c.pulse", Records: concat[4:6]},
	}
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	values, err := svc.Facet(context.Background(), "arch.pulse", "color")
	if err != nil {
		t.Fatalf("Facet archive: %v", err)
	}
	wantSet := map[string]struct{}{"red": {}, "green": {}, "blue": {}, "yellow": {}}
	if len(values) != len(wantSet) {
		t.Fatalf("len(values) = %d, want %d (got=%v)", len(values), len(wantSet), values)
	}
	for _, v := range values {
		if _, ok := wantSet[v]; !ok {
			t.Errorf("unexpected value %q", v)
		}
	}

	// Single-file parity: concatenated equivalent returns the same set.
	parity, err := svc.Facet(context.Background(), "arch.pulse.concat", "color")
	if err != nil {
		t.Fatalf("Facet concat: %v", err)
	}
	if !sameStringSet(values, parity) {
		t.Errorf("archive=%v concat=%v", values, parity)
	}
}

// TestShardArchiveFacet_NumericUnion confirms the numeric path
// (no dictionary fast path) walks the union of all shards and returns
// distinct float values across the whole archive.
func TestShardArchiveFacet_NumericUnion(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	got, err := svc.Facet(context.Background(), "arch.pulse", "score")
	if err != nil {
		t.Fatalf("Facet archive: %v", err)
	}
	want, err := svc.Facet(context.Background(), "arch.pulse.concat", "score")
	if err != nil {
		t.Fatalf("Facet concat: %v", err)
	}
	if !sameStringSet(got, want) {
		t.Errorf("archive=%v concat=%v", got, want)
	}
	if len(got) != 12 {
		t.Errorf("len(got) = %d, want 12 (got=%v)", len(got), got)
	}
}

func TestShardArchiveFacet_SingleFileUnchanged(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "green", "blue"} {
		dict.Add(v)
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	records := [][]uint64{
		{0, math.Float64bits(10.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(30.0)},
	}
	cfg := setupTestFS(t, "test.pulse", schema, records)
	svc := New(cfg)

	got, err := svc.Facet(context.Background(), "test.pulse", "color")
	if err != nil {
		t.Fatalf("Facet: %v", err)
	}
	if !sameStringSet(got, []string{"red", "green", "blue"}) {
		t.Errorf("got=%v want=[red green blue]", got)
	}
}

// --- FacetSchema union semantics ---

func TestShardArchiveFacetSchemaMatches(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	t.Run("numeric stats match concatenated", func(t *testing.T) {
		reqA := &types.FacetRequest{
			Cohort: &types.Cohort{Filename: "arch.pulse"},
			Fields: []string{"score"},
		}
		reqB := &types.FacetRequest{
			Cohort: &types.Cohort{Filename: "arch.pulse.concat"},
			Fields: []string{"score"},
		}
		respA, err := svc.FacetSchema(context.Background(), reqA)
		if err != nil {
			t.Fatalf("FacetSchema archive: %v", err)
		}
		respB, err := svc.FacetSchema(context.Background(), reqB)
		if err != nil {
			t.Fatalf("FacetSchema concat: %v", err)
		}
		a := respA.Fields["score"].Numeric
		b := respB.Fields["score"].Numeric
		if a.Count != b.Count || a.Count != 12 {
			t.Errorf("count: archive=%d concat=%d (want 12)", a.Count, b.Count)
		}
		if a.Sum != b.Sum {
			t.Errorf("sum: archive=%v concat=%v", a.Sum, b.Sum)
		}
		if a.Min != b.Min || a.Max != b.Max {
			t.Errorf("min/max: archive=(%v,%v) concat=(%v,%v)", a.Min, a.Max, b.Min, b.Max)
		}
		if math.Abs(a.Mean-b.Mean) > 1e-9 {
			t.Errorf("mean: archive=%v concat=%v (ULP-tolerant)", a.Mean, b.Mean)
		}
		if math.Abs(a.StdDev-b.StdDev) > 1e-9 {
			t.Errorf("stddev: archive=%v concat=%v", a.StdDev, b.StdDev)
		}
		if respA.TotalRecords != respB.TotalRecords || respA.TotalRecords != 12 {
			t.Errorf("total: archive=%d concat=%d (want 12)", respA.TotalRecords, respB.TotalRecords)
		}
	})

	t.Run("percentiles match concatenated (forced buffered)", func(t *testing.T) {
		percentiles := []float64{0.25, 0.5, 0.75, 0.9}
		reqA := &types.FacetRequest{
			Cohort:             &types.Cohort{Filename: "arch.pulse"},
			Fields:             []string{"score"},
			NumericPercentiles: percentiles,
		}
		reqB := &types.FacetRequest{
			Cohort:             &types.Cohort{Filename: "arch.pulse.concat"},
			Fields:             []string{"score"},
			NumericPercentiles: percentiles,
		}
		respA, err := svc.FacetSchema(context.Background(), reqA)
		if err != nil {
			t.Fatalf("FacetSchema archive: %v", err)
		}
		respB, err := svc.FacetSchema(context.Background(), reqB)
		if err != nil {
			t.Fatalf("FacetSchema concat: %v", err)
		}
		pA := respA.Fields["score"].Numeric.Percentiles
		pB := respB.Fields["score"].Numeric.Percentiles
		if len(pA) != len(pB) || len(pA) != len(percentiles) {
			t.Fatalf("percentile map sizes: archive=%d concat=%d (want %d)", len(pA), len(pB), len(percentiles))
		}
		for k, va := range pA {
			vb, ok := pB[k]
			if !ok {
				t.Errorf("key %q missing in concat result", k)
				continue
			}
			if math.Abs(va-vb) > 1e-9 {
				t.Errorf("percentile %s: archive=%v concat=%v", k, va, vb)
			}
		}
	})

	t.Run("histogram matches concatenated (single-pass)", func(t *testing.T) {
		reqA := &types.FacetRequest{
			Cohort:           &types.Cohort{Filename: "arch.pulse"},
			Fields:           []string{"score"},
			IncludeHistogram: true,
			HistogramRange:   [2]float64{0, 120},
			HistogramBins:    6,
		}
		reqB := &types.FacetRequest{
			Cohort:           &types.Cohort{Filename: "arch.pulse.concat"},
			Fields:           []string{"score"},
			IncludeHistogram: true,
			HistogramRange:   [2]float64{0, 120},
			HistogramBins:    6,
		}
		respA, err := svc.FacetSchema(context.Background(), reqA)
		if err != nil {
			t.Fatalf("FacetSchema archive: %v", err)
		}
		respB, err := svc.FacetSchema(context.Background(), reqB)
		if err != nil {
			t.Fatalf("FacetSchema concat: %v", err)
		}
		hA := respA.Fields["score"].Numeric.Histogram
		hB := respB.Fields["score"].Numeric.Histogram
		if hA == nil || hB == nil {
			t.Fatalf("histogram nil: archive=%v concat=%v", hA, hB)
		}
		if len(hA.Bins) != len(hB.Bins) || len(hA.Bins) != 6 {
			t.Fatalf("bins len: archive=%d concat=%d (want 6)", len(hA.Bins), len(hB.Bins))
		}
		var totalA, totalB int64
		for i := range hA.Bins {
			if hA.Bins[i] != hB.Bins[i] {
				t.Errorf("bin[%d]: archive=%d concat=%d", i, hA.Bins[i], hB.Bins[i])
			}
			totalA += hA.Bins[i]
			totalB += hB.Bins[i]
		}
		if totalA != 12 || totalB != 12 {
			t.Errorf("histogram totals: archive=%d concat=%d (want 12)", totalA, totalB)
		}
	})
}

// TestShardArchiveFacetSchema_CategoricalCounts confirms the dict-counts
// path accumulates discrete value counts across the union of shards
// identically to the concatenated single-file equivalent.
func TestShardArchiveFacetSchema_CategoricalCounts(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "green", "blue"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	concat := [][]uint64{
		{0, math.Float64bits(10.0)},
		{0, math.Float64bits(15.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(25.0)},
		{0, math.Float64bits(30.0)},
		{1, math.Float64bits(35.0)},
		{0, math.Float64bits(40.0)},
		{2, math.Float64bits(45.0)},
		{1, math.Float64bits(50.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:3]},
		{Name: "b.pulse", Records: concat[3:6]},
		{Name: "c.pulse", Records: concat[6:9]},
	}
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	reqA := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Fields: []string{"color"},
	}
	reqB := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: "arch.pulse.concat"},
		Fields: []string{"color"},
	}
	respA, err := svc.FacetSchema(context.Background(), reqA)
	if err != nil {
		t.Fatalf("FacetSchema archive: %v", err)
	}
	respB, err := svc.FacetSchema(context.Background(), reqB)
	if err != nil {
		t.Fatalf("FacetSchema concat: %v", err)
	}
	asMap := func(ff *types.FacetField) map[string]int64 {
		out := make(map[string]int64)
		for _, v := range ff.Discrete.Values {
			out[v.Value] = v.Count
		}
		return out
	}
	mA := asMap(respA.Fields["color"])
	mB := asMap(respB.Fields["color"])
	if len(mA) != len(mB) {
		t.Fatalf("len(mA)=%d len(mB)=%d", len(mA), len(mB))
	}
	for k, v := range mB {
		if mA[k] != v {
			t.Errorf("color %q: archive=%d concat=%d", k, mA[k], v)
		}
	}
	wantCounts := map[string]int64{"red": 4, "green": 3, "blue": 2}
	for k, v := range wantCounts {
		if mA[k] != v {
			t.Errorf("color %q: archive=%d, want %d", k, mA[k], v)
		}
	}
}

// TestShardArchiveFacetSchema_SingleFileUnchanged confirms single-file
// FacetSchema results are unaffected by the newScanIter dispatch.
func TestShardArchiveFacetSchema_SingleFileUnchanged(t *testing.T) {
	svc, path := buildCategoricalCohort(t)

	req := &types.FacetRequest{
		Cohort: &types.Cohort{Filename: path},
		Fields: []string{"region", "score"},
	}
	resp, err := svc.FacetSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if resp.TotalRecords != 12 {
		t.Errorf("total = %d, want 12", resp.TotalRecords)
	}
	if resp.Fields["region"].Discrete.DistinctCount != 4 {
		t.Errorf("region distinct = %d, want 4", resp.Fields["region"].Discrete.DistinctCount)
	}
	if resp.Fields["score"].Numeric.Count != 12 {
		t.Errorf("score count = %d, want 12", resp.Fields["score"].Numeric.Count)
	}
}

// --- helpers ---

// sameStringSet compares two slices ignoring order.
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	ac := append([]string{}, a...)
	bc := append([]string{}, b...)
	sort.Strings(ac)
	sort.Strings(bc)
	for i := range ac {
		if ac[i] != bc[i] {
			return false
		}
	}
	return true
}
