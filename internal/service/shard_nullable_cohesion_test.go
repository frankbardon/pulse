package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// nullableCohesionSchema is id u32 + score f64 + flag u8 with the
// caller's per-field nullability; score stays nullable in every fixture
// below, so the bitmap width is one byte on both sides and the stride
// check alone cannot tell the schemas apart.
func nullableCohesionSchema(idNullable bool) *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0, Nullable: idNullable},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		{Name: "flag", Type: encoding.FieldTypeU8, ByteOffset: 12, CsvColumnIdx: 2},
	}}
}

// writeSinglePulseNulls is writeSinglePulse plus the trailing null
// bitmap: nulls[r][f] marks field f of record r null. A null's value
// bytes are written as given, so a test can plant a recognisable
// placeholder under a null.
func writeSinglePulseNulls(t *testing.T, schema *encoding.Schema, records [][]uint64, nulls [][]bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for ri, rec := range records {
		for fi, f := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, f.Type, rec[fi]); err != nil {
				t.Fatal(err)
			}
		}
		if schema.HasBitmap() {
			bm := make([]byte, schema.BitmapByteSize())
			for fi, null := range nulls[ri] {
				if null {
					encoding.BitmapSetNull(bm, fi)
				}
			}
			if err := encoding.WriteBitmap(&buf, bm); err != nil {
				t.Fatal(err)
			}
		}
	}
	return buf.Bytes()
}

// nullableShards returns a canonical shard (id NOT nullable) and an
// arriving shard that declares id nullable and stores a null id over the
// placeholder 777.
func nullableShards(t *testing.T) (canonical *encoding.Schema, seed, arriving []byte) {
	t.Helper()
	canonical = nullableCohesionSchema(false)
	seed = writeSinglePulseNulls(t, canonical,
		[][]uint64{{1, math.Float64bits(1.5), 3}}, [][]bool{{false, false, false}})
	arriving = writeSinglePulseNulls(t, nullableCohesionSchema(true),
		[][]uint64{{777, math.Float64bits(2.5), 4}}, [][]bool{{true, false, false}})
	return canonical, seed, arriving
}

func assertNullableMismatch(t *testing.T, where string, err error) {
	t.Helper()
	ce, ok := err.(*errors.CodedError)
	if !ok || ce.Code != errors.PULSE_SHARD_SCHEMA_MISMATCH {
		t.Fatalf("%s: got %v, want PULSE_SHARD_SCHEMA_MISMATCH", where, err)
	}
	if ce.Details["dimension"] != "nullable" || ce.Details["field"] != "id" {
		t.Fatalf("%s: details %v, want dimension nullable on field id", where, ce.Details)
	}
}

// TestShardNullableCohesion_ReaderRefuses: an archive whose stored shard
// declares a field nullable that the canonical schema does not is
// refused at record-reader open — whole archive and anchor alike. Before
// #207 the reader admitted it (equal strides) and the shard's null id
// decoded as the placeholder 777.
func TestShardNullableCohesion_ReaderRefuses(t *testing.T) {
	canonical, seed, arriving := nullableShards(t)
	data := synthesizeArchiveWithRawPayload(t, canonical,
		[]rawShard{{Name: "a.pulse", Payload: seed}, {Name: "x.pulse", Payload: arriving}})
	svc, _ := gEnv(t, map[string][]byte{"arch.pulse": data})
	for _, path := range []string{"arch.pulse", "arch.pulse#x.pulse"} {
		c, err := svc.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := c.OpenRecordReader()
		if err == nil {
			rows := readAllRows(t, r)
			_ = r.Close()
			t.Fatalf("%s: shard admitted; rows decoded as %v (the stored null id reads as a value)", path, rows)
		}
		assertNullableMismatch(t, path, err)
	}
}

