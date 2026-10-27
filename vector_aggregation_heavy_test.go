//go:build heavy

package pulse_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestVecAgg_BitExactAcrossWorkers: a single file answers the same bits
// serially and under every DecodeWorkers count, and a shard archive
// holding the same rows — split unevenly, with shard edges inside merge
// blocks, 1-row shards and an empty shard — answers THOSE bits under
// every ShardWorkers × DecodeWorkers count: blocks key by the global
// record ordinal, so the twin and the archive fold the same blocks in
// the same tree. Grouped and ungrouped; weighted and unweighted vector
// aggregates (AGG_VEC_SD's Chan-combined moments included) and
// covariance matrices.
func TestVecAgg_BitExactAcrossWorkers(t *testing.T) {
	t.Parallel()

	const rows = 5*4096 + 1234
	fsys := afero.NewMemMapFs()
	writeVaggBitsCohort(t, fsys, "single.pulse", 0, rows)
	bounds := []int{0, 1, 2, 4097, 4097, 9000, 9001, 14000, 14000 + 4096, rows}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("s%d.pulse", i)
		writeVaggBitsCohort(t, fsys, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	builder, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	for _, grouped := range []bool{false, true} {
		t.Run(fmt.Sprintf("grouped=%v", grouped), func(t *testing.T) {
			serial, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			want := vaggBits(t, serial, "single.pulse", grouped)
			for _, dw := range []int{1, 2, 3, 7} {
				p, err := pulse.New(pulse.Options{FS: fsys, DecodeWorkers: dw})
				if err != nil {
					t.Fatal(err)
				}
				if got := vaggBits(t, p, "single.pulse", grouped); !reflect.DeepEqual(got, want) {
					t.Errorf("single file DecodeWorkers=%d: bits differ from serial", dw)
				}
				for _, sw := range []int{1, 2, 3, 4} {
					p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: sw, DecodeWorkers: dw})
					if err != nil {
						t.Fatal(err)
					}
					if got := vaggBits(t, p, "archive.pulse", grouped); !reflect.DeepEqual(got, want) {
						t.Errorf("archive ShardWorkers=%d DecodeWorkers=%d: bits differ from the single-file twin", sw, dw)
					}
				}
			}
		})
	}
}
