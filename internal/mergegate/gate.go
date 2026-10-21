// Package mergegate is the one pure mergeability rule shared by the
// engine and the no-execute validators. processing.CanMergeRequest*
// (the parallel shard / decode reducers' gate) and
// processing.ChainRefusal (ProcessChain's per-stage gate) call it with
// an adapter over the runtime ExtensionRegistry; the chain validator in
// internal/descriptor calls it with an adapter over the read-only
// ExtensionsSnapshot. Both therefore refuse the same requests with the
// same code, message and details.
//
// The package imports only types, encoding and errors so that both
// sides can reach it: internal/descriptor may not import
// internal/processing (TestPredictNoExecutionImports), and the engine
// does not import the descriptor layer.
package mergegate

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// Extensions answers the embedder-registered half of the rule. Each
// method reports ok=false for a name that is not a registered
// extension of that category, in which case the built-in per-type
// facts in package types decide. An implementation may also answer
// ok=true, false for a built-in name its instance hides, which refuses
// it exactly as an unregistered name is refused. An implementation must be safe to
// call on its zero / nil value (no extensions).
type Extensions interface {
	// Aggregator reports an extension aggregator's DECLARED Mergeable
	// flag.
	Aggregator(name string) (mergeable, ok bool)
	// Grouper reports an extension grouper's DECLARED Mergeable flag.
	Grouper(name string) (mergeable, ok bool)
	// Filterer reports an extension filterer's streamability.
	Filterer(name string) (streamable, ok bool)
	// Attribute reports whether an extension attribute merges as a
	// row-local operator: declared row_local (streamable, not
	// two-pass) and — when it emits Components.Attributes figures —
	// declared Mergeable (its per-run tally folds through
	// extend.MergeableAttribute.MergeTally).
	Attribute(name string) (rowLocal, ok bool)
	// Matrix reports whether an extension MAT_* operator merges: its
	// declared co-moment tier (Streamable) merges through the blocked
	// merge; the buffered tier does not.
	Matrix(name string) (mergeable, ok bool)
}

// None is the Extensions value for a built-in-only gate.
type None struct{}

func (None) Aggregator(string) (bool, bool) { return false, false }
func (None) Grouper(string) (bool, bool)    { return false, false }
func (None) Filterer(string) (bool, bool)   { return false, false }
func (None) Attribute(string) (bool, bool)  { return false, false }
func (None) Matrix(string) (bool, bool)     { return false, false }

// MergeRefusal returns "" when req's online state is mergeable across
// input partitions, else the reason it is not. A request merges iff it
// has at least one aggregator or matrix; no windows, features,
// regressions, tests or post-tests; matrices only of a mergeable type
// (an extension MAT_* operator's declared co-moment tier, else
// types.MatrixSpec.Mergeable — they merge through the blocked merge
// tree, bit-identical to serial; a grouped request's per-bucket
// matrices merge bucket by bucket the same way); only row-local
// attributes (ATTR_FORMULA without a total(expr) call — or with one
// that ParallelTotals admits —, ATTR_DATE_PART, ATTR_REG_PREDICT,
// ATTR_SCALE_SCORE, a non-centroid ATTR_PROFILE_SIMILARITY — both fold
// their per-run tallies across partitions —, an ATTR_MAHALANOBIS /
// ATTR_PC_SCORE / ATTR_FACTOR_SCORE / ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL bound to a prebuilt fit by params.fit —
// vectors.FitBound —, row_local extensions, a component-emitting one
// only when declared Mergeable); only mergeable groupers
// (built-in Mergeable() or a declared extension); only streamable
// filterers; and only mergeable aggregators, none of them a built-in
// one over a decimal128 field (the built-in decimal fold is
// buffered-only — an extension aggregator reads decimals itself).
// schema may be nil (the decimal check is then skipped); a nil ext is
// None.
//
// A total-bearing ATTR_FORMULA merges through the reducers' two-phase
// drive (U51 E3-S1, #399): a parallel pre-pass folds each total()
// argument into a binned {sum, count} per partition, the partitions
// merge exactly and the published totals bind into every partition's
// formula before the ordinary reduce. Only the first cut is admitted —
// ParallelTotals: an ungrouped request whose total() formulas sit in
// one pre-pass layer. Any other total() formula is refused with the
// row-local reason, as before; so is every total() formula on a
// ProcessChain stage (ChainRefusal), which has no two-phase runtime.
func MergeRefusal(req *types.Request, schema *encoding.Schema, ext Extensions) string {
	return mergeRefusal(req, schema, ext, true)
}

