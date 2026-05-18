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
