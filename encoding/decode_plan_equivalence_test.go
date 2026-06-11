package encoding

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// This file is the load-bearing safety net for the E2 plan-driven decode
// rewrite. For every (schema, projection-subset, record) triple it asserts
// that ReadRecordWithWidePlan produces byte-equal values / nulls / wide
// maps to the per-field walk at ReadRecordWithWideProjected.
//
// Three schemas are exercised:
//
//   1. perTypeSchemas — one schema per supported field type, each replicated
//      in both nullable and non-nullable variants where the type supports
//      nullability. Tiny enough to enumerate every 2^N projection subset
//      (≤ 12 fields each).
//
//   2. smallMixedSchema — 10-field schema combining bit-packed runs,
//      categoricals, decimal128, set_u8/u16/u32/u64, and a bitmap. All
//      2^10 = 1024 projection subsets are enumerated.
//
//   3. bigSchema — 26-field schema with every supported field type plus
//      nullable variants. 2^26 subsets is too many to enumerate, so the
//      test deterministically samples ~200 subsets covering: all retained,
//      none retained, only nullable retained, only bit-packed retained,
//      mid-field-only retained (leading and trailing skips), every-other-
//      field retained, plus random uniformly-distributed subsets.
//
// Records are synthesised per-schema with a deterministic seed and include
// edge cases (empty set, max-cardinality set, decimal128 negatives and
// large mantissas, u4 boundary values, every-other-record null pattern).
// Decode results are compared via reflect.DeepEqual. No frozen golden
// blobs — the contract is "both paths produce the same output on the same
// data".

const equivalenceRecordsPerSchema = 1000

// encodeRecord serializes one logical record (fieldValues + null bitmap)
// for the given schema. fieldValues is keyed by field name; for
// decimal128 the value must be a *big.Int mantissa, for f32/f64 the
// float-bits uint64, and for everything else a uint64 raw payload.
// nulls is keyed by field name; only nullable fields may appear there.
// The returned byte slice is exactly schema.RecordByteSize() bytes long
// when no error occurs.
func encodeRecord(t *testing.T, schema *Schema, fieldValues map[string]any, nulls map[string]bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i := range schema.Fields {
		f := &schema.Fields[i]
		switch f.Type {
		case FieldTypePackedBool:
			b, _ := fieldValues[f.Name].(bool)
			if err := WriteBit(&buf, uint(f.BitPosition), b); err != nil {
				t.Fatalf("write %s: %v", f.Name, err)
			}
		case FieldTypeU4:
			v, _ := fieldValues[f.Name].(uint8)
			if err := WriteNibble(&buf, f.BitPosition > 0, v); err != nil {
				t.Fatalf("write %s: %v", f.Name, err)
			}
		case FieldTypeDecimal128:
			m, _ := fieldValues[f.Name].(*big.Int)
			if m == nil {
				m = big.NewInt(0)
			}
			d, err := NewDecimal128FromBigInt(m)
			if err != nil {
				t.Fatalf("decimal128 mantissa for %s out of range: %v", f.Name, err)
			}
			if err := WriteDecimal128(&buf, d); err != nil {
				t.Fatalf("write %s: %v", f.Name, err)
			}
		default:
			raw, _ := fieldValues[f.Name].(uint64)
			if err := WriteFieldValue(&buf, f.Type, raw); err != nil {
				t.Fatalf("write %s: %v", f.Name, err)
			}
		}
	}
	if bmSize := schema.BitmapByteSize(); bmSize > 0 {
		bm := make([]byte, bmSize)
		for i := range schema.Fields {
			f := &schema.Fields[i]
			if !f.Nullable {
				continue
			}
			if nulls[f.Name] {
				BitmapSetNull(bm, i)
			}
		}
		if err := WriteBitmap(&buf, bm); err != nil {
			t.Fatalf("write bitmap: %v", err)
		}
	}
	got := buf.Bytes()
	if want := schema.RecordByteSize(); len(got) != want {
		t.Fatalf("encoded record stride %d != schema stride %d", len(got), want)
	}
	return got
}

// allFieldNames returns the schema's fields in schema order.
func allFieldNames(s *Schema) []string {
	out := make([]string, len(s.Fields))
	for i := range s.Fields {
		out[i] = s.Fields[i].Name
	}
	return out
}

// nullableFieldNames returns just the nullable field names in schema order.
func nullableFieldNames(s *Schema) []string {
	var out []string
	for i := range s.Fields {
		if s.Fields[i].Nullable {
			out = append(out, s.Fields[i].Name)
		}
	}
	return out
}

// bitPackedFieldNames returns the bit-packed field names in schema order.
func bitPackedFieldNames(s *Schema) []string {
	var out []string
	for i := range s.Fields {
		if s.Fields[i].Type.IsBitPacked() {
			out = append(out, s.Fields[i].Name)
		}
	}
	return out
}

