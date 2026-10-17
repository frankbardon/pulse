package processing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted pairwise proportion tests read the BASE, not the cell (U47
// #324). On a weighted crosstab host the proportion leg is the cell
// value — on a normalised host Σw_cell / Σw_base — so the n leg must be
// the same base's N* (Σw under kind frequency, Kish n_eff under kind
// probability): the column margin under normalize column, the row
// margin under row, the grand margin under total, and the partial-depth
// (normalize_level) or cross-axis (normalize_within) denominator when
// those engage. Reading the cell's own N* paired a column share with a
// sliver of the column and drove every p-value far too high.

// baseObs is one respondent of the base fixture.
type baseObs struct {
	grp, row, col, sub string
	ind, w             float64
}

func baseSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add: %v", err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "grp", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("g0", "g1")},
		{Name: "row", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("dk", "no", "yes")},
		{Name: "col", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("c0", "c1", "c2")},
		{Name: "sub", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("s0", "s1")},
		{Name: "market", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("all")},
		{Name: "ind", Type: encoding.FieldTypeF64},
		{Name: "weight", Type: encoding.FieldTypeF64},
	}}
}

// baseFixture is deterministic: per column a different base size and
// success rate, weights on a non-constant cycle (so Kish n_eff < Σw and
// a frequency / probability mix-up moves the answer), the two sub /
// grp levels interleaved, and a "dk" row the request's Include drops —
// a respondent who reached no cell and so must sit in no base.
func baseFixture() []baseObs {
	type colPlan struct {
		col     string
		n, succ int
	}
	plans := []colPlan{{"c0", 40, 18}, {"c1", 30, 9}, {"c2", 26, 3}}
	weights := []float64{1, 3, 2, 5, 1} // integers: valid under kind frequency too
	var out []baseObs
	k := 0
	for _, p := range plans {
		for i := 0; i < p.n; i++ {
			row := "no"
			ind := 0.0
			if i < p.succ {
				row, ind = "yes", 1
			}
			if i%9 == 8 {
				row = "dk"
			}
			out = append(out, baseObs{
				grp: []string{"g0", "g1"}[(i/2)%2], row: row, col: p.col,
				sub: []string{"s0", "s1"}[i%2], ind: ind, w: weights[k%len(weights)],
			})
			k++
		}
	}
	return out
}

func baseRecords(schema *encoding.Schema, obs []baseObs) []*Record {
	idx := func(field, v string) float64 {
		f := schema.Field(field)
		i, _ := f.Dictionary.IDFor(v)
		return float64(i)
	}
	out := make([]*Record, 0, len(obs))
	for _, o := range obs {
		out = append(out, NewRecord(schema, map[string]float64{
			"grp": idx("grp", o.grp), "row": idx("row", o.row), "col": idx("col", o.col),
			"sub": idx("sub", o.sub), "market": 0, "ind": o.ind, "weight": o.w,
		}))
	}
	return out
}

// baseNStar is a set of respondents' N* under basis (their count on an
// unweighted host).
func baseNStar(obs []baseObs, keep func(baseObs) bool, basis weighting.Basis) (sumW, nStar float64) {
	var sw, sww float64
	for _, o := range obs {
		if keep(o) {
			w := o.w
			if basis == weighting.Unweighted {
				w = 1
			}
			sw += w
			sww += w * w
		}
	}
	if basis == weighting.Probability {
		return sw, sw * sw / sww
	}
	return sw, sw
}

// baseCase is one normalize mode: the crosstab shape, how a respondent
// maps to its row / column key, and which respondents form cell (r, c)'s
// denominator.
type baseCase struct {
	name      string
	rows      []*types.Group
	cols      []*types.Group
	normalize types.CrosstabNormalize
	level     *int
	within    *int
	scope     types.OverlayScope
	rowKey    func(baseObs) string
	colKey    func(baseObs) string
	inBase    func(o baseObs, rk, ck string) bool
}

func intRef(v int) *int { return &v }

