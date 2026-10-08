package pulse_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// E2-S1 (PRD FR-5/6/9/10): OperationResult reports which execution arm
// ran, its workers, shards and projection, the rows it scanned /
// matched / emitted and the bytes it read — set by the service's
// decision points through the per-request carrier, independent of
// Response.Components.

// execRecorder captures the OperationResult of every finished
// operation and, in call order, every phase and end hook call.
type execRecorder struct {
	mu   sync.Mutex
	ends []observe.OperationResult
	// log is "<phase>" per OnPhase and "end" per OnOperationEnd.
	log []string
}

func (r *execRecorder) hooks() *observe.Hooks {
	return &observe.Hooks{
		OnOperationEnd: func(_ context.Context, _ observe.OperationInfo, res observe.OperationResult) {
			r.mu.Lock()
			r.ends = append(r.ends, res)
			r.log = append(r.log, "end")
			r.mu.Unlock()
		},
		OnPhase: func(_ context.Context, _ observe.OperationInfo, ph observe.PhaseTiming) {
			r.mu.Lock()
			r.log = append(r.log, string(ph.Phase))
			r.mu.Unlock()
		},
	}
}

// takeLog returns and clears the phase / end call log.
func (r *execRecorder) takeLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.log
	r.log = nil
	return out
}

// last returns the single result recorded since the previous call.
func (r *execRecorder) last(t *testing.T) observe.OperationResult {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.ends) != 1 {
		t.Fatalf("recorded %d operation ends, want 1", len(r.ends))
	}
	res := r.ends[0]
	r.ends = nil
	return res
}

const execCohort = "exec.pulse"

func execSmallFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, execCohort, paritySchema(t), 0, paritySmallRows)
	return fsys
}

func execSumReq(cohort string) *types.Request {
	return &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}
}

func execNew(t *testing.T, opts pulse.Options, rec *execRecorder) *pulse.Pulse {
	t.Helper()
	opts.Hooks = rec.hooks()
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

func fileSize(t *testing.T, fsys afero.Fs, path string) int64 {
	t.Helper()
	fi, err := fsys.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// assertRowsMatchRun: the carrier's row counters equal Components.Run
// (and the result rows), whenever Run is present.
func assertRowsMatchRun(t *testing.T, res observe.OperationResult, resp *types.Response) {
	t.Helper()
	if resp.Components == nil || resp.Components.Run == nil {
		t.Fatal("fixture: no Components.Run to compare against")
	}
	run := resp.Components.Run
	if res.RowsScanned != run.TotalRecords || res.RowsMatched != run.FilteredRecords {
		t.Errorf("rows scanned/matched = %d/%d, Components.Run = %d/%d",
			res.RowsScanned, res.RowsMatched, run.TotalRecords, run.FilteredRecords)
	}
	if res.RowsScanned == 0 {
		t.Error("rows scanned is 0 — the comparison is vacuous")
	}
	if res.RowsOut != int64(len(resp.Data)) {
		t.Errorf("rows out = %d, len(Data) = %d", res.RowsOut, len(resp.Data))
	}
}

// TestObservabilityExecArms: each of the six arms reports its own Arm,
// Workers, Shards and Projected, rows equal to Components.Run, and
// bytes_read > 0.
//
// Falsified per arm by deleting that arm's setPlan stamp (the arm comes
// back empty or as another arm's).
// armCase is one execution arm's fixture: an instance wired to rec and
// a request that rides the arm.
type armCase struct {
	name      string
	open      func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request)
	arm       observe.Arm
	workers   int
	shards    int
	projected bool
}

// execArmCases returns one case per execution arm (plus the crosstab
// variants), shared by the arm and phase tests.
func execArmCases(t *testing.T) []armCase {
	t.Helper()
	ctx := context.Background()
	large := parityLargeCohort(t.TempDir())

	crosstabReq := func(joins []*types.JoinSpec) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: execCohort},
			Joins:  joins,
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "score"},
			},
		}
	}
	selfJoin := []*types.JoinSpec{{Right: execCohort, As: "r_", On: []types.OnPair{{LeftField: "qty", RightField: "qty"}}}}
	return []armCase{
		{"streaming", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			return execNew(t, pulse.Options{FS: execSmallFS(t)}, rec), execSumReq(execCohort)
		}, observe.ArmStreaming, 1, 0, true},
		{"buffered", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			req := execSumReq(execCohort)
			// ATTR_PERCENTILE is non-streamable: it forces the buffered arm.
			req.Attributes = []*types.Attribute{{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"}}
			return execNew(t, pulse.Options{FS: execSmallFS(t)}, rec), req
		}, observe.ArmBuffered, 1, 0, true},
		{"fused_crosstab", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			req := crosstabReq(nil)
			if ok, why := processing.CanFuseCrosstab(req, paritySchema(t), nil); !ok {
				t.Fatalf("fixture: crosstab declined fusion (%s)", why)
			}
			return execNew(t, pulse.Options{FS: execSmallFS(t)}, rec), req
		}, observe.ArmFusedCrosstab, 1, 0, true},
		// The same crosstab with fusion off rides the buffered arm.
		{"crosstab_buffered", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			return execNew(t, pulse.Options{FS: execSmallFS(t), DisableCrosstabFusion: true}, rec), crosstabReq(nil)
		}, observe.ArmBuffered, 1, 0, true},
		{"crosstab_join", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			return execNew(t, pulse.Options{FS: execSmallFS(t)}, rec), crosstabReq(selfJoin)
		}, observe.ArmJoin, 1, 0, false},
		{"join", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			req := execSumReq(execCohort)
			req.Joins = selfJoin
			return execNew(t, pulse.Options{FS: execSmallFS(t)}, rec), req
		}, observe.ArmJoin, 1, 0, false},
		{"shard_parallel", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			fsys := afero.NewMemMapFs()
			s := paritySchema(t)
			shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
			for i, path := range shards {
				writeParityCohort(t, fsys, path, s, i*paritySmallRows/3, (i+1)*paritySmallRows/3)
			}
			builder, err := pulse.New(pulse.Options{FS: fsys})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := builder.CreateShardArchive(ctx, "archive.pulse", shards); err != nil {
				t.Fatalf("CreateShardArchive: %v", err)
			}
			return execNew(t, pulse.Options{FS: fsys, ShardWorkers: 3}, rec), execSumReq("archive.pulse")
		}, observe.ArmShardParallel, 3, 3, false},
		{"parallel_decode", func(t *testing.T, rec *execRecorder) (*pulse.Pulse, *types.Request) {
			return execNew(t, pulse.Options{DataDir: large(t), DecodeWorkers: 4}, rec), execSumReq("large.pulse")
		}, observe.ArmParallelDecode, 4, 0, true},
	}

}