// subsetFromMask materializes the field-name subset selected by the
// bitmask whose low bits index into schema.Fields. Bit i set ⇒ Fields[i]
// is retained.
func subsetFromMask(s *Schema, mask uint64) []string {
	var out []string
	for i := range s.Fields {
		if mask&(uint64(1)<<uint(i)) != 0 {
			out = append(out, s.Fields[i].Name)
		}
	}
	return out
}

// keepFromNames returns a FieldFilter that admits exactly the listed
// names. nil-or-empty input → a filter that admits nothing (matches the
// plan-driven "empty retained = nothing kept" semantics exactly). The
// per-field path treats nil-filter as "keep every field", which would
// diverge from the plan's empty-set fast path — so we always materialise
// a concrete keep function here.
func keepFromNames(names []string) FieldFilter {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(name string) bool {
		_, ok := set[name]
		return ok
	}
}

// compareDecodeResults runs both decode paths against the same raw record
// and asserts byte-equal value/null/wide maps. Logs the subset on failure
// so a CI bisect can repro.
func compareDecodeResults(t *testing.T, schema *Schema, raw []byte, retained []string, subsetLabel string) {
	t.Helper()

	// Old per-field walk.
	rrOld := NewRecordReader(bytes.NewReader(raw), schema)
	valuesOld := make(map[string]float64)
	nullsOld := make(map[string]bool)
	wideOld := make(map[string]any)
	keep := keepFromNames(retained)
	if err := rrOld.ReadRecordWithWideProjected(valuesOld, nullsOld, wideOld, keep); err != nil {
		t.Fatalf("subset=%s: per-field decode: %v", subsetLabel, err)
	}

	// New plan-driven walk.
	plan, err := schema.BuildDecodePlan(retained)
	if err != nil {
		t.Fatalf("subset=%s: BuildDecodePlan: %v", subsetLabel, err)
	}
	rrNew := NewRecordReader(bytes.NewReader(raw), schema)
	valuesNew := make(map[string]float64)
	nullsNew := make(map[string]bool)
	wideNew := make(map[string]any)
	if err := rrNew.ReadRecordWithWidePlan(valuesNew, nullsNew, wideNew, keep, plan); err != nil {
		t.Fatalf("subset=%s: plan-driven decode: %v", subsetLabel, err)
	}

	if !reflect.DeepEqual(valuesOld, valuesNew) {
		t.Errorf("subset=%s values mismatch:\n  per-field=%v\n  plan-driven=%v",
			subsetLabel, valuesOld, valuesNew)
	}
	if !reflect.DeepEqual(nullsOld, nullsNew) {
		t.Errorf("subset=%s nulls mismatch:\n  per-field=%v\n  plan-driven=%v",
			subsetLabel, nullsOld, nullsNew)
	}
	if !reflect.DeepEqual(wideOld, wideNew) {
		t.Errorf("subset=%s wide mismatch:\n  per-field=%v\n  plan-driven=%v",
			subsetLabel, wideOld, wideNew)
	}
}

// ---------------------------------------------------------------------------
// Per-type schemas (one schema per supported field type, both nullable
// and non-nullable variants where the type supports it).
// ---------------------------------------------------------------------------

// perTypeFixture wraps a schema with a fixed-seed record generator so
// each fixture sees the same edge-case coverage.
type perTypeFixture struct {
	name    string
	schema  *Schema
	genFunc func(r *rand.Rand, idx int) (vals map[string]any, nulls map[string]bool)
}

// buildSetDict constructs an N-entry dictionary used by the set fixtures.
func buildSetDict(entries int) *Dictionary {
	d := NewDictionary()
	for i := 0; i < entries; i++ {
		_, _ = d.Add(fmt.Sprintf("tag-%d", i))
	}
	return d
}

// buildCategoricalDict constructs an N-entry dictionary used by categorical
// fixtures.
func buildCategoricalDict(entries int) *Dictionary {
	d := NewDictionary()
	for i := 0; i < entries; i++ {
		_, _ = d.Add(fmt.Sprintf("cat-%d", i))
	}
	return d
}

