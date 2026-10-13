package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// offsetTrustShard writes a single-file .pulse whose schema block
// carries the caller's STORED offsets verbatim while the record bytes
// are laid out the way every writer lays them out — a running cursor
// over Schema.FieldWidth, the layout the decoder walks. The two disagree
// whenever the stored offsets are not canonical.
func offsetTrustShard(t *testing.T, fsys afero.Fs, path string, fields []encoding.Field, dict []string, rows [][]uint64) {
	t.Helper()
	schema := &encoding.Schema{Fields: append([]encoding.Field(nil), fields...)}
	for i := range schema.Fields {
		if schema.Fields[i].Type.HasDictionary() {
			d := encoding.NewDictionary()
			for _, v := range dict {
				if _, err := d.Add(v); err != nil {
					t.Fatalf("dict add: %v", err)
				}
			}
			schema.Fields[i].Dictionary = d
		}
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, row := range rows {
		for fi := range schema.Fields {
			var cell [8]byte
			binary.LittleEndian.PutUint64(cell[:], row[fi])
			buf.Write(cell[:schema.FieldWidth(fi)])
		}
	}
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// TestShardArchiveAdd_DictRewriteIgnoresStoredOffsets pins that the
// shard dictionary rewrite addresses a categorical's bytes where the
// DECODER finds them (a type-derived cursor), not at the stored
// Field.ByteOffset. Both shards carry the same non-canonical stored
// offsets, so strict cohesion admits them; the arriving shard's
// dictionary diverges, so `shard add` rewrites its categorical indices.
// Trusting the stored offset wrote the remapped index over a
// neighbouring field and left the categorical untouched.
func TestShardArchiveAdd_DictRewriteIgnoresStoredOffsets(t *testing.T) {
	cases := []struct {
		name   string
		fields []encoding.Field
		// country is the categorical's field index; rows are in
		// schema order with the categorical holding a dict index.
		country int
		rows    [][]uint64
	}{
		{
			// An embedder ImportJob.Schema / ConvertJob schema that
			// never set ByteOffset: every field stored at 0.
			name: "all_zero_offsets",
			fields: []encoding.Field{
				{Name: "id", Type: encoding.FieldTypeU32},
				{Name: "country", Type: encoding.FieldTypeCategoricalU8},
				{Name: "score", Type: encoding.FieldTypeU16},
			},
			country: 1,
			rows: [][]uint64{
				// id's low byte is 1 — a remap key — so a rewrite
				// aimed at stored offset 0 visibly corrupts id.
				{0x0A0B0C01, 1, 0xBEEF},
				{0x0A0B0C0D, 0, 0x1234},
			},
		},
		{
			// A schema-file cohort written before the canonical layout:
			// offsets summed ByteSize(), which is 0 for packed_bool, so
			// the categorical after it was stored one byte short.
			name: "bit_packed_before_categorical",
			fields: []encoding.Field{
				{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 0},
				{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0},
				{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 1},
			},
			country: 1,
			rows: [][]uint64{
				{1, 1, 0x01020304},
				{0, 0, 0x0A0B0C0D},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fs.NewMemMap()
			fsys := cfg.Fs()
			svc := New(cfg)

			// Archive dict: [US, CA]. Arriving dict: [US, MX] — MX is
			// index 1 in the shard and index 2 in the union, so every
			// row holding 1 must be rewritten to 2.
			offsetTrustShard(t, fsys, "a.pulse", tc.fields, []string{"US", "CA"}, tc.rows)
			offsetTrustShard(t, fsys, "b.pulse", tc.fields, []string{"US", "MX"}, tc.rows)
			if _, err := svc.CreateShardArchive(context.Background(), "arch.pulse", []string{"a.pulse"}); err != nil {
				t.Fatalf("CreateShardArchive: %v", err)
			}
			if _, err := svc.AddShard(context.Background(), "arch.pulse", "b.pulse"); err != nil {
				t.Fatalf("AddShard: %v", err)
			}

			rc, err := svc.ExtractShard(context.Background(), "arch.pulse", "b.pulse")
			if err != nil {
				t.Fatalf("ExtractShard: %v", err)
			}
			defer rc.Close()
			raw, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			r := bytes.NewReader(raw)
			ver, err := encoding.ReadHeader(r)
			if err != nil {
				t.Fatalf("ReadHeader: %v", err)
			}
			schema, err := encoding.ReadSchema(r, ver)
			if err != nil {
				t.Fatalf("ReadSchema: %v", err)
			}
			if got := schema.Fields[tc.country].Dictionary.Values(); len(got) != 3 || got[2] != "MX" {
				t.Fatalf("rewritten dict = %v, want [US CA MX]", got)
			}

			rr := encx.NewRecordReader(r, schema)
			for ri, row := range tc.rows {
				vals := map[string]float64{}
				nulls := map[string]bool{}
				if err := rr.ReadRecord(vals, nulls); err != nil {
					t.Fatalf("ReadRecord %d: %v", ri, err)
				}
				for fi, f := range tc.fields {
					want := float64(row[fi])
					if fi == tc.country && row[fi] == 1 {
						want = 2 // MX: shard index 1 -> union index 2
					}
					if got := vals[f.Name]; got != want {
						t.Errorf("row %d field %q = %v, want %v", ri, f.Name, got, want)
					}
				}
			}
		})
	}
}
