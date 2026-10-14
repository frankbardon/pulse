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
	anchor := memoryArchiveAnchor(t, afs, arch, 1)

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
		{name: "archive serial", fs: afs, path: arch, shardWorkers: 1, req: func() *types.Request { return archiveRanged(arch) }},
		{name: "archive per-shard", fs: afs, path: arch, shardWorkers: 2, req: func() *types.Request { return archiveRanged(arch) }},
		{name: "anchored shard", fs: afs, path: anchor, anchor: true, req: func() *types.Request { return archiveRanged(anchor) }},
		{name: "join process", fs: jfs, path: left, req: func() *types.Request { return joinedSum(left, right) }},
		{name: "join crosstab", fs: jfs, path: left, req: func() *types.Request { return joinedCrosstab(left, right) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open := func(max int64) *pulse.Pulse {
				p, err := pulse.New(pulse.Options{FS: c.fs, DisableCrosstabFusion: c.noFusion, ShardWorkers: c.shardWorkers, Limits: pulse.Limits{MaxEstimatedMemory: max}})
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

// TestLimits_MaxEstimatedMemory_AnchorChargesArchive (E8-S4): a run over
// an anchor-opened shard reads the WHOLE archive to reach the shard, so
// predict and the run both charge the archive's byte length in place of
// the shard's records x stride — the anchor's estimate is exactly
// 3 x (archive bytes - shard payload) above the same shard's as a
// standalone file — and report the same figure on Predict and on the
// pre-flight refusal; Explain's request check grades the same estimate.
func TestLimits_MaxEstimatedMemory_AnchorChargesArchive(t *testing.T) {
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
	// The shard's payload is its bytes past the preamble: 5 records.
	cohort, err := p.Open(ctx, "standalone.pulse")
	if err != nil {
		t.Fatal(err)
	}
	payload := int64(cohort.Schema().RecordByteSize()) * 5
	anchored, standalone := estimate(anchor), estimate("standalone.pulse")
	if d, want := anchored-standalone, 3*(int64(len(whole))-payload); d != want {
		t.Fatalf("the anchor adds %d bytes over its standalone shard, want 3 x (%d archive bytes - %d payload) = %d", d, len(whole), payload, want)
	}

	// Explain's request check predicts the anchor the same way: invalid
	// one byte under the anchored estimate (the standalone shard's
	// estimate would fit), valid at it.
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
	// 4,097 + 5 partial buckets of one aggregation (512 + 256 each).
	if d, want := fanned-serial, int64((linalg.MergeBlockSize+1+5)*(512+256)); d != want {
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
