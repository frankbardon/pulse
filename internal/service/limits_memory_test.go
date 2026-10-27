package service

import (
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// memoryHost is one request arm the MaxEstimatedMemory pre-flight
// guards: run drives it on svc, cohorts are the files it must not
// drain when refused.
type memoryHost struct {
	seed    func(t *testing.T, mem afero.Fs)
	run     func(svc *Service) error
	cohorts []string
}

func memoryHosts() map[string]memoryHost {
	ctx := context.Background()
	synthetic := func(t *testing.T, mem afero.Fs) { makeSyntheticCohort(t, mem, "bench.pulse") }
	process := func(req func() *types.Request) func(*Service) error {
		return func(svc *Service) error { _, err := svc.Process(ctx, req()); return err }
	}
	hosts := map[string]memoryHost{
		"process": {synthetic, process(func() *types.Request { return processSumReq("bench.pulse") }), []string{"bench.pulse"}},
		"buffered process": {synthetic, process(func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "bench.pulse"},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "a", Label: "m"}}}
		}), []string{"bench.pulse"}},
		"crosstab": {synthetic, process(func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "bench.pulse"}, Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat0"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat1"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "a", Label: "s"},
			}}
		}), []string{"bench.pulse"}},
		"matrix": {synthetic, process(func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "bench.pulse"},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
				Matrices:     []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "cov", Fields: []string{"a", "b", "c"}}}}
		}), []string{"bench.pulse"}},
		"facet": {synthetic, func(svc *Service) error {
			_, err := svc.FacetSchema(ctx, &types.FacetRequest{Cohort: &types.Cohort{Filename: "bench.pulse"}, Fields: []string{"a", "cat0"}})
			return err
		}, []string{"bench.pulse"}},
		// The simple Facet's streamed (numeric) path; its categorical
		// fast path reads no record and has no pre-flight
		// (TestFacet_DictionaryPathSkipsMemoryPreflight).
		"simple facet": {synthetic, func(svc *Service) error {
			_, err := svc.Facet(ctx, "bench.pulse", "a")
			return err
		}, []string{"bench.pulse"}},
	}
	for name, req := range joinLimitRequests() {
		hosts["join "+name] = memoryHost{joinLimitFixture, process(req), []string{"left.pulse", "right.pulse"}}
	}
	return hosts
}

// TestLimitsPreflight_MaxEstimatedMemoryRefusesBeforeDecode: on every
// host — the serial dispatch (streaming and buffered), the crosstab,
// a matrix request, the rich facet, the simple facet's streamed path,
// the plain join and the joined crosstab — a MaxEstimatedMemory below the estimate is refused by the
// pre-flight with PULSE_LIMIT_EXCEEDED before any handle drains a
// cohort (a join's build side included), and the same run AT the
// estimate it reported drains them (the control that keeps the read
// assertion from being vacuous, and pins the refusal to the estimate)
// — for a join whose right key repeats, at the build-side backstop's
// figure, which refuses the pre-flight's estimate after the build.
func TestLimitsPreflight_MaxEstimatedMemoryRefusesBeforeDecode(t *testing.T) {
	for name, h := range memoryHosts() {
		t.Run(name, func(t *testing.T) {
			run := func(max int64) (fsCounts, map[string]int64, error) {
				mem := afero.NewMemMapFs()
				h.seed(t, mem)
				wrapped := newCountingFs(mem)
				cfg, err := fs.New(fs.WithFs(wrapped))
				if err != nil {
					t.Fatal(err)
				}
				svc := New(cfg)
				l := limits.Defaults()
				l.MaxEstimatedMemory = max
				svc.SetLimits(l)
				perr := h.run(svc)
				sizes := map[string]int64{}
				for _, c := range h.cohorts {
					fi, err := mem.Stat(c)
					if err != nil {
						t.Fatal(err)
					}
					sizes[c] = fi.Size()
				}
				return wrapped.snapshot(), sizes, perr
			}

			snap, sizes, err := run(1)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
			}
			if ce.Details["limit"] != "max_estimated_memory" || ce.Details["configured"] != int64(1) {
				t.Fatalf("details = %v", ce.Details)
			}
			estimate, _ := ce.Details["observed"].(int64)
			if estimate <= 1 {
				t.Fatalf("observed = %v", ce.Details["observed"])
			}
			for _, c := range h.cohorts {
				if fr := snap.FullReads[c]; fr != 0 {
					t.Fatalf("refused run drained %s %d time(s); the pre-flight must refuse before decode", c, fr)
				}
				if got := snap.BytesRead[c]; got >= sizes[c] {
					t.Fatalf("refused run read %d of %d bytes of %s; want header + schema only", got, sizes[c], c)
				}
			}

			snap, _, err = run(estimate)
			// A join whose right key repeats (the joined crosstab: each id
			// 166-167 times, buffered) passes the pre-flight at its
			// estimate and is refused by the build-side backstop (#229) on
			// left x the measured multiplicity — after the build drained,
			// before the left side is probed — so the control runs at that
			// figure.
			if stderrors.As(err, &ce) && ce.Details["join"] == 0 {
				if snap.FullReads["right.pulse"] == 0 || snap.FullReads["left.pulse"] != 0 {
					t.Fatalf("backstop: drained right %d, left %d time(s); want the build only", snap.FullReads["right.pulse"], snap.FullReads["left.pulse"])
				}
				estimate, _ = ce.Details["observed"].(int64)
				snap, _, err = run(estimate)
			}
			if err != nil {
				t.Fatalf("at the estimate (%d): %v", estimate, err)
			}
			for _, c := range h.cohorts {
				if snap.FullReads[c] == 0 {
					t.Fatalf("control: the run at the estimate never drained %s — the read assertion above would be vacuous", c)
				}
			}
			if _, _, err := run(estimate - 1); err == nil {
				t.Fatalf("one byte under the estimate (%d) ran", estimate-1)
			}
		})
	}
}

