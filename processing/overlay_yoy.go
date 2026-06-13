package processing

import (
	"encoding/json"
	"math"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_YOY — per-point year-over-year ratio against the same period
// one year prior in an ordered SERIES (grouped Process) host whose
// grouper is `GROUP_DATE`.
//
// E4-S7 scope:
//
//   - Sixth windowed-Process overlay in the catalog (siblings: E4-S2
//     INDEX_VS_BASELINE, E4-S3 DELTA_VS_BASELINE, E4-S4 INDEX_VS_PRIOR,
//     E4-S5 INDEX_VS_ROLLING_MEAN, E4-S6 ZSCORE_VS_ROLLING). Registered
//     in processing/overlay_series.go's seriesOverlayHandlers dispatch
//     table; the dispatch route is the buffered post-host-finalize
//     entry point for the SERIES overlay engine.
//   - First kind to consume the `Ref.YoY` empty marker arm of the
//     OverlayRef discriminated union. The arm is an empty marker — the
//     frequency value lives on `OverlaySpec.Params["frequency"]` (the
//     YoY's own override) or falls back to `req.Groups[0].Params["frequency"]`
//     (the canonical GROUP_DATE authoring slot).
//   - Per-point math: `index_i = point_value_i / prior_year_value * 100`
//     where `prior_year_value` is the host series value at the matching
//     prior-year ordinal computed per frequency:
//
//	annual    ⇒ i - 1
//	quarterly ⇒ i - 4
//	monthly   ⇒ i - 12
//	weekly    ⇒ i - 52 (calendar-week aligned; no day-of-week realignment in v1)
//	daily     ⇒ exact-date 365-days-prior arithmetic via t.AddDate(-1, 0, 0)
//	            + exact-key lookup against the host key index. Feb 29 in
//	            a non-leap prior year emits NaN (no exact-key match) —
//	            explicit non-goal in v1 (no leap-year realignment).
//	hourly    ⇒ exact-hour 365×24-hour-prior arithmetic with the same
//	            exact-key rule.
//
// Required GROUP_DATE host grouper (runtime mirror of predict gate):
// the kind only operates against a SERIES host whose first grouper is
// `GROUP_DATE`. The runtime introspection reads the grouper kind via
// `SeriesHostView.GrouperKinds()[0]` (populated by the E3-S6
// orchestrator wiring + the E4-S7 SeriesHostView extension). When the
// host's grouper kind list is empty (callers that built the view via
// the pre-E4-S7 constructors) the runtime defers to the predict gate's
// earlier rejection — predict already surfaces
// `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE` for non-DATE hosts via
// `descriptor.validateOverlayYoY` (the runtime defense is cheap and
// matches the INDEX_VS_PRIOR / INDEX_VS_ROLLING_MEAN safety pattern).
//
// Frequency resolution order (predict and runtime agree):
//
//  1. spec.Params["frequency"] (the YoY's own override).
//  2. req.Groups[0].Params["frequency"] (the canonical GROUP_DATE
//     authoring slot).
//  3. Missing both ⇒ PULSE_OVERLAY_YOY_FREQUENCY_MISSING.
//
// Note: this handler runs at the SERIES exit and receives the spec but
// NOT the request — the host's grouper Params are NOT available here.
// The runtime relies on the spec.Params["frequency"] arm; the predict
// gate walks req.Groups[0].Params and the orchestrator wiring (E3-S6
// applyOverlaysSeriesToResponse) is responsible for promoting the
// grouper's frequency onto every YoY spec before the handler runs.
// The hook lives at applyOverlaysSeriesToResponse — see the call site
// comments there for the promotion details. The handler defends
// against a missing frequency by emitting the
// PULSE_OVERLAY_YOY_FREQUENCY_MISSING code from this surface even
// though predict and the orchestrator hook should catch it first.
//
// First-year-no-comparison semantics: every ordinal whose prior-year
// index lands at < 0 (coarse-frequency arms) OR whose daily/hourly
// prior-year date does not match an exact host key emits NaN without
// warning — "no comparison available" is structurally distinct from
// "denominator was zero" (mirrors the OVERLAY_INDEX_VS_PRIOR
// first-point NaN rule). The handler does NOT raise
// PULSE_OVERLAY_REF_ZERO on this branch.
//
// Zero-prior path: when the resolved prior value is `0` at the divide
// step (legitimate prior point with a zero post-filter sum) the
// handler emits ONE PULSE_OVERLAY_REF_ZERO warning per layer and
// surfaces NaN on the affected entry. Mirrors the share / index /
// zscore family's zero-denominator contract (one warning per layer,
// not per cell).
//
// Absent-point policy: an absent host ordinal (resolver reports
// (0, false)) emits a present SeriesEntry whose Summary leaves
// Statistic unset and does NOT participate in the YoY computation.
// Each prior-year lookup is independent (no carrier state) — an
// absent ordinal at position `i` does not affect the lookup for any
// other ordinal.
//
// Structural invariants:
//
//   - This file MUST NOT import service/ or descriptor/. Runtime
//     overlay execution rides inside processing/ alongside the
//     aggregator / attribute / grouper layers (mirrors overlay.go /
//     overlay_series.go / overlay_index_vs_prior.go /
//     overlay_index_vs_rolling_mean.go).
//   - No fmt.Sprintf in any JSON-bearing path. Warning messages are
//     built with string concatenation so envelope output stays
//     grep-clean against the structural defense ban.

// yoyFrequency enumerates the supported `frequency` Param values for
// OVERLAY_YOY. Listed here in the canonical order the
// PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY warning surfaces them so
// the Details["supported"] slot stays deterministic.
var yoySupportedFrequencies = []string{
	"annual",
	"quarterly",
	"monthly",
	"weekly",
	"daily",
	"hourly",
}

// applyYoY is the OVERLAY_YOY runtime handler. For every host group
// ordinal it surfaces `point / prior_year_value * 100` on the
// SeriesEntry's Summary.Statistic. The handler dispatches by
// frequency (annual / quarterly / monthly / weekly / daily / hourly)
// to compute the prior-year ordinal index.
//
// Defense in depth: the descriptor validator rejects ref / scope shape
// mismatches at predict time. The handler still defends against (a) a
// nil host, (b) a non-GROUP_DATE host grouper (when the grouper-kind
// list is populated), and (c) a missing / unsupported frequency Param.
// All three defenses surface coded errors mirroring the predict
// gate's failure shapes.
func applyYoY(spec *types.OverlaySpec, host *SeriesHostView) (types.OverlayLayer, []OverlayWarning, error) {
	if host == nil {
		return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires a non-nil SeriesHostView",
			map[string]any{
				"code": string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
				"kind": string(spec.Kind),
			})
	}

	// Runtime host-grouper-kind introspection. When the host carries a
	// populated grouper-kind list (the E3-S6 orchestrator wiring
	// thread-through), enforce the "first grouper must be GROUP_DATE"
	// requirement at runtime. When the list is empty (legacy
	// constructors) defer to the predict gate's earlier rejection.
	if kinds := host.GrouperKinds(); len(kinds) > 0 {
		if kinds[0] != types.GROUP_DATE {
			return types.OverlayLayer{}, nil, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"overlay "+string(spec.Kind)+" requires a SERIES host whose first grouper is GROUP_DATE; got "+string(kinds[0]),
				map[string]any{
					"code":          string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE),
					"kind":          string(spec.Kind),
					"host_grouper":  string(kinds[0]),
					"required":      string(types.GROUP_DATE),
				})
		}
	}

	// Resolve the frequency from the spec's own Params first (the YoY's
	// override). The predict gate + the orchestrator hook walk
	// req.Groups[0].Params["frequency"] and promote it onto the spec
	// before the handler runs, so the runtime fall-through to a missing
	// frequency surface is defense in depth.
	frequency, err := extractYoYFrequency(spec)
	if err != nil {
		return types.OverlayLayer{}, nil, err
	}

	groupCount := host.GroupCount()

	// Per-frequency lookup-index resolver. Coarse-frequency arms map an
	// ordinal `i` to `i - <stride>` directly. Fine-frequency arms
	// (daily / hourly) walk the host's key list as a backing array, treat
	// every key as a parsed time.Time, and exact-key-match against the
	// derived prior-year timestamp.
	priorIndex, frequencyDetails := newYoYPriorLookup(host, frequency)

	entries := make([]types.SeriesEntry, 0, groupCount)
	var (
		minV         float64
		maxV         float64
		seen         int
		zeroPriorHit bool
	)
	for i := 0; i < groupCount; i++ {
		key, _ := host.GroupKey(i)
		var keyCopy types.AxisKey
		if key != nil {
			keyCopy = append(types.AxisKey(nil), key...)
		}

		value, present := host.ValueAt(i)
		var summary types.OverlaySummary
		if present {
			index, ok := priorIndex(i)
			var stat float64
			switch {
			case !ok:
				// No prior-year ordinal available — first-year-no-
				// comparison case. Emit NaN without warning ("no
				// comparison available" is structurally distinct from
				// "denominator was zero").
				stat = math.NaN()
			default:
				priorValue, priorPresent := host.ValueAt(index)
				switch {
				case !priorPresent:
					// Prior-year ordinal landed on an absent host
					// point. Mirror the first-year-no-comparison
					// policy: NaN, no warning.
					stat = math.NaN()
				case priorValue == 0:
					stat = math.NaN()
					zeroPriorHit = true
				default:
					stat = value / priorValue * 100.0
				}
			}
			statCopy := stat
			summary.Statistic = &statCopy
			if !math.IsNaN(stat) {
				if seen == 0 {
					minV, maxV = stat, stat
				} else {
					if stat < minV {
						minV = stat
					}
					if stat > maxV {
						maxV = stat
					}
				}
				seen++
			}
		}
		// Absent host point: emit a present SeriesEntry with an unset
		// Summary.Statistic (the canonical "present slot, empty
		// summary" shape from E3-S1).
		entries = append(entries, types.SeriesEntry{
			Key:     keyCopy,
			Summary: summary,
		})
	}

	var warnings []OverlayWarning
	if zeroPriorHit {
		details := map[string]any{
			"kind":        string(spec.Kind),
			"host":        "series",
			"group_count": groupCount,
			"frequency":   frequency,
		}
		for k, v := range frequencyDetails {
			details[k] = v
		}
		warnings = append(warnings, OverlayWarning{
			Code:    string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " encountered a zero prior-year value; affected indices emitted as NaN",
			Details: details,
		})
	}

	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape: types.OverlayShapeSeries,
			Series: &types.SeriesPayload{
				Entries: entries,
			},
		},
	}

	// Layer-level Summary: baseline = 100 (mirrors INDEX_VS_MARGIN /
	// INDEX_VS_TOTAL / INDEX_VS_SIBLING / INDEX_VS_PRIOR /
	// INDEX_VS_BASELINE / INDEX_VS_ROLLING_MEAN — the index family
	// centres diverging colour ramps on 100). Min / Max / Count
	// populate from present + non-NaN entries.
	baseline := 100.0
	summary := &types.OverlaySummary{Baseline: &baseline}
	if seen > 0 {
		mn, mx, count := minV, maxV, seen
		summary.Min = &mn
		summary.Max = &mx
		summary.Count = &count
	} else {
		zeroCount := 0
		summary.Count = &zeroCount
	}
	layer.Summary = summary

	return layer, warnings, nil
}

