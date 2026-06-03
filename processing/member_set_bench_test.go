package processing

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// Per-lookup latency benchmarks for the three MemberSet impls. Run via:
//
//	go test ./processing/ -run=^$ -bench=BenchmarkMemberSet -benchmem
//
// Each Contains call should land in the low-nanosecond range:
//
//   - BitsetSet:  ~1 ns  (single word load + bit test)
//   - Uint64Set:  ~7-15 ns (Go map[uint64]struct{})
//   - StringSet:  ~15-25 ns (Go map[string]struct{}, key hash)
//
// The numbers above are guidance, not asserted bounds — Go's bench
// machinery has too much variance across hosts to gate on absolutes.

const benchSetSize = 1_000_000

func benchKeys(n int) []uint64 {
	keys := make([]uint64, n)
	for i := range keys {
		keys[i] = uint64(i)
	}
	return keys
}

func benchStrKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = strconv.FormatInt(int64(i), 10)
	}
	return keys
}

func BenchmarkMemberSet_Bitset_Contains(b *testing.B) {
	bs := newBitsetSet(benchSetSize)
	for i := 0; i < benchSetSize; i += 2 { // half the keys present
		bs.Add(uint32(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = bs.Contains(uint32(i % benchSetSize))
	}
}

func BenchmarkMemberSet_Uint64_Contains(b *testing.B) {
	us := newUint64Set(benchSetSize)
	for _, k := range benchKeys(benchSetSize / 2) {
		us.Add(k)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = us.Contains(uint64(i % benchSetSize))
	}
}

func BenchmarkMemberSet_String_Contains(b *testing.B) {
	ss := newStringSet(benchSetSize)
	keys := benchStrKeys(benchSetSize / 2)
	for _, k := range keys {
		ss.Add(k)
	}
	probe := benchStrKeys(benchSetSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ss.Contains(probe[i%benchSetSize])
	}
}

// BenchmarkMemberSet_LoadIntegerFromReader measures end-to-end loader
// throughput for a 1M-line uint64 include file. Reports ns/line.
func BenchmarkMemberSet_LoadIntegerFromReader(b *testing.B) {
	const n = 100_000
	var buf []byte
	for i := 0; i < n; i++ {
		buf = strconv.AppendUint(buf, uint64(i), 10)
		buf = append(buf, '\n')
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64},
	}}
	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadMemberSetFromReader(bytes.NewReader(buf), schema, "id")
		if err != nil {
			b.Fatalf("LoadMemberSetFromReader: %v", err)
		}
	}
}

// BenchmarkMemberSet_BuildPredicate_Bitset measures the per-row cost of
// the closure returned by BuildMemberSetPredicate when applied to a
// fresh Record. Compares the categorical-bitset hot path against a
// uint64-map path for the same record shape.
func BenchmarkMemberSet_BuildPredicate_Bitset(b *testing.B) {
	dict := encoding.NewDictionary()
	for i := 0; i < 1024; i++ {
		dict.Add(strconv.Itoa(i))
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict},
	}}
	bs := newBitsetSet(1024)
	for i := 0; i < 512; i++ {
		bs.Add(uint32(i))
	}
	fn, err := BuildMemberSetPredicate(bs, schema, "k")
	if err != nil {
		b.Fatalf("BuildMemberSetPredicate: %v", err)
	}
	rec := NewRecord(schema, map[string]float64{"k": 0})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.values["k"] = float64(i & 1023)
		_, _ = fn(rec)
	}
}

func BenchmarkMemberSet_BuildPredicate_Uint64(b *testing.B) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU64},
	}}
	us := newUint64Set(1024)
	for i := uint64(0); i < 512; i++ {
		us.Add(i)
	}
	fn, err := BuildMemberSetPredicate(us, schema, "k")
	if err != nil {
		b.Fatalf("BuildMemberSetPredicate: %v", err)
	}
	rec := NewRecord(schema, map[string]float64{"k": 0})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.values["k"] = float64(uint64(i) & 1023)
		_, _ = fn(rec)
	}
}
