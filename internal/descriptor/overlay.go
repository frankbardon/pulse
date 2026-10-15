package descriptor

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// Overlay validator — no-execute, header-and-schema-only validation
// for Request.Overlays specs. Walks every OverlaySpec and surfaces
// structural failures on the envelope alongside the aggregator / test
// / crosstab gates the rest of Predict runs.
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import github.com/frankbardon/pulse/service
//     or github.com/frankbardon/pulse/internal/processing. Predict is no-execute;
//     overlay catalog data lives in types/, capability lookups go
//     through types/ constants. TestPredictNoExecutionImports gates
//     the predict.go source list and CLAUDE.md "What NOT to Do" gates
//     the package as a whole.
//   - No fmt.Sprintf in any JSON-bearing path. Error messages are
//     built with string concatenation so descriptor envelope output
//     stays grep-clean against the structural defense ban.

// pairwiseNBasisAdvice completes the n_basis refusal on a kind that
// does not read it. The weighted kind (and the AGG_WEIGHTED_MEAN cell it
// requires) is named only when the instance offers it: a refusal never
// advertises a hidden feature.
func pairwiseNBasisAdvice(opts *PredictOptions) string {
	if opts.overlayRoute(types.OverlayKindPairwiseWeightedTwoMeansZ) == "" {
		return "no pairwise kind this instance offers reads it. Remove n_basis"
	}
	return "it is read only by " + string(types.OverlayKindPairwiseWeightedTwoMeansZ) +
		". Remove n_basis, or use that kind over an AGG_WEIGHTED_MEAN cell"
}

// weightedMomentCell reports whether a crosstab cell emits the weighted
// moments OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z reads: AGG_WEIGHTED_MEAN
// always (it is weighted by definition), AGG_AVERAGE only when a weight
// resolves on the cell — its own slot weight, else the request weight,
// else the instance default (opts.DefaultWeight); `weight: null` opts
// the cell out and leaves it an unweighted mean. The same precedence as
// ResolveWeights and processing.StampWeights, so predict and runtime
// agree on the host.
func weightedMomentCell(req *types.Request, cell *types.Aggregation, opts *PredictOptions) bool {
	switch cell.Type {
	case types.AGG_WEIGHTED_MEAN:
		return true
	case types.AGG_AVERAGE:
	default:
		return false
	}
	switch {
	case cell.Weight.IsNull():
		return false
	case !cell.Weight.IsZero():
		return true
	}
	return req.Weight != nil || (opts != nil && opts.DefaultWeight != nil)
}

// welfordTripleCellRefused reports whether a crosstab cell of type
// cellType statically cannot carry the Welford triple {mean, variance,
// n} the moment pairwise kinds (OVERLAY_PAIRWISE_WELCH_T /
// _TWO_MEANS_Z) read. The runtime's rule (processing.runPairwiseOverlay,
// CrosstabHostView.HasWelfordCells) is "some cell's components carry the
// three keys", so predict asks the capability table: the cell type's
// declared ComponentSchema — a built-in's, or a registered extension's
// (aggregatorComponentSchemaIndex) — lacks `mean` or `variance` (`n` is
// the universal floor every cell carries). A registered extension with
// no declared schema emits the floor only, so it is refused too. An
// empty or unknown type (refused elsewhere) is not judged here.
func welfordTripleCellRefused(cellType types.AggregationType, opts *PredictOptions) bool {
	if cellType == "" {
		return false
	}
	schema, ok := aggregatorComponentSchemaIndex(opts)[string(cellType)]
	if !ok {
		snap := extensionsFromOpts(opts)
		if snap == nil {
			return false
		}
		for _, m := range snap.Aggregators {
			if m.Name == string(cellType) {
				return true
			}
		}
		return false
	}
	have := map[string]bool{}
	for _, k := range schema.Keys {
		have[k.Name] = true
	}
	return !have["mean"] || !have["variance"]
}

// weightedMomentCellName names a refused cell for the shape refusal,
// qualifying an AGG_AVERAGE as unweighted (the weighted one is admitted).
func weightedMomentCellName(req *types.Request, cell *types.Aggregation, opts *PredictOptions) string {
	if cell.Type == types.AGG_AVERAGE && !weightedMomentCell(req, cell, opts) {
		return "an unweighted " + string(cell.Type)
	}
	return string(cell.Type)
}

// pairwiseProportionAdvice is the ", or use …" tail of a Welford-kind
// selector refusal: the proportion kinds the instance offers, which
// what. Empty when it offers neither.
func pairwiseProportionAdvice(opts *PredictOptions, what string) string {
	var offered []string
	for _, k := range []types.OverlayKind{types.OverlayKindPairwisePropZ, types.OverlayKindPairwiseProbitT} {
		if opts.overlayRoute(k) != "" {
			offered = append(offered, string(k))
		}
	}
	switch len(offered) {
	case 0:
		return ""
	case 1:
		return ", or use " + offered[0] + ", which can " + what
	}
	return ", or use " + strings.Join(offered, " / ") + ", which " + what
}

