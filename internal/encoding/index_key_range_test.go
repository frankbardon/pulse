package encoding

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// index_key_range_test.go pins the per-shard key range (#493): the
// order-preserving component encoding sorts every keyable type by
// value, the range is a per-component bounding box, and the exclusion
// test only ever proves absence — anything it cannot read means probe.

func leUint(v uint64, w int) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b[:w]
}

func leDecimal(t *testing.T, s string) []byte {
	t.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("bad decimal %q", s)
	}
	d, err := encoding.NewDecimal128FromBigInt(n)
	if err != nil {
		t.Fatal(err)
	}
	enc := encoding.EncodeDecimal128(d)
	return enc[:]
}

// TestIndexKeyOrder_SortsByValue: for every keyable type, wire keys
// listed in ascending VALUE order come out of IndexKeyOrderComponents
// in strictly ascending byte order — negatives below zero for the
// signed words and floats, u64 neighbours above 2^53 kept apart.
func TestIndexKeyOrder_SortsByValue(t *testing.T) {
	t.Parallel()

	f64 := func(v float64) []byte { return leUint(math.Float64bits(v), 8) }
	f32 := func(v float32) []byte { return leUint(uint64(math.Float32bits(v)), 4) }
	i32 := func(v int32) []byte { return leUint(uint64(uint32(v)), 4) }
	i64 := func(v int64) []byte { return leUint(uint64(v), 8) }
	cases := []struct {
		ft   encoding.FieldType
		wire [][]byte
	}{
		{encoding.FieldTypeU8, [][]byte{leUint(0, 1), leUint(1, 1), leUint(255, 1)}},
		{encoding.FieldTypeU16, [][]byte{leUint(1, 2), leUint(256, 2), leUint(65535, 2)}},
		{encoding.FieldTypeU32, [][]byte{leUint(1, 4), leUint(1<<24, 4), leUint(math.MaxUint32, 4)}},
		{encoding.FieldTypeU64, [][]byte{leUint(1, 8), leUint(1<<53, 8), leUint(1<<53+1, 8), leUint(math.MaxUint64, 8)}},
		{encoding.FieldTypeU4, [][]byte{leUint(0, 1), leUint(15, 1)}},
		{encoding.FieldTypePackedBool, [][]byte{leUint(0, 1), leUint(1, 1)}},
		{encoding.FieldTypeCategoricalU16, [][]byte{leUint(0, 2), leUint(3, 2), leUint(700, 2)}},
		{encoding.FieldTypeDate, [][]byte{i32(math.MinInt32), i32(-1), i32(0), i32(1), i32(math.MaxInt32)}},
		{encoding.FieldTypeDateTime, [][]byte{i64(math.MinInt64), i64(-86400), i64(0), i64(1 << 40)}},
		{encoding.FieldTypeF64, [][]byte{f64(math.Inf(-1)), f64(-2.5), f64(-1e-300), f64(math.Copysign(0, -1)), f64(0), f64(1e-300), f64(3), f64(math.Inf(1))}},
		{encoding.FieldTypeF32, [][]byte{f32(float32(math.Inf(-1))), f32(-2.5), f32(0), f32(0.5), f32(float32(math.Inf(1)))}},
		{encoding.FieldTypeDecimal128, [][]byte{leDecimal(t, "-99999999999999999999999999999999999999"), leDecimal(t, "-1"), leDecimal(t, "0"), leDecimal(t, "1"), leDecimal(t, "18446744073709551616")}},
	}
	for _, c := range cases {
		var prev []byte
		for i, w := range c.wire {
			comps, ok := IndexKeyOrderComponents([]encoding.FieldType{c.ft}, w)
			if !ok {
				t.Fatalf("%s: wire key %d did not split", c.ft, i)
			}
			if prev != nil && bytes.Compare(prev, comps[0]) >= 0 {
				t.Errorf("%s: value %d orders at or below value %d (%x vs %x)", c.ft, i, i-1, comps[0], prev)
			}
			prev = comps[0]
		}
	}
}