func baseCases() []baseCase {
	row := &types.Group{Type: types.GROUP_CATEGORY, Field: "row", Include: []string{"yes", "no"}}
	grp := &types.Group{Type: types.GROUP_CATEGORY, Field: "grp"}
	col := &types.Group{Type: types.GROUP_CATEGORY, Field: "col"}
	sub := &types.Group{Type: types.GROUP_CATEGORY, Field: "sub"}
	asked := func(o baseObs) bool { return o.row != "dk" }
	rowOf := func(o baseObs) string { return o.row }
	colOf := func(o baseObs) string { return o.col }
	return []baseCase{
		{
			name: "column", rows: []*types.Group{row}, cols: []*types.Group{col},
			normalize: types.CrosstabNormalizeColumn, scope: types.OverlayScopeColumn,
			rowKey: rowOf, colKey: colOf,
			inBase: func(o baseObs, _, ck string) bool { return asked(o) && o.col == ck },
		},
		{
			name: "row", rows: []*types.Group{row}, cols: []*types.Group{col},
			normalize: types.CrosstabNormalizeRow, scope: types.OverlayScopeRow,
			rowKey: rowOf, colKey: colOf,
			inBase: func(o baseObs, rk, _ string) bool { return asked(o) && o.row == rk },
		},
		{
			name: "total", rows: []*types.Group{row}, cols: []*types.Group{col},
			normalize: types.CrosstabNormalizeTotal, scope: types.OverlayScopeColumn,
			rowKey: rowOf, colKey: colOf,
			inBase: func(o baseObs, _, _ string) bool { return asked(o) },
		},
		{
			// normalize_level 0 on a nested column axis: the denominator
			// is the col PREFIX (both sub levels together).
			name: "column_level0", rows: []*types.Group{row}, cols: []*types.Group{col, sub},
			normalize: types.CrosstabNormalizeColumn, level: intRef(0), scope: types.OverlayScopeColumn,
			rowKey: rowOf, colKey: func(o baseObs) string { return o.col + "|" + o.sub },
			inBase: func(o baseObs, _, ck string) bool { return asked(o) && o.col == strings.Split(ck, "|")[0] },
		},
		{
			// normalize_within 0: the denominator fixes the column AND the
			// first row grouper (grp), summing across the row leaf.
			name: "column_within0", rows: []*types.Group{grp, row}, cols: []*types.Group{col},
			normalize: types.CrosstabNormalizeColumn, within: intRef(0), scope: types.OverlayScopeColumn,
			rowKey: func(o baseObs) string { return o.grp + "|" + o.row }, colKey: colOf,
			inBase: func(o baseObs, rk, ck string) bool {
				return asked(o) && o.col == ck && o.grp == strings.Split(rk, "|")[0]
			},
		},
	}
}

// baseRequest builds case c's crosstab with one pairwise prop-z layer;
// an empty kind leaves the cell unweighted.
func baseRequest(c baseCase, kind types.WeightKind, params types.PairwiseOverlayParams, margins types.CrosstabMargins) *types.Request {
	cell := &types.Aggregation{Type: types.AGG_COUNT, Field: "ind", Label: "n"}
	if kind != "" {
		cell.Weight = types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: kind})
	}
	return &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: c.rows, Columns: c.cols,
			Cell:      cell,
			Normalize: c.normalize, NormalizeLevel: c.level, NormalizeWithin: c.within,
			Margins: margins,
		},
		Overlays: []types.OverlaySpec{{Name: "pw", Kind: types.OverlayKindPairwisePropZ, Scope: c.scope,
			Params: json.RawMessage(mustParamsJSON(params))}},
	}
}

func mustParamsJSON(p types.PairwiseOverlayParams) []byte {
	b, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return b
}

// axisLabel is the pair-key label of a host axis key.
func axisLabel(k types.AxisKey) string { return stringifyOverlayAxisKey(k) }

// expectedPairP is the closed-form pooled two-proportion z p-value of a
// pair of host cells: p̂ = Σw_cell / Σw_base and n = N*_base, every
// figure recomputed from the respondents.
func expectedPairP(obs []baseObs, c baseCase, basis weighting.Basis, r1, c1, r2, c2 string) float64 {
	leg := func(rk, ck string) (float64, float64) {
		var cell float64
		for _, o := range obs {
			if o.row != "dk" && c.rowKey(o) == rk && c.colKey(o) == ck {
				if basis == weighting.Unweighted {
					cell++
				} else {
					cell += o.w
				}
			}
		}
		sw, nStar := baseNStar(obs, func(o baseObs) bool { return c.inBase(o, rk, ck) }, basis)
		return cell / sw, nStar
	}
	p1, n1 := leg(r1, c1)
	p2, n2 := leg(r2, c2)
	p, _ := twoProportionZ(p1*n1, n1, p2*n2, n2)
	return p
}