// validateOverlayPairwise validates the shared contract for every
// OVERLAY_PAIRWISE_* kind: implicit-margin (empty Ref), MATRIX host,
// ROW / COLUMN scope, and well-formed Params (decodable, known n_source /
// p_source modes, non-negative pair_along_dim / n_within_depth) — plus the
// per-kind rule that the two Welford-input kinds accept NEITHER mode
// selector, because both legs come from the triple; the weighted kind
// likewise refuses both selectors, requires n_basis and refuses a
// built-in non-AGG_WEIGHTED_MEAN cell; every other kind refuses n_basis.
// The Welford-shape requirement of the two moment kinds is judged from
// the cell type's declared ComponentSchema (welfordTripleCellRefused),
// the runtime's key rule read off the capability table. The per-cell
// components requirement (PULSE_OVERLAY_COMPONENTS_REQUIRED) stays a
// runtime condition.
func validateOverlayPairwise(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — the pair axis is a host crosstab axis.
	if req == nil || req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Params must decode and name known modes / non-negative depths.
	params, err := types.DecodePairwiseParams(spec.Params)
	if err != nil {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" has malformed Params: "+err.Error(),
			map[string]any{"index": index, "kind": string(spec.Kind)})
		return
	}
	// Welford cell shape: a moment kind over a cell whose components
	// cannot carry the triple is the runtime's
	// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, same message. With
	// components disabled the runtime refuses first with
	// PULSE_OVERLAY_COMPONENTS_REQUIRED, so the shape rule stands down.
	if types.PairwiseKindUsesWelford(spec.Kind) && req.Crosstab.Cell != nil && !opts.componentsDisabled(req) &&
		welfordTripleCellRefused(opRoute(opts.instance(), req.Crosstab.Cell.Type), opts) {
		env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
			overlayhost.WelfordCellMessage(spec.Kind),
			map[string]any{"index": index, "kind": string(spec.Kind), "cell_type": string(req.Crosstab.Cell.Type)})
		return
	}
	// Welford-input inertness. OVERLAY_PAIRWISE_WELCH_T and
	// OVERLAY_PAIRWISE_TWO_MEANS_Z read the mean, the variance AND the
	// n out of the AGG_WELFORD triple, so neither mode selector ever
	// reaches the math: setting one changed nothing while the caller
	// believed they had moved the n leg or the proportion leg. Refused
	// rather than ignored.
	//
	// Deliberately NOT scoped to the distinct-key modes. If the
	// selector is meaningless on these kinds it is meaningless for
	// every mode, and refusing three of nine would leave a rule nobody
	// can state. This is a behaviour break: a request naming any
	// n_source / p_source on a Welford kind predicted clean before and
	// refuses now.
	//
	// Runs BEFORE the known-mode checks on purpose — "unknown n_source"
	// would tell the caller a known one would work, and none would.
	//
	// n_within_depth is deliberately excluded. It is equally inert
	// here, but only BECAUSE n_source is: a non-zero depth without a
	// slab n_source is just as inert on the proportion kinds, so a
	// Welford-only refusal would be the incoherence this gate avoids.
	// It is also a plain int whose zero value is meaningful, so
	// "was it set?" is not observable after decode.
	//
	// Predict-only, by decision. Unlike the slab partition gate below,
	// no wrong NUMBER can come of this — the param is inert, so the
	// p-values are correct either way and a runtime twin would convert
	// a currently-succeeding pulse.Process into a hard failure for no
	// correctness gain. The runtime arm keeps only the distinct-key
	// cell-aggregator admission in processing.runPairwiseOverlay, whose
	// three modes are new in this release and can break no existing
	// caller.
	// n_basis on any kind but the weighted one. It is read ONLY by
	// OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z; on every other pairwise
	// kind it changes nothing while the caller believes they picked a
	// weighted variance convention (most plausibly on the Welford
	// OVERLAY_PAIRWISE_TWO_MEANS_Z twin). Same rule and same scope as
	// the Welford selector refusal above: predict-only, because the
	// param is inert and no wrong number can come of it, so a runtime
	// twin would fail a succeeding pulse.Process for no correctness
	// gain. Reported alongside any selector refusal, not instead of it.
	nBasisRefused := false
	if params.NBasis != "" && !types.PairwiseKindUsesWeightedMoments(spec.Kind) {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" does not accept n_basis ("+params.NBasis+"): "+pairwiseNBasisAdvice(opts),
			map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_basis",
				"n_basis": params.NBasis})
		nBasisRefused = true
	}

	if types.PairwiseKindUsesWelford(spec.Kind) {
		const welfordReason = "n, mean and variance all come from the AGG_WELFORD triple {mean, variance, n}, so the mode selector is inert on this kind"
		refused := nBasisRefused
		// Weighted-host n_source rule (weighting.NSourceRefusal): on a
		// weighted cell the legs read N*, so a raw-row-count source (or
		// the weight sum under kind probability) is PROCESSING_CONFIG —
		// the runtime's code (processing.runPairwiseOverlay) — in place
		// of the inertness refusal below.
		if reason := weighting.NSourceRefusal(params.NSource, crosstabCellWeightBasis(req, opts)); reason != "" {
			env.AddError(string(errors.PROCESSING_CONFIG),
				"overlay "+string(spec.Kind)+" n_source "+params.NSource+": "+reason,
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_source",
					"n_source": params.NSource})
			refused = true
		} else if params.NSource != "" {
			env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"overlay "+string(spec.Kind)+" does not accept n_source ("+params.NSource+"): "+welfordReason+
					". Remove n_source"+pairwiseProportionAdvice(opts, "read a proportion and a separate n leg"),
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_source",
					"n_source": params.NSource, "reason": welfordReason})
			refused = true
		}
		if params.PSource != "" {
			env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"overlay "+string(spec.Kind)+" does not accept p_source ("+params.PSource+"): "+welfordReason+
					". Remove p_source"+pairwiseProportionAdvice(opts, "derive a proportion leg"),
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "p_source",
					"p_source": params.PSource, "reason": welfordReason})
			refused = true
		}
		if refused {
			return
		}
	}
	if nBasisRefused {
		return
	}

	// Weighted-moments kind. OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z reads
	// mean, variance and sample size from the AGG_WEIGHTED_MEAN moments
	// under n_basis, so n_source / p_source are inert and refused, and
	// n_basis is REQUIRED (no default: the convention is the embedder's
	// call). Unlike the Welford refusal this one has a runtime twin in
	// processing.applyPairwiseWeightedTwoMeansZ under the same code — the
	// kind is new, so a hard runtime refusal breaks no existing caller.
	if types.PairwiseKindUsesWeightedMoments(spec.Kind) {
		const weightedReason = "mean, variance and sample size all come from the AGG_WEIGHTED_MEAN moments under n_basis, so the selector is inert on this kind"
		refused := false
		if params.NSource != "" {
			env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"overlay "+string(spec.Kind)+" does not accept n_source ("+params.NSource+"): "+weightedReason+
					". Remove n_source and choose the sample-size convention with n_basis",
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_source",
					"n_source": params.NSource, "reason": weightedReason})
			refused = true
		}
		if params.PSource != "" {
			env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
				"overlay "+string(spec.Kind)+" does not accept p_source ("+params.PSource+"): "+weightedReason+
					". Remove p_source",
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "p_source",
					"p_source": params.PSource, "reason": weightedReason})
			refused = true
		}
		// Cell host. Among the built-ins only AGG_WEIGHTED_MEAN and a
		// WEIGHTED AGG_AVERAGE (a weight resolves on the cell: its slot
		// weight, else the request weight, else the instance default;
		// `weight: null` opts out) emit the weighted moments, so any
		// OTHER built-in cell is a shape error predict can see from the
		// request alone — the same PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
		// the runtime raises off the materialised host. An empty Type (a
		// smart default resolves later) or an extension type defers to
		// runtime: an extension may emit the moment keys, and runtime
		// gates on the keys, not the type name.
		// A cell type the instance hides is judged as a never-registered
		// (non-built-in) name.
		if cell := req.Crosstab.Cell; cell != nil && cell.Type != "" && !weightedMomentCell(req, cell, opts) &&
			isBuiltinAggregationType(opRoute(opts.instance(), cell.Type)) {
			env.AddError(string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"overlay "+string(spec.Kind)+" requires an AGG_WEIGHTED_MEAN or weighted AGG_AVERAGE cell (it reads the weighted moments off Response.Components); the crosstab cell is "+weightedMomentCellName(req, cell, opts),
				map[string]any{"index": index, "kind": string(spec.Kind), "cell_type": string(cell.Type),
					"required_cell_type": []string{string(types.AGG_WEIGHTED_MEAN), string(types.AGG_AVERAGE)}})
			refused = true
		}
		if !types.ValidPairwiseNBasis(params.NBasis) {
			msg := "overlay " + string(spec.Kind) + " requires params.n_basis (" + types.PairwiseNBasisWeights +
				" or " + types.PairwiseNBasisKish + "); there is no default"
			if params.NBasis != "" {
				msg = "overlay " + string(spec.Kind) + " has unknown n_basis: " + params.NBasis +
					" (supports: " + types.PairwiseNBasisWeights + ", " + types.PairwiseNBasisKish + ")"
			}
			env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING), msg,
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_basis",
					"n_basis":   params.NBasis,
					"supported": []string{types.PairwiseNBasisWeights, types.PairwiseNBasisKish}})
			refused = true
		} else if reason := weighting.NBasisRefusal(params.NBasis, crosstabCellWeightBasis(req, opts)); reason != "" {
			// n_basis "weights" on a probability-weighted cell reads Σw
			// as n (U12 review WS-06): the runtime twin is
			// processing.applyPairwiseWeightedTwoMeansZ, same code and
			// message.
			env.AddError(string(errors.PROCESSING_CONFIG),
				"overlay "+string(spec.Kind)+" n_basis "+params.NBasis+": "+reason,
				map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_basis",
					"n_basis": params.NBasis})
			refused = true
		}
		if refused {
			return
		}
	}

	if !types.ValidPairwiseNSource(params.NSource) {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" has unknown n_source: "+params.NSource,
			map[string]any{"index": index, "kind": string(spec.Kind), "n_source": params.NSource})
		return
	}
	// Weighted-host n_source rule on the proportion kinds
	// (weighting-inferential E3-S2): the runtime twin is
	// processing.runPairwiseOverlay, same code and message.
	if reason := weighting.NSourceRefusal(params.NSource, crosstabCellWeightBasis(req, opts)); reason != "" {
		env.AddError(string(errors.PROCESSING_CONFIG),
			"overlay "+string(spec.Kind)+" n_source "+params.NSource+": "+reason,
			map[string]any{"index": index, "kind": string(spec.Kind), "param": "n_source",
				"n_source": params.NSource})
		return
	}
	if !types.ValidPairwisePSource(params.PSource) {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" has unknown p_source: "+params.PSource,
			map[string]any{"index": index, "kind": string(spec.Kind), "p_source": params.PSource})
		return
	}
	if params.PairAlongDim != nil && *params.PairAlongDim < 0 {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" pair_along_dim must be >= 0",
			map[string]any{"index": index, "kind": string(spec.Kind), "pair_along_dim": *params.PairAlongDim})
		return
	}
	if params.NWithinDepth < 0 {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" n_within_depth must be >= 0",
			map[string]any{"index": index, "kind": string(spec.Kind), "n_within_depth": params.NWithinDepth})
		return
	}

	// Distinct-key slab partition gate. A distinct cardinality summed
	// across cells equals the slab's true distinct count only when
	// those cells partition the key set; a fan-out grouper among the
	// summed-across pair-axis dims breaks that and inflates n silently.
	// Static property of the request shape, so predict can refuse it
	// without reading a record — but predict alone does not stop
	// pulse.Process, so processing.applyOverlaysToResponse carries the
	// runtime twin under the same code.
	//
	// opts carries the extensions snapshot, which is how an
	// embedder-registered fan-out grouper reaches this arm: descriptor/
	// may not import internal/processing/ and so cannot assert
	// MultiKeyStreamingGrouper itself. The runtime twin reads the live
	// registry instead; both hand their resolver to the SAME
	// types-side predicate, which tries the built-in constant first.
	if v, bad := types.CheckPairwiseSlabPartitionWith(req.Crosstab, spec.Scope, params,
		snapshotGroupFanOut(extensionsFromOpts(opts))); bad {
		env.AddError(string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED),
			v.Message(spec.Kind, params),
			v.Details(spec.Kind, params, index))
	}
}

// validateOverlayPanel validates the params blob of the COMPOSE-host
// OVERLAY_PROP_Z_PANEL, the multi-reference sibling of the
// OVERLAY_PAIRWISE_* family. Malformed params fire
// PULSE_OVERLAY_PARAM_MISSING with the same Details shape
// validateOverlayPairwise emits, so one renderer branch handles both
// families.
//
// Nothing else about the panel is checked here. The kind's structural
// contract — reference / target slot resolution, MATRIX host on every
// slot, axis-schema agreement and the MaxPanelTargets cap — is
// COMPOSE-shaped and lives in validateComposeOverlaySpec
// (internal/descriptor/compose.go), which is also where this helper is called
// from: ComposedRequest.Overlays is the only slot the panel can
// actually execute out of.
//
// It is deliberately NOT wired into validateOverlaySpec's per-kind
// switch. A panel spec on Request.Overlays is a WRONG-HOST spec, and
// the honest diagnostic there names the host, not the params:
// ValidateOverlays refuses it with the shared overlayhost refusal
// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) the runtime raises too.
//
// Params carrier note: the raw slot differs per host —
// OverlaySpec.Params is json.RawMessage, ComposeOverlaySpec.Params is
// map[string]any — so the caller does the decode with the matching
// types.DecodePanelParams* entry point and hands the error here. Both
// entry points funnel into one decoder, so the two hosts cannot
// disagree about what a params blob means.
func validateOverlayPanel(env *descriptor.Envelope, kind types.OverlayKind, params types.PanelOverlayParams, err error, index int, slots []types.PanelSlabPartitionSlot, resolveExt types.ExtensionGroupFanOutFunc) {
	if err != nil {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(kind)+" has malformed Params: "+err.Error(),
			map[string]any{"index": index, "kind": string(kind)})
		return
	}

	// Unknown n_source. Named rather than ignored: the panel accepts
	// unknown params KEYS (forward compatibility against an older
	// binary), but an unknown VALUE in a key it does know is a typo in
	// configuration the caller believes is applied, and silently
	// running the default would hand back the legacy number under the
	// caller's impression that they had moved the n leg.
	//
	// The Details carry the offending value AND the valid set, because
	// the whole set is two entries long and a renderer that can show it
	// turns "unknown n_source" into a one-click fix.
	if !types.ValidPanelNSource(params.NSource) {
		valid := types.PanelNSources()
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(kind)+" has unknown n_source: "+params.NSource+
				" (valid: "+strings.Join(valid, ", ")+")",
			map[string]any{"index": index, "kind": string(kind),
				"n_source": params.NSource, "valid_n_sources": valid})
		return
	}

	// n_within_depth shape. Both checks are pure spec configuration —
	// no host is reachable from here — so both are predict-safe and
	// both carry a runtime twin in applyPropZPanel, because
	// pulse.Compose does not run predict.
	//
	// Set alongside a mode that does not consume it, the depth is
	// INERT, and an inert param the caller believes is applied is the
	// silent no-op this family refuses. The panel can make that
	// refusal only because PanelOverlayParams.NWithinDepth is a *int:
	// "written" is distinguishable from "zero", so the check cannot
	// misfire on a caller who never named the key. (The axis-pairing
	// family's plain int cannot make that distinction, which is why
	// its equivalent no-op is still open — this is not a fix for it.)
	if params.NWithinDepth != nil && !types.PanelNSourceUsesWithinDepth(params.NSource) {
		nSource := params.NSource
		if nSource == "" {
			nSource = types.PanelNSourceRowMarginValue
		}
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(kind)+" n_within_depth is not read by n_source "+nSource+
				" (it applies to "+strings.Join(types.PanelNSourcesUsingWithinDepth(), ", ")+" only)",
			map[string]any{"index": index, "kind": string(kind),
				"n_source": params.NSource, "n_within_depth": *params.NWithinDepth})
		return
	}
	if params.NWithinDepth != nil && *params.NWithinDepth < 0 {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(kind)+" n_within_depth must be >= 0",
			map[string]any{"index": index, "kind": string(kind),
				"n_source": params.NSource, "n_within_depth": *params.NWithinDepth})
		return
	}

	// Within-prefix slab partition gate. An explicit n_within_depth
	// turns the leg into a SUM ACROSS ROWS, and a summed slab equals
	// the slab's true sample size only when its rows partition the key
	// set — a fan-out grouper among the summed-across row-axis dims
	// lands one record in two summed rows and n comes out too large,
	// silently and liberally.
	//
	// Unlike the range guard below it, this IS predict-safe: the
	// offending fact is the declared GROUPER TYPE on each slot's
	// authored Crosstab.Rows, a static property of the request that
	// needs no materialised axis. Predict alone does not stop
	// pulse.Compose, so processing.checkPanelSlabPartition carries the
	// runtime twin under the same code and the same words.
	//
	// resolveExt is how an embedder-registered fan-out grouper reaches
	// this arm — descriptor/ may not import internal/processing/ and so cannot
	// assert MultiKeyStreamingGrouper itself.
	if v, bad := types.CheckPanelSlabPartitionWith(slots, params, resolveExt); bad {
		env.AddError(string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED),
			v.Message(kind, params),
			v.Details(kind, params, index))
		return
	}

	// The depth-vs-actual-row-axis-depth range guard is deliberately
	// NOT here. It is per SLOT and needs each slot's materialised row
	// keys, which a no-execute validator cannot see; the MATRIX arm
	// draws the same line (descriptor checks `< 0`, buildPairwisePairs
	// checks the range against the live host).
}