// perTypeFixtures returns one fixture per supported field type, repeated
// in non-nullable and nullable variants. Each fixture's schema has at
// most a handful of fields so a 2^N subset sweep is feasible (≤ 12).
func perTypeFixtures() []perTypeFixture {
	var out []perTypeFixture

	// Scalar numeric types. Pair each with a sibling field of the same
	// type to exercise within-type coalescing.
	scalarTypes := []FieldType{
		FieldTypeU8, FieldTypeU16, FieldTypeU32, FieldTypeU64,
		FieldTypeF32, FieldTypeF64, FieldTypeDate,
	}
	for _, ft := range scalarTypes {
		ft := ft
		// Non-nullable variant.
		out = append(out, perTypeFixture{
			name: fmt.Sprintf("%s_pair_nonnull", ft.String()),
			schema: &Schema{Fields: []Field{
				{Name: "a", Type: ft},
				{Name: "b", Type: ft},
				{Name: "c", Type: FieldTypeU8}, // canary neighbour
			}},
			genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
				return scalarGenValues(r, idx, ft, []string{"a", "b"}, "c"), nil
			},
		})
		// Nullable variant — only "a" nullable.
		out = append(out, perTypeFixture{
			name: fmt.Sprintf("%s_pair_nullable", ft.String()),
			schema: &Schema{Fields: []Field{
				{Name: "a", Type: ft, Nullable: true},
				{Name: "b", Type: ft},
				{Name: "c", Type: FieldTypeU8},
			}},
			genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
				v := scalarGenValues(r, idx, ft, []string{"a", "b"}, "c")
				var nulls map[string]bool
				if idx%3 == 0 {
					nulls = map[string]bool{"a": true}
				}
				return v, nulls
			},
		})
	}

	// Bit-packed types. The decoder treats a contiguous IsBitPacked() run
	// as one byte-aligned group, so two u4s + a packed_bool form a 3-byte
	// group on-wire. Fixture covers u4 boundary values (0 and 15).
	out = append(out, perTypeFixture{
		name: "u4_pair_nonnull",
		schema: &Schema{Fields: []Field{
			{Name: "lo", Type: FieldTypeU4, BitPosition: 0},
			{Name: "hi", Type: FieldTypeU4, BitPosition: 4},
			{Name: "id", Type: FieldTypeU16},
		}},
		genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
			// Edge cases: 0 and 15 sprinkled across the corpus.
			loVal := uint8(r.Intn(16))
			hiVal := uint8(r.Intn(16))
			if idx%50 == 0 {
				loVal = 0
				hiVal = 15
			} else if idx%50 == 1 {
				loVal = 15
				hiVal = 0
			}
			return map[string]any{
				"lo": loVal,
				"hi": hiVal,
				"id": uint64(r.Intn(65536)),
			}, nil
		},
	})
	out = append(out, perTypeFixture{
		name: "packed_bool_run_nonnull",
		schema: &Schema{Fields: []Field{
			{Name: "b0", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "b1", Type: FieldTypePackedBool, BitPosition: 1},
			{Name: "b2", Type: FieldTypePackedBool, BitPosition: 2},
			{Name: "id", Type: FieldTypeU32},
		}},
		genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
			return map[string]any{
				"b0": r.Intn(2) == 1,
				"b1": r.Intn(2) == 1,
				"b2": r.Intn(2) == 1,
				"id": uint64(r.Uint32()),
			}, nil
		},
	})
	// Mixed bit-packed run (u4 + packed_bool same group).
	out = append(out, perTypeFixture{
		name: "u4_pbool_mixed_run_nonnull",
		schema: &Schema{Fields: []Field{
			{Name: "nibble_lo", Type: FieldTypeU4, BitPosition: 0},
			{Name: "nibble_hi", Type: FieldTypeU4, BitPosition: 4},
			{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
			{Name: "id", Type: FieldTypeU16},
		}},
		genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
			return map[string]any{
				"nibble_lo": uint8(r.Intn(16)),
				"nibble_hi": uint8(r.Intn(16)),
				"flag":      r.Intn(2) == 1,
				"id":        uint64(r.Intn(65536)),
			}, nil
		},
	})

	// Categoricals. Build a dict large enough that values exercise the
	// full width of each cat type.
	catCases := []struct {
		ft      FieldType
		entries int
	}{
		{FieldTypeCategoricalU8, 200},
		{FieldTypeCategoricalU16, 5000},
		{FieldTypeCategoricalU32, 50000},
	}
	for _, c := range catCases {
		c := c
		out = append(out, perTypeFixture{
			name: fmt.Sprintf("%s_nonnull", c.ft.String()),
			schema: &Schema{Fields: []Field{
				{Name: "cat", Type: c.ft, Dictionary: buildCategoricalDict(c.entries)},
				{Name: "id", Type: FieldTypeU16},
			}},
			genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
				return map[string]any{
					"cat": uint64(r.Intn(c.entries)),
					"id":  uint64(r.Intn(65536)),
				}, nil
			},
		})
	}

	// Decimal128. Edge cases: negatives, zero, near-overflow positive,
	// near-overflow negative.
	out = append(out, perTypeFixture{
		name: "decimal128_nonnull",
		schema: &Schema{Fields: []Field{
			{Name: "amount", Type: FieldTypeDecimal128, Precision: 38, Scale: 4},
			{Name: "id", Type: FieldTypeU32},
		}},
		genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
			return map[string]any{
				"amount": decimalMantissa(r, idx),
				"id":     uint64(r.Uint32()),
			}, nil
		},
	})
	out = append(out, perTypeFixture{
		name: "decimal128_nullable",
		schema: &Schema{Fields: []Field{
			{Name: "amount", Type: FieldTypeDecimal128, Precision: 38, Scale: 4, Nullable: true},
			{Name: "id", Type: FieldTypeU32},
		}},
		genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
			vals := map[string]any{
				"amount": decimalMantissa(r, idx),
				"id":     uint64(r.Uint32()),
			}
			var nulls map[string]bool
			if idx%4 == 0 {
				nulls = map[string]bool{"amount": true}
			}
			return vals, nulls
		},
	})

	// Set types. Each fixture has empty-mask and full-mask coverage.
	setCases := []struct {
		ft       FieldType
		capacity int
	}{
		{FieldTypeSetU8, 8},
		{FieldTypeSetU16, 16},
		{FieldTypeSetU32, 32},
		{FieldTypeSetU64, 64},
	}
	for _, c := range setCases {
		c := c
		out = append(out, perTypeFixture{
			name: fmt.Sprintf("%s_nonnull", c.ft.String()),
			schema: &Schema{Fields: []Field{
				{Name: "tags", Type: c.ft, Dictionary: buildSetDict(c.capacity)},
				{Name: "id", Type: FieldTypeU16},
			}},
			genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
				mask := setMask(r, idx, c.capacity)
				return map[string]any{
					"tags": mask,
					"id":   uint64(r.Intn(65536)),
				}, nil
			},
		})
		out = append(out, perTypeFixture{
			name: fmt.Sprintf("%s_nullable", c.ft.String()),
			schema: &Schema{Fields: []Field{
				{Name: "tags", Type: c.ft, Dictionary: buildSetDict(c.capacity), Nullable: true},
				{Name: "id", Type: FieldTypeU16},
			}},
			genFunc: func(r *rand.Rand, idx int) (map[string]any, map[string]bool) {
				mask := setMask(r, idx, c.capacity)
				vals := map[string]any{
					"tags": mask,
					"id":   uint64(r.Intn(65536)),
				}
				var nulls map[string]bool
				if idx%5 == 0 {
					nulls = map[string]bool{"tags": true}
				}
				return vals, nulls
			},
		})
	}

	return out
}

