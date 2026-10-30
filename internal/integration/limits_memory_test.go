package integration_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
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
	t.Parallel()

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
	// U51 E5-S9: a weighted two-measure crosstab with margins and a
	// margin_aggregations entry — cells, axis keys, margin and auxiliary
	// slots charged per measure from the per-aggregator table.
	measures := func(c string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: c}, Weight: &types.WeightSpec{Field: "c"}, Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b"}},
			Cells:              []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}, {Type: types.AGG_COUNT, Field: "a", Label: "n"}},
			MarginAggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "c", Label: "avg_c"}},
			Margins:            types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
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
		{name: "fused crosstab measures", fs: fs, path: cohort, req: func() *types.Request { return measures(cohort) }},
		{name: "buffered crosstab measures", fs: fs, path: cohort, req: func() *types.Request { return measures(cohort) }, noFusion: true},
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
		// U51 E5-S4: under a budget both shrink the window to fit, so the
		// probe's estimate is one live partial's and the run at it fans
		// out at that window (TestLimits_ShardMergeWindowShrinksToBudget).
		{name: "archive per-shard window", fs: afs, path: wideArch, shardWorkers: 2, req: func() *types.Request { return archiveRanged(wideArch) }},
		// U51 E5-S6: a grouper of data-dependent cardinality over the
		// imported u4 column `b` is charged its 16-key width bound, below
		// the 30 records.
		{name: "value-width grouper", fs: fs, path: cohort, req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}
		}},
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

