package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// bit_exact_invariance_test.go is the bit-exactness family (U51 E2-S3,
// #357, FR-20 — see bit_exact_test.go for the fixture) on the shared
// worker-invariance harness: over non-dyadic, mixed-magnitude rows,
// every reproducible-sum shape answers the same whole response
// (LayoutFree), bitwise — Welford figures (m2_weighted,
// weighted_variance) lifted out and held within 1e-12, by contract:
//
//   - archive (12,000 rows): the nine-shard hash-scattered archive (its rows in
//     ANOTHER order than the single file, so even its serial scan folds
//     differently) under ShardWorkers 1/2/3/8 and DecodeWorkers 7 ≡ the
//     single file, plus the forced buffered arm (an AGG_MEDIAN beside the
//     slots, stripped again; on a crosstab, fusion off) on both;
//   - decode (heavy, bit_exact_invariance_heavy_test.go): DecodeWorkers
//     2/3/7/8 over the 120,000-record file ≡ serial;
//
// for the binned sums ungrouped and grouped (AGG_SUM / AVERAGE / RATIO,
// the weighted sums, AGG_WEIGHTED_MEAN, the sum_weights / n_eff floor,
// AGG_FREQUENCY's Σw, AGG_SET_FREQUENCY / CARDINALITY_SUM / AVG,
// AGG_DISTINCT_SUM), GROUP_ROUNDED alone and beside a categorical
// grouper, ATTR_FORMULA total() — two-phase on the parallel reducers,
// plain, weighted, and a filtered share over a label beside a matrix
// (matrices fold by merge-block position, so they compare within a
// cohort only: the decode group and an archive-only group) — and the
// grouped / multi-layer total() the merge gate keeps sequential (every
// cell on its serial arm), and a crosstab cell (AGG_SUM, a weighted
// AGG_AVERAGE) on the fused and the buffered crosstab arm (the crosstab
// path reads an archive serially, never on the shard reducer).

// bitInvKeepMatrices marks a cell whose answer keeps Response.Matrices
// (compared within one cohort only).
const bitInvKeepMatrices invariance.Mode = "matrices"

// bitInvMedian is the forced buffered arm's slot (stripped again).
const bitInvMedian = "inv_median"

// bitInvAnswer is a run's response with the Welford keys lifted out.
type bitInvAnswer struct {
	Resp    *types.Response
	Welford map[string]float64
}

// bitInvRule compares the response bitwise and the Welford keys within
// bitExactWelfordTol.
func bitInvRule(got, want any) []string {
	g, w := got.(bitInvAnswer), want.(bitInvAnswer)
	return append(invariance.BitExact(g.Resp, w.Resp), invariance.Within(bitExactWelfordTol, bitExactWelfordTol)(g.Welford, w.Welford)...)
}

// bitInvShape is one request of the family.
type bitInvShape struct {
	name string
	// serial is the arm every cell takes when the merge gate keeps the
	// request sequential ("" = a merging request).
	serial   observe.Arm
	crosstab bool
	// matrices: the answer carries Response.Matrices.
	matrices bool
	build    func(cohort string) *types.Request
	check    func(t *testing.T, resp *types.Response)
}

// bitInvNonIntegral fails when Data[0][key] is zero or integral (the
// fixture would not be mixed-magnitude).
func bitInvNonIntegral(keys ...string) func(t *testing.T, resp *types.Response) {
	return func(t *testing.T, resp *types.Response) {
		t.Helper()
		if len(resp.Data) == 0 {
			t.Fatal("no rows")
		}
		for _, k := range keys {
			if v, _ := resp.Data[0][k].(float64); v == 0 || v == math.Trunc(v) {
				t.Fatalf("%s = %v is zero or integral: the fixture is not mixed-magnitude", k, v)
			}
		}
	}
}

// bitInvSums is every reproducible-sum slot (bitExactAggs) and the
// weighted counting aggregators beside them.
func bitInvSums() []*types.Aggregation {
	freq, _ := json.Marshal(map[string]string{"value": "east"})
	distinct, _ := json.Marshal(map[string]string{"distinct_by": "rid"})
	return append(bitExactAggs(),
		&types.Aggregation{Type: types.AGG_FREQUENCY, Field: "region", Label: "f", Params: freq, Weight: bitExactPW()},
		&types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "sf", Weight: bitExactPW()},
		&types.Aggregation{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "cs", Weight: bitExactPW()},
		&types.Aggregation{Type: types.AGG_SET_CARDINALITY_AVG, Field: "tags", Label: "ca", Weight: bitExactPW()},
		&types.Aggregation{Type: types.AGG_DISTINCT_SUM, Field: "rv", Label: "ds", Params: distinct})
}

