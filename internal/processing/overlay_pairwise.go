package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// This file implements the OVERLAY_PAIRWISE_* family: intra-matrix
// axis-pairwise significance overlays that test one host-matrix slot
// against another ALONG a single axis of the same materialised crosstab
// (row-vs-row for each column under ROW scope; column-vs-column for each
// row under COLUMN scope). It is the per-Request counterpart to the
// Compose-host OVERLAY_PROP_Z_PANEL — where the panel pairs across slots,
// this family pairs across the host's own axis indices.
//
// Output: a MATRIX payload whose PAIR axis carries one entry per evaluated
// (i, j) index pair (key = the 2-tuple of the compared legs' display
// labels) and whose OPPOSITE axis echoes the host's other axis. Each cell
// holds the pair's two-sided p-value at that opposite-axis position, or is
// absent when a leg was unreadable or the test degenerate. The primary
// carries RAW p-values only; params.letters adds the `letters` part
// (overlay_pairwise_letters.go), and min-n flags stay the embedder's
// presentation concern.
//
// All five kinds reuse the shared harness below; only the per-pair Compute
// kernel and the input shape (proportion + n, Welford triple, or
// weighted moments) differ.

// applyPairwisePropZ / ...ProbitT / ...WelchT / ...TwoMeansZ are the
// dispatch entry points registered in overlayHandlers. Each pins the kind
// and delegates to the shared runner.
func applyPairwisePropZ(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwisePropZKernel, pwShapeProportion)
}

func applyPairwiseProbitT(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseProbitTKernel, pwShapeProportion)
}

func applyPairwiseWelchT(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseWelchTKernel, pwShapeWelford)
}

func applyPairwiseTwoMeansZ(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return runPairwiseOverlay(spec, host, pairwiseTwoMeansZKernel, pwShapeWelford)
}

// applyPairwiseWeightedTwoMeansZ refuses the request-shape faults up
// front — n_basis missing or unknown, n_source / p_source present — then
// runs the shared harness over the AGG_WEIGHTED_MEAN moments with the
// kernel bound to the chosen n_basis.
//
// The n_source / p_source refusal is a hard RUNTIME refusal here, unlike
// the Welford siblings (predict-only, so no previously-succeeding request
// starts failing): this kind is new, so refusing breaks no caller, and a
// selector that is silently inert is the failure the refusal exists to
// remove.
func applyPairwiseWeightedTwoMeansZ(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	paramread.Declare(paramread.Overlay, string(spec.Kind), (*types.PairwiseOverlayParams)(nil))
	params, err := types.DecodePairwiseParams(spec.Params)
	if err != nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" has malformed Params: "+err.Error(),
			map[string]any{"kind": string(spec.Kind)})
	}
	if err := checkWeightedTwoMeansZParams(spec, params); err != nil {
		return types.OverlayLayer{}, nil, err
	}
	// n_basis "weights" on a probability host reads Σw as n — refused
	// like the n_source weight sum (weighting.NBasisRefusal, U12 review
	// WS-06); predict raises the same code and message.
	if reason := weighting.NBasisRefusal(params.NBasis, host.WeightBasis()); reason != "" {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"overlay "+string(spec.Kind)+" n_basis "+params.NBasis+": "+reason,
			map[string]any{"kind": string(spec.Kind), "param": "n_basis", "n_basis": params.NBasis})
	}
	return runPairwiseOverlay(spec, host, pairwiseWeightedTwoMeansZKernel(params.NBasis), pwShapeWeighted)
}

// checkWeightedTwoMeansZParams is the runtime param contract of
// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z. Every refusal carries
// PULSE_OVERLAY_PARAM_MISSING, the code the Welford siblings use for an
// inert or unknown selector.
func checkWeightedTwoMeansZParams(spec *types.OverlaySpec, params types.PairwiseOverlayParams) error {
	const inertReason = "mean, variance and sample size all come from the AGG_WEIGHTED_MEAN moments " +
		"{weighted_mean, m2_weighted, sum_weights, sum_weights_sq} under n_basis, so the selector is inert on this kind"
	if params.NSource != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" does not accept n_source ("+params.NSource+"): "+inertReason+
				". Remove n_source and choose the sample-size convention with n_basis",
			map[string]any{"kind": string(spec.Kind), "param": "n_source", "n_source": params.NSource})
	}
	if params.PSource != "" {
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" does not accept p_source ("+params.PSource+"): "+inertReason+
				". Remove p_source",
			map[string]any{"kind": string(spec.Kind), "param": "p_source", "p_source": params.PSource})
	}
	if !types.ValidPairwiseNBasis(params.NBasis) {
		msg := "overlay " + string(spec.Kind) + " requires params.n_basis (\"" + types.PairwiseNBasisWeights +
			"\" or \"" + types.PairwiseNBasisKish + "\"); there is no default"
		if params.NBasis != "" {
			msg = "overlay " + string(spec.Kind) + " has unknown n_basis: " + params.NBasis +
				" (supports: " + types.PairwiseNBasisWeights + ", " + types.PairwiseNBasisKish + ")"
		}
		return errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING, msg,
			map[string]any{"kind": string(spec.Kind), "param": "n_basis", "n_basis": params.NBasis,
				"supported": []string{types.PairwiseNBasisWeights, types.PairwiseNBasisKish}})
	}
	return nil
}

// pwShape names a pairwise kind's per-leg input shape: what the shape
// gate checks for and what extractPairInputs reads.
type pwShape int

const (
	// pwShapeProportion: a proportion leg (p_source) + an n leg (n_source).
	pwShapeProportion pwShape = iota
	// pwShapeWelford: the AGG_WELFORD triple {mean, variance, n}.
	pwShapeWelford
	// pwShapeWeighted: the AGG_WEIGHTED_MEAN moments {weighted_mean,
	// m2_weighted, sum_weights, sum_weights_sq}.
	pwShapeWeighted
)