// checkBaseLayer compares every present p-value of a column- or
// row-scope pairwise layer with the closed form; returns the count
// checked.
func checkBaseLayer(t *testing.T, where string, resp *types.Response, obs []baseObs, c baseCase, basis weighting.Basis) int {
	t.Helper()
	if len(resp.Overlays) != 1 || resp.Overlays[0].Payload.Matrix == nil {
		t.Fatalf("%s: no pairwise layer: %+v", where, resp.Overlays)
	}
	layer := resp.Overlays[0].Payload.Matrix
	rowKeyOf := func(k types.AxisKey) string { return axisLabel(k) }
	checked := 0
	if c.scope == types.OverlayScopeColumn {
		for r, rk := range layer.RowKeys {
			for p, pk := range layer.ColumnKeys {
				cell := layer.Cells[r][p]
				if !cell.Present {
					continue
				}
				want := expectedPairP(obs, c, basis, rowKeyOf(rk), pk[0].(string), rowKeyOf(rk), pk[1].(string))
				relClose(t, where+" "+rowKeyOf(rk)+" "+pk[0].(string)+" vs "+pk[1].(string), cell.Value.(float64), want, 1e-9)
				checked++
			}
		}
	} else {
		for p, pk := range layer.RowKeys {
			for cc, ck := range layer.ColumnKeys {
				cell := layer.Cells[p][cc]
				if !cell.Present {
					continue
				}
				want := expectedPairP(obs, c, basis, pk[0].(string), axisLabel(ck), pk[1].(string), axisLabel(ck))
				relClose(t, where+" "+pk[0].(string)+" vs "+pk[1].(string)+" @ "+axisLabel(ck), cell.Value.(float64), want, 1e-9)
				checked++
			}
		}
	}
	return checked
}

// TestCrosstab_WeightedPairwisePropZ_NFollowsNormalize: under every
// normalize mode — column, row, total, normalize_level, normalize_within
// — and both weight kinds, the default n leg on a weighted host is the
// N* of the cell's own (admitted) normalise denominator, on the buffered
// and the fused arm alike, with no margin displayed (the denominator is
// computed for normalisation only). An unweighted host reads the same
// base's count, so a frequency weight stays the expanded rows. The two
// arms agree bit for bit.
func TestCrosstab_WeightedPairwisePropZ_NFollowsNormalize(t *testing.T) {
	schema := baseSchema(t)
	obs := baseFixture()
	recs := baseRecords(schema, obs)
	kinds := map[types.WeightKind]weighting.Basis{
		"":                          weighting.Unweighted,
		types.WeightKindFrequency:   weighting.Frequency,
		types.WeightKindProbability: weighting.Probability,
	}
	for _, c := range baseCases() {
		for kind, basis := range kinds {
			where := c.name + " " + string(kind)
			params := types.PairwiseOverlayParams{PSource: types.PairwisePSourceCellValue}
			req := func() *types.Request { return baseRequest(c, kind, params, types.CrosstabMargins{}) }
			if ok, reason := CanFuseCrosstab(req(), schema, nil); !ok {
				t.Fatalf("%s: fused gate declined: %s", where, reason)
			}
			buf, err := runBufferedCrosstabWithComponents(t, schema, req(), recs, false)
			if err != nil {
				t.Fatalf("%s buffered: %v", where, err)
			}
			fused, err := runFusedCrosstabViaRunner(t, schema, req(), recs, false)
			if err != nil {
				t.Fatalf("%s fused: %v", where, err)
			}
			if n := checkBaseLayer(t, where+" buffered", buf, obs, c, basis); n == 0 {
				t.Fatalf("%s: no present p-value checked", where)
			}
			if a, b := jsonOf(t, buf.Overlays), jsonOf(t, fused.Overlays); a != b {
				t.Errorf("%s: arms diverge:\nbuffered %s\nfused    %s", where, a, b)
			}
			// Parameters tally the bases read, each once: on the column
			// case every column base, i.e. every asked respondent.
			if c.name == "column" && basis.Weighted() {
				sw, nStar := baseNStar(obs, func(o baseObs) bool { return o.row != "dk" }, basis)
				want := map[string]float64{"sum_weights": sw}
				if basis == weighting.Probability {
					want["n_eff"] = nStar
				}
				got := buf.Overlays[0].Summary.Parameters
				if len(got) != len(want) {
					t.Fatalf("%s: Parameters %v, want %v", where, got, want)
				}
				for k, v := range want {
					relClose(t, where+" Parameters "+k, got[k], v, 1e-12)
				}
			}
		}
	}
}

