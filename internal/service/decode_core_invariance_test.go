package service

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/testutil/invariance"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// decode_core_invariance_test.go is the parallel-decode core family on
// the shared worker-invariance harness: over a single file above the
// parallel-decode threshold (mmap'd off a real file — MemMapFs bails to
// serial), DecodeWorkers 2/3/7/8 answer the same whole response
// (LayoutFree, bit-exact) as the serial read, each cell on the
// parallel-decode arm, for the engine's plain requests — order-free
// scalars (the binned reproducible sum included), a GROUP_CATEGORY sum,
// the grouped holes the shared shard / decode tail once had (Sort,
// set groupers, a tally attribute), a buffered crosstab's parallel
// record materialisation, and the per-worker filter precompute over a
// parent-grouped (0x02) cohort. Reducer cells (svcInvReducer) run the
// parallel reducer itself on one worker too, and below the threshold
// for the bound-fit and coef() requests, whose 512-row fixture the
// Process dispatch would read serially.

// decodeCoreEquivCohort writes buildParallelEquivCohort's rows on disk.
func decodeCoreEquivCohort(t *testing.T, rows int) svcInvCohort {
	t.Helper()
	cfg, osFs, dir := svcInvDisk(t)
	schema, payload := buildParallelEquivCohort(t, rows)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	buf.Write(payload)
	path := filepath.Join(dir, "decode_core.pulse")
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return svcInvCohort{cfg, path}
}

// decodeCoreFamily runs mk on the one big-file cohort c (svcInvReducer
// cells on the reducer, under ctx when non-nil); check guards the
// reference.
func decodeCoreFamily(name string, ctx func() context.Context, c svcInvCohort, groups []invariance.Group, mk func() *types.Request, setup func(*Service, invariance.Knobs), check func(t *testing.T, resp *types.Response)) invariance.Family[*types.Response] {
	if ctx == nil {
		ctx = context.Background
	}
	return invariance.Family[*types.Response]{
		Name:   name,
		Groups: groups,
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			if k.Mode == svcInvReducer {
				return svcInvReduce(t, ctx(), c, k, mk())
			}
			var s func(*Service)
			if setup != nil {
				s = func(svc *Service) { setup(svc, k) }
			}
			return svcInvProcessIn(t, ctx(), c, k, mk(), s)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) { check(t, resp) },
	}
}

// decodeCoreReducerGroup compares the reducer on 1/2/4/7 workers over
// layout against the serial Process read.
func decodeCoreReducerGroup(layout invariance.Layout) invariance.Group {
	return invariance.Group{Name: "reducer", Ref: invariance.Cell{Knobs: invariance.Knobs{Layout: layout, DecodeWorkers: 1, ShardWorkers: 1}}, Cells: svcInvReducerCells(layout)}
}

// TestDecodeCoreInvariance_Mergeable: COUNT / SUM / MIN / MAX of an
// f64 column ungrouped, and COUNT / SUM per GROUP_CATEGORY bucket (the
// per-worker partials union their buckets in worker order), on every
// DecodeWorkers count and on the reducer itself.
func TestDecodeCoreInvariance_Mergeable(t *testing.T) {
	t.Parallel()

	const rows = parallelDecodeRecordThreshold + 4096
	c := decodeCoreEquivCohort(t, rows)
	groups := []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile), decodeCoreReducerGroup(invariance.BigFile)}
	scalar := func() *types.Request {
		return &types.Request{Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
			{Type: types.AGG_SUM, Field: "weight", Label: "total"},
			{Type: types.AGG_MIN, Field: "weight", Label: "lo"},
			{Type: types.AGG_MAX, Field: "weight", Label: "hi"},
		}}
	}
	invariance.Run(t, decodeCoreFamily("scalar", nil, c, groups, scalar, nil, func(t *testing.T, resp *types.Response) {
		if len(resp.Data) != 1 || resp.Data[0]["n"] != float64(rows) || resp.Metadata == nil || resp.Metadata.TotalRows != rows {
			t.Fatalf("want one row counting %d records, got %v (metadata %+v)", rows, resp.Data, resp.Metadata)
		}
	}))
	grouped := func() *types.Request {
		return &types.Request{
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
				{Type: types.AGG_SUM, Field: "weight", Label: "total"},
			},
		}
	}
	invariance.Run(t, decodeCoreFamily("grouped", nil, c, groups, grouped, nil, func(t *testing.T, resp *types.Response) {
		if len(resp.Data) != 8 {
			t.Fatalf("want one row per brand (8), got %d", len(resp.Data))
		}
	}))
}