// TestShardNullableCohesion_VerifyRefuses: shard verify reports the
// same refusal against the offending shard.
func TestShardNullableCohesion_VerifyRefuses(t *testing.T) {
	canonical, seed, arriving := nullableShards(t)
	data := synthesizeArchiveWithRawPayload(t, canonical,
		[]rawShard{{Name: "a.pulse", Payload: seed}, {Name: "x.pulse", Payload: arriving}})
	svc, _ := gEnv(t, map[string][]byte{"arch.pulse": data})
	res, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 1 {
		t.Fatalf("verify errors = %v, want exactly the nullable mismatch", res.Errors)
	}
	assertNullableMismatch(t, "verify", res.Errors[0])
	if res.Errors[0].Details["entry"] != "x.pulse" {
		t.Fatalf("verify names entry %v, want x.pulse", res.Errors[0].Details["entry"])
	}
}

// TestShardNullableCohesion_AddShardRefuses: AddShard refuses the
// nullable arrival and leaves the archive byte-identical.
func TestShardNullableCohesion_AddShardRefuses(t *testing.T) {
	_, seed, arriving := nullableShards(t)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": seed, "x.pulse": arriving})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AddShard(ctx, "arch.pulse", "x.pulse")
	assertNullableMismatch(t, "AddShard", err)
	after, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("a refused AddShard rewrote the archive")
	}
	// CreateShardArchive runs the same merge: seeding with both shards
	// in this order refuses identically.
	_, err = svc.CreateShardArchive(ctx, "arch2.pulse", []string{"a.pulse", "x.pulse"})
	assertNullableMismatch(t, "CreateShardArchive", err)
}

// TestShardNullableCohesion_ReverseDirection: a nullable archive field
// admits a non-nullable shard field while the bitmaps agree in width,
// and the shard's rows read back exactly (non-null) under the canonical
// flags; once the shard's bitmap disappears the strides disagree and it
// is refused.
func TestShardNullableCohesion_ReverseDirection(t *testing.T) {
	ctx := context.Background()
	t.Run("bitmap width equal is admitted", func(t *testing.T) {
		nullableArch := writeSinglePulseNulls(t, nullableCohesionSchema(true),
			[][]uint64{{777, math.Float64bits(1.5), 3}}, [][]bool{{true, false, false}})
		nonNull := writeSinglePulseNulls(t, nullableCohesionSchema(false),
			[][]uint64{{9, math.Float64bits(2.5), 4}}, [][]bool{{false, true, false}})
		svc, _ := gEnv(t, map[string][]byte{"a.pulse": nullableArch, "b.pulse": nonNull})
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
			t.Fatalf("AddShard: %v", err)
		}
		res, err := svc.VerifyShardArchive(ctx, "arch.pulse")
		if err != nil || len(res.Errors) != 0 {
			t.Fatalf("verify = %v / %v, want clean", res, err)
		}
		rows := readAllRows(t, openRecordReader(t, svc, "arch.pulse"))
		if len(rows) != 2 || rows[0][0] != nil || rows[1][0] == nil || rows[1][1] != nil {
			t.Fatalf("rows = %v, want [null id, 1.5, 3] then [9, null score, 4]", rows)
		}
	})
	t.Run("bitmap dropped is refused", func(t *testing.T) {
		archSchema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, Nullable: true},
			{Name: "flag", Type: encoding.FieldTypeU8, ByteOffset: 4, CsvColumnIdx: 1},
		}}
		shardSchema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
			{Name: "flag", Type: encoding.FieldTypeU8, ByteOffset: 4, CsvColumnIdx: 1},
		}}
		a := writeSinglePulseNulls(t, archSchema, [][]uint64{{1, 2}}, [][]bool{{false, false}})
		b := writeSinglePulseNulls(t, shardSchema, [][]uint64{{3, 4}}, nil)
		svc, _ := gEnv(t, map[string][]byte{"a.pulse": a, "b.pulse": b})
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
			t.Fatal(err)
		}
		_, err := svc.AddShard(ctx, "arch.pulse", "b.pulse")
		assertNullableMismatch(t, "AddShard", err)
	})
}
