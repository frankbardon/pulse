//go:build heavy

package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

// TestMaxGroups_ParallelDecode: the per-segment reducer (single file
// above the parallel-decode threshold) trips on the partition count it
// shares with the shard reducer, with no partial result; the serial
// arm trips identically.
func TestMaxGroups_ParallelDecode(t *testing.T) {
	t.Parallel()

	const rowCount = parallelDecodeRecordThreshold + 4096
	d := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, Dictionary: d},
	}}
	recs := make([][]uint64, rowCount)
	for i := range recs {
		recs[i] = []uint64{uint64(i), uint64(i % 4)}
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/decode_groups.pulse"
	if err := afero.WriteFile(osFs, path, writePulseFile(t, schema, recs), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	req := countryCount(path)
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("request is not mergeable; the parallel decode reducer would never engage")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	for _, workers := range []int{1, 4} {
		svc := New(cfg)
		svc.SetDecodeWorkers(workers)
		svc.SetLimits(withMaxGroups(4))
		if resp, err := svc.Process(context.Background(), req); err != nil || len(resp.Data) != 4 {
			t.Fatalf("DecodeWorkers=%d at the limit: %v, %v", workers, resp, err)
		}
		svc.SetLimits(withMaxGroups(3))
		resp, err := svc.Process(context.Background(), req)
		if resp != nil {
			t.Fatalf("DecodeWorkers=%d: partial result %+v", workers, resp)
		}
		requireMaxGroupsExceeded(t, err, 3)
	}
}