// TestDecodeCoreInvariance_Crosstab: the fused crosstab arm ignores
// DecodeWorkers, so the buffered crosstab (fusion off) is the arm whose
// records the parallel decode materialises; under every worker count it
// reproduces the serial fused answer, matrix and margins included.
func TestDecodeCoreInvariance_Crosstab(t *testing.T) {
	t.Parallel()

	c := decodeCoreEquivCohort(t, parallelDecodeRecordThreshold+1024)
	decode := invariance.DecodeGroup("decode", invariance.BigFile)
	for i := range decode.Cells {
		decode.Cells[i].Mode = invariance.Buffered
	}
	decode.Cells = append(decode.Cells,
		invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 1, ShardWorkers: 1, Mode: invariance.Buffered}, Arm: observe.ArmBuffered},
		invariance.Cell{Knobs: invariance.Knobs{Layout: invariance.BigFile, DecodeWorkers: 4, ShardWorkers: 1}, Arm: observe.ArmFusedCrosstab})
	decode.Ref.Arm = observe.ArmFusedCrosstab
	mk := func() *types.Request {
		return &types.Request{Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "weight", Label: "sum_weight"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		}}
	}
	fusion := func(svc *Service, k invariance.Knobs) { svc.SetDisableCrosstabFusion(k.Mode == invariance.Buffered) }
	invariance.Run(t, decodeCoreFamily("crosstab", nil, c, []invariance.Group{decode}, mk, fusion, func(t *testing.T, resp *types.Response) {
		if resp.Crosstab == nil || resp.Crosstab.Matrix == nil || len(resp.Crosstab.Matrix.Cells) != 8 {
			t.Fatalf("want an 8-row crosstab matrix, got %+v", resp.Crosstab)
		}
	}))
}

// TestDecodeCoreInvariance_GroupedAnswers: the decode arm shares
// shardPartial / foldGroupedRow / finalizeMergedPartial with the shard
// reducer, so it once had the same grouped holes (no groupers block,
// Sort and include order dropped, GROUP_SET_PER_ELEMENT refused, a
// tally attribute's counts unfolded); each request answers the serial
// whole document.
func TestDecodeCoreInvariance_GroupedAnswers(t *testing.T) {
	t.Parallel()

	const rows = parallelDecodeRecordThreshold + 4096
	schema := componentsParitySchema()
	recs, nullAt := componentsParityRows(rows, 0)
	cfg, osFs, dir := svcInvDisk(t)
	path := filepath.Join(dir, "grouped_parity.pulse")
	if err := afero.WriteFile(osFs, path, writeNullablePulse(t, schema, recs, nullAt), 0o644); err != nil {
		t.Fatal(err)
	}
	c := svcInvCohort{cfg, path}
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
		}
	}
	reqs := []struct {
		name string
		mk   func() *types.Request
	}{
		{"range_sorted", func() *types.Request {
			return &types.Request{Aggregations: aggs(),
				Groups: []*types.Group{{Type: types.GROUP_RANGE, Field: "score", Interval: 20000}},
				Sort:   []types.OrderKey{{Field: "n", Desc: true}}}
		}},
		{"set_value", func() *types.Request {
			return &types.Request{Aggregations: aggs(), Groups: []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "picks"}}}
		}},
		{"set_per_element", func() *types.Request {
			return &types.Request{Aggregations: aggs(), Groups: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "picks"}}}
		}},
		// A tally attribute's per-worker counts fold (U51 E3-S2).
		{"scale_score_set_value", func() *types.Request {
			return &types.Request{
				Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Fields: []string{"score", "id"}, Label: "sc",
					Params: json.RawMessage(`{"scale_min":0,"scale_max":90000}`)}},
				Aggregations: append(aggs(), &types.Aggregation{Type: types.AGG_SUM, Field: "sc", Label: "sum_sc"}),
				Groups:       []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "picks"}}}
		}},
	}
	for _, r := range reqs {
		if req := r.mk(); !processing.CanMergeRequest(req, schema) {
			t.Fatalf("%s: request is not mergeable; the parallel decode reducer would never engage", r.name)
		}
		invariance.Run(t, decodeCoreFamily(r.name, nil, c, []invariance.Group{invariance.DecodeGroup("decode", invariance.BigFile)}, r.mk, nil, func(t *testing.T, resp *types.Response) {
			if resp.Components == nil || len(resp.Components.Groupers) != 1 {
				t.Fatalf("no groupers block: %+v", resp.Components)
			}
			if ac := resp.Components.Aggregations; len(ac) < 2 || len(ac[0].Groups) != len(resp.Data) || len(resp.Data) == 0 {
				t.Fatalf("no per-group aggregation components")
			}
			if r.name == "scale_score_set_value" {
				if ac := resp.Components.Attributes; len(ac) != 1 || ac[0].Operator["n_out_of_range"] == 0 || len(resp.Warnings) == 0 {
					t.Fatalf("degenerate attribute tally %+v", ac)
				}
			}
		}))
	}
}

