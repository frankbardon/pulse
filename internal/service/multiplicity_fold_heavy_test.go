//go:build heavy

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func TestMultiplicityFold_ParallelDecodeArm(t *testing.T) {
	t.Parallel()

	const rowCount = parallelDecodeRecordThreshold + 4096
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/m.pulse"
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, multSchema(), multRows(rowCount, 0), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	svc.SetDecodeWorkers(4)
	mergeable := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
		}
	}
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, perr := svc.processSingleFileParallelMaybe(context.Background(), mergeable(), cohort, path); perr != nil || !ok {
		t.Fatalf("parallel decode did not engage (ok=%v err=%v)", ok, perr)
	}
	t.Run("test-free parallel identical", func(t *testing.T) {
		assertNoTestArmIdentical(t, svc, mergeable)
	})
	t.Run("tests fall back and are corrected", func(t *testing.T) {
		// At this n every fixture p is ~0; aim the one-sample tests
		// just off the observed mean so the family holds moderate
		// p-values the correction visibly moves.
		avg := mergeable()
		avg.Aggregations = []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "x", Label: "m"}}
		resp, err := svc.Process(context.Background(), avg)
		if err != nil {
			t.Fatal(err)
		}
		mean := resp.Data[0]["m"].(float64)
		assertProcessFold(t, svc, func() *types.Request {
			r := mergeable()
			r.Tests = []*types.Test{
				{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, mean+0.009)), Label: "a"},
				{Type: types.TEST_T, Field: "x", Params: json.RawMessage(fmt.Sprintf(`{"mu":%v}`, mean+0.005)), Label: "b"},
				{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y", Label: "r"},
			}
			return r
		})
	})
}
