package processing

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Dictionary-prefix drift probe for the COMPOSE-host overlay dispatch.
// Runs AFTER the key-set (E7-S6) and shape/schema (E7-S7) gates and
// BEFORE per-kind handler dispatch — but ONLY when the caller opted
// into the byte-equal fast path via
// ComposeOverlaySpec.Options.DictPrefixFast = true.
//
// Background:
//
//   Compose-only overlays compare target slot values against a
//   reference slot at byte-equal coordinates. The DEFAULT execution
//   path is the SAFE "by-label join" — every cell / group key is
//   compared via its already-decoded label string (AxisKey []any
//   carries the decoded label per-element, so the chassis-side
//   axisKeyToString helper produces byte-equal canonical strings
//   regardless of whether the underlying categorical_u32 columns
//   happened to assign different dictionary indices to the same label
//   across slots). That path is intrinsically tolerant of arbitrary
//   dict reordering — the renderer-facing contract holds whether the
//   reference dict is ["a","b"] and the target is ["b","a"] or the
//   reference is ["a","b","c"] and the target is ["b"] only.
//
//   E7-S8 opts callers into a faster DIRECT-INDEX comparison path via
//   ComposeOverlaySpec.Options.DictPrefixFast = true. That path
//   skips the per-key decode and compares categorical index bytes
//   directly across slots; correctness depends on every slot's
//   categorical dictionary being byte-equal on the common prefix.
//   When the prefix-equality assumption holds the fast path is
//   indistinguishable from the safe path; when it does NOT hold the
//   fast path silently misaligns cells. We fail loud with
//   PULSE_OVERLAY_DICT_PREFIX_DRIFT rather than degrade silently.
//
// Probe-validation-at-pulse.New scoping non-goal:
//
//   The E7-S8 acceptance criteria originally specified probe-validation
//   at `pulse.New` per the existing extension probe pattern. In
//   practice that requires the runtime to know which Compose-able
//   cohorts will be paired BEFORE any request runs — cohort pairing
//   is a per-request decision (each ComposedRequest names its slot
//   cohorts), not a registration-time one, so the registration-time
//   probe has no input to probe against. The E7-S8 pragmatic scope
//   instead runs the probe per ApplyComposeOverlays invocation
//   against the already-finalised per-slot *Response objects. The
//   per-invocation probe carries the same correctness guarantee at
//   roughly the same cost (a single linear-scan comparison per
//   categorical axis per spec) and lifts the registration-time
//   coupling constraint.
//
// What "dictionary" means at the *types.Response layer:
//
//   *types.Response does NOT carry the underlying schema-level
//   *encoding.Dictionary by design — overlay machinery operates on
//   finalised results, not raw cohort schemas. The Compose-host probe
//   instead compares the ordered axis-key sequences (MatrixPayload's
//   RowKeys / ColumnKeys, which are the per-axis tuples already
//   decoded to their renderer-facing label form). Two slots whose
//   underlying schema dicts share a byte-equal prefix produce axis
//   key sequences that also share a byte-equal prefix at the
//   response layer (because the matrix builder sorts axes by key
//   lexicographically, the response-layer order is a deterministic
//   function of the schema-layer dict order). The check is therefore
//   structurally equivalent to a schema-layer dict-prefix check
//   while keeping the probe inside the processing package free of
//   service / encoding cross-imports.
//
// Structural invariants:
//
//   - Pure function. No I/O. No goroutines. No global state. No
//     mutation of `refResp` / `targetResps` / `spec`. Same inputs →
//     same outputs.
//   - MUST NOT import service/ or descriptor/. The check stays inside
//     processing/ alongside the rest of the overlay machinery.
//   - No fmt.Sprintf in any JSON-bearing path. Detail keys are plain
//     map[string]any populated with encoding/json-friendly types
//     (string, []string) so the envelope serializer renders them
//     verbatim — no hand-built JSON shaping in the error payload
//     (CLAUDE.md "Structural defense bans").
//
// Codes:
//
//   - PULSE_OVERLAY_DICT_PREFIX_DRIFT: emitted when the caller opted
//     into DictPrefixFast and ANY target's per-axis dictionary prefix
//     diverges from the reference's. Details carry { reference,
//     target_label, field, reference_dict_prefix, target_dict_prefix }.

