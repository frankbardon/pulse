package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// SERIES-host overlay fold engine — runtime side of the grouped Process
// overlay catalog.
//
// E3-S1 scope: structural pre-req for the five grouped-Process overlay
// kinds that land in E3-S2 .. E3-S5. The buffered crosstab overlay path
// (overlay.go) folds against a MATRIX host (CrosstabHostView); this file
// is its parallel for a SERIES host — a grouped Process result where the
// caller passed `Request.Groupers` and the response carries one record
// per group key. The grouped Process orchestrator (processing/processor.go)
// emits a parallel-slice host view ordered by the caller's group-key
// list; each overlay layer in the response is a SeriesPayload of the
// same arity.
//
// What this file lands (per the E3-S1 acceptance criteria):
//
//   - SeriesHostView wraps an ordered slice of types.AxisKey group-key
//     tuples plus a sparse `key → value` resolver for the host's metric
//     surface. SERIES handlers read group i's value via
//     `host.ValueAt(i) (float64, bool)` so absent groups (the host did
//     not produce a record for that group) surface as `(0, false)` —
//     handlers emit `SeriesEntry.Summary` without a Statistic/PValue
//     instead of crashing the fold (mirrors the MATRIX path's
//     absent-host-cell discipline).
//   - seriesOverlayHandler is the per-kind execution signature. Handlers
//     receive the spec, the host, and an integer scratch (placeholder
//     for future kind-specific options) and return a fully-shaped
//     OverlayLayer + warnings + error. E3-S2..S5 register the real
//     handlers; E3-S1 only ships the dispatch.
//   - seriesOverlayHandlers is the per-kind dispatch table. Initialised
//     empty in production — no SERIES kinds exist as of E3-S1. The
//     E3-S2..S5 stories add entries.
//   - ApplyOverlaysSeries walks `specs` in matching order, dispatches
//     each via seriesOverlayHandlers, and returns one OverlayLayer per
//     spec in matching order plus a flat warnings slice. Unknown kinds
//     short-circuit with the same PROCESSING_INTERNAL + PULSE_OVERLAY_KIND_UNKNOWN
//     details shape the MATRIX path emits — handlers register through
//     seriesOverlayHandlers, so a SERIES spec naming a MATRIX-only kind
//     falls through this branch and the caller gets a coded failure.
//
// Streamability discipline (story note): SERIES handlers may be
// streamable (E3-S2/S3/S4) or buffered (E3-S5). The dispatch entry
// MUST NOT commit to a memory model at fold-entry — that decision rides
// on the per-kind handler. ApplyOverlaysSeries is purely structural; it
// does not inspect `types.OverlayStreamability`.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime overlay
//     execution stays inside processing/ alongside overlay.go.
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are built
//     with string concatenation so envelope output stays grep-clean
//     against the structural defense ban (CLAUDE.md "Output Format
//     Contract").