// extractYoYFrequency reads OverlaySpec.Params["frequency"] and validates
// it against the supported set. Returns (frequency, nil) on a known
// value; returns ("", err) on missing / non-string / unsupported. The
// coded error mirrors the predict gate's failure shape so MCP / CLI
// envelopes carry the same structured context the predict step would
// have emitted.
//
// Note: this helper only reads spec.Params — req.Groups[0].Params is
// the orchestrator hook's responsibility (predict walks both). When the
// orchestrator wiring promotes the GROUP_DATE frequency onto the
// spec.Params slot before dispatch (the canonical authoring pattern),
// the handler sees the promoted value here. When the orchestrator does
// not promote and the caller did not author spec.Params["frequency"]
// the handler surfaces PULSE_OVERLAY_YOY_FREQUENCY_MISSING as a
// defense-in-depth defense (predict + the orchestrator should catch it
// first).
func extractYoYFrequency(spec *types.OverlaySpec) (string, error) {
	frequency, present := readYoYFrequencyFromParams(spec.Params)
	if !present {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" requires Params[\"frequency\"] (one of: annual | quarterly | monthly | weekly | daily | hourly); not found on OverlaySpec.Params or host GROUP_DATE.Params",
			map[string]any{
				"code":  string(errors.PULSE_OVERLAY_YOY_FREQUENCY_MISSING),
				"kind":  string(spec.Kind),
				"param": "frequency",
			})
	}
	if !isYoYSupportedFrequency(frequency) {
		return "", errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"overlay "+string(spec.Kind)+" Params[\"frequency\"] value is not in the supported set (annual | quarterly | monthly | weekly | daily | hourly): "+frequency,
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY),
				"kind":      string(spec.Kind),
				"frequency": frequency,
				"supported": append([]string(nil), yoySupportedFrequencies...),
			})
	}
	return frequency, nil
}

