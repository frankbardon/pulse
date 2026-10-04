package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/crosstabfuse"
	"github.com/frankbardon/pulse/types"
)

// CanFuseCrosstab reports whether a crosstab request is eligible for
// the fused in-decode execution path that materializes per-cell state
// while iterating records — bypassing the full buffered Row materializa-
// tion the standard processCrosstab path uses today. Pure predicate: no
// side effects, no execution.
//
// Naming mirrors CanMergeRequest / CanStreamRequest / CanChainRequest.
// The returned reason string is short and operator-specific so callers
// (dispatch in internal/service/crosstab.go, predict surfaces in a follow-up)
// can surface a human-readable explanation without re-deriving the
// rule.
//
// Eligibility = ALL of:
//
//   - req.Crosstab != nil — nothing to fuse otherwise.
//
//   - The cell aggregator is mergeable per ext.IsMergeable — the
//     built-in AggregationType.Mergeable(), or an extension's DECLARED
//     pulse.AggregatorRegistration.Mergeable. The fused path folds
//     per-cell online state row-by-row; non-mergeable aggregators
//     (median/percentile/zscore/skewness/kurtosis) need a finalize-time
//     sorted view that the fused walk cannot provide.
//
//   - The cell aggregator's margin class (ext.AggregatorMarginReducibility:
//     the built-in table, or an extension's DECLARED
//     pulse.AggregatorRegistration.MarginReducibility, where undeclared
//     reads as recompute) is MarginSummable,
//     MarginMeanReducible, or MarginIndependent. MarginRecompute
//     aggregators force a re-scan of raw rows for margin derivation,
//     which defeats the fused path by construction. The three non-
//     recompute classes are exactly the aggregators whose margins the
//     fused walk can satisfy in one pass — the first two because the
//     margin follows from the cells, MarginIndependent because the
//     operator keeps its own row / column / grand accumulators, which
//     FusedCrosstabState already feeds record-by-record.
//
//   - Every grouper on req.Crosstab.Rows ∪ req.Crosstab.Columns keys
//     per record: StreamableGrouper (a per-record KeyFor, one bucket
//     per record) or MultiKeyStreamingGrouper (a per-record KeysForRow,
//     N buckets per record — the GROUP_SET_PER_ELEMENT fan-out). A
//     built-in answers from the static crosstabfuse.BuiltinGrouperKeyable
//     table (wider than types.GroupType.Streamable(): GROUP_DATE is
//     non-streamable at the Process layer but keys per record;
//     GROUP_QUANTILE keys nothing per record), an extension from its
//     probe-validated DECLARED Streamable or FansOut flag. The runtime
//     arm alone additionally declines a grouper whose factory fails to
//     construct against the schema — the buffered path then reports the
//     coded error.
//
//   - No req.Features — every FEAT_* operator forces a buffered
//     pre-filter pass that the fused path skips.
//
//   - No req.Joins — the fused walk decodes the left cohort directly
//     and has no join leg; a joined crosstab runs buffered over the
//     joined row stream (internal/service processCrosstabWithJoin).
//     Checked before the axis probe because the schema handed in is
//     the left cohort's, against which a right-side axis field does
//     not resolve.
//
//   - No two-pass attribute — a built-in one (requiresTwoPass) or an
//     extension attribute registered with the two_pass mode
//     (ExtensionRegistry.TwoPassAttributes). The fused walk values
//     attributes row-locally and never runs the population PrePass.
//
//   - No req.Attributes of type ATTR_FORMULA with a non-empty
//     Expression. Expression-runtime field extraction is conservative;
//     #59 bail rules treat it as a forced widen, which the fused path
//     can't honour while keeping per-field decode bounds tight.
//
//   - No req.Filterers of type FILTER_EXPRESSION (same reason).
//
//   - No req.Tests and no req.PostTests. Tier-1 row tests and tier-2
//     post-tests fold over the buffered row set after aggregation;
//     the fused path doesn't buffer.
//
//   - No extension-bound operator anywhere in the request without a
//     registered FieldInputs hook. The fused path's projection bound
//     is built from NeededFields; an opaque extension operator would
//     widen the projection to "every field", which collapses the fused
//     path's decode-cost advantage and is treated as ineligible here.
//
//   - No mergeable-but-decimal BUILT-IN aggregation target on the cell.
//     Decimal-typed fields aggregate via AggregateDecimalField (the
//     wide decimal path); Pulse forces buffered for those today and the
//     fused gate mirrors that constraint. An extension aggregator never
//     takes that path — it reads the decimal through DecimalValue on
//     both arms — so a decimal extension cell fuses on its declarations.
//
//   - Every entry of req.Crosstab.MarginAggregations is mergeable
//     (ext.IsMergeable, so an extension auxiliary answers with its
//     declaration) and, for a built-in, does not target a decimal-typed
//     field — the same two checks the
//     cell gets, for the same two reasons: an auxiliary rides the same
//     per-record UpdateRow walk (so it must be online, which Mergeable
//     implies) and the wide decimal path is buffered-only. An auxiliary
//     that fails either declines fusion rather than being dropped,
//     because dropping it returns a margin with the requested figure
//     silently missing.
//
//     Their MarginReducibility is deliberately NOT consulted, and that
//     is not an oversight. The classification answers "can this
//     aggregator's margin be derived from its CELLS", which is a
//     question an auxiliary does not have: it has no cells, and both
//     paths give it its own row / column / grand accumulator fed record
//     by record. Every auxiliary is therefore MarginIndependent in role
//     whatever its declared class, and requiring a class here would
//     decline fusion for a request the fused walk computes exactly.
//
// req.Overlays is explicitly NOT an exclusion. Overlays decorate a
// finalised response and consume no records, so RunCrosstabFused folds
// them at its exit through the same applyOverlaysToResponse hook the
// buffered exit uses. An overlay-carrying crosstab is fusable whenever
// the rest of the request is.
//
// Returns (true, "") for an eligible request. Returns (false, reason)
// for an ineligible one — the reason is intentionally short ("non-
// mergeable cell aggregator (AGG_MEDIAN)", "stat tests force buffered",
// "non-streamable grouper on column axis (GROUP_QUANTILE)",
// "ATTR_FORMULA bail", etc.).
//
// The gate is a pure predicate. It does NOT modify req or schema, and
// it does NOT touch the orchestrator (RunCrosstab / processCrosstab).
// internal/service/crosstab.go wires the dispatch around the result of this
// call.
// The rule itself is crosstabfuse.Decide — the one body shared with
// the no-execute predict layer, which calls it over the read-only
// ExtensionsSnapshot. This wrapper adapts the registry to
// crosstabfuse.Facts and returns the first decline reason.
func CanFuseCrosstab(req *types.Request, schema *encoding.Schema, ext *ExtensionRegistry) (bool, string) {
	ok, reasons := crosstabfuse.Decide(req, schema, ext.fuseFacts())
	if ok {
		return true, ""
	}
	return false, reasons[0]
}