// decodeCoreGroupedLayout is the parent-grouped (0x02) twin of the
// stride cohort, above the threshold.
const decodeCoreGroupedLayout invariance.Layout = "big_file_grouped"

// TestDecodeCoreInvariance_FilterPrecompute (E5-S1): the parallel
// buffered arm builds one FilterFunc set per worker, each with its own
// verdict table over the shared group dictionary; filterer components
// included, the parent-grouped cohort answers the 0x01 cohort's serial
// read at every worker count. The per-worker evaluation bound and the
// precompute-off twin stay in TestFilterPrecompute_ParallelDecode.
func TestDecodeCoreInvariance_FilterPrecompute(t *testing.T) {
	t.Parallel()

	cfg, osFs, dir := svcInvDisk(t)
	cohorts := strideParallelCohorts(t)
	paths := map[invariance.Layout]string{invariance.BigFile: filepath.Join(dir, "flat.pulse"), decodeCoreGroupedLayout: filepath.Join(dir, "grouped.pulse")}
	if err := afero.WriteFile(osFs, paths[invariance.BigFile], cohorts["flat"], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(osFs, paths[decodeCoreGroupedLayout], cohorts["grouped"], 0o644); err != nil {
		t.Fatal(err)
	}
	grouped := invariance.DecodeGroup("grouped", invariance.BigFile)
	for i := range grouped.Cells {
		grouped.Cells[i].Layout = decodeCoreGroupedLayout
	}
	grouped.Cells = append(grouped.Cells, invariance.Cell{Knobs: invariance.Knobs{Layout: decodeCoreGroupedLayout, DecodeWorkers: 1, ShardWorkers: 1}})
	mk := func() *types.Request {
		return &types.Request{
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "west"}},
				{Type: types.FILTER_RANGE, Field: "weight", Values: []string{"12", "100"}},
				{Type: types.FILTER_INCLUDE, Field: "src", Values: []string{"batch-a"}},
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}, {Type: types.AGG_SUM, Field: "amount", Label: "amount"}},
		}
	}
	invariance.Run(t, invariance.Family[*types.Response]{
		Name:   "filter_precompute",
		Groups: []invariance.Group{invariance.DecodeGroup("flat", invariance.BigFile), grouped},
		Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
			return svcInvProcess(t, svcInvCohort{cfg, paths[k.Layout]}, k, mk(), nil)
		},
		Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
			if resp.Components == nil || len(resp.Components.Filterers) != 3 || len(resp.Data) != 2 {
				t.Fatalf("want two kept regions and three filterer components, got %v / %+v", resp.Data, resp.Components)
			}
		},
	})
}

// decodeCoreXY is writeParallelXYCohort's 512-row file as a cohort.
func decodeCoreXY(t *testing.T, name string) svcInvCohort {
	t.Helper()
	path, cfg := writeParallelXYCohort(t, name)
	return svcInvCohort{cfg, path}
}

