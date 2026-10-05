package processing

import (
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Weighted contingency and proportion overlays (weighting-inferential
// E3-S2). A weighted host's cells are Σw; under kind frequency the χ²
// family runs Pearson on that table (the expanded rows) and under kind
// probability on the table — or the row / column margin — scaled to its
// Kish n_eff (NOT Rao-Scott). Proportion overlays read p̂ = Σw_success /
// Σw_base with n = N*_base. References are computed here from the raw
// (row, col, w) records in closed form.

// ctRec is one raw record: its cell and its weight.
type ctRec struct {
	r, c int
	w    float64
}

// ctFixture: 2 rows × 3 columns, integer weights (valid frequency
// weights) of very unequal size, so n_eff is far below Σw.
var ctFixture = []ctRec{
	{0, 0, 1}, {0, 0, 9}, {0, 0, 2},
	{0, 1, 3}, {0, 1, 1},
	{0, 2, 8}, {0, 2, 1}, {0, 2, 1}, {0, 2, 2},
	{1, 0, 2}, {1, 0, 2},
	{1, 1, 10}, {1, 1, 1}, {1, 1, 1},
	{1, 2, 1}, {1, 2, 3},
}

// wsum is Σw and Σw² over a record subset.
type wsum struct{ sw, sww float64 }

func (s *wsum) add(w float64) { s.sw += w; s.sww += w * w }

// floorMap is the components map a weighted AGG_COUNT cell / margin
// emits over s (n_eff only under probability).
func floorMap(s wsum, n int, basis weighting.Basis) map[string]any {
	m := map[string]any{"n": n, "n_null": 0}
	if !basis.Weighted() {
		return m
	}
	m["sum_weights"] = s.sw
	m["n_weight_invalid"] = 0
	if basis == weighting.Probability {
		m["n_eff"] = s.sw * s.sw / s.sww
	}
	return m
}

// ctTables tallies the fixture per cell, row, column and table.
type ctTables struct {
	cell        [][]wsum
	cellN       [][]int
	row, col    []wsum
	rowN, colN  []int
	table       wsum
	rows, cols  int
	recordCount int
}

func tallyCT(recs []ctRec, rows, cols int) ctTables {
	t := ctTables{rows: rows, cols: cols, row: make([]wsum, rows), col: make([]wsum, cols),
		rowN: make([]int, rows), colN: make([]int, cols)}
	t.cell = make([][]wsum, rows)
	t.cellN = make([][]int, rows)
	for i := range t.cell {
		t.cell[i] = make([]wsum, cols)
		t.cellN[i] = make([]int, cols)
	}
	for _, r := range recs {
		t.cell[r.r][r.c].add(r.w)
		t.cellN[r.r][r.c]++
		t.row[r.r].add(r.w)
		t.rowN[r.r]++
		t.col[r.c].add(r.w)
		t.colN[r.c]++
		t.table.add(r.w)
		t.recordCount++
	}
	return t
}

// weightedCountHost is the crosstab a weighted AGG_COUNT cell produces
// over recs: cells and margins = Σw, components carrying the floor.
// basis Unweighted yields the same numbers with no floor keys.
func weightedCountHost(recs []ctRec, rows, cols int, basis weighting.Basis) *CrosstabHostView {
	t := tallyCT(recs, rows, cols)
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"r"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"c"}, Types: []string{"GROUP_CATEGORY"}},
		GrandTotal:   types.MatrixCell{Value: t.table.sw, Present: true},
	}
	comps := &types.CrosstabComponents{GrandTotalComponents: floorMap(t.table, t.recordCount, basis), GrandTotalCount: t.recordCount}
	for i := 0; i < rows; i++ {
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{string(rune('A' + i))})
		mx.RowMargins = append(mx.RowMargins, types.MatrixCell{Value: t.row[i].sw, Present: true})
		comps.RowMarginComponents = append(comps.RowMarginComponents, floorMap(t.row[i], t.rowN[i], basis))
		comps.RowMarginCounts = append(comps.RowMarginCounts, t.rowN[i])
		cells := make([]types.MatrixCell, cols)
		cc := make([]map[string]any, cols)
		counts := make([]int, cols)
		for j := 0; j < cols; j++ {
			cells[j] = types.MatrixCell{Value: t.cell[i][j].sw, Present: true}
			cc[j] = floorMap(t.cell[i][j], t.cellN[i][j], basis)
			counts[j] = t.cellN[i][j]
		}
		mx.Cells = append(mx.Cells, cells)
		comps.CellComponents = append(comps.CellComponents, cc)
		comps.CellCounts = append(comps.CellCounts, counts)
	}
	for j := 0; j < cols; j++ {
		mx.ColumnKeys = append(mx.ColumnKeys, types.AxisKey{string(rune('x' + j))})
		mx.ColumnMargins = append(mx.ColumnMargins, types.MatrixCell{Value: t.col[j].sw, Present: true})
		comps.ColumnMarginComponents = append(comps.ColumnMarginComponents, floorMap(t.col[j], t.colN[j], basis))
		comps.ColumnMarginCounts = append(comps.ColumnMarginCounts, t.colN[j])
	}
	return newCrosstabHostViewWithComponents(mx, comps)
}

