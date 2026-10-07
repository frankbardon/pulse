package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestReturnSkipsComputation pins U18's compute plan for the
// Components sub-parts (rank 1): a part the request's `return`
// excludes is never BUILT — its processing.WorkStats counter does not
// move — while the same request with `return` absent (and under a
// selection that keeps it) builds it, so every zero is non-vacuous.
// It runs every arm that builds Components: the serial streaming and
// buffered scans (ungrouped and grouped), the parallel-decode reducer
// and the shard reducer.

// skipSchema: a nullable score (so floors carry n_null), a categorical
// grouping column and an id the filter reads.
func skipSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"x", "y", "z"} {
		_, _ = dict.Add(v)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 12, CsvColumnIdx: 2, Dictionary: dict},
	}}
}

func skipPayload(t testing.TB, n, offset int) []byte {
	t.Helper()
	recs := make([][]uint64, n)
	for i := range recs {
		v := offset + i
		recs[i] = []uint64{uint64(v), math.Float64bits(float64(v%97) * 1.25), uint64(v % 3)}
	}
	return writeNullablePulse(t, skipSchema(), recs, func(r, f int) bool { return f == 1 && (offset+r)%4 == 0 })
}

func skipArchive(t testing.TB, shardRows []int) []byte {
	t.Helper()
	var total uint64
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	for _, n := range shardRows {
		total += uint64(n)
	}
	var doc bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, skipSchema(), total, uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	offset := 0
	for i, n := range shardRows {
		write(fmt.Sprintf("s%d.pulse", i), skipPayload(t, n, offset))
		offset += n
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// skipRequest: two aggregation slots, a filter, optionally a grouper,
// optionally a buffered-only aggregator (AGG_MEDIAN routes the serial
// scan buffered: processing.canStream).
func skipRequest(path string, grouped, buffered bool) *types.Request {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Filterers: []*types.Filterer{
			{Type: types.FILTER_RANGE, Field: "id", Values: []string{"0", "100000000"}},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
		},
	}
	if buffered {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score", Label: "med"})
	}
	if grouped {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat"}}
	}
	return req
}

// skipArm builds a service and the cohort path one execution arm runs.
type skipArm struct {
	name      string
	long      bool // -short skips it (parallel decode needs >100K rows)
	mergeable bool // the arm needs a mergeable request (no AGG_MEDIAN)
	build     func(t *testing.T) (*Service, string)
}

func skipArms() []skipArm {
	return []skipArm{
		{name: "serial", build: func(t *testing.T) (*Service, string) {
			cfg := fs.NewMemMap()
			if err := afero.WriteFile(cfg.Fs(), "skip.pulse", skipPayload(t, 600, 0), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			svc := New(cfg)
			svc.SetDecodeWorkers(1)
			return svc, "skip.pulse"
		}},
		{name: "parallel_decode", long: true, mergeable: true, build: func(t *testing.T) (*Service, string) {
			dir := t.TempDir()
			osFs := afero.NewOsFs()
			path := dir + "/skip.pulse"
			if err := afero.WriteFile(osFs, path, skipPayload(t, parallelDecodeRecordThreshold+2048, 0), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
			if err != nil {
				t.Fatalf("fs.New: %v", err)
			}
			svc := New(cfg)
			svc.SetDecodeWorkers(4)
			cohort, err := svc.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			_, cleanup, available, err := buildParallelDecodeContext(svc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
			if err != nil {
				t.Fatalf("buildParallelDecodeContext: %v", err)
			}
			if cleanup != nil {
				defer func() { _ = cleanup() }()
			}
			if !available {
				t.Fatal("parallel decode unavailable: this arm would silently run serial")
			}
			return svc, path
		}},
		{name: "shard_reduce", mergeable: true, build: func(t *testing.T) (*Service, string) {
			cfg := fs.NewMemMap()
			if err := afero.WriteFile(cfg.Fs(), "skip_archive.pulse", skipArchive(t, []int{200, 170, 230}), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			svc := New(cfg)
			svc.SetShardWorkers(2)
			cohort, err := svc.Open(context.Background(), "skip_archive.pulse")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, ok := svc.shouldFanOut(skipRequest("skip_archive.pulse", true, false), cohort); !ok {
				t.Fatal("shouldFanOut refused: this arm would silently run serial")
			}
			return svc, "skip_archive.pulse"
		}},
	}
}

// skipCounter names one WorkStats counter and the sub-part it guards.
type skipCounter struct {
	name    string
	grouped bool // only a grouped run builds it
	get     func(processing.WorkStatsSnapshot) int64
}

var skipCounters = []skipCounter{
	{name: "aggregations", get: func(s processing.WorkStatsSnapshot) int64 { return s.AggComponentBuilds }},
	{name: "groups", grouped: true, get: func(s processing.WorkStatsSnapshot) int64 { return s.GroupFloorBuilds }},
	{name: "groupers", grouped: true, get: func(s processing.WorkStatsSnapshot) int64 { return s.GrouperComponentBuilds }},
	{name: "filterers", get: func(s processing.WorkStatsSnapshot) int64 { return s.FiltererComponentBuilds }},
	{name: "run", get: func(s processing.WorkStatsSnapshot) int64 { return s.RunComponentBuilds }},
}

// skipSelection is one `return` block and the counters it must zero.
type skipSelection struct {
	name string
	ret  *types.Return
	zero map[string]bool
}

func skipSelections() []skipSelection {
	all := map[string]bool{"aggregations": true, "groups": true, "groupers": true, "filterers": true, "run": true}
	ex := func(paths ...string) *types.Return { return &types.Return{Exclude: paths} }
	return []skipSelection{
		{name: "absent"},
		{name: "keeps_components", ret: ex("metadata")},
		{name: "exclude_components", ret: ex("components"), zero: all},
		{name: "standard", ret: &types.Return{Preset: types.ReturnPresetStandard}, zero: all},
		{name: "minimal", ret: &types.Return{Preset: types.ReturnPresetMinimal}, zero: all},
		{name: "exclude_aggregations", ret: ex("components.aggregations"), zero: map[string]bool{"aggregations": true, "groups": true}},
		{name: "exclude_groups", ret: ex("components.aggregations[*].groups"), zero: map[string]bool{"groups": true}},
		{name: "exclude_groupers", ret: ex("components.groupers"), zero: map[string]bool{"groupers": true}},
		{name: "exclude_filterers", ret: ex("components.filterers"), zero: map[string]bool{"filterers": true}},
		{name: "exclude_run", ret: ex("components.run"), zero: map[string]bool{"run": true}},
	}
}

func TestReturnSkipsComputation(t *testing.T) {
	for _, arm := range skipArms() {
		t.Run(arm.name, func(t *testing.T) {
			if arm.long && testing.Short() {
				t.Skip("parallel decode needs a cohort above the decode threshold")
			}
			svc, path := arm.build(t)
			for _, grouped := range []bool{false, true} {
				for _, buffered := range []bool{false, true} {
					if buffered && arm.mergeable {
						continue // AGG_MEDIAN is not mergeable: the arm would not engage
					}
					shape := fmt.Sprintf("grouped=%v/buffered=%v", grouped, buffered)
					var base *types.ResponseComponents // the `absent` run's block
					for _, sel := range skipSelections() {
						t.Run(shape+"/"+sel.name, func(t *testing.T) {
							req := skipRequest(path, grouped, buffered)
							req.Return = sel.ret
							before := processing.WorkStats()
							resp, err := svc.Process(context.Background(), req)
							if err != nil {
								t.Fatalf("Process: %v", err)
							}
							delta := processing.WorkStats().Sub(before)
							if len(resp.Data) == 0 {
								t.Fatal("no data rows: the fixture proves nothing")
							}
							for _, c := range skipCounters {
								if c.grouped && !grouped {
									continue
								}
								got := c.get(delta)
								if sel.zero[c.name] && got != 0 {
									t.Errorf("%s: excluded, yet built %d time(s)", c.name, got)
								}
								if !sel.zero[c.name] && got <= 0 {
									t.Errorf("%s: kept, yet never built (delta %d) — the zero assertions would be vacuous", c.name, got)
								}
							}
							if sel.ret == nil {
								base = resp.Components
								return
							}
							assertKeptComponentsEqual(t, base, resp.Components, sel.zero)
						})
					}
				}
			}
		})
	}
}

// The DisableComponents gate closes every sub-part whatever `return`
// keeps; a request overlay that reads its host's components keeps
// components.crosstab only (a payload-only overlay keeps nothing); a
// Compose overlay naming the slot (componentsVetoed) keeps every
// sub-part; the matrices slot follows the selection unvetoed (minimal
// drops its auxiliary / scalars / vectors); tests, post-tests and
// regressions follow the selection (minimal keeps them).
func TestResolveComputePlan_VetoAndGate(t *testing.T) {
	full := processing.FullComputePlan()
	svc := &Service{}
	ctx := context.Background()
	minimal := processing.ComputePlanFor(nil).WithoutComponents()
	minimal.MatrixAuxiliary, minimal.MatrixScalars, minimal.MatrixVectors = false, false, false
	vetoed := minimal.WithComponentsOf(full)
	crosstabKept := minimal
	crosstabKept.Crosstab = true

	excl := &types.Request{}
	ret := mustResolveReturn(t, &types.Request{Return: &types.Return{Preset: types.ReturnPresetMinimal}})
	if got := svc.resolveComputePlan(ctx, excl, ret, nil); got != minimal {
		t.Errorf("minimal: %+v; want components and matrix sub-parts off, whole-slot parts on", got)
	}
	payloadOnly := &types.Request{Overlays: []types.OverlaySpec{{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeRow}}}
	if got := svc.resolveComputePlan(ctx, payloadOnly, ret, nil); got != minimal {
		t.Errorf("payload-only overlay: %+v; want no veto", got)
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindPairwisePropZ, types.OverlayKindChiSqRow, types.OverlayKindFisherExactCell} {
		reading := &types.Request{Overlays: []types.OverlaySpec{
			{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeRow},
			{Kind: kind, Scope: types.OverlayScopeRow},
		}}
		if got := svc.resolveComputePlan(ctx, reading, ret, nil); got != crosstabKept {
			t.Errorf("%s: %+v; want components.crosstab kept, aux margins and the rest skipped", kind, got)
		}
	}
	if got := svc.resolveComputePlan(withComponentsVeto(ctx), excl, ret, nil); got != vetoed {
		t.Errorf("Compose slot veto: %+v; want every component kept", got)
	}
	svc.SetDisableComponents(true)
	reading := &types.Request{Overlays: []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow}}}
	if got := svc.resolveComputePlan(withComponentsVeto(ctx), reading, nil, nil); got.AnyComponents() {
		t.Errorf("closed gate under a veto: %+v; want every sub-part off", got)
	}
	// computePlanFor prefers the plan Service.process put on ctx.
	if got := svc.computePlanFor(withComputePlan(ctx, minimal), excl); got != minimal {
		t.Errorf("computePlanFor ignored the ctx plan: %+v", got)
	}
}

// composeSlotVetoes names exactly the slots a Compose overlay reads:
// its reference and its targets (no targets: every slot).
func TestComposeSlotVetoes(t *testing.T) {
	reqs := []*types.Request{{Label: "a"}, {Label: "b"}, {Label: "c"}, nil}
	if got := composeSlotVetoes(nil, reqs); got != nil {
		t.Errorf("no overlays: %v; want nil", got)
	}
	got := composeSlotVetoes([]types.ComposeOverlaySpec{{Reference: "a", Targets: []string{"c"}}}, reqs)
	if want := []bool{true, false, true, false}; !reflect.DeepEqual(got, want) {
		t.Errorf("ref a, target c: %v; want %v", got, want)
	}
	got = composeSlotVetoes([]types.ComposeOverlaySpec{{Reference: "b"}}, reqs)
	if want := []bool{true, true, true, false}; !reflect.DeepEqual(got, want) {
		t.Errorf("no targets: %v; want %v", got, want)
	}
}

func mustResolveReturn(t *testing.T, req *types.Request) *returnplan.Plan {
	t.Helper()
	p, err := descx.ResolveReturn(req, nil)
	if err != nil {
		t.Fatalf("ResolveReturn: %v", err)
	}
	return p
}

// assertKeptComponentsEqual: skipping a sub-part never moves a KEPT one
// — each kept sub-part equals the `return`-absent run's (aggregations
// minus groups[] when only the groups are skipped); a skipped one is
// absent.
func assertKeptComponentsEqual(t *testing.T, base, got *types.ResponseComponents, zero map[string]bool) {
	t.Helper()
	if base == nil {
		t.Fatal("the return-absent run carried no Components")
	}
	if got == nil {
		got = &types.ResponseComponents{}
	}
	check := func(name string, kept bool, want, have any, haveNil bool) {
		if !kept {
			if !haveNil {
				t.Errorf("%s: skipped, yet present: %+v", name, have)
			}
			return
		}
		if !reflect.DeepEqual(want, have) {
			t.Errorf("%s: kept figures moved\n  got:  %+v\n  want: %+v", name, have, want)
		}
	}
	wantAggs := base.Aggregations
	if zero["groups"] && !zero["aggregations"] {
		wantAggs = make([]types.AggregationComponents, len(base.Aggregations))
		for i, a := range base.Aggregations {
			a.Groups = nil
			wantAggs[i] = a
		}
	}
	check("aggregations", !zero["aggregations"], wantAggs, got.Aggregations, got.Aggregations == nil)
	check("groupers", !zero["groupers"], base.Groupers, got.Groupers, got.Groupers == nil)
	check("filterers", !zero["filterers"], base.Filterers, got.Filterers, got.Filterers == nil)
	check("run", !zero["run"], base.Run, got.Run, got.Run == nil)
}