// yoySupportedFrequencies enumerates the supported `frequency` Param
// values for OVERLAY_YOY at predict time. Mirrors
// processing.yoySupportedFrequencies (the runtime helper) so the gate
// shapes stay in lock-step.
var yoySupportedFrequencies = []string{
	"annual",
	"quarterly",
	"monthly",
	"weekly",
	"daily",
	"hourly",
}

// ValidateOverlays walks every spec in req.Overlays and appends
// structural errors to the envelope. Exported so engines and tests
// can run the validator standalone; Predict wires it inline so
// overlay issues surface alongside crosstab / aggregator / test ones.
//
// No-op when req is nil or req.Overlays is empty. Schema is currently
// unused — every rule is structural — but the signature accepts it
// so later kinds can validate against schema-derived state (e.g.
// referenced field types on sibling / baseline-index families) without
// re-opening every call site.
//
// Level / Within out-of-range gate: runs alongside the per-
// kind ref/scope checks via validateOverlayLevelWithinPredict. Mirrors
// the PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE shape on the
// crosstab path — out-of-range slots surface
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE on the envelope with Details
// carrying the spec index, kind, level / within, and the axis depth.
// The runtime mirror lives at processing.validateOverlayLevelWithinRuntime
// so a programmatic Process caller that skipped predict still gets
// the same failure shape.
func ValidateOverlays(env *descriptor.Envelope, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	if req == nil || len(req.Overlays) == 0 {
		return
	}
	for i := range req.Overlays {
		spec := &req.Overlays[i]
		// A kind that does not run on Request.Overlays (a Compose,
		// whole-chain or FACET kind) is refused before any per-kind
		// rule — the shared overlayhost refusal the runtime's Process
		// hooks raise, same code and Details.
		if cerr := overlayhost.Refusal(overlayhost.Request, spec.Kind, opts.overlayRoute(spec.Kind), i); cerr != nil {
			addCodedError(env, cerr)
			continue
		}
		// A kind over the wrong Process shape (a MATRIX-only kind on a
		// grouped request, a SERIES-only kind on a crosstab) — the shared
		// overlayhost shape refusal, judged on the authored shape
		// (inferComposeSlotShape, the runtime's AuthoredHostShape rule)
		// before any per-kind validator.
		// A request carrying matrices offers the MATRIX_RESULT host too
		// (overlayhost.JudgedShape — the runtime hooks' rule).
		judged := overlayhost.JudgedShape(inferComposeSlotShape(req), len(req.Matrices) > 0, opts.overlayRoute(spec.Kind))
		if cerr := overlayhost.ShapeRefusal(judged, spec.Kind, opts.overlayRoute(spec.Kind), i); cerr != nil {
			addCodedError(env, cerr)
			continue
		}
		// A SCALAR host (no crosstab, no matrices, no grouped
		// aggregations) runs no kind: the shared SCALAR host rule — the
		// kind's Ref / Scope table, then the host it needs — which the
		// runtime Process hooks (refuseWrongHostOverlays) and the chain
		// pre-pass raise too, same code, message and Details.
		if judged == types.OverlayShapeScalar {
			if cerr := overlayhost.ScalarSpecRefusal(spec, opts.overlayRoute(spec.Kind), len(req.Groups) > 0, i); cerr != nil {
				addCodedError(env, cerr)
				continue
			}
		}
		validateOverlaySpec(env, req, spec, opts, i)
		validateOverlayMatrixResult(env, req, spec, opts.overlayRoute(spec.Kind), i)
		validateOverlayCorrespondence(env, req, spec, schema, opts.instance(), opts.overlayRoute(spec.Kind), i)
		validateOverlayMarkov(env, req, spec, opts.overlayRoute(spec.Kind), i)
		validateOverlayRake(env, req, spec, schema, opts.instance(), opts.overlayRoute(spec.Kind), i)
		validateOverlayLevelWithinPredict(env, req, spec, opts.overlayRoute(spec.Kind), i)
		validateOverlayBaselineIndexPredict(env, req, spec, schema, opts.instance(), i)
		validateOverlaySeriesField(env, req, spec, opts.overlayRoute(spec.Kind), i,
			vectors.AggregationColumns(req, func() *encoding.Schema { return schema }, aggregationKinds(opts.instance(), nil)))
		validateOverlaySeriesPartition(env, req, spec, opts.overlayRoute(spec.Kind), i)
	}
}

// validateOverlaySeriesPartition refuses a series overlay whose
// params.partition_by is not a leading prefix of the request's group key
// columns (PULSE_OVERLAY_PARTITION_INVALID, Details {index, kind,
// partition_by, groups, reason[, field]}). With two or more groupers the
// prior / rolling / baseline / sibling kinds and the three _TOTAL kinds
// (seriespartition.Kinds) run once per partition — every key but the last by default — so the
// composite rows are no longer refused. The reading is
// internal/seriespartition.Depth, the one the runtime hook
// (processing.applyOverlaysSeriesToResponse) calls, so the refusal is
// identical on both sides. SERIES host only (Groups set, Crosstab nil).
func validateOverlaySeriesPartition(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if req == nil || spec == nil || req.Crosstab != nil || len(req.Groups) == 0 || !seriespartition.Applies(route) {
		return
	}
	if _, err := seriespartition.Depth(req.Groups, spec, index); err != nil {
		addCodedError(env, err)
	}
}

// validateOverlaySeriesField refuses a series overlay whose
// params.field names no request aggregation (PULSE_OVERLAY_FIELD_UNKNOWN,
// Details {index, kind, field, available}). Shared by every series kind
// beside the per-kind validators; the
// reading is internal/seriesfield.Resolve, the one the runtime host
// builder (processing.applyOverlaysSeriesToResponse) calls, so the
// refusal is identical on both sides. Runs on the defaults-resolved
// request, so a defaulted aggregation's TYPE_field label resolves. Only
// a SERIES host (Groups set, Crosstab nil) reads the param; a MATRIX
// host of SHARE_OF_TOTAL / ZSCORE_VS_TOTAL ignores it. cols lists the
// columns each aggregation writes over the request's schema
// (vectors.AggregationColumns), so an expanded element column
// "<label>[<element label>]" resolves; one the slot does not write is
// refused here, its element columns listed in Details.available.
func validateOverlaySeriesField(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int, cols seriesfield.Columns) {
	if req == nil || spec == nil || req.Crosstab != nil || len(req.Groups) == 0 || !seriesfield.Applies(route) {
		return
	}
	if _, err := seriesfield.Resolve(req.Aggregations, spec, index, cols); err != nil {
		addCodedError(env, err)
	}
}

// validateOverlayBaselineIndexPredict is the no-execute predict mirror of
// `processing.ResolveBaselineIndex`. It walks the Ref.BaselineIndex arm
// and surfaces `PULSE_OVERLAY_REF_UNKNOWN` for negative or out-of-range
// `Position` values. Mirrors the runtime resolver's
// `{baseline_index, series_length}` Details map shape so MCP / CLI
// envelopes carry the same structured context the runtime would have
// emitted.
//
// Foundation gate — the spec is intentionally tolerant of the
// other shipping overlay kinds because every other kind already
// rejects a populated `BaselineIndex` slot via its own per-kind validator
// (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE). When `BaselineIndex` is
// absent the gate is a no-op. When present:
//
//   - Negative `Position` always fires PULSE_OVERLAY_REF_UNKNOWN with
//     `series_length` derived from the predict-time schema upper bound
//     where computable, zero otherwise (the runtime resolver carries the
//     actual host length when the gate skipped predict).
//
//   - `Position >= predictedLength` fires PULSE_OVERLAY_REF_UNKNOWN ONLY
//     when the upper bound is derivable from the schema. Today the
//     only derivable case is a single-grouper SERIES host whose grouper
//     is `GROUP_CATEGORY` over a `categorical_u8 / u16 / u32` field —
//     the dict cardinality is the upper bound for the host's group-key
//     count. `GROUP_DATE` / `GROUP_RANGE` / `GROUP_ROUNDED` /
//     `GROUP_QUANTILE` upper bounds depend on bin width or runtime
//     content and cannot be predicted from the schema alone, so the
//     gate defers to runtime for those cases (the runtime resolver
//     still catches the out-of-range slot).
//
//   - Multi-grouper SERIES hosts: the cartesian product of dict
//     cardinalities is the upper bound, but only when every grouper
//     resolves to a categorical-dict-backed grouper. If any grouper is
//     non-categorical the predict gate defers to runtime (the cartesian
//     bound is not computable). When every grouper IS categorical the
//     gate multiplies dict counts; product is still an upper bound (the
//     actual host may emit fewer keys because of empty buckets, but the
//     resolver only fails when `Position` is strictly outside the
//     cartesian, which is always also outside the actual host length).
//
//   - Crosstab hosts: BaselineIndex.Position is the SERIES-host arm of
//     the OverlayBaselineIndexRef union — it has no meaning against a
//     MATRIX host (the Row + Column arms are the MATRIX-host slot, and
//     no shipping kind consumes those). The gate skips when
//     `req.Crosstab` is set so a future MATRIX-host kind landing on a
//     different Ref arm does not collide with the SERIES check.
func validateOverlayBaselineIndexPredict(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, schema *encoding.Schema, inst *InstanceSnapshot, index int) {
	if spec == nil || spec.Ref.BaselineIndex == nil {
		return
	}
	if req != nil && req.Crosstab != nil {
		// MATRIX host arm — the Position slot has no SERIES context. The
		// per-kind validator already rejects any populated BaselineIndex
		// pointer on the shipping MATRIX kinds; nothing further to gate
		// here.
		return
	}
	ref := spec.Ref.BaselineIndex
	// Compute the predicted series length upper bound. -1 means "not
	// derivable from schema; defer the range check to runtime".
	predictedLength := overlayBaselineIndexPredictedSeriesLength(req, schema, inst)
	if ref.Position < 0 {
		env.AddError(string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			"overlay "+string(spec.Kind)+" baseline-index position must be non-negative",
			map[string]any{
				"index":          index,
				"kind":           string(spec.Kind),
				"baseline_index": ref.Position,
				"series_length":  baselineIndexSeriesLengthDetail(predictedLength),
			})
		return
	}
	if predictedLength < 0 {
		// Schema cannot bound the host series length (non-categorical
		// grouper or other runtime-derivable surface). Defer to the
		// runtime resolver.
		return
	}
	if ref.Position >= predictedLength {
		env.AddError(string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			"overlay "+string(spec.Kind)+" baseline-index position exceeds the predicted host series length",
			map[string]any{
				"index":          index,
				"kind":           string(spec.Kind),
				"baseline_index": ref.Position,
				"series_length":  predictedLength,
			})
		return
	}
}