// pearsonRef is the ordinary Pearson χ² of a table and its expected
// minimum.
func pearsonRef(table [][]float64) (stat, expMin float64) {
	rows, cols := len(table), len(table[0])
	rt := make([]float64, rows)
	ct := make([]float64, cols)
	var g float64
	for i := range table {
		for j, v := range table[i] {
			rt[i] += v
			ct[j] += v
			g += v
		}
	}
	expMin = math.Inf(1)
	for i := range table {
		for j, v := range table[i] {
			e := rt[i] * ct[j] / g
			expMin = math.Min(expMin, e)
			stat += (v - e) * (v - e) / e
		}
	}
	return stat, expMin
}

// sigmaWTable is the fixture's Σw table, kishTable that table scaled to
// the table's n_eff: T* = n_eff·Σw_ij/Σw.
func sigmaWTable(t ctTables) [][]float64 {
	out := make([][]float64, t.rows)
	for i := range out {
		out[i] = make([]float64, t.cols)
		for j := range out[i] {
			out[i][j] = t.cell[i][j].sw
		}
	}
	return out
}

func kishTable(t ctTables) [][]float64 {
	nEff := t.table.sw * t.table.sw / t.table.sww
	out := sigmaWTable(t)
	for i := range out {
		for j := range out[i] {
			out[i][j] = nEff * out[i][j] / t.table.sw
		}
	}
	return out
}

func lowExpectedMin(t *testing.T, ws []types.OverlayWarning) float64 {
	t.Helper()
	if len(ws) != 1 || ws[0].Code != string(errors.PULSE_OVERLAY_EXPECTED_LOW) {
		t.Fatalf("warnings = %+v, want one PULSE_OVERLAY_EXPECTED_LOW", ws)
	}
	return ws[0].Details["expected_min"].(float64)
}

// TestChiSqMatrix_WeightedHost: frequency = Pearson on the Σw table (the
// expanded rows), probability = Pearson on the table scaled to the
// table's Kish n_eff — not the Σw answer — with the low-expected warning
// and expected_min read off the SCALED table; Parameters carry
// sum_weights (+ n_eff); an unweighted host reports df only.
func TestChiSqMatrix_WeightedHost(t *testing.T) {
	ct := tallyCT(ctFixture, 2, 3)
	spec := &types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix}
	run := func(basis weighting.Basis) (types.OverlayLayer, []types.OverlayWarning) {
		layer, ws, err := applyChiSqMatrix(spec, weightedCountHost(ctFixture, 2, 3, basis))
		if err != nil {
			t.Fatal(err)
		}
		return layer, ws
	}
	rawStat, rawMin := pearsonRef(sigmaWTable(ct))
	kishStat, kishMin := pearsonRef(kishTable(ct))
	if math.Abs(rawStat-kishStat) < 1e-6*rawStat {
		t.Fatal("fixture: the Kish table does not move the statistic")
	}
	nEff := ct.table.sw * ct.table.sw / ct.table.sww

	freq, fws := run(weighting.Frequency)
	relClose(t, "frequency χ²", *freq.Summary.Statistic, rawStat, 1e-12)
	relClose(t, "frequency p", *freq.Summary.PValue, chiSquareSurvival(rawStat, 2), 1e-12)
	// The Σw table's expected counts clear 5; the scaled table's do not
	// — the warning reads the SCALED expected.
	if rawMin < 5 || kishMin >= 5 || len(fws) != 0 {
		t.Fatalf("fixture: Σw expected_min %v, scaled %v, frequency warnings %+v", rawMin, kishMin, fws)
	}
	assertParams(t, "frequency", freq.Summary, map[string]float64{"df": 2, "sum_weights": ct.table.sw})

	prob, pws := run(weighting.Probability)
	relClose(t, "probability χ²", *prob.Summary.Statistic, kishStat, 1e-12)
	relClose(t, "probability p", *prob.Summary.PValue, chiSquareSurvival(kishStat, 2), 1e-12)
	relClose(t, "probability expected_min (scaled)", lowExpectedMin(t, pws), kishMin, 1e-12)
	assertParams(t, "probability", prob.Summary, map[string]float64{"df": 2, "sum_weights": ct.table.sw, "n_eff": nEff})
	// Min / Max describe the observed Σw table, unscaled.
	if *prob.Summary.Max != *freq.Summary.Max {
		t.Fatalf("observed max moved with the scaling: %v vs %v", *prob.Summary.Max, *freq.Summary.Max)
	}

	unw, _ := run(weighting.Unweighted)
	if *unw.Summary.Statistic != *freq.Summary.Statistic || len(unw.Summary.Parameters) != 1 {
		t.Fatalf("unweighted host: stat %v params %v, want the Σw statistic and df only", *unw.Summary.Statistic, unw.Summary.Parameters)
	}
}

