//go:build heavy

package service

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMatrixRankCorrelation_SerialUnderParallelKnobs: a rank method is
// not mergeable, so the merge gate refuses the request and DecodeWorkers
// / ShardWorkers do not fan it out: the result is the serial one bit
// for bit, on a single file above the parallel-decode threshold and on
// a shard archive of the same rows.
func TestMatrixRankCorrelation_SerialUnderParallelKnobs(t *testing.T) {
	schema := coMomentSchema()
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	n := parallelDecodeRecordThreshold + 1234
	recs, nullAt := coMomentRows(n, 0)
	single := filepath.Join(dir, "single.pulse")
	if err := afero.WriteFile(osFs, single, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	archive := filepath.Join(dir, "archive.pulse")
	if err := afero.WriteFile(osFs, archive, coMomentArchive(t, schema, []int{40_000, 1_234, 60_000}), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	run := func(path, method, missing string, decode, shard int) []uint64 {
		t.Helper()
		spec := types.MatrixSpec{Name: "r", Type: types.MAT_CORRELATION, Fields: []string{"x1", "x2", "x3"},
			Params: json.RawMessage(`{"method": "` + method + `", "missing": "` + missing + `"}`)}
		req := &types.Request{Cohort: &types.Cohort{Filename: path}, Matrices: []types.MatrixSpec{spec}}
		if processing.CanMergeRequest(req, schema) {
			t.Fatalf("%s: the merge gate admits a rank-method request", method)
		}
		svc := New(cfg)
		svc.SetDecodeWorkers(decode)
		svc.SetShardWorkers(shard)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process(decode=%d, shard=%d): %v", decode, shard, err)
		}
		var words []uint64
		for _, row := range resp.Matrices[0].Primary.Values {
			for _, v := range row {
				words = append(words, math.Float64bits(v))
			}
		}
		return words
	}
	for _, method := range []string{"spearman", "kendall"} {
		for _, missing := range []string{"listwise", "pairwise"} {
			label := method + "/" + missing
			serial := run(single, method, missing, 1, 0)
			if got := run(single, method, missing, 8, 0); firstWordDiff(got, serial) != -1 {
				t.Errorf("%s: DecodeWorkers=8 differs from serial", label)
			}
			archSerial := run(archive, method, missing, 0, 1)
			if got := run(archive, method, missing, 0, 4); firstWordDiff(got, archSerial) != -1 {
				t.Errorf("%s: ShardWorkers=4 differs from serial", label)
			}
		}
	}
}
