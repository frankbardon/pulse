package processing

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Facet-side population reference resolver — runtime support for the
// Facet-host overlay family.
//
// The four Facet overlay kinds (`OVERLAY_INDEX_VS_POP`,
// `OVERLAY_ZSCORE_VS_POP`, `OVERLAY_CHISQ_VS_POP`,
// `OVERLAY_KS_VS_POP`) all need the same operation — derive a
// "whole-field" population view from a fully-finalized
// `*types.FacetResult`. The resolver lets every handler read against
// one consistent denominator shape so the four kinds do not
// independently reinvent population access.
//
// Two paths emerge from `FacetField.Kind`:
//
//   - Categorical fast path (`Kind == "discrete"`): the population is a
//     bag of `(value, count)` tuples sourced directly from
//     `FacetField.Discrete.Values` plus a precomputed total count. INDEX_VS_POP
//     reads the per-value freq; CHISQ_VS_POP reads the same `(value, count)`
//     tuples as the population contingency.
//
//   - Numeric path (`Kind == "numeric"`): the population is the streaming
//     Welford summary (count, mean, stddev, sum, min, max) plus optional
//     percentiles / histogram already materialized by `FacetSchema`.
//     ZSCORE_VS_POP reads mean + stddev; KS_VS_POP reads the percentile /
//     histogram surface.
//
// The resolver itself does NO recomputation — it borrows whatever
// `FacetSchema` already produced and exposes lookup primitives. This is
// the PRD §6 "no second pass" contract: every Facet overlay handler reads
// the already-folded population state without re-scanning the cohort.
//
// Architectural shape mirrors `CrosstabHostView` (overlay.go) and
// `SeriesHostView` (overlay_series.go) — a view wrapping a fully-
// materialized host payload, exposing per-kind lookups, never mutating
// the underlying payload. Per-kind handlers consume the view to fold
// against population statistics; this file ships only the view + the
// resolver entry point + the unknown-field rejection arm.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside overlay.go.
//   - No fmt.Sprintf in any JSON-bearing path. The resolver builds
//     CodedError messages with string concatenation; structured detail
//     fields ride on the CodedError Details map.
//   - Population resolution is a pure function of the host FacetResult +
//     the requested overlay field. Same FacetResult + same field name →
//     byte-equal lookup return on every invocation.

// FacetPopulationView is the read-only window a Facet overlay handler
// uses to read population statistics for a single overlay field. The view
// holds a pointer to the underlying FacetResult and a resolved
// *FacetField cached at construction so per-call lookups are
// constant-time map walks rather than repeated map probes.
//
// Construction: ResolveFacetPopulation(result, field). The view holds a
// pointer to the underlying FacetResult; lifetime is the caller's
// responsibility and the view does not mutate the payload.
//
// All lookups read what FacetSchema already computed and emitted — the
// view never re-derives counts, never re-runs Welford, never recomputes
// percentiles. The four handlers read what is already on the wire.
type FacetPopulationView struct {
	// result is the underlying FacetResult the host produced. Read-only.
	result *types.FacetResult

	// field names the FacetField slot the overlay resolves against.
	field string

	// resolved is the cached *FacetField pointer (one of
	// result.Fields[field]). Set at construction.
	resolved *types.FacetField
}