// TestChiSqRowCol_WeightedHost: per row (column), frequency = the
// goodness-of-fit on the Σw cells, probability = the row's (column's)
// cells scaled to that margin's Kish n_eff against the unscaled column
// (row) distribution; each entry's Parameters carry its margin's floor.
func TestChiSqRowCol_WeightedHost(t *testing.T) {
	ct := tallyCT(ctFixture, 2, 3)
	tab := sigmaWTable(ct)
	g := ct.table.sw
	// gof is one margin's statistic: obs scaled by c against
	// c·total·share.
	gof := func(obs []float64, total float64, share []float64, c float64) float64 {
		var stat float64
		for k, o := range obs {
			e := c * total * share[k]
			stat += (c*o - e) * (c*o - e) / e
		}
		return stat
	}
	colShare := make([]float64, 3)
	for j := range colShare {
		colShare[j] = ct.col[j].sw / g
	}
	rowShare := []float64{ct.row[0].sw / g, ct.row[1].sw / g}
	kish := func(s wsum) float64 { return (s.sw * s.sw / s.sww) / s.sw }

	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		host := weightedCountHost(ctFixture, 2, 3, basis)
		rowLayer, _, err := applyChiSqRow(&types.OverlaySpec{Kind: types.OverlayKindChiSqRow, Scope: types.OverlayScopeRow}, host)
		if err != nil {
			t.Fatal(err)
		}
		for i, e := range rowLayer.Payload.Series.Entries {
			c := 1.0
			want := map[string]float64{"df": 2, "sum_weights": ct.row[i].sw}
			if basis == weighting.Probability {
				c = kish(ct.row[i])
				want["n_eff"] = ct.row[i].sw * ct.row[i].sw / ct.row[i].sww
			}
			ref := gof(tab[i], ct.row[i].sw, colShare, c)
			relClose(t, "row χ²", *e.Summary.Statistic, ref, 1e-12)
			if basis == weighting.Probability && math.Abs(ref-gof(tab[i], ct.row[i].sw, colShare, 1)) < 1e-9 {
				t.Fatal("fixture: the row's Kish scale does not move its statistic")
			}
			assertParams(t, "row entry", &e.Summary, want)
		}
		colLayer, _, err := applyChiSqCol(&types.OverlaySpec{Kind: types.OverlayKindChiSqCol, Scope: types.OverlayScopeColumn}, host)
		if err != nil {
			t.Fatal(err)
		}
		for j, e := range colLayer.Payload.Series.Entries {
			c := 1.0
			want := map[string]float64{"df": 1, "sum_weights": ct.col[j].sw}
			if basis == weighting.Probability {
				c = kish(ct.col[j])
				want["n_eff"] = ct.col[j].sw * ct.col[j].sw / ct.col[j].sww
			}
			ref := gof([]float64{tab[0][j], tab[1][j]}, ct.col[j].sw, rowShare, c)
			relClose(t, "column χ²", *e.Summary.Statistic, ref, 1e-12)
			assertParams(t, "column entry", &e.Summary, want)
		}
	}
}