// pwInputs carries one pair's two legs. Proportion kinds populate p1/p2 +
// nf1/nf2 (m/v are NaN); Welford kinds populate m/v/nf (p is NaN); the
// weighted kind populates w1/w2 only.
type pwInputs struct {
	p1, p2 float64
	m1, m2 float64
	v1, v2 float64
	// nf1 / nf2 are the legs' sample sizes. Welford legs: the raw n on
	// an unweighted cell, N* on a weighted one (CrosstabHostView.MeanLeg).
	// Proportion legs: the n_source figure (an integer count, exactly
	// representable) or, on a weighted host, a floor's N* — with
	// n_source omitted the cell's BASE (pairwiseSampleSize).
	nf1, nf2 float64
	w1, w2   weightedMoments
}

// pwKernel computes one pair's two-sided p-value and the signed direction
// of its test statistic. ok=false with a reason string signals a
// degenerate / unreadable pair that stays absent on the payload and folds
// into one aggregated warning per reason.
type pwKernel func(in pwInputs) (out pwOutcome, reason string, ok bool)

// pwOutcome is one scored pair: its two-sided p-value and dir, the sign of
// the test statistic — +1 when leg i (pair_a) is the higher, -1 when leg j
// (pair_b) is, 0 on a tie. The letters part reads dir to know which leg a
// significant pair puts above the other.
type pwOutcome struct {
	p   float64
	dir float64
}

// signOf is the direction of a statistic: +1, -1, or 0 (a tie, and a NaN
// difference, which never reaches a scored pair).
func signOf(d float64) float64 {
	switch {
	case d > 0:
		return 1
	case d < 0:
		return -1
	}
	return 0
}

// pairwisePair is one evaluated (i, j) index pair along the pair axis,
// carrying each leg's display label for the output key.
type pairwisePair struct {
	i, j           int
	labelI, labelJ string
}

// pairwiseGateResult is what pairwiseGate resolves for one host: the
// decoded params (p_source resolved against the host's normalize), the
// host's weight basis and whether its proportion / Welford legs read N*.
type pairwiseGateResult struct {
	params       types.PairwiseOverlayParams
	basis        weighting.Basis
	weightedHost bool
}

// pairwiseGate runs every up-front refusal of a pairwise kind against one
// single-measure host — params, the components gate, the per-shape cell
// gates, the weighted-host n_source rule, p_source on a normalised host
// and the distinct-key admission — and resolves the facts the pair loop
// reads. A multi-measure host runs it once per measure, so a refusal is
// raised exactly as the single-measure crosstab of that measure raises it.
func pairwiseGate(spec *types.OverlaySpec, host *CrosstabHostView, shape pwShape) (pairwiseGateResult, error) {
	paramread.Declare(paramread.Overlay, string(spec.Kind), (*types.PairwiseOverlayParams)(nil))
	params, err := types.DecodePairwiseParams(spec.Params)
	if err != nil {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" has malformed Params: "+err.Error(),
			map[string]any{"kind": string(spec.Kind)})
	}
	// Significance letters (U47 #327): the alpha rule predict raises with
	// the same code and message (types.PairwiseLettersRefusal).
	if msg := types.PairwiseLettersRefusal(spec.Kind, params); msg != "" {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING, msg,
			map[string]any{"kind": string(spec.Kind), "param": "letters", "alpha": *params.Letters.Alpha})
	}

	// Components gate — every kind reads per-cell counters / Welford
	// triples / margin counts. Without them there is no sample-size leg.
	if !host.HasComponents() {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_COMPONENTS_REQUIRED,
			overlayhost.ComponentsRequiredMessage(spec.Kind),
			map[string]any{"kind": string(spec.Kind)})
	}
	// Welford-input kinds need {mean, variance, n} on at least one cell.
	if shape == pwShapeWelford && !host.HasWelfordCells() {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			overlayhost.WelfordCellMessage(spec.Kind),
			map[string]any{"kind": string(spec.Kind)})
	}
	// Weighted-host n_source rule (weighting.NSourceRefusal): on a host
	// whose crosstab cell is weighted the Welford legs read N*, and so do
	// the proportion legs when n_source is omitted; an unweighted count
	// (raw rows, a slab, distinct keys) — or a weight sum under kind
	// probability — is PROCESSING_CONFIG, as predict reports it. The
	// weighted-moments kind keeps its own n_basis contract.
	basis := host.WeightBasis()
	weightedHost := false
	if shape != pwShapeWeighted {
		if err := overlayNSourceWeightedRefusal(spec.Kind, params.NSource, basis); err != nil {
			return pairwiseGateResult{}, err
		}
		weightedHost = basis.Weighted()
	}
	// Proportion legs on a normalised host (survey-tables E1-S3): every
	// cell is already a 0..1 share, so an explicit cell_value_pct (÷100)
	// is refused as predict refuses it, and the omitted p_source reads
	// the share as is (overlayhost.EffectivePairwisePSource).
	if shape == pwShapeProportion {
		if msg := overlayhost.PairwisePSourceRefusal(spec.Kind, params.PSource, host.normalize); msg != "" {
			return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING, msg,
				map[string]any{"kind": string(spec.Kind), "param": "p_source", "p_source": params.PSource,
					"normalize": string(host.normalize)})
		}
		params.PSource = overlayhost.EffectivePairwisePSource(params.PSource, host.normalize)
	}
	// The weighted kind needs the weighted moments on at least one cell
	// — emitted by AGG_WEIGHTED_MEAN and by a WEIGHTED AGG_AVERAGE (its
	// moments plus the weighted floor's sum_weights). An unweighted
	// AGG_AVERAGE or an AGG_WELFORD triple does not qualify: its variance
	// is unweighted and its n is a record count.
	if shape == pwShapeWeighted && !host.HasWeightedMomentCells() {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires AGG_WEIGHTED_MEAN or weighted AGG_AVERAGE cells (weighted moments "+
				"{weighted_mean, m2_weighted, sum_weights, sum_weights_sq} on CellComponents); host matrix has none",
			map[string]any{"kind": string(spec.Kind)})
	}

	// Distinct-key n admission — refused UP FRONT, on the cell
	// aggregator's IDENTITY, never per pair. AGG_MODE_COUNT and AGG_MODE
	// both emit a component literally spelled "distinct_count" that
	// counts distinct VALUES OF THE MEASURE FIELD; only
	// AGG_DISTINCT_SUM's means distinct KEYS, and AGG_DISTINCT_COUNT
	// spells its figure "cardinality". A key-PRESENCE gate would read
	// the number of distinct answer codes and call it a sample size, so
	// the gate identifies the aggregator instead. The per-cell key probe
	// inside the slab accessors is a convenience on top of this decision,
	// not a substitute for it: do not simplify this gate away.
	//
	// Fires for the Welford-input kinds too, exactly as the
	// n_within_depth range guard below does: those kinds ignore NSource,
	// so a request that names one would otherwise be silently dropped.
	//
	// Keyed off PairwiseNSourceReadsDistinctKeys, not one constant: the
	// MARGIN distinct modes read the same figure off the same cell
	// aggregator and need the same admission. Only the SLAB mode needs
	// the separate partition gate.
	// Welford-input arm agreement. A Welford kind reads n, mean and
	// variance out of the AGG_WELFORD triple, so NSource never reaches
	// the math and descriptor.validateOverlayPairwise refuses ANY
	// n_source on these kinds with PULSE_OVERLAY_PARAM_MISSING. That
	// predict refusal is deliberately NOT twinned in general (E2-S1: a
	// non-distinct selector stays inert and still runs here, so no
	// currently-succeeding pulse.Process starts failing).
	//
	// A DISTINCT selector is the one case the runtime ALREADY refuses,
	// on the admission gate immediately below — and it refused under
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, sending the caller to
	// fix the cell aggregator. That cannot help: predict refuses
	// n_source on a Welford kind whatever the host holds. So where the
	// runtime already refuses, it refuses under PREDICT's code, and the
	// two arms give one `pulse errors lookup` answer. Scoped to the
	// distinct modes on purpose — widening it would add runtime
	// failures E2-S1 decided against.
	if types.PairwiseKindUsesWelford(spec.Kind) && types.PairwiseNSourceReadsDistinctKeys(params.NSource) {
		return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" does not accept n_source ("+params.NSource+
				"): n, mean and variance all come from the AGG_WELFORD triple {mean, variance, n}, "+
				"so the mode selector is inert on this kind. Remove n_source"+remedyPairwiseNLeg(nil),
			map[string]any{
				"kind":     string(spec.Kind),
				"param":    "n_source",
				"n_source": params.NSource,
			})
	}

	if types.PairwiseNSourceReadsDistinctKeys(params.NSource) {
		if _, _, ok := host.AdmitsDistinctKeyN(); !ok {
			observed, identified := host.CellAggregatorIdentity()
			observedName := string(observed)
			if !identified {
				observedName = "unidentified"
			}
			admitted := PairwiseDistinctNAdmitted()
			admittedNames := make([]string, len(admitted))
			for i, a := range admitted {
				admittedNames[i] = string(a)
			}
			return pairwiseGateResult{}, errors.NewCodedErrorWithDetails(
				errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
				"overlay "+string(spec.Kind)+" n_source="+params.NSource+" requires a distinct-key cell aggregator; observed cell aggregator "+
					observedName+remedyDistinctNAdmitted(nil),
				map[string]any{
					"kind":                      string(spec.Kind),
					"n_source":                  params.NSource,
					"observed_cell_aggregator":  observedName,
					"admitted_cell_aggregators": admittedNames,
				})
		}
	}

	return pairwiseGateResult{params: params, basis: basis, weightedHost: weightedHost}, nil
}

