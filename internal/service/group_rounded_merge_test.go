package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// TestGroupRounded_DistanceBetweenMerges (U51 E3-S3): a MAT_DISTANCE
// whose params.between is GROUP_ROUNDED is mergeable now that the
// grouper is (types.MatrixSpec.Mergeable), so it fans out on both
// reducers — the vehicle aggregator's instance count proves the arm —
// and returns the serial run's bits. Its buckets are GROUP_RANGE's at
// the same width (both floor to the interval and emit in numeric key
// order), so the matrix also equals the GROUP_RANGE-between twin.
func TestGroupRounded_DistanceBetweenMerges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100K-record distance merge gate in -short mode")
	}
	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	v := matrixInvarianceVariant{withVehicle: true, typ: types.MAT_DISTANCE}
	withBetween := func(path string, between types.GroupType) *types.Request {
		req := matrixInvarianceRequest(path, v)
		req.Matrices[0].Params = json.RawMessage(strings.Replace(string(req.Matrices[0].Params),
			string(types.GROUP_RANGE), string(between), 1))
		if !strings.Contains(string(req.Matrices[0].Params), string(between)) {
			t.Fatalf("between not set: %s", req.Matrices[0].Params)
		}
		return req
	}

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	n := 25*B + 2777
	recs, nullAt := coMomentRows(n, 0)
	single := dir + "/single.pulse"
	if err := afero.WriteFile(osFs, single, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	shardRows := []int{3 * B, 5000, B, 123, 9000, 2*B + 1, 777, 6000}
	archive := dir + "/archive.pulse"
	if err := afero.WriteFile(osFs, archive, coMomentArchive(t, schema, shardRows), 0o644); err != nil {
		t.Fatal(err)
	}

	serial := runMatrixInvariance(t, cfg, withBetween(single, types.GROUP_ROUNDED), 1, 1)
	twin := runMatrixInvariance(t, cfg, withBetween(single, types.GROUP_RANGE), 1, 1)
	if i := firstWordDiff(serial.words, twin.words); i != -1 || len(serial.words) < 5 {
		t.Fatalf("GROUP_ROUNDED between differs from its GROUP_RANGE twin at word %d (%d words)", i, len(serial.words))
	}
	for _, w := range []int{2, 3, 7} {
		got := runMatrixInvariance(t, cfg, withBetween(single, types.GROUP_ROUNDED), w, 1)
		if got.instances != int64(w) {
			t.Fatalf("DecodeWorkers=%d fed %d instances — the parallel reducer did not engage", w, got.instances)
		}
		assertMatrixRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", w), got, serial)
	}
	archSerial := runMatrixInvariance(t, cfg, withBetween(archive, types.GROUP_ROUNDED), 1, 1)
	for _, w := range []int{2, 3, 8} {
		got := runMatrixInvariance(t, cfg, withBetween(archive, types.GROUP_ROUNDED), 1, w)
		if got.instances != int64(len(shardRows)) {
			t.Fatalf("ShardWorkers=%d fed %d instances — the shard reducer did not engage", w, got.instances)
		}
		assertMatrixRunsEqual(t, fmt.Sprintf("ShardWorkers=%d", w), got, archSerial)
	}
}
