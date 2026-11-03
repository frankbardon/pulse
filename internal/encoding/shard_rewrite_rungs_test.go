package encoding_test

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// rungSchema carries one field of every dictionary rung the shard
// rewrite remaps, behind a plain u32 prefix.
func rungSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			if _, err := d.Add(string(rune('A'+i%26)) + string(rune('a'+i/26))); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "c16", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict(4)},
		{Name: "c32", Type: encoding.FieldTypeCategoricalU32, Dictionary: dict(4)},
		{Name: "s8", Type: encoding.FieldTypeSetU8, Dictionary: dict(8)},
		{Name: "s16", Type: encoding.FieldTypeSetU16, Dictionary: dict(16)},
		{Name: "s32", Type: encoding.FieldTypeSetU32, Dictionary: dict(32)},
		{Name: "s64", Type: encoding.FieldTypeSetU64, Dictionary: dict(64)},
		{Name: "s128", Type: encoding.FieldTypeSetU128, Dictionary: dict(128)},
	}}
}

// rungShard is a single-file shard of rungSchema with one record whose
// every dictionary field holds entry / bit 1.
func rungShard(t *testing.T, s *encoding.Schema) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := encx.WritePreamble(&b, s); err != nil {
		t.Fatal(err)
	}
	for _, f := range s.Fields {
		var err error
		switch f.Type {
		case encoding.FieldTypeU32:
			err = encoding.WriteFieldValue(&b, f.Type, 77)
		case encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
			err = encoding.WriteFieldValue(&b, f.Type, 1)
		case encoding.FieldTypeSetU128:
			err = encoding.WriteSetMask(&b, f.Type, encoding.SetMask{}.WithBit(1))
		default:
			err = encoding.WriteFieldValue(&b, f.Type, 1<<1)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

// TestRewriteShardCategoricals_EveryRungRemaps: an index / bit remap
// lands on every dictionary rung at its own type-derived offset, and
// the non-dictionary prefix is untouched.
func TestRewriteShardCategoricals_EveryRungRemaps(t *testing.T) {
	t.Parallel()

	s := rungSchema(t)
	shard := rungShard(t, s)
	remap := map[int]encx.DictRemap{}
	for i := 1; i < len(s.Fields); i++ {
		remap[i] = encx.DictRemap{1: 3}
	}
	out, err := encx.RewriteShardCategoricals(shard, s, remap)
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	r := bytes.NewReader(out)
	got, _, err := encx.ReadPreamble(r)
	if err != nil {
		t.Fatal(err)
	}
	vals, wide := map[string]float64{}, map[string]any{}
	if err := encx.NewRecordReader(r, got).ReadRecordWithWide(vals, map[string]bool{}, wide); err != nil {
		t.Fatal(err)
	}
	if vals["id"] != 77 {
		t.Errorf("prefix id = %v, want 77 (untouched)", vals["id"])
	}
	for _, n := range []string{"c16", "c32"} {
		if vals[n] != 3 {
			t.Errorf("%s = %v, want index 3", n, vals[n])
		}
	}
	for _, n := range []string{"s8", "s16", "s32", "s64"} {
		if vals[n] != 1<<3 {
			t.Errorf("%s = %v, want mask bit 3 (%d)", n, vals[n], 1<<3)
		}
	}
	if m, ok := wide["s128"].(encoding.SetMask); !ok || !m.Has(3) || m.Has(1) {
		t.Errorf("s128 = %v, want only bit 3", wide["s128"])
	}
}

// TestRewriteShardCategoricals_Refusals: every malformed input and every
// remap that would not fit its field is refused with its own code —
// never written as a record that still parses.
func TestRewriteShardCategoricals_Refusals(t *testing.T) {
	t.Parallel()

	s := rungSchema(t)
	shard := rungShard(t, s)
	fewer := &encoding.Schema{Fields: s.Fields[:3]}
	cases := []struct {
		name   string
		shard  []byte
		target *encoding.Schema
		remap  map[int]encx.DictRemap
		code   errors.Code
	}{
		{"nil target", shard, nil, nil, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"not a cohort", []byte("garbage!!"), s, nil, errors.PULSE_SHARD_HEADER_INVALID},
		{"schema cut short", shard[:encoding.HeaderSize+2], s, nil, errors.PULSE_SHARD_HEADER_INVALID},
		{"field count differs", shard, fewer, nil, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"partial trailing record", append(bytes.Clone(shard), 0xEE), s, map[int]encx.DictRemap{1: {1: 2}}, errors.PULSE_SHARD_HEADER_INVALID},
		{"u16 index overflow", shard, s, map[int]encx.DictRemap{1: {1: 0x10000}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{"non-dictionary field", shard, s, map[int]encx.DictRemap{0: {1: 2}}, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"set_u8 bit overflow", shard, s, map[int]encx.DictRemap{3: {1: 8}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{"set_u16 bit overflow", shard, s, map[int]encx.DictRemap{4: {1: 16}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{"set_u32 bit overflow", shard, s, map[int]encx.DictRemap{5: {1: 32}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{"set_u64 bit overflow", shard, s, map[int]encx.DictRemap{6: {1: 64}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
		{"set_u128 bit overflow", shard, s, map[int]encx.DictRemap{7: {1: 128}}, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW},
	}
	for _, tc := range cases {
		out, err := encx.RewriteShardCategoricals(tc.shard, tc.target, tc.remap)
		if out != nil || !errors.HasCode(err, tc.code) {
			t.Errorf("%s: = (%d bytes, %v), want %s", tc.name, len(out), err, tc.code)
		}
	}
}
