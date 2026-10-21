package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
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

// TestMemoryEstimate_Calibration measures the peak heap
// (measurePeakHeap: the sampled HeapAlloc high-water mark above a
// post-GC baseline — never B/op) of buildWideCohort runs (95 fields,
// 200,000 records, stride 228 B) on every arm and requires the
// pre-flight's estimate — read back as the `observed` of a refusal at
// MaxEstimatedMemory=1, the exact figure the runtime checks — to be at
// least the measured peak and within the documented factor of it.
// The factors (estimate / peak, Apple M1 Max) are recorded in
// .claude/reference/predict-inspect.md: the full-width decode, the
// streaming arms, the joins and the facet sit at 1.3-2.2x; field
// projection (on by default) and the buffered crosstab's projection
// shrink the real footprint the schema-width model cannot see, to
// 3.4-4.9x.
func TestMemoryEstimate_Calibration(t *testing.T) {
	if testing.Short() {
		t.Skip("200,000-record calibration runs")
	}
	const rows = 200_000
	mem := afero.NewMemMapFs()
	// writeWide writes a buildWideCohort file onto dst; allIssuers
	// selects every issuers member on every row (the full fan-out).
	writeWide := func(dst afero.Fs, path string, fields, n int, allIssuers bool) {
		schema, payload := buildWideCohort(t, fields, n)
		if allIssuers {
			off, stride := schema.Field("issuers").ByteOffset, schema.RecordByteSize()
			for r := 0; r < n; r++ {
				payload[r*stride+off] = 0x3F
			}
		}
		var buf bytes.Buffer
		if err := encoding.WriteHeader(&buf); err != nil {
			t.Fatal(err)
		}
		if err := encoding.WriteSchema(&buf, schema); err != nil {
			t.Fatal(err)
		}
		buf.Write(payload)
		if err := afero.WriteFile(dst, path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeWide(mem, "w.pulse", 95, rows, false)
	writeWide(mem, "s.pulse", 10, 16, false) // a 16-row lookup keyed on brand
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	co := &types.Cohort{Filename: "w.pulse"}
	full := &types.Cohort{Filename: "f.pulse"}
	brand := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}}
	issuers := []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "issuers"}}
	sum := []*types.Aggregation{{Type: types.AGG_SUM, Field: "weight", Label: "s"}}
	median := []*types.Aggregation{{Type: types.AGG_MEDIAN, Field: "weight", Label: "m"}}
	xt := &types.CrosstabSpec{Rows: brand, Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "cardFeeling"}},
		Cell: &types.Aggregation{Type: types.AGG_SUM, Field: "weight", Label: "s"}}
	lookup := []*types.JoinSpec{{Right: "s.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "brand", RightField: "brand"}}}}
	// fanout joins a 16-row left to the 200,000-row cohort as the build
	// side: the join arm's state is the build.
	fanout := []*types.JoinSpec{{Right: "w.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "brand", RightField: "brand"}}}}
	small := &types.Cohort{Filename: "s.pulse"}

	cases := []struct {
		name    string
		req     func() *types.Request
		facet   *types.FacetRequest
		project bool
		fuse    bool
		fanOut  bool // runs on f.pulse, written for the case alone
		// joinFanOut runs on jl.pulse (95 fields x 50,000 records)
		// joined to jr.pulse (64 records: every brand 4 times), written
		// for the case alone; the estimate is the build-side backstop's
		// (#229: left x the measured key multiplicity), read back as
		// the observed of its refusal at the pre-flight's estimate.
		joinFanOut bool
		maxFactor  float64
	}{
		{name: "buffered full width", req: func() *types.Request { return &types.Request{Cohort: co, Aggregations: median} }, maxFactor: 2.5},
		{name: "buffered grouped full width", req: func() *types.Request { return &types.Request{Cohort: co, Groups: brand, Aggregations: median} }, maxFactor: 2.5},
		{name: "buffered projected", req: func() *types.Request { return &types.Request{Cohort: co, Aggregations: median} }, project: true, maxFactor: 6},
		{name: "streaming", req: func() *types.Request { return &types.Request{Cohort: co, Aggregations: sum} }, maxFactor: 2.5},
		{name: "streaming grouped", req: func() *types.Request { return &types.Request{Cohort: co, Groups: brand, Aggregations: sum} }, maxFactor: 2.5},
		{name: "streaming projected", req: func() *types.Request { return &types.Request{Cohort: co, Aggregations: sum} }, project: true, maxFactor: 4.5},
		// U51 E5-S6: GROUP_CATEGORY over the u16 pad_018 is charged its
		// 65,536-key width bound (below the 200,000 records it was
		// charged before); the data holds 4,096 of them, so the bound is
		// loose here, never under.
		{name: "streaming value-width grouped", req: func() *types.Request {
			return &types.Request{Cohort: co, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "pad_018"}}, Aggregations: sum}
		}, maxFactor: 4.5},
		{name: "streaming value state", req: func() *types.Request {
			return &types.Request{Cohort: co, Aggregations: []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "weight", Label: "d"}}}
		}, maxFactor: 2.5},
		{name: "fused crosstab", req: func() *types.Request { return &types.Request{Cohort: co, Crosstab: xt} }, fuse: true, maxFactor: 2.5},
		{name: "buffered crosstab", req: func() *types.Request { return &types.Request{Cohort: co, Crosstab: xt} }, maxFactor: 4.5},
		{name: "matrix", req: func() *types.Request {
			return &types.Request{Cohort: co, Groups: brand, Aggregations: sum,
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"weight", "pad_014", "pad_015"}}}}
		}, maxFactor: 2.5},
		// U35 #437: grouped tier-1 tests (one instance per test per brand,
		// buffered path) — a moments test and a rank test keeping every
		// value.
		{name: "grouped t-test", req: func() *types.Request {
			return &types.Request{Cohort: co, Groups: brand, Aggregations: sum,
				Tests: []*types.Test{{Type: types.TEST_T, Field: "weight"}}}
		}, maxFactor: 2.5},
		{name: "grouped rank test", req: func() *types.Request {
			return &types.Request{Cohort: co, Groups: brand, Aggregations: sum,
				Tests: []*types.Test{{Type: types.TEST_KRUSKAL_WALLIS, Field: "weight", SplitBy: "cardFeeling"}}}
		}, maxFactor: 2.5},
		// A rank-method matrix keeps every row and ranks the columns at
		// finalize (limits.MatrixRowWorkBytes).
		{name: "rank matrix", req: func() *types.Request {
			return &types.Request{Cohort: co, Aggregations: sum,
				Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Name: "r", Fields: []string{"weight", "pad_014", "pad_015"}, Params: []byte(`{"method": "spearman"}`)}}}
		}, maxFactor: 2.5},
		// U35 #267: a GROUP_SET_PER_ELEMENT fan-out over f.pulse, whose
		// rows select all 6 issuers (the worst case the dictionary-size
		// multiplier bounds) — a rank matrix's row store and finalize,
		// at p = 3 and p = 8, and a distinct count's value state, hold a
		// copy per selected issuer.
		{name: "fan-out rank matrix", req: func() *types.Request {
			return &types.Request{Cohort: full, Groups: issuers, Aggregations: sum,
				Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Name: "r", Fields: []string{"weight", "pad_014", "pad_015"}, Params: []byte(`{"method": "spearman"}`)}}}
		}, fanOut: true, maxFactor: 2.5},
		{name: "fan-out wide rank matrix", req: func() *types.Request {
			return &types.Request{Cohort: full, Groups: issuers, Aggregations: sum,
				Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Name: "r", Fields: []string{"weight", "pad_014", "pad_015", "pad_016", "pad_017", "pad_034", "pad_035", "pad_036"}, Params: []byte(`{"method": "spearman"}`)}}}
		}, fanOut: true, maxFactor: 2.5},
		// The value-state model assumes every record a distinct value per
		// bucket; weight repeats, so the bound is loose here exactly as
		// on "streaming value state" (2.13x), six buckets over.
		{name: "fan-out value state", req: func() *types.Request {
			return &types.Request{Cohort: full, Groups: issuers,
				Aggregations: []*types.Aggregation{{Type: types.AGG_DISTINCT_COUNT, Field: "weight", Label: "d"}}}
		}, fanOut: true, maxFactor: 3.5},
		{name: "lookup join", req: func() *types.Request { return &types.Request{Cohort: co, Joins: lookup, Aggregations: sum} }, maxFactor: 2.5},
		{name: "join build side", req: func() *types.Request { return &types.Request{Cohort: small, Joins: fanout, Aggregations: sum} }, maxFactor: 2.5},
		{name: "facet", facet: &types.FacetRequest{Cohort: co, Fields: []string{"brand", "weight"}}, maxFactor: 2.5},
		// A 1:4 join on the buffered arm: 200,000 joined records
		// materialised from a 50,000-record left side.
		{name: "join fan-out", req: func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "jl.pulse"}, Aggregations: median,
				Joins: []*types.JoinSpec{{Right: "jr.pulse", Kind: "inner", As: "r_", On: []types.OnPair{{LeftField: "brand", RightField: "brand"}}}}}
		}, joinFanOut: true, maxFactor: 2.5},
	}
	ctx := context.Background()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := New(cfg)
			if c.fanOut {
				// A fs of its own, so the file is garbage outside this
				// case (a live extra cohort raises every other case's
				// heap goal).
				fm := afero.NewMemMapFs()
				writeWide(fm, "f.pulse", 95, rows, true)
				fcfg, err := fs.New(fs.WithFs(fm))
				if err != nil {
					t.Fatal(err)
				}
				svc = New(fcfg)
			}
			if c.joinFanOut {
				jm := afero.NewMemMapFs()
				writeWide(jm, "jl.pulse", 95, rows/4, false)
				writeWide(jm, "jr.pulse", 10, 64, false)
				jcfg, err := fs.New(fs.WithFs(jm))
				if err != nil {
					t.Fatal(err)
				}
				svc = New(jcfg)
			}
			svc.SetProjectBufferedFields(c.project)
			svc.SetDisableCrosstabFusion(!c.fuse)
			run := func() error {
				if c.facet != nil {
					f := *c.facet
					_, err := svc.FacetSchema(ctx, &f)
					return err
				}
				_, err := svc.Process(ctx, c.req())
				return err
			}
			l := limits.Defaults()
			l.MaxEstimatedMemory = 1
			svc.SetLimits(l)
			var ce *errors.CodedError
			if err := run(); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
				t.Fatalf("estimate probe: %v", err)
			}
			estimate := float64(ce.Details["observed"].(int64))
			if c.joinFanOut {
				l.MaxEstimatedMemory = ce.Details["observed"].(int64)
				svc.SetLimits(l)
				if err := run(); !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED || ce.Details["right_key_multiplicity"] != int64(4) {
					t.Fatalf("backstop probe: %v", err)
				}
				estimate = float64(ce.Details["observed"].(int64))
			}
			svc.SetLimits(limits.Defaults())
			peak, err := measurePeakHeap(run)
			if err != nil {
				t.Fatal(err)
			}
			factor := estimate / peak
			t.Logf("estimate %.1f MB, peak heap %.1f MB, factor %.2f", estimate/1e6, peak/1e6, factor)
			if estimate < peak {
				t.Fatalf("estimate %.0f B is below the measured peak heap %.0f B", estimate, peak)
			}
			if factor > c.maxFactor {
				t.Fatalf("estimate is %.2fx the measured peak heap; documented bound %.1fx", factor, c.maxFactor)
			}
		})
	}
}
