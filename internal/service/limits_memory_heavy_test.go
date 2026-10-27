//go:build heavy

package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

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
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

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
	wideGrid := func(cell *types.Aggregation) *types.CrosstabSpec {
		if cell == nil {
			cell = &types.Aggregation{Type: types.AGG_SUM, Field: "weight", Label: "s"}
		}
		return &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "pad_018"}}, Columns: brand, Cell: cell,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true}}
	}
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
		// U51 E5-S9: a grid of 4,096 x 16 populated cells (pad_018, a u16
		// whose 65,536-key width bounds the rows; the grid itself only by
		// the 200,000 records) by brand, with every margin — the
		// per-measure cell, axis-key and margin charges dominate — and a
		// vector element cell over it. The bounds are loose here (16x the
		// populated rows' keys, 3x the cells), never under; the buffered
		// arm also projects its records (as "buffered projected"). U51
		// E5-S11 adds the dense grid's empty positions — 65,536 x 16
		// charged, 1,048,576 positions less the 200,000-cell bound, at
		// CrosstabGridPositionBytes (~110 MB fused, ~42 MB buffered) —
		// while the run's peak fell (the finalize maps no longer
		// pre-size to the grid), so these factors rose (5.9 / 7.2 / 4.4).
		{name: "fused crosstab wide grid", req: func() *types.Request { return &types.Request{Cohort: co, Crosstab: wideGrid(nil)} }, fuse: true, maxFactor: 7.5},
		{name: "buffered crosstab wide grid", req: func() *types.Request { return &types.Request{Cohort: co, Crosstab: wideGrid(nil)} }, maxFactor: 9.5},
		{name: "fused crosstab vector element", req: func() *types.Request {
			return &types.Request{Cohort: co, Crosstab: wideGrid(&types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"weight", "pad_014", "pad_015"}, Label: "v", Params: []byte(`{"element":"pad_015"}`)})}
		}, fuse: true, maxFactor: 6},
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
	// fanCfg holds f.pulse for the fan-out cases: written once, at the
	// first, and dropped after the last, so the file is garbage outside
	// them (a live extra cohort raises every other case's heap goal) and
	// each fan-out case runs over the same live set as with a file of
	// its own.
	var fanCfg *fs.Config
	lastFanOut := -1
	for i, c := range cases {
		if c.fanOut {
			lastFanOut = i
		}
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := New(cfg)
			if c.fanOut {
				if fanCfg == nil {
					fm := afero.NewMemMapFs()
					writeWide(fm, "f.pulse", 95, rows, true)
					fcfg, err := fs.New(fs.WithFs(fm))
					if err != nil {
						t.Fatal(err)
					}
					fanCfg = fcfg
				}
				svc = New(fanCfg)
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
		if i == lastFanOut {
			fanCfg = nil
		}
	}
}