// overlayBaselineIndexPredictedSeriesLength returns the upper-bound
// predicted series length for the SERIES host implied by req, computed
// purely from schema state (dict cardinalities). Returns -1 when the
// schema cannot bound the host length (any grouper is non-categorical,
// schema is nil, or the host is not a SERIES host).
//
// Today the schema-derivable surface is GROUP_CATEGORY over a
// categorical_* field — the dict count is the upper bound for that
// axis. GROUP_DATE / GROUP_RANGE / GROUP_ROUNDED / GROUP_QUANTILE all
// produce bin counts that depend on the cohort content or
// caller-supplied bin width, so the gate defers to runtime for those.
// Multi-grouper hosts multiply per-axis dict counts (cartesian upper
// bound); a single non-categorical grouper anywhere in the list yields
// -1 (the cartesian is non-computable). A grouper inst hides counts as
// a non-categorical (never-registered) one.
func overlayBaselineIndexPredictedSeriesLength(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) int {
	if req == nil || schema == nil {
		return -1
	}
	if len(req.Groups) == 0 {
		return -1
	}
	product := 1
	for _, g := range req.Groups {
		if g == nil {
			return -1
		}
		if opRoute(inst, g.Type) != types.GROUP_CATEGORY {
			return -1
		}
		f := schema.Field(g.Field)
		if f == nil || !f.Type.IsCategorical() || f.Dictionary == nil {
			return -1
		}
		count := f.Dictionary.Count()
		if count <= 0 {
			return -1
		}
		product *= count
		if product <= 0 {
			// Defensive guard against overflow on pathologically wide
			// multi-axis hosts (a 32-bit overflow lands non-positive on
			// most platforms). Treat as non-derivable so the runtime
			// resolver owns the range check.
			return -1
		}
	}
	return product
}

// baselineIndexSeriesLengthDetail returns a JSON-friendly representation
// of the predicted series length for the negative-Position error
// Details map. Negative `predictedLength` (the "not derivable from
// schema" sentinel) surfaces as 0 so the wire detail map carries an
// integer rather than a Go-internal sentinel; the runtime mirror's
// `series_length` slot is always the actual host length.
func baselineIndexSeriesLengthDetail(predictedLength int) int {
	if predictedLength < 0 {
		return 0
	}
	return predictedLength
}