// ResolveFacetPopulation resolves a population reference against a
// finalized FacetResult and returns `(view, err)`. The view exposes
// per-kind lookups (categorical fast path + numeric streaming-state path)
// so the four Facet overlay handlers can consume one consistent
// surface.
//
// Two rejection arms — both return `(nil, CodedError)` with code
// `PULSE_OVERLAY_REF_UNKNOWN`:
//
//   - `result == nil` OR `result.Fields == nil`: the host did not produce
//     a FacetResult the resolver can consume. Details carry
//     `{field, available_fields: []}`.
//
//   - `field` is absent from `result.Fields`: the overlay named a
//     population field that was not part of the FacetRequest.Fields
//     slice (the canonical "unknown reference field" arm for this kind).
//     Details carry `{field, available_fields: [<sorted field names>]}`.
//
// Defense-in-depth returns:
//
//   - `field == ""`: same code with `{field: ""}` Details so the caller
//     does not crash on a missing reference. The descriptor validator
//     should reject empty field names at predict time; the runtime
//     resolver stays robust.
//
//   - `result.Fields[field] == nil`: same shape as the absent-field arm
//     (a nil entry under a present key is structurally equivalent to a
//     missing key for the resolver's purposes).
//
// Deterministic + order-stable: the resolver does no folding, no I/O —
// the same FacetResult + same field name produces the same view + the
// same coded-error shape on every invocation. The view itself is a
// pointer-equal wrapper — handlers may compare views via pointer
// identity safely.
func ResolveFacetPopulation(result *types.FacetResult, field string) (*FacetPopulationView, error) {
	if result == nil || result.Fields == nil {
		return nil, newFacetPopulationUnknownError(field, result)
	}
	resolved, ok := result.Fields[field]
	if !ok || resolved == nil {
		return nil, newFacetPopulationUnknownError(field, result)
	}
	return &FacetPopulationView{
		result:   result,
		field:    field,
		resolved: resolved,
	}, nil
}

// Field returns the field name the view resolves against. Read-only.
func (v *FacetPopulationView) Field() string {
	if v == nil {
		return ""
	}
	return v.field
}

// Kind returns the underlying FacetField's Kind discriminator
// (`"discrete"` for the categorical fast path, `"numeric"` for the
// streaming Welford path). Empty string when the view is nil. Handlers
// branch on this string to dispatch the per-kind lookup surface.
func (v *FacetPopulationView) Kind() string {
	if v == nil || v.resolved == nil {
		return ""
	}
	return v.resolved.Kind
}

// FacetField returns the underlying *types.FacetField the resolver
// matched. Read-only — handlers must not mutate the returned struct.
// Nil-safe: returns nil when the view is nil.
func (v *FacetPopulationView) FacetField() *types.FacetField {
	if v == nil {
		return nil
	}
	return v.resolved
}

// TotalRecords returns the host FacetResult's `FilteredRecords` count.
// This is the canonical denominator for index-vs-pop-style ratios on the
// categorical fast path — every per-value count is divided by the
// post-filter record total to obtain a population frequency. Zero when
// the view is nil or the underlying FacetResult is nil.
//
// Distinct from `FacetResult.TotalRecords` (the unfiltered cohort
// record count) — the overlay family compares against the same
// post-filter denominator the per-value counts were folded against, so
// using `FilteredRecords` keeps the per-value-frequency contract
// internally consistent.
func (v *FacetPopulationView) TotalRecords() int64 {
	if v == nil || v.result == nil {
		return 0
	}
	return v.result.FilteredRecords
}

// NullCount returns the count of filtered records where the resolved
// field was null. Mirrors `FacetField.NullCount` on the underlying
// payload. Handlers use this to compute the non-null denominator for
// per-value frequency math (`TotalRecords - NullCount = non-null
// records`). Zero when the view is nil or the resolved field is nil.
func (v *FacetPopulationView) NullCount() int64 {
	if v == nil || v.resolved == nil {
		return 0
	}
	return v.resolved.NullCount
}

// IsDiscrete reports whether the view resolves a categorical (or
// boolean) field — handlers branch on this to enter the
// `DiscreteCounts` / `DiscreteFrequency` fast path. False when the view
// is nil or the resolved field is non-discrete.
func (v *FacetPopulationView) IsDiscrete() bool {
	return v.Kind() == "discrete"
}

// IsNumeric reports whether the view resolves a numeric field —
// handlers branch on this to enter the `NumericSummary` /
// `NumericPercentiles` / `NumericHistogram` streaming-state path. False
// when the view is nil or the resolved field is non-numeric.
func (v *FacetPopulationView) IsNumeric() bool {
	return v.Kind() == "numeric"
}