// TestFisherCell_WeightedHost: frequency-only. A frequency host runs
// the exact test on the integer Σw table (identical to the unweighted
// host carrying the same counts) and reports the table's Σw; a
// probability host is PULSE_WEIGHT_UNSUPPORTED naming the kind; the
// basis comes from the STAMPED cell weight when known — a cell whose
// own n_eff key is emitted under kind frequency (AGG_WEIGHTED_MEAN's
// map) is not mistaken for a probability host.
func TestFisherCell_WeightedHost(t *testing.T) {
	ct := tallyCT(ctFixture, 2, 3)
	specs := []types.OverlaySpec{{Kind: types.OverlayKindFisherExactCell, Scope: types.OverlayScopeCell}}

	unw, _, err := ApplyOverlaysWithExtensions(specs, weightedCountHost(ctFixture, 2, 3, weighting.Unweighted), nil)
	if err != nil {
		t.Fatal(err)
	}
	freq, _, err := ApplyOverlaysWithExtensions(specs, weightedCountHost(ctFixture, 2, 3, weighting.Frequency), nil)
	if err != nil {
		t.Fatal(err)
	}
	if jsonOf(t, freq[0].Payload) != jsonOf(t, unw[0].Payload) {
		t.Fatal("frequency host: the exact test moved off the Σw table")
	}
	assertParams(t, "frequency", freq[0].Summary, map[string]float64{"sum_weights": ct.table.sw})
	if unw[0].Summary.Parameters != nil {
		t.Fatalf("unweighted host Parameters = %v", unw[0].Summary.Parameters)
	}

	_, _, err = ApplyOverlaysWithExtensions(specs, weightedCountHost(ctFixture, 2, 3, weighting.Probability), nil)
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_WEIGHT_UNSUPPORTED {
		t.Fatalf("probability host: %v, want PULSE_WEIGHT_UNSUPPORTED", err)
	}
	msg, details := weighting.FrequencyOnlyHostRefusal("overlays[0]", string(types.OverlayKindFisherExactCell))
	if ce.Message != msg || ce.Details["kind"] != details["kind"] || ce.Details["slot"] != "overlays[0]" {
		t.Fatalf("refusal %q %v", ce.Message, ce.Details)
	}

	// Floor keys say probability (n_eff present), the stamped cell says
	// frequency: the stamped weight wins.
	host := weightedCountHost(ctFixture, 2, 3, weighting.Probability).withCellWeight(&types.Aggregation{
		Type: types.AGG_COUNT, Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})})
	if _, _, err := ApplyOverlaysWithExtensions(specs, host, nil); err != nil {
		t.Fatalf("stamped-frequency host refused: %v", err)
	}
	// An unstamped cell (weight absent) leaves the floor inference.
	if weightedCountHost(ctFixture, 2, 3, weighting.Probability).withCellWeight(&types.Aggregation{Type: types.AGG_COUNT}).WeightBasis() != weighting.Probability {
		t.Fatal("an unstamped cell overrode the floor-key basis")
	}
}

// propHost is a 2 × 1 pairwise host whose cells are percentages over a
// weighted base: row i has base records bases[i], of which the first
// successes[i] are successes.
func propHost(bases [][]float64, successes []int, basis weighting.Basis) *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"aud"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnKeys:   []types.AxisKey{{"x"}},
	}
	comps := &types.CrosstabComponents{}
	for i, ws := range bases {
		var all, succ wsum
		for k, w := range ws {
			all.add(w)
			if k < successes[i] {
				succ.add(w)
			}
		}
		mx.RowKeys = append(mx.RowKeys, types.AxisKey{string(rune('A' + i))})
		mx.Cells = append(mx.Cells, []types.MatrixCell{{Value: 100 * succ.sw / all.sw, Present: true}})
		comps.CellComponents = append(comps.CellComponents, []map[string]any{floorMap(all, len(ws), basis)})
		comps.CellCounts = append(comps.CellCounts, []int{len(ws)})
		comps.RowMarginCounts = append(comps.RowMarginCounts, len(ws))
	}
	comps.ColumnMarginCounts = []int{len(bases[0]) + len(bases[1])}
	return newCrosstabHostViewWithComponents(mx, comps)
}