func TestObservabilityExecArms(t *testing.T) {
	ctx := context.Background()
	cases := execArmCases(t)

	seen := map[observe.Arm]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &execRecorder{}
			p, req := c.open(t, rec)
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			res := rec.last(t)
			if res.Arm != c.arm || res.Workers != c.workers || res.Shards != c.shards || res.Projected != c.projected {
				t.Errorf("arm/workers/shards/projected = %q/%d/%d/%v, want %q/%d/%d/%v",
					res.Arm, res.Workers, res.Shards, res.Projected, c.arm, c.workers, c.shards, c.projected)
			}
			if res.BytesRead <= 0 {
				t.Errorf("bytes_read = %d, want > 0", res.BytesRead)
			}
			if req.Crosstab == nil {
				assertRowsMatchRun(t, res, resp)
			} else if res.RowsScanned != resp.Metadata.TotalRows || res.RowsScanned == 0 {
				t.Errorf("crosstab rows scanned = %d, Metadata.TotalRows = %d", res.RowsScanned, resp.Metadata.TotalRows)
			}
		})
		seen[c.arm] = true
	}
	for _, a := range observe.AllArms() {
		if !seen[a] {
			t.Errorf("arm %q has no case", a)
		}
	}
}

// TestObservabilityExecRowsIndependentOfComponents: the row counters
// are still set when components are disabled (engine and request) and
// when `return` drops components.run.
//
// Falsified by sourcing the counters from Components.Run.
func TestObservabilityExecRowsIndependentOfComponents(t *testing.T) {
	ctx := context.Background()
	on := true
	variants := []struct {
		name string
		opts pulse.Options
		req  func(*types.Request)
	}{
		{"engine_disable_components", pulse.Options{DisableComponents: true}, func(*types.Request) {}},
		{"request_disable_components", pulse.Options{}, func(r *types.Request) { r.DisableComponents = &on }},
		{"return_excludes_run", pulse.Options{}, func(r *types.Request) {
			r.Return = &types.Return{Exclude: []string{"components.run"}}
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			rec := &execRecorder{}
			v.opts.FS = execSmallFS(t)
			p := execNew(t, v.opts, rec)
			req := execSumReq(execCohort)
			v.req(req)
			resp, err := p.Process(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Components != nil && resp.Components.Run != nil {
				t.Fatal("fixture: Components.Run present — the variant proves nothing")
			}
			res := rec.last(t)
			if res.RowsScanned != paritySmallRows || res.RowsMatched != paritySmallRows || res.RowsOut != int64(len(resp.Data)) {
				t.Errorf("rows scanned/matched/out = %d/%d/%d, want %d/%d/%d",
					res.RowsScanned, res.RowsMatched, res.RowsOut, paritySmallRows, paritySmallRows, len(resp.Data))
			}
		})
	}
}

// TestObservabilityExecBytesRead: a full scan reads exactly the cohort
// file — the afero read on a MemMapFs, the mapped region on disk.
//
// Falsified by dropping the counting wrapper (countReads) or the
// noteMapped call.
func TestObservabilityExecBytesRead(t *testing.T) {
	ctx := context.Background()
	t.Run("afero_read", func(t *testing.T) {
		rec := &execRecorder{}
		fsys := execSmallFS(t)
		p := execNew(t, pulse.Options{FS: fsys}, rec)
		if _, err := p.Process(ctx, execSumReq(execCohort)); err != nil {
			t.Fatal(err)
		}
		if got, want := rec.last(t).BytesRead, fileSize(t, fsys, execCohort); got != want {
			t.Errorf("bytes_read = %d, want the file size %d", got, want)
		}
	})
	t.Run("mmap", func(t *testing.T) {
		dir := t.TempDir()
		writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, execCohort), paritySchema(t), 0, paritySmallRows)
		rec := &execRecorder{}
		p := execNew(t, pulse.Options{DataDir: dir}, rec)
		if _, err := p.Process(ctx, execSumReq(execCohort)); err != nil {
			t.Fatal(err)
		}
		if got, want := rec.last(t).BytesRead, fileSize(t, afero.NewOsFs(), filepath.Join(dir, execCohort)); got != want {
			t.Errorf("bytes_read = %d, want the file size %d", got, want)
		}
	})
}