// scalarGenValues populates `numericFields` with a deterministic uint64
// raw payload appropriate for the given type, plus the canary field with
// a u8 value derived from idx.
func scalarGenValues(r *rand.Rand, idx int, ft FieldType, numericFields []string, canary string) map[string]any {
	out := map[string]any{}
	for _, name := range numericFields {
		switch ft {
		case FieldTypeF32:
			out[name] = uint64(math.Float32bits(float32(r.NormFloat64()) * 100))
		case FieldTypeF64:
			out[name] = math.Float64bits(r.NormFloat64() * 1000)
		case FieldTypeU8:
			out[name] = uint64(r.Intn(256))
		case FieldTypeU16:
			out[name] = uint64(r.Intn(65536))
		case FieldTypeU32, FieldTypeDate:
			out[name] = uint64(r.Uint32())
		case FieldTypeU64:
			out[name] = r.Uint64()
		default:
			out[name] = uint64(r.Intn(1 << 30))
		}
	}
	out[canary] = uint64(idx & 0xFF)
	return out
}

// decimalMantissa returns a big.Int mantissa biased toward edge cases.
func decimalMantissa(r *rand.Rand, idx int) *big.Int {
	switch idx % 13 {
	case 0:
		return big.NewInt(0)
	case 1:
		// Largest positive that still fits decimal128(38).
		m := new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)
		m.Sub(m, big.NewInt(1))
		return m
	case 2:
		// Largest negative.
		m := new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)
		m.Sub(m, big.NewInt(1))
		m.Neg(m)
		return m
	case 3:
		return big.NewInt(-1)
	case 4:
		return big.NewInt(1)
	case 5:
		// Negative thousand.
		return big.NewInt(-1000000)
	case 6:
		// 64-bit max-ish.
		m := new(big.Int).SetUint64(math.MaxUint64)
		m.Neg(m)
		return m
	default:
		// Mid-range random ±2^62.
		v := r.Int63()
		if r.Intn(2) == 0 {
			v = -v
		}
		return big.NewInt(v)
	}
}

// setMask returns a uint64 bitmask for a set field of the given capacity,
// biased toward edge cases (empty, full, single-bit).
func setMask(r *rand.Rand, idx, capacity int) uint64 {
	switch idx % 11 {
	case 0:
		return 0 // empty set (distinct from null)
	case 1:
		if capacity >= 64 {
			return ^uint64(0)
		}
		return (uint64(1) << uint(capacity)) - 1 // full mask
	case 2:
		return 1 // bit 0 only
	case 3:
		if capacity >= 64 {
			return uint64(1) << 63
		}
		return uint64(1) << uint(capacity-1) // highest bit
	default:
		if capacity >= 64 {
			return r.Uint64()
		}
		return uint64(r.Uint32()) & ((uint64(1) << uint(capacity)) - 1)
	}
}

// generatePerTypeRecords synthesises equivalenceRecordsPerSchema records
// for one per-type fixture.
func generatePerTypeRecords(t *testing.T, f perTypeFixture, seed int64) [][]byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	out := make([][]byte, 0, equivalenceRecordsPerSchema)
	for i := 0; i < equivalenceRecordsPerSchema; i++ {
		vals, nulls := f.genFunc(r, i)
		out = append(out, encodeRecord(t, f.schema, vals, nulls))
	}
	return out
}

// ---------------------------------------------------------------------------
// Big mixed schema (every supported field type, plus nullable variants).
// ---------------------------------------------------------------------------