// DiscreteCounts returns the per-value `(value, count)` tuple slice from
// the underlying FacetField.Discrete.Values when the view resolves a
// categorical / boolean field. Returns `(nil, false)` when the view
// is nil, when the resolved field is non-discrete, or when the discrete
// payload is missing.
//
// The slice is the canonical population contingency the per-value
// overlay kinds read from (CHISQ_VS_POP folds the observed cohort
// distribution against this slice; INDEX_VS_POP divides per-value
// observed by per-value population). Sort order matches
// `FacetField.Discrete.Values` (descending by count, ties ascending by
// value string) — handlers can iterate in payload order without
// re-sorting.
//
// Read-only: handlers must not mutate the returned slice. The view
// returns the underlying slice pointer (no defensive copy) to keep the
// hot path allocation-free; if a handler needs to mutate it must clone
// first.
func (v *FacetPopulationView) DiscreteCounts() ([]types.FacetValueCount, bool) {
	if v == nil || v.resolved == nil || v.resolved.Discrete == nil {
		return nil, false
	}
	return v.resolved.Discrete.Values, true
}

// DiscreteFrequency returns the per-value frequency for a single value
// in the categorical population — `count / total_records` where
// `total_records` is the view's `TotalRecords()`. Returns
// `(frequency, true)` when the value resolves and the denominator is
// non-zero. Returns `(0, false)` when:
//
//   - the view is nil OR the resolved field is non-discrete OR the
//     discrete payload is missing;
//   - the value is not present in the discrete payload (legitimate
//     "value never observed in the population" — the caller surfaces a
//     handler-defined zero / NaN / warning depending on kind);
//   - `TotalRecords()` is zero (the population is empty — handlers
//     surface a layer-level zero-denominator warning the same way the
//     existing SERIES grand-total kinds do).
//
// Lookup is O(n) over the discrete payload; n is the truncated top-K
// slice length (capped per FacetRequest.DiscreteTopK or by the
// dictionary cardinality otherwise — typically small). The handlers can
// cache a value→count map externally if their per-cell hot path warrants
// it; this resolver stays allocation-free for the common single-lookup
// case.
func (v *FacetPopulationView) DiscreteFrequency(value string) (float64, bool) {
	counts, ok := v.DiscreteCounts()
	if !ok {
		return 0, false
	}
	total := v.TotalRecords()
	if total <= 0 {
		return 0, false
	}
	for _, vc := range counts {
		if vc.Value == value {
			return float64(vc.Count) / float64(total), true
		}
	}
	return 0, false
}