func runPairwiseOverlay(spec *types.OverlaySpec, host *CrosstabHostView, kernel pwKernel, shape pwShape) (types.OverlayLayer, []types.OverlayWarning, error) {
	if host.measures != nil {
		return runPairwiseOverlayMeasures(spec, host, kernel, shape)
	}
	g, err := pairwiseGate(spec, host, shape)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	rowScope := spec.Scope == types.OverlayScopeRow

	pairs, groups, perr := buildPairwisePairs(spec, host, g.params, rowScope)
	if perr != nil {
		return types.OverlayLayer{}, nil, perr
	}

	acc := newPairwiseAccumulator()
	pvals, dirs := acc.evaluate(host, g, kernel, shape, pairs, rowScope, 0, nil)

	layer := buildPairwiseLayer(spec, host, pairs, pvals, rowScope)
	acc.legs.stamp(layer.Summary)
	if g.params.Letters != nil {
		dirGrid := layoutPairCells(dirs, rowScope, host.RowCount(), len(pairs))
		if err := attachPairwiseLetters(&layer, g.params, host.Payload(), dirGrid, groups); err != nil {
			return types.OverlayLayer{}, nil, err
		}
	}
	// Attach the per-reason skip diagnostics to the layer's own carrier
	// (in addition to the returned slice the orchestrator promotes to
	// Response.Warnings) so a per-Request consumer that reads layers
	// directly can associate each skip with its originating spec.
	warns := acc.tally.warnings(spec)
	layer.Warnings = warns
	return layer, warns, nil
}

// pairwiseAccumulator is the layer-wide state the pair loop folds into:
// the per-reason skip tally and the weighted legs the layer read, each
// leg once. A multi-measure layer shares ONE accumulator across its
// measures, so its summary and warnings describe the whole layer; leg
// identities carry the measure, so two measures never share a leg.
type pairwiseAccumulator struct {
	tally *pairwiseTally
	// The weighted legs the layer read, each cell once (a cell is a leg
	// of every pair it sits in), for the summary's Parameters.
	legs    weightedLegTally
	legSeen map[pairwiseLegID]bool
	// A proportion layer's n legs are bases, shared by every cell a base
	// divided: each is tallied once (nLegKey).
	nLegSeen map[pairwiseNLegID]bool
}

