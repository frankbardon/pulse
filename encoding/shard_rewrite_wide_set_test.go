package encoding_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// buildShardWithWideSet emits a minimal valid single-file .pulse shard
// with an id (u32) column and an `issuers` column of the given wide set
// rung. Records are (id, mask) pairs — the mask is in the shard's OWN
// dictionary frame, exactly as buildShardWithSet does for set_u8.
func buildShardWithWideSet(t *testing.T, ft encoding.FieldType, dictValues []string, records []wideSetRecord) ([]byte, *encoding.Schema) {
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
			{Name: "issuers", Type: ft, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d},
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
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], r.id)
		buf.Write(id[:])
		if err := encoding.WriteSetMask(&buf, ft, r.mask); err != nil {
			t.Fatalf("WriteSetMask: %v", err)
		}
	}
	return buf.Bytes(), schema
}

type wideSetRecord struct {
	id   uint32
	mask encoding.SetMask
}

// decodeWideSetShard reads back a wide-set shard's records.
func decodeWideSetShard(t *testing.T, b []byte, ft encoding.FieldType) (*encoding.Schema, []wideSetRecord) {
	t.Helper()
	r := bytes.NewReader(b)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	stride := 4 + ft.ByteSize()
	tail := make([]byte, r.Len())
	if _, err := r.Read(tail); err != nil {
		t.Fatalf("read tail: %v", err)
	}
	if len(tail)%stride != 0 {
		t.Fatalf("payload %d bytes is not a multiple of stride %d", len(tail), stride)
	}
	out := make([]wideSetRecord, 0, len(tail)/stride)
	for i := 0; i < len(tail); i += stride {
		m, err := encoding.SetMaskFromBytes(ft, tail[i+4:i+stride])
		if err != nil {
			t.Fatalf("SetMaskFromBytes: %v", err)
		}
		out = append(out, wideSetRecord{id: binary.LittleEndian.Uint32(tail[i : i+4]), mask: m})
	}
	return schema, out
}

func maskOf(bits ...int) encoding.SetMask {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	return m
}

// TestMergeDictUnion_Divergent_RewritesWideSetMasks is the wide-rung
// analog of TestMergeDictUnion_Divergent_RewritesSetMasks. The narrow
// case rewrites bits inside one uint64; the wide case must move bits
// ACROSS words — a bit at incoming position 1 landing at union position
// 70 crosses from words[0] into words[1], and a rewrite that forgot the
// high words would drop the selection silently while still producing a
// parseable record.
func TestMergeDictUnion_Divergent_RewritesWideSetMasks(t *testing.T) {
	// Canonical dict holds 70 labels: label-0..label-69. Incoming holds
	// three of them in a DIFFERENT order plus one new label, so the union
	// both reorders and appends.
	canonicalLabels := make([]string, 70)
	for i := range canonicalLabels {
		canonicalLabels[i] = labelAt(i)
	}
	canonical := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "issuers", Type: encoding.FieldTypeSetU128, ByteOffset: 4,
				CsvColumnIdx: 1, Dictionary: dictOf(t, canonicalLabels...)},
		},
	}

	// Incoming dict: label-69=0, label-0=1, label-64=2, brand-new=3.
	// Union appends "brand-new" at 70, so the remap is
	//   0→69, 1→0, 2→64, 3→70
	// which moves bits in BOTH directions across the 64-bit word line.
	incomingBytes, incomingSchema := buildShardWithWideSet(t, encoding.FieldTypeSetU128,
		[]string{labelAt(69), labelAt(0), labelAt(64), "brand-new"},
		[]wideSetRecord{
			{1, maskOf(0)},          // label-69 → union bit 69 (words[1])
			{2, maskOf(1)},          // label-0  → union bit 0  (words[0])
			{3, maskOf(2)},          // label-64 → union bit 64 (words[1])
			{4, maskOf(3)},          // brand-new → union bit 70 (words[1])
			{5, maskOf(0, 1, 2, 3)}, // all four at once
			{6, encoding.SetMask{}}, // empty mask survives intact
		})

	extended, remap, err := encoding.MergeDictUnion(canonical, incomingSchema)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}
	if got := extended.Fields[1].Dictionary.Count(); got != 71 {
		t.Fatalf("union dict size: got %d, want 71", got)
	}

	r, ok := remap[1]
	if !ok {
		t.Fatalf("expected remap for field index 1, got %v", remap)
	}
	wantRemap := encoding.DictRemap{0: 69, 1: 0, 2: 64, 3: 70}
	for k, v := range wantRemap {
		if r[k] != v {
			t.Errorf("remap[%d]: got %d, want %d", k, r[k], v)
		}
	}

	rewritten, err := encoding.RewriteShardCategoricals(incomingBytes, extended, remap)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}
	_, recs := decodeWideSetShard(t, rewritten, encoding.FieldTypeSetU128)

	want := []wideSetRecord{
		{1, maskOf(69)},
		{2, maskOf(0)},
		{3, maskOf(64)},
		{4, maskOf(70)},
		{5, maskOf(0, 64, 69, 70)},
		{6, encoding.SetMask{}},
	}
	if len(recs) != len(want) {
		t.Fatalf("rewritten record count: got %d, want %d", len(recs), len(want))
	}
	for i := range recs {
		if recs[i].id != want[i].id {
			t.Errorf("rec[%d] id: got %d, want %d", i, recs[i].id, want[i].id)
		}
		if !recs[i].mask.Equal(want[i].mask) {
			t.Errorf("rec[%d] mask: got words %v, want words %v",
				i, recs[i].mask.Words(), want[i].mask.Words())
		}
	}
}

