package service

import (
	"archive/zip"
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// writeSinglePulse writes a complete single-file .pulse (header +
// schema + records) for use either as a flat cohort or as a shard
// payload inside an archive.
func writeSinglePulse(t *testing.T, schema *encoding.Schema, records [][]uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range records {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				t.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	return buf.Bytes()
}

// buildShardArchive composes a zip archive containing the canonical
// `_schema.pulse` entry plus the named shards in the supplied order.
// Each shard payload is a complete single-file `.pulse` (header +
// schema + records). The total record count is folded into the
// schema-doc's AggregateRecordCount for cohort.Open sanity.
func buildShardArchive(t *testing.T, schema *encoding.Schema, shards []struct {
	Name    string
	Records [][]uint64
}) []byte {
	t.Helper()

	// _schema.pulse: header-only Pulse + SHRD extension.
	var schemaDoc bytes.Buffer
	var total uint64
	for _, s := range shards {
		total += uint64(len(s.Records))
	}
	if err := encoding.WriteSchemaDoc(&schemaDoc, schema, total, uint16(len(shards))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}

	write(encoding.ReservedSchemaName, schemaDoc.Bytes())
	for _, s := range shards {
		write(s.Name, writeSinglePulse(t, schema, s.Records))
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// twoShardScoreSchema is a fixed schema used by the multi-shard
// arithmetic tests: u32 id + f64 score.
func twoShardScoreSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
		},
	}
}

// canonicalThreeShards splits 12 score rows across three shards so
// every test verifies cross-shard accumulation rather than single-
// shard pass-through.
func canonicalThreeShards() (*encoding.Schema, []struct {
	Name    string
	Records [][]uint64
}, [][]uint64) {
	schema := twoShardScoreSchema()
	concat := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(20.0)},
		{3, math.Float64bits(30.0)},
		{4, math.Float64bits(40.0)},
		{5, math.Float64bits(50.0)},
		{6, math.Float64bits(60.0)},
		{7, math.Float64bits(70.0)},
		{8, math.Float64bits(80.0)},
		{9, math.Float64bits(90.0)},
		{10, math.Float64bits(100.0)},
		{11, math.Float64bits(110.0)},
		{12, math.Float64bits(120.0)},
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:4]},
		{Name: "b.pulse", Records: concat[4:8]},
		{Name: "c.pulse", Records: concat[8:12]},
	}
	return schema, shards, concat
}

// setupShardArchive writes a built archive to a fresh memfs at the
// given path and returns a Service wired to it. The matching single-
// file cohort is written alongside as `path + ".concat"` for parity
// comparisons.
func setupShardArchive(t *testing.T, path string, schema *encoding.Schema, shards []struct {
	Name    string
	Records [][]uint64
}, concat [][]uint64) (*Service, *fs.Config) {
	t.Helper()
	cfg := fs.NewMemMap()
	archive := buildShardArchive(t, schema, shards)
	if err := afero.WriteFile(cfg.Fs(), path, archive, 0644); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	single := writeSinglePulse(t, schema, concat)
	if err := afero.WriteFile(cfg.Fs(), path+".concat", single, 0644); err != nil {
		t.Fatalf("WriteFile concat: %v", err)
	}
	return New(cfg), cfg
}

// Required CI gate per the sharding design contract: associative+
// commutative ops over the shard archive are byte-equal to the same
// ops over a concatenated single-file cohort; AGG_MEAN is ULP-tolerant
// (Welford-Pébaÿ).
func TestShardArchiveProcessSums(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	cases := []struct {
		label string
		agg   *types.Aggregation
		want  float64
		ulp   bool
	}{
		{"count", &types.Aggregation{Type: types.AGG_COUNT, Field: "score", Label: "v"}, 12, false},
		{"sum", &types.Aggregation{Type: types.AGG_SUM, Field: "score", Label: "v"}, 780, false},
		{"min", &types.Aggregation{Type: types.AGG_MIN, Field: "score", Label: "v"}, 10, false},
		{"max", &types.Aggregation{Type: types.AGG_MAX, Field: "score", Label: "v"}, 120, false},
		{"mean", &types.Aggregation{Type: types.AGG_AVERAGE, Field: "score", Label: "v"}, 65, true},
	}

	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			reqA := &types.Request{
				Cohort:       &types.Cohort{Filename: "arch.pulse"},
				Aggregations: []*types.Aggregation{c.agg},
			}
			reqB := &types.Request{
				Cohort:       &types.Cohort{Filename: "arch.pulse.concat"},
				Aggregations: []*types.Aggregation{c.agg},
			}
			respA, err := svc.Process(context.Background(), reqA)
			if err != nil {
				t.Fatalf("Process archive: %v", err)
			}
			respB, err := svc.Process(context.Background(), reqB)
			if err != nil {
				t.Fatalf("Process concat: %v", err)
			}
			got, _ := respA.Data[0]["v"].(float64)
			want, _ := respB.Data[0]["v"].(float64)
			if c.ulp {
				if math.Abs(got-want) > 1e-9 {
					t.Errorf("%s: archive=%v concat=%v (ULP)", c.label, got, want)
				}
				if math.Abs(got-c.want) > 1e-9 {
					t.Errorf("%s: archive=%v expected=%v", c.label, got, c.want)
				}
			} else {
				if got != want {
					t.Errorf("%s: archive=%v concat=%v (must be byte-equal)", c.label, got, want)
				}
				if got != c.want {
					t.Errorf("%s: archive=%v expected=%v", c.label, got, c.want)
				}
			}
			if respA.Metadata == nil || respA.Metadata.TotalRows != 12 {
				t.Errorf("metadata.TotalRows = %d, want 12", respA.Metadata.TotalRows)
			}
		})
	}
}