// validateOverlayLevelWithinPredict mirrors the runtime
// processing.validateOverlayLevelWithinRuntime gate at predict time.
// Rules:
//
//   - For the share / index / delta / zscore family Level / Within
//     are each in `[0, axisDepth)` for their respective axis. The
//     axis Level addresses is the same axis the overlay is centerpoint-
//     locked to; Within addresses the OPPOSITE axis. Out-of-range
//     fires PULSE_OVERLAY_LEVEL_OUT_OF_RANGE with Details carrying
//     the spec index, kind, level / within, and axis depth.
//
//   - For the χ² / Fisher inferential family Level / Within MUST be
//     zero — those handlers compute their own contingency from the
//     host margins inline and Level / Within would alter the implicit-
//     margin contract. Non-zero values fire
//     PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
//
//   - When req.Crosstab is nil the gate skips (the per-kind ref/scope
//     check already surfaced PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE).
//
// Zero defaults (Level == 0 && Within == 0) pass — the runtime
// resolver short-circuits to the legacy MarginFor lookup, preserving
// the byte-identity contract.
//
// route is the kind the gate keys on (PredictOptions.overlayRoute): a
// kind the instance hides takes the never-registered branches, while
// messages keep naming spec.Kind.
func validateOverlayLevelWithinPredict(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, route types.OverlayKind, index int) {
	if spec == nil {
		return
	}
	// INDEX_VS_TOTAL is a SERIES-host kind (req.Groups, no req.Crosstab);
	// its Level / Within gate is independent of the crosstab axis depths
	// since the implicit-grand-total denominator does not partition by
	// any axis prefix. Run the gate before the no-crosstab short-circuit
	// so the rule still fires when Request.Crosstab is nil.
	if route == types.OverlayKindIndexVsTotal {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-grand-total kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// DELTA_VS_SIBLING / INDEX_VS_SIBLING are SERIES-host kinds
	// (req.Groups, no req.Crosstab) whose Level / Within gate mirrors
	// INDEX_VS_TOTAL because the sibling reference is a SINGLE FIXED
	// group identified by Ref.Sibling.{Field, Value}, NOT an axis-
	// prefix denominator. Level / Within would alter the implicit
	// sibling-reference contract (the resolver would have to descend
	// into a prefix-bucket rather than match a single group), which
	// is out of scope for v1 of the sibling family. Non-zero values
	// fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. Run the gate before the
	// no-crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil.
	if route == types.OverlayKindDeltaVsSibling || route == types.OverlayKindIndexVsSibling {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (sibling reference is a single fixed group)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// DELTA_VS_BASELINE is a windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_BASELINE
	// because the baseline is a single fixed positional anchor
	// (Ref.BaselineIndex.Position), not an axis prefix. Absolute-difference
	// twin of INDEX_VS_BASELINE — same windowed-family implicit-margin rule.
	// Run the gate before the no-crosstab short-circuit so the rule still
	// fires when Request.Crosstab is nil.
	if route == types.OverlayKindDeltaVsBaseline {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed positional baseline is a single fixed anchor without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// INDEX_VS_BASELINE is a windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_TOTAL /
	// INDEX_VS_PRIOR because the baseline is a single fixed positional
	// anchor (Ref.BaselineIndex.Position), not an axis prefix. Run the gate
	// before the no-crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil. Implicit-margin / windowed family rule.
	if route == types.OverlayKindIndexVsBaseline {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed positional baseline is a single fixed anchor without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// INDEX_VS_PRIOR is a windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_TOTAL
	// because the single-state lag carrier folds across the ordered axis
	// without a prefix-bucket denominator. Run the gate before the no-
	// crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil. Implicit-margin / windowed family rule.
	if route == types.OverlayKindIndexVsPrior {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed lag carrier folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// DELTA_VS_PRIOR carries the identical windowed lag carrier and so
	// the identical Level / Within prohibition — the carrier folds across
	// the ordered axis without a prefix-bucket denominator whether the
	// finalize step divides or subtracts. Run the gate before the
	// no-crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil. Implicit-margin / windowed family rule.
	if route == types.OverlayKindDeltaVsPrior {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed lag carrier folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// INDEX_VS_ROLLING_MEAN is a windowed-SERIES kind (req.Groups,
	// no req.Crosstab); its Level / Within gate mirrors INDEX_VS_PRIOR
	// because the rolling-window carrier folds across the ordered axis
	// without a prefix-bucket denominator. Run the gate before the no-
	// crosstab short-circuit so the rule still fires when
	// Request.Crosstab is nil. Implicit-margin / windowed family rule.
	if route == types.OverlayKindIndexVsRollingMean {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed rolling-mean carrier folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// OVERLAY_YOY is a windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_PRIOR /
	// INDEX_VS_ROLLING_MEAN because the year-over-year prior-period
	// lookup folds across the ordered axis without a prefix-bucket
	// denominator. Same implicit-margin / windowed family rule.
	if route == types.OverlayKindYoY {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed year-over-year lookup folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// ZSCORE_VS_ROLLING is a windowed-SERIES kind (req.Groups, no
	// req.Crosstab); its Level / Within gate mirrors INDEX_VS_ROLLING_MEAN
	// because the rolling-window carrier folds across the ordered axis
	// without a prefix-bucket denominator. Sibling windowed-rolling kind
	// — same implicit-margin / windowed family rule.
	if route == types.OverlayKindZScoreVsRolling {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (windowed rolling sample-SD carrier folds across the ordered axis without a prefix-bucket denominator)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// ZSCORE_VS_TOTAL is a SERIES-host kind (req.Groups, no req.Crosstab);
	// its Level / Within gate mirrors INDEX_VS_TOTAL because the
	// implicit-grand-total mean + SD do not partition by any axis prefix.
	// Run the gate before the no-crosstab short-circuit so the rule still
	// fires when Request.Crosstab is nil. Sibling rule to the
	// INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES dispatch above.
	if route == types.OverlayKindZScoreVsTotal {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-grand-total kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// SHARE_OF_TOTAL SERIES dispatch honours the same implicit-
	// grand-total contract as INDEX_VS_TOTAL — Level / Within must both
	// be zero. The MATRIX dispatch falls through to the
	// crosstab-axis-depth check below where the kind's row+col depths
	// are accepted, preserving the byte-identity contract with the
	// original SHARE_OF_TOTAL MATRIX requests. Host-shape disambiguation
	// matches `validateOverlayShareOfTotal`'s dispatch policy.
	if route == types.OverlayKindShareOfTotal && req != nil && req.Crosstab == nil && len(req.Groups) > 0 {
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" SERIES dispatch does not support Level / Within (implicit-grand-total contract)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
					"host":   "series",
				})
		}
		return
	}
	if req == nil || req.Crosstab == nil {
		return
	}
	switch route {
	case types.OverlayKindChiSqCol,
		types.OverlayKindChiSqMatrix,
		types.OverlayKindChiSqRow,
		types.OverlayKindFisherExactCell,
		types.OverlayKindStdResidual,
		types.OverlayKindCorrespondence,
		types.OverlayKindMarkov,
		types.OverlayKindRake,
		types.OverlayKindSymmetry,
		types.OverlayKindRetention:
		// Inferential family (and the implicit-margin whole-table
		// CORRESPONDENCE / MARKOV / RAKE / RETENTION) — Level / Within
		// must both be zero.
		if spec.Level != 0 || spec.Within != 0 {
			env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
				"overlay "+string(spec.Kind)+" does not support Level / Within (implicit-margin inferential kind)",
				map[string]any{
					"index":  index,
					"kind":   string(spec.Kind),
					"level":  spec.Level,
					"within": spec.Within,
				})
		}
		return
	}
	// Share / index / delta / zscore family — Level on SAME axis,
	// Within on OPPOSITE axis. Resolve the axis pair off the kind so
	// the gate stays kind-aware (mirrors the runtime dispatch).
	levelAxisDepth, withinAxisDepth, levelAxisLabel, withinAxisLabel := overlayLevelWithinAxisDepthsPredict(spec, route, req)
	if spec.Level < 0 || (levelAxisDepth > 0 && spec.Level >= levelAxisDepth) {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Level is out of range for the "+levelAxisLabel+" axis",
			map[string]any{
				"index":      index,
				"kind":       string(spec.Kind),
				"level":      spec.Level,
				"axis":       levelAxisLabel,
				"axis_depth": levelAxisDepth,
			})
	}
	if spec.Within < 0 || (withinAxisDepth > 0 && spec.Within >= withinAxisDepth) {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Within is out of range for the "+withinAxisLabel+" axis",
			map[string]any{
				"index":      index,
				"kind":       string(spec.Kind),
				"within":     spec.Within,
				"axis":       withinAxisLabel,
				"axis_depth": withinAxisDepth,
			})
	}
}

// overlayLevelWithinAxisDepthsPredict resolves which crosstab axis the
// spec's Level / Within slots address. Returns the axis depths
// (len(Rows) / len(Columns)) and axis labels ("rows" / "columns") for
// the error details.
//
//   - SHARE_OF_ROW: Level on ROW axis, Within on COLUMN axis.
//   - SHARE_OF_COL: Level on COLUMN axis, Within on ROW axis.
//   - SHARE_OF_TOTAL: Level / Within nominally on grand; the helper
//     returns row + column depths so non-zero values are accepted
//     within the row / column range (SHARE_OF_TOTAL ignores Level /
//     Within at runtime but the predict gate does not need to reject
//     them — they are inert).
//   - INDEX_VS_MARGIN / DELTA_VS_MARGIN / ZSCORE_VS_MARGIN: axis is
//     driven by Ref.Margin.Axis; Level addresses that axis and
//     Within addresses the opposite one.
func overlayLevelWithinAxisDepthsPredict(spec *types.OverlaySpec, route types.OverlayKind, req *types.Request) (
	levelAxisDepth, withinAxisDepth int,
	levelAxisLabel, withinAxisLabel string,
) {
	if req == nil || req.Crosstab == nil {
		return 0, 0, "rows", "columns"
	}
	rowDepth := len(req.Crosstab.Rows)
	colDepth := len(req.Crosstab.Columns)
	switch route {
	case types.OverlayKindShareOfRow:
		return rowDepth, colDepth, "rows", "columns"
	case types.OverlayKindShareOfCol:
		return colDepth, rowDepth, "columns", "rows"
	case types.OverlayKindShareOfTotal:
		return rowDepth, colDepth, "rows", "columns"
	case types.OverlayKindIndexVsMargin,
		types.OverlayKindDeltaVsMargin,
		types.OverlayKindZScoreVsMargin:
		if spec.Ref.Margin == nil {
			return 0, 0, "rows", "columns"
		}
		switch spec.Ref.Margin.Axis {
		case types.MarginAxisRow:
			return rowDepth, colDepth, "rows", "columns"
		case types.MarginAxisColumn:
			return colDepth, rowDepth, "columns", "rows"
		case types.MarginAxisGrand:
			return rowDepth, colDepth, "rows", "columns"
		}
	}
	return 0, 0, "rows", "columns"
}

// OverlayReadsHostComponents reports whether a Request.Overlays kind,
// over a crosstab host whose cell is weighted under cellBasis
// (CrosstabCellWeightBasis), reads that host's
// Response.Components.Crosstab — the rule the service's compute plan
// vetoes a `return` skip with (U18), beside its refusal half
// validateOverlayHiddenFloor. The OVERLAY_PAIRWISE_* family always
// reads per-cell floors, Welford triples, margin and slab counts
// (PULSE_OVERLAY_COMPONENTS_REQUIRED without them). The
// weighting.ScalesByHostFloor kinds and OVERLAY_FISHER_EXACT_CELL read
// the cells' weighted floors only on a WEIGHTED host — the stamped
// sum_weights their summary Parameters carry (frequency), plus the Kish
// n_eff the χ² table is scaled by (probability); on an unweighted host
// CellFloor reads nothing and their layer is the MatrixPayload's alone.
// Their host basis comes from the stamped cell weight, never from the
// floor keys, so a skip cannot hide a weighted host from Fisher's
// frequency-only refusal. Every other host kind reads the MatrixPayload
// only. Components.Crosstab is the only Components sub-part any overlay
// reads.
func OverlayReadsHostComponents(kind types.OverlayKind, cellBasis weighting.Basis) bool {
	if types.IsPairwiseOverlayKind(kind) {
		return true
	}
	return (weighting.ScalesByHostFloor(kind) || kind == types.OverlayKindFisherExactCell) && cellBasis.Weighted()
}

// validateOverlaySpec applies the per-kind ruleset to one OverlaySpec.
// Errors are emitted with deterministic Details so MCP / CLI
// envelopes can render the index, kind, and offending value without
// re-parsing the message string.
//
// `opts` is threaded through so per-kind validators reach
// `opts.Extensions` for embedder-side state. Most kinds do not
// consume opts today; the FORMULA dispatch reads
// `opts.Extensions.ExprFunctions` to widen the allowed identifier set.
// validateOverlayHiddenFloor is the predict twin of the runtime's
// components-disabled refusal (processing.ApplyOverlaysWithExtensions,
// weighting-inferential E3-S3): a kind that scales the host's Σw by the
// n_eff on its floor, over a crosstab cell weighted under kind
// probability whose components are disabled (the request's
// disable_components, else Options.DisableComponents). Every case it
// refuses is one OverlayReadsHostComponents flags (a floor kind over a
// weighted cell), so a `return` skip never manufactures the refusal.
func validateOverlayHiddenFloor(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
	if !weighting.ScalesByHostFloor(spec.Kind) || !opts.componentsDisabled(req) ||
		crosstabCellWeightBasis(req, opts) != weighting.Probability {
		return
	}
	msg, details := weighting.HiddenFloorRefusal("overlays["+strconv.Itoa(index)+"]", spec.Kind, "crosstab.cell")
	env.AddError(string(errors.PROCESSING_CONFIG), msg, details)
}

func validateOverlaySpec(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, opts *PredictOptions, index int) {
	if spec == nil {
		return
	}

	// Unknown-kind check first — every other rule is keyed by Kind so
	// we cannot reasonably validate Scope / Ref against an unknown
	// catalog entry. OverlayStreamable(known=false) is the authoritative
	// "is this kind in the catalog?" probe; AllOverlayKinds() and the
	// streamability table are co-maintained per TestStreamability_OverlaysKnown.
	if _, known := types.OverlayStreamable(opts.overlayRoute(spec.Kind)); !known {
		env.AddError(string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
			"overlay kind is not in the catalog: "+string(spec.Kind),
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
			})
		return
	}

	switch spec.Kind {
	case types.OverlayKindChiSqCol:
		validateOverlayChiSqCol(env, req, spec, index)
		validateOverlayHiddenFloor(env, req, spec, opts, index)
	case types.OverlayKindChiSqMatrix:
		validateOverlayChiSqMatrix(env, req, spec, index)
		validateOverlayHiddenFloor(env, req, spec, opts, index)
	case types.OverlayKindStdResidual:
		validateOverlayStdResidual(env, req, spec, index)
		validateOverlayHiddenFloor(env, req, spec, opts, index)
	case types.OverlayKindSymmetry:
		validateOverlaySymmetry(env, req, spec, opts.overlayRoute(spec.Kind), index)
		validateOverlayHiddenFloor(env, req, spec, opts, index)
	case types.OverlayKindRetention:
		validateOverlayRetention(env, req, spec, opts.overlayRoute(spec.Kind), index)
	case types.OverlayKindChiSqRow:
		validateOverlayChiSqRow(env, req, spec, index)
		validateOverlayHiddenFloor(env, req, spec, opts, index)
	case types.OverlayKindDeltaVsBaseline:
		validateOverlayDeltaVsBaseline(env, req, spec, index)
	case types.OverlayKindDeltaVsMargin:
		validateOverlayDeltaVsMargin(env, req, spec, index)
	case types.OverlayKindDeltaVsSibling:
		validateOverlayDeltaVsSibling(env, req, spec, index)
	case types.OverlayKindFisherExactCell:
		validateOverlayFisherExactCell(env, req, spec, index)
		// Frequency-only on a host whose crosstab cell resolves a
		// probability weight the overlay slot itself does not see
		// (weighting-inferential E3-S2); the runtime twin is
		// processing.ApplyOverlaysWithExtensions, same code and message.
		if crosstabCellWeightBasis(req, opts) == weighting.Probability {
			msg, details := weighting.FrequencyOnlyHostRefusal("overlays["+strconv.Itoa(index)+"]", string(spec.Kind))
			env.AddError(string(errors.PULSE_WEIGHT_UNSUPPORTED), msg, details)
		}
	case types.OverlayKindPairwiseProbitT,
		types.OverlayKindPairwisePropZ,
		types.OverlayKindPairwiseTwoMeansZ,
		types.OverlayKindPairwiseWeightedTwoMeansZ,
		types.OverlayKindPairwiseWelchT:
		validateOverlayPairwise(env, req, spec, opts, index)
	case types.OverlayKindFormula:
		validateFormulaOverlay(env, req, spec, opts, index)
	case types.OverlayKindIndexVsBaseline:
		validateOverlayIndexVsBaseline(env, req, spec, index)
	case types.OverlayKindIndexVsMargin:
		validateOverlayIndexVsMargin(env, req, spec, index)
	case types.OverlayKindIndexVsPrior:
		validateOverlayIndexVsPrior(env, req, spec, index)
	case types.OverlayKindDeltaVsPrior:
		validateOverlayDeltaVsPrior(env, req, spec, index)
	case types.OverlayKindIndexVsRollingMean:
		validateOverlayIndexVsRollingMean(env, req, spec, index)
	case types.OverlayKindIndexVsSibling:
		validateOverlayIndexVsSibling(env, req, spec, index)
	case types.OverlayKindIndexVsTotal:
		validateOverlayIndexVsTotal(env, req, spec, index)
	case types.OverlayKindShareOfCol:
		validateOverlayShareOfCol(env, req, spec, index)
	case types.OverlayKindShareOfRow:
		validateOverlayShareOfRow(env, req, spec, index)
	case types.OverlayKindShareOfTotal:
		validateOverlayShareOfTotal(env, req, spec, index)
	case types.OverlayKindYoY:
		validateOverlayYoY(env, req, spec, opts.instance(), index)
	case types.OverlayKindZScoreVsMargin:
		validateOverlayZScoreVsMargin(env, req, spec, index)
	case types.OverlayKindZScoreVsRolling:
		validateOverlayZScoreVsRolling(env, req, spec, index)
	case types.OverlayKindZScoreVsTotal:
		validateOverlayZScoreVsTotal(env, req, spec, index)
	}
}

// validateOverlayIndexVsMargin enforces the per-kind contract for
// OVERLAY_INDEX_VS_MARGIN: Ref must populate Margin, Margin.Axis must
// be a known MarginAxis, the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be in the supported
// set. Every condition emits a distinct error so a caller surfacing
// multiple structural problems sees them all in one pass.
func validateOverlayIndexVsMargin(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped. Today the only MATRIX-shaped host is
	// req.Crosstab; without it there are no margin slots for the overlay
	// to reference. Future kinds may broaden the matrix-host predicate
	// (e.g. when group-result overlays land), but INDEX_VS_MARGIN
	// specifically derives its denominator from a crosstab margin.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayIndexVsTotal enforces the per-kind contract for
// OVERLAY_INDEX_VS_TOTAL: the Ref union must be EMPTY (implicit-
// grand-total — the host series' own grand total is the denominator),
// the host result must be SERIES-shaped (i.e. Request.Groups is non-
// empty and Request.Crosstab is nil), and Scope must be GROUP.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host, not a MATRIX one) OR
//     when Request.Groups is empty (no host series to compute against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayIndexVsTotal(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	// A request with no groupers has no series to compute against.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayIndexVsPrior enforces the per-kind contract for
// OVERLAY_INDEX_VS_PRIOR (first windowed-Process kind in the catalog and
// first consumer of the `Ref.Prior` arm of the discriminated
// OverlayRef union):
//
//   - Ref.Prior populated → accepted. Ref.Prior.Lag MUST be zero (v1
//     ships lag-1 only via the implicit-default arm; the slot is
//     forward-compat for future window-N priors).
//   - Ref entirely empty → accepted (the implicit-default authoring
//     shape — both spellings spell "lag-1 prior").
//   - Any other ref-family pointer populated (Margin / Sibling /
//     BaselineIndex / Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty — the kind targets a windowed ordered-
//     axis SERIES host, not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL
//     SERIES / ZSCORE_VS_TOTAL).
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the lag
// carrier folds across the ordered axis without a prefix-bucket
// denominator, so non-zero Level / Within values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The runtime mirror
// (processing.validateOverlayLevelWithinRuntime) enforces the same
// rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayIndexVsPrior(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayDeltaVsPrior enforces the per-kind contract for
// OVERLAY_DELTA_VS_PRIOR — the additive twin of OVERLAY_INDEX_VS_PRIOR
// and the second consumer of the `Ref.Prior` arm of the discriminated
// OverlayRef union. The contract is the index twin's, unchanged:
//
//   - Ref.Prior populated → accepted. Ref.Prior.Lag MUST be zero (v1
//     ships lag-1 only via the implicit-default arm; the slot is
//     forward-compat for future window-N priors).
//   - Ref entirely empty → accepted (the implicit-default authoring
//     shape — both spellings spell "lag-1 prior").
//   - Any other ref-family pointer populated (Margin / Sibling /
//     BaselineIndex / Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty).
//   - Scope must be GROUP.
//
// Written out rather than delegating to validateOverlayIndexVsPrior for
// the same reason validateOverlayDeltaVsBaseline does not delegate to
// validateOverlayIndexVsBaseline: the two kinds are free to diverge,
// and a shared validator would make one kind's contract change silently
// rewrite the other's.
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the lag
// carrier folds across the ordered axis without a prefix-bucket
// denominator, so non-zero Level / Within values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The runtime mirror
// (processing.validateOverlayLevelWithinRuntime) enforces the same
// rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayDeltaVsPrior(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayIndexVsRollingMean enforces the per-kind contract for
// OVERLAY_INDEX_VS_ROLLING_MEAN (a windowed-Process kind that is the
// first consumer of the `Ref.RollingMean` arm of the OverlayRef
// discriminated union):
//
//   - Ref.RollingMean MUST be populated (the empty marker struct tags the
//     ref family; the v1 window value lives on Params per the WIN_*
//     operator convention). Empty Ref is rejected with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Any other ref-family pointer populated (Margin / Sibling / Prior /
//     BaselineIndex / Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND Request.Groups
//     non-empty — the kind targets a windowed ordered-axis SERIES host,
//     not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_PRIOR / INDEX_VS_BASELINE /
//     INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES / ZSCORE_VS_TOTAL).
//   - Params["window"] MUST be present and MUST be a positive integer.
//     Missing → PULSE_OVERLAY_PARAM_MISSING with {kind, param} Details.
//     Non-integer / non-positive → PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//     with {kind, window} Details.
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the rolling-
// mean carrier folds across the ordered axis without a prefix-bucket
// denominator, so non-zero Level / Within values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The runtime mirror
// (processing.validateOverlayLevelWithinRuntime) enforces the same rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayIndexVsRollingMean(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Params["window"] validation. The window width lives on Params per
	// the WIN_* operator convention; the kind cannot run without it.
	validateOverlayRollingWindowParam(env, spec, index)
}

// validateOverlayZScoreVsRolling enforces the per-kind contract for
// OVERLAY_ZSCORE_VS_ROLLING (windowed-Process kind that is the second
// consumer of the `Ref.RollingMean` arm of the OverlayRef discriminated
// union — sibling windowed-rolling kind to INDEX_VS_ROLLING_MEAN):
//
//   - Ref.RollingMean MUST be populated (the empty marker struct tags the
//     ref family; the v1 window value lives on Params per the WIN_*
//     operator convention — identical contract to INDEX_VS_ROLLING_MEAN).
//     Empty Ref is rejected with PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Any other ref-family pointer populated (Margin / Sibling / Prior /
//     BaselineIndex / Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND Request.Groups
//     non-empty — the kind targets a windowed ordered-axis SERIES host,
//     not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_ROLLING_MEAN / INDEX_VS_PRIOR /
//     INDEX_VS_BASELINE / INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES /
//     ZSCORE_VS_TOTAL).
//   - Params["window"] MUST be present and MUST be a positive integer.
//     Missing → PULSE_OVERLAY_PARAM_MISSING with {kind, param} Details.
//     Non-integer / non-positive → PULSE_OVERLAY_LEVEL_OUT_OF_RANGE
//     with {kind, window} Details. Routes through the shared
//     `validateOverlayRollingWindowParam` helper so the rolling family
//     stays parity-true at predict time.
//
// Mirrors `validateOverlayIndexVsRollingMean` shape verbatim — the two
// kinds share the same Ref-family / host-shape / scope / Params
// contract; only the runtime per-point math differs (z-score vs
// ratio). Level / Within rule lives in
// validateOverlayLevelWithinPredict — the kind is in the implicit-
// margin / windowed family because the rolling-window carrier folds
// across the ordered axis without a prefix-bucket denominator, so
// non-zero Level / Within values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE.
// The runtime mirror (processing.validateOverlayLevelWithinRuntime)
// enforces the same rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayZScoreVsRolling(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Params["window"] validation. The window width lives on Params per
	// the WIN_* operator convention; the kind cannot run without it.
	// Reuses the shared rolling-family helper so the predict gate shape
	// stays uniform across INDEX_VS_ROLLING_MEAN / ZSCORE_VS_ROLLING.
	validateOverlayRollingWindowParam(env, spec, index)
}

// validateOverlayYoY enforces the per-kind contract for OVERLAY_YOY
// (a windowed-Process kind that is the first consumer of the empty
// `Ref.YoY` marker arm of the OverlayRef discriminated union):
//
//   - Ref.YoY MUST be populated (the empty marker tags the ref family;
//     the v1 frequency value lives on Params per the WIN_* operator
//     convention OR on the host GROUP_DATE grouper's Params). Empty Ref
//     is rejected with PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Any other ref-family pointer populated (Margin / Sibling / Prior /
//     BaselineIndex / RollingMean / Population / Stage / Slot) → reject
//     with PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty — the kind targets a windowed
//     ordered-axis SERIES host, not a MATRIX one).
//   - Host first grouper MUST be GROUP_DATE. The YoY kind cannot
//     resolve "same period one year prior" semantics against
//     GROUP_CATEGORY / GROUP_RANGE / GROUP_ROUNDED / GROUP_QUANTILE /
//     GROUP_SET_VALUE hosts. Non-DATE first grouper fires
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE with Details naming the
//     actual grouper type.
//   - Scope must be GROUP (mirrors INDEX_VS_PRIOR / INDEX_VS_BASELINE /
//     INDEX_VS_ROLLING_MEAN / ZSCORE_VS_ROLLING).
//   - Frequency Param MUST be present (either on the OverlaySpec.Params
//     slot OR on the host GROUP_DATE grouper's Params). Missing both
//     fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING with {kind, host_grouper}
//     Details.
//   - Frequency value MUST be in the supported set (annual | quarterly |
//     monthly | weekly | daily | hourly). Out-of-set values fire
//     PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY with {frequency,
//     supported} Details.
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the
// year-over-year lookup folds across the ordered axis without a
// prefix-bucket denominator, so non-zero Level / Within values fire
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The runtime mirror
// (processing.validateOverlayLevelWithinRuntime) enforces the same
// rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayYoY(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, inst *InstanceSnapshot, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Exactly one GROUP_DATE grouper, anywhere in groups; the overlay
	// runs once per combination of every other key, and an explicit
	// params.partition_by must name exactly those non-date keys (U46
	// E2-S3, #429). seriespartition.YoY is the one reading — the
	// runtime fold (processing.seriesPartitionFor) calls it too — so the
	// refusals cannot drift. A GROUP_DATE the instance hides is not a
	// date grouper here; messages keep naming the authored type.
	isDate := func(g *types.Group) bool { return opRoute(inst, g.Type) == types.GROUP_DATE }
	if _, _, err := seriespartition.YoY(req.Groups, isDate, spec, index); err != nil {
		addCodedError(env, err)
		return
	}

	// Frequency resolution. The handler reads spec.Params["frequency"]
	// first (the YoY's own override) and falls back to
	// the GROUP_DATE grouper's Params["frequency"] (wherever it sits
	// in groups). The predict gate walks both surfaces. Missing
	// both fires PULSE_OVERLAY_YOY_FREQUENCY_MISSING; out-of-set value
	// fires PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY.
	validateOverlayYoYFrequency(env, req, spec, index, seriespartition.DateGrouper(req.Groups, isDate))
}

// validateOverlayYoYFrequency validates the OVERLAY_YOY `frequency`
// Param against the supported set. Reads spec.Params["frequency"]
// first (the YoY's own override) and falls back to
// the GROUP_DATE grouper's Params["frequency"] (groups[date],
// wherever it sits). Surfaces PULSE_OVERLAY_YOY_FREQUENCY_MISSING when
// neither carries the slot and PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY
// when the value is out-of-set. Mirrors the runtime gate in
// processing.extractYoYFrequency — the predict gate surfaces the same
// failure shapes so MCP / CLI envelopes carry the same structured
// context the runtime would have emitted.
func validateOverlayYoYFrequency(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index, date int) {
	frequency, source := readYoYFrequencyForPredict(spec, req, date)
	hostGrouper := string(types.GROUP_DATE)
	if frequency == "" {
		env.AddError(string(errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING),
			"overlay "+string(spec.Kind)+" requires a `frequency` Param on either OverlaySpec.Params or the host GROUP_DATE grouper's Params (one of: annual | quarterly | monthly | weekly | daily | hourly)",
			map[string]any{
				"index":        index,
				"kind":         string(spec.Kind),
				"param":        "frequency",
				"host_grouper": hostGrouper,
			})
		return
	}
	if !isYoYSupportedFrequencyPredict(frequency) {
		env.AddError(string(errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY),
			"overlay "+string(spec.Kind)+" `frequency` Param value is not in the supported set (annual | quarterly | monthly | weekly | daily | hourly): "+frequency,
			map[string]any{
				"index":     index,
				"kind":      string(spec.Kind),
				"frequency": frequency,
				"source":    source,
				"supported": append([]string(nil), yoySupportedFrequencies...),
			})
		return
	}
}

// readYoYFrequencyForPredict reads the OVERLAY_YOY frequency Param from
// the OverlaySpec.Params slot first (the YoY's own override) and falls
// back to the host GROUP_DATE grouper's Params second. Returns
// (frequency, source) where `source` names which slot the frequency
// came from ("spec_params" | "group_params" | ""). When neither slot
// carries a usable frequency the helper returns ("", "") and the
// caller surfaces PULSE_OVERLAY_YOY_FREQUENCY_MISSING.
func readYoYFrequencyForPredict(spec *types.OverlaySpec, req *types.Request, date int) (string, string) {
	if freq, ok := readYoYFrequencyJSON(spec.Params); ok {
		return freq, "spec_params"
	}
	if req != nil && date >= 0 && date < len(req.Groups) && req.Groups[date] != nil {
		if freq, ok := readYoYFrequencyJSON(req.Groups[date].Params); ok {
			return freq, "group_params"
		}
	}
	return "", ""
}

// readYoYFrequencyJSON pulls the "frequency" key out of a raw JSON
// Params blob. Returns (value, true) when the blob is a valid object
// carrying a string-typed "frequency" entry; ("", false) when the
// blob is absent / empty / non-object / missing the key / the value
// is non-string. Mirrors processing.readYoYFrequencyFromParams (the
// runtime helper); the two surfaces stay parity-true.
func readYoYFrequencyJSON(params json.RawMessage) (string, bool) {
	if len(params) == 0 {
		return "", false
	}
	var m map[string]any
	if err := json.Unmarshal(params, &m); err != nil {
		return "", false
	}
	raw, present := m["frequency"]
	if !present {
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	if s == "" {
		return "", false
	}
	return s, true
}

// isYoYSupportedFrequencyPredict reports whether the given frequency
// string is in the OVERLAY_YOY supported set. Linear scan over the
// 6-element table; mirrors processing.isYoYSupportedFrequency.
func isYoYSupportedFrequencyPredict(frequency string) bool {
	for _, f := range yoySupportedFrequencies {
		if frequency == f {
			return true
		}
	}
	return false
}

// validateOverlayRollingWindowParam validates the OverlaySpec.Params
// "window" key for the windowed rolling-* family
// (OVERLAY_INDEX_VS_ROLLING_MEAN, OVERLAY_ZSCORE_VS_ROLLING).
// Both kinds carry the window width on Params["window"] per the WIN_*
// operator convention; the helper centralises the parse + range check
// so the rolling family stays parity-true at predict time. Surfaces
// PULSE_OVERLAY_PARAM_MISSING when the slot is absent and
// PULSE_OVERLAY_LEVEL_OUT_OF_RANGE when the value is non-integer or
// non-positive. Mirrors the runtime gate in
// processing.extractWindowParam — the predict gate surfaces the same
// failure shape so MCP / CLI envelopes carry the same structured
// context the runtime would have emitted.
func validateOverlayRollingWindowParam(env *descriptor.Envelope, spec *types.OverlaySpec, index int) {
	if len(spec.Params) == 0 {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" requires Params[\"window\"] (positive integer); Params is missing or empty",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"param": "window",
			})
		return
	}
	var m map[string]any
	if err := json.Unmarshal(spec.Params, &m); err != nil {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" Params must be a JSON object carrying \"window\"",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"param": "window",
			})
		return
	}
	raw, present := m["window"]
	if !present {
		env.AddError(string(errors.PULSE_OVERLAY_PARAM_MISSING),
			"overlay "+string(spec.Kind)+" requires Params[\"window\"] (positive integer)",
			map[string]any{
				"index": index,
				"kind":  string(spec.Kind),
				"param": "window",
			})
		return
	}
	window, ok := coerceIntegralOverlayParam(raw)
	if !ok {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Params[\"window\"] must be a positive integer",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"param":  "window",
				"window": raw,
			})
		return
	}
	if window <= 0 {
		env.AddError(string(errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE),
			"overlay "+string(spec.Kind)+" Params[\"window\"] must be > 0",
			map[string]any{
				"index":  index,
				"kind":   string(spec.Kind),
				"param":  "window",
				"window": window,
			})
		return
	}
	// The kind's floor (OVERLAY_ZSCORE_VS_ROLLING: >= 2), shared with the
	// runtime handler (U35 #431).
	if cerr := overlayhost.RollingWindowFloorRefusal(spec, spec.Kind, window, index); cerr != nil {
		addCodedError(env, cerr)
	}
}