// bitInvRounded is a GROUP_ROUNDED shape over groups; noWelford keeps
// the binned sums only (width-1000 buckets of values up to 1e6 are
// ill-conditioned for the Welford moments, whose mean dwarfs their
// spread: they merge within ULP-scaled cancellation, not 1e-12).
func bitInvRounded(name string, groups []*types.Group, noWelford bool) bitInvShape {
	return bitInvShape{name: name, build: func(cohort string) *types.Request {
		aggs := bitExactAggs()
		if noWelford {
			kept := aggs[:0]
			for _, a := range aggs {
				if a.Label != "wa" && a.Label != "wm" {
					kept = append(kept, a)
				}
			}
			aggs = kept
		}
		gs := make([]*types.Group, len(groups))
		for i, g := range groups {
			c := *g
			gs[i] = &c
		}
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Groups: gs, Aggregations: aggs}
	}, check: func(t *testing.T, resp *types.Response) {
		if len(resp.Data) < 10 {
			t.Fatalf("%d rows: the fixture is degenerate", len(resp.Data))
		}
		for _, g := range resp.Components.Groupers {
			if _, ok := g.Operator["precision"]; ok { // GROUP_ROUNDED's own key
				if b, _ := g.Operator["buckets"].([]map[string]any); len(b) < 10 {
					t.Fatalf("GROUP_ROUNDED components carry %d buckets", len(b))
				}
				return
			}
		}
		t.Fatal("no GROUP_ROUNDED components")
	}}
}

// bitInvCrosstab is a crosstab over region × a fixed-width range of y.
func bitInvCrosstab(name string, cell types.Aggregation) bitInvShape {
	return bitInvShape{name: name, crosstab: true, build: func(cohort string) *types.Request {
		c := cell
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "y", Interval: 25}},
			Cell:    &c,
		}}
	}, check: func(t *testing.T, resp *types.Response) {
		if resp.Crosstab == nil || resp.Crosstab.Matrix == nil || len(resp.Crosstab.Matrix.Cells) < 5 {
			t.Fatal("no crosstab: the case proves nothing")
		}
	}}
}

func bitInvShapes() []bitInvShape {
	total := func(label, expr string, w *types.SlotWeight) *types.Attribute {
		a := &types.Attribute{Type: types.ATTR_FORMULA, Expression: expr, Label: label}
		if w != nil {
			a.Weight = *w
		}
		return a
	}
	fw := types.SlotWeightOf(types.WeightSpec{Field: "fw", Kind: types.WeightKindFrequency})
	return []bitInvShape{
		{name: "sums", build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: bitInvSums()}
		}, check: func(t *testing.T, resp *types.Response) {
			bitInvNonIntegral("s", "cs", "ds")(t, resp)
			for _, e := range resp.Components.Aggregations {
				if e.Label == "ws" && (e.SumWeights == nil || e.NEff == nil) {
					t.Fatalf("weighted slot %s carries no sum_weights / n_eff floor", e.Label)
				}
			}
		}},
		{name: "sums_grouped", build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: bitInvSums(),
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}}
		}, check: bitInvNonIntegral("s", "cs", "ds")},
		bitInvRounded("rounded_y", []*types.Group{{Type: types.GROUP_ROUNDED, Field: "y", Interval: 2.5}}, false),
		bitInvRounded("rounded_x", []*types.Group{{Type: types.GROUP_ROUNDED, Field: "x", Interval: 1000}}, true),
		bitInvRounded("rounded_region_y", []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_ROUNDED, Field: "y", Interval: 7.3}}, false),
		{name: "formula_total", build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes: []*types.Attribute{total("tot", "total(x)", nil), total("wtot", "total(x)", &fw)},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_MAX, Field: "tot", Label: "max"},
					{Type: types.AGG_MAX, Field: "wtot", Label: "wmax"},
				}}
		}, check: bitInvNonIntegral("max", "wmax")},
		{name: "formula_total_share", matrices: true, build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "y > 20"}},
				Attributes: []*types.Attribute{
					total("xy", "x == nil ? nil : x * y", nil),
					total("share", "xy == nil ? nil : xy / total(xy) + total(y)", nil),
				},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "share", Label: "s"},
					{Type: types.AGG_AVERAGE, Field: "share", Label: "a"},
					{Type: types.AGG_MAX, Field: "share", Label: "m"},
					{Type: types.AGG_COUNT, Field: "xy", Label: "n"},
				},
				// A matrix beside the total() formula rides phase 2's
				// blocked merge (the serial arm runs it buffered).
				Matrices: []types.MatrixSpec{{Type: types.MAT_COVARIANCE, Name: "c", Fields: []string{"x", "y"}}}}
		}, check: bitInvNonIntegral("s")},
		{name: "formula_total_grouped", serial: observe.ArmBuffered, build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{total("sh", "(x ?? 0) / total(x)", nil)},
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "sh", Label: "s"}}}
		}, check: bitInvNonIntegral("s")},
		{name: "formula_total_multilayer", serial: observe.ArmStreaming, build: func(cohort string) *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{total("t1", "total(x)", nil), total("t2", "(x ?? 0) / total(x - t1 / 7)", nil)},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "t2", Label: "s"}}}
		}, check: bitInvNonIntegral("s")},
		bitInvCrosstab("crosstab_sum", types.Aggregation{Type: types.AGG_SUM, Field: "x", Label: "s"}),
		bitInvCrosstab("crosstab_weighted_average", types.Aggregation{Type: types.AGG_AVERAGE, Field: "x", Label: "wa", Weight: bitExactPW()}),
	}
}