// TestShardArchiveProcess_Frequency confirms AGG_FREQUENCY accumulates
// dict-string counts across shards identically to the concatenated
// cohort.
func TestShardArchiveProcess_Frequency(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("red")
	dict.Add("green")
	dict.Add("blue")
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	concat := [][]uint64{
		{0, math.Float64bits(10.0)}, // red
		{1, math.Float64bits(20.0)}, // green
		{0, math.Float64bits(30.0)}, // red
		{2, math.Float64bits(40.0)}, // blue
		{1, math.Float64bits(50.0)}, // green
		{0, math.Float64bits(60.0)}, // red
	}
	shards := []struct {
		Name    string
		Records [][]uint64
	}{
		{Name: "a.pulse", Records: concat[0:2]},
		{Name: "b.pulse", Records: concat[2:5]},
		{Name: "c.pulse", Records: concat[5:6]},
	}
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_FREQUENCY, Field: "color", Label: "freq"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	// AGG_FREQUENCY returns the count of the most-frequent value.
	// Across 6 rows (red=3, green=2, blue=1) the answer is 3 — and
	// must equal the concatenated single-file result byte-equal.
	a, _ := respA.Data[0]["freq"].(float64)
	b, _ := respB.Data[0]["freq"].(float64)
	if a != b {
		t.Errorf("AGG_FREQUENCY: archive=%v concat=%v", a, b)
	}
	if a != 3 {
		t.Errorf("AGG_FREQUENCY = %v, want 3", a)
	}
}

// TestShardArchiveProcess_GroupedSum confirms a grouped aggregation
// (GROUP_CATEGORY + AGG_SUM) accumulates per-key sums across shards.
func TestShardArchiveProcess_GroupedSum(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 1, CsvColumnIdx: 1},
		},
	}
	concat := [][]uint64{
		{1, math.Float64bits(10.0)},
		{2, math.Float64bits(15.0)},
		{1, math.Float64bits(20.0)},
		{2, math.Float64bits(25.0)},
		{1, math.Float64bits(30.0)},
		{2, math.Float64bits(35.0)},
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

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Groups: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "score", Label: "v"},
			},
		}
	}

	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	if len(respA.Data) != len(respB.Data) {
		t.Fatalf("grouped row count: archive=%d concat=%d", len(respA.Data), len(respB.Data))
	}
	// Order may differ — compare as a map keyed by region.
	asMap := func(rows []map[string]any) map[float64]float64 {
		out := make(map[float64]float64, len(rows))
		for _, row := range rows {
			k, _ := row["region"].(float64)
			v, _ := row["v"].(float64)
			out[k] = v
		}
		return out
	}
	mA := asMap(respA.Data)
	mB := asMap(respB.Data)
	for k, v := range mB {
		if mA[k] != v {
			t.Errorf("region %v: archive=%v concat=%v", k, mA[k], v)
		}
	}
}