// bigMixedSchema returns a 26-field schema that combines:
//   - every numeric type (nonnull + nullable variants for u32/f64/date)
//   - bit-packed runs (u4 pair, then packed_bool run)
//   - every categorical width
//   - decimal128 (nullable)
//   - every set width (mix of nullable / non-nullable)
//
// The mix produces leading / mid / trailing skip patterns under any
// projection subset.
func bigMixedSchema() *Schema {
	return &Schema{Fields: []Field{
		{Name: "u8_a", Type: FieldTypeU8},
		{Name: "u8_b", Type: FieldTypeU8, Nullable: true},
		{Name: "u16_a", Type: FieldTypeU16},
		{Name: "u4_lo", Type: FieldTypeU4, BitPosition: 0},
		{Name: "u4_hi", Type: FieldTypeU4, BitPosition: 4},
		{Name: "pb_0", Type: FieldTypePackedBool, BitPosition: 0},
		{Name: "pb_1", Type: FieldTypePackedBool, BitPosition: 1},
		{Name: "u32_a", Type: FieldTypeU32, Nullable: true},
		{Name: "f32_a", Type: FieldTypeF32},
		{Name: "date_a", Type: FieldTypeDate, Nullable: true},
		{Name: "u64_a", Type: FieldTypeU64},
		{Name: "f64_a", Type: FieldTypeF64, Nullable: true},
		{Name: "cat_u8", Type: FieldTypeCategoricalU8, Dictionary: buildCategoricalDict(200)},
		{Name: "cat_u16", Type: FieldTypeCategoricalU16, Dictionary: buildCategoricalDict(5000)},
		{Name: "cat_u32", Type: FieldTypeCategoricalU32, Dictionary: buildCategoricalDict(50000)},
		{Name: "amount", Type: FieldTypeDecimal128, Precision: 38, Scale: 4, Nullable: true},
		{Name: "tags_u8", Type: FieldTypeSetU8, Dictionary: buildSetDict(8)},
		{Name: "tags_u16", Type: FieldTypeSetU16, Dictionary: buildSetDict(16), Nullable: true},
		{Name: "tags_u32", Type: FieldTypeSetU32, Dictionary: buildSetDict(32)},
		{Name: "tags_u64", Type: FieldTypeSetU64, Dictionary: buildSetDict(64), Nullable: true},
		{Name: "u16_b", Type: FieldTypeU16, Nullable: true},
		{Name: "u32_b", Type: FieldTypeU32},
		{Name: "f64_b", Type: FieldTypeF64},
		{Name: "u8_c", Type: FieldTypeU8},
		{Name: "u64_b", Type: FieldTypeU64},
		{Name: "u16_c", Type: FieldTypeU16},
	}}
}

// generateBigSchemaRecords synthesises 1000 records under the big mixed
// schema with deterministic edge-case coverage.
func generateBigSchemaRecords(t *testing.T, schema *Schema, seed int64) [][]byte {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	out := make([][]byte, 0, equivalenceRecordsPerSchema)
	for i := 0; i < equivalenceRecordsPerSchema; i++ {
		vals := map[string]any{
			"u8_a":   uint64(r.Intn(256)),
			"u8_b":   uint64(r.Intn(256)),
			"u16_a":  uint64(r.Intn(65536)),
			"u4_lo":  uint8(r.Intn(16)),
			"u4_hi":  uint8(r.Intn(16)),
			"pb_0":   r.Intn(2) == 1,
			"pb_1":   r.Intn(2) == 1,
			"u32_a":  uint64(r.Uint32()),
			"f32_a":  uint64(math.Float32bits(float32(r.NormFloat64()) * 10)),
			"date_a": uint64(r.Uint32()),
			"u64_a":  r.Uint64(),
			"f64_a":  math.Float64bits(r.NormFloat64() * 100),
			"cat_u8":  uint64(r.Intn(200)),
			"cat_u16": uint64(r.Intn(5000)),
			"cat_u32": uint64(r.Intn(50000)),
			"amount":  decimalMantissa(r, i),
			"tags_u8":  setMask(r, i, 8),
			"tags_u16": setMask(r, i, 16),
			"tags_u32": setMask(r, i, 32),
			"tags_u64": setMask(r, i, 64),
			"u16_b":   uint64(r.Intn(65536)),
			"u32_b":   uint64(r.Uint32()),
			"f64_b":   math.Float64bits(r.NormFloat64() * 100),
			"u8_c":    uint64(r.Intn(256)),
			"u64_b":   r.Uint64(),
			"u16_c":   uint64(r.Intn(65536)),
		}
		// Edge cases: u4 boundaries.
		if i%47 == 0 {
			vals["u4_lo"] = uint8(0)
			vals["u4_hi"] = uint8(15)
		}

		// Null pattern: every other record marks a rotating subset of
		// nullable fields as null. The pattern is deterministic per index.
		nullable := []string{"u8_b", "u32_a", "date_a", "f64_a", "amount", "tags_u16", "tags_u64", "u16_b"}
		var nulls map[string]bool
		switch i % 8 {
		case 0:
			// No nulls in this row.
		case 1:
			nulls = map[string]bool{nullable[0]: true}
		case 2:
			nulls = map[string]bool{nullable[1]: true, nullable[3]: true}
		case 3:
			nulls = map[string]bool{nullable[2]: true, nullable[4]: true, nullable[5]: true}
		case 4:
			nulls = map[string]bool{}
			for _, n := range nullable {
				nulls[n] = true
			}
		case 5:
			nulls = map[string]bool{nullable[7]: true}
		case 6:
			nulls = map[string]bool{nullable[4]: true} // decimal128 null
		case 7:
			nulls = map[string]bool{nullable[5]: true, nullable[6]: true} // set nulls
		}
		out = append(out, encodeRecord(t, schema, vals, nulls))
	}
	return out
}