// mergeRefusal is MergeRefusal; admitTotals=false keeps every
// total-bearing formula off (the chain stage rule).
func mergeRefusal(req *types.Request, schema *encoding.Schema, ext Extensions, admitTotals bool) string {
	if ext == nil {
		ext = None{}
	}
	if req == nil {
		return "the request is nil"
	}
	// A tests-only or regression-only request streams (the engine's
	// canStream and predict's computeStreamable admit it) but does not
	// merge: it falls through to the exclusion below, so the refusal
	// names its tests / regressions rather than a missing aggregator.
	if len(req.Aggregations) == 0 && len(req.Matrices) == 0 && len(req.Tests) == 0 && len(req.Regressions) == 0 {
		return "it has no aggregator"
	}
	if len(req.Windows) > 0 || len(req.Features) > 0 ||
		len(req.Regressions) > 0 || len(req.Tests) > 0 ||
		len(req.PostTests) > 0 {
		return "windows, features, tests, post-tests and regressions are excluded"
	}
	if len(req.Matrices) > 0 {
		for _, m := range req.Matrices {
			mergeable, ok := ext.Matrix(string(m.Type))
			if !ok {
				mergeable = m.Mergeable() // spec-level: the type folded with its params
			}
			if !mergeable {
				return fmt.Sprintf("matrix operator %s is not mergeable", m.Type)
			}
		}
	}
	var totals []bool // ParallelTotals, computed on the first total() formula
	for i, attr := range req.Attributes {
		if attr == nil {
			return "an attribute slot is nil"
		}
		// The extension answer is consulted FIRST, like every other
		// category below: an adapter may claim a built-in name (an
		// instance feature set reports a hidden one as known-and-not
		// row-local), and it must then refuse exactly as an
		// unregistered name does rather than pass on the built-in list.
		rowLocal, ok := ext.Attribute(string(attr.Type))
		if !ok {
			switch attr.Type {
			case types.ATTR_FORMULA:
				// Row-local unless it calls total(expr): a cohort total
				// folded per partition would be partition-local. A
				// total() formula the two-phase drive covers merges
				// (ParallelTotals) — never on a chain stage.
				rowLocal = !totalcall.Contains(attr.Expression)
				if !rowLocal && admitTotals {
					if totals == nil {
						totals = ParallelTotals(req, ext)
					}
					rowLocal = totals[i]
				}
			case types.ATTR_DATE_PART,
				// Pure per-row reads of the row's own stored values,
				// no per-run state (#391).
				types.ATTR_CODE_IN, types.ATTR_SET_HAS, types.ATTR_SET_POPCOUNT,
				// Bound before the row pass: a pure per-row score.
				types.ATTR_REG_PREDICT,
				// Row-local with a per-run tally (its
				// Components.Attributes counters and its Response
				// warning — out-of-range members) the reducers fold
				// across partitions (processing.MergeableAttributeTally,
				// U51 E3-S2, #456).
				types.ATTR_SCALE_SCORE:
				rowLocal = true
			case types.ATTR_PROFILE_SIMILARITY:
				// The reference form tallies per run (missing
				// references, domain misses) and folds like
				// ATTR_SCALE_SCORE; a centroid reference is a
				// cohort-wide pre-pass (two-pass), which a partition
				// cannot fold.
				rowLocal = !vectors.ComparisonCentroid(attr.Type, attr)
			case types.ATTR_MAHALANOBIS, types.ATTR_PC_SCORE, types.ATTR_FACTOR_SCORE,
				types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL:
				// Row-local only when bound to a prebuilt fit
				// (params.fit): a fit_on fold — or an ATTR_REG_FITTED /
				// RESIDUAL OLS refit — over one partition's rows would
				// be partition-local. ATTR_VEC_DISTANCE stays
				// off the list either way, its row-local metrics
				// included (their tally folds like
				// ATTR_PROFILE_SIMILARITY's, but the gate does not
				// admit them yet).
				rowLocal = vectors.FitBound(attr.Type, attr)
			}
		}
		if rowLocal {
			continue
		}
		return fmt.Sprintf("attribute %s is not row-local (two-pass and buffered attributes are excluded)", attr.Type)
	}
	for _, g := range req.Groups {
		if g == nil {
			return "a group slot is nil"
		}
		mergeable, ok := ext.Grouper(string(g.Type))
		if !ok {
			mergeable = g.Type.Mergeable()
		}
		if !mergeable {
			return fmt.Sprintf("grouper %s is not mergeable", g.Type)
		}
	}
	for _, f := range req.Filterers {
		if f == nil {
			return "a filterer slot is nil"
		}
		streamable, ok := ext.Filterer(string(f.Type))
		if !ok {
			streamable = f.Type.Streamable()
		}
		if !streamable {
			return fmt.Sprintf("filterer %s is not streamable", f.Type)
		}
	}
	for _, agg := range req.Aggregations {
		if agg == nil {
			return "an aggregation slot is nil"
		}
		mergeable, extension := ext.Aggregator(string(agg.Type))
		if !extension {
			mergeable = agg.Type.Mergeable()
		}
		if !mergeable {
			return fmt.Sprintf("aggregator %s is not mergeable", agg.Type)
		}
		if extension || schema == nil {
			continue
		}
		if f := schema.Field(agg.Field); f != nil && f.Type.IsDecimal() {
			return fmt.Sprintf("aggregator %s targets decimal128 field %q (the built-in decimal fold is buffered-only)", agg.Type, agg.Field)
		}
	}
	return ""
}