// coerceIntegralOverlayParam coerces a JSON-decoded overlay Params
// value into an int. Returns (n, true) when the input is an integer or
// an integral float64; (0, false) otherwise. Mirrors
// processing.coerceIntegralNumber so the predict gate and runtime
// emission shape stay aligned.
func coerceIntegralOverlayParam(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		i := int(n)
		if float64(i) != n {
			return 0, false
		}
		return i, true
	case float32:
		f := float64(n)
		i := int(f)
		if float64(i) != f {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

// validateOverlayIndexVsBaseline enforces the per-kind contract for
// OVERLAY_INDEX_VS_BASELINE (a windowed-Process kind that is the first
// consumer of the `Ref.BaselineIndex.Position` arm):
//
//   - Ref.BaselineIndex MUST be populated (the resolver consumes Position
//     as the windowed positional anchor). Empty Ref is rejected with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Any other ref-family pointer populated (Margin / Sibling / Prior /
//     Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND Request.Groups
//     non-empty — the kind targets a windowed ordered-axis SERIES host,
//     not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES
//     / ZSCORE_VS_TOTAL / INDEX_VS_PRIOR).
//   - Ref.BaselineIndex.Position is range-checked downstream by
//     validateOverlayBaselineIndexPredict — negative or
//     out-of-range values fire PULSE_OVERLAY_REF_UNKNOWN with the
//     `{baseline_index, series_length}` Details map.
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the baseline
// is a single fixed positional anchor, not an axis prefix, so non-zero
// Level / Within values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The
// runtime mirror (processing.validateOverlayLevelWithinRuntime) enforces
// the same rule.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayIndexVsBaseline(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil (an
	// active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Position range check is delegated to
	// validateOverlayBaselineIndexPredict which runs alongside this
	// per-kind validator from ValidateOverlays. Negative or out-of-range
	// values fire PULSE_OVERLAY_REF_UNKNOWN.
}

// validateOverlayDeltaVsBaseline enforces the per-kind contract for
// OVERLAY_DELTA_VS_BASELINE (a windowed-Process kind that is the
// absolute-difference twin of OVERLAY_INDEX_VS_BASELINE):
//
//   - Ref.BaselineIndex MUST be populated (the resolver consumes Position
//     as the windowed positional anchor). Empty Ref is rejected with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Any other ref-family pointer populated (Margin / Sibling / Prior /
//     Population / Stage / Slot) → reject with
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.
//   - Host must be SERIES-shaped (Request.Crosstab nil AND Request.Groups
//     non-empty — the kind targets a windowed ordered-axis SERIES host,
//     not a MATRIX one).
//   - Scope must be GROUP (mirrors INDEX_VS_BASELINE / INDEX_VS_TOTAL /
//     SHARE_OF_TOTAL SERIES / ZSCORE_VS_TOTAL / INDEX_VS_PRIOR).
//   - Ref.BaselineIndex.Position is range-checked downstream by
//     validateOverlayBaselineIndexPredict — negative or out-of-range
//     values fire PULSE_OVERLAY_REF_UNKNOWN with the
//     `{baseline_index, series_length}` Details map.
//
// Level / Within rule lives in validateOverlayLevelWithinPredict — the
// kind is in the implicit-margin / windowed family because the baseline
// is a single fixed positional anchor, not an axis prefix, so non-zero
// Level / Within values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. The
// runtime mirror (processing.validateOverlayLevelWithinRuntime) enforces
// the same rule.
//
// Mirrors validateOverlayIndexVsBaseline shape verbatim — the two kinds
// share the same Ref-family / host-shape / scope contract; only the
// runtime per-point math differs (subtraction vs. ratio).
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayDeltaVsBaseline(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil (an
	// active crosstab routes the request down the MATRIX-host path).
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}

	// Position range check is delegated to
	// validateOverlayBaselineIndexPredict which runs alongside this
	// per-kind validator from ValidateOverlays. Negative or out-of-range
	// values fire PULSE_OVERLAY_REF_UNKNOWN.
}

// validateOverlayChiSqMatrix enforces the per-kind contract for
// OVERLAY_CHISQ_MATRIX: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be MATRIX, and the Ref
// union must be EMPTY — unlike the Margin-bearing CELL overlays the
// χ² independence test is implicit-margin and uses the host's row /
// column / grand margins inline. A caller-supplied Ref.Margin (or any
// other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref arm is set
//     (overlayhost.CrosstabSpecRefusal, judged first).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than MATRIX.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
func validateOverlayChiSqMatrix(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — χ² independence consumes a row ×
	// column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayChiSqRow enforces the per-kind contract for
// OVERLAY_CHISQ_ROW: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be ROW, and the Ref union
// must be EMPTY — the per-row χ² goodness-of-fit test is implicit-
// margin and uses the host's row / column / grand margins inline
// (mirrors the CHISQ_MATRIX contract). A caller-supplied Ref.Margin
// (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref arm is set
//     (overlayhost.CrosstabSpecRefusal, judged first).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than ROW.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
func validateOverlayChiSqRow(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — per-row χ² goodness-of-fit consumes
	// a row × column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayChiSqCol enforces the per-kind contract for
// OVERLAY_CHISQ_COL: the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), Scope must be COLUMN, and the Ref union
// must be EMPTY — the per-column χ² goodness-of-fit test is implicit-
// margin and uses the host's row / column / grand margins inline
// (mirrors the CHISQ_MATRIX / CHISQ_ROW contract). A caller-supplied
// Ref.Margin (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref arm is set
//     (overlayhost.CrosstabSpecRefusal, judged first).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than COLUMN.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
func validateOverlayChiSqCol(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — per-column χ² goodness-of-fit
	// consumes a row × column contingency table sourced from the host
	// crosstab.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayDeltaVsMargin enforces the per-kind contract for
// OVERLAY_DELTA_VS_MARGIN: Ref must populate Margin (the axis-margin
// reference family), Margin.Axis must be a known MarginAxis (any of
// row / column / grand — unlike the SHARE_OF_* triad the runtime
// handler dispatches all three axes, mirroring INDEX_VS_MARGIN and
// ZSCORE_VS_MARGIN), the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayDeltaVsMargin(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — delta-vs-margin needs a crosstab
	// margin slot to subtract from each cell.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayFisherExactCell enforces the per-kind contract for
// OVERLAY_FISHER_EXACT_CELL: the host result must be MATRIX-shaped
// (i.e. Request.Crosstab is non-nil), Scope must be CELL, and the Ref
// union must be EMPTY — the per-cell 2×2 Fisher's exact test is
// implicit-margin and reads the host's row + column margins inline
// (mirrors the CHISQ_* implicit-margin contract). A caller-supplied
// Ref.Margin (or any other ref-family pointer) is a shape mismatch.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref arm is set
//     (overlayhost.CrosstabSpecRefusal, judged first).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than CELL.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when req.Crosstab is
//     nil (no MATRIX host to test against).
func validateOverlayFisherExactCell(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — per-cell Fisher's exact consumes a
	// row × column contingency table sourced from the host crosstab.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayShareOfRow enforces the per-kind contract for
// OVERLAY_SHARE_OF_ROW: Ref must populate Margin (the row-margin
// reference family), Margin.Axis must be a known MarginAxis (the
// runtime handler is row-axis-locked, but the validator accepts any
// known axis at predict time so a misconfigured caller fails closed
// with a single shape-mismatch code rather than a stricter "must be
// row" code), the host result must be MATRIX-shaped (i.e.
// Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayShareOfRow(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — share-of-row needs a crosstab row
	// margin to divide by.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayShareOfCol enforces the per-kind contract for
// OVERLAY_SHARE_OF_COL: Ref must populate Margin (the column-margin
// reference family), Margin.Axis must be a known MarginAxis (the
// runtime handler is column-axis-locked, but the validator accepts
// any known axis at predict time so a misconfigured caller fails
// closed with a single shape-mismatch code rather than a stricter
// "must be column" code — matches the SHARE_OF_ROW followup policy),
// the host result must be MATRIX-shaped (i.e. Request.Crosstab is
// non-nil), and Scope must be CELL.
func validateOverlayShareOfCol(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — share-of-column needs a crosstab
	// column margin to divide by.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayShareOfTotal enforces the per-kind contract for
// OVERLAY_SHARE_OF_TOTAL. The kind is dual-shape — the dispatch chooses
// between the MATRIX-host validator and the SERIES-host validator based
// on the request's host shape:
//
//   - Request.Crosstab non-nil ⇒ MATRIX dispatch: Ref must populate
//     Margin (the grand-total reference family), Margin.Axis must be a
//     known MarginAxis, host must be MATRIX-shaped, Scope must be CELL.
//   - Request.Crosstab nil + Request.Groups non-empty ⇒ SERIES
//     dispatch: Ref must be empty (implicit-grand-total — sibling rule
//     to OVERLAY_INDEX_VS_TOTAL), Scope must be GROUP.
//   - Neither host shape present ⇒ MATRIX-style rejection so the
//     existing failure mode is preserved (callers without a host get
//     PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE the same way they did
//     against the original MATRIX-only contract).
//
// Host-shape disambiguation by inspecting `req.Crosstab` / `req.Groups`
// matches `validateOverlayIndexVsTotal`'s policy and the runtime
// dispatch in `internal/processing/overlay_series.go` (ApplyOverlaysSeries) vs
// `internal/processing/overlay.go` (ApplyOverlays) — each runtime path consumes
// its own dispatch table, so the predict-time validator has to make the
// same routing decision.
func validateOverlayShareOfTotal(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// SERIES-host dispatch path — Request.Crosstab nil AND
	// Request.Groups non-empty. The SERIES dispatch is implicit-grand-
	// total so the Ref union MUST be empty (mirrors the INDEX_VS_TOTAL
	// rule). Falls through to MATRIX-style validation otherwise.
	if req != nil && req.Crosstab == nil && len(req.Groups) > 0 {
		validateOverlayShareOfTotalSeries(env, req, spec, index)
		return
	}
	validateOverlayShareOfTotalMatrix(env, req, spec, index)
}

// validateOverlayShareOfTotalMatrix enforces the MATRIX-host dispatch:
// Ref must populate Margin (the grand-total reference family),
// Margin.Axis must be a known MarginAxis (the runtime handler is grand-
// axis-locked, but the validator accepts any known axis at predict time
// so a misconfigured caller fails closed with a single shape-mismatch
// code rather than a stricter "must be grand" code — matches the
// SHARE_OF_ROW / SHARE_OF_COL followup policy), the host result must be
// MATRIX-shaped (i.e. Request.Crosstab is non-nil), and Scope must be
// CELL.
func validateOverlayShareOfTotalMatrix(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — share-of-total needs a crosstab
	// grand-total slot to divide by.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayShareOfTotalSeries enforces the SERIES-host dispatch:
// the Ref union must be EMPTY (implicit-grand-total — the host
// series' own grand total is the denominator), the host result must be
// SERIES-shaped (Request.Crosstab nil AND Request.Groups non-empty —
// the caller-side dispatcher already verified the host shape before
// routing here, so we re-assert in case a future caller calls into this
// branch directly), and Scope must be GROUP.
//
// Sibling rule to validateOverlayIndexVsTotal — the two SERIES SHARE /
// INDEX kinds share the implicit-grand-total contract verbatim.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayShareOfTotalSeries(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Belt-and-suspenders host check — the caller already asserted this,
	// but re-check so a direct call into this branch from a future
	// validator-aware caller still gets the right rejection shape.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, true, index)
		return
	}
}

// validateOverlayZScoreVsMargin enforces the per-kind contract for
// OVERLAY_ZSCORE_VS_MARGIN: Ref must populate Margin (the axis-margin
// reference family), Margin.Axis must be a known MarginAxis (any of
// row / column / grand — unlike the SHARE_OF_* triad the runtime
// handler dispatches all three axes), the host result must be MATRIX-
// shaped (i.e. Request.Crosstab is non-nil), and Scope must be CELL.
func validateOverlayZScoreVsMargin(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared crosstab rule the runtime MATRIX dispatcher
	// raises too (overlayhost.CrosstabSpecRefusal).
	if cerr := overlayhost.CrosstabSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be MATRIX-shaped — z-score-vs-margin needs a crosstab
	// margin slot to subtract from each cell AND a crosstab cell grid
	// to drive the per-slice Welford recurrence.
	if req.Crosstab == nil {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayZScoreVsTotal enforces the per-kind contract for
// OVERLAY_ZSCORE_VS_TOTAL: the Ref union must be EMPTY (implicit-
// grand-total — the host series' own grand-total mean + SD are the
// centerpoint), the host result must be SERIES-shaped (i.e.
// Request.Groups is non-empty and Request.Crosstab is nil), and Scope
// must be GROUP. Sibling validator to validateOverlayIndexVsTotal and
// validateOverlayShareOfTotalSeries — the streamable SERIES-host
// grouped-Process subset shares the implicit-grand-total contract
// verbatim.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any Ref family
//     pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host, not a MATRIX one) OR
//     when Request.Groups is empty (no host series to standardise
//     against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlayZScoreVsTotal(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped. A SERIES host is a grouped Process
	// result — Request.Groups is non-empty AND Request.Crosstab is nil
	// (an active crosstab routes the request down the MATRIX-host path).
	// A request with no groupers has no series to standardise against.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// validateOverlayDeltaVsSibling enforces the per-kind contract for
// OVERLAY_DELTA_VS_SIBLING: the Ref family must be Sibling (not
// Margin / BaselineIndex / Population / Stage / Slot), Sibling.Field
// and Sibling.Value must both be non-empty, the host result must be
// SERIES-shaped (i.e. Request.Groups is non-empty and Request.Crosstab
// is nil), and Scope must be GROUP. Sibling validator to
// `validateOverlayIndexVsSibling`.
//
// Errors emitted (in order, first hit short-circuits the spec):
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when any non-Sibling
//     family pointer is populated.
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Ref.Sibling is
//     nil OR when its Field / Value are empty (the sibling pair is the
//     denominator anchor and both halves are required).
//   - PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE when Request.Crosstab
//     is non-nil (the kind targets a SERIES host) OR when
//     Request.Groups is empty (no host series to compute against).
//   - PULSE_OVERLAY_SCOPE_UNSUPPORTED when Scope is anything other
//     than GROUP.
func validateOverlayDeltaVsSibling(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	validateOverlaySiblingKind(env, req, spec, index)
}

// validateOverlayIndexVsSibling enforces the per-kind contract for
// OVERLAY_INDEX_VS_SIBLING. Twin validator to
// `validateOverlayDeltaVsSibling` — the two sibling-reference kinds
// share an identical predict-time contract; only the runtime math
// differs (subtraction vs ratio scaling). Routes through the same
// `validateOverlaySiblingKind` helper so a future schema/runtime
// rule that fires on one kind automatically extends to the other.
func validateOverlayIndexVsSibling(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	validateOverlaySiblingKind(env, req, spec, index)
}

// validateOverlaySiblingKind is the shared predict-time validator for
// the sibling-reference SERIES-host family (DELTA_VS_SIBLING +
// INDEX_VS_SIBLING). The two kinds carry identical structural
// contracts — only the runtime math differs (subtraction vs ratio
// scaling) — so the validator collapses both into a single helper.
//
// Contract enforced (in order, first hit short-circuits):
//
//   - Ref family must be Sibling. Any other ref-family pointer
//     (Margin / BaselineIndex / Population / Stage / Slot) is a
//     shape mismatch (PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE).
//   - Ref.Sibling MUST be non-nil; Sibling.Field and Sibling.Value
//     MUST both be non-empty strings. The sibling reference is the
//     denominator anchor and both halves are required.
//   - Host MUST be SERIES-shaped (Request.Crosstab nil AND
//     Request.Groups non-empty). A request with no groupers has no
//     series to compute against; an active crosstab routes down the
//     MATRIX-host path which the sibling family does not target in
//     v1.
//   - Scope MUST be in the supported set (GROUP today).
//
// The Ref and Scope rules are overlayhost.SeriesSpecRefusal's, judged
// first (every Ref arm, not only the ones listed above; then the scope)
// and raised identically by every runtime SERIES arm; the SERIES-host
// check follows them.
func validateOverlaySiblingKind(env *descriptor.Envelope, req *types.Request, spec *types.OverlaySpec, index int) {
	// Ref / Scope: the shared SERIES rule every runtime arm raises too
	// (overlayhost.SeriesSpecRefusal).
	if cerr := overlayhost.SeriesSpecRefusal(spec, spec.Kind, index); cerr != nil {
		addCodedError(env, cerr)
		return
	}

	// Host must be SERIES-shaped — grouped Process result.
	if req == nil || req.Crosstab != nil || len(req.Groups) == 0 {
		addHostRequired(env, spec, spec.Kind, req != nil && len(req.Groups) > 0, index)
		return
	}
}

// snapshotGroupFanOut adapts the extensions snapshot to the types-side
// extension-grouper resolver the pairwise slab-partition gate takes.
// A nil snapshot yields a nil resolver, which the gate reads as "no
// extension groupers registered" — the same thing the runtime arm does
// for a nil ExtensionRegistry, so a host with no extensions has the two
// arms agreeing by construction.
func snapshotGroupFanOut(snap *ExtensionsSnapshot) types.ExtensionGroupFanOutFunc {
	if snap == nil {
		return nil
	}
	return func(t types.GroupType) (bool, bool) {
		return snap.GrouperFanOut(string(t))
	}
}

// isBuiltinAggregationType reports whether t is a built-in aggregator
// (types.AllAggregationTypes). Extension-registered types are NOT
// built-in, so a shape check keyed on it defers them to runtime.
func isBuiltinAggregationType(t types.AggregationType) bool {
	for _, b := range types.AllAggregationTypes() {
		if b == t {
			return true
		}
	}
	return false
}