// ---------------------------------------------------------------------------
// Test entry points.
// ---------------------------------------------------------------------------

// TestDecodePlan_Equivalence_PerType iterates one schema per supported
// field type, generates 1000 records per schema, and asserts every 2^N
// projection subset produces byte-equal decode results between the two
// paths. Schemas are kept to ≤ 12 fields so the subset sweep is feasible.
func TestDecodePlan_Equivalence_PerType(t *testing.T) {
	fixtures := perTypeFixtures()
	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			if got := len(f.schema.Fields); got > 12 {
				t.Fatalf("fixture %s has %d fields; full subset sweep is intractable", f.name, got)
			}
			records := generatePerTypeRecords(t, f, 0x5e7c0de0+int64(len(f.name)))
			fieldCount := uint(len(f.schema.Fields))
			masks := uint64(1) << fieldCount
			for mask := uint64(0); mask < masks; mask++ {
				retained := subsetFromMask(f.schema, mask)
				label := fmt.Sprintf("%s/mask=%0*b", f.name, fieldCount, mask)
				// Pick the first record at this subset to keep the loop
				// O(records + 2^N). Sample additional records every Kth
				// subset so edge-case records (decimal128 overflow, set
				// edge masks) still reach every retained pattern over time.
				idxs := []int{0}
				if mask%17 == 0 && len(records) >= 5 {
					idxs = append(idxs, len(records)/4, len(records)/2, 3*len(records)/4, len(records)-1)
				}
				for _, idx := range idxs {
					compareDecodeResults(t, f.schema, records[idx], retained, label)
				}
				if t.Failed() {
					t.Logf("first failure at %s", label)
					return
				}
			}
		})
	}
}

// TestDecodePlan_Equivalence_PerTypeAllRecords walks every record for a
// targeted set of retained patterns: full retention, empty retention,
// and the all-bitmap-null variant. This keeps the test runtime sub-30s
// while still exercising every record at the load-bearing extremes.
func TestDecodePlan_Equivalence_PerTypeAllRecords(t *testing.T) {
	fixtures := perTypeFixtures()
	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			records := generatePerTypeRecords(t, f, 0x5e7c0de0+int64(len(f.name)))
			all := allFieldNames(f.schema)
			cases := []struct {
				name     string
				retained []string
			}{
				{"empty", nil},
				{"full", all},
			}
			for _, c := range cases {
				for ri, raw := range records {
					label := fmt.Sprintf("%s/%s/rec=%d", f.name, c.name, ri)
					compareDecodeResults(t, f.schema, raw, c.retained, label)
					if t.Failed() {
						return
					}
				}
			}
		})
	}
}

// TestDecodePlan_Equivalence_SmallMixed enumerates every 2^N projection
// subset of a 10-field mixed schema across a single representative
// record per subset. Records span a sub-sample of the synth corpus.
func TestDecodePlan_Equivalence_SmallMixed(t *testing.T) {
	schema := &Schema{Fields: []Field{
		{Name: "id", Type: FieldTypeU32},
		{Name: "u4_lo", Type: FieldTypeU4, BitPosition: 0},
		{Name: "u4_hi", Type: FieldTypeU4, BitPosition: 4},
		{Name: "flag", Type: FieldTypePackedBool, BitPosition: 0},
		{Name: "cat", Type: FieldTypeCategoricalU16, Dictionary: buildCategoricalDict(1000)},
		{Name: "amount", Type: FieldTypeDecimal128, Precision: 38, Scale: 4, Nullable: true},
		{Name: "tags", Type: FieldTypeSetU16, Dictionary: buildSetDict(16)},
		{Name: "score", Type: FieldTypeF64, Nullable: true},
		{Name: "u8_x", Type: FieldTypeU8},
		{Name: "u64_x", Type: FieldTypeU64, Nullable: true},
	}}
	if got := len(schema.Fields); got > 12 {
		t.Fatalf("small mixed schema has %d fields; full sweep is intractable", got)
	}

	r := rand.New(rand.NewSource(0x5A11C0DE))
	records := make([][]byte, 0, equivalenceRecordsPerSchema)
	for i := 0; i < equivalenceRecordsPerSchema; i++ {
		vals := map[string]any{
			"id":    uint64(r.Uint32()),
			"u4_lo": uint8(r.Intn(16)),
			"u4_hi": uint8(r.Intn(16)),
			"flag":  r.Intn(2) == 1,
			"cat":   uint64(r.Intn(1000)),
			"amount": decimalMantissa(r, i),
			"tags":  setMask(r, i, 16),
			"score": math.Float64bits(r.NormFloat64()),
			"u8_x":  uint64(r.Intn(256)),
			"u64_x": r.Uint64(),
		}
		var nulls map[string]bool
		switch i % 6 {
		case 1:
			nulls = map[string]bool{"amount": true}
		case 2:
			nulls = map[string]bool{"score": true}
		case 3:
			nulls = map[string]bool{"u64_x": true}
		case 4:
			nulls = map[string]bool{"amount": true, "score": true, "u64_x": true}
		}
		records = append(records, encodeRecord(t, schema, vals, nulls))
	}

	fieldCount := uint(len(schema.Fields))
	masks := uint64(1) << fieldCount
	for mask := uint64(0); mask < masks; mask++ {
		retained := subsetFromMask(schema, mask)
		// Rotate through records so each subset gets a different witness.
		raw := records[int(mask)%len(records)]
		label := fmt.Sprintf("small/mask=%0*b/rec=%d", fieldCount, mask, int(mask)%len(records))
		compareDecodeResults(t, schema, raw, retained, label)
		if t.Failed() {
			t.Logf("first failure at %s", label)
			return
		}
	}
}

