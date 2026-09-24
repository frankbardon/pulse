package processing

import (
	"github.com/frankbardon/pulse/types"
)

// filter_pass.go centralises the per-slot {n_in, n_out, n_null_input}
// universal-floor counter pass that drives Response.Components.Filterers
// for every Pulse execution path. The orchestrator instantiates one
// filterPassCounters per Request.Filterers slot before the filter walk;
// each per-record applyFilterPass call updates the counters in place
// and returns whether the record was admitted to the next stage.
//
// The counter triple is uniform across all 11 built-in filterers — the
// MetaFilterer sibling is the embedder-parity surface for per-filter
// operator-specific extras, but v1 has no built-in implementations:
// the universal floor is the whole payload. Built-in filterers
// therefore need no per-kind hook; the orchestrator's applyFilterPass
// is the single emission site.
//
// AND semantics match the historical inline filter walk: the first
// filterer that rejects short-circuits — no later slot's counters
// observe the record, mirroring the original applyFilters / streaming-
// path behaviour. The first slot's n_in equals the post-feature-pass
// total; every subsequent slot's n_in equals the prior slot's n_out
// (the "n_in invariant" called out in the story).

// filterPassCounters tracks the universal-floor counters for one
// Request.Filterers slot. Values rendered into types.FiltererComponents
// at end of pass via buildFiltererComponents.
type filterPassCounters struct {
	// nIn counts records that entered this filter (= records that
	// passed every prior filterer, or the post-feature-pass total for
	// the first slot).
	nIn int
	// nOut counts records that passed this filter (admitted to the
	// next stage).
	nOut int
	// nNullInput counts records where the filterer's Field was null
	// on input. Tallied independently of pass/fail — a record can be
	// both null-input AND admitted (FILTER_EXCLUDE / FILTER_NULL
	// is_null) or both null-input AND rejected (FILTER_INCLUDE).
	// FILTER_EXPRESSION carries no Field, so nNullInput stays 0.
	nNullInput int
}

// newFilterPassCounters allocates one counter slot per filterer in the
// request. Returns nil for an empty filter chain so callers can keep
// the no-filter fast path allocation-free.
func newFilterPassCounters(filterers []*types.Filterer) []filterPassCounters {
	if len(filterers) == 0 {
		return nil
	}
	return make([]filterPassCounters, len(filterers))
}

// applyFilterPass runs every filterer in the chain against a single
// record while updating the per-slot counters in place. Returns
// (true, nil) when the record was admitted (passed every filterer) or
// (false, nil) when any filter rejected it. A non-nil error follows
// the standard error-routing contract (PROCESSING_RUNTIME / wrapped
// CodedError).
//
// Counter increment order, per slot i:
//
//  1. nIn[i]++ unconditionally on entry — a record reaches slot i iff
//     every prior slot admitted it.
//  2. nNullInput[i]++ when filterers[i].Field is non-empty and the
//     record's Field is null on input.
//  3. nOut[i]++ iff the filter function returns true.
//
// AND semantics: the first rejecting filterer short-circuits — no
// later slot sees the record, matching the historical applyFilters /
// streaming-path walk.
//
// Callers MUST pre-allocate filterFns and counters to the same length
// as filterers (newFilterPassCounters does this for counters). The
// helper is hot-path code; a precondition mismatch is a programming
// error rather than a runtime fault.
func applyFilterPass(
	record *Record,
	filterers []*types.Filterer,
	filterFns []FilterFunc,
	counters []filterPassCounters,
) (bool, error) {
	for i, fn := range filterFns {
		counters[i].nIn++
		if field := filterers[i].Field; field != "" && record.IsNull(field) {
			counters[i].nNullInput++
		}
		ok, err := fn(record)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		counters[i].nOut++
	}
	return true, nil
}

