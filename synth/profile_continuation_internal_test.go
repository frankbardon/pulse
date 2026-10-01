package synth

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// continuationSchema: a non-null u8 key, a u16 that changes every row,
// a packed_bool and a nullable u8 — enough to exercise a multi-byte
// span, a bit-packed byte and the null-bit half of the rule.
func continuationSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU8},
		{Name: "v", Type: encoding.FieldTypeU16},
		{Name: "b", Type: encoding.FieldTypePackedBool},
		{Name: "n", Type: encoding.FieldTypeU8, Nullable: true},
	}}
}

// continuationRows is six rows whose per-field continuation is known by
// hand over the five adjacent pairs:
//
//	k: 1 1 1 2 2 2         -> 4/5
//	v: 10..15              -> 0/5
//	b: 1 1 0 0 1 1         -> 3/5
//	n: 5 ∅ ∅ 0 0 7         -> 2/5 (∅→∅ and 0→0 repeat; ∅→0 carries
//	                          the same zero bytes but a different null
//	                          bit, so it does NOT repeat)
func continuationRows(t *testing.T, schema *encoding.Schema) []byte {
	t.Helper()
	rows := []map[string]any{
		{"k": 1.0, "v": 10.0, "b": 1.0, "n": 5.0},
		{"k": 1.0, "v": 11.0, "b": 1.0, "n": 0.0},
		{"k": 1.0, "v": 12.0, "b": 0.0, "n": 0.0},
		{"k": 2.0, "v": 13.0, "b": 0.0, "n": 0.0},
		{"k": 2.0, "v": 14.0, "b": 1.0, "n": 0.0},
		{"k": 2.0, "v": 15.0, "b": 1.0, "n": 7.0},
	}
	nulls := []map[string]bool{{}, {"n": true}, {"n": true}, {}, {}, {}}
	return encodeModelRows(t, schema, rows, nulls)
}

func TestRunContinuation_PerFieldRatesExact(t *testing.T) {
	schema := continuationSchema()
	data := continuationRows(t, schema)
	// The null-bit half of the rule only means something if a null and
	// a non-null zero share their value bytes on the wire.
	stride := schema.RecordByteSize()
	if data[2*stride+4] != data[3*stride+4] {
		t.Fatalf("fixture: null and zero of %q must share value bytes", "n")
	}

	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	rc := prof.RunContinuation
	if rc == nil {
		t.Fatal("RunContinuation is nil with the option set")
	}
	if rc.Pairs != 5 || rc.Shards != 1 {
		t.Fatalf("pairs/shards = %d/%d, want 5/1", rc.Pairs, rc.Shards)
	}
	want := map[string]float64{"k": 0.8, "v": 0, "b": 0.6, "n": 0.4}
	for i, fc := range rc.Fields {
		if fc.Name != schema.Fields[i].Name {
			t.Fatalf("Fields[%d] = %q, want schema order", i, fc.Name)
		}
		if fc.Rate != want[fc.Name] {
			t.Errorf("rate(%s) = %v, want %v", fc.Name, fc.Rate, want[fc.Name])
		}
	}
	if rc.Overall != 9.0/20.0 {
		t.Errorf("overall = %v, want 0.45", rc.Overall)
	}
	if len(rc.HighFields) != 1 || rc.HighFields[0] != "k" {
		t.Errorf("high fields = %v, want [k]", rc.HighFields)
	}
	if rc.HighThreshold != RunContinuationHighThreshold {
		t.Errorf("high threshold = %v", rc.HighThreshold)
	}
	if !strings.HasPrefix(rc.Advice, "low:") || !strings.Contains(rc.Advice, "ORDER BY") {
		t.Errorf("advice for a 0.45 cohort must say low and point at ORDER BY: %q", rc.Advice)
	}
}