// ChainRefusal is the v1 ProcessChain stage gate: nil when req passes
// MergeRefusal — with every total-bearing ATTR_FORMULA refused as
// not row-local, since a chain stage has no two-phase total() runtime
// (the reducers' ParallelTotals admission stops at the chain) —, else a PULSE_CHAIN_NOT_MERGEABLE "chain stage is not
// mergeable: <reason>" with details {stage_index, stage_name}. Every
// mergeable scalar built-in aggregator emits one float64 per output row the
// next stage's f64 column reads — AGG_MODE's modal value (on a
// categorical field, the dictionary index, not the label),
// AGG_MODE_COUNT's modal count and AGG_FREQUENCY's value count
// included — so mergeability is the whole rule here; the mergeable
// non-scalar folds (AGG_SET_UNION / _INTERSECTION / _FREQUENCY) are
// StageArrayValuedRefusal's.
//
// A stage's `matrices` slot is not part of the rule: a matrix result
// lands in Response.Matrices, never in the rows the next stage reads,
// so it is judged on the stage with the slot stripped — a matrix-only
// stage (which MergeRefusal admits) still has no aggregator, and so no
// rows to hand on, and is refused. Stage 0 runs as
// a plain Process over the cohort and computes it; a later stage's is
// refused first by StageMatrixRefusal.
//
// A stage carrying Request.Crosstab is refused first, whatever else it
// carries (CrosstabStageReason): a crosstab answers a MatrixPayload, never
// the rows the next stage reads, and beside aggregations it used to be
// dropped silently on a later stage (stage 0 hit Process's
// PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS, which predict-chain never
// raised). The chain runtime and the chain validator both call this
// rule, so both refuse it.
func ChainRefusal(req *types.Request, schema *encoding.Schema, ext Extensions, stageIndex int, stageName string) error {
	if req != nil && req.Crosstab != nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_NOT_MERGEABLE,
			"chain stage is not mergeable: "+CrosstabStageReason,
			map[string]any{"stage_index": stageIndex, "stage_name": stageName})
	}
	if req != nil && len(req.Matrices) > 0 {
		stripped := *req
		stripped.Matrices = nil
		req = &stripped
	}
	reason := mergeRefusal(req, schema, ext, false)
	if reason == "" {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_NOT_MERGEABLE,
		"chain stage is not mergeable: "+reason,
		map[string]any{"stage_index": stageIndex, "stage_name": stageName})
}

