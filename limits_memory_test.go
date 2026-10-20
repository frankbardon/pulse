package pulse_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
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
// packs them into a shard archive: three merge fragments (blocks key by
// the global ordinal; shard 1 starts inside block 1 and buffers its 5
// rows) where one file of the same 4,102 rows holds two blocks.
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

// wideMemoryArchive packs seven small numeric cohorts (uneven: 3 to 40
// records) into one shard archive — more shards than the per-shard
// reducer's merge window under 2 workers.
func wideMemoryArchive(t *testing.T, fs afero.Fs) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var paths []string
	for i, n := range []int{12, 40, 3, 25, 9, 31, 6} {
		var b strings.Builder
		b.WriteString("a,b\n")
		for r := range n {
			fmt.Fprintf(&b, "%d,%d\n", (i*41+r*13)%97, 20+(r*7)%13)
		}
		name := fmt.Sprintf("w%d.csv", i)
		if err := afero.WriteFile(fs, name, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: name})
		if err != nil {
			t.Fatalf("ImportFile: %v", err)
		}
		paths = append(paths, res.Path)
	}
	if _, err := p.CreateShardArchive(ctx, "wide.pulse", paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return "wide.pulse"
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
// estimate both run — a join whose right key repeats at its possible
// fan-out estimate, the build-side backstop refusing it at the certain
// one; under the default (Unlimited) there is no finding.
func TestLimits_MaxEstimatedMemory_PredictCertainAndProcessRefuses(t *testing.T) {
	ctx := context.Background()
	fs, cohort := limitsCohort(t)
	jfs, left, right := joinLimitCohorts(t)
	afs := afero.NewMemMapFs()
	arch := memoryArchive(t, afs)
	anchor := memoryArchiveAnchor(t, afs, arch, 1)
	wideArch := wideMemoryArchive(t, afs)

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
		// shardWorkers is Options.ShardWorkers: over an archive it picks
		// the serial iterator (1) or the per-shard reducer (2), whose
		// partials the estimate charges.
		shardWorkers int
		// anchor: path is an `archive#shard` anchor, which PredictBytes
		// (bytes in hand, no anchor resolution) cannot address.
		anchor bool
		// joinFanOut: a join whose right key repeats on an arm whose
		// state grows with the joined rows (#229). At the certain
		// estimate predict reports a POSSIBLE fan-out finding and the
		// build-side backstop refuses the run on the measured
		// multiplicity; both clear at the possible estimate.
		joinFanOut bool
		// defaultWeight is Options.DefaultWeight: a slot with no weight
		// of its own runs weighted, and both sides charge it so (U51
		// E2-S4: LimitInputs.DefaultWeight).
		defaultWeight *types.WeightSpec
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
		// U35 #437: grouped tier-1 tests — a moments test and a buffered
		// rank test, one instance per group.
		{name: "grouped tests", fs: fs, path: cohort, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
				Tests:        []*types.Test{{Type: types.TEST_T, Field: "a"}, {Type: types.TEST_SPEARMAN_R, Field: "a", Field2: "c"}}}
		}},
		{name: "archive matrix", fs: afs, path: arch, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: arch}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b"}}}}
		}},
		{name: "archive serial", fs: afs, path: arch, shardWorkers: 1, req: func() *types.Request { return archiveRanged(arch) }},
		{name: "archive per-shard", fs: afs, path: arch, shardWorkers: 2, req: func() *types.Request { return archiveRanged(arch) }},
		// U51 E2-S4: more shards than the merge window (2 workers: 4) —
		// both sides charge only the window's largest shards' partials.
		{name: "archive per-shard window", fs: afs, path: wideArch, shardWorkers: 2, req: func() *types.Request { return archiveRanged(wideArch) }},
		{name: "weighted by instance default", fs: fs, path: cohort, defaultWeight: &types.WeightSpec{Field: "c"}, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}, {Type: types.AGG_AVERAGE, Field: "b", Label: "m"}}}
		}},
		{name: "anchored shard", fs: afs, path: anchor, anchor: true, req: func() *types.Request { return archiveRanged(anchor) }},
		{name: "join process", fs: jfs, path: left, req: func() *types.Request { return joinedSum(left, right) }},
		{name: "join crosstab", fs: jfs, path: left, req: func() *types.Request { return joinedCrosstab(left, right) }, joinFanOut: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open := func(max int64) *pulse.Pulse {
				p, err := pulse.New(pulse.Options{FS: c.fs, DisableCrosstabFusion: c.noFusion, ShardWorkers: c.shardWorkers, DefaultWeight: c.defaultWeight, Limits: pulse.Limits{MaxEstimatedMemory: max}})
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
			_, perr := p.Process(ctx, c.req())
			d := requireLimitExceeded(t, perr, "max_estimated_memory", est-1, "Options.Limits.MaxEstimatedMemory")
			if d["observed"] != est {
				t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], est)
			}
			if !c.anchor {
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
				sameEntry(t, env, perr)
			}

			at := open(est)
			if c.joinFanOut {
				res, err := at.Predict(ctx, c.req())
				if err != nil || !res.Valid || len(res.LimitFindings) != 1 {
					t.Fatalf("join fan-out at the estimate: valid=%v findings=%+v err=%v", res.Valid, res.LimitFindings, err)
				}
				fd := res.LimitFindings[0]
				if fd.Grade != descriptor.LimitGradePossible || fd.Configured != est || fd.Details["join"] != 0 {
					t.Fatalf("join fan-out finding %+v", fd)
				}
				_, perr := at.Process(ctx, c.req())
				d := requireLimitExceeded(t, perr, "max_estimated_memory", est, "Options.Limits.MaxEstimatedMemory")
				if obs, _ := d["observed"].(int64); d["join"] != 0 || obs <= est || obs > fd.Estimated {
					t.Fatalf("backstop details %v (possible estimate %d)", d, fd.Estimated)
				}
				at = open(fd.Estimated)
			}
			if res, err := at.Predict(ctx, c.req()); err != nil || !res.Valid || res.LimitFindings != nil {
				t.Fatalf("at the estimate: valid=%v findings=%+v err=%v", res.Valid, res.LimitFindings, err)
			}
			if _, err := at.Process(ctx, c.req()); err != nil {
				t.Fatalf("at the estimate: %v", err)
			}
		})
	}
}