// TestLimits_ShardMergeWindowShrinksToBudget (U51 E5-S4): on an
// archive of more shards than the merge window, a MaxEstimatedMemory
// at the one-live-partial estimate — below the unshrunk window's — is
// a valid prediction AND a run: the per-shard reducer shrinks its
// window to fit (limits.ShardMergeWindowWithin, predict's rule) instead
// of being refused, and answers exactly as the unbudgeted run does.
// Window-for-window parity: internal/service
// TestGroupMemory_MergeWindowFitsBudget.
func TestLimits_ShardMergeWindowShrinksToBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	afs := afero.NewMemMapFs()
	wideArch := wideMemoryArchive(t, afs)
	open := func(max int64) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: afs, ShardWorkers: 2, Limits: pulse.Limits{MaxEstimatedMemory: max}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	probe, err := open(1).Predict(ctx, archiveRanged(wideArch))
	if err != nil || len(probe.LimitFindings) != 1 {
		t.Fatalf("probe: %v %+v", err, probe.LimitFindings)
	}
	est := probe.LimitFindings[0].Estimated
	// The estimate charges ONE live partial — the largest shard's 40
	// buckets (an unknown grouper is bounded by records) at 256 + 128
	// state each (AGG_SUM's per-aggregator entry, U51 E5-S7) — over the
	// serial arm's; the unshrunk window (4) would
	// charge the four largest shards' 40 + 31 + 25 + 12.
	sp, err := pulse.New(pulse.Options{FS: afs, ShardWorkers: 1, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
	if err != nil {
		t.Fatal(err)
	}
	serial, err := sp.Predict(ctx, archiveRanged(wideArch))
	if err != nil || len(serial.LimitFindings) != 1 {
		t.Fatalf("serial probe: %v %+v", err, serial.LimitFindings)
	}
	if d := est - serial.LimitFindings[0].Estimated; d != 40*(256+128) {
		t.Fatalf("one-partial window charges %d over serial, want %d", d, 40*(256+128))
	}
	base, err := open(0).Process(ctx, archiveRanged(wideArch))
	if err != nil {
		t.Fatal(err)
	}
	at := open(est)
	res, err := at.Predict(ctx, archiveRanged(wideArch))
	if err != nil || !res.Valid || res.LimitFindings != nil {
		t.Fatalf("predict at %d: valid=%v findings=%+v err=%v", est, res.Valid, res.LimitFindings, err)
	}
	got, err := at.Process(ctx, archiveRanged(wideArch))
	if err != nil {
		t.Fatalf("process at %d: %v", est, err)
	}
	if !reflect.DeepEqual(got.Data, base.Data) {
		t.Fatalf("shrunk-window answer %v, unbudgeted %v", got.Data, base.Data)
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
	t.Parallel()

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
// dispatch does — the per-shard reducer's partials (4,097 + 5 records;
// a u8 range grouper's buckets, at most 256 a shard) are charged only when
// the run fans out — and predict and the run report the same figure
// under either cap.
func TestLimits_MaxEstimatedMemory_ArchiveShardWorkers(t *testing.T) {
	t.Parallel()

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
	// The largest shard's partial buckets of one aggregation — a
	// partition bucket's STATE, 256 + 128 each (AGG_SUM's entry, U51
	// E5-S7; U51 E2-S4: a partition
	// never finalizes, so it carries no output share). GROUP_RANGE over
	// the imported u8 column `a` mints at most 256 keys, below the
	// shard's 4,097 records (U51 E5-S6, the value-width bound). Only that
	// shard: a 1-byte budget shrinks the merge window to one live partial
	// on both sides (U51 E5-S4, limits.ShardMergeWindowWithin), where the
	// unbudgeted window (4) would charge both shards' 256 + 5.
	if d, want := fanned-serial, int64(256*(256+128)); d != want {
		t.Fatalf("the per-shard arm adds %d bytes, want %d (the u8 width bound of the window's shard)", d, want)
	}
}

// TestPredict_MatrixEstimatedBytes_ArchiveBlocks: predict's
// estimated_bytes counts merge fragments — 4,097 + 5 sharded rows hold
// three — plus the 5 rows shard 1 buffers (columns a, b and the weight:
// 24 bytes each).
func TestPredict_MatrixEstimatedBytes_ArchiveBlocks(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// TestLimits_ValueWidthBoundCoversEveryKey (U51 E5-S6): the memory
// estimate's value-width bucket bound is never under the keys a run
// mints. Over a cohort holding EVERY value of its u4 and u8 columns,
// the run's group count equals the bound predict charges (16, 256 —
// the bound is attained, so any smaller bound would under-estimate);
// over a u16 column with fewer records than 2^16 values the bound
// stays the record count. Buckets are read off the estimate: the
// grouped request's figure less the ungrouped one's, in one-aggregation
// merged buckets (state 256 + 128 — AGG_SUM's per-aggregator entry —
// and output 1,024 + 576), plus the one
// bucket an ungrouped run holds.
func TestLimits_ValueWidthBoundCoversEveryKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	const records = 3_000
	var b strings.Builder
	b.WriteString("n,v8,w,x\n")
	for r := range records {
		fmt.Fprintf(&b, "%d,%d,%d,%d.5\n", r%16, (r*7)%256, r, r%11)
	}
	if err := afero.WriteFile(fs, "v.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
	if err != nil {
		t.Fatal(err)
	}
	imp, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "v.csv"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := p.Inspect(ctx, imp.Path)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := map[string]string{"n": "u4", "v8": "u8", "w": "u16"}
	for _, f := range info.Fields {
		if w, ok := wantTypes[f.Name]; ok && string(f.Type) != w {
			t.Fatalf("column %s imported as %s, want %s", f.Name, f.Type, w)
		}
	}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "total"}}
	estimate := func(groups []*types.Group) int64 {
		t.Helper()
		res, err := p.Predict(ctx, &types.Request{Cohort: &types.Cohort{Filename: imp.Path}, Groups: groups, Aggregations: sum})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.LimitFindings) != 1 {
			t.Fatalf("findings = %+v", res.LimitFindings)
		}
		return res.LimitFindings[0].Estimated
	}
	const bucket = 256 + 128 + 1024 + 576
	base := estimate(nil)
	run, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		group *types.Group
		want  int64
	}{
		{&types.Group{Type: types.GROUP_CATEGORY, Field: "n"}, 16},
		{&types.Group{Type: types.GROUP_CATEGORY, Field: "v8"}, 256},
		{&types.Group{Type: types.GROUP_ROUNDED, Field: "v8", Interval: 1}, 256},
		{&types.Group{Type: types.GROUP_CATEGORY, Field: "w"}, records},
	} {
		d := estimate([]*types.Group{c.group}) - base
		if d%bucket != 0 {
			t.Fatalf("%s %s: grouped delta %d is not whole buckets", c.group.Type, c.group.Field, d)
		}
		charged := d/bucket + 1
		resp, err := run.Process(ctx, &types.Request{Cohort: &types.Cohort{Filename: imp.Path}, Groups: []*types.Group{c.group}, Aggregations: sum})
		if err != nil {
			t.Fatal(err)
		}
		minted := int64(len(resp.Data))
		if charged != c.want || minted != c.want {
			t.Errorf("%s %s: predict charges %d buckets, the run mints %d; want both %d", c.group.Type, c.group.Field, charged, minted, c.want)
		}
	}
}

