//go:build heavy

package service

import (
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMatrixGrouped_ParallelArms_DecodeWorkers is the 100K-record
// DecodeWorkers arm of TestMatrixGrouped_ParallelArms: the decode
// reducer returns the serial per-bucket matrices bit for bit.
func TestMatrixGrouped_ParallelArms_DecodeWorkers(t *testing.T) {
	schema := coMomentSchema()
	B := linalg.MergeBlockSize
	v := matrixInvarianceVariant{weighted: true, withVehicle: true, typ: types.MAT_CORRELATION}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	n := 25*B + 2777
	recs, nullAt := coMomentRows(n, 0)
	path := dir + "/grouped.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := groupedInvarianceRequest(path, v)
	serial := runGroupedInvariance(t, cfg, req, 1, 0)
	for _, workers := range []int{3, 8} {
		got := runGroupedInvariance(t, cfg, req, workers, 0)
		if got.instances <= serial.instances {
			t.Fatalf("DecodeWorkers=%d fed %d vehicle instances, serial %d — the decode reducer did not engage", workers, got.instances, serial.instances)
		}
		assertGroupedRunsEqual(t, fmt.Sprintf("DecodeWorkers=%d", workers), got, serial)
	}
}