// pairwiseLegID / pairwiseNLegID identify a cell leg / an n leg within
// one measure (0 on a single-measure host).
type pairwiseLegID struct{ measure, r, c int }
type pairwiseNLegID struct {
	measure int
	key     nLegKey
}

func newPairwiseAccumulator() *pairwiseAccumulator {
	return &pairwiseAccumulator{
		tally:    newPairwiseTally(),
		legSeen:  map[pairwiseLegID]bool{},
		nLegSeen: map[pairwiseNLegID]bool{},
	}
}

// evaluate runs the kernel over every (pair, opposite index) of one
// single-measure host and returns pvals[pairPos][opp] — laid out
// pair-major; the caller transposes it into the output MatrixPayload per
// scope. measure namespaces the leg identities; oppIdx maps an opposite
// index to the coordinate a skip warning reports (nil: itself).
//
// dirs mirrors pvals cell for cell: each scored pair-cell's direction
// (pwOutcome.dir), absent where the p-value is.
func (a *pairwiseAccumulator) evaluate(host *CrosstabHostView, g pairwiseGateResult, kernel pwKernel, shape pwShape,
	pairs []pairwisePair, rowScope bool, measure int, oppIdx func(int) int) (pvals, dirs [][]types.MatrixCell) {
	params, basis := g.params, g.basis
	// Opposite-axis extent: ROW scope pairs rows, so the opposite axis is
	// columns; COLUMN scope pairs columns, opposite axis is rows.
	oppCount := host.ColumnCount()
	if !rowScope {
		oppCount = host.RowCount()
	}
	report := func(o int) int {
		if oppIdx == nil {
			return o
		}
		return oppIdx(o)
	}
	addLeg := func(r, c int) {
		id := pairwiseLegID{measure, r, c}
		if a.legSeen[id] {
			return
		}
		a.legSeen[id] = true
		if shape == pwShapeWelford {
			if l, ok := host.MeanLeg(r, c); ok {
				a.legs.add(l)
			}
			return
		}
		if l, key, ok := pairwiseWeightedNLeg(host, params, basis, r, c); ok && !a.nLegSeen[pairwiseNLegID{measure, key}] {
			a.nLegSeen[pairwiseNLegID{measure, key}] = true
			a.legs.add(l)
		}
	}

	pvals = make([][]types.MatrixCell, len(pairs))
	dirs = make([][]types.MatrixCell, len(pairs))
	for p := range pairs {
		pvals[p] = make([]types.MatrixCell, oppCount)
		dirs[p] = make([]types.MatrixCell, oppCount)
		for o := 0; o < oppCount; o++ {
			r1, c1 := pairwiseLegCoord(rowScope, pairs[p].i, o)
			r2, c2 := pairwiseLegCoord(rowScope, pairs[p].j, o)
			in, ok := extractPairInputs(host, params, shape, basis, rowScope, r1, c1, r2, c2)
			if !ok {
				a.tally.add("PULSE_STATS_SKIP_EXTRACT_FAILED", report(o))
				continue
			}
			if g.weightedHost {
				addLeg(r1, c1)
				addLeg(r2, c2)
			}
			out, reason, ok := kernel(in)
			if !ok {
				a.tally.add(reason, report(o))
				continue
			}
			pvals[p][o] = types.MatrixCell{Value: out.p, Present: true}
			dirs[p][o] = types.MatrixCell{Value: out.dir, Present: true}
		}
	}
	return pvals, dirs
}

// buildPairwisePairs enumerates (i, j) index pairs along the pair axis.
// PairAlongDim=nil yields the full upper triangular; a non-nil value
// buckets indexes by their axis-key tuple minus that dim and pairs only
// within each bucket. Out-of-range pair_along_dim / n_within_depth against
// the actual axis depth fail with PULSE_OVERLAY_PARAM_MISSING.
//
// groups lists the comparison groups in pair order — each a set of
// pair-axis indexes (ascending) that are paired with one another, so the
// pairs are exactly every a < b within each group, group by group. A
// lone index (nothing to compare it to — the implicit Total column, a
// singleton bucket) is in no group: the letters part gives it no letter.
func buildPairwisePairs(spec *types.OverlaySpec, host *CrosstabHostView, params types.PairwiseOverlayParams, rowScope bool) (pairs []pairwisePair, groups [][]int, err error) {
	count := host.ColumnCount()
	depth := host.ColumnAxisDepth()
	keyAt := host.columnKey
	labelAt := func(i int) string { return stringifyOverlayAxisKey(host.columnKey(i)) }
	if rowScope {
		count = host.RowCount()
		depth = host.RowAxisDepth()
		keyAt = host.rowKey
		labelAt = func(i int) string { return stringifyOverlayAxisKey(host.rowKey(i)) }
	}

	if types.PairwiseNSourceUsesWithinDepth(params.NSource) && params.NWithinDepth >= depth {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" n_within_depth exceeds pair-axis dim count",
			map[string]any{"kind": string(spec.Kind),
				"n_within_depth": params.NWithinDepth, "dim_count": depth})
	}

	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}

	if params.PairAlongDim == nil {
		out := make([]pairwisePair, 0, count*(count-1)/2)
		for a := 0; a < len(indexes); a++ {
			for b := a + 1; b < len(indexes); b++ {
				i, j := indexes[a], indexes[b]
				out = append(out, pairwisePair{i: i, j: j, labelI: labelAt(i), labelJ: labelAt(j)})
			}
		}
		if len(indexes) > 1 {
			groups = [][]int{indexes}
		}
		return out, groups, nil
	}

	dim := *params.PairAlongDim
	if dim >= depth {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
			"overlay "+string(spec.Kind)+" pair_along_dim out of range for pair-axis dim count",
			map[string]any{"kind": string(spec.Kind),
				"pair_along_dim": dim, "dim_count": depth})
	}

	// Bucket by tuple-minus-dim, preserving insertion order so pairs come
	// out in axis-natural order (mirrors the nil-PairAlongDim double loop).
	type bucket struct {
		key     types.AxisKey
		indexes []int
	}
	buckets := make([]*bucket, 0, count)
	for _, i := range indexes {
		ki := keyAt(i)
		if ki == nil || dim >= len(ki) {
			continue
		}
		matched := false
		for _, b := range buckets {
			if axisKeyEqualExceptDim(b.key, ki, dim) {
				b.indexes = append(b.indexes, i)
				matched = true
				break
			}
		}
		if !matched {
			buckets = append(buckets, &bucket{key: ki, indexes: []int{i}})
		}
	}

	out := make([]pairwisePair, 0)
	for _, b := range buckets {
		for a := 0; a < len(b.indexes); a++ {
			for c := a + 1; c < len(b.indexes); c++ {
				i, j := b.indexes[a], b.indexes[c]
				out = append(out, pairwisePair{i: i, j: j, labelI: labelAt(i), labelJ: labelAt(j)})
			}
		}
		if len(b.indexes) > 1 {
			groups = append(groups, b.indexes)
		}
	}
	return out, groups, nil
}