// TestDecodeCoreInvariance_FitBound (U30 E2-S5): a fitted vector
// attribute bound to a prebuilt fit is row-local and mergeable, so the
// parallel reducer runs it — its vector input resolved like every other
// arm, a bound fit's warning raised ONCE (not once per worker), and a
// Compose capture offered once; every worker count equals the serial
// Process read.
func TestDecodeCoreInvariance_FitBound(t *testing.T) {
	t.Parallel()

	c := decodeCoreXY(t, "fit_bound_parallel.pulse")
	fit := func(name string, n int64) *model.VectorFit {
		return &model.VectorFit{Name: name, Labels: []string{"x", "y"}, Members: []string{"x", "y"},
			Basis: model.VectorBasisUnweighted, N: n, NMass: n, SumWeights: float64(n), SumWeightsSq: float64(n),
			Mean: []float64{50, 31}, M2: [][]float64{{8000, 3500}, {3500, 3000}}}
	}
	set := &model.Set{}
	for _, vf := range []*model.VectorFit{fit("pop", 100), fit("tiny", 2)} {
		if err := set.AddVector(vf); err != nil {
			t.Fatal(err)
		}
	}
	for _, pop := range []string{"pop", "tiny"} {
		for _, viaVector := range []bool{false, true} {
			mk := func() *types.Request {
				attr := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"x", "y"}, Params: json.RawMessage(`{"fit":"` + pop + `"}`)}
				r := &types.Request{
					Attributes: []*types.Attribute{attr},
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "d", Label: "n"},
						{Type: types.AGG_MIN, Field: "d", Label: "lo"},
						{Type: types.AGG_MAX, Field: "d", Label: "hi"},
					},
				}
				if viaVector {
					r.Vectors = []types.VectorSpec{{Name: "battery", Fields: []string{"x", "y"}}}
					attr.Fields, attr.Vector = nil, "battery"
				}
				return r
			}
			name := pop
			if viaVector {
				name += "_vector"
			}
			f := invariance.Family[*types.Response]{
				Name:   name,
				Groups: []invariance.Group{decodeCoreReducerGroup(invariance.File)},
				Run: func(t *testing.T, k invariance.Knobs) invariance.Result[*types.Response] {
					if k.Mode != svcInvReducer {
						return svcInvProcessIn(t, withModelsBound(context.Background(), set), c, k, mk(), nil)
					}
					capture := &model.VectorCapture{}
					res := svcInvReduce(t, withModelsBound(context.Background(), set.WithCapture(capture)), c, k, mk())
					if n := len(capture.Fits()); n != 1 {
						t.Fatalf("%s: the battery offered %d states, want 1", k, n)
					}
					return res
				},
				Check: func(t *testing.T, _ invariance.Knobs, resp *types.Response) {
					if len(resp.Data) != 1 {
						t.Fatalf("want one row, got %v", resp.Data)
					}
					if pop == "tiny" && (len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N)) {
						t.Fatalf("tiny fit: warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", resp.Warnings)
					}
				},
			}
			invariance.Run(t, f)
		}
	}
}

// TestDecodeCoreInvariance_CoefFilter (#394): a FILTER_EXPRESSION
// reading a bound regression through coef() compiles against the
// request's bound-model set on every parallel-decode worker, so each
// worker count keeps exactly the rows the serial read keeps.
func TestDecodeCoreInvariance_CoefFilter(t *testing.T) {
	t.Parallel()

	c := decodeCoreXY(t, "coef_filter_parallel.pulse")
	b, err := model.Bind("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 0, "x": 25}})
	if err != nil {
		t.Fatal(err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	mk := func() *types.Request {
		return &types.Request{
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > coef("m", "x") * 2`}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_SUM, Field: "y", Label: "sum_y"},
			},
		}
	}
	ctx := func() context.Context { return withModelsBound(context.Background(), set) }
	invariance.Run(t, decodeCoreFamily("coef_filter", ctx, c, []invariance.Group{decodeCoreReducerGroup(invariance.File)}, mk, nil, func(t *testing.T, resp *types.Response) {
		if len(resp.Data) != 1 {
			t.Fatalf("want one row, got %v", resp.Data)
		}
		if n := mustJSON(t, resp.Data[0]["n"]); n == "0" || n == "512" {
			t.Fatalf("vacuous fixture: the coef() filter keeps %v of 512 rows", resp.Data[0]["n"])
		}
	}))
}