// ParallelTotals reports, per req.Attributes slot, whether it is a
// total-bearing ATTR_FORMULA the parallel reducers' two-phase drive
// runs (U51 E3-S1, #399): the request is ungrouped (no Groups, no
// Crosstab) and the formula sits in pre-pass layer 0 — none of its
// inputs is, directly or through row-local attributes, the label of an
// earlier total() formula. That is processing's two-pass planner rule
// (twoPassPlan) over the same inputs (vectors.AttributeInputNames,
// labels by vectors.DefaultAttributeLabel), except that a slot whose
// inputs cannot be proven — an ATTR_REG_PREDICT, a fit-referenced slot,
// an extension attribute (its FieldInputs are not visible here) —
// depends on every earlier slot, so the gate never admits a formula the
// planner would put in a later layer. Every other slot reports false.
// nil ext is None.
func ParallelTotals(req *types.Request, ext Extensions) []bool {
	if ext == nil {
		ext = None{}
	}
	if req == nil {
		return nil
	}
	n := len(req.Attributes)
	out := make([]bool, n)
	if len(req.Groups) > 0 || req.Crosstab != nil {
		return out
	}
	labels := make([]string, n)
	for i, a := range req.Attributes {
		if a == nil {
			return out
		}
		labels[i] = a.Label
		if labels[i] == "" {
			labels[i] = vectors.DefaultAttributeLabel(a)
		}
	}
	// tainted[j]: slot j's output depends on a published total (it is a
	// total() formula, or reads one through earlier slots).
	tainted := make([]bool, n)
	for i, a := range req.Attributes {
		_, extension := ext.Attribute(string(a.Type))
		isTotal := !extension && a.Type == types.ATTR_FORMULA && totalcall.Contains(a.Expression)
		var inputs map[string]struct{}
		known := false
		if !extension {
			inputs, known = vectors.AttributeInputNames(a)
		}
		reads := false
		for j := 0; j < i && !reads; j++ {
			if !tainted[j] {
				continue
			}
			if !known {
				reads = true
				continue
			}
			_, reads = inputs[labels[j]]
		}
		tainted[i] = isTotal || reads
		out[i] = isTotal && !reads
	}
	return out
}

// CrosstabStageReason is ChainRefusal's reason for a stage carrying
// Request.Crosstab.
const CrosstabStageReason = "a crosstab stage is excluded (a crosstab answers a matrix, not the rows the next stage reads)"

// StageJoinRefusal is the ProcessChain rule that only stage 0 may
// join: nil for stage 0 or a stage without Joins, else a
// PULSE_CHAIN_STAGE_JOIN with details {count, stage, stage_name}. A
// later stage reads the previous stage's output rows, not a cohort, so
// there is no left side to join — the runtime used to drop the slot
// silently.
func StageJoinRefusal(req *types.Request, stageIndex int, stageName string) error {
	if stageIndex == 0 || req == nil || len(req.Joins) == 0 {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_CHAIN_STAGE_JOIN,
		"only chain stage 0 may carry Joins; a later stage reads the previous stage's output",
		map[string]any{"count": len(req.Joins), "stage": stageIndex, "stage_name": stageName})
}