// DiscreteFrequencyStdev returns the population standard deviation of
// the per-category frequencies (count / TotalRecords) over the
// resolved discrete payload. Returns `(sd, true)` when the resolved
// field is discrete and the denominator is non-zero; `(0, false)`
// otherwise (view is nil, non-discrete, missing payload, or empty
// counts slice).
//
// This is the per-category frequency-SD that the Facet-host z-score
// overlay (OVERLAY_ZSCORE_VS_POP) reads as `sd_pop`. The
// implementation walks the already-resolved `DiscreteCounts()` slice
// (the per-value (value, count) tuples FacetSchema folded into the
// payload) and uses the canonical Welford-Pébaÿ recurrence over those
// frequencies — one pass, allocation-free, byte-equal to the
// recurrence used by `WelfordStdDev` (`processing/welford.go`) for any
// other population-SD computation. POPULATION SD (divide by N, not
// N-1) is the correct convention because the per-category frequency
// set IS the whole population being summarised — mirrors the
// `OVERLAY_ZSCORE_VS_TOTAL` variance-choice rationale.
//
// Single-entry payload (N=1): the recurrence is well-defined and
// returns `(0, true)` — every frequency equals the single observed
// value's frequency so the variance is exactly zero. Callers (the
// ZSCORE_VS_POP handler) then route every entry through the
// PULSE_OVERLAY_REF_ZERO arm. Distinct from the unknown-shape case
// `(0, false)` which means the view itself is not discrete-enabled.
//
// Read-only: the function does not mutate the underlying payload.
// O(n) over the discrete payload; n is the truncated top-K slice
// length (capped per FacetRequest.DiscreteTopK or by the dictionary
// cardinality otherwise — typically small).
//
// Additive accessor on the resolver — keeps the "no second pass over
// records" contract intact by reading only already-folded population
// state.
func (v *FacetPopulationView) DiscreteFrequencyStdev() (float64, bool) {
	counts, ok := v.DiscreteCounts()
	if !ok {
		return 0, false
	}
	total := v.TotalRecords()
	if total <= 0 {
		return 0, false
	}
	if len(counts) == 0 {
		return 0, false
	}
	// Single-pass Welford-Pébaÿ over per-category frequencies. n is the
	// number of categories (one frequency per discrete value); mean +
	// M2 fold into the population variance `M2 / n`. Matches the same
	// numerical convention (single-pass Welford-Pébaÿ) used by the
	// `WelfordStdDev` helper so cross-mode equivalence tests stay
	// byte-equal within ULP.
	var (
		n    int64
		mean float64
		m2   float64
	)
	denom := float64(total)
	for _, vc := range counts {
		n++
		freq := float64(vc.Count) / denom
		delta := freq - mean
		mean += delta / float64(n)
		delta2 := freq - mean
		m2 += delta * delta2
	}
	if n == 0 {
		return 0, false
	}
	// Population SD: sqrt(M2 / n).
	variance := m2 / float64(n)
	if variance <= 0 {
		// Defense in depth: floating-point rounding may produce a tiny
		// negative variance for constant inputs. Clamp to zero so the
		// downstream sqrt never returns NaN.
		return 0, true
	}
	return math.Sqrt(variance), true
}

// DiscreteCount returns the per-value count for a single value in the
// categorical population. Returns `(count, true)` when the value
// resolves; `(0, false)` when the view is nil / non-discrete / missing
// payload, OR when the value is not present in the discrete payload.
// Distinct from DiscreteFrequency in that the count is returned without
// dividing by TotalRecords — handlers that need the raw observed-vs-
// expected contingency (CHISQ_VS_POP) read this surface; handlers that
// want a normalized index (INDEX_VS_POP) read DiscreteFrequency.
func (v *FacetPopulationView) DiscreteCount(value string) (int64, bool) {
	counts, ok := v.DiscreteCounts()
	if !ok {
		return 0, false
	}
	for _, vc := range counts {
		if vc.Value == value {
			return vc.Count, true
		}
	}
	return 0, false
}

// NumericSummary returns the streaming Welford summary
// (`*types.FacetNumeric`) when the view resolves a numeric field.
// Returns `(nil, false)` when the view is nil, when the resolved field
// is non-numeric, or when the numeric payload is missing.
//
// The handlers read `Mean`, `StdDev`, `Sum`, `Count`, `Min`, `Max`
// directly off the returned struct — these are the exact slots
// `FacetSchema` populated during the streaming pass. No second pass; no
// recomputation. ZSCORE_VS_POP reads `Mean + StdDev`; KS_VS_POP reads
// the percentile / histogram slots.
//
// Read-only: handlers must not mutate the returned struct.
func (v *FacetPopulationView) NumericSummary() (*types.FacetNumeric, bool) {
	if v == nil || v.resolved == nil || v.resolved.Numeric == nil {
		return nil, false
	}
	return v.resolved.Numeric, true
}