// TestPairwisePropZ_WeightedHost: p̂ = Σw_success/Σw_base (the weighted
// cell percentage), n = N*_base — Σw under frequency, Kish n_eff under
// probability — with n_source omitted; never the raw row count. The
// unweighted-count modes are PROCESSING_CONFIG on the weighted host;
// cell_weight_sum under frequency equals the omitted default.
func TestPairwisePropZ_WeightedHost(t *testing.T) {
	bases := [][]float64{{4, 1, 1, 6, 2, 1, 1, 3}, {1, 2, 2, 7, 1, 1}}
	successes := []int{3, 4}
	spec := func(nSource string) *types.OverlaySpec {
		return &types.OverlaySpec{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow,
			Params: mustParams(t, types.PairwiseOverlayParams{NSource: nSource})}
	}
	p := func(host *CrosstabHostView, nSource string) (types.OverlayLayer, error) {
		layer, _, err := applyPairwisePropZ(spec(nSource), host)
		return layer, err
	}
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		var ref [2]struct{ p, n, sw, sww float64 }
		for i, ws := range bases {
			var all, succ wsum
			for k, w := range ws {
				all.add(w)
				if k < successes[i] {
					succ.add(w)
				}
			}
			n := all.sw
			if basis == weighting.Probability {
				n = all.sw * all.sw / all.sww
			}
			ref[i].p, ref[i].n, ref[i].sw, ref[i].sww = succ.sw/all.sw, n, all.sw, all.sww
		}
		want, _ := twoProportionZ(ref[0].p*ref[0].n, ref[0].n, ref[1].p*ref[1].n, ref[1].n)
		rawN, _ := twoProportionZ(ref[0].p*8, 8, ref[1].p*6, 6)
		layer, err := p(propHost(bases, successes, basis), "")
		if err != nil {
			t.Fatal(err)
		}
		got := layer.Payload.Matrix.Cells[0][0].Value.(float64)
		relClose(t, "prop z", got, want, 1e-12)
		if math.Abs(got-rawN) < 1e-9 {
			t.Fatal("the weighted answer equals the raw-n answer")
		}
		params := map[string]float64{"sum_weights": ref[0].sw + ref[1].sw}
		if basis == weighting.Probability {
			s := ref[0].sw + ref[1].sw
			params["n_eff"] = s * s / (ref[0].sww + ref[1].sww)
		}
		assertParams(t, "prop z", layer.Summary, params)

		for _, mode := range []string{types.PairwiseNSourceCellNUnweighted, types.PairwiseNSourceNWithin, types.PairwiseNSourceRowMarginDistinct} {
			_, err := p(propHost(bases, successes, basis), mode)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG ||
				ce.Message != "overlay OVERLAY_PAIRWISE_PROP_Z n_source "+mode+": "+weighting.NSourceRefusal(mode, basis) {
				t.Fatalf("basis %v n_source %s: %v, want PROCESSING_CONFIG", basis, mode, err)
			}
		}
		if basis == weighting.Frequency {
			sum, err := p(propHost(bases, successes, basis), types.PairwiseNSourceCellWeightSum)
			if err != nil || sum.Payload.Matrix.Cells[0][0].Value != layer.Payload.Matrix.Cells[0][0].Value {
				t.Fatalf("frequency cell_weight_sum: %v %v, want the default's p", sum.Payload.Matrix, err)
			}
		}
	}
	// Unweighted host: the default reads the counted n, no Parameters.
	layer, err := p(propHost(bases, successes, weighting.Unweighted), "")
	if err != nil || layer.Summary.Parameters != nil {
		t.Fatalf("unweighted: %v %v", layer.Summary, err)
	}
}

// composeCountResponse wraps a weighted-count host as a Compose slot.
func composeCountResponse(recs []ctRec, basis weighting.Basis) *types.Response {
	h := weightedCountHost(recs, 2, 3, basis)
	return &types.Response{Crosstab: &types.CrosstabResult{Matrix: h.payload},
		Components: &types.ResponseComponents{Crosstab: h.components}}
}