// SeriesHostView is the read-only window a SERIES overlay handler uses
// to fold a derived projection over a grouped-Process result. Exposing
// group keys + a value resolver through a view (rather than re-scanning
// records) is the analogue of the buffered CrosstabHostView contract for
// the SERIES family: handlers consume the orchestrator's
// already-computed per-group metric and never re-scan the source cohort.
//
// Construction: NewSeriesHostView(keys, resolver). The view holds the
// caller's slice pointer to the ordered group-key list and a callable
// resolver that turns an index into (value, present). The caller retains
// ownership of both; the view does not mutate.
//
// SERIES alignment contract (FR-A2): handler-emitted SeriesPayload
// entries align with the host's GroupKeys list element-for-element. The
// dispatch never reorders, never drops, and never expands the key list;
// loose alignment (overlay payload as a subset of host keys) is the
// per-handler responsibility — the dispatch passes the full host key
// list down so handlers can decide whether each entry surfaces a real
// value or carries a zero-value/no-Statistic Summary.
//
// Per-group nullity policy: a host that did not produce a record for
// group i (the orchestrator's group iterator skipped the group, the
// filter elided every row in the bucket, etc.) surfaces `(0, false)`
// from `ValueAt(i)`. Handlers emit a SeriesEntry whose Summary has no
// Statistic / PValue populated — the renderer-facing zero-value Summary
// is the documented "present but empty" shape (mirrors the MATRIX path's
// absent-cell handling: the entry slot stays in the parallel slice so
// downstream consumers can index by group ordinal).
type SeriesHostView struct {
	// groupKeys is the ordered list of group-key tuples the host
	// produced. Each AxisKey is a composite tuple matching the
	// orchestrator's grouper list (e.g. ["US"], ["US", "CA"]). Order is
	// the caller's responsibility and the view does not sort.
	groupKeys []types.AxisKey

	// resolver returns the host's metric value for the group at index
	// i. The (value, present) signature mirrors CrosstabHostView.CellAt
	// so handlers can branch on absent observations without
	// distinguishing "0 because the host omitted the group" from "0
	// because the metric is genuinely zero".
	resolver func(i int) (float64, bool)

	// groupFields names the grouper Fields the host's AxisKey tuples
	// align to element-for-element. When populated (E3-S6 orchestrator
	// wiring), the sibling resolver (E3-S5) maps a
	// `Ref.Sibling.Field` to the matching axis-key element index via
	// this slot — index `i` of every AxisKey corresponds to
	// groupFields[i]. When empty (the E3-S5 default, before the
	// orchestrator wires the slot), the sibling resolver falls back to
	// scanning every axis-key element. The slot is optional so the
	// existing E3-S2 / E3-S3 / E3-S4 handlers (which do not consume
	// the field list) keep working against the legacy constructor.
	groupFields []string
}

// NewSeriesHostView wraps an ordered list of group keys plus a value
// resolver as a SERIES host view. groupKeys may be empty (handlers
// receive an empty SeriesPayload); resolver may be nil for hosts that
// expose only the structural shape (e.g. a future axis-only host whose
// overlay handlers read a Ref-driven side channel). When resolver is
// nil ValueAt always returns (0, false).
//
// The view holds the caller's groupKeys slice header by value; mutating
// the slice's backing array after construction yields undefined fold
// behavior. The dispatch contract documents that callers (the grouped
// Process orchestrator in E3-S6) treat the slice as read-only between
// the host build and ApplyOverlaysSeries return.
func NewSeriesHostView(groupKeys []types.AxisKey, resolver func(i int) (float64, bool)) *SeriesHostView {
	return &SeriesHostView{groupKeys: groupKeys, resolver: resolver}
}

// NewSeriesHostViewWithFields wraps an ordered list of group keys, a
// resolver, and the grouper-field list the AxisKey tuples align to.
// The grouper-field list is the source-of-truth for the sibling
// resolver (E3-S5) — `groupFields[i]` names the field index `i` of
// every AxisKey corresponds to. Length of groupFields SHOULD match the
// width of every AxisKey tuple (the orchestrator's grouper list); a
// shorter list is tolerated and the resolver simply cannot resolve
// fields outside the declared range. Empty groupFields keeps the
// resolver in its E3-S5 fallback (scan every axis-key element). The
// helper exists alongside NewSeriesHostView so the legacy E3-S2 /
// E3-S3 / E3-S4 handlers (which never consult the field list) keep
// compiling against the simpler two-argument constructor.
func NewSeriesHostViewWithFields(groupKeys []types.AxisKey, resolver func(i int) (float64, bool), groupFields []string) *SeriesHostView {
	return &SeriesHostView{groupKeys: groupKeys, resolver: resolver, groupFields: groupFields}
}

// GroupCount returns the number of group tuples on the host. Zero when
// the view is nil or the underlying slice is empty.
func (h *SeriesHostView) GroupCount() int {
	if h == nil {
		return 0
	}
	return len(h.groupKeys)
}