// memoryArchiveAnchor is the `arch#shard` anchor of arch's i-th shard.
func memoryArchiveAnchor(t *testing.T, fs afero.Fs, arch string, i int) string {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	shards, err := p.ListShards(context.Background(), arch)
	if err != nil || len(shards) <= i {
		t.Fatalf("ListShards: %v %+v", err, shards)
	}
	return arch + "#" + shards[i].Filename
}

// TestLimits_MaxEstimatedMemory_AnchorChargesShard (E8-S4, re-pinned
// U51 E1-S2): a run over an anchor-opened shard reads only that shard
// (OpenAnchor serves it as a section of the archive), so predict and the
// run both charge it exactly as the same shard extracted to a standalone
// file — no archive byte, no directory — and report the same figure on
// Predict and on the pre-flight refusal; Explain's request check grades
// the same estimate.
func TestLimits_MaxEstimatedMemory_AnchorChargesShard(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	arch := memoryArchive(t, fs)
	anchor := memoryArchiveAnchor(t, fs, arch, 1)
	whole, err := afero.ReadFile(fs, arch)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(whole), int64(len(whole)))
	if err != nil {
		t.Fatal(err)
	}
	shardName := anchor[len(arch)+1:]
	var shard []byte
	for _, f := range zr.File {
		if f.Name == shardName {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			shard, err = io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := afero.WriteFile(fs, "standalone.pulse", shard, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
	if err != nil {
		t.Fatal(err)
	}
	estimate := func(path string) int64 {
		t.Helper()
		req := &types.Request{Cohort: &types.Cohort{Filename: path}, Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.LimitFindings) != 1 {
			t.Fatalf("%s: findings = %+v", path, res.LimitFindings)
		}
		_, perr := p.Process(ctx, req)
		d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
		if d["observed"] != res.LimitFindings[0].Estimated {
			t.Fatalf("%s: runtime observed %v, predict estimated %d", path, d["observed"], res.LimitFindings[0].Estimated)
		}
		return res.LimitFindings[0].Estimated
	}
	anchored, standalone := estimate(anchor), estimate("standalone.pulse")
	if anchored != standalone {
		t.Fatalf("the anchored shard's estimate %d differs from its standalone twin's %d (archive %d bytes): an anchor charges its shard only", anchored, standalone, len(whole))
	}

	// Explain's request check predicts the anchor the same way: invalid
	// one byte under the anchored estimate, valid at it.
	for _, c := range []struct {
		max   int64
		valid bool
	}{{anchored - 1, false}, {anchored, true}} {
		q, err := pulse.New(pulse.Options{FS: fs, Limits: pulse.Limits{MaxEstimatedMemory: c.max}})
		if err != nil {
			t.Fatal(err)
		}
		res, err := q.Explain(ctx, descriptor.ExplainRequest{Request: &types.Request{Cohort: &types.Cohort{Filename: anchor},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Valid == nil {
			t.Fatalf("Explain under a %d cap: no verdict", c.max)
		}
		if *res.Valid != c.valid {
			t.Fatalf("Explain under a %d cap: valid = %v, want %v (anchored estimate %d)", c.max, *res.Valid, c.valid, anchored)
		}
	}
}

// archiveRanged groups the archive's records by a ranges of 10 — a
// grouper of unknown cardinality, so every shard partial on the
// per-shard arm is bounded by its own records.
func archiveRanged(arch string) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: arch},
		Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "a", Interval: 10}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "b", Label: "s"}}}
}

