//go:build heavy

package service

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/spf13/afero"
)

func TestDecodeWorkers_WeightedParity(t *testing.T) {
	const rowCount = parallelDecodeRecordThreshold + 4096
	recs, nullAt := weightModesRows(rowCount, 0)
	payload := writeNullablePulse(t, weightModesSchema(), recs, nullAt)
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/weighted.pulse"
	if err := afero.WriteFile(osFs, path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	serial := New(cfg)
	serial.SetDecodeWorkers(1)
	want, err := serial.Process(context.Background(), weightModesRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedUseful(t, want)

	par := New(cfg)
	par.SetDecodeWorkers(4)
	cohort, err := par.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, perr := par.processSingleFileParallelMaybe(context.Background(), processing.StampWeights(weightModesRequest(path), nil), cohort, path); perr != nil || !ok {
		t.Fatalf("parallel decode did not engage (ok=%v err=%v); the test would compare serial to serial", ok, perr)
	}
	got, err := par.Process(context.Background(), weightModesRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedAgree(t, got, want)
}