// TestLimits_MaxEstimatedMemory_ComponentsPredictMatchesRuntime (U51
// E5-S7): the memory estimate charges a components-off run the
// components-off finalize output, and predict and the pre-flight
// resolve the flag the same way — the request's disable_components when
// set, else Options.DisableComponents (the runtime's components gate):
// on every combination predict's estimate is the figure the run is
// refused with, and the off figure is below the on one.
func TestLimits_MaxEstimatedMemory_ComponentsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("g,x\n")
	for r := range 2_000 {
		fmt.Fprintf(&b, "%d,%d.5\n", r, r%11)
	}
	if err := afero.WriteFile(fs, "c.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	importer, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	imp, err := importer.ImportFile(ctx, pulse.ImportSpec{SourcePath: "c.csv"})
	if err != nil {
		t.Fatal(err)
	}
	req := func(disable *bool) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: imp.Path}, DisableComponents: disable,
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "x", Label: "s"},
				{Type: types.AGG_MAX, Field: "x", Label: "m"},
			}}
	}
	yes, no := true, false
	estimates := map[bool][]int64{}
	for _, c := range []struct {
		name      string
		engineOff bool
		disable   *bool
		off       bool
	}{
		{"engine on", false, nil, false},
		{"engine off", true, nil, true},
		{"request off", false, &yes, true},
		{"request on over engine off", true, &no, false},
		{"request off over engine off", true, &yes, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: fs, DisableComponents: c.engineOff, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Predict(ctx, req(c.disable))
			if err != nil {
				t.Fatal(err)
			}
			if len(res.LimitFindings) != 1 {
				t.Fatalf("findings = %+v", res.LimitFindings)
			}
			est := res.LimitFindings[0].Estimated
			_, perr := p.Process(ctx, req(c.disable))
			d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
			if d["observed"] != est {
				t.Fatalf("runtime observed %v, predict estimated %d", d["observed"], est)
			}
			estimates[c.off] = append(estimates[c.off], est)
		})
	}
	for off, es := range estimates {
		for _, e := range es[1:] {
			if e != es[0] {
				t.Errorf("components off=%v: estimates %v differ", off, es)
			}
		}
	}
	if len(estimates[true]) == 0 || len(estimates[false]) == 0 || estimates[true][0] >= estimates[false][0] {
		t.Errorf("components-off estimate %v not below the components-on %v", estimates[true], estimates[false])
	}
}

// TestLimits_MaxEstimatedMemory_CrosstabGridPredictMatchesRuntime (U51
// E5-S11): a crosstab over two unbounded axes charges its dense grid —
// rows x cols, capped by MaxCrosstabCells — on both arms, and predict
// and the pre-flight judge the same cap (both through
// RequestLimitFindings): on every cap and arm predict's estimate is the
// figure the run is refused with, and a smaller cap charges less.
func TestLimits_MaxEstimatedMemory_CrosstabGridPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("g,h,x\n")
	for r := range 2_000 {
		fmt.Fprintf(&b, "%d,%d,%d.5\n", r, r, r%11)
	}
	if err := afero.WriteFile(fs, "c.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	importer, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	imp, err := importer.ImportFile(ctx, pulse.ImportSpec{SourcePath: "c.csv"})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: imp.Path}, Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "s"},
	}}
	for _, buffered := range []bool{false, true} {
		var prev int64
		for _, cellCap := range []int64{pulse.Unlimited, 1_000_000, 100_000} {
			p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: buffered,
				Limits: pulse.Limits{MaxEstimatedMemory: 1, MaxCrosstabCells: cellCap}})
			if err != nil {
				t.Fatal(err)
			}
			res, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			var est int64 = -1
			for _, f := range res.LimitFindings {
				if f.Limit == "max_estimated_memory" {
					est = f.Estimated
				}
			}
			if est < 0 {
				t.Fatalf("buffered=%v cap=%d: no memory finding in %+v", buffered, cellCap, res.LimitFindings)
			}
			_, perr := p.Process(ctx, req)
			d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
			if d["observed"] != est {
				t.Fatalf("buffered=%v cap=%d: runtime observed %v, predict estimated %d", buffered, cellCap, d["observed"], est)
			}
			if prev != 0 && est >= prev {
				t.Errorf("buffered=%v cap=%d: estimate %d not below the looser cap's %d", buffered, cellCap, est, prev)
			}
			prev = est
		}
	}
}