// pairwiseLegCoord maps a (pairIndex, oppositeIndex) into (row, col). For
// ROW scope the pair axis is rows (pairIndex = row, opp = column); for
// COLUMN scope it is columns (pairIndex = column, opp = row).
func pairwiseLegCoord(rowScope bool, pairIdx, oppIdx int) (row, col int) {
	if rowScope {
		return pairIdx, oppIdx
	}
	return oppIdx, pairIdx
}

// extractPairInputs reads both legs' inputs for a pair at a fixed
// opposite-axis coordinate. Returns ok=false when either leg is
// unreadable (missing cell / components).
func extractPairInputs(host *CrosstabHostView, params types.PairwiseOverlayParams, shape pwShape, basis weighting.Basis, rowScope bool, r1, c1, r2, c2 int) (pwInputs, bool) {
	if shape == pwShapeWeighted {
		w1, ok1 := host.WeightedMoments(r1, c1)
		w2, ok2 := host.WeightedMoments(r2, c2)
		if !ok1 || !ok2 {
			return pwInputs{}, false
		}
		return pwInputs{w1: w1, w2: w2,
			p1: math.NaN(), p2: math.NaN(), m1: math.NaN(), m2: math.NaN(),
			v1: math.NaN(), v2: math.NaN()}, true
	}
	if shape == pwShapeWelford {
		// Through the weighted-host rule: a weighted cell answers its
		// variance on w* and N*, never the triple's raw n.
		l1, ok1 := host.MeanLeg(r1, c1)
		l2, ok2 := host.MeanLeg(r2, c2)
		if !ok1 || !ok2 {
			return pwInputs{}, false
		}
		return pwInputs{m1: l1.mean, m2: l2.mean, v1: l1.variance, v2: l2.variance,
			nf1: l1.nStar, nf2: l2.nStar,
			p1: math.NaN(), p2: math.NaN()}, true
	}
	p1, ok1 := pairwiseProportion(host, params, r1, c1)
	p2, ok2 := pairwiseProportion(host, params, r2, c2)
	n1, nok1 := pairwiseSampleSize(host, params, basis, rowScope, r1, c1)
	n2, nok2 := pairwiseSampleSize(host, params, basis, rowScope, r2, c2)
	if !ok1 || !ok2 || !nok1 || !nok2 {
		return pwInputs{}, false
	}
	return pwInputs{p1: p1, p2: p2, nf1: n1, nf2: n2,
		m1: math.NaN(), m2: math.NaN(), v1: math.NaN(), v2: math.NaN()}, true
}

func pairwiseProportion(host *CrosstabHostView, params types.PairwiseOverlayParams, r, c int) (float64, bool) {
	v, ok := host.CellAt(r, c)
	if !ok {
		return 0, false
	}
	if params.PSource == types.PairwisePSourceCellValue {
		return v, true
	}
	return v / 100.0, true
}

// pairwiseSampleSize reads the n leg per the n_source mode. rowScope names
// the pair axis so n_within sums CellCounts along it.
//
// Weighted host (basis weighted): n is a weighted sample size N* —
// sum_weights under kind frequency, n_eff under kind probability — read
// off a floor (pairwiseWeightedNLeg). With n_source omitted it is the
// cell's BASE (U47 #324): the denominator normalize divided the cell by,
// so the successes p̂·N* rest on Σw_cell/Σw_base and the base's own
// effective size; row_margin_n / column_margin_n read the displayed
// margin's floor. The unweighted-count modes never reach here on a
// weighted host (refused up front, weighting.NSourceRefusal). Every
// count mode answers an integer, exactly representable as float64, so
// an unweighted host's kernels read the same numbers as before.
func pairwiseSampleSize(host *CrosstabHostView, params types.PairwiseOverlayParams, basis weighting.Basis, rowScope bool, r, c int) (float64, bool) {
	if basis.Weighted() && pairwiseNSourceReadsNStar(params.NSource) {
		l, _, ok := pairwiseWeightedNLeg(host, params, basis, r, c)
		if !ok {
			return 0, false
		}
		return l.nStar, true
	}
	n, ok := pairwiseCountSampleSize(host, params, rowScope, r, c)
	return float64(n), ok
}

// pairwiseNSourceReadsNStar reports whether an n_source on a weighted
// host reads a floor's N* (the omitted default and the two margin
// modes) rather than its unweighted count reading (the weight-sum
// sources, admitted under kind frequency only).
func pairwiseNSourceReadsNStar(nSource string) bool {
	switch nSource {
	case "", types.PairwiseNSourceRowMarginN, types.PairwiseNSourceColumnMarginN:
		return true
	}
	return false
}