// TestFacet_DictionaryPathSkipsMemoryPreflight: the simple Facet's
// categorical fast path returns the schema's dictionary — it reads no
// record and mints nothing — so even a 1-byte MaxEstimatedMemory does
// not refuse it, and it still drains nothing; the same limit refuses
// the streamed (numeric) path.
func TestFacet_DictionaryPathSkipsMemoryPreflight(t *testing.T) {
	mem := afero.NewMemMapFs()
	makeSyntheticCohort(t, mem, "bench.pulse")
	wrapped := newCountingFs(mem)
	cfg, err := fs.New(fs.WithFs(wrapped))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1
	svc.SetLimits(l)
	values, err := svc.Facet(context.Background(), "bench.pulse", "cat0")
	if err != nil {
		t.Fatalf("dictionary path refused: %v", err)
	}
	if len(values) == 0 {
		t.Fatal("dictionary path returned no values")
	}
	if fr := wrapped.snapshot().FullReads["bench.pulse"]; fr != 0 {
		t.Fatalf("dictionary path drained the cohort %d time(s)", fr)
	}
	if _, err := svc.Facet(context.Background(), "bench.pulse", "a"); !errors.HasCode(err, errors.PULSE_LIMIT_EXCEEDED) {
		t.Fatalf("streamed path: err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
}

// TestMaxEstimatedMemory_DefaultReadsNoCount: under the default
// (Unlimited) memory limit the pre-flight reads no record count — no
// extra header open of the scanned cohort, no build-side count — so an
// unset limit costs nothing; a set one reads each once.
func TestMaxEstimatedMemory_DefaultReadsNoCount(t *testing.T) {
	orig := joinBuildCount
	calls := 0
	joinBuildCount = func(ctx context.Context, s *Service, path string) (uint64, error) {
		calls++
		return orig(ctx, s, path)
	}
	t.Cleanup(func() { joinBuildCount = orig })
	run := func(memory int64) (opens int) {
		mem := afero.NewMemMapFs()
		joinLimitFixture(t, mem)
		wrapped := newCountingFs(mem)
		cfg, err := fs.New(fs.WithFs(wrapped))
		if err != nil {
			t.Fatal(err)
		}
		svc := New(cfg)
		l := limits.Defaults()
		l.MaxJoinBuildRows = limits.Unlimited
		l.MaxEstimatedMemory = memory
		svc.SetLimits(l)
		if _, err := svc.Process(context.Background(), joinLimitRequests()["process"]()); err != nil {
			t.Fatal(err)
		}
		for _, p := range wrapped.snapshot().OpenedPaths {
			if p == "left.pulse" {
				opens++
			}
		}
		return opens
	}
	defOpens := run(limits.Unlimited)
	if calls != 0 {
		t.Fatalf("default memory limit read the build-side count %d time(s)", calls)
	}
	setOpens := run(math.MaxInt64)
	if calls != 1 {
		t.Fatalf("a set memory limit read the build-side count %d time(s), want 1", calls)
	}
	if setOpens != defOpens+1 {
		t.Fatalf("left cohort opened %d time(s) under a set limit, %d under the default; want exactly one more (the header count)", setOpens, defOpens)
	}
}

// TestMaxEstimatedMemory_ChainStage: a chain stage after the first is
// judged on its own input — the previous stage's rows, counted exactly
// — so a stage whose state outgrows the limit its source stage fits is
// refused at that stage (details.stage), before it runs.
func TestMaxEstimatedMemory_ChainStage(t *testing.T) {
	mem := afero.NewMemMapFs()
	makeSyntheticCohort(t, mem, "bench.pulse")
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	// Stage 0: 16 category buckets of one sum. Stage 1: an
	// unknown-cardinality grouper over those 16 rows — bounded by the
	// record count — with three aggregations, so its bucket state
	// outgrows stage 0's.
	stage0 := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: "bench.pulse"},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat0"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}
	}
	chain := func() *types.ChainRequest {
		return &types.ChainRequest{Cohort: &types.Cohort{Filename: "bench.pulse"}, Stages: []*types.ChainStage{
			{Name: "by_cat", Request: stage0()},
			{Name: "by_range", Request: &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "s", Interval: 1}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "s", Label: "t"},
					{Type: types.AGG_COUNT, Field: "s", Label: "n"},
					{Type: types.AGG_MAX, Field: "s", Label: "x"},
				}}},
		}}
	}
	svc := New(cfg)
	l := limits.Defaults()
	l.MaxEstimatedMemory = 1
	svc.SetLimits(l)
	var ce *errors.CodedError
	if _, err := svc.Process(context.Background(), stage0()); !stderrors.As(err, &ce) {
		t.Fatalf("stage-0 probe: %v", err)
	}
	stage0Estimate := ce.Details["observed"].(int64)

	l.MaxEstimatedMemory = stage0Estimate
	svc.SetLimits(l)
	_, err = svc.ProcessChain(context.Background(), chain())
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED at stage 1", err)
	}
	if ce.Details["stage"] != 1 || ce.Details["limit"] != "max_estimated_memory" {
		t.Fatalf("details = %v, want stage 1 max_estimated_memory", ce.Details)
	}
	if obs := ce.Details["observed"].(int64); obs <= stage0Estimate {
		t.Fatalf("stage 1 observed %d, not above stage 0's %d", obs, stage0Estimate)
	}
	l.MaxEstimatedMemory = ce.Details["observed"].(int64)
	svc.SetLimits(l)
	if _, err := svc.ProcessChain(context.Background(), chain()); err != nil {
		t.Fatalf("at stage 1's estimate: %v", err)
	}
}

