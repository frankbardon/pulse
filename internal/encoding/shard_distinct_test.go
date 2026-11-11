package encoding

import (
	"bytes"
	"encoding/binary"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// shard_distinct_test.go pins the DIST trailer (U73 #500): its wire
// round trip, that ReadSchemaDoc — the path every data reader and every
// older build takes — ignores it, its absent / newer / malformed
// answers, and the counter's definition of distinct (stored bit
// patterns, nulls excluded, the limit).

// distinctSchema has one field of every eligible type plus a u8 that is
// not counted; every field is nullable.
func distinctSchema() *encoding.Schema {
	types := []encoding.FieldType{
		encoding.FieldTypeU32, encoding.FieldTypeU64, encoding.FieldTypeDate,
		encoding.FieldTypeDateTime, encoding.FieldTypeF32, encoding.FieldTypeF64,
		encoding.FieldTypeU8,
	}
	names := []string{"u32", "u64", "date", "dt", "f32", "f64", "u8"}
	s := &encoding.Schema{}
	off := 0
	for i, ft := range types {
		s.Fields = append(s.Fields, encoding.Field{Name: names[i], Type: ft, Nullable: true, ByteOffset: off, CsvColumnIdx: i})
		off += ft.ByteSize()
	}
	return s
}

// distinctPayload writes a single-file cohort of rows; a nil cell is
// null.
func distinctPayload(t *testing.T, s *encoding.Schema, rows [][]*uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := WritePreamble(&buf, s); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		bm := make([]byte, s.BitmapByteSize())
		for i, f := range s.Fields {
			v := uint64(0)
			if row[i] == nil {
				encoding.BitmapSetNull(bm, i)
			} else {
				v = *row[i]
			}
			if err := encoding.WriteFieldValue(&buf, f.Type, v); err != nil {
				t.Fatal(err)
			}
		}
		buf.Write(bm)
	}
	return buf.Bytes()
}

func u(v uint64) *uint64 { return &v }

func TestCountShardDistinct_BitPatternsNullsAndIneligible(t *testing.T) {
	t.Parallel()
	s := distinctSchema()
	f32 := func(v float32) *uint64 { return u(uint64(math.Float32bits(v))) }
	f64 := func(v float64) *uint64 { return u(math.Float64bits(v)) }
	nan1 := u(0x7ff8000000000001)
	nan2 := u(0x7ff8000000000002)
	negZero := u(0x8000000000000000)
	rows := [][]*uint64{
		// u32, u64, date, dt, f32, f64, u8
		{u(0), u(1 << 40), u(10), u(100), f32(0), f64(0), u(1)},
		{u(0), u(1 << 40), u(11), u(100), f32(1.5), negZero, u(2)},
		{u(7), nil, u(10), nil, f32(1.5), nan1, u(3)},
		{nil, u(3), nil, u(101), nil, nan2, u(4)},
		{u(7), u(3), u(12), u(102), u(uint64(math.Float32bits(float32(math.Copysign(0, -1))))), nan1, u(5)},
	}
	fields := EligibleDistinctFields(s)
	if len(fields) != 6 {
		t.Fatalf("eligible fields = %+v, want the six eligible types (u8 excluded)", fields)
	}
	got, err := CountShardDistinct(bytes.NewReader(distinctPayload(t, s, rows)), s, fields, 0)
	if err != nil {
		t.Fatal(err)
	}
	// u32 {0,7}; u64 {2^40,3} (null skipped); date {10,11,12}; dt
	// {100,101,102}; f32 {+0,1.5,−0} — ±0 are two stored words; f64 {+0,
	// −0, nan1, nan2} — every NaN payload its own word.
	want := []uint64{2, 2, 3, 3, 3, 4}
	for i, w := range want {
		if got[i] != (DistinctCount{Status: DistinctExact, Count: w}) {
			t.Errorf("%s: %+v, want exact %d", fields[i].Name, got[i], w)
		}
	}
}