// TestIndexKeyRange_PerComponentBoundingBox: the range of a composite
// key set is each component's own min and max (not the lexicographic
// tuple range), in fixed-width hex; no key records no range, and a key
// that does not split records none rather than a wrong one.
func TestIndexKeyRange_PerComponentBoundingBox(t *testing.T) {
	t.Parallel()

	keys := []IndexKeySpec{{Name: "region", Type: encoding.FieldTypeCategoricalU8}, {Name: "id", Type: encoding.FieldTypeU64}}
	key := func(region, id uint64) []byte { return append(leUint(region, 1), leUint(id, 8)...) }
	buckets := []IndexBucket{
		{Entries: []IndexEntry{{Key: key(2, 7)}, {Key: key(0, 1<<53+1)}}},
		{},
		{Entries: []IndexEntry{{Key: key(1, 3)}}},
	}
	keyMin, keyMax := IndexKeyRange(keys, buckets)
	if want := []string{"00", "0000000000000003"}; !reflect.DeepEqual(keyMin, want) {
		t.Errorf("key_min = %v, want %v", keyMin, want)
	}
	if want := []string{"02", "0020000000000001"}; !reflect.DeepEqual(keyMax, want) {
		t.Errorf("key_max = %v, want %v", keyMax, want)
	}

	if lo, hi := IndexKeyRange(keys, []IndexBucket{{}, {}}); lo != nil || hi != nil {
		t.Errorf("no keys recorded range %v..%v, want none", lo, hi)
	}
	short := []IndexBucket{{Entries: []IndexEntry{{Key: key(1, 3)}, {Key: []byte{1}}}}}
	if lo, hi := IndexKeyRange(keys, short); lo != nil || hi != nil {
		t.Errorf("unsplittable key recorded range %v..%v, want none", lo, hi)
	}
}

// TestIndexManifestShard_KeyRangeExcludes: a shard is skipped only
// when a component falls outside a well-formed recorded range; an
// absent, wrong-arity or malformed range always probes.
func TestIndexManifestShard_KeyRangeExcludes(t *testing.T) {
	t.Parallel()

	types := []encoding.FieldType{encoding.FieldTypeCategoricalU8, encoding.FieldTypeU64}
	probe := func(region, id uint64) [][]byte {
		c, ok := IndexKeyOrderComponents(types, append(leUint(region, 1), leUint(id, 8)...))
		if !ok {
			t.Fatal("probe did not split")
		}
		return c
	}
	ranged := IndexManifestShard{KeyMin: []string{"00", "0020000000000000"}, KeyMax: []string{"02", "0020000000000002"}}
	cases := []struct {
		name  string
		shard IndexManifestShard
		key   [][]byte
		want  bool
	}{
		{"inside", ranged, probe(1, 1<<53+1), false},
		{"on both bounds", ranged, probe(2, 1<<53), false},
		{"id one above max (u64 > 2^53 exact)", ranged, probe(1, 1<<53+3), true},
		{"id below min", ranged, probe(1, 5), true},
		{"region outside, id inside", ranged, probe(3, 1<<53+1), true},
		{"no range recorded", IndexManifestShard{}, probe(9, 9), false},
		{"min without max", IndexManifestShard{KeyMin: ranged.KeyMin}, probe(9, 9), false},
		{"arity mismatch", IndexManifestShard{KeyMin: []string{"00"}, KeyMax: []string{"02"}}, probe(9, 9), false},
		{"malformed later bound", IndexManifestShard{KeyMin: []string{"00", "zz"}, KeyMax: []string{"02", "0020000000000002"}}, probe(9, 9), false},
		{"wrong-width bound", IndexManifestShard{KeyMin: []string{"00", "00"}, KeyMax: []string{"02", "ff"}}, probe(9, 9), false},
	}
	for _, c := range cases {
		if got := c.shard.KeyRangeExcludes(c.key); got != c.want {
			t.Errorf("%s: KeyRangeExcludes = %v, want %v", c.name, got, c.want)
		}
	}
}