// pairwiseWeightedNLeg is the weight-only leg {basis, Σw, N*} a
// proportion pair's n reads on a weighted host, and its identity: the
// cell's base (n_source omitted, BaseFloor), the row / column margin
// (row_margin_n / column_margin_n — the displayed margin's floor; a
// hidden margin carries none and the pair skips), else the cell's own
// floor (the weight-sum sources, whose Σw is that floor's).
func pairwiseWeightedNLeg(host *CrosstabHostView, params types.PairwiseOverlayParams, basis weighting.Basis, r, c int) (meanLeg, nLegKey, bool) {
	switch params.NSource {
	case "":
		return host.BaseFloor(r, c, basis)
	case types.PairwiseNSourceRowMarginN:
		l, ok := host.RowMarginFloor(r, basis)
		return l, nLegKey{site: nLegRowMargin, a: r}, ok
	case types.PairwiseNSourceColumnMarginN:
		l, ok := host.ColumnMarginFloor(c, basis)
		return l, nLegKey{site: nLegColumnMargin, a: c}, ok
	}
	l, ok := host.CellFloor(r, c, basis)
	return l, nLegKey{site: nLegCell, a: r, b: c}, ok
}

// pairwiseCountSampleSize is the count reading of every n_source mode.
func pairwiseCountSampleSize(host *CrosstabHostView, params types.PairwiseOverlayParams, rowScope bool, r, c int) (int, bool) {
	switch params.NSource {
	case types.PairwiseNSourceCellValueWeight:
		v, ok := host.CellAt(r, c)
		if !ok {
			return 0, false
		}
		return int(v), true
	case types.PairwiseNSourceRowMarginN:
		return host.RowMarginN(r)
	case types.PairwiseNSourceColumnMarginN:
		return host.ColumnMarginN(c)
	case types.PairwiseNSourceRowMarginDistinct:
		// Exact by construction — the margin accumulates over raw
		// records, so no partition precondition applies. A nil / absent
		// margin components entry returns ok=false and the pair skips;
		// it must never fall through to a zero sample size.
		return host.RowMarginDistinctN(r)
	case types.PairwiseNSourceColumnMarginDistinct:
		return host.ColumnMarginDistinctN(c)
	case types.PairwiseNSourceCellWeightSum:
		f, ok := host.CellWeightSum(r, c)
		if !ok {
			return 0, false
		}
		return int(f), true
	case types.PairwiseNSourceNWithin:
		prefix := params.NWithinDepth + 1
		if rowScope {
			return host.RowSlabN(r, c, prefix)
		}
		return host.ColumnSlabN(r, c, prefix)
	case types.PairwiseNSourceNWithinDistinct:
		// Same slab geometry as n_within, accumulating the cell
		// aggregator's distinct-KEY cardinality. Admission already
		// established the cell aggregator is one of the two that carry
		// one (runPairwiseOverlay), so an unreadable slab here means a
		// missing anchor key, not a wrong aggregator.
		prefix := params.NWithinDepth + 1
		if rowScope {
			return host.RowSlabDistinctN(r, c, prefix)
		}
		return host.ColumnSlabDistinctN(r, c, prefix)
	case "":
		// The omitted default reads the cell's BASE (U47 #324): on a
		// normalised host the cell value is a share of its normalise
		// denominator, so n is that denominator's count; under
		// normalize none the cell is its own base. The weighted twin is
		// pairwiseWeightedNLeg, so a frequency weight equals the
		// expanded rows and a unity weight equals no weight.
		return host.BaseN(r, c)
	default:
		return host.CellN(r, c)
	}
}

// buildPairwiseLayer assembles the MATRIX-payload OverlayLayer. ROW scope
// emits rows = pairs, columns = host columns; COLUMN scope transposes.
func buildPairwiseLayer(spec *types.OverlaySpec, host *CrosstabHostView, pairs []pairwisePair, pvals [][]types.MatrixCell, rowScope bool) types.OverlayLayer {
	pairHeader := types.AxisHeader{Fields: []string{"pair_a", "pair_b"}, Types: []string{"PAIR", "PAIR"}}
	pairKeys := make([]types.AxisKey, len(pairs))
	for p, pr := range pairs {
		pairKeys[p] = types.AxisKey{pr.labelI, pr.labelJ}
	}

	var mx types.MatrixPayload
	mx.CellLabel = "p_value"
	present := 0

	if rowScope {
		mx.RowHeader = pairHeader
		mx.RowKeys = pairKeys
		mx.ColumnHeader = host.Payload().ColumnHeader
		mx.ColumnKeys = copyAxisKeys(host.Payload().ColumnKeys)
		mx.Cells = pvals // already [pair][col]
		for _, row := range pvals {
			for _, cell := range row {
				if cell.Present {
					present++
				}
			}
		}
	} else {
		mx.RowHeader = host.Payload().RowHeader
		mx.RowKeys = copyAxisKeys(host.Payload().RowKeys)
		mx.ColumnHeader = pairHeader
		mx.ColumnKeys = pairKeys
		mx.Cells = layoutPairCells(pvals, rowScope, host.RowCount(), len(pairs))
		for _, row := range mx.Cells {
			for _, cell := range row {
				if cell.Present {
					present++
				}
			}
		}
	}

	count := present
	return types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeMatrix,
			Matrix: &mx,
		},
		Summary: &types.OverlaySummary{Count: &count},
	}
}

// layoutPairCells lays pair-major cells ([pair][opposite]) out on the
// layer's grid: as is under ROW scope (rows = pairs), transposed to
// [row][pair] under COLUMN scope.
func layoutPairCells(pairMajor [][]types.MatrixCell, rowScope bool, rowCount, nPairs int) [][]types.MatrixCell {
	if rowScope {
		return pairMajor
	}
	cells := make([][]types.MatrixCell, rowCount)
	for r := 0; r < rowCount; r++ {
		cells[r] = make([]types.MatrixCell, nPairs)
		for p := 0; p < nPairs; p++ {
			cells[r][p] = pairMajor[p][r]
		}
	}
	return cells
}