// TestLimits_MaxEstimatedMemory_ArchiveShardWorkers (E8-S3): over a
// shard archive the estimate follows Options.ShardWorkers as the
// dispatch does — the per-shard reducer's partials (4,097 + 5 records,
// one bucket bound each under an unknown grouper) are charged only when
// the run fans out — and predict and the run report the same figure
// under either cap.
func TestLimits_MaxEstimatedMemory_ArchiveShardWorkers(t *testing.T) {
	ctx := context.Background()
	fs := afero.NewMemMapFs()
	arch := memoryArchive(t, fs)
	estimate := func(workers int) int64 {
		t.Helper()
		p, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: workers, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Predict(ctx, archiveRanged(arch))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.LimitFindings) != 1 {
			t.Fatalf("findings = %+v", res.LimitFindings)
		}
		_, perr := p.Process(ctx, archiveRanged(arch))
		d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
		if d["observed"] != res.LimitFindings[0].Estimated {
			t.Fatalf("ShardWorkers %d: runtime observed %v, predict estimated %d", workers, d["observed"], res.LimitFindings[0].Estimated)
		}
		return res.LimitFindings[0].Estimated
	}
	serial, fanned := estimate(1), estimate(2)
	// 4,097 + 5 partial buckets of one aggregation — a partition
	// bucket's STATE, 256 + 256 each (U51 E2-S4: a partition never
	// finalizes, so it carries no output share).
	if d, want := fanned-serial, int64((linalg.MergeBlockSize+1+5)*(256+256)); d != want {
		t.Fatalf("the per-shard arm adds %d bytes, want %d (one bucket per shard record)", d, want)
	}
}

// TestPredict_MatrixEstimatedBytes_ArchiveBlocks: predict's
// estimated_bytes counts merge fragments — 4,097 + 5 sharded rows hold
// three — plus the 5 rows shard 1 buffers (columns a, b and the weight:
// 24 bytes each).
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
	if want := 3*m.AccumulatorBytes + 5*24; *m.EstimatedBytes != want {
		t.Fatalf("estimated_bytes = %d, want 3 fragments x %d + 5 buffered rows x 24 = %d", *m.EstimatedBytes, m.AccumulatorBytes, want)
	}
}

// TestLimits_MaxEstimatedMemory_DecodeWorkersPredictMatchesRuntime
// (U51 E2-S2, FR-39): predict reads Options.DecodeWorkers, so over a
// mappable single file past the parallel-decode threshold its memory
// estimate counts the per-worker partials exactly when the run takes the
// parallel-decode reducer — the figure predict reports is the one the
// runtime pre-flight refuses with, at DecodeWorkers 1 (serial) and 4
// (fanned out), and the fanned-out estimate is the larger.
//
// Falsified by dropping DecodeWorkers from the facade's predictOptions
// (predict then charges the serial arm at 4 workers too).
func TestLimits_MaxEstimatedMemory_DecodeWorkersPredictMatchesRuntime(t *testing.T) {
	ctx := context.Background()
	dir := parityLargeCohort(t.TempDir())(t)
	req := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "large.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}}}
	}
	estimate := map[int]int64{}
	for _, workers := range []int{1, 4} {
		open := func(max int64) *pulse.Pulse {
			p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: workers, Limits: pulse.Limits{MaxEstimatedMemory: max}})
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		probe, err := open(1).Predict(ctx, req())
		if err != nil {
			t.Fatal(err)
		}
		if len(probe.LimitFindings) != 1 {
			t.Fatalf("DecodeWorkers %d: probe findings = %+v", workers, probe.LimitFindings)
		}
		est := probe.LimitFindings[0].Estimated
		estimate[workers] = est
		_, perr := open(est-1).Process(ctx, req())
		d := requireLimitExceeded(t, perr, "max_estimated_memory", est-1, "Options.Limits.MaxEstimatedMemory")
		if d["observed"] != est {
			t.Fatalf("DecodeWorkers %d: runtime observed %v, predict estimated %d", workers, d["observed"], est)
		}
	}
	if estimate[4] <= estimate[1] {
		t.Fatalf("estimate at DecodeWorkers 4 = %d, at 1 = %d: want the per-worker partials counted", estimate[4], estimate[1])
	}
}