// readYoYFrequencyFromParams pulls the "frequency" key out of a raw
// JSON Params blob. Returns (value, true) when the blob is a valid
// object carrying a string-typed "frequency" entry; ("", false) when
// the blob is absent / empty / non-object / missing the key / the value
// is non-string. The helper is shared between the runtime
// extractYoYFrequency surface and any future predict-side helper that
// needs to peek into the Params blob.
func readYoYFrequencyFromParams(params json.RawMessage) (string, bool) {
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

// isYoYSupportedFrequency reports whether the given frequency string is
// in the OVERLAY_YOY supported set. Linear scan over a 6-element table
// — no allocation, no map lookup needed.
func isYoYSupportedFrequency(frequency string) bool {
	for _, f := range yoySupportedFrequencies {
		if frequency == f {
			return true
		}
	}
	return false
}

// newYoYPriorLookup returns the per-frequency prior-year ordinal
// resolver plus a per-frequency Details map fragment surfaced on the
// PULSE_OVERLAY_REF_ZERO warning (so renderers can audit which
// frequency arm produced the zero-prior path). The lookup signature is
// `(i int) -> (priorIndex int, ok bool)`:
//
//   - Coarse-frequency arms (annual / quarterly / monthly / weekly):
//     return (i - <stride>, true) when i - <stride> >= 0; (0, false)
//     otherwise (first-year-no-comparison).
//   - Fine-frequency arms (daily / hourly): parse the host's key at
//     ordinal i as a time.Time, derive the prior-year timestamp via
//     t.AddDate(-1, 0, 0) (daily) or t.Add(-365*24*time.Hour) (hourly),
//     and exact-key-match against a precomputed key→index map. When the
//     key parse fails OR no exact match exists, return (0, false).
//
// Note on hourly arithmetic: 365*24*time.Hour is the canonical
// "one year prior" stride for hourly buckets. Leap years are an
// explicit non-goal in v1; an hourly bucket exactly 365 days prior to
// a Feb 29 timestamp lands on Feb 28 of the prior year, which may or
// may not exist as a host key depending on the cohort. The exact-key
// rule treats both cases identically (no match ⇒ NaN, no warning).
func newYoYPriorLookup(host *SeriesHostView, frequency string) (func(i int) (int, bool), map[string]any) {
	switch frequency {
	case "annual":
		return func(i int) (int, bool) {
			if i-1 < 0 {
				return 0, false
			}
			return i - 1, true
		}, map[string]any{"stride": 1}
	case "quarterly":
		return func(i int) (int, bool) {
			if i-4 < 0 {
				return 0, false
			}
			return i - 4, true
		}, map[string]any{"stride": 4}
	case "monthly":
		return func(i int) (int, bool) {
			if i-12 < 0 {
				return 0, false
			}
			return i - 12, true
		}, map[string]any{"stride": 12}
	case "weekly":
		return func(i int) (int, bool) {
			if i-52 < 0 {
				return 0, false
			}
			return i - 52, true
		}, map[string]any{"stride": 52}
	case "daily":
		// Build a key→index map keyed on the canonical date-string
		// representation of each host key. Each key's element[0]
		// holds the GROUP_DATE bucket value as a string (the
		// orchestrator's group-key emission shape mirrors
		// processGrouped / processStreamingGrouped). Parsable
		// timestamps land in the map; unparseable keys are skipped
		// (they cannot serve as a prior anchor by definition).
		index := buildYoYDateKeyIndex(host, yoyDailyKeyLayouts)
		return func(i int) (int, bool) {
			key, ok := host.GroupKey(i)
			if !ok || len(key) == 0 {
				return 0, false
			}
			t, ok := parseYoYDateKey(key[0], yoyDailyKeyLayouts)
			if !ok {
				return 0, false
			}
			prior := t.AddDate(-1, 0, 0)
			priorKey := prior.Format(yoyDailyCanonicalLayout)
			j, present := index[priorKey]
			if !present {
				return 0, false
			}
			return j, true
		}, map[string]any{"stride_kind": "exact_date"}
	case "hourly":
		index := buildYoYDateKeyIndex(host, yoyHourlyKeyLayouts)
		return func(i int) (int, bool) {
			key, ok := host.GroupKey(i)
			if !ok || len(key) == 0 {
				return 0, false
			}
			t, ok := parseYoYDateKey(key[0], yoyHourlyKeyLayouts)
			if !ok {
				return 0, false
			}
			prior := t.Add(-365 * 24 * time.Hour)
			priorKey := prior.Format(yoyHourlyCanonicalLayout)
			j, present := index[priorKey]
			if !present {
				return 0, false
			}
			return j, true
		}, map[string]any{"stride_kind": "exact_hour"}
	}
	// Unreachable — extractYoYFrequency rejects unsupported frequencies
	// before this dispatch runs. Default to "always no-prior" so a
	// future frequency that lands in the static table without a dispatch
	// arm degrades gracefully (every entry emits NaN with no warning).
	return func(int) (int, bool) { return 0, false }, map[string]any{"stride_kind": "unknown"}
}

// yoyDailyKeyLayouts enumerates the date layouts the daily exact-key
// arm accepts when parsing host keys. The first match wins. The set
// covers the GROUP_DATE day-component canonical layout (`2006-01-02`)
// plus the ISO-8601 date+time layout (`2006-01-02T15:04:05`) and the
// RFC 3339 layout (`time.RFC3339`) so cohorts that author day-bucket
// keys with embedded time-of-day still parse.
var yoyDailyKeyLayouts = []string{
	"2006-01-02",
	"2006-01-02T15:04:05",
	time.RFC3339,
}

// yoyDailyCanonicalLayout is the layout used to build prior-year keys
// for the daily exact-key arm. The handler renders the derived prior
// date via this layout and matches against the host key index built
// with the SAME layout (every host key's parsed time is re-rendered
// through this layout when populating the index).
var yoyDailyCanonicalLayout = "2006-01-02"

// yoyHourlyKeyLayouts enumerates the date layouts the hourly exact-key
// arm accepts when parsing host keys. The first match wins. Covers
// GROUP_DATE hour-component canonical layouts plus RFC 3339.
var yoyHourlyKeyLayouts = []string{
	"2006-01-02T15",
	"2006-01-02 15",
	"2006-01-02T15:04:05",
	time.RFC3339,
}

// yoyHourlyCanonicalLayout is the layout used to build prior-year keys
// for the hourly exact-key arm. Mirrors yoyDailyCanonicalLayout's role
// for the hourly arm.
var yoyHourlyCanonicalLayout = "2006-01-02T15"

// buildYoYDateKeyIndex precomputes a key→index map for the
// fine-frequency daily / hourly arms. Each host key's element[0] is
// parsed against the supplied layout list (first match wins); on
// successful parse the canonical-layout-rendered key lands in the
// returned map mapping to the host ordinal. Unparseable keys are
// skipped (they cannot serve as prior anchors).
//
// The canonical layout is the FIRST entry in the supplied list — both
// callers pass `yoyDailyKeyLayouts` / `yoyHourlyKeyLayouts` whose first
// entry is the canonical layout (`2006-01-02` / `2006-01-02T15`). The
// handler matches against the SAME canonical layout when building the
// prior-year lookup key so the rendered representation lines up
// regardless of the original host-key authoring shape.
func buildYoYDateKeyIndex(host *SeriesHostView, layouts []string) map[string]int {
	groupCount := host.GroupCount()
	out := make(map[string]int, groupCount)
	canonical := layouts[0]
	for i := 0; i < groupCount; i++ {
		key, ok := host.GroupKey(i)
		if !ok || len(key) == 0 {
			continue
		}
		t, ok := parseYoYDateKey(key[0], layouts)
		if !ok {
			continue
		}
		out[t.Format(canonical)] = i
	}
	return out
}

// parseYoYDateKey turns the first axis-key element into a time.Time
// against the supplied layout list. Returns (t, true) on the first
// successful parse; (zero time, false) when no layout matches OR the
// element is not a string-shaped value.
func parseYoYDateKey(raw any, layouts []string) (time.Time, bool) {
	s, ok := raw.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
