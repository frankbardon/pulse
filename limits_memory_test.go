package pulse_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// memoryArchive imports two numeric cohorts of 4,097 and 5 records and
// packs them into a shard archive: three merge blocks (numbering
// restarts per shard) where one file of the same 4,102 rows holds two.
func memoryArchive(t *testing.T, fs afero.Fs) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var paths []string
	for i, n := range []int{linalg.MergeBlockSize + 1, 5} {
		var b strings.Builder
		b.WriteString("a,b\n")
		for r := range n {
			fmt.Fprintf(&b, "%d,%d\n", 50+r%47, 20+(r*7)%13)
		}
		name := fmt.Sprintf("m%d.csv", i)
		if err := afero.WriteFile(fs, name, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name})
		if err != nil {
			t.Fatalf("ImportFile: %v", err)
		}
		paths = append(paths, res.Path)
	}
	if _, err := p.CreateShardArchive(ctx, "arch.pulse", paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return "arch.pulse"
}

// TestLimits_MaxEstimatedMemory_PredictCertainAndProcessRefuses: on
// every arm — streaming, buffered, fused and buffered crosstab, a
// matrix over a single file and over a shard archive, the joined
// Process and the joined crosstab — predict reports the memory
// estimate as a CERTAIN finding under a MaxEstimatedMemory below it
// (Valid false on Predict, PredictBytes and MCP pulse_predict) and the
// runtime refuses with the identical error before scanning (the
// before-decode read assertions are internal/service
// TestLimitsPreflight_MaxEstimatedMemoryRefusesBeforeDecode). At the
// estimate both run; under the default (Unlimited) there is no finding.
func TestLimits_MaxEstimatedMemory_PredictCertainAndProcessRefuses(t *testing.T) {
	ctx := context.Background()
	fs, cohort := limitsCohort(t)
	jfs, left, right := joinLimitCohorts(t)
	afs := afero.NewMemMapFs()
	arch := memoryArchive(t, afs)

	crosstab := func(c string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: c}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "a", Label: "s"},
		}}
	}
	cases := []struct {
		name     string
		fs       afero.Fs
		path     string
		req      func() *types.Request
		noFusion bool
	}{
		{name: "streaming", fs: fs, path: cohort, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}
		}},
		{name: "buffered", fs: fs, path: cohort, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "a", Label: "m"}}}
		}},
		{name: "fused crosstab", fs: fs, path: cohort, req: func() *types.Request { return crosstab(cohort) }},
		{name: "buffered crosstab", fs: fs, path: cohort, req: func() *types.Request { return crosstab(cohort) }, noFusion: true},
		{name: "matrix", fs: fs, path: cohort, req: func() *types.Request { return matrix3(cohort) }},
		{name: "archive matrix", fs: afs, path: arch, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: arch}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b"}}}}
		}},
		{name: "join process", fs: jfs, path: left, req: func() *types.Request { return joinedSum(left, right) }},
		{name: "join crosstab", fs: jfs, path: left, req: func() *types.Request { return joinedCrosstab(left, right) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open := func(max int64) *pulse.Pulse {
				p, err := pulse.New(pulse.Options{FS: c.fs, DisableCrosstabFusion: c.noFusion, Limits: pulse.Limits{MaxEstimatedMemory: max}})
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			// Under the default: no finding, the run succeeds.
			def := open(0)
			res, err := def.Predict(ctx, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if !res.Valid || res.LimitFindings != nil {
				t.Fatalf("defaults: valid=%v findings=%+v", res.Valid, res.LimitFindings)
			}
			if _, err := def.Process(ctx, c.req()); err != nil {
				t.Fatalf("defaults: %v", err)
			}

			// The estimate, read from a limit no request fits.
			probe, err := open(1).Predict(ctx, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if len(probe.LimitFindings) != 1 {
				t.Fatalf("probe findings = %+v", probe.LimitFindings)
			}
			est := probe.LimitFindings[0].Estimated

			p := open(est - 1)
			want := []descriptor.LimitFinding{{Limit: "max_estimated_memory", Configured: est - 1, Estimated: est, Grade: descriptor.LimitGradeCertain}}
			res, err = p.Predict(ctx, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if res.Valid || fmt.Sprint(res.LimitFindings) != fmt.Sprint(want) {
				t.Fatalf("Predict valid=%v findings=%+v, want false %+v", res.Valid, res.LimitFindings, want)
			}
			out, err := mcp.HandlePredict(ctx, p, mcp.PredictIn{Request: *c.req()})
			if err != nil {
				t.Fatal(err)
			}
			if out.Valid || fmt.Sprint(out.LimitFindings) != fmt.Sprint(want) {
				t.Fatalf("pulse_predict valid=%v findings=%+v", out.Valid, out.LimitFindings)
			}
			data, err := afero.ReadFile(c.fs, c.path)
			if err != nil {
				t.Fatal(err)
			}
			env, err := p.PredictBytes(ctx, data, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if pr := env.Data.(*descriptor.PredictResult); pr.Valid || fmt.Sprint(pr.LimitFindings) != fmt.Sprint(want) {
				t.Fatalf("PredictBytes valid=%v findings=%+v", pr.Valid, pr.LimitFindings)
			}

			_, perr := p.Process(ctx, c.req())
			d := requireLimitExceeded(t, perr, "max_estimated_memory", est-1, "Options.Limits.MaxEstimatedMemory")
			if d["observed"] != est {
				t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], est)
			}
			sameEntry(t, env, perr)

			at := open(est)
			if res, err := at.Predict(ctx, c.req()); err != nil || !res.Valid || res.LimitFindings != nil {
				t.Fatalf("at the estimate: valid=%v findings=%+v err=%v", res.Valid, res.LimitFindings, err)
			}
			if _, err := at.Process(ctx, c.req()); err != nil {
				t.Fatalf("at the estimate: %v", err)
			}
		})
	}
}

// TestPredict_MatrixEstimatedBytes_ArchiveBlocks: predict's
// estimated_bytes counts merge blocks per shard — 4,097 + 5 sharded
// rows hold three blocks.
func TestPredict_MatrixEstimatedBytes_ArchiveBlocks(t *testing.T) {
	fs := afero.NewMemMapFs()
	arch := memoryArchive(t, fs)
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Predict(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: arch},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matrices) != 1 || res.Matrices[0].EstimatedBytes == nil {
		t.Fatalf("matrices = %+v", res.Matrices)
	}
	m := res.Matrices[0]
	if *m.EstimatedBytes != 3*m.AccumulatorBytes {
		t.Fatalf("estimated_bytes = %d, want 3 blocks x %d", *m.EstimatedBytes, m.AccumulatorBytes)
	}
}
