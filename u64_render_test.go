package pulse_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// u64_render_test.go pins E4-S4: a u64 cell rendered as a row (Lookup,
// Sample) is the EXACT stored word — Go uint64, JSON the exact integer —
// on a single file, a shard archive and an anchored shard. Ids straddle
// 2^53 (2^53-1, 2^53+1) and reach MaxUint64; every one is distinct.

var u64RenderIDs = []uint64{1<<53 - 1, 1<<53 + 1, math.MaxUint64, math.MaxUint64 - 1, 7, 1 << 53}

func writeU64RenderCohort(t *testing.T, fsys afero.Fs, path string, ids []uint64) {
	t.Helper()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU64, ByteOffset: 0},
		{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 8},
	}}); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		buf = binary.LittleEndian.AppendUint64(buf, id)
		buf = binary.LittleEndian.AppendUint64(buf, math.Float64bits(float64(i)+0.5))
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertExactU64Cell checks one rendered cell: Go uint64 == want, and
// its JSON spelling is want's decimal digits.
func assertExactU64Cell(t *testing.T, where string, cell any, want uint64) {
	t.Helper()
	got, ok := cell.(uint64)
	if !ok || got != want {
		t.Errorf("%s: id = %T %v, want uint64 %d", where, cell, cell, want)
	}
	raw, err := json.Marshal(map[string]any{"id": cell})
	if err != nil {
		t.Fatal(err)
	}
	if wantRaw := `{"id":` + strconv.FormatUint(want, 10) + `}`; string(raw) != wantRaw {
		t.Errorf("%s: json = %s, want %s", where, raw, wantRaw)
	}
}

func TestArchiveLookup_U64RowsExact(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeU64RenderCohort(t, fsys, "single.pulse", u64RenderIDs)
	writeU64RenderCohort(t, fsys, "s0.pulse", u64RenderIDs[:3])
	writeU64RenderCohort(t, fsys, "s1.pulse", u64RenderIDs[3:])
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateShardArchive(ctx, "archive.pulse", []string{"s0.pulse", "s1.pulse"}); err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []string{"single.pulse", "archive.pulse"} {
		if _, err := p.BuildIndex(ctx, cohort, []string{"id"}); err != nil {
			t.Fatalf("BuildIndex(%s): %v", cohort, err)
		}
		for _, id := range u64RenderIDs {
			res, err := p.Lookup(ctx, &pulse.LookupRequest{Cohort: &types.Cohort{Filename: cohort}, Field: "id", Value: strconv.FormatUint(id, 10)})
			if err != nil {
				t.Fatalf("Lookup(%s, %d): %v", cohort, id, err)
			}
			if len(res.Rows) != 1 {
				t.Fatalf("Lookup(%s, %d) rows = %d, want 1", cohort, id, len(res.Rows))
			}
			assertExactU64Cell(t, cohort+" lookup", res.Rows[0]["id"], id)
		}
	}
	for _, c := range []struct {
		cohort string
		ids    []uint64
	}{{"single.pulse", u64RenderIDs}, {"archive.pulse", u64RenderIDs}, {"archive.pulse#s1.pulse", u64RenderIDs[3:]}} {
		rows, err := p.Sample(ctx, c.cohort, len(c.ids))
		if err != nil {
			t.Fatalf("Sample(%s): %v", c.cohort, err)
		}
		if len(rows) != len(c.ids) {
			t.Fatalf("Sample(%s) rows = %d, want %d", c.cohort, len(rows), len(c.ids))
		}
		for i, id := range c.ids {
			assertExactU64Cell(t, c.cohort+" sample", rows[i]["id"], id)
		}
	}
	// The wire through the envelope: exact digits survive MarshalFinite.
	res, err := p.Lookup(ctx, &pulse.LookupRequest{Cohort: &types.Cohort{Filename: "archive.pulse"}, Field: "id", Value: strconv.FormatUint(math.MaxUint64, 10)})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := types.MarshalFinite(res)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"id":18446744073709551615`)) {
		t.Errorf("MarshalFinite(lookup) = %s, want the exact MaxUint64", raw)
	}
}
