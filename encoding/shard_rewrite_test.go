package encoding_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// buildShard emits a minimal valid single-file .pulse shard with an
// id (u32) column and a country (categorical_u8) column. Records are
// supplied as (id, countryIndex) pairs.
func buildShard(t *testing.T, dictValues []string, records [][2]uint32) ([]byte, *encoding.Schema) {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range dictValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: d},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range records {
		var rec [5]byte
		binary.LittleEndian.PutUint32(rec[0:4], r[0])
		rec[4] = byte(r[1])
		buf.Write(rec[:])
	}
	return buf.Bytes(), schema
}

func decodeShardRecords(t *testing.T, b []byte) (*encoding.Schema, [][2]uint32) {
	t.Helper()
	r := bytes.NewReader(b)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	tail := make([]byte, r.Len())
	if _, err := r.Read(tail); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	out := make([][2]uint32, 0, len(tail)/5)
	for i := 0; i < len(tail); i += 5 {
		id := binary.LittleEndian.Uint32(tail[i : i+4])
		ci := uint32(tail[i+4])
		out = append(out, [2]uint32{id, ci})
	}
	return schema, out
}

// buildShardWithSet emits a minimal valid single-file .pulse shard with
// an id (u32) column and an issuers (set_u8) column. Records are
// supplied as (id, mask) pairs — the mask is the raw bitmask in the
// shard's own dictionary frame.
func buildShardWithSet(t *testing.T, dictValues []string, records [][2]uint32) ([]byte, *encoding.Schema) {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range dictValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "issuers", Type: encoding.FieldTypeSetU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: d},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range records {
		var rec [5]byte
		binary.LittleEndian.PutUint32(rec[0:4], r[0])
		rec[4] = byte(r[1])
		buf.Write(rec[:])
	}
	return buf.Bytes(), schema
}

func decodeShardRecordsWithSet(t *testing.T, b []byte) (*encoding.Schema, [][2]uint32) {
	t.Helper()
	r := bytes.NewReader(b)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	tail := make([]byte, r.Len())
	if _, err := r.Read(tail); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	out := make([][2]uint32, 0, len(tail)/5)
	for i := 0; i < len(tail); i += 5 {
		id := binary.LittleEndian.Uint32(tail[i : i+4])
		mask := uint32(tail[i+4])
		out = append(out, [2]uint32{id, mask})
	}
	return schema, out
}

// TestMergeDictUnion_Divergent_RewritesSetMasks pins the set-field
// analog of TestMergeDictUnion_Divergent_RewritesIndices. The
// categorical case rewrites a single index per record; the set case
// must rewrite EVERY set bit independently according to the dict
// remap. A divergent union that both reorders an existing label and
// appends a new one is the failure mode most likely to escape the
// remapSetMask unit case. Silent-corruption risk if this bit math
// regresses, since masks would still parse but reference the wrong
// dictionary labels.
func TestMergeDictUnion_Divergent_RewritesSetMasks(t *testing.T) {
	// Canonical dict: Apple=0, Banana=1, Cherry=2.
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "issuers", Type: encoding.FieldTypeSetU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: dictOf(t, "Apple", "Banana", "Cherry")},
		},
	}
	// Incoming dict: Banana=0, Apple=1, Date=2 — reorders Apple/Banana
	// and introduces Date as a new label. Union must become
	// [Apple, Banana, Cherry, Date] with remap {0→1, 1→0, 2→3}.
	incomingBytes, incomingSchema := buildShardWithSet(t,
		[]string{"Banana", "Apple", "Date"},
		[][2]uint32{
			{1, 0b001}, // Banana only         (incoming bit 0)
			{2, 0b010}, // Apple only          (incoming bit 1)
			{3, 0b100}, // Date only           (incoming bit 2)
			{4, 0b111}, // Banana+Apple+Date
			{5, 0b000}, // empty mask survives intact
		})

	extended, remap, err := encoding.MergeDictUnion(canonical, incomingSchema)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}

	gotDict := extended.Fields[1].Dictionary.Values()
	wantDict := []string{"Apple", "Banana", "Cherry", "Date"}
	if len(gotDict) != len(wantDict) {
		t.Fatalf("union dict size: got %d (%v), want %d (%v)", len(gotDict), gotDict, len(wantDict), wantDict)
	}
	for i := range gotDict {
		if gotDict[i] != wantDict[i] {
			t.Errorf("dict[%d]: got %q, want %q", i, gotDict[i], wantDict[i])
		}
	}

	r, ok := remap[1]
	if !ok {
		t.Fatalf("expected remap for field index 1, got %v", remap)
	}
	wantRemap := encoding.DictRemap{0: 1, 1: 0, 2: 3}
	if len(r) != len(wantRemap) {
		t.Errorf("remap size: got %d (%v), want %d (%v)", len(r), r, len(wantRemap), wantRemap)
	}
	for k, v := range wantRemap {
		if r[k] != v {
			t.Errorf("remap[%d]: got %d, want %d", k, r[k], v)
		}
	}

	rewritten, err := encoding.RewriteShardCategoricals(incomingBytes, extended, remap)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}

	rewrittenSchema, recs := decodeShardRecordsWithSet(t, rewritten)
	rwDict := rewrittenSchema.Fields[1].Dictionary.Values()
	if len(rwDict) != 4 || rwDict[3] != "Date" {
		t.Fatalf("rewritten shard dict: got %v", rwDict)
	}

	// Hand-computed expected masks in the UNION frame:
	//   Banana=1, Apple=0, Cherry=2, Date=3.
	// id=1: Banana (incoming 0b001) → Banana in union frame is bit 1 → 0b010
	// id=2: Apple (incoming 0b010)  → Apple  in union frame is bit 0 → 0b001
	// id=3: Date  (incoming 0b100)  → Date   in union frame is bit 3 → 0b1000
	// id=4: B+A+D (incoming 0b111)  → bits 0+1+3 in union frame      → 0b1011
	// id=5: empty                                                    → 0b000
	wantRecs := [][2]uint32{
		{1, 0b0010},
		{2, 0b0001},
		{3, 0b1000},
		{4, 0b1011},
		{5, 0b0000},
	}
	if len(recs) != len(wantRecs) {
		t.Fatalf("rewritten record count: got %d, want %d", len(recs), len(wantRecs))
	}
	for i := range recs {
		if recs[i] != wantRecs[i] {
			t.Errorf("rec[%d]: got id=%d mask=0b%04b, want id=%d mask=0b%04b",
				i, recs[i][0], recs[i][1], wantRecs[i][0], wantRecs[i][1])
		}
	}
}