// TestMaxEstimatedMemory_ChainStageComponentsOff (U51 E5-S7): a chain
// stage after the first is judged under the instance's components
// default too — with Options.DisableComponents the stage's pre-flight
// charges the components-off output, exactly descx.EstimateMemory's
// figure on the stage's input rows.
func TestMaxEstimatedMemory_ChainStageComponentsOff(t *testing.T) {
	mem := afero.NewMemMapFs()
	makeSyntheticCohort(t, mem, "bench.pulse")
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	chain := func() *types.ChainRequest {
		return &types.ChainRequest{Cohort: &types.Cohort{Filename: "bench.pulse"}, Stages: []*types.ChainStage{
			{Name: "by_cat", Request: &types.Request{Cohort: &types.Cohort{Filename: "bench.pulse"},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cat0"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}}}},
			{Name: "by_range", Request: &types.Request{
				Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "s", Interval: 1}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "s", Label: "t"},
					{Type: types.AGG_MAX, Field: "s", Label: "x"},
				}}},
		}}
	}
	// stage1Observed is stage 1's refused estimate: a budget just under
	// it admits stage 0 (16 rows) and refuses stage 1.
	stage1Observed := func(off bool) int64 {
		t.Helper()
		svc := New(cfg)
		svc.SetDisableComponents(off)
		var last int64
		for budget := int64(1); ; {
			l := limits.Defaults()
			l.MaxEstimatedMemory = budget
			svc.SetLimits(l)
			_, err := svc.ProcessChain(context.Background(), chain())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("off=%v budget %d: %v", off, budget, err)
			}
			obs := ce.Details["observed"].(int64)
			if ce.Details["stage"] == 1 {
				return obs
			}
			if obs == last {
				t.Fatalf("off=%v: stage 0 refused at its own estimate %d", off, obs)
			}
			last, budget = obs, obs
		}
	}
	on, off := stage1Observed(false), stage1Observed(true)
	// Stage 1 runs over stage 0's 16 rows: 16 buckets of two slots, each
	// charged 1024 + 2 x 576 on and 512 + 2 x 96 off.
	if d, want := on-off, int64(16*((1024+2*576)-(512+2*96))); d != want {
		t.Fatalf("components off lowers stage 1's estimate by %d, want %d (16 buckets' components share)", d, want)
	}
}
