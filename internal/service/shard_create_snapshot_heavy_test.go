//go:build heavy

package service

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// snapshotBigEntries is the size of the large categorical dictionary the
// first source carries: big enough that one schema snapshot of it is
// hundreds of KB, so N copies of it dwarf every other plan cost.
const snapshotBigEntries = 5000

func snapshotBigValue(i int) string { return fmt.Sprintf("value-%05d", i) }

// writeSnapshotSource writes a one-record .pulse with a categorical_u16
// "big" column and a categorical_u8 "small" column, each record holding
// dictionary index 0.
func writeSnapshotSource(t *testing.T, fsys afero.Fs, path string, big, small []string) {
	t.Helper()
	dict := func(vals []string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "big", Type: encoding.FieldTypeCategoricalU16, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dict(big)},
		{Name: "small", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 2, CsvColumnIdx: 1, Dictionary: dict(small)},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write([]byte{0, 0, 0})
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestShardCreate_PlanSnapshotsDoNotScaleWithSources is the #495 gate:
// archive create keeps each dictionary-remapping source's merge result
// alive in that source's replay plan, so the plan's retained schema
// snapshots must cost O(distinct schemas x grown dictionary), never
// O(sources x every dictionary). Source 0 carries a 5000-entry "big"
// dictionary; N more sources follow, and the live heap (probed after a
// full GC at every source open and payload write, liveHeapProbe) is
// compared at N=20 and N=200.
//
//   - reordered: every later source holds two of big's own values in
//     another order — a remap, but the union adds nothing. Each merge
//     used to deep-copy the whole canonical schema anyway, so 200
//     sources held 200 copies of the 5000-entry dictionary.
//   - extending: every later source adds one new "small" value — 200
//     genuinely distinct schemas. Each used to deep-copy big too; now
//     only the grown small dictionary is new and big is shared.
func TestShardCreate_PlanSnapshotsDoNotScaleWithSources(t *testing.T) {
	// serial: measures process-wide live heap (runtime GC + metrics), which concurrent tests inflate

	ctx := context.Background()
	big := make([]string, snapshotBigEntries)
	for i := range big {
		big[i] = snapshotBigValue(i)
	}
	// One extra source copy of big is ~400 KB live; 180 extra copies is
	// ~70 MB. The grown small dictionaries of the extending arm sum to
	// ~20k tiny entries (~2 MB). 8 MiB separates the two by an order of
	// magnitude either way.
	const growthBound = uint64(8 << 20)

	arms := []struct {
		name  string
		later func(j int) (big, small []string)
	}{
		{"reordered", func(int) ([]string, []string) {
			return []string{snapshotBigValue(snapshotBigEntries - 1), snapshotBigValue(0)}, []string{"s000"}
		}},
		{"extending", func(j int) ([]string, []string) {
			return []string{snapshotBigValue(0)}, []string{fmt.Sprintf("s%03d", j)}
		}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			var got [2]uint64
			for i, n := range []int{20, 200} {
				_, fsys, dir := streamOsEnv(t)
				paths := make([]string, n+1)
				paths[0] = filepath.Join(dir, "s000.pulse")
				writeSnapshotSource(t, fsys, paths[0], big, []string{"s000"})
				for j := 1; j <= n; j++ {
					paths[j] = filepath.Join(dir, fmt.Sprintf("s%03d.pulse", j))
					b, s := arm.later(j)
					writeSnapshotSource(t, fsys, paths[j], b, s)
				}
				archive := filepath.Join(dir, "arch.pulse")
				got[i], _ = liveHeapPeakDuring(t, fsys, func(svc *Service) error {
					_, err := svc.CreateShardArchive(ctx, archive, paths)
					return err
				})

				// The archive is still the union: big unchanged, small
				// grown by one entry per extending source.
				svc, _, _ := streamOsEnv(t)
				c, err := svc.Open(ctx, archive)
				if err != nil {
					t.Fatal(err)
				}
				if d := c.Schema().Fields[0].Dictionary.Count(); d != snapshotBigEntries {
					t.Fatalf("canonical big dict = %d entries, want %d", d, snapshotBigEntries)
				}
				wantSmall := 1
				if arm.name == "extending" {
					wantSmall = n + 1
				}
				if d := c.Schema().Fields[1].Dictionary.Count(); d != wantSmall {
					t.Fatalf("canonical small dict = %d entries, want %d", d, wantSmall)
				}
				total, err := svc.CountRecords(ctx, archive)
				if err != nil || total != uint64(n+1) {
					t.Fatalf("archive records = %d (%v), want %d", total, err, n+1)
				}
			}
			t.Logf("%s create: 20 sources -> peak %d B, 200 sources -> peak %d B", arm.name, got[0], got[1])
			if got[1] > got[0]+growthBound {
				t.Errorf("create plan grows with the sources: peak %d B at 20, %d B at 200 (bound +%d B)",
					got[0], got[1], growthBound)
			}
		})
	}
}
