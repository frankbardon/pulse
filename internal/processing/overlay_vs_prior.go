package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// The inferential prior-period kinds (U47 #328): OVERLAY_T_VS_PRIOR,
// OVERLAY_Z_VS_PRIOR and OVERLAY_PROP_Z_VS_PRIOR test each period against
// the PRESENT period before it, reusing the OVERLAY_PAIRWISE_* kernels
// (pairwiseWelchTKernel, pairwiseTwoMeansZKernel, pairwisePropZKernel) —
// leg 1 is the current period, leg 2 the prior, so the statistic is
// current minus prior.
//
// Two hosts, one walk (priorTestWalk):
//
//   - SERIES (grouped Process): the periods are the host rows, run per
//     params.partition_by partition by runSeriesHandlerPartitioned; the
//     legs come from the scored aggregation's per-group components
//     (seriesLegSource, attached by withPriorLegs).
//   - MATRIX (a crosstab with no column grouper): the periods are the row
//     keys of the implicit Total column, partitioned here on the leading
//     row keys (seriespartition.Depth over the row groupers); the legs
//     come from the cell components (CrosstabHostView.MeanLeg, the
//     pairwise base n).
//
// Each leg is read through the weighted-host rule: a weighted T / Z leg
// is (mean, variance on w*, N*) (readMeanLeg), a weighted proportion
// leg's n is N* (sum_weights under frequency, n_eff under probability).
//
// Per period: a period with no value (an absent or undefined figure) is
// MISSING — it keeps its key, carries no statistic and does not advance
// the prior; the first present period of a partition has no prior and
// carries a null (NaN) statistic and NO p-value (no test ran, so it is
// not a member of any multiplicity family); a degenerate test (n too
// small, zero SE, a proportion outside [0, 1]) carries a null statistic
// and a null p-value and is tallied into one PULSE_OVERLAY_REF_ZERO
// warning per reason.

// seriesLegSource is the SERIES host's leg source for the prior-period
// kinds: the scored aggregation's per-row components (Response.Data
// order) and its type.
type seriesLegSource struct {
	groups []types.AggregationGroupComponents
	// rows maps a host ordinal to its groups index (nil: identity) — a
	// partition's sub-host reads its own rows of the parent's groups.
	rows []int
	// off: the host was built with components disabled (no
	// Response.Components, or no per-group entries for the measure).
	off bool
	// measure is the scored aggregation's type.
	measure types.AggregationType
}

// withPriorLegs attaches the prior-period leg source for the scored
// aggregation req.Aggregations[aggIndex] to h (a no-op when h already
// carries one or is nil). Called by the grouped Process hook only for
// a spec of those kinds.
func withPriorLegs(h *SeriesHostView, req *types.Request, resp *types.Response, aggIndex int) {
	if h == nil || h.legs != nil || aggIndex < 0 || aggIndex >= len(req.Aggregations) || req.Aggregations[aggIndex] == nil {
		return
	}
	agg := req.Aggregations[aggIndex]
	src := &seriesLegSource{measure: agg.Type}
	if resp == nil || resp.Components == nil {
		src.off = true
	} else {
		src.groups = scoredGroupComponents(resp, types.AggregationLabelOf(agg), aggIndex)
		src.off = src.groups == nil
	}
	h.legs = src
}

func (s *seriesLegSource) subset(rows []int) *seriesLegSource {
	if s == nil {
		return nil
	}
	out := *s
	out.rows = make([]int, len(rows))
	for j, i := range rows {
		out.rows[j] = s.index(i)
	}
	return &out
}

func (s *seriesLegSource) index(i int) int {
	if s.rows == nil {
		return i
	}
	if i < 0 || i >= len(s.rows) {
		return -1
	}
	return s.rows[i]
}

func (s *seriesLegSource) at(i int) (*types.AggregationGroupComponents, bool) {
	j := s.index(i)
	if j < 0 || j >= len(s.groups) {
		return nil, false
	}
	return &s.groups[j], true
}

// hasWelford reports whether any row carries the {mean, variance}
// operator keys — the runtime reading of an EXTENSION measure's
// Welford shape (a built-in is judged by type, PriorWelfordMeasure).
func (s *seriesLegSource) hasWelford() bool {
	for i := range s.groups {
		_, m := s.groups[i].Operator["mean"]
		_, v := s.groups[i].Operator["variance"]
		if m && v {
			return true
		}
	}
	return false
}