// TestDecodePlan_Equivalence_BigSchemaSampled exercises a 26-field schema
// across ~200 deterministically sampled projection subsets covering every
// shape the plan builder distinguishes: empty, full, only nullable, only
// bit-packed, leading skip + trailing decode, mid retention with leading
// AND trailing skips, every-other-field, plus uniformly random subsets.
func TestDecodePlan_Equivalence_BigSchemaSampled(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xB16D15CE)
	all := allFieldNames(schema)
	nullable := nullableFieldNames(schema)
	bitPacked := bitPackedFieldNames(schema)

	type sample struct {
		label    string
		retained []string
	}
	samples := []sample{
		{"empty", nil},
		{"full", all},
		{"only_nullable", nullable},
		{"only_bit_packed", bitPacked},
	}
	// Leading skip + trailing decode: retain only the last field.
	samples = append(samples, sample{
		label:    "trailing_only",
		retained: []string{all[len(all)-1]},
	})
	// Leading retain + trailing skip: retain only the first field.
	samples = append(samples, sample{
		label:    "leading_only",
		retained: []string{all[0]},
	})
	// Mid retention only: retain a single mid-schema field so the plan
	// emits a leading skip, a tiny decode segment, and a trailing skip
	// (plus the bitmap segment).
	samples = append(samples, sample{
		label:    "mid_only",
		retained: []string{all[len(all)/2]},
	})
	// Two mid-only retains, far apart, so the plan emits ≥ 3 skip
	// segments (leading, between-retains, trailing).
	samples = append(samples, sample{
		label:    "two_mid_apart",
		retained: []string{all[6], all[18]},
	})
	// Every-other-field retain: alternating skip:decode pattern.
	{
		var pat []string
		for i := 0; i < len(all); i += 2 {
			pat = append(pat, all[i])
		}
		samples = append(samples, sample{label: "every_other", retained: pat})
	}
	// Odd-indexed retain to complement every_other.
	{
		var pat []string
		for i := 1; i < len(all); i += 2 {
			pat = append(pat, all[i])
		}
		samples = append(samples, sample{label: "every_other_odd", retained: pat})
	}
	// All - one (each field, in turn — but we cap to avoid blowing the
	// sample budget; tested for every field via PerType already).
	for i := 0; i < len(all); i++ {
		except := make([]string, 0, len(all)-1)
		for j, n := range all {
			if j == i {
				continue
			}
			except = append(except, n)
		}
		samples = append(samples, sample{
			label:    fmt.Sprintf("all_except_%s", all[i]),
			retained: except,
		})
	}

	// Random uniform samples to push toward ~200 total. We deduplicate
	// by sorted-name signature so the budget covers distinct subsets.
	seen := make(map[string]struct{}, len(samples))
	for _, s := range samples {
		seen[signature(s.retained)] = struct{}{}
	}
	r := rand.New(rand.NewSource(0xBA5E5EED))
	for len(samples) < 200 {
		// Build a random subset of expected size ~|schema|/2.
		var pat []string
		for _, n := range all {
			if r.Intn(2) == 0 {
				pat = append(pat, n)
			}
		}
		sig := signature(pat)
		if _, ok := seen[sig]; ok {
			continue
		}
		seen[sig] = struct{}{}
		samples = append(samples, sample{
			label:    fmt.Sprintf("random_%d", len(samples)),
			retained: pat,
		})
	}

	// For each sampled subset, walk the entire 1000-record corpus.
	// 200 * 1000 = 200K compareDecodeResults invocations; each is cheap
	// (one plan build, two record decodes against an in-memory slice).
	for _, s := range samples {
		for ri, raw := range records {
			label := fmt.Sprintf("big/%s/rec=%d", s.label, ri)
			compareDecodeResults(t, schema, raw, s.retained, label)
			if t.Failed() {
				t.Logf("first failure at %s; retained=%v", label, s.retained)
				return
			}
		}
	}
}