// CrosstabFuseReasons is CanFuseCrosstab with every decline reason, in
// rule order. Empty when the request fuses.
func CrosstabFuseReasons(req *types.Request, schema *encoding.Schema, ext *ExtensionRegistry) []string {
	_, reasons := crosstabfuse.Decide(req, schema, ext.fuseFacts())
	return reasons
}

// fuseFacts adapts the registry to crosstabfuse.Facts. Nil-safe: a nil
// registry answers with the built-in tables alone. Every fact consults
// the instance feature set first, so a hidden name answers exactly as a
// never-registered one.
func (r *ExtensionRegistry) fuseFacts() crosstabfuse.Facts {
	return registryFuseFacts{r}
}

type registryFuseFacts struct{ r *ExtensionRegistry }

func (f registryFuseFacts) AggregatorMergeable(t types.AggregationType) bool {
	return f.r.IsMergeable("aggregator", string(t))
}

func (f registryFuseFacts) AggregatorMarginClass(t types.AggregationType) types.MarginReducibility {
	return f.r.AggregatorMarginReducibility(t)
}

func (f registryFuseFacts) AttributeTwoPass(t types.AttributeType) bool {
	return f.r.attributeRequiresTwoPass(t)
}

// GrouperKeyable answers from the static table (built-in) or the
// declaration (extension), then — runtime only — confirms the factory
// constructs against schema. A construction failure declines fusion so
// the buffered path surfaces the factory's coded error, exactly as the
// probe-based gate did. Kept in lockstep with buildStreamableAxis
// (crosstab_fused.go): anything admitted here must construct there.
func (f registryFuseFacts) GrouperKeyable(g *types.Group, schema *encoding.Schema) bool {
	factory, ok := f.r.LookupGrouper(g.Type)
	if !ok {
		return false
	}
	if f.IsExtension("grouper", string(g.Type)) {
		if !f.r.Streamable[StreamabilityKey("grouper", string(g.Type))] && !f.r.FansOut[g.Type] {
			return false
		}
	} else if keyable, _ := crosstabfuse.BuiltinGrouperKeyable(g.Type); !keyable {
		return false
	}
	_, err := factory(g, schema)
	return err == nil
}

// IsExtension is overlay-map membership. Hidden extension operators
// never reach the registry (pulse.New drops them before building it).
func (f registryFuseFacts) IsExtension(category, name string) bool {
	if f.r == nil {
		return false
	}
	var ok bool
	switch category {
	case "aggregator":
		_, ok = f.r.Aggregators[types.AggregationType(name)]
	case "attribute":
		_, ok = f.r.Attributes[types.AttributeType(name)]
	case "filterer":
		_, ok = f.r.Filterers[types.FiltererType(name)]
	case "grouper":
		_, ok = f.r.Groupers[types.GroupType(name)]
	}
	return ok
}

// Hidden is the registry's instance feature-set check (nil-safe).
func (f registryFuseFacts) Hidden(name string) bool { return f.r.isHidden(name) }

func (f registryFuseFacts) HasFieldInputs(category, name string) bool {
	if f.r == nil {
		return false
	}
	_, ok := f.r.FieldInputs[StreamabilityKey(category, name)]
	return ok
}