// TestCrosstab_WeightedPairwisePropZ_MarginNSources: row_margin_n and
// column_margin_n stand on a weighted host and read the DISPLAYED
// margin's N* (its floor), so under the matching normalize mode they
// answer exactly the default; a margin that is not displayed carries no
// floor and its pairs skip — never a fallback to the cell or a raw
// count.
func TestCrosstab_WeightedPairwisePropZ_MarginNSources(t *testing.T) {
	schema := baseSchema(t)
	obs := baseFixture()
	recs := baseRecords(schema, obs)
	shown := types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	pairs := map[string]string{"column": types.PairwiseNSourceColumnMarginN, "row": types.PairwiseNSourceRowMarginN}
	for _, c := range baseCases() {
		mode, ok := pairs[c.name]
		if !ok {
			continue
		}
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			where := c.name + " " + string(kind)
			def := baseRequest(c, kind, types.PairwiseOverlayParams{PSource: types.PairwisePSourceCellValue}, shown)
			exp := baseRequest(c, kind, types.PairwiseOverlayParams{PSource: types.PairwisePSourceCellValue, NSource: mode}, shown)
			for _, arm := range []string{"buffered", "fused"} {
				run := runBufferedCrosstabWithComponents
				if arm == "fused" {
					run = runFusedCrosstabViaRunner
				}
				d, err := run(t, schema, def, recs, false)
				if err != nil {
					t.Fatal(err)
				}
				e, err := run(t, schema, exp, recs, false)
				if err != nil {
					t.Fatalf("%s %s %s: %v", where, arm, mode, err)
				}
				if pairwisePropZLayerPresentCells(e) == 0 {
					t.Fatalf("%s %s: explicit %s produced no p-value", where, arm, mode)
				}
				if a, b := jsonOf(t, d.Overlays), jsonOf(t, e.Overlays); a != b {
					t.Errorf("%s %s: %s differs from the default:\n%s\n%s", where, arm, mode, a, b)
				}
				hidden := baseRequest(c, kind, types.PairwiseOverlayParams{PSource: types.PairwisePSourceCellValue, NSource: mode}, types.CrosstabMargins{})
				h, err := run(t, schema, hidden, recs, false)
				if err != nil {
					t.Fatalf("%s %s hidden margin: %v", where, arm, err)
				}
				if n := pairwisePropZLayerPresentCells(h); n != 0 {
					t.Errorf("%s %s: %s with the margin hidden scored %d pairs, want every pair skipped", where, arm, mode, n)
				}
			}
		}
	}
}