func copyAxisKeys(in []types.AxisKey) []types.AxisKey {
	if in == nil {
		return nil
	}
	out := make([]types.AxisKey, len(in))
	for i, k := range in {
		out[i] = append(types.AxisKey(nil), k...)
	}
	return out
}

// PairKeyElement is the pair-axis element a pairwise overlay writes for
// one host axis key — stringifyOverlayAxisKey, exported so the label
// pass (internal/service) relabels a PAIR key from the host key it was
// built from, through the one rule.
func PairKeyElement(k types.AxisKey) string { return stringifyOverlayAxisKey(k) }

// stringifyOverlayAxisKey collapses an AxisKey into a deterministic label
// for the pair-key 2-tuple. Single-element keys produce the bare value;
// multi-element keys join with "|".
func stringifyOverlayAxisKey(k types.AxisKey) string {
	switch len(k) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%v", k[0])
	default:
		parts := make([]string, len(k))
		for i, v := range k {
			parts[i] = fmt.Sprintf("%v", v)
		}
		return joinPipe(parts)
	}
}

func joinPipe(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "|"
		}
		out += p
	}
	return out
}

// unitProportion reports whether p is a proportion in [0, 1]. A leg
// outside it — a p_source of cell_value over a raw count cell, or of
// cell_value_pct over a share already in [0, 1] read as a percentage of
// 100+ — is not a proportion at all: the pair skips as
// PULSE_STATS_SKIP_PROPORTION_INVALID rather than scoring a p-value off
// a clipped (probit) or accidentally in-range pooled (prop-z) figure.
func unitProportion(p float64) bool { return p >= 0 && p <= 1 }

// pairwisePropZKernel is the pooled-SE two-proportion z-test. Reuses the
// shared twoProportionZ helper so p-values match OVERLAY_PROP_Z_CELL.
func pairwisePropZKernel(in pwInputs) (pwOutcome, string, bool) {
	if in.nf1 == 0 || in.nf2 == 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_N_ZERO", false
	}
	if !unitProportion(in.p1) || !unitProportion(in.p2) {
		return pwOutcome{}, "PULSE_STATS_SKIP_PROPORTION_INVALID", false
	}
	n1f, n2f := in.nf1, in.nf2
	p, ok := twoProportionZ(in.p1*n1f, n1f, in.p2*n2f, n2f)
	if !ok {
		return pwOutcome{}, "PULSE_STATS_SKIP_POOLED_SE_INVALID", false
	}
	// z = (p1 - p2) / SE with SE > 0: the sign of the share difference.
	return pwOutcome{p: p, dir: signOf(in.p1 - in.p2)}, "", true
}

// pairwiseProbitTKernel is the probit t-test: Φ⁻¹ transform of each leg's
// proportion, Student-t tail on df = n1 + n2 - 2.
func pairwiseProbitTKernel(in pwInputs) (pwOutcome, string, bool) {
	if in.nf1 == 0 || in.nf2 == 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_N_ZERO", false
	}
	if !unitProportion(in.p1) || !unitProportion(in.p2) {
		return pwOutcome{}, "PULSE_STATS_SKIP_PROPORTION_INVALID", false
	}
	dof := in.nf1 + in.nf2 - 2
	if dof <= 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_DOF_INVALID", false
	}
	const eps = 1e-10
	p1c := clipUnit(in.p1, eps, 1-eps)
	p2c := clipUnit(in.p2, eps, 1-eps)
	denom := math.Sqrt(1.0/in.nf1 + 1.0/in.nf2)
	if denom == 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_DENOM_INVALID", false
	}
	t := (standardNormalPPF(p1c) - standardNormalPPF(p2c)) / denom
	if math.IsNaN(t) || math.IsInf(t, 0) {
		return pwOutcome{}, "PULSE_STATS_SKIP_T_INVALID", false
	}
	pv := statdist.StudentTTwoSidedP(t, dof)
	if math.IsNaN(pv) {
		return pwOutcome{}, "PULSE_STATS_SKIP_PVALUE_NAN", false
	}
	return pwOutcome{p: pv, dir: signOf(t)}, "", true
}

// pairwiseWelchTKernel is the Welch–Satterthwaite t-test on Welford
// triples.
func pairwiseWelchTKernel(in pwInputs) (pwOutcome, string, bool) {
	n1f, n2f := in.nf1, in.nf2
	if !(n1f > 1) || !(n2f > 1) {
		return pwOutcome{}, "PULSE_STATS_SKIP_N_TOO_SMALL", false
	}
	a := in.v1 / n1f
	b := in.v2 / n2f
	// !(a+b > 0), not a+b <= 0: an undefined (NaN) leg variance — a
	// Welford triple at N < 2 or Σw ≤ 1 (E4-S6) — skips the pair
	// rather than scoring a NaN p-value.
	if !(a+b > 0) {
		return pwOutcome{}, "PULSE_STATS_SKIP_VARIANCE_INVALID", false
	}
	se := math.Sqrt(a + b)
	if se == 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_SE_ZERO", false
	}
	t := (in.m1 - in.m2) / se
	dofDenom := (a*a)/(n1f-1) + (b*b)/(n2f-1)
	if dofDenom <= 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_DOF_INVALID", false
	}
	dof := (a + b) * (a + b) / dofDenom
	if dof <= 0 || math.IsNaN(dof) || math.IsInf(dof, 0) {
		return pwOutcome{}, "PULSE_STATS_SKIP_DOF_INVALID", false
	}
	pv := statdist.StudentTTwoSidedP(t, dof)
	if math.IsNaN(pv) {
		return pwOutcome{}, "PULSE_STATS_SKIP_PVALUE_NAN", false
	}
	return pwOutcome{p: pv, dir: signOf(t)}, "", true
}