func TestMergeDictUnion_Divergent_RewritesIndices(t *testing.T) {
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: dictOf(t, "US", "CA", "MX")},
		},
	}
	incomingBytes, incomingSchema := buildShard(t, []string{"US", "CA", "BR"}, [][2]uint32{
		{1, 0}, // US
		{2, 2}, // BR
		{3, 1}, // CA
		{4, 2}, // BR
	})

	extended, remap, err := encoding.MergeDictUnion(canonical, incomingSchema)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}

	dict := extended.Fields[1].Dictionary
	got := dict.Values()
	want := []string{"US", "CA", "MX", "BR"}
	if len(got) != len(want) {
		t.Fatalf("union dict size: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("dict[%d]: got %q, want %q", i, got[i], want[i])
		}
	}

	if _, ok := remap[1]; !ok {
		t.Fatalf("expected remap for field index 1, got %v", remap)
	}
	if remap[1][2] != 3 { // BR moves from incoming idx 2 to canonical idx 3
		t.Errorf("expected remap[1][2]==3, got %d", remap[1][2])
	}
	if _, identityKept := remap[1][0]; identityKept {
		t.Errorf("identity remap entries should be omitted, got %v", remap[1])
	}

	rewritten, err := encoding.RewriteShardCategoricals(incomingBytes, extended, remap)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}

	rewrittenSchema, recs := decodeShardRecords(t, rewritten)
	rwDict := rewrittenSchema.Fields[1].Dictionary.Values()
	if len(rwDict) != 4 || rwDict[3] != "BR" {
		t.Fatalf("rewritten shard dict: got %v", rwDict)
	}

	wantRecs := [][2]uint32{
		{1, 0}, // US -> 0
		{2, 3}, // BR -> 3
		{3, 1}, // CA -> 1
		{4, 3}, // BR -> 3
	}
	if len(recs) != len(wantRecs) {
		t.Fatalf("rewritten record count: got %d, want %d", len(recs), len(wantRecs))
	}
	for i := range recs {
		if recs[i] != wantRecs[i] {
			t.Errorf("rec[%d]: got %v, want %v", i, recs[i], wantRecs[i])
		}
	}
}

func TestMergeDictUnion_Prefix_NoRemap(t *testing.T) {
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0,
				CsvColumnIdx: 0, Dictionary: dictOf(t, "US", "CA", "MX")},
		},
	}
	incoming := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0,
				CsvColumnIdx: 0, Dictionary: dictOf(t, "US", "CA")},
		},
	}
	extended, remap, err := encoding.MergeDictUnion(canonical, incoming)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}
	if extended != canonical {
		t.Errorf("expected canonical pointer unchanged when no extension")
	}
	if len(remap) != 0 {
		t.Errorf("expected empty remap for prefix case, got %v", remap)
	}
}

