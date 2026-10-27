package pulse

import (
	"context"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// TestCohortBuilder_NonNullableShardInNullableArchive is the writer half
// of the #207 reverse-direction rule: an archive field that is nullable
// admits a shard whose field is NOT nullable only because every Pulse
// writer leaves that field's null-bitmap bit 0. The anchored builder
// appends such a shard; the stored shard's bitmap bit for the
// non-nullable field is 0 on every row (while the nullable field's bit
// is set exactly on its nulls, so the bitmap is really written), and the
// archive reads every row back exactly under the canonical flags.
func TestCohortBuilder_NonNullableShardInNullableArchive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := memBuilderEngine(t)
	arch := precheckBase()
	arch[0].nullable = true // id nullable in the archive
	archRows := precheckRows()
	archRows[1][0] = nil
	buildCohort(t, p, "arch.pulse", precheckSchema(arch...), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 3}}, archRows)

	drop := []CohortRow{
		{uint64(40), "a", []string{"x"}, nil},
		{uint64(41), "b", []string{"x"}, 2.5},
		{uint64(42), "a", []string{"x"}, nil},
	}
	buildCohort(t, p, "arch.pulse#drop.pulse", precheckSchema(precheckBase()...), CohortBuilderOptions{}, drop)

	rc, err := p.ExtractShard(ctx, "arch.pulse", "drop.pulse")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	own, _, err := encx.ReadPreamble(rc)
	if err != nil {
		t.Fatal(err)
	}
	if own.Fields[0].Nullable || !own.Fields[3].Nullable {
		t.Fatalf("stored shard flags: id nullable=%v, score nullable=%v; want false, true",
			own.Fields[0].Nullable, own.Fields[3].Nullable)
	}
	payload, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	stride, bm := own.RecordByteSize(), own.BitmapByteSize()
	if bm == 0 || len(payload) != stride*len(drop) {
		t.Fatalf("payload %d bytes, stride %d, bitmap %d, want %d rows with a bitmap", len(payload), stride, bm, len(drop))
	}
	for r := range drop {
		bitmap := payload[r*stride+stride-bm : (r+1)*stride]
		if encoding.BitmapIsNull(bitmap, 0) {
			t.Fatalf("row %d: the non-nullable id's bitmap bit is 1", r)
		}
		if got, want := encoding.BitmapIsNull(bitmap, 3), drop[r][3] == nil; got != want {
			t.Fatalf("row %d: score null bit %v, want %v", r, got, want)
		}
	}

	rd := openReader(t, p, "arch.pulse")
	if rd.Len() != int64(len(archRows)+len(drop)) {
		t.Fatalf("archive Len = %d", rd.Len())
	}
	for i, want := range append(archRows, drop...) {
		got, err := rd.RecordAt(int64(i))
		if err != nil {
			t.Fatal(err)
		}
		if (got[0] == nil) != (want[0] == nil) || (got[3] == nil) != (want[3] == nil) {
			t.Fatalf("record %d = %v, want nulls as in %v", i, got, want)
		}
		if want[0] != nil && got[0] != want[0] {
			t.Fatalf("record %d id = %v, want %v", i, got[0], want[0])
		}
	}
}