// NumericPercentiles returns the percentile map populated by
// `FacetSchema` when `FacetRequest.NumericPercentiles` was non-empty.
// Returns `(nil, false)` when the view is nil, non-numeric, has no
// numeric payload, or the percentile map is empty (the caller did not
// request percentiles — handlers that need them MUST request them on
// the FacetRequest; the resolver does not synthesise).
//
// Map keys are percentile labels (`"p50"`, `"p95"`, ...); values are
// the linearly-interpolated percentile points. The map is the exact
// surface `FacetSchema` populated via `linearPercentile`. KS_VS_POP
// uses this to build the population CDF baseline.
//
// Read-only: handlers must not mutate the returned map.
func (v *FacetPopulationView) NumericPercentiles() (map[string]float64, bool) {
	num, ok := v.NumericSummary()
	if !ok || len(num.Percentiles) == 0 {
		return nil, false
	}
	return num.Percentiles, true
}

// NumericHistogram returns the fixed-width histogram populated by
// `FacetSchema` when `FacetRequest.IncludeHistogram` was true. Returns
// `(nil, false)` when the view is nil, non-numeric, has no numeric
// payload, or the histogram is absent (the caller did not request a
// histogram — handlers that need one MUST request it on the
// FacetRequest; the resolver does not synthesise).
//
// The returned `*types.FacetHistogram` carries `Min`, `Max`, and `Bins`
// (a slice of bucket counts). KS_VS_POP can use the histogram as a
// coarse population CDF approximation when percentiles are not
// available; INDEX_VS_POP can use it for per-bucket frequency math.
//
// Read-only: handlers must not mutate the returned struct.
func (v *FacetPopulationView) NumericHistogram() (*types.FacetHistogram, bool) {
	num, ok := v.NumericSummary()
	if !ok || num.Histogram == nil {
		return nil, false
	}
	return num.Histogram, true
}

// newFacetPopulationUnknownError builds the canonical
// PULSE_OVERLAY_REF_UNKNOWN coded error for an unknown population field.
// The error carries `{field, available_fields}` Details so the caller
// can surface a fixup that suggests the legitimate field choices the
// FacetRequest declared.
//
// The available_fields slice is sorted ascending for determinism — the
// FacetResult.Fields map iteration order is random, but the fixup
// surface a deterministic caller-facing list so error messages stay
// stable across runs. When result is nil OR result.Fields is empty the
// slice is empty (the caller cannot recover by picking a different
// field; the FacetRequest itself is malformed).
func newFacetPopulationUnknownError(field string, result *types.FacetResult) error {
	available := facetPopulationAvailableFields(result)
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_INTERNAL,
		"overlay population reference field "+facetPopulationFieldDisplay(field)+" is not present on the host FacetResult",
		map[string]any{
			"code":             string(errors.PULSE_OVERLAY_REF_UNKNOWN),
			"field":            field,
			"available_fields": available,
		})
}

// facetPopulationAvailableFields returns the sorted ascending list of
// field names the host FacetResult declared. Used to populate the
// PULSE_OVERLAY_REF_UNKNOWN error's `available_fields` Details slot so
// the fixup surface a deterministic caller-facing list of legitimate
// alternates.
//
// Sort is bubble-sort to avoid a `sort` import (the slice length is
// bounded by the FacetRequest.Fields length, typically tiny — under 20
// in practice; bubble-sort stays allocation-free). Returns an empty
// slice when result is nil OR result.Fields is empty.
func facetPopulationAvailableFields(result *types.FacetResult) []string {
	if result == nil || len(result.Fields) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(result.Fields))
	for name := range result.Fields {
		out = append(out, name)
	}
	// Bubble-sort ascending — keeps the helper standalone (no sort
	// import) and the slice is small (bounded by FacetRequest.Fields).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// facetPopulationFieldDisplay renders a field name for inclusion in the
// PULSE_OVERLAY_REF_UNKNOWN error message. Empty names surface as `""`
// (the literal empty-string display) so a caller passing an empty
// reference sees a clear message instead of an apparently-blank field
// substring. Non-empty names surface verbatim wrapped in double quotes.
// Concatenation only — no fmt.Sprintf per the descriptor / processing
// no-fmt-Sprintf ban.
func facetPopulationFieldDisplay(field string) string {
	if field == "" {
		return "\"\""
	}
	return "\"" + field + "\""
}
