package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
)

// The three read-past sites below read a cohort's (or a shard's) own
// schema block only to reach the first record byte: the caller already
// holds the schema that decodes the rows (lookup) or the canonical
// schema (shard admin, filter-to-file via skipShardPreamble). Each reads that block
// LAYOUT-ONLY (encx.ReadSchemaLayout), so a 100,000-entry dictionary is
// read past through a fixed buffer instead of being allocated (~16 MB a
// decode). Each pin fails if its site goes back to encoding.ReadSchema.

const layoutPinEntries = 100_000

// dictCohortBytes is a single-file cohort over
// dictArchiveSchema(layoutPinEntries) with rows records.
func dictCohortBytes(t *testing.T, rows int) ([]byte, *encoding.Schema) {
	t.Helper()
	schema := dictArchiveSchema(layoutPinEntries)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, rows*schema.RecordByteSize()))
	return buf.Bytes(), schema
}

// dictDecodeBytes is what one full ReadSchema of dictArchiveSchema
// allocates — the per-site cost a regression would add back.
func dictDecodeBytes(t *testing.T, schema *encoding.Schema) uint64 {
	t.Helper()
	var block bytes.Buffer
	if err := encoding.WriteSchema(&block, schema); err != nil {
		t.Fatal(err)
	}
	decode := totalAlloc(t, func() error {
		_, err := encoding.ReadSchema(bytes.NewReader(block.Bytes()), encoding.FormatVersion)
		return err
	})
	if decode < 4<<20 {
		t.Fatalf("a full dictionary decode allocates only %d bytes; the fixture is too small to pin the drop", decode)
	}
	return decode
}

// TestOpenRecordLocator_SkipsDictionaries: a point lookup opens a fresh
// handle per call to measure where the record region starts; it must
// not decode the cohort's dictionaries to do so.
func TestOpenRecordLocator_SkipsDictionaries(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	data, schema := dictCohortBytes(t, 16)
	decode := dictDecodeBytes(t, schema)
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "c.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	var loc *encoding.RecordLocator
	got := totalAlloc(t, func() error {
		f, l, err := openRecordLocator(mem, "c.pulse", schema, uint64(len(data)))
		if err != nil {
			return err
		}
		loc = l
		return f.Close()
	})
	t.Logf("openRecordLocator allocates %d bytes; a full decode %d", got, decode)
	if got > 64<<10 {
		t.Fatalf("openRecordLocator allocates %d bytes (a full decode: %d), want <= 64 KiB — it must not decode the dictionaries", got, decode)
	}
	if want := int64(len(data) - 16*schema.RecordByteSize()); loc.RecordRegionStart != want || loc.TotalRecords != 16 {
		t.Fatalf("locator = start %d / %d records, want %d / 16", loc.RecordRegionStart, loc.TotalRecords, want)
	}
}

// TestRecordCountFromBytes_SkipsDictionaries: shard create / add /
// remove / compact count every shard's records; the shard's own schema
// block is only read past (the canonical schema gives the stride).
func TestRecordCountFromBytes_SkipsDictionaries(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	data, schema := dictCohortBytes(t, 16)
	decode := dictDecodeBytes(t, schema)
	var n int64
	got := totalAlloc(t, func() error {
		var err error
		n, err = recordCountFromBytes(data, schema)
		return err
	})
	t.Logf("recordCountFromBytes allocates %d bytes; a full decode %d", got, decode)
	if got > 64<<10 {
		t.Fatalf("recordCountFromBytes allocates %d bytes (a full decode: %d), want <= 64 KiB — it must not decode the dictionaries", got, decode)
	}
	if n != 16 {
		t.Fatalf("recordCountFromBytes = %d, want 16", n)
	}
}

// TestSkipShardPreamble_SkipsDictionaries: filtering a shard archive
// copies each shard's header + schema bytes verbatim and needs only the
// block's end offset (skipShardPreamble), so no shard schema is decoded
// with its dictionary. The end offset must equal a full decode's.
func TestSkipShardPreamble_SkipsDictionaries(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	data, schema := dictCohortBytes(t, 16)
	decode := dictDecodeBytes(t, schema)
	var end int64
	got := totalAlloc(t, func() error {
		br := bytes.NewReader(data)
		if err := skipShardPreamble(br, "s00.pulse"); err != nil {
			return err
		}
		end = int64(len(data) - br.Len())
		return nil
	})
	t.Logf("skipShardPreamble allocates %d bytes; a full decode %d", got, decode)
	if got > 64<<10 {
		t.Fatalf("skipShardPreamble allocates %d bytes (a full decode: %d), want <= 64 KiB — it must not decode the dictionaries", got, decode)
	}
	if want := int64(len(data) - 16*schema.RecordByteSize()); end != want {
		t.Fatalf("skipShardPreamble ends at %d, want %d", end, want)
	}
}

// TestFilterToFile_ShardArchiveLayoutOnlyRoundTrip: the layout-only
// preamble skip keeps the filter's output a valid archive whose shards
// carry exactly the kept rows behind their verbatim header + schema.
func TestFilterToFile_ShardArchiveLayoutOnlyRoundTrip(t *testing.T) {
	t.Parallel()

	const shards = 4
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "dict.pulse", shards, 64, 1_000)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	written, err := New(cfg).FilterToFile(context.Background(), "dict.pulse", "out.pulse", "x0 >= 0")
	if err != nil {
		t.Fatal(err)
	}
	if written <= 0 || written >= shards*64 {
		t.Fatalf("filter wrote %d records, want a strict subset of %d", written, shards*64)
	}
	out, err := afero.ReadFile(mem, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	arch, err := encx.OpenArchive(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for i := range shards {
		n, err := arch.PeekShardRecordCount(fmt.Sprintf("s%02d.pulse", i))
		if err != nil {
			t.Fatal(err)
		}
		total += n
	}
	if total != written {
		t.Fatalf("output shards hold %d records, filter reported %d", total, written)
	}
}