func TestMergeDictUnion_WidthOverflow(t *testing.T) {
	// categorical_u8 capacity is 256. Build canonical with 250 values
	// and incoming with 50 new values; union exceeds 256.
	makeDict := func(prefix string, start, count int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := start; i < start+count; i++ {
			if _, err := d.Add(prefix + string(rune('A'+i%26)) + string(rune('0'+(i/26)%10))); err != nil {
				t.Fatalf("dict add: %v", err)
			}
		}
		return d
	}
	canon := makeDict("c", 0, 250)
	inc := makeDict("i", 0, 50) // entirely new entries
	canonical := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, Dictionary: canon},
	}}
	incoming := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeCategoricalU8, Dictionary: inc},
	}}
	_, _, err := encoding.MergeDictUnion(canonical, incoming)
	if err == nil {
		t.Fatal("expected width-overflow error")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW {
		t.Errorf("expected PULSE_SHARD_DICT_WIDTH_OVERFLOW, got %v", err)
	}
}

func TestRewriteShardCategoricals_EmptyRemap_PreservesRecords(t *testing.T) {
	target := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: dictOf(t, "US", "CA", "MX", "BR")},
		},
	}
	shardBytes, _ := buildShard(t, []string{"US", "CA"}, [][2]uint32{
		{10, 0}, {11, 1},
	})
	out, err := encoding.RewriteShardCategoricals(shardBytes, target, nil)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}
	rewrittenSchema, recs := decodeShardRecords(t, out)
	if len(rewrittenSchema.Fields[1].Dictionary.Values()) != 4 {
		t.Errorf("expected target union dict carried in shard schema")
	}
	want := [][2]uint32{{10, 0}, {11, 1}}
	for i := range recs {
		if recs[i] != want[i] {
			t.Errorf("rec[%d]: got %v, want %v", i, recs[i], want[i])
		}
	}
}

// buildPackedShard emits a minimal valid single-file .pulse shard whose
// schema mixes byte-aligned fields with bit-packed fields. Layout:
//
//	offset 0 : id              (u64,            8 bytes)
//	offset 8 : brand           (categorical_u8, 1 byte)
//	offset 9 : aware           (packed_bool,    1 byte on wire)
//	offset 10: familiarity     (u4,             1 byte on wire)
//	offset 11: weight          (f64,            8 bytes)
//
// True record stride is 19 bytes. The pre-fix recordSize calc that
// summed FieldType.ByteSize() reported 17 (it dropped the two packed
// bytes), which fired PULSE_SHARD_HEADER_INVALID on every shard whose
// dictionary required a non-empty remap. This is the brand_respondent
// layout from issue #42.
func buildPackedShard(t *testing.T, brandValues []string, records []packedRec) ([]byte, *encoding.Schema) {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range brandValues {
		if _, err := d.Add(v); err != nil {
			t.Fatalf("dict add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8,
				CsvColumnIdx: 1, Dictionary: d},
			{Name: "aware", Type: encoding.FieldTypePackedBool, ByteOffset: 9, BitPosition: 0, CsvColumnIdx: 2},
			{Name: "familiarity", Type: encoding.FieldTypeU4, ByteOffset: 10, BitPosition: 0, CsvColumnIdx: 3},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 11, CsvColumnIdx: 4},
		},
	}
	// True wire stride is 19 (8+1+1+1+8). Asserted via direct byte
	// counts below, not via Schema.RecordByteSize, so the test
	// exercises the rewrite codepath end-to-end even if the helper
	// were ever regressed back to summing ByteSize().
	const wireStride = 19
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range records {
		var rec [wireStride]byte
		binary.LittleEndian.PutUint64(rec[0:8], r.id)
		rec[8] = r.brandIdx
		rec[9] = r.aware
		rec[10] = r.familiarity
		binary.LittleEndian.PutUint64(rec[11:19], r.weightBits)
		buf.Write(rec[:])
	}
	return buf.Bytes(), schema
}

type packedRec struct {
	id          uint64
	brandIdx    byte
	aware       byte // 0 or 1
	familiarity byte // 0x00..0x0F (4-bit value; null state would live in bitmap)
	weightBits  uint64
}