// welfordLeg reads ordinal i's two-sample leg through the weighted-host
// rule (readMeanLeg); an unweighted AGG_VARIANCE / AGG_STDDEV leg's
// population variance becomes the sample variance, as MeanLeg does for
// a crosstab cell.
func (s *seriesLegSource) welfordLeg(i int) (meanLeg, bool) {
	g, ok := s.at(i)
	if !ok {
		return meanLeg{}, false
	}
	m := map[string]any{"n": g.N}
	for _, k := range []string{"mean", "variance", "m2"} {
		if x, ok := g.Operator[k]; ok {
			m[k] = x
		}
	}
	if g.SumWeights != nil {
		m["sum_weights"] = *g.SumWeights
	}
	if g.NEff != nil {
		m["n_eff"] = *g.NEff
	}
	leg, ok := readMeanLeg(m)
	if ok && leg.basis == weighting.Unweighted && overlayhost.PopulationVarianceCell(s.measure) {
		m2, hasM2 := componentsNumeric(m, "m2")
		leg.variance = overlayhost.SampleVariance(leg.variance, m2, hasM2, leg.nStar)
	}
	return leg, ok
}

// floorN is ordinal i's proportion n: N* on a weighted row (n_eff under
// probability, Σw under frequency), the non-null count otherwise.
func (s *seriesLegSource) floorN(i int) (float64, bool) {
	g, ok := s.at(i)
	if !ok {
		return 0, false
	}
	switch {
	case g.NEff != nil:
		return *g.NEff, true
	case g.SumWeights != nil:
		return *g.SumWeights, true
	}
	return float64(g.N), true
}

// priorSide is one period's inputs: the T / Z kinds read mean /
// variance / n, the proportion kind p / n.
type priorSide struct {
	mean, variance, p, n float64
}

// priorTestKernel is route's two-sample kernel.
func priorTestKernel(route types.OverlayKind) pwKernel {
	switch route {
	case types.OverlayKindTVsPrior:
		return pairwiseWelchTKernel
	case types.OverlayKindZVsPrior:
		return pairwiseTwoMeansZKernel
	}
	return pairwisePropZKernel
}

// priorTally folds degenerate periods into one warning per reason.
type priorTally struct {
	order  []string
	counts map[string]int
	sample map[string]int
}

func (t *priorTally) add(reason string, row int) {
	if t.counts == nil {
		t.counts, t.sample = map[string]int{}, map[string]int{}
	}
	if _, seen := t.counts[reason]; !seen {
		t.order = append(t.order, reason)
		t.sample[reason] = row
	}
	t.counts[reason]++
}

func (t *priorTally) warnings(spec *types.OverlaySpec) []types.OverlayWarning {
	if len(t.order) == 0 {
		return nil
	}
	out := make([]types.OverlayWarning, 0, len(t.order))
	for _, reason := range t.order {
		out = append(out, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " skipped " + strconv.Itoa(t.counts[reason]) + " period(s): " + reason,
			Details: map[string]any{
				"kind":       string(spec.Kind),
				"reason":     reason,
				"skipped":    t.counts[reason],
				"sample_row": t.sample[reason],
			},
		})
	}
	return out
}

// priorTestWalk runs route's test down each partition (parts nil: the
// whole host as one) and returns the SERIES layer and its warnings.
// side reads period i (ok false: missing).
func priorTestWalk(spec *types.OverlaySpec, route types.OverlayKind, keys []types.AxisKey, side func(i int) (priorSide, bool), parts [][]int) (types.OverlayLayer, []types.OverlayWarning) {
	if parts == nil {
		all := make([]int, len(keys))
		for i := range all {
			all[i] = i
		}
		parts = [][]int{all}
	}
	kernel := priorTestKernel(route)
	entries := make([]types.SeriesEntry, len(keys))
	for i, k := range keys {
		if k != nil {
			entries[i].Key = append(types.AxisKey(nil), k...)
		}
	}
	var (
		tally    priorTally
		minV     float64
		maxV     float64
		tested   int
		nan      = math.NaN()
		nanValue = func() *float64 { v := nan; return &v }
	)
	for _, part := range parts {
		var (
			prior priorSide
			armed bool
		)
		for _, i := range part {
			cur, ok := side(i)
			if !ok {
				continue
			}
			s := &entries[i].Summary
			if !armed {
				// No prior: no test ran. A null statistic, no p-value.
				s.Statistic = nanValue()
				prior, armed = cur, true
				continue
			}
			out, reason, ok := kernel(pwInputs{
				m1: cur.mean, m2: prior.mean,
				v1: cur.variance, v2: prior.variance,
				p1: cur.p, p2: prior.p,
				nf1: cur.n, nf2: prior.n,
			})
			prior = cur
			if !ok {
				s.Statistic, s.PValue = nanValue(), nanValue()
				tally.add(reason, i)
				continue
			}
			stat, p := out.stat, out.p
			s.Statistic, s.PValue = &stat, &p
			if tested == 0 || stat < minV {
				minV = stat
			}
			if tested == 0 || stat > maxV {
				maxV = stat
			}
			tested++
		}
	}
	summary := &types.OverlaySummary{Count: &tested}
	if tested > 0 {
		mn, mx := minV, maxV
		summary.Min, summary.Max = &mn, &mx
	}
	warns := tally.warnings(spec)
	return types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeSeries,
			Series: &types.SeriesPayload{Entries: entries},
		},
		Summary:  summary,
		Warnings: warns,
	}, warns
}