// bitInvSmoke run in the fast tier: the grouped sums (with the forced
// buffered arm), the share-over-label total() and the weighted crosstab.
var bitInvSmoke = map[string]bool{"sums_grouped": true, "formula_total_share": true, "crosstab_weighted_average": true}

// bitInvSmokeRows is the fast tier's cohort (the heavy one is
// bitExactRows, past the parallel-decode threshold).
const bitInvSmokeRows = 12_000

// newBitInvFixture writes the single file and the nine-shard archive
// of bitInvSmokeRows rows (the archive groups: their bits do not need
// the decode threshold) and — heavy — the bitExactRows single file, the
// BigFile layout of the decode group.
func newBitInvFixture(t *testing.T, heavy bool) map[invariance.Layout]invCohort {
	t.Helper()
	small := afero.NewBasePathFs(afero.NewOsFs(), bitExactCohorts(t, bitInvSmokeRows, true))
	out := map[invariance.Layout]invCohort{invariance.File: {small, "flat.pulse"}, invariance.Archive: {small, "arch.pulse"}}
	if heavy {
		out[invariance.BigFile] = invCohort{afero.NewBasePathFs(afero.NewOsFs(), bitExactCohorts(t, bitExactRows, false)), "flat.pulse"}
	}
	return out
}

// bitInvCell is a cell of knobs on arm.
func bitInvCell(l invariance.Layout, dw, sw int, m invariance.Mode, arm observe.Arm) invariance.Cell {
	return invariance.Cell{Knobs: invariance.Knobs{Layout: l, DecodeWorkers: dw, ShardWorkers: sw, Mode: m}, Arm: arm}
}