func decodePackedShard(t *testing.T, b []byte) (*encoding.Schema, []packedRec) {
	t.Helper()
	r := bytes.NewReader(b)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	tail := make([]byte, r.Len())
	if _, err := r.Read(tail); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(tail)%19 != 0 {
		t.Fatalf("packed shard tail length %d not a multiple of 19", len(tail))
	}
	out := make([]packedRec, 0, len(tail)/19)
	for i := 0; i < len(tail); i += 19 {
		out = append(out, packedRec{
			id:          binary.LittleEndian.Uint64(tail[i : i+8]),
			brandIdx:    tail[i+8],
			aware:       tail[i+9],
			familiarity: tail[i+10],
			weightBits:  binary.LittleEndian.Uint64(tail[i+11 : i+19]),
		})
	}
	return schema, out
}

// Regression for issue #42: RewriteShardCategoricals must walk the
// true wire stride (which includes the host byte of every bit-packed
// field), not the sum of FieldType.ByteSize(). Before the fix the
// non-empty remap path raised PULSE_SHARD_HEADER_INVALID on schemas
// that mixed byte-aligned and bit-packed fields.
func TestRewriteShardCategoricals_BitPackedSchema_NonEmptyRemap(t *testing.T) {
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "brand", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8,
				CsvColumnIdx: 1, Dictionary: dictOf(t, "Acme", "Globex", "Initech")},
			{Name: "aware", Type: encoding.FieldTypePackedBool, ByteOffset: 9, BitPosition: 0, CsvColumnIdx: 2},
			{Name: "familiarity", Type: encoding.FieldTypeU4, ByteOffset: 10, BitPosition: 0, CsvColumnIdx: 3},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 11, CsvColumnIdx: 4},
		},
	}

	// Incoming dict introduces "Soylent" (idx 2) that's absent from
	// canonical, forcing MergeDictUnion to emit a non-empty remap.
	incomingBytes, incomingSchema := buildPackedShard(t,
		[]string{"Acme", "Globex", "Soylent"},
		[]packedRec{
			{id: 100, brandIdx: 0, aware: 1, familiarity: 0x05, weightBits: 0x1111},
			{id: 101, brandIdx: 2, aware: 0, familiarity: 0x0F, weightBits: 0x2222},
			{id: 102, brandIdx: 1, aware: 1, familiarity: 0x03, weightBits: 0x3333},
			{id: 103, brandIdx: 2, aware: 0, familiarity: 0x07, weightBits: 0x4444},
		})

	extended, remap, err := encoding.MergeDictUnion(canonical, incomingSchema)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}
	if len(remap) == 0 {
		t.Fatalf("expected non-empty remap from divergent dict, got %v", remap)
	}
	if remap[1][2] != 3 { // Soylent: incoming 2 → canonical 3 (appended)
		t.Errorf("expected remap[1][2]==3, got %v", remap[1])
	}

	// The bug under issue #42: this call returned
	// PULSE_SHARD_HEADER_INVALID with record_size:17 (true stride is 19).
	rewritten, err := encoding.RewriteShardCategoricals(incomingBytes, extended, remap)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals returned %v; want nil", err)
	}

	_, recs := decodePackedShard(t, rewritten)
	want := []packedRec{
		{id: 100, brandIdx: 0, aware: 1, familiarity: 0x05, weightBits: 0x1111},
		{id: 101, brandIdx: 3, aware: 0, familiarity: 0x0F, weightBits: 0x2222}, // brand remapped 2->3
		{id: 102, brandIdx: 1, aware: 1, familiarity: 0x03, weightBits: 0x3333},
		{id: 103, brandIdx: 3, aware: 0, familiarity: 0x07, weightBits: 0x4444}, // brand remapped 2->3
	}
	if len(recs) != len(want) {
		t.Fatalf("rewritten record count: got %d, want %d", len(recs), len(want))
	}
	for i := range recs {
		if recs[i] != want[i] {
			t.Errorf("rec[%d]: got %+v, want %+v", i, recs[i], want[i])
		}
	}
}

// TestSchema_RecordByteSize_MixedPackedAndAligned asserts the helper
// reports the true wire stride for the schema shape that broke shard
// rewrite under issue #42.
func TestSchema_RecordByteSize_MixedPackedAndAligned(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0},
			{Name: "brand", Type: encoding.FieldTypeCategoricalU16, ByteOffset: 8},
			{Name: "aware", Type: encoding.FieldTypePackedBool, ByteOffset: 10, BitPosition: 0},
			{Name: "familiarity", Type: encoding.FieldTypeU4, ByteOffset: 11, BitPosition: 0},
			{Name: "trust", Type: encoding.FieldTypePackedBool, ByteOffset: 12, BitPosition: 0},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 13},
		},
	}
	got := schema.RecordByteSize()
	if got != 21 {
		t.Fatalf("RecordByteSize: got %d, want 21 (8+2+1+1+1+8)", got)
	}
}