// ctRefFixture is the reference slot: same cells, other weights.
var ctRefFixture = []ctRec{
	{0, 0, 2}, {0, 0, 2}, {0, 0, 3},
	{0, 1, 1}, {0, 1, 1}, {0, 1, 1},
	{0, 2, 4}, {0, 2, 2},
	{1, 0, 1}, {1, 0, 5}, {1, 0, 1},
	{1, 1, 2}, {1, 1, 2},
	{1, 2, 6}, {1, 2, 1}, {1, 2, 1},
}

// TestPropZCell_WeightedSlots: per cell, successes and n are the cell
// and row margin Σw scaled by the row base's c = n_eff/Σw under
// probability (n = the margin's Kish n_eff), read as is under frequency;
// the row-margin floor and the cell-union fallback agree; a
// probability row with no readable floor is a missing margin, never Σw.
func TestPropZCell_WeightedSlots(t *testing.T) {
	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell, Reference: "ref", Targets: []string{"t"}}
	tc, rc := tallyCT(ctFixture, 2, 3), tallyCT(ctRefFixture, 2, 3)
	for _, basis := range []weighting.Basis{weighting.Unweighted, weighting.Frequency, weighting.Probability} {
		target, ref := composeCountResponse(ctFixture, basis), composeCountResponse(ctRefFixture, basis)
		layer, _, err := applyPropZCell(spec, ref, []*types.Response{target}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			ct, cr := 1.0, 1.0
			if basis == weighting.Probability {
				ct = tc.row[i].sw / tc.row[i].sww
				cr = rc.row[i].sw / rc.row[i].sww
			}
			for j := 0; j < 3; j++ {
				want, _ := twoProportionZ(ct*tc.cell[i][j].sw, ct*tc.row[i].sw, cr*rc.cell[i][j].sw, cr*rc.row[i].sw)
				relClose(t, "prop z cell", layer.Payload.Matrix.Cells[i][j].Value.(float64), want, 1e-12)
			}
		}
		if !basis.Weighted() {
			if layer.Summary.Parameters != nil {
				t.Fatalf("unweighted Parameters = %v", layer.Summary.Parameters)
			}
			continue
		}
		want := map[string]float64{"sum_weights": tc.table.sw + rc.table.sw}
		if basis == weighting.Probability {
			var sww float64
			for i := 0; i < 2; i++ {
				sww += tc.row[i].sww + rc.row[i].sww
			}
			want["n_eff"] = want["sum_weights"] * want["sum_weights"] / sww
		}
		assertParams(t, "prop z cell", layer.Summary, want)

		// The row margin's own floor is the base read first: a margin
		// whose n_eff disagrees with its cells moves that row (only
		// under probability, where n_eff is the base's n).
		if basis == weighting.Probability {
			moved := composeCountResponse(ctFixture, basis)
			moved.Components.Crosstab.RowMarginComponents[0]["n_eff"] = tc.row[0].sw / 2
			alt, _, err := applyPropZCell(spec, ref, []*types.Response{moved}, 0, []int{1})
			if err != nil {
				t.Fatal(err)
			}
			if alt.Payload.Matrix.Cells[0][0].Value == layer.Payload.Matrix.Cells[0][0].Value ||
				alt.Payload.Matrix.Cells[1][0].Value != layer.Payload.Matrix.Cells[1][0].Value {
				t.Fatal("the row margin's own n_eff is not the base read")
			}
		}

		// Margin components dropped: the cell-union fallback answers the
		// same (the cells partition the row).
		target.Components.Crosstab.RowMarginComponents = nil
		ref.Components.Crosstab.RowMarginComponents = nil
		again, _, err := applyPropZCell(spec, ref, []*types.Response{target}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		for i := range again.Payload.Matrix.Cells {
			for j := range again.Payload.Matrix.Cells[i] {
				relClose(t, "fallback", again.Payload.Matrix.Cells[i][j].Value.(float64), layer.Payload.Matrix.Cells[i][j].Value.(float64), 1e-12)
			}
		}
	}

	// A probability slot whose row 0 carries no readable floor: that
	// row's cells are missing-margin NaN, row 1 still answers.
	target, ref := composeCountResponse(ctFixture, weighting.Probability), composeCountResponse(ctRefFixture, weighting.Probability)
	target.Components.Crosstab.RowMarginComponents = nil
	for j := range target.Components.Crosstab.CellComponents[0] {
		delete(target.Components.Crosstab.CellComponents[0][j], "sum_weights")
	}
	layer, ws, err := applyPropZCell(spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if v := layer.Payload.Matrix.Cells[0][0].Value.(float64); !math.IsNaN(v) {
		t.Fatalf("row without a floor answered %v, want NaN", v)
	}
	if v := layer.Payload.Matrix.Cells[1][0].Value.(float64); math.IsNaN(v) {
		t.Fatal("row 1 lost its answer")
	}
	if len(ws) != 3 || ws[0].Details["margin_missing"] != true {
		t.Fatalf("warnings = %+v, want three margin_missing", ws)
	}
}

// TestPropZPanel_WeightedSlots: the default value mode scales each
// slot's value and n by its row base's Kish c under probability; an
// unweighted-count mode on a weighted slot is PROCESSING_CONFIG.
func TestPropZPanel_WeightedSlots(t *testing.T) {
	tc, rc := tallyCT(ctFixture, 2, 3), tallyCT(ctRefFixture, 2, 3)
	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindPropZPanel, Scope: types.OverlayScopeCell, Reference: "ref", Targets: []string{"t"}}
	for _, basis := range []weighting.Basis{weighting.Frequency, weighting.Probability} {
		target, ref := composeCountResponse(ctFixture, basis), composeCountResponse(ctRefFixture, basis)
		layer, _, err := applyPropZPanel(spec, ref, []*types.Response{target}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			ct, cr := 1.0, 1.0
			if basis == weighting.Probability {
				ct = tc.row[i].sw / tc.row[i].sww
				cr = rc.row[i].sw / rc.row[i].sww
			}
			for j := 0; j < 3; j++ {
				want, _ := twoProportionZ(cr*rc.cell[i][j].sw, cr*rc.row[i].sw, ct*tc.cell[i][j].sw, ct*tc.row[i].sw)
				relClose(t, "panel pair", layer.Payload.Matrix.Cells[i][j].Value.([]float64)[0], want, 1e-12)
			}
		}
		relClose(t, "panel Parameters.sum_weights", layer.Summary.Parameters["sum_weights"], tc.table.sw+rc.table.sw, 1e-12)

		cnSpec := *spec
		cnSpec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}
		_, _, err = applyPropZPanel(&cnSpec, ref, []*types.Response{target}, 0, []int{1})
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG ||
			ce.Message != "overlay OVERLAY_PROP_Z_PANEL n_source cell_n_unweighted: "+weighting.NSourceRefusal("cell_n_unweighted", basis) {
			t.Fatalf("cell_n_unweighted on a weighted slot: %v", err)
		}
	}
}

