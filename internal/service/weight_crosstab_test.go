package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// Weighted crosstabs on the BUFFERED arm (weighting-descriptive E3-S1,
// .claude/reference/weighting.md "Crosstab"):
//
//   - TestWeightCrosstabUnityParity — an all-1.0 weight answers
//     byte-identically to no weight for every weight-aware cell
//     aggregator × weight source, cells, row / column / grand margins
//     and auxiliary margins alike, once the weighted floor keys
//     (pinned to their unity values) are shed;
//   - TestWeightCrosstabFrequencyExpansion — integer weights equal the
//     physically duplicated rows run unweighted, for cells, every
//     margin class and normalization (row at a partial depth, column
//     within a row prefix, total), while CellCounts and margin counts
//     stay the RAW row counts and a `weight: null` auxiliary is the
//     unweighted base;
//   - TestWeightCrosstabProbabilityQuantileScaleInvariance — scaled
//     probability weights leave a weighted median / percentile crosstab
//     unchanged;
//   - TestWeightCrosstabAuxSlotOverride, TestWeightCrosstabInvalidRows,
//     TestWeightCrosstabFusionDecision.
//
// The fused arm's weighted parity is E3-S2's; these tests force the
// buffered arm (crosstabBufferedSteer) and assert it engaged.

// crosstabBufferedSteer is a FILTER_EXPRESSION that keeps every row:
// the fusion rule declines any FILTER_EXPRESSION, so it pins a
// mergeable cell to the buffered arm without changing a figure.
func crosstabBufferedSteer() *types.Filterer {
	return &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "id >= 0"}
}

// weightFloorKeys are the weighted universal-floor keys of a flat
// crosstab component map.
var weightFloorKeys = []string{"sum_weights", "n_eff", "n_weight_invalid"}

// crosstabParityRequest builds the crosstab under test: the cell is
// row.op over its first field, rows by g, columns by id ranges, every
// margin displayed, and two auxiliaries — `base`, an AGG_COUNT opted
// out of any weight (the unweighted base), and `aux_sum`, an AGG_SUM
// that inherits.
func crosstabParityRequest(path string, row parityOp, op types.AggregationType) *types.Request {
	return &types.Request{
		Cohort:    &types.Cohort{Filename: path},
		Filterers: []*types.Filterer{crosstabBufferedSteer()},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "id", Interval: 200}},
			Cell:    &types.Aggregation{Type: op, Field: row.fields[0], Label: "cell", Params: row.params},
			MarginAggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "base", Weight: types.NullSlotWeight()},
				{Type: types.AGG_SUM, Field: "y", Label: "aux_sum"},
			},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
}

// crosstabSources mirror paritySources for the crosstab slots: the
// "slot" source weights the cell and the inheriting auxiliary.
func crosstabSources() []paritySource {
	out := paritySources()
	for i := range out {
		if out[i].name != "slot" {
			continue
		}
		out[i].apply = func(req *types.Request, _ *Service, spec types.WeightSpec) *types.WeightSpec {
			req.Crosstab.Cell.Weight = types.SlotWeightOf(spec)
			for _, a := range req.Crosstab.MarginAggregations {
				if a.Label != "base" {
					a.Weight = types.SlotWeightOf(spec)
				}
			}
			return nil
		}
	}
	return out
}