// priorTestSeriesHandler is route's SERIES-host handler.
func priorTestSeriesHandler(route types.OverlayKind) seriesOverlayHandler {
	return func(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
		if host == nil {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView", map[string]any{"kind": string(spec.Kind)})
		}
		src := host.legs
		if src == nil {
			// A caller-built host (Pulse.ApplySeriesOverlays) carries no
			// components to read a leg from.
			src = &seriesLegSource{off: true}
		}
		if cerr := overlayhost.PriorMeasureRefusal(spec.Kind, route, src.measure, -1); cerr != nil {
			return types.OverlayLayer{}, nil, cerr
		}
		if src.off {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
				overlayhost.PriorComponentsRequiredMessage(spec.Kind), map[string]any{"kind": string(spec.Kind)})
		}
		welford := overlayhost.PriorTestUsesWelford(route)
		if welford && !overlayhost.PriorMeasureBuiltin(src.measure) && !src.hasWelford() {
			return types.OverlayLayer{}, nil, overlayhost.PriorWelfordRefusal(spec.Kind, src.measure, -1)
		}
		side := func(i int) (priorSide, bool) {
			if welford {
				leg, ok := src.welfordLeg(i)
				if !ok || math.IsNaN(leg.mean) {
					return priorSide{}, false
				}
				return priorSide{mean: leg.mean, variance: leg.variance, n: leg.nStar, p: math.NaN()}, true
			}
			v, ok := host.ValueAt(i)
			if !ok {
				return priorSide{}, false
			}
			n, ok := src.floorN(i)
			if !ok {
				return priorSide{}, false
			}
			return priorSide{p: v, n: n, mean: math.NaN(), variance: math.NaN()}, true
		}
		layer, warns := priorTestWalk(spec, route, host.groupKeys, side, nil)
		return layer, warns, nil
	}
}

// applyPriorTestMatrixAt is route's MATRIX-host handler (a crosstab with
// no column grouper: the dispatcher's refuseWrongHostOverlays pre-pass
// refused any other crosstab). Its refusals carry the spec index, as
// predict's do.
func applyPriorTestMatrixAt(spec *types.OverlaySpec, route types.OverlayKind, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	// params.field names the cell (the host's one measure).
	var cells []*types.Aggregation
	if host.cell != nil {
		cells = []*types.Aggregation{host.cell}
	}
	if len(cells) > 0 {
		if _, err := seriesfield.Resolve(cells, spec, index, nil); err != nil {
			return types.OverlayLayer{}, nil, err
		}
	}
	depth := host.RowAxisDepth() - 1
	if host.rowGroups != nil {
		d, err := seriespartition.Depth(host.rowGroups, spec, index)
		if err != nil {
			return types.OverlayLayer{}, nil, err
		}
		depth = d
	}
	if depth < 0 {
		depth = 0
	}
	if cerr := overlayhost.PriorMeasureRefusal(spec.Kind, route, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.PriorNormalizedRefusal(spec.Kind, route, host.normalize, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if !host.HasComponents() {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
			overlayhost.PriorComponentsRequiredMessage(spec.Kind), map[string]any{"index": index, "kind": string(spec.Kind)})
	}
	welford := overlayhost.PriorTestUsesWelford(route)
	if welford && !overlayhost.PriorMeasureBuiltin(host.cellType) && !host.HasWelfordCells() {
		return types.OverlayLayer{}, nil, overlayhost.PriorWelfordRefusal(spec.Kind, host.cellType, index)
	}
	basis := host.WeightBasis()
	side := func(r int) (priorSide, bool) {
		if welford {
			leg, ok := host.MeanLeg(r, 0)
			if !ok || math.IsNaN(leg.mean) {
				return priorSide{}, false
			}
			return priorSide{mean: leg.mean, variance: leg.variance, n: leg.nStar, p: math.NaN()}, true
		}
		v, ok := host.CellAt(r, 0)
		if !ok || math.IsNaN(v) {
			return priorSide{}, false
		}
		// The cell's base n (U47 #324): under normalize none the cell is
		// its own base — its non-null count, or N* when weighted.
		n, ok := pairwiseSampleSize(host, types.PairwiseOverlayParams{}, basis, true, r, 0)
		if !ok {
			return priorSide{}, false
		}
		return priorSide{p: v, n: n, mean: math.NaN(), variance: math.NaN()}, true
	}
	keys := make([]types.AxisKey, host.RowCount())
	for r := range keys {
		keys[r] = host.rowKey(r)
	}
	layer, warns := priorTestWalk(spec, route, keys, side, seriespartition.Partition(keys, depth))
	return layer, warns, nil
}