// TestRewriteShardCategoricals_WideSet_HighWordRemapSurvives pins the
// set_u256 rung across its whole 256-bit range in BOTH directions. The
// SOURCE bits sit in every word (0, 65, 130, 200) and the remap moves
// each to a different word, including the last legal bit. Two distinct
// regressions are caught here and neither raises an error on its own:
// a rewrite that only WALKS words[0] leaves the high selections
// untranslated (they keep pointing at the shard's old dictionary frame),
// and one that only WRITES words[0] drops them entirely.
func TestRewriteShardCategoricals_WideSet_HighWordRemapSurvives(t *testing.T) {
	ft := encoding.FieldTypeSetU256
	labels := make([]string, 256)
	for i := range labels {
		labels[i] = labelAt(i)
	}
	src, schema := buildShardWithWideSet(t, ft, labels, []wideSetRecord{
		{1, maskOf(0, 65, 130, 200)},
		{2, maskOf(255)},        // untouched by the remap: stays put
		{3, encoding.SetMask{}}, // empty selection survives
	})
	// Every source bit moves to a DIFFERENT word than the one it started
	// in, so neither a walk nor a write confined to the low word can
	// reproduce the expected result.
	remap := map[int]encoding.DictRemap{
		1: {0: 254, 65: 3, 130: 64, 200: 129},
	}
	rewritten, err := encoding.RewriteShardCategoricals(src, schema, remap)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}
	_, recs := decodeWideSetShard(t, rewritten, ft)
	want := []encoding.SetMask{
		maskOf(3, 64, 129, 254),
		maskOf(255),
		{},
	}
	if len(recs) != len(want) {
		t.Fatalf("record count: got %d, want %d", len(recs), len(want))
	}
	for i := range want {
		if !recs[i].mask.Equal(want[i]) {
			t.Fatalf("rec[%d] mask: got words %v, want words %v",
				i, recs[i].mask.Words(), want[i].Words())
		}
	}
}

// TestRewriteShardCategoricals_WideSet_HighSourceBitOverflows asserts the
// width guard fires on a bit that STARTED in a high word. The low-word
// arm of the guard is exercised by the narrow rungs; this is the one that
// can only be reached by walking past bit 63.
func TestRewriteShardCategoricals_WideSet_HighSourceBitOverflows(t *testing.T) {
	ft := encoding.FieldTypeSetU128
	labels := make([]string, 128)
	for i := range labels {
		labels[i] = labelAt(i)
	}
	src, schema := buildShardWithWideSet(t, ft, labels, []wideSetRecord{
		{1, maskOf(100)},
	})
	remap := map[int]encoding.DictRemap{
		1: {100: 200}, // legal for SetMask's 256-bit array, illegal for set_u128
	}
	_, err := encoding.RewriteShardCategoricals(src, schema, remap)
	if err == nil {
		t.Fatal("expected PULSE_SHARD_DICT_WIDTH_OVERFLOW, got nil error")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW {
		t.Fatalf("expected PULSE_SHARD_DICT_WIDTH_OVERFLOW, got %v", err)
	}
}

// TestRewriteShardCategoricals_WideSet_WidthOverflow asserts a remap
// that would push a bit past the rung's declared capacity surfaces
// PULSE_SHARD_DICT_WIDTH_OVERFLOW instead of silently dropping the
// selection. set_u128 caps at 128 bits even though SetMask itself
// carries 256.
func TestRewriteShardCategoricals_WideSet_WidthOverflow(t *testing.T) {
	ft := encoding.FieldTypeSetU128
	src, schema := buildShardWithWideSet(t, ft, []string{"a", "b"}, []wideSetRecord{
		{1, maskOf(0)},
	})
	remap := map[int]encoding.DictRemap{
		1: {0: 128}, // one past the last legal bit for set_u128
	}
	_, err := encoding.RewriteShardCategoricals(src, schema, remap)
	if err == nil {
		t.Fatal("expected PULSE_SHARD_DICT_WIDTH_OVERFLOW, got nil error")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW {
		t.Fatalf("expected PULSE_SHARD_DICT_WIDTH_OVERFLOW, got %v", err)
	}
}

// TestRewriteShardCategoricals_WideSet_EmptyRemapPreservesBytes asserts
// the verbatim-copy arm still holds when a wide set is present: an empty
// remap re-emits the schema but must not touch a single record byte.
func TestRewriteShardCategoricals_WideSet_EmptyRemapPreservesBytes(t *testing.T) {
	ft := encoding.FieldTypeSetU256
	recsIn := []wideSetRecord{
		{1, maskOf(0, 63, 64, 127, 128, 191, 192, 255)},
		{2, encoding.SetMask{}},
		{3, maskOf(7)},
	}
	src, schema := buildShardWithWideSet(t, ft, []string{"a", "b", "c"}, recsIn)
	rewritten, err := encoding.RewriteShardCategoricals(src, schema, nil)
	if err != nil {
		t.Fatalf("RewriteShardCategoricals: %v", err)
	}
	if !bytes.Equal(src, rewritten) {
		t.Fatalf("empty remap changed %d bytes of a %d-byte shard", countDiff(src, rewritten), len(src))
	}
	_, recs := decodeWideSetShard(t, rewritten, ft)
	for i := range recsIn {
		if !recs[i].mask.Equal(recsIn[i].mask) {
			t.Errorf("rec[%d] mask: got %v, want %v", i, recs[i].mask.Words(), recsIn[i].mask.Words())
		}
	}
}

func labelAt(i int) string {
	return "label-" + itoaShard(i)
}

func itoaShard(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

func countDiff(a, b []byte) int {
	n := 0
	if len(a) != len(b) {
		return max(len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			n++
		}
	}
	return n
}