// checkDictPrefixEquality is the per-invocation probe the COMPOSE
// dispatch calls per spec when ComposeOverlaySpec.Options.DictPrefixFast
// is true. Walks `targetResps` against `refResp` and asserts that for
// every per-axis dictionary present on the reference, the target's
// dictionary is a byte-equal prefix (or extension) of the reference's
// on the common length. Returns the first divergence as a coded
// error carrying PULSE_OVERLAY_DICT_PREFIX_DRIFT in Details; nil when
// every target's per-axis dictionaries align.
//
// What the probe walks:
//
//	For each (refResp, tResp) pair, the probe consults both the row
//	axis and the column axis of the MatrixPayload when both slots are
//	MATRIX shape. SCALAR / SERIES slots have no comparable
//	categorical dictionary at the response layer (SCALAR has no axis;
//	SERIES carries grouper-key columns whose ordering is not
//	guaranteed to derive from a categorical dictionary) so the probe
//	degenerates to a no-op for those host shapes — the safe by-label
//	join is the only correct path for non-MATRIX hosts regardless of
//	the fast-path opt-in.
//
// Comparison rule:
//
//	Two ordered AxisKey sequences agree on a prefix iff for every
//	index i < min(len(ref), len(target)), the canonical string form
//	(axisKeyToString) of ref[i] equals the canonical string form of
//	target[i]. When one sequence is shorter than the other the
//	comparison ends at the shorter length — the longer one is allowed
//	to "extend" the dictionary so long as the prefix matches (mirrors
//	encoding.isPrefix / encoding.ValidateDictPrefixRule semantics for
//	sharded cohorts). When ANY position diverges the probe fails for
//	that target with the offending field name and the canonical
//	prefix strings (entries joined "|" up to the divergence point —
//	see canonicalAxisPrefix).
//
// Details payload (encoding/json-friendly):
//
//   - "code"                  → string(PULSE_OVERLAY_DICT_PREFIX_DRIFT)
//   - "index"                 → spec index
//   - "reference"             → reference slot label (from spec.Reference)
//   - "target_label"          → diverging target slot label
//   - "field"                 → the axis-name (or "row_axis" / "column_axis"
//     when the AxisHeader.Fields slot is empty)
//   - "reference_dict_prefix" → canonical dictionary prefix string for
//     the reference slot (axis keys joined "|"
//     up to the divergence point)
//   - "target_dict_prefix"    → same for the target slot
//
// "First divergence wins" matches the existing key-set / schema
// gates' single-fail behaviour — the orchestrator stops at the first
// coded error rather than batching every divergent target into one
// payload.
func checkDictPrefixEquality(refResp *types.Response, targetResps []*types.Response, spec types.ComposeOverlaySpec, specIdx int) error {
	// Defensive guards: no reference, no targets, or reference is
	// non-MATRIX (no per-axis dictionary to compare against) all
	// degenerate to no-ops. The shape / schema gates upstream have
	// already enforced that reference and target host shapes agree
	// so the explicit shape check here is belt-and-braces.
	if refResp == nil || refResp.Crosstab == nil || refResp.Crosstab.Matrix == nil {
		return nil
	}
	refMx := refResp.Crosstab.Matrix
	for i, tResp := range targetResps {
		if tResp == nil || tResp.Crosstab == nil || tResp.Crosstab.Matrix == nil {
			// Mixed-shape pairs are owned by the shape gate (E7-S7);
			// stay orthogonal so the canonical-code dispatch does not
			// double-trigger.
			continue
		}
		tMx := tResp.Crosstab.Matrix
		targetLabel := ""
		if i < len(spec.Targets) {
			targetLabel = spec.Targets[i]
		}
		// Row axis prefix probe.
		if err := compareAxisPrefix(
			refMx.RowKeys, tMx.RowKeys,
			axisFieldName(refMx.RowHeader, "row_axis"),
			spec, specIdx, targetLabel,
		); err != nil {
			return err
		}
		// Column axis prefix probe.
		if err := compareAxisPrefix(
			refMx.ColumnKeys, tMx.ColumnKeys,
			axisFieldName(refMx.ColumnHeader, "column_axis"),
			spec, specIdx, targetLabel,
		); err != nil {
			return err
		}
	}
	return nil
}

// compareAxisPrefix is the per-axis dictionary-prefix probe entry
// point. Asserts that for every index i < min(len(ref), len(target)),
// the canonical string form of ref[i] equals the canonical string
// form of target[i]. Returns a coded error on the first divergence;
// nil on full agreement.
func compareAxisPrefix(ref, target []types.AxisKey, fieldName string, spec types.ComposeOverlaySpec, specIdx int, targetLabel string) error {
	common := len(ref)
	if len(target) < common {
		common = len(target)
	}
	for i := 0; i < common; i++ {
		refStr := axisKeyToString(ref[i])
		tgtStr := axisKeyToString(target[i])
		if refStr == tgtStr {
			continue
		}
		// Divergence at position i. Emit the canonical prefix strings
		// up to AND INCLUDING the divergence so renderers can pinpoint
		// the offending entry in the dictionary diff payload.
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay dict prefix drift: reference and target slots carry categorical dictionaries that do not share a byte-equal common prefix",
			map[string]any{
				"code":                  string(errors.PULSE_OVERLAY_DICT_PREFIX_DRIFT),
				"index":                 specIdx,
				"reference":             spec.Reference,
				"target_label":          targetLabel,
				"field":                 fieldName,
				"reference_dict_prefix": canonicalAxisPrefix(ref, i+1),
				"target_dict_prefix":    canonicalAxisPrefix(target, i+1),
			})
	}
	return nil
}

// canonicalAxisPrefix renders the first `n` entries of an AxisKey
// sequence into a deterministic "|"-joined canonical string for the
// Details payload. Uses axisKeyToString per-entry so the rendering
// stays consistent with the rest of the chassis-side lookup keys.
// Empty input → "" so the payload still encodes deterministically.
func canonicalAxisPrefix(keys []types.AxisKey, n int) string {
	if n <= 0 || len(keys) == 0 {
		return ""
	}
	if n > len(keys) {
		n = len(keys)
	}
	out := axisKeyToString(keys[0])
	for i := 1; i < n; i++ {
		out += "|" + axisKeyToString(keys[i])
	}
	return out
}

// axisFieldName returns the first declared field name on an
// AxisHeader, falling back to the supplied default when the header
// has no Fields slot populated. Used so the Details payload carries
// the renderer-facing field identifier when available (matching how
// the rest of the schema-match / key-set gates surface field
// identifiers) and a stable "row_axis" / "column_axis" placeholder
// otherwise.
func axisFieldName(header types.AxisHeader, fallback string) string {
	if len(header.Fields) == 0 {
		return fallback
	}
	return header.Fields[0]
}