// TestCrosstab_WeightedPairwisePropZ_IndicatorMeanOracle is the U47
// reference-oracle case, shaped on the brand-tracker comparison that
// exposed #324 (Stylish 18-34 at 28.6% vs 55+ at 6.8%; casebook raw
// p = 3.5e-7 on n_eff 750.8 vs 120.3): a probability-weighted AGG_COUNT
// crosstab normalised to column percent must test each column pair at
// the SAME p as the weighted indicator-mean method — a one-row
// AGG_AVERAGE of the 0/1 indicator per column, whose cell IS the column
// (p̂ = Σw·ind / Σw, n = the column's Kish n_eff). Reverting the n leg
// to the normalised cell's own N* breaks the equality and lifts the
// p-value past 0.001.
func TestCrosstab_WeightedPairwisePropZ_IndicatorMeanOracle(t *testing.T) {
	schema := baseSchema(t)
	type plan struct {
		col     string
		n, succ int
	}
	// The casebook's per-column base sizes and shares (761 / 316 / 122 at
	// 28.6% / 14.6% / 6.8%).
	plans := []plan{{"c0", 761, 218}, {"c1", 316, 46}, {"c2", 122, 8}}
	weights := []float64{0.9, 1.1, 1, 1.25, 0.8}
	var obs []baseObs
	k := 0
	for _, p := range plans {
		for i := 0; i < p.n; i++ {
			row, ind := "no", 0.0
			if i < p.succ {
				row, ind = "yes", 1
			}
			obs = append(obs, baseObs{grp: "g0", row: row, col: p.col, sub: "s0", ind: ind, w: weights[k%len(weights)]})
			k++
		}
	}
	recs := baseRecords(schema, obs)
	prob := types.SlotWeightOf(types.WeightSpec{Field: "weight", Kind: types.WeightKindProbability})
	overlay := []types.OverlaySpec{{Name: "pw", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
		Params: json.RawMessage(`{"p_source":"cell_value"}`)}}
	col := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "col"}}
	count := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "row"}}, Columns: col,
			Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "ind", Label: "n", Weight: prob},
			Normalize: types.CrosstabNormalizeColumn,
		},
		Overlays: overlay,
	}
	mean := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "market"}}, Columns: col,
			Cell: &types.Aggregation{Type: types.AGG_AVERAGE, Field: "ind", Label: "share", Weight: prob},
		},
		Overlays: overlay,
	}
	pairP := func(resp *types.Response, rowLabel string) map[string]float64 {
		t.Helper()
		layer := resp.Overlays[0].Payload.Matrix
		out := map[string]float64{}
		for r, rk := range layer.RowKeys {
			if axisLabel(rk) != rowLabel {
				continue
			}
			for p, pk := range layer.ColumnKeys {
				if cell := layer.Cells[r][p]; cell.Present {
					out[pk[0].(string)+"/"+pk[1].(string)] = cell.Value.(float64)
				}
			}
		}
		return out
	}
	for _, arm := range []string{"buffered", "fused"} {
		run := runBufferedCrosstabWithComponents
		if arm == "fused" {
			run = runFusedCrosstabViaRunner
		}
		cResp, err := run(t, schema, count, recs, false)
		if err != nil {
			t.Fatalf("%s count host: %v", arm, err)
		}
		mResp, err := run(t, schema, mean, recs, false)
		if err != nil {
			t.Fatalf("%s indicator-mean host: %v", arm, err)
		}
		got, want := pairP(cResp, "yes"), pairP(mResp, "all")
		if len(want) != 3 || len(got) != 3 {
			t.Fatalf("%s: pairs count %v, mean %v", arm, got, want)
		}
		for pair, w := range want {
			relClose(t, arm+" "+pair, got[pair], w, 1e-9)
		}
		young := got["c0/c2"]
		if !(young < 0.001) {
			t.Errorf("%s: 18-34 vs 55+ p = %g, want < 0.001", arm, young)
		}
		// Independent closed form on the columns' Kish n_eff.
		_, n1 := baseNStar(obs, func(o baseObs) bool { return o.col == "c0" }, weighting.Probability)
		_, n2 := baseNStar(obs, func(o baseObs) bool { return o.col == "c2" }, weighting.Probability)
		sw1, _ := baseNStar(obs, func(o baseObs) bool { return o.col == "c0" }, weighting.Frequency)
		sw2, _ := baseNStar(obs, func(o baseObs) bool { return o.col == "c2" }, weighting.Frequency)
		s1, _ := baseNStar(obs, func(o baseObs) bool { return o.col == "c0" && o.ind == 1 }, weighting.Frequency)
		s2, _ := baseNStar(obs, func(o baseObs) bool { return o.col == "c2" && o.ind == 1 }, weighting.Frequency)
		closed, _ := twoProportionZ(s1/sw1*n1, n1, s2/sw2*n2, n2)
		relClose(t, arm+" closed form", young, closed, 1e-9)
		// The pre-#324 reading: the normalised cell's own N* (the
		// successes' Kish n_eff) as n. It must not be what ran.
		_, c1 := baseNStar(obs, func(o baseObs) bool { return o.col == "c0" && o.ind == 1 }, weighting.Probability)
		_, c2 := baseNStar(obs, func(o baseObs) bool { return o.col == "c2" && o.ind == 1 }, weighting.Probability)
		cellN, _ := twoProportionZ(s1/sw1*c1, c1, s2/sw2*c2, c2)
		if math.Abs(young-cellN) < 1e-12 || cellN < 0.001 {
			t.Fatalf("%s: fixture does not separate the base from the cell n (base p %g, cell-n p %g)", arm, young, cellN)
		}
	}
}

// TestPairwiseBaseFloor_UnreadableBaseSkips: a normalised weighted host
// whose base floors are missing (a host built without the arms'
// carrier) skips every pair — it never falls back to the cell's own
// N*, the pre-#324 reading.
func TestPairwiseBaseFloor_UnreadableBaseSkips(t *testing.T) {
	schema := baseSchema(t)
	recs := baseRecords(schema, baseFixture())
	c := baseCases()[0]
	req := baseRequest(c, types.WeightKindProbability, types.PairwiseOverlayParams{PSource: types.PairwisePSourceCellValue}, types.CrosstabMargins{})
	resp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
	if err != nil {
		t.Fatal(err)
	}
	if pairwisePropZLayerPresentCells(resp) == 0 {
		t.Fatal("fixture scores nothing with the carrier")
	}
	host := newCrosstabHostViewWithComponents(resp.Crosstab.Matrix, resp.Components.Crosstab)
	host.withNormalize(types.CrosstabNormalizeColumn, nil).withCellWeight(req.Crosstab.Cell)
	layer, warns, err := applyPairwisePropZ(&req.Overlays[0], host)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range layer.Payload.Matrix.Cells {
		for _, cell := range row {
			if cell.Present {
				t.Fatalf("a pair scored without its base: %+v", layer.Payload.Matrix.Cells)
			}
		}
	}
	if len(warns) == 0 {
		t.Fatal("every pair skipped silently")
	}
}