func TestRunContinuation_OffByDefault(t *testing.T) {
	schema := continuationSchema()
	data := continuationRows(t, schema)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{})
	if err != nil {
		t.Fatalf("profileRecords: %v", err)
	}
	if prof.RunContinuation != nil {
		t.Fatal("RunContinuation populated without the option")
	}
	out, err := json.Marshal(prof)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte("run_continuation")) {
		t.Fatal("document carries run_continuation without the option")
	}
}

// TestRunContinuation_RidesTheExistingScan: the measurement pulls no
// extra byte, and every other section of the document is byte-identical
// to the capture without it.
func TestRunContinuation_RidesTheExistingScan(t *testing.T) {
	schema := continuationSchema()
	data := continuationRows(t, schema)

	baseline := &countingReader{r: bytes.NewReader(data)}
	base, err := profileRecords(schema, baseline, ProfileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	measured := &countingReader{r: bytes.NewReader(data)}
	with, err := profileRecords(schema, measured, ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatal(err)
	}
	if measured.n != baseline.n {
		t.Errorf("continuation read %d bytes, baseline %d — it must ride the existing scan", measured.n, baseline.n)
	}
	with.RunContinuation = nil
	a, _ := json.Marshal(base)
	b, _ := json.Marshal(with)
	if !bytes.Equal(a, b) {
		t.Errorf("the rest of the document moved:\n%s\n%s", a, b)
	}
}

func TestRunContinuation_HonoursSampleLimit(t *testing.T) {
	schema := continuationSchema()
	data := continuationRows(t, schema)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{RunContinuation: true, SampleLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := prof.RunContinuation.Pairs; got != 2 {
		t.Fatalf("pairs = %d under SampleLimit 3, want 2", got)
	}
	// Rows 0..2: k repeats on both pairs.
	if got := prof.RunContinuation.Fields[0].Rate; got != 1 {
		t.Errorf("rate(k) = %v over the first three rows, want 1", got)
	}
}

// TestRunContinuation_PairsNeverSpanASegment: two shards, each constant
// in k but different from each other. Within each shard k always
// repeats; only a pair across the boundary would not.
func TestRunContinuation_PairsNeverSpanASegment(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "k", Type: encoding.FieldTypeU8}}}
	shard := func(k float64) []byte {
		rows := []map[string]any{{"k": k}, {"k": k}, {"k": k}}
		return encodeModelRows(t, schema, rows, nil)
	}
	prof, err := profileSegments(schema,
		[]io.Reader{bytes.NewReader(shard(1)), bytes.NewReader(shard(2))},
		ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatal(err)
	}
	if prof.RowCount != 6 {
		t.Fatalf("row count = %d across two segments, want 6", prof.RowCount)
	}
	rc := prof.RunContinuation
	if rc.Pairs != 4 || rc.Shards != 2 {
		t.Fatalf("pairs/shards = %d/%d, want 4/2", rc.Pairs, rc.Shards)
	}
	if rc.Fields[0].Rate != 1 || rc.Overall != 1 {
		t.Errorf("rate = %v overall = %v, want 1/1 — a pair spanned the shard boundary", rc.Fields[0].Rate, rc.Overall)
	}
	if !strings.HasPrefix(rc.Advice, "high:") {
		t.Errorf("advice = %q, want high", rc.Advice)
	}
}

func TestRunContinuation_NoPairs(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "k", Type: encoding.FieldTypeU8}}}
	data := encodeModelRows(t, schema, []map[string]any{{"k": 1.0}}, nil)
	prof, err := profileRecords(schema, bytes.NewReader(data), ProfileOptions{RunContinuation: true})
	if err != nil {
		t.Fatal(err)
	}
	rc := prof.RunContinuation
	if rc.Pairs != 0 || rc.Overall != 0 || len(rc.HighFields) != 0 {
		t.Fatalf("one-row cohort: %+v", rc)
	}
	if !strings.Contains(rc.Advice, "no adjacent pairs") {
		t.Errorf("advice = %q", rc.Advice)
	}
}
