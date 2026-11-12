package integration_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// distinctArchive imports three cohorts of 200 records and packs them
// into a shard archive written under opts (FS set here). Each shard's
// u32 `id` holds 40 distinct values, offset by 10 a shard, so a value
// repeats ACROSS shards: the per-shard counts sum to 120 while the
// union holds 60. `id2` holds 3 distinct values a shard (5 overall),
// `id3` the same 4 in every shard (12 recorded, 4 overall),
// every 9th row's `id` is empty (a nullable u32 — a null row mints no
// key), and `x` is the summed value.
func distinctArchive(t *testing.T, fs afero.Fs, name string, opts pulse.Options) string {
	t.Helper()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var paths []string
	for s := range 3 {
		var b strings.Builder
		b.WriteString("id,id2,id3,x\n")
		for r := range 200 {
			id := fmt.Sprintf("%d", 100_000+s*10+r%40)
			if r%9 == 0 {
				id = ""
			}
			fmt.Fprintf(&b, "%s,%d,%d,%d.5\n", id, 70_000+s+r%3, 90_000+r%4, r%11)
		}
		src := fmt.Sprintf("%s-%d.csv", name, s)
		if err := afero.WriteFile(fs, src, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: src})
		if err != nil {
			t.Fatalf("ImportFile: %v", err)
		}
		paths = append(paths, res.Path)
	}
	path := name + ".pulse"
	if _, err := p.CreateShardArchive(ctx, path, paths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	return path
}

// TestLimits_DistinctBoundCoversEveryKey (U73 #500): over a shard
// archive whose writer recorded per-shard distinct counts, the memory
// estimate charges a value-keyed grouper on a u32 field the SUM of the
// shards' counts (120) — below the records (600) and the u32 width
// (2^32) it charged before — and the multi-grouper product of those
// bounds capped by the records. The bound is never below the keys the
// run mints, a value repeated across shards included (the run mints
// the 60-key union; nulls mint none), and predict and the runtime
// pre-flight report the same figure. A writer opted out of counting,
// or one whose shards ran past its counting limit, leaves today's
// record bound.
func TestLimits_DistinctBoundCoversEveryKey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fs := afero.NewMemMapFs()
	counted := distinctArchive(t, fs, "counted", pulse.Options{})
	optedOut := distinctArchive(t, fs, "optout", pulse.Options{DisableShardDistinct: true})
	overLimit := distinctArchive(t, fs, "overlimit", pulse.Options{ShardDistinctLimit: 10})

	p, err := pulse.New(pulse.Options{FS: fs, ShardWorkers: 1, Limits: pulse.Limits{MaxEstimatedMemory: 1}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := p.Inspect(ctx, counted)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range info.Fields {
		if strings.HasPrefix(f.Name, "id") && string(f.Type) != "u32" {
			t.Fatalf("column %s imported as %s, want u32", f.Name, f.Type)
		}
	}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "total"}}
	request := func(path string, groups []*types.Group) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: path}, Groups: groups, Aggregations: sum}
	}
	estimate := func(path string, groups []*types.Group) int64 {
		t.Helper()
		req := request(path, groups)
		res, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.LimitFindings) != 1 {
			t.Fatalf("findings = %+v", res.LimitFindings)
		}
		_, perr := p.Process(ctx, req)
		d := requireLimitExceeded(t, perr, "max_estimated_memory", 1, "Options.Limits.MaxEstimatedMemory")
		if d["observed"] != res.LimitFindings[0].Estimated {
			t.Fatalf("%s %v: runtime observed %v, predict estimated %d", path, groups, d["observed"], res.LimitFindings[0].Estimated)
		}
		return res.LimitFindings[0].Estimated
	}
	// One merged AGG_SUM bucket: state 256 + 128, output 1,024 + 576.
	const bucket = 256 + 128 + 1024 + 576
	run, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	category := &types.Group{Type: types.GROUP_CATEGORY, Field: "id"}
	rounded := &types.Group{Type: types.GROUP_ROUNDED, Field: "id", Interval: 1}
	second := &types.Group{Type: types.GROUP_CATEGORY, Field: "id2"}
	for _, c := range []struct {
		path   string
		groups []*types.Group
		want   int64 // buckets charged
		minted int64 // keys the run mints
	}{
		{counted, []*types.Group{category}, 120, 60},
		{counted, []*types.Group{rounded}, 120, 60},
		{counted, []*types.Group{second}, 9, 5},
		{optedOut, []*types.Group{category}, 600, 60},
		{overLimit, []*types.Group{category}, 600, 60},
	} {
		d := estimate(c.path, c.groups) - estimate(c.path, nil) + bucket
		if d%bucket != 0 {
			t.Fatalf("%s %v: grouped delta %d is not whole buckets", c.path, c.groups[0].Type, d)
		}
		if charged := d / bucket; charged != c.want {
			t.Errorf("%s %s: predict charges %d buckets, want %d", c.path, c.groups[0].Type, charged, c.want)
		}
		requireMinted(t, run, request(c.path, c.groups), c.minted, c.want)
	}

	// Multi-grouper: the product of the per-field bounds, capped by the
	// records — each read against its own archive's ungrouped figure
	// (the archives' directory bytes differ). id x id2 (120 x 9 = 1,080)
	// caps at the 600 records, the opted-out twin's figure; id2 x a
	// range over id3 (9 x 12 = 108) is 492 composite buckets under it.
	delta := func(path string, groups []*types.Group) int64 {
		return estimate(path, groups) - estimate(path, nil)
	}
	capped := []*types.Group{category, second}
	if a, b := delta(counted, capped), delta(optedOut, capped); a != b {
		t.Errorf("id x id2: charged %d, the opted-out twin %d; both cap at the records", a, b)
	}
	requireMinted(t, run, request(counted, capped), 0, 600)
	pair := []*types.Group{second, {Type: types.GROUP_RANGE, Field: "id3", Interval: 2}}
	a, b := delta(counted, pair), delta(optedOut, pair)
	if a >= b || (b-a)%(600-108) != 0 || (b-a)/(600-108) <= bucket {
		t.Errorf("id2 x range(id3): charged %d, the opted-out twin %d; want 492 composite buckets fewer", a, b)
	}
	requireMinted(t, run, request(counted, pair), 0, 108)
}

// requireMinted runs req and checks the keys it mints: exactly want
// when want > 0, and never above bound — the buckets predict charged.
func requireMinted(t *testing.T, run *pulse.Pulse, req *types.Request, want, bound int64) {
	t.Helper()
	resp, err := run.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	minted := int64(len(resp.Data))
	if want > 0 && minted != want {
		t.Errorf("%v: the run mints %d keys, want %d", req.Groups, minted, want)
	}
	if minted > bound {
		t.Errorf("%v: the run mints %d keys, above the %d predict charges", req.Groups, minted, bound)
	}
}