func TestCountShardDistinct_LimitAndReuse(t *testing.T) {
	t.Parallel()
	s := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}}}
	rows := func(n int) [][]*uint64 {
		out := make([][]*uint64, n)
		for i := range out {
			out[i] = []*uint64{u(uint64(i) * 0x9e3779b97f4a7c15)}
		}
		return out
	}
	fields := EligibleDistinctFields(s)
	c := NewDistinctCounter(5000)
	// Over, then under, then at the limit on ONE reused counter: a set
	// released over the limit, or a stale set, would miscount the next.
	for _, tc := range []struct {
		n    int
		want DistinctCount
	}{
		{5001, DistinctCount{Status: DistinctOverLimit, Count: 5000}},
		{3000, DistinctCount{Status: DistinctExact, Count: 3000}},
		{5000, DistinctCount{Status: DistinctExact, Count: 5000}},
		{10, DistinctCount{Status: DistinctExact, Count: 10}},
	} {
		got, err := c.Count(bytes.NewReader(distinctPayload(t, s, rows(tc.n))), s, fields)
		if err != nil {
			t.Fatal(err)
		}
		if got[0] != tc.want {
			t.Errorf("%d rows: %+v, want %+v", tc.n, got[0], tc.want)
		}
	}
}

func sampleDistinct() *ArchiveDistinct {
	return &ArchiveDistinct{
		Fields: []DistinctField{{Name: "id", Type: encoding.FieldTypeU32}, {Name: "ts", Type: encoding.FieldTypeDateTime}},
		Shards: []ShardDistinct{
			{Name: "a.pulse", Counts: []DistinctCount{{DistinctExact, 3}, {DistinctOverLimit, 4 << 20}}},
			{Name: "b.pulse", Counts: []DistinctCount{{DistinctOptedOut, 0}, {DistinctAbsent, 0}}},
		},
	}
}

func docBytes(t *testing.T, d *ArchiveDistinct) []byte {
	t.Helper()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 4},
	}}
	var buf bytes.Buffer
	if err := WriteSchemaDocDistinct(&buf, s, 42, 2, d); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDistinctTrailer_RoundTrip(t *testing.T) {
	t.Parallel()
	want := sampleDistinct()
	doc, got, err := ReadSchemaDocDistinct(bytes.NewReader(docBytes(t, want)))
	if err != nil {
		t.Fatal(err)
	}
	if doc.AggregateRecordCount != 42 || doc.ShardCount != 2 {
		t.Errorf("SHRD = %d / %d, want 42 / 2", doc.AggregateRecordCount, doc.ShardCount)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("trailer = %+v, want %+v", got, want)
	}
}

// The OLD-READER gate: a `_schema.pulse` with the trailer decodes
// through ReadSchemaDoc exactly as one without it; the trailer is pure
// appended bytes after SHRD; a nil trailer writes the old bytes.
func TestDistinctTrailer_OldReaderIgnoresIt(t *testing.T) {
	t.Parallel()
	with, without := docBytes(t, sampleDistinct()), docBytes(t, nil)
	if !bytes.HasPrefix(with, without) || len(with) == len(without) {
		t.Fatal("the trailer is not appended after the pre-trailer bytes")
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 4},
	}}
	var legacy bytes.Buffer
	if err := WriteSchemaDoc(&legacy, s, 42, 2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacy.Bytes(), without) {
		t.Error("WriteSchemaDocDistinct(nil) differs from WriteSchemaDoc")
	}
	a, err := ReadSchemaDoc(bytes.NewReader(with))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadSchemaDoc(bytes.NewReader(without))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("ReadSchemaDoc with trailer = %+v, without = %+v", a, b)
	}
	if with[encoding.HeaderSize-1] != encoding.FormatVersionV1 {
		t.Errorf("version byte = %#x, want 0x01", with[encoding.HeaderSize-1])
	}
}