// GroupKey returns the composite group-key tuple at index i. Returns
// (nil, false) when i is out of range. Callers consume the returned
// AxisKey read-only — the slice header points at the host's backing
// array and mutating it would corrupt the dispatch's parallel-slice
// alignment with downstream handler emissions.
func (h *SeriesHostView) GroupKey(i int) (types.AxisKey, bool) {
	if h == nil || i < 0 || i >= len(h.groupKeys) {
		return nil, false
	}
	return h.groupKeys[i], true
}

// GroupKeys returns the full ordered group-key slice. Callers consume
// the slice read-only — the slice header points at the host's backing
// array. The helper exists so a handler that needs the full key list
// (e.g. to copy keys into the SeriesPayload for FR-A2 alignment) can
// avoid the per-index GroupKey loop. Returns nil when the view is nil.
func (h *SeriesHostView) GroupKeys() []types.AxisKey {
	if h == nil {
		return nil
	}
	return h.groupKeys
}

// GroupFields returns the grouper Fields the host's AxisKey tuples
// align to element-for-element. Returns nil when the view is nil OR
// when the view was built via NewSeriesHostView (E3-S5 default — the
// field list is optional). The sibling resolver consults this slot to
// map a `Ref.Sibling.Field` to the matching axis-key element index;
// when the slot is empty the resolver falls back to scanning every
// axis-key element for a value match. Callers consume the returned
// slice read-only — the slice header points at the host's backing
// array.
func (h *SeriesHostView) GroupFields() []string {
	if h == nil {
		return nil
	}
	return h.groupFields
}

// ValueAt returns the host's metric value for the group at index i plus
// a present flag mirroring CrosstabHostView.CellAt. Returns (0, false)
// when the index is out of range, the view is nil, the resolver is nil,
// or the resolver itself reports the group as absent. A handler should
// treat (0, false) as "no observation" — emit a SeriesEntry whose
// Summary leaves Statistic / PValue unset rather than computing against
// a sentinel value.
func (h *SeriesHostView) ValueAt(i int) (float64, bool) {
	if h == nil || h.resolver == nil {
		return 0, false
	}
	if i < 0 || i >= len(h.groupKeys) {
		return 0, false
	}
	return h.resolver(i)
}

// seriesOverlayHandler is the per-kind execution signature for the
// SERIES host path. Handlers receive the request spec plus the host
// view and return a fully-shaped OverlayLayer (Name + Kind + Scope +
// Ref + Payload + optional Summary) plus a slice of warnings for groups
// the handler could not resolve (zero denominator, missing host value).
// A handler error short-circuits ApplyOverlaysSeries — used by the
// dispatch for the unknown-kind defense-in-depth branch; per-kind
// contract failures (axis mismatch, scope unsupported) are caught at
// predict time and should not reach here.
//
// Signature parity with overlayHandler (MATRIX path) is intentional —
// the only difference is the host parameter type. Future stories may
// unify the two through a shared interface; v1 keeps them separate so
// each path's host-shape contract stays explicit at the type level.
type seriesOverlayHandler func(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error)