// MatrixRefusal is the matrix-host rule shared by runtime Process
// (internal/service, beside the join-count rule and before the crosstab
// and join dispatch) and every no-execute validator (Predict,
// ValidateJoin, the Compose slots, a joined chain stage 0), so both
// refuse with the same code, message and details. Matrix operators fold
// cohort rows the decode stamps with their merge position, and they
// have no crosstab arm, so a request carrying `matrices` is refused
// when it also carries:
//
//   - Joins: PULSE_MATRIX_UNSUPPORTED_SOURCE {source: "join", matrices, joins};
//   - a Crosstab: PULSE_MATRIX_HOST_CONFLICT {host: "crosstab", matrices}.
//
// The join refusal wins when both apply. nil without a matrices slot —
// every other request (every crosstab request included) is untouched.
func MatrixRefusal(req *types.Request) error {
	if req == nil || len(req.Matrices) == 0 {
		return nil
	}
	if len(req.Joins) > 0 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_UNSUPPORTED_SOURCE,
			"matrices are not supported on a joined request; run them on a Request over the cohort without joins",
			map[string]any{"source": "join", "matrices": len(req.Matrices), "joins": len(req.Joins)})
	}
	if req.Crosstab != nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_HOST_CONFLICT,
			"matrices are not supported on a crosstab request; run them on a separate Request",
			map[string]any{"host": "crosstab", "matrices": len(req.Matrices)})
	}
	return nil
}

// StageMatrixRefusal is the ProcessChain rule that only stage 0 may
// carry matrices: nil for stage 0 or a stage without them, else a
// PULSE_MATRIX_UNSUPPORTED_SOURCE {source: "chain_stage", matrices,
// stage, stage_name}. A later stage reads the previous stage's output
// rows, not the cohort. The runtime and the chain validator both check
// it right after StageJoinRefusal.
func StageMatrixRefusal(req *types.Request, stageIndex int, stageName string) error {
	if stageIndex == 0 || req == nil || len(req.Matrices) == 0 {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_MATRIX_UNSUPPORTED_SOURCE,
		"only chain stage 0 may carry matrices; a later stage reads the previous stage's output",
		map[string]any{"source": "chain_stage", "matrices": len(req.Matrices), "stage": stageIndex, "stage_name": stageName})
}

// StageArrayValuedRefusal is the ProcessChain rule that an
// array-valued aggregation (AGG_VEC_MEAN / AGG_VEC_SUM / AGG_VEC_SD, or an extension
// declared ArrayValued) may not feed a later stage: the next
// stage reads each output column as one f64, so the array would read
// as null and every figure over it — a total(), a ratio, a plain sum —
// would be silently wrong. nil for the last stage (its rows are the
// chain's output) or a stage with no such slot, else PROCESSING_CONFIG
// {stage_index, stage_name, aggregation, label, reason:
// "array_valued"}. An EXPANDING one (params.expand,
// vectors.AggregationExpands) writes no array — one f64 column per
// member, "<label>[<element label>]" — so it feeds the next stage, which
// reads each element column as an ordinary nullable f64 (the
// synthesised schema, vectors.ChainStageSchema, lists them); its bare
// label is no column there. The runtime and the chain validator both check it
// right after ChainRefusal. kinds is the instance's view (a declared
// ArrayValued extension aggregator is array-valued too); the zero value
// is the built-in view.
//
// A non-scalar aggregation (AggregationKinds.IsNonScalar — a mergeable
// set fold's label list or count map, an extension declared NonScalar)
// is refused the same way, reason "non_scalar": its column read as
// null (a sum over it 0) in the next stage, silently (U46 E4-S11). A
// non-mergeable one never gets here — ChainRefusal refuses it first.
func StageArrayValuedRefusal(req *types.Request, stageIndex, stageCount int, stageName string, kinds vectors.AggregationKinds) error {
	if req == nil || stageIndex >= stageCount-1 {
		return nil
	}
	for _, agg := range req.Aggregations {
		if agg == nil || !kinds.IsNonScalar(agg.Type) || vectors.AggregationExpands(agg, kinds) {
			continue
		}
		label := types.AggregationLabelOf(agg)
		what, reason := "array-valued", "array_valued"
		if !kinds.IsArrayValued(agg.Type) {
			what, reason = "non-scalar (a label list, a count map or a struct, not one number)", "non_scalar"
		}
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("chain stage %d aggregation %s (%s) is %s and cannot feed a later stage, which reads one number per column; make it the last stage", stageIndex, label, agg.Type, what),
			map[string]any{"stage_index": stageIndex, "stage_name": stageName, "aggregation": string(agg.Type), "label": label, "reason": reason})
	}
	return nil
}