// bitInvGroups are shape s's groups in the tier.
func bitInvGroups(s bitInvShape, heavy bool) []invariance.Group {
	A, F, B := invariance.Archive, invariance.File, invariance.BigFile
	ref := bitInvCell(F, 1, 1, invariance.Default, "")
	switch {
	case s.crosstab:
		fused, buf := observe.ArmFusedCrosstab, observe.ArmBuffered
		if heavy {
			// The fused arm never fans out: an odd and a wide count pin
			// that; the buffered arm fans out under every count.
			g := invariance.Group{Name: "decode", Ref: bitInvCell(B, 1, 1, invariance.Default, fused),
				Cells: []invariance.Cell{bitInvCell(B, 3, 1, invariance.Default, fused), bitInvCell(B, 8, 1, invariance.Default, fused)}}
			for _, dw := range invariance.BigFileDecoders {
				g.Cells = append(g.Cells, bitInvCell(B, dw, 1, invariance.Buffered, observe.ArmParallelDecode))
			}
			return []invariance.Group{g}
		}
		g := invariance.Group{Name: "archive", Ref: bitInvCell(F, 1, 1, invariance.Default, fused)}
		for _, sw := range invariance.ArchiveShardWorkers {
			g.Cells = append(g.Cells, bitInvCell(A, 1, sw, invariance.Default, fused))
		}
		g.Cells = append(g.Cells, bitInvCell(A, 7, 3, invariance.Default, fused), bitInvCell(F, 1, 1, invariance.Buffered, buf), bitInvCell(A, 1, 8, invariance.Buffered, buf))
		return []invariance.Group{g}
	case s.serial != "":
		if heavy {
			return []invariance.Group{invSerialDecodeGroup("decode", B, s.serial)}
		}
		g := invariance.ArchiveGroup()
		for i := range g.Cells {
			g.Cells[i].Arm = s.serial
		}
		return []invariance.Group{g}
	case heavy:
		g := invariance.DecodeGroup("decode", B)
		if s.matrices {
			g.Ref.Mode = bitInvKeepMatrices
			for i := range g.Cells {
				g.Cells[i].Mode = bitInvKeepMatrices
			}
		}
		return []invariance.Group{g}
	}
	g := invariance.ArchiveGroup()
	g.Ref = ref
	if !s.matrices {
		g.Cells = append(g.Cells, invariance.BufferedCell(), bitInvCell(A, 1, 1, invariance.Buffered, observe.ArmBuffered))
		return []invariance.Group{g}
	}
	// The matrices compare within the archive: its serial scan against
	// every ShardWorkers count.
	m := invariance.Group{Name: "archive_matrices", Ref: bitInvCell(A, 1, 1, bitInvKeepMatrices, "")}
	for _, sw := range invariance.ArchiveShardWorkers {
		m.Cells = append(m.Cells, bitInvCell(A, 1, sw, bitInvKeepMatrices, observe.ArmShardParallel))
	}
	m.Cells = append(m.Cells, bitInvCell(A, 7, 3, bitInvKeepMatrices, observe.ArmShardParallel))
	return []invariance.Group{g, m}
}

// bitInvStripMedian removes the buffered arm's AGG_MEDIAN column (and a
// row it alone filled) and its Components entry.
func bitInvStripMedian(t *testing.T, resp *types.Response) {
	t.Helper()
	var rows []map[string]any
	for _, row := range resp.Data {
		if _, ok := row[bitInvMedian]; !ok {
			t.Fatalf("buffered row lacks %s: %v", bitInvMedian, row)
		}
		delete(row, bitInvMedian)
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	resp.Data = rows
	if c := resp.Components; c != nil {
		var aggs []types.AggregationComponents
		for _, a := range c.Aggregations {
			if a.Label != bitInvMedian {
				aggs = append(aggs, a)
			}
		}
		c.Aggregations = aggs
	}
}

// bitInvFamily is shape s over its groups in the tier.
func bitInvFamily(cohorts map[invariance.Layout]invCohort, s bitInvShape, heavy bool) invariance.Family[bitInvAnswer] {
	return invariance.Family[bitInvAnswer]{
		Name:    s.name,
		Groups:  bitInvGroups(s, heavy),
		Compare: bitInvRule,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[bitInvAnswer] {
			co, ok := cohorts[k.Layout]
			if !ok {
				t.Fatalf("layout %s not built in this tier", k.Layout)
			}
			opts := pulse.Options{}
			req := s.build(co.path)
			if k.Mode == invariance.Buffered {
				if s.crosstab {
					opts.DisableCrosstabFusion = true
				} else {
					req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "x", Label: bitInvMedian})
				}
			}
			p, arm := invEngine(t, opts, co, k)
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("%s: Process: %v", k, err)
			}
			if k.Mode == invariance.Buffered && !s.crosstab {
				bitInvStripMedian(t, resp)
			}
			if s.matrices && k.Mode != bitInvKeepMatrices {
				resp.Matrices = nil
				if resp.Components != nil {
					resp.Components.Matrices = nil
				}
			}
			welford := liftWelford(resp)
			return invariance.Result[bitInvAnswer]{Answer: bitInvAnswer{invariance.LayoutFree(resp), welford}, Arm: arm()}
		},
		Check: func(t *testing.T, _ invariance.Knobs, a bitInvAnswer) {
			if a.Resp.Components == nil {
				t.Fatal("reference run has no components: the gate proves nothing")
			}
			if s.check != nil {
				s.check(t, a.Resp)
			}
		},
	}
}

// TestBitExact_Archive is the fast-tier smoke of the bit-exactness
// family (bitInvSmoke); TestBitExact_Heavy runs the other shapes' archive
// groups and every shape's decode group over 120,000 rows.
func TestBitExact_Archive(t *testing.T) {
	t.Parallel()

	cohorts := newBitInvFixture(t, false)
	for _, s := range bitInvShapes() {
		if bitInvSmoke[s.name] {
			invariance.Run(t, bitInvFamily(cohorts, s, false))
		}
	}
}