// TestShardArchiveProcess_FilterInclude confirms FILTER_INCLUDE applies
// per-row identically across shards.
func TestShardArchiveProcess_FilterInclude(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Filterers: []*types.Filterer{
				{Type: types.FILTER_RANGE, Field: "score", Values: []string{"40", "100"}},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				{Type: types.AGG_SUM, Field: "score", Label: "s"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	if respA.Data[0]["n"] != respB.Data[0]["n"] {
		t.Errorf("filtered count: archive=%v concat=%v", respA.Data[0]["n"], respB.Data[0]["n"])
	}
	if respA.Data[0]["s"] != respB.Data[0]["s"] {
		t.Errorf("filtered sum: archive=%v concat=%v", respA.Data[0]["s"], respB.Data[0]["s"])
	}
	if respA.Metadata.FilteredRows != respB.Metadata.FilteredRows {
		t.Errorf("filtered_rows: archive=%d concat=%d",
			respA.Metadata.FilteredRows, respB.Metadata.FilteredRows)
	}
}

// TestShardArchiveProcess_TwoPassZScore confirms ATTR_ZSCORE produces
// values consistent with the union mean/variance — the two-pass
// streaming orchestrator must walk every shard for both passes.
func TestShardArchiveProcess_TwoPassZScore(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	req := func(filename string) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: filename},
			Attributes: []*types.Attribute{
				{Type: types.ATTR_ZSCORE, Field: "score", Label: "z"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "z", Label: "sum_z"},
				{Type: types.AGG_AVERAGE, Field: "z", Label: "avg_z"},
			},
		}
	}
	respA, err := svc.Process(context.Background(), req("arch.pulse"))
	if err != nil {
		t.Fatalf("Process archive: %v", err)
	}
	respB, err := svc.Process(context.Background(), req("arch.pulse.concat"))
	if err != nil {
		t.Fatalf("Process concat: %v", err)
	}
	sA, _ := respA.Data[0]["sum_z"].(float64)
	sB, _ := respB.Data[0]["sum_z"].(float64)
	aA, _ := respA.Data[0]["avg_z"].(float64)
	aB, _ := respB.Data[0]["avg_z"].(float64)

	// Z-score over a centred series sums to ~0 and averages to ~0.
	if math.Abs(sA) > 1e-9 {
		t.Errorf("sum_z (archive) = %v, want ~0", sA)
	}
	if math.Abs(sA-sB) > 1e-9 {
		t.Errorf("sum_z: archive=%v concat=%v", sA, sB)
	}
	if math.Abs(aA-aB) > 1e-9 {
		t.Errorf("avg_z: archive=%v concat=%v", aA, aB)
	}
}

// TestShardArchive_NewShardIter_SingleFileFallthrough confirms that the
// scanIterator dispatcher chooses the single-file streaming iterator
// when the cohort has no shards. Regression guard for the dispatch in
// Service.newScanIter.
func TestShardArchive_NewShardIter_SingleFileFallthrough(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "single.pulse", schema, testRecords())
	svc := New(cfg)

	cohort, err := svc.Open(context.Background(), "single.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(cohort.Shards()) != 0 {
		t.Fatalf("Shards on single-file cohort = %d, want 0", len(cohort.Shards()))
	}
	iter := svc.newScanIter(cohort, "single.pulse")
	defer iter.Close()
	if _, ok := iter.(*streamingIterator); !ok {
		t.Errorf("newScanIter returned %T, want *streamingIterator for single-file cohort", iter)
	}
}

// TestShardArchive_NewShardIter_ArchiveDispatch confirms the
// dispatcher picks shardIter for an archive-backed cohort.
func TestShardArchive_NewShardIter_ArchiveDispatch(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(cohort.Shards()) != 3 {
		t.Fatalf("Shards = %d, want 3", len(cohort.Shards()))
	}
	iter := svc.newScanIter(cohort, "arch.pulse")
	defer iter.Close()
	if _, ok := iter.(*shardIter); !ok {
		t.Errorf("newScanIter returned %T, want *shardIter for archive cohort", iter)
	}
}

// TestShardArchive_RowIterationOrder confirms shardIter surfaces rows
// in shard insertion order (central-directory order). Records inside
// each shard preserve their on-disk order.
func TestShardArchive_RowIterationOrder(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	iter := newShardIter(cohort.fs, "arch.pulse", cohort.Schema(), cohort.Shards())
	defer iter.Close()

	var got []float64
	for iter.Next() {
		v, _ := iter.Record().NumericValue("score")
		got = append(got, v)
	}
	if iter.Err() != nil {
		t.Fatalf("iter error: %v", iter.Err())
	}
	if len(got) != len(concat) {
		t.Fatalf("rows iterated = %d, want %d", len(got), len(concat))
	}
	for i, want := range concat {
		wantScore := math.Float64frombits(want[1])
		if got[i] != wantScore {
			t.Errorf("row[%d] score = %v, want %v", i, got[i], wantScore)
		}
	}
}

// TestShardArchive_RowIterationOrder_Reset confirms Reset rewinds the
// iterator to shard 0 row 0 — the contract relied on by the two-pass
// orchestrator.
func TestShardArchive_RowIterationOrder_Reset(t *testing.T) {
	schema, shards, concat := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, concat)

	cohort, err := svc.Open(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	iter := newShardIter(cohort.fs, "arch.pulse", cohort.Schema(), cohort.Shards())
	defer iter.Close()

	pass := func() []float64 {
		var out []float64
		for iter.Next() {
			v, _ := iter.Record().NumericValue("score")
			out = append(out, v)
		}
		return out
	}
	p1 := pass()
	iter.Reset()
	p2 := pass()

	if len(p1) != len(p2) || len(p1) != len(concat) {
		t.Fatalf("pass lengths: p1=%d p2=%d want=%d", len(p1), len(p2), len(concat))
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			t.Errorf("row[%d] differs across Reset: p1=%v p2=%v", i, p1[i], p2[i])
		}
	}
}