// seriesOverlayHandlers is the per-kind dispatch table for the SERIES
// host path. Stories E3-S2 / E3-S3 / E3-S4 / E3-S5 register the real
// handlers (and add the matching kind constants + capability rows +
// skill mentions per the CLAUDE.md Update Demand row for overlays).
//
// Tests register synthetic handlers by writing into this map and using
// `t.Cleanup` to restore the prior state — the test stub pattern
// documented in processing/overlay_series_test.go. The dispatch is
// intentionally not hidden behind a registration function: keeping the
// table as a plain package-level map lets the test infra swap entries
// without touching production-only code paths.
//
// Adding a new SERIES OverlayKind: declare the constant + streamability
// row (types/overlay.go + types/overlay_streamability.go), add the
// runtime handler in this package, and add the dispatch entry here.
var seriesOverlayHandlers = map[types.OverlayKind]seriesOverlayHandler{
	// OVERLAY_DELTA_VS_SIBLING (E3-S5): per-group additive delta
	// against a sibling group named in Ref.Sibling. Buffered (sibling
	// resolution requires the full materialised SeriesPayload).
	// Handler: applyDeltaVsSibling in
	// processing/overlay_delta_vs_sibling.go.
	types.OverlayKindDeltaVsSibling: applyDeltaVsSibling,
	// OVERLAY_INDEX_VS_BASELINE (E4-S2): per-point ratio index against a
	// single fixed positional baseline of the ordered host series — the
	// baseline is resolved once via processing.ResolveBaselineIndex
	// (E4-S1 foundation helper) and every present point divides by it.
	// Buffered (baseline resolution requires the materialised host
	// series). Handler: applyIndexVsBaseline in
	// processing/overlay_index_vs_baseline.go.
	types.OverlayKindIndexVsBaseline: applyIndexVsBaseline,
	// OVERLAY_INDEX_VS_PRIOR (E4-S4): per-point windowed index against
	// the immediately preceding ordered-axis point — single-state lag
	// carrier (one f64 alongside the per-group accumulators in the
	// streaming fold). First streamable windowed-Process handler in the
	// catalog. Handler: applyIndexVsPrior in
	// processing/overlay_index_vs_prior.go.
	types.OverlayKindIndexVsPrior: applyIndexVsPrior,
	// OVERLAY_INDEX_VS_SIBLING (E3-S5): per-group ratio index against a
	// sibling group named in Ref.Sibling, scaled to ×100. Buffered
	// (sibling resolution requires the full materialised SeriesPayload).
	// Handler: applyIndexVsSibling in
	// processing/overlay_index_vs_sibling.go.
	types.OverlayKindIndexVsSibling: applyIndexVsSibling,
	// OVERLAY_INDEX_VS_TOTAL (E3-S2): per-group index score against the
	// grand total of the host series — first streamable SERIES-host
	// handler in the catalog. Handler: applyIndexVsTotal in
	// processing/overlay_index_vs_total.go.
	types.OverlayKindIndexVsTotal: applyIndexVsTotal,
	// OVERLAY_SHARE_OF_TOTAL (E3-S3): per-group share of the host series'
	// grand total (`group_val / grand_total`, scale 1.0). Sibling handler
	// to INDEX_VS_TOTAL — same grand-total accumulator, different
	// scaling. Handler: applyShareOfTotalSeries in
	// processing/overlay_share_of_total_series.go.
	types.OverlayKindShareOfTotal: applyShareOfTotalSeries,
	// OVERLAY_ZSCORE_VS_TOTAL (E3-S4): per-group standardized z-score
	// against the host series' grand-total distribution
	// (`(group_val - mean) / sd`, population variance). Third and final
	// streamable SERIES-host handler in the E3 grouped-Process subset.
	// Welford accumulator (count + mean + M2) is sibling to the
	// grand-total accumulator INDEX_VS_TOTAL / SHARE_OF_TOTAL share —
	// see computeSeriesWelfordPopulation in
	// processing/overlay_zscore_vs_total.go.
	types.OverlayKindZScoreVsTotal: applyZScoreVsTotal,
}