// buildFiltererComponents materialises one types.FiltererComponents
// entry per filterer slot from the post-pass counter state. The Label
// field mirrors the originating types.Filterer.Label so callers can
// join the components entry back to its request slot — types.Filterer
// does not yet carry a Label field in v1, so the Label stays empty
// (omitempty on the wire) until the slot gains one. Universal-floor
// counters always emit; per-filter operator-specific extras (no
// built-in implementations in v1) ride off the MetaFilterer sibling
// when an embedder-supplied filterer implements it.
//
// Returns nil for an empty filter chain so callers can preserve
// byte-identical wire output (omitempty drops the Filterers slice
// entirely).
func buildFiltererComponents(filterers []*types.Filterer, counters []filterPassCounters) []types.FiltererComponents {
	if len(filterers) == 0 {
		return nil
	}
	out := make([]types.FiltererComponents, len(filterers))
	for i := range filterers {
		out[i] = types.FiltererComponents{
			NIn:        counters[i].nIn,
			NOut:       counters[i].nOut,
			NNullInput: counters[i].nNullInput,
		}
	}
	return out
}

// attachFiltererComponents appends the per-slot FiltererComponents
// entries to resp.Components.Filterers, allocating the parent
// ResponseComponents shell as needed. Mirrors attachAggregationComponents
// / attachGrouperComponents so every execution path populates the
// components shell identically; an empty entries slice is a no-op and
// preserves byte-identical wire output.
func attachFiltererComponents(resp *types.Response, entries []types.FiltererComponents) {
	if len(entries) == 0 || resp == nil {
		return
	}
	if resp.Components == nil {
		resp.Components = &types.ResponseComponents{}
	}
	resp.Components.Filterers = append(resp.Components.Filterers, entries...)
}

// ---------------------------------------------------------------
// Exported surface for service/'s parallel reducers.
//
// The per-shard (Options.ShardWorkers) and per-segment
// (Options.DecodeWorkers) reducers live in service/ but must produce
// the SAME Response.Components a serial run produces — the contract is
// keyed to the request, not to a concurrency knob. Rather than let
// service/ re-derive the counter semantics (the n_in invariant, the
// null-input tally that is independent of pass/fail, the AND
// short-circuit), the walk above is exported verbatim. A second
// implementation of these rules is a second set of numbers waiting to
// drift.
// ---------------------------------------------------------------

// FilterPassCounters is the exported handle on one filterer slot's
// universal-floor counters. Values are produced by
// NewFilterPassCounters and consumed by ApplyFilterPass /
// MergeFilterPassCounters / BuildFiltererComponents; the fields stay
// unexported so the increment rules have exactly one implementation.
type FilterPassCounters = filterPassCounters

// NewFilterPassCounters allocates one counter slot per filterer.
// Returns nil for an empty chain so callers keep the no-filter fast
// path allocation-free.
func NewFilterPassCounters(filterers []*types.Filterer) []FilterPassCounters {
	return newFilterPassCounters(filterers)
}

// ApplyFilterPass is the exported form of the per-record filter walk.
// See applyFilterPass for the counter-increment contract.
func ApplyFilterPass(
	record *Record,
	filterers []*types.Filterer,
	filterFns []FilterFunc,
	counters []FilterPassCounters,
) (bool, error) {
	return applyFilterPass(record, filterers, filterFns, counters)
}

// MergeFilterPassCounters folds src into dst slot-wise. All three
// counters are plain per-record tallies, so the fold is a sum and is
// both associative and commutative — a partition of the record stream
// across workers produces the same totals as a single pass in any
// merge order.
//
// A length mismatch is a programming error (both slices come from
// NewFilterPassCounters over the same request) and is ignored rather
// than panicking in a hot merge; the shorter slice bounds the walk.
func MergeFilterPassCounters(dst, src []FilterPassCounters) {
	n := len(dst)
	if len(src) < n {
		n = len(src)
	}
	for i := 0; i < n; i++ {
		dst[i].nIn += src[i].nIn
		dst[i].nOut += src[i].nOut
		dst[i].nNullInput += src[i].nNullInput
	}
}

// BuildFiltererComponents is the exported form of the post-pass
// renderer. Returns nil for an empty chain.
func BuildFiltererComponents(filterers []*types.Filterer, counters []FilterPassCounters) []types.FiltererComponents {
	return buildFiltererComponents(filterers, counters)
}

// AttachFiltererComponents is the exported form of the attach helper.
func AttachFiltererComponents(resp *types.Response, entries []types.FiltererComponents) {
	attachFiltererComponents(resp, entries)
}