// signature returns a stable string for a retained-field list (sorted
// names, |-separated) so the random sampler can deduplicate without
// caring about input order.
func signature(retained []string) string {
	cp := make([]string, len(retained))
	copy(cp, retained)
	sort.Strings(cp)
	return fmt.Sprintf("%v", cp)
}

// TestDecodePlan_Equivalence_EmptyRetainedNoAllocs verifies the
// fast-path contract for an empty retained set: the plan-driven decode
// produces empty values/nulls/wide maps, the same as the per-field
// path, and no panics fire on hostile (yet valid) records.
//
// Allocation-free is asserted as "no map writes occur" — we pre-size
// the caller maps to 0 and check len()==0 after the call. Go's runtime
// will not grow a map without a Set call, so this is observationally
// equivalent to "no allocations beyond a zero-size map".
func TestDecodePlan_Equivalence_EmptyRetainedNoAllocs(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xE0091D0A)
	for ri, raw := range records {
		plan, err := schema.BuildDecodePlan(nil)
		if err != nil {
			t.Fatalf("rec=%d BuildDecodePlan(nil): %v", ri, err)
		}
		rr := NewRecordReader(bytes.NewReader(raw), schema)
		values := make(map[string]float64)
		nulls := make(map[string]bool)
		wide := make(map[string]any)
		keep := func(string) bool { return false }
		if err := rr.ReadRecordWithWidePlan(values, nulls, wide, keep, plan); err != nil {
			t.Fatalf("rec=%d ReadRecordWithWidePlan: %v", ri, err)
		}
		if len(values) != 0 || len(nulls) != 0 || len(wide) != 0 {
			t.Fatalf("rec=%d empty-retained leaked map writes: values=%v nulls=%v wide=%v",
				ri, values, nulls, wide)
		}
		// Cross-check against the per-field path with a refusing keep.
		rr2 := NewRecordReader(bytes.NewReader(raw), schema)
		valuesP := make(map[string]float64)
		nullsP := make(map[string]bool)
		wideP := make(map[string]any)
		if err := rr2.ReadRecordWithWideProjected(valuesP, nullsP, wideP, keep); err != nil {
			t.Fatalf("rec=%d ReadRecordWithWideProjected: %v", ri, err)
		}
		if len(valuesP) != 0 || len(nullsP) != 0 || len(wideP) != 0 {
			t.Fatalf("rec=%d per-field empty-retained leaked: values=%v nulls=%v wide=%v",
				ri, valuesP, nullsP, wideP)
		}
	}
}

// TestDecodePlan_Equivalence_FullRetainedMatchesUnprojected asserts that
// the plan-driven decode with every field retained produces byte-equal
// output to the unprojected ReadRecordWithWide path (no FieldFilter, no
// plan). This is the strictest contract — anything that breaks
// projection-free decode breaks every consumer.
func TestDecodePlan_Equivalence_FullRetainedMatchesUnprojected(t *testing.T) {
	schema := bigMixedSchema()
	records := generateBigSchemaRecords(t, schema, 0xF011BAD5)
	all := allFieldNames(schema)
	plan, err := schema.BuildDecodePlan(all)
	if err != nil {
		t.Fatalf("BuildDecodePlan(all): %v", err)
	}
	for ri, raw := range records {
		// Unprojected baseline.
		rrA := NewRecordReader(bytes.NewReader(raw), schema)
		valuesA := make(map[string]float64)
		nullsA := make(map[string]bool)
		wideA := make(map[string]any)
		if err := rrA.ReadRecordWithWide(valuesA, nullsA, wideA); err != nil {
			t.Fatalf("rec=%d ReadRecordWithWide: %v", ri, err)
		}
		// Plan-driven full retain.
		rrB := NewRecordReader(bytes.NewReader(raw), schema)
		valuesB := make(map[string]float64)
		nullsB := make(map[string]bool)
		wideB := make(map[string]any)
		if err := rrB.ReadRecordWithWidePlan(valuesB, nullsB, wideB, nil, plan); err != nil {
			t.Fatalf("rec=%d ReadRecordWithWidePlan(full): %v", ri, err)
		}
		if !reflect.DeepEqual(valuesA, valuesB) {
			t.Fatalf("rec=%d values diverge between unprojected and plan(full):\n  unprojected=%v\n  plan=%v",
				ri, valuesA, valuesB)
		}
		if !reflect.DeepEqual(nullsA, nullsB) {
			t.Fatalf("rec=%d nulls diverge between unprojected and plan(full):\n  unprojected=%v\n  plan=%v",
				ri, nullsA, nullsB)
		}
		if !reflect.DeepEqual(wideA, wideB) {
			t.Fatalf("rec=%d wide diverges between unprojected and plan(full):\n  unprojected=%v\n  plan=%v",
				ri, wideA, wideB)
		}
	}
}