func TestDistinctTrailer_AbsentNewerAndMalformed(t *testing.T) {
	t.Parallel()
	without := docBytes(t, nil)
	with := docBytes(t, sampleDistinct())
	trailerAt := len(without)

	read := func(b []byte) (*ArchiveDistinct, error) {
		_, d, err := ReadSchemaDocDistinct(bytes.NewReader(b))
		return d, err
	}
	if d, err := read(without); d != nil || err != nil {
		t.Errorf("no trailer: %+v, %v; want nil, nil", d, err)
	}
	other := append(append([]byte(nil), without...), []byte("XXXXjunk")...)
	if d, err := read(other); d != nil || err != nil {
		t.Errorf("foreign bytes: %+v, %v; want nil, nil", d, err)
	}
	newer := append([]byte(nil), with...)
	newer[trailerAt+4] = DistinctTrailerVersion + 1
	if d, err := read(newer); d != nil || err != nil {
		t.Errorf("newer version: %+v, %v; want nil, nil (skipped)", d, err)
	}
	for name, b := range map[string][]byte{
		"truncated body":   with[:len(with)-3],
		"truncated header": with[:trailerAt+7],
		"bad status":       func() []byte { c := append([]byte(nil), with...); c[len(c)-9] = 9; return c }(),
		"short body length": func() []byte {
			c := append([]byte(nil), with...)
			binary.LittleEndian.PutUint64(c[trailerAt+5:], uint64(len(with)-trailerAt-13-1))
			return c
		}(),
	} {
		if _, err := read(b); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: err = %v, want ENCODING_INVALID", name, err)
		}
	}
	// A trailer listing another shard count than SHRD is refused.
	var buf bytes.Buffer
	s := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}}}
	if err := WriteSchemaDocDistinct(&buf, s, 1, 3, sampleDistinct()); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("write with mismatched shard count: %v, want ENCODING_INVALID", err)
	}
}

func TestArchiveDistinct_FieldBoundAndCarry(t *testing.T) {
	t.Parallel()
	d := &ArchiveDistinct{
		Fields: []DistinctField{{Name: "id", Type: encoding.FieldTypeU32}},
		Shards: []ShardDistinct{
			{Name: "a", Counts: []DistinctCount{{DistinctExact, 3}}},
			{Name: "b", Counts: []DistinctCount{{DistinctExact, 5}}},
		},
	}
	if n, ok := d.FieldBound("id"); !ok || n != 8 {
		t.Errorf("FieldBound = %d, %v; want 8, true", n, ok)
	}
	if _, ok := d.FieldBound("nope"); ok {
		t.Error("an untracked field has a bound")
	}
	for _, st := range []DistinctStatus{DistinctAbsent, DistinctOverLimit, DistinctOptedOut} {
		c := *d
		c.Shards = []ShardDistinct{d.Shards[0], {Name: "b", Counts: []DistinctCount{{st, 5}}}}
		if _, ok := c.FieldBound("id"); ok {
			t.Errorf("a %s shard still yields a bound", st)
		}
	}

	// Carry: by shard and field name AND type; fresh wins; unknown is
	// absent. canonical's "id" is now u64 (a type change never carries).
	canonical := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "ts", Type: encoding.FieldTypeDate},
		{Name: "label", Type: encoding.FieldTypeU8},
	}}
	next := NextArchiveDistinct(canonical, []string{"b", "c", "a"}, d,
		map[string][]DistinctCount{"c": {{DistinctExact, 1}, {DistinctExact, 2}}})
	want := &ArchiveDistinct{
		Fields: []DistinctField{{Name: "id", Type: encoding.FieldTypeU32}, {Name: "ts", Type: encoding.FieldTypeDate}},
		Shards: []ShardDistinct{
			{Name: "b", Counts: []DistinctCount{{DistinctExact, 5}, {}}},
			{Name: "c", Counts: []DistinctCount{{DistinctExact, 1}, {DistinctExact, 2}}},
			{Name: "a", Counts: []DistinctCount{{DistinctExact, 3}, {}}},
		},
	}
	if !reflect.DeepEqual(next, want) {
		t.Errorf("NextArchiveDistinct = %+v, want %+v", next, want)
	}
	canonical.Fields[0].Type = encoding.FieldTypeU64
	if got := NextArchiveDistinct(canonical, []string{"a"}, d, nil).Shards[0].Counts[0]; got.Status != DistinctAbsent {
		t.Errorf("a retyped field carried %+v", got)
	}
}