// TestChiSqVsRef_WeightedTarget: under probability the target's Σw table
// is scaled to its Kish n_eff (χ² = c × the Σw statistic, the reference a
// fixed distribution); frequency reads it as is; Parameters carry the
// target's floor.
func TestChiSqVsRef_WeightedTarget(t *testing.T) {
	spec := &types.ComposeOverlaySpec{Kind: types.OverlayKindChiSqVsRef, Scope: types.OverlayScopeMatrix, Reference: "ref", Targets: []string{"t"}}
	tc := tallyCT(ctFixture, 2, 3)
	stat := func(basis weighting.Basis) *types.OverlaySummary {
		layer, _, err := applyChiSqVsRef(spec, composeCountResponse(ctRefFixture, basis), []*types.Response{composeCountResponse(ctFixture, basis)}, 0, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		return layer.Summary
	}
	unw, freq, prob := stat(weighting.Unweighted), stat(weighting.Frequency), stat(weighting.Probability)
	if *freq.Statistic != *unw.Statistic {
		t.Fatal("frequency target moved off the Σw table")
	}
	c := tc.table.sw / tc.table.sww
	relClose(t, "probability χ²", *prob.Statistic, c**unw.Statistic, 1e-12)
	assertParams(t, "probability", prob, map[string]float64{"df": 5, "sum_weights": tc.table.sw, "n_eff": tc.table.sw * tc.table.sw / tc.table.sww})
	assertParams(t, "frequency", freq, map[string]float64{"df": 5, "sum_weights": tc.table.sw})
	if len(unw.Parameters) != 1 {
		t.Fatalf("unweighted Parameters = %v", unw.Parameters)
	}
}