// runCrosstab runs req on a fresh service over cfg, asserting the
// buffered arm when wantBuffered.
func runCrosstab(t *testing.T, store *parityStore, req *types.Request, src *paritySource, wantBuffered bool) *types.Response {
	t.Helper()
	svc := New(store.mem)
	svc.SetShardWorkers(1)
	svc.SetDecodeWorkers(1)
	var def *types.WeightSpec
	if src != nil {
		def = src.apply(req, svc, types.WeightSpec{Field: "w", Kind: src.kind})
	}
	if wantBuffered {
		if ok, _ := processing.CanFuseCrosstab(processing.StampWeights(req, def), paritySchema(), svc.extensions); ok {
			t.Fatal("the request would take the fused arm; this gate covers the buffered arm")
		}
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

// TestWeightCrosstabUnityParity: unity weights are invisible on the
// buffered crosstab arm.
func TestWeightCrosstabUnityParity(t *testing.T) {
	t.Run("coverage", assertParityCoverage)
	aware := manifestAware()
	store := newParityStore(t, "xt_unity", func(n int) []parityRow { return parityRows(n, unityWeight) })
	path := store.paths[paritySingleFile]
	for _, row := range parityOps {
		t.Run(string(row.op), func(t *testing.T) {
			base := runCrosstab(t, store, crosstabParityRequest(path, row, row.baselineOp()), nil, true)
			assertNoWeightFloor(t, base)
			want := mustMarshal(t, base)
			for _, src := range crosstabSources() {
				t.Run(src.name, func(t *testing.T) {
					got := runCrosstab(t, store, crosstabParityRequest(path, row, row.op), &src, true)
					shedCrosstabUnity(t, got, row, aware, src.kind)
					if g := mustMarshal(t, got); !bytes.Equal(g, want) {
						t.Errorf("unity weight changed the crosstab:\n weighted   %s\n unweighted %s", g, want)
					}
				})
			}
		})
	}
}

// assertNoWeightFloor: an unweighted crosstab carries no weighted key.
func assertNoWeightFloor(t *testing.T, resp *types.Response) {
	t.Helper()
	forEachCrosstabMap(t, resp, func(where, _ string, m map[string]any) {
		for _, k := range weightFloorKeys {
			if _, ok := m[k]; ok {
				t.Errorf("%s: unweighted map carries %s", where, k)
			}
		}
	})
}

// forEachCrosstabMap visits every flat component map of a crosstab
// response: cells, the cell aggregator's three margin classes, and
// every auxiliary figure (label passed; "" for the cell aggregator's).
func forEachCrosstabMap(t *testing.T, resp *types.Response, fn func(where, auxLabel string, m map[string]any)) {
	t.Helper()
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("crosstab response has no components")
	}
	c := resp.Components.Crosstab
	for r, row := range c.CellComponents {
		for col, m := range row {
			if m != nil {
				fn(fmt.Sprintf("cell[%d][%d]", r, col), "", m)
			}
		}
	}
	for i, m := range c.RowMarginComponents {
		fn(fmt.Sprintf("row_margin[%d]", i), "", m)
	}
	for i, m := range c.ColumnMarginComponents {
		fn(fmt.Sprintf("column_margin[%d]", i), "", m)
	}
	if c.GrandTotalComponents != nil {
		fn("grand_total", "", c.GrandTotalComponents)
	}
	aux := func(where string, figs map[string]types.MarginAggregationFigure) {
		for label, f := range figs {
			fn(where+"."+label, label, f.Components)
		}
	}
	for i, figs := range c.RowMarginAggregations {
		aux(fmt.Sprintf("row_aux[%d]", i), figs)
	}
	for i, figs := range c.ColumnMarginAggregations {
		aux(fmt.Sprintf("column_aux[%d]", i), figs)
	}
	aux("grand_aux", c.GrandTotalAggregations)
}

// shedCrosstabUnity asserts every weighted map carries the unity floor
// (sum_weights = n_eff = n, n_weight_invalid = 0), that `base` carries
// none, and removes what a weight adds.
func shedCrosstabUnity(t *testing.T, resp *types.Response, row parityOp, aware map[string]descriptor.Operator, kind types.WeightKind) {
	t.Helper()
	opFor := func(auxLabel string) (descriptor.Operator, []string, map[string]string) {
		switch auxLabel {
		case "":
			return aware[string(row.op)], row.weightOnlyKeys, row.renameKeys
		case "aux_sum":
			return aware[string(types.AGG_SUM)], nil, nil
		}
		return descriptor.Operator{}, nil, nil
	}
	forEachCrosstabMap(t, resp, func(where, auxLabel string, m map[string]any) {
		if auxLabel == "base" {
			for _, k := range weightFloorKeys {
				if _, ok := m[k]; ok {
					t.Errorf("%s: the weight: null base carries %s", where, k)
				}
			}
			return
		}
		op, weightOnly, rename := opFor(auxLabel)
		n, _ := m["n"].(int)
		sw, ok := m["sum_weights"].(float64)
		if !ok {
			t.Fatalf("%s: weighted floor missing — the weight did not apply (%v)", where, m)
		}
		if sw != float64(n) || m["n_weight_invalid"] != 0 {
			t.Errorf("%s: unity floor sum_weights=%v n_weight_invalid=%v, want %d / 0", where, sw, m["n_weight_invalid"], n)
		}
		operatorNEff := false
		for _, k := range weightOnly {
			operatorNEff = operatorNEff || k == "n_eff"
		}
		nEff, has := m["n_eff"]
		switch {
		case kind == types.WeightKindProbability && nEff != float64(n):
			t.Errorf("%s: unity n_eff = %v, want %d", where, nEff, n)
		case kind == types.WeightKindFrequency && has && !operatorNEff:
			t.Errorf("%s: n_eff %v under kind frequency", where, nEff)
		}
		for _, k := range weightFloorKeys {
			delete(m, k)
		}
		for from, to := range rename {
			if v, ok := m[from]; ok {
				delete(m, from)
				m[to] = v
			}
		}
		for _, k := range op.ComponentSchema.Keys {
			if k.Optional {
				delete(m, k.Name)
			}
		}
		for _, k := range weightOnly {
			delete(m, k)
		}
	})
}

// --- frequency expansion ----------------------------------------------------

// crosstabVariant is one crosstab shape the expansion gate runs.
type crosstabVariant struct {
	name  string
	shape func(spec *types.CrosstabSpec)
	// scalarOnly: normalization refuses a map-valued cell.
	scalarOnly bool
}

func intp(v int) *int { return &v }

func crosstabVariants() []crosstabVariant {
	twoLevelRows := []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "g"},
		{Type: types.GROUP_RANGE, Field: "x", Interval: 10},
	}
	return []crosstabVariant{
		{name: "margins", shape: func(*types.CrosstabSpec) {}},
		{name: "normalize_row_partial_depth", scalarOnly: true, shape: func(s *types.CrosstabSpec) {
			s.Rows = twoLevelRows
			s.Normalize = types.CrosstabNormalizeRow
			s.NormalizeLevel = intp(0)
		}},
		{name: "normalize_column_within", scalarOnly: true, shape: func(s *types.CrosstabSpec) {
			s.Rows = twoLevelRows
			s.Normalize = types.CrosstabNormalizeColumn
			s.NormalizeWithin = intp(0)
		}},
		{name: "normalize_total", scalarOnly: true, shape: func(s *types.CrosstabSpec) {
			s.Normalize = types.CrosstabNormalizeTotal
		}},
	}
}