// computeSeriesGrandTotal folds a running grand-total accumulator over
// every present host value in the SERIES host. Returns the accumulator
// plus the parallel presentMask + values slices the per-group emission
// pass consumes (so callers walk the host once at this entry point and
// keep the per-group accumulators they already need without a second
// resolver dispatch).
//
// Shared between OVERLAY_INDEX_VS_TOTAL (E3-S2) and OVERLAY_SHARE_OF_TOTAL
// (E3-S3) — both kinds fold the same grand-total accumulator over a
// SERIES host and only differ in the scaling step at per-group emission.
// Keeping the accumulator in one helper lines up with the story note for
// E3-S3 (`"do NOT duplicate the running-sum carrier"`) and matches the
// streaming-Process orchestrator's shape: the streaming pass keeps ONE
// f64 grand-total accumulator alongside the per-group accumulators
// regardless of how many SHARE / INDEX / etc. overlays the request
// carries.
//
// Absent groups (the resolver reports `(0, false)`) are skipped — they
// do NOT contribute to the grand total. AGG_SUM semantics over post-
// filter rows, matching the SeriesHostView resolver contract documented
// alongside `ValueAt`.
//
// Returns (0, empty mask, empty values) when host is nil. The
// per-handler caller treats `grandTotal == 0 || math.IsNaN(grandTotal)`
// as the zero-denominator path (one PULSE_OVERLAY_REF_ZERO warning + NaN
// statistics on every present entry).
func computeSeriesGrandTotal(host *SeriesHostView) (grandTotal float64, presentMask []bool, values []float64) {
	if host == nil {
		return 0, nil, nil
	}
	groupCount := host.GroupCount()
	presentMask = make([]bool, groupCount)
	values = make([]float64, groupCount)
	for i := 0; i < groupCount; i++ {
		v, ok := host.ValueAt(i)
		if !ok {
			continue
		}
		presentMask[i] = true
		values[i] = v
		grandTotal += v
	}
	return grandTotal, presentMask, values
}

// ApplyOverlaysSeries executes every spec in specs against the SERIES
// host view and returns one OverlayLayer per spec in matching order
// (FR-A2: stable spec-order layer emission), plus a flat warning slice.
// Returns (nil, nil, nil) when specs is empty so the grouped Process
// orchestrator can call ApplyOverlaysSeries unconditionally — mirrors
// the MATRIX ApplyOverlays short-circuit contract.
//
// Host policy:
//
//   - When specs is empty, host may be nil. The dispatch short-circuits
//     before touching the host.
//   - When specs is non-empty, host may still be nil OR carry an empty
//     group-key list — the dispatch passes the view straight through to
//     the handler. Handlers see the empty group-key case the same way
//     they see "every group has no value" and emit an empty Entries
//     slice. The fold never panics on an empty host.
//   - Loose alignment (FR-A2): when a handler's payload covers a strict
//     subset of host group keys, the dispatch does NOT re-order or drop
//     host keys. The handler is responsible for emitting one SeriesEntry
//     per host group ordinal — entries for groups outside the handler's
//     covered subset carry a zero-value Summary.
//
// Defense in depth: the descriptor.ValidateOverlays gate rejects bad
// kinds at predict time, so a missing dispatch entry should never reach
// the runtime in practice; nonetheless ApplyOverlaysSeries guards
// against an unknown kind and returns a CodedError whose details carry
// errors.PULSE_OVERLAY_KIND_UNKNOWN so the orchestrator surfaces the
// same failure mode predict would have flagged. The Level / Within
// belt-and-suspenders runtime gate the MATRIX path runs at
// validateOverlayLevelWithinRuntime does NOT fire here — SERIES kinds
// do not engage prefix-bucket denominators in E3-S1, and the per-kind
// E3-S2..S5 handlers add their own gates when their math requires it.
func ApplyOverlaysSeries(specs []types.OverlaySpec, host *SeriesHostView) ([]types.OverlayLayer, []OverlayWarning, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	layers := make([]types.OverlayLayer, 0, len(specs))
	var warnings []OverlayWarning
	for i := range specs {
		spec := &specs[i]
		handler, ok := seriesOverlayHandlers[spec.Kind]
		if !ok {
			return nil, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay kind has no SERIES runtime handler: "+string(spec.Kind),
				map[string]any{
					"code":  string(errors.PULSE_OVERLAY_KIND_UNKNOWN),
					"index": i,
					"kind":  string(spec.Kind),
					"host":  "series",
				})
		}
		layer, ws, err := handler(spec, host)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
		if len(ws) > 0 {
			warnings = append(warnings, ws...)
		}
	}
	return layers, warnings, nil
}