// pairwiseTwoMeansZKernel is the two-means z-test on Welford triples
// (normal CDF tail, no df adjustment).
func pairwiseTwoMeansZKernel(in pwInputs) (pwOutcome, string, bool) {
	if !(in.nf1 > 1) || !(in.nf2 > 1) {
		return pwOutcome{}, "PULSE_STATS_SKIP_N_TOO_SMALL", false
	}
	a := in.v1 / in.nf1
	b := in.v2 / in.nf2
	// !(a+b > 0), not a+b <= 0: an undefined (NaN) leg variance — a
	// Welford triple at N < 2 or Σw ≤ 1 (E4-S6) — skips the pair
	// rather than scoring a NaN p-value.
	if !(a+b > 0) {
		return pwOutcome{}, "PULSE_STATS_SKIP_VARIANCE_INVALID", false
	}
	se := math.Sqrt(a + b)
	if se == 0 {
		return pwOutcome{}, "PULSE_STATS_SKIP_SE_ZERO", false
	}
	z := (in.m1 - in.m2) / se
	pv := normalTwoSidedP(z)
	return pwOutcome{p: pv, dir: signOf(z)}, "", true
}

// pairwiseWeightedTwoMeansZKernel returns the two-means z-test kernel on
// AGG_WEIGHTED_MEAN moments under nBasis (already validated):
//
//	z = (m_i − m_j) / sqrt(var_i/n_i + var_j/n_j),  p = 2·Φ(−|z|)
//
// with var and n per weightedLegVarianceOverN. A leg whose convention is
// undefined skips the pair-cell as PULSE_STATS_SKIP_N_TOO_SMALL; a pair
// whose legs both have zero spread skips as PULSE_STATS_SKIP_SE_ZERO.
// Both fold into the shared PULSE_OVERLAY_REF_ZERO tally.
func pairwiseWeightedTwoMeansZKernel(nBasis string) pwKernel {
	return func(in pwInputs) (pwOutcome, string, bool) {
		a, ok := weightedLegVarianceOverN(nBasis, in.w1)
		if !ok {
			return pwOutcome{}, "PULSE_STATS_SKIP_N_TOO_SMALL", false
		}
		b, ok := weightedLegVarianceOverN(nBasis, in.w2)
		if !ok {
			return pwOutcome{}, "PULSE_STATS_SKIP_N_TOO_SMALL", false
		}
		if !(a+b > 0) {
			return pwOutcome{}, "PULSE_STATS_SKIP_SE_ZERO", false
		}
		se := math.Sqrt(a + b)
		z := (in.w1.mean - in.w2.mean) / se
		pv := normalTwoSidedP(z)
		if math.IsNaN(pv) {
			return pwOutcome{}, "PULSE_STATS_SKIP_PVALUE_NAN", false
		}
		return pwOutcome{p: pv, dir: signOf(z)}, "", true
	}
}

// weightedLegVarianceOverN returns one leg's var/n under nBasis:
//
//	weights: var = m2/(Σw − 1),      n = Σw        — undefined when Σw ≤ 1
//	kish:    var = m2/(Σw − Σw²/Σw), n = (Σw)²/Σw² — undefined when
//	         Σw ≤ 0 or Σw − Σw²/Σw ≤ 0 (all weight on one row)
//
// The kish test reads the raw moments directly rather than the emitted
// n_eff component: n_eff is 1 for a single row, which is a legal base but
// an undefined variance.
func weightedLegVarianceOverN(nBasis string, w weightedMoments) (float64, bool) {
	switch nBasis {
	case types.PairwiseNBasisWeights:
		if !(w.sumW > 1) {
			return 0, false
		}
		return (w.m2 / (w.sumW - 1)) / w.sumW, true
	case types.PairwiseNBasisKish:
		if !(w.sumW > 0) || !(w.sumWSq > 0) {
			return 0, false
		}
		// Σw − Σw²/Σw computed as ((Σw)² − Σw²)/Σw. The explicit
		// float64 conversion forbids fusing the square into an FMA, so a
		// single-row cell — whose Σw² was accumulated as the same rounded
		// w·w — yields an exact zero on every architecture and skips,
		// rather than a rounding-residue denominator that admits it.
		excess := float64(w.sumW*w.sumW) - w.sumWSq
		if !(excess > 0) {
			return 0, false
		}
		denom := excess / w.sumW
		nEff := w.sumW * w.sumW / w.sumWSq
		return (w.m2 / denom) / nEff, true
	}
	return 0, false
}

func clipUnit(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

// standardNormalPPF returns Φ⁻¹(p) — statdist.NormalQuantile, the one
// implementation the regression engines' Wald intervals share (checked
// against R's qnorm by reference_oracle_test.go).
func standardNormalPPF(p float64) float64 { return statdist.NormalQuantile(p) }

// pairwiseTally aggregates per-(pair, opposite) skips by reason code into
// one warning per code carrying the count and a sample opposite-axis
// index — avoids one warning per degenerate cell on a wide matrix.
type pairwiseTally struct {
	order  []string
	counts map[string]int
	sample map[string]int
}

func newPairwiseTally() *pairwiseTally {
	return &pairwiseTally{counts: map[string]int{}, sample: map[string]int{}}
}

func (t *pairwiseTally) add(reason string, oppIdx int) {
	if reason == "" {
		reason = "PULSE_STATS_SKIP_EXTRACT_FAILED"
	}
	if _, seen := t.counts[reason]; !seen {
		t.order = append(t.order, reason)
		t.sample[reason] = oppIdx
	}
	t.counts[reason]++
}

func (t *pairwiseTally) warnings(spec *types.OverlaySpec) []types.OverlayWarning {
	if len(t.order) == 0 {
		return nil
	}
	out := make([]types.OverlayWarning, 0, len(t.order))
	for _, reason := range t.order {
		out = append(out, types.OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " skipped " + fmt.Sprint(t.counts[reason]) + " pair-cell(s): " + reason,
			Details: map[string]any{
				"kind":                string(spec.Kind),
				"reason":              reason,
				"skipped":             t.counts[reason],
				"sample_opposite_idx": t.sample[reason],
			},
		})
	}
	return out
}