// TestWeightCrosstabFrequencyExpansion: integer weights (0..3) answer
// like the cohort whose rows are physically duplicated, on every cell,
// margin class, auxiliary margin and normalization — while the counts
// stay raw and the `weight: null` base is the unweighted figure.
func TestWeightCrosstabFrequencyExpansion(t *testing.T) {
	weighted := newParityStore(t, "xt_weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "xt_expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	src := paritySources()[1] // request_frequency
	ops := []parityOp{}
	for _, r := range parityOps {
		switch r.op {
		case types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_MEDIAN, types.AGG_PERCENTILE,
			types.AGG_MODE_COUNT, types.AGG_SET_FREQUENCY:
			ops = append(ops, r)
		}
	}
	for _, v := range crosstabVariants() {
		for _, row := range ops {
			if v.scalarOnly && row.op.MapValued() {
				continue
			}
			t.Run(v.name+"/"+string(row.op), func(t *testing.T) {
				build := func(store *parityStore) *types.Request {
					req := crosstabParityRequest(store.paths[paritySingleFile], row, row.op)
					v.shape(req.Crosstab)
					return req
				}
				want := runCrosstab(t, expanded, build(expanded), nil, true)
				raw := runCrosstab(t, weighted, build(weighted), nil, true)
				got := runCrosstab(t, weighted, build(weighted), &src, true)

				// Cells, every margin and normalization: the weighted
				// figures ARE the expanded figures.
				compareJSONClose(t, "crosstab", toJSONValue(t, got.Crosstab), toJSONValue(t, want.Crosstab))

				gc, wc, rc := got.Components.Crosstab, want.Components.Crosstab, raw.Components.Crosstab
				// Counts stay raw ints: equal to the unweighted run
				// over the SAME (weighted) cohort.
				for name, pair := range map[string][2]any{
					"cell_counts":          {gc.CellCounts, rc.CellCounts},
					"row_margin_counts":    {gc.RowMarginCounts, rc.RowMarginCounts},
					"column_margin_counts": {gc.ColumnMarginCounts, rc.ColumnMarginCounts},
					"grand_total_count":    {gc.GrandTotalCount, rc.GrandTotalCount},
				} {
					if g, r := mustMarshal(t, pair[0]), mustMarshal(t, pair[1]); !bytes.Equal(g, r) {
						t.Errorf("%s = %s, want the raw counts %s", name, g, r)
					}
				}
				// The floor: each weighted map's sum_weights is the
				// expanded map's n.
				wantN := map[string]int{}
				forEachCrosstabMap(t, want, func(where, label string, m map[string]any) {
					if label != "base" {
						wantN[where], _ = m["n"].(int)
					}
				})
				forEachCrosstabMap(t, got, func(where, label string, m map[string]any) {
					if label == "base" {
						return
					}
					sw, ok := m["sum_weights"].(float64)
					if !ok {
						t.Errorf("%s: no sum_weights on a weighted map", where)
						return
					}
					if n, ok := wantN[where]; !ok || sw != float64(n) {
						t.Errorf("%s: sum_weights %v, expanded n %d", where, sw, n)
					}
				})
				// Auxiliaries: the inheriting aux_sum is the expanded
				// figure; `base` (weight: null) the raw unweighted one.
				compareJSONClose(t, "aux_sum", auxValues(t, gc, "aux_sum"), auxValues(t, wc, "aux_sum"))
				if g, r := mustMarshal(t, auxValues(t, gc, "base")), mustMarshal(t, auxValues(t, rc, "base")); !bytes.Equal(g, r) {
					t.Errorf("weight: null base = %s, want the unweighted %s", g, r)
				}
			})
		}
	}
}

// auxValues collects one auxiliary's figures across every margin slot.
func auxValues(t *testing.T, c *types.CrosstabComponents, label string) []any {
	t.Helper()
	var out []any
	for _, figs := range c.RowMarginAggregations {
		out = append(out, figs[label].Value)
	}
	for _, figs := range c.ColumnMarginAggregations {
		out = append(out, figs[label].Value)
	}
	out = append(out, c.GrandTotalAggregations[label].Value)
	if len(out) < 3 {
		t.Fatalf("auxiliary %s: only %d figures", label, len(out))
	}
	return out
}

func toJSONValue(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(mustMarshal(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// compareJSONClose compares two decoded JSON values, numbers within
// 1e-12 relative.
func compareJSONClose(t *testing.T, where string, got, want any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for k, wv := range w {
			compareJSONClose(t, where+"."+k, g[k], wv)
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for i := range w {
			compareJSONClose(t, fmt.Sprintf("%s[%d]", where, i), g[i], w[i])
		}
	case float64:
		g, ok := got.(float64)
		if !ok || !parityClose(g, w) {
			t.Errorf("%s = %v, want %v", where, got, w)
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", where, got, want)
		}
	}
}

// --- probability quantile scale invariance ----------------------------------

// TestWeightCrosstabProbabilityQuantileScaleInvariance: probability
// weights are rescaled to sum to n before a weighted median /
// percentile takes its rank, so a crosstab of them — cells and every
// margin — is byte-identical under any constant weight scale.
func TestWeightCrosstabProbabilityQuantileScaleInvariance(t *testing.T) {
	base := newParityStore(t, "xt_base", func(n int) []parityRow { return parityRows(n, fracWeight) })
	src := paritySources()[0] // request_probability
	for _, row := range parityOps {
		if !row.scaleNormalized {
			continue
		}
		t.Run(string(row.op), func(t *testing.T) {
			want := runCrosstab(t, base, crosstabParityRequest(base.paths[paritySingleFile], row, row.op), &src, true)
			for name, c := range map[string]float64{"x1/3": 1.0 / 3, "x7.3": 7.3, "x1e-6": 1e-6} {
				scaled := newParityStore(t, "xt_scaled", func(n int) []parityRow {
					return parityRows(n, func(i int) float64 { return fracWeight(i) * c })
				})
				got := runCrosstab(t, scaled, crosstabParityRequest(scaled.paths[paritySingleFile], row, row.op), &src, true)
				if g, w := mustMarshal(t, got.Crosstab), mustMarshal(t, want.Crosstab); !bytes.Equal(g, w) {
					t.Errorf("%s: crosstab %s, unscaled %s", name, g, w)
				}
			}
		})
	}
}

// --- per-slot override on auxiliaries ---------------------------------------

// TestWeightCrosstabAuxSlotOverride: an auxiliary's own weight wins
// over the request's — an unweighted crosstab with a weighted
// auxiliary, and a probability-weighted crosstab whose auxiliary reads
// the column as a frequency weight.
func TestWeightCrosstabAuxSlotOverride(t *testing.T) {
	store := newParityStore(t, "xt_override", func(n int) []parityRow { return parityRows(n, freqWeight) })
	path := store.paths[paritySingleFile]
	countRow := parityOp{op: types.AGG_SUM, fields: []string{"x"}}

	t.Run("weighted_aux_only", func(t *testing.T) {
		req := crosstabParityRequest(path, countRow, types.AGG_SUM)
		req.Crosstab.MarginAggregations[1].Weight = types.SlotWeightField("w")
		got := runCrosstab(t, store, req, nil, true)
		forEachCrosstabMap(t, got, func(where, label string, m map[string]any) {
			_, has := m["sum_weights"]
			if has != (label == "aux_sum") {
				t.Errorf("%s: sum_weights present=%v; only the aux_sum slot is weighted", where, has)
			}
		})
	})

	t.Run("aux_kind_override", func(t *testing.T) {
		req := crosstabParityRequest(path, countRow, types.AGG_SUM)
		req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability}
		req.Crosstab.MarginAggregations[1].Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		got := runCrosstab(t, store, req, nil, true)
		forEachCrosstabMap(t, got, func(where, label string, m map[string]any) {
			_, hasEff := m["n_eff"]
			switch label {
			case "base":
			case "aux_sum":
				if hasEff {
					t.Errorf("%s: n_eff on a frequency-weighted auxiliary", where)
				}
			default:
				if !hasEff {
					t.Errorf("%s: no n_eff on the probability-weighted cell aggregator", where)
				}
			}
		})
	})
}

// --- invalid weights --------------------------------------------------------

// invalidWeight: valid fractional weights with a negative every 17th
// row and a NaN every 19th.
func invalidWeight(i int) float64 {
	switch {
	case i%17 == 5:
		return -1
	case i%19 == 7:
		return math.NaN()
	}
	return 1.5
}

// TestWeightCrosstabInvalidRows: a crosstab carries ONE
// PULSE_WEIGHT_INVALID_ROWS warning over its filter-passing rows —
// whether the cell or only an auxiliary reads the weight, on both arms
// — the cell floor reports n_weight_invalid, and strict refuses.
func TestWeightCrosstabInvalidRows(t *testing.T) {
	store := newParityStore(t, "xt_invalid", func(n int) []parityRow { return parityRows(n, invalidWeight) })
	path := store.paths[paritySingleFile]
	var wantNeg, wantNaN int64
	for i := range 600 {
		switch w := invalidWeight(i); {
		case math.IsNaN(w):
			wantNaN++
		case w < 0:
			wantNeg++
		}
	}
	sumRow := parityOp{op: types.AGG_SUM, fields: []string{"x"}}
	for _, tc := range []struct {
		name     string
		buffered bool
		auxOnly  bool
	}{
		{"buffered", true, false},
		{"fused", false, false},
		{"buffered_aux_only", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			build := func() *types.Request {
				req := crosstabParityRequest(path, sumRow, types.AGG_SUM)
				if !tc.buffered {
					req.Filterers = nil
				}
				if tc.auxOnly {
					req.Crosstab.MarginAggregations[1].Weight = types.SlotWeightField("w")
				} else {
					req.Weight = &types.WeightSpec{Field: "w"}
				}
				return req
			}
			req := build()
			if ok, _ := processing.CanFuseCrosstab(req, paritySchema(), New(store.mem).extensions); ok == tc.buffered {
				t.Fatalf("fusable = %v, want %v", ok, !tc.buffered)
			}
			resp := runCrosstab(t, store, req, nil, false)
			var found []*types.ResponseWarning
			for _, w := range resp.Warnings {
				if w.Code == string(errors.PULSE_WEIGHT_INVALID_ROWS) {
					found = append(found, w)
				}
			}
			if len(found) != 1 {
				t.Fatalf("%d PULSE_WEIGHT_INVALID_ROWS warnings, want 1 (%v)", len(found), resp.Warnings)
			}
			by := found[0].Details["by_reason"].(map[string]any)
			if found[0].Details["count"] != wantNeg+wantNaN || by["negative"] != wantNeg || by["nan_inf"] != wantNaN {
				t.Errorf("warning details %v, want count %d (negative %d, nan_inf %d)", found[0].Details, wantNeg+wantNaN, wantNeg, wantNaN)
			}
			if !tc.auxOnly {
				var invalid int
				for _, row := range resp.Components.Crosstab.CellComponents {
					for _, m := range row {
						if m != nil {
							invalid += m["n_weight_invalid"].(int)
						}
					}
				}
				if invalid == 0 {
					t.Error("no cell reports n_weight_invalid over a cohort with invalid weights")
				}
			}

			svc := New(store.mem)
			svc.SetStrict(true)
			_, err := svc.Process(context.Background(), build())
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_INVALID_ROWS {
				t.Errorf("strict: err = %v, want PULSE_WEIGHT_INVALID_ROWS", err)
			}
		})
	}
}

// --- fusion decision --------------------------------------------------------

// TestWeightCrosstabFusionDecision: a weight never changes the fusion
// answer — a weighted median / percentile cell or auxiliary stays on
// the buffered arm (non-mergeable, as unweighted), and a weighted
// mergeable cell stays fusable.
func TestWeightCrosstabFusionDecision(t *testing.T) {
	ext := New(fs.NewMemMap()).extensions
	w := &types.WeightSpec{Field: "w"}
	xt := func(cell *types.Aggregation, aux ...*types.Aggregation) *types.Request {
		return &types.Request{Weight: w, Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Cell:               cell,
			MarginAggregations: aux,
			Margins:            types.CrosstabMargins{Rows: true},
		}}
	}
	pct := json.RawMessage(`{"percentile":90}`)
	for _, tc := range []struct {
		name string
		req  *types.Request
		fuse bool
	}{
		{"median_cell", xt(&types.Aggregation{Type: types.AGG_MEDIAN, Field: "y"}), false},
		{"percentile_cell", xt(&types.Aggregation{Type: types.AGG_PERCENTILE, Field: "y", Params: pct}), false},
		{"median_aux", xt(&types.Aggregation{Type: types.AGG_SUM, Field: "y"}, &types.Aggregation{Type: types.AGG_MEDIAN, Field: "y", Label: "m"}), false},
		{"sum_cell", xt(&types.Aggregation{Type: types.AGG_SUM, Field: "y"}), true},
		{"count_cell_null_base", xt(&types.Aggregation{Type: types.AGG_COUNT, Field: "y"},
			&types.Aggregation{Type: types.AGG_COUNT, Field: "y", Label: "base", Weight: types.NullSlotWeight()}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stamped := processing.StampWeights(tc.req, nil)
			ok, reasons := processing.CanFuseCrosstab(stamped, paritySchema(), ext)
			if ok != tc.fuse {
				t.Errorf("fusable = %v (%q), want %v", ok, reasons, tc.fuse)
			}
			unweighted := *tc.req
			unweighted.Weight = nil
			if ok2, _ := processing.CanFuseCrosstab(&unweighted, paritySchema(), ext); ok2 != ok {
				t.Errorf("a weight changed the fusion answer: weighted %v, unweighted %v", ok, ok2)
			}
		})
	}
}
