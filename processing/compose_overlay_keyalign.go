package processing

import (
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Strict cross-Request key alignment for the COMPOSE-host overlay
// dispatch — the cheapest signal the chassis can fail on before any
// per-kind handler executes.
//
// Compose-only overlays fold target slot values against a reference
// slot's value at byte-equal per-coordinate keys. The renderer-facing
// contract is "every target cell / series entry sits at a coordinate
// that also exists on the reference, and vice versa" — divergent key
// sets cannot be folded byte-equal and the chassis rejects them loud
// rather than silently dropping the surplus entries. Tolerant
// alignment is an explicit v1 non-goal (interview "Resolved Decisions
// / Cross-Request key alignment"); callers needing it pre-align their
// inputs or fall back to a multi-reference kind.
//
// This file lands the key-set extraction + comparison helper invoked
// from ApplyComposeOverlays AFTER the slot-label resolver returns the
// reference / target *Response values but BEFORE the per-kind handler
// dispatch. Predicate ordering is intentional — key-set divergence is
// O(slots × cells) and computes per spec, so we fail fast before the
// per-kind handlers walk the matrix / series payload again. Schema
// match (E7-S7) and dict-drift (E7-S8) gates run AFTER this one.
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
//   - Key encoding reuses the canonical helpers shared with the
//     CHAIN-host equivalent (axisKeyToString / encodeSeriesRow from
//     overlay_index_vs_stage.go) so callers that compare keys across
//     CHAIN and COMPOSE — e.g. shape-divergence diagnostics — get
//     byte-equal forms across both hosts.
//
// Codes:
//
//   - PULSE_OVERLAY_KEY_SET_DIVERGENT (errors.codes.go): emitted when
//     ANY target's key set diverges from the reference's. Details
//     carry { reference, target, missing, extra } — `missing` lists
//     coordinates present on the reference but absent from the
//     target, `extra` lists coordinates present on the target but
//     absent from the reference, both sorted for deterministic
//     output. The minimal codeMetadata entry lands in this story;
//     E7-S13 polishes the Message + Fixup catalogue.

// composeOverlayKeySet is the per-slot key set extracted from a
// *Response. The set values are the canonical per-coordinate strings
// produced by encodeComposeMatrixKey / encodeComposeSeriesKey; the
// `shape` field carries the OverlayShape the chassis would have
// otherwise inferred (matrix / series / scalar), preserved so the
// caller-side divergence check can short-circuit when both slots
// agree on SCALAR (no coordinate grid to align).
type composeOverlayKeySet struct {
	shape types.OverlayShape
	keys  map[string]struct{}
}

// extractKeySet builds the per-coordinate key set the strict
// cross-Request alignment check consults. The shape is inferred via
// the same precedence rule the stub handler uses
// (inferComposeStubShape): matrix when the response carries a
// non-nil Crosstab.Matrix, series when it carries non-empty Data,
// scalar otherwise. The returned set is empty under SCALAR shape —
// scalar slots have no coordinate grid, so the alignment check
// degenerates to a no-op.
//
// Matrix arm: walk the dense [rows × cols] grid of MatrixCell
// positions. The keys are the (rowKey, colKey) tuples encoded
// via encodeComposeMatrixKey — every position in the dense grid
// participates regardless of MatrixCell.Present, because the
// renderer's "this coordinate exists on this slot" contract is
// structural (the row × column tuple is declared on the axis even
// when the cell is absent). Cell-presence divergence is a SEPARATE
// degeneracy the per-kind handler decides how to handle (e.g.
// missing-cell warning); the chassis-side key-set check is purely
// structural.
//
// Series arm: walk `Response.Data` rows and encode the canonical
// row-key string via the same encodeSeriesRow helper the
// CHAIN-host INDEX_VS_STAGE / DELTA_VS_STAGE handlers use. The key
// drops the value column from the encoding so two slots that
// produce different aggregator outputs at the same group key
// still match (the renderer compares values inside the handler,
// not in the chassis-side gate). Empty Data → empty set, which is
// only structurally diverged from a non-empty set; the chassis
// surfaces that the same way as any other divergence.
//
// Returns SCALAR / nil-keys for nil *Response — defensive guard so
// callers that reach this helper without going through the slot
// resolver (descriptor predict at E7-S14) still get a well-formed
// short-circuit.
func extractKeySet(resp *types.Response) composeOverlayKeySet {
	if resp == nil {
		return composeOverlayKeySet{shape: types.OverlayShapeScalar}
	}
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		return composeOverlayKeySet{
			shape: types.OverlayShapeMatrix,
			keys:  extractMatrixKeySet(resp.Crosstab.Matrix),
		}
	}
	if len(resp.Data) > 0 {
		return composeOverlayKeySet{
			shape: types.OverlayShapeSeries,
			keys:  extractSeriesKeySet(resp.Data),
		}
	}
	return composeOverlayKeySet{shape: types.OverlayShapeScalar}
}

// extractMatrixKeySet walks the matrix's declared (RowKeys × ColumnKeys)
// grid and produces one canonical-string key per (row, col) tuple.
// Uses axisKeyToString — the same helper the CHAIN-host matrix
// dispatch consumes — so two slots carrying the same composite axis
// tuple always produce byte-equal string keys regardless of whether
// the axis values came from the in-process grouper (int) or a JSON
// round-trip (float64).
func extractMatrixKeySet(mx *types.MatrixPayload) map[string]struct{} {
	if mx == nil {
		return nil
	}
	out := make(map[string]struct{}, len(mx.RowKeys)*len(mx.ColumnKeys))
	for _, rk := range mx.RowKeys {
		rowStr := axisKeyToString(rk)
		for _, ck := range mx.ColumnKeys {
			out[encodeComposeMatrixKey(rowStr, axisKeyToString(ck))] = struct{}{}
		}
	}
	return out
}

// extractSeriesKeySet walks Response.Data rows and produces one
// canonical-string key per row. Uses encodeSeriesRow's key-only
// output (the value column is folded into the key when more than
// one numeric column exists; the FIRST sorted numeric column is the
// "value" and dropped from the key) so two slots carrying the same
// grouped Process result produce byte-equal key sets regardless of
// the aggregator value at each row.
//
// Empty rows produce empty strings; degenerate slots with no
// non-value columns (single AGG_SUM with no Groups, etc.) still
// produce a single empty-string key, which the divergence check
// surfaces structurally if the other slot carries non-empty keys.
func extractSeriesKeySet(rows []map[string]any) map[string]struct{} {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		key, _, _, _ := encodeSeriesRow(row)
		out[key] = struct{}{}
	}
	return out
}

// encodeComposeMatrixKey joins the canonical row + column string
// halves into the per-cell tuple key the matrix-arm divergence
// check consumes. The pipe separator matches the axisKeyToString
// convention for multi-grouper axes; the doubled "||" between
// row and column halves visually separates the two axis halves
// when the per-axis key happens to contain a pipe in its own
// canonical form. The exact separator never lands in the
// envelope — it stays inside the chassis-side lookup table — so
// the choice is purely a debug-readability concern.
func encodeComposeMatrixKey(rowStr, colStr string) string {
	return rowStr + "||" + colStr
}

// checkKeySetAlignment is the entry point the compose dispatch calls
// per spec. Walks `targetResps` against `refResp`, computes the
// symmetric difference of each target's key set vs the reference's,
// and returns the first divergence as a coded error carrying
// PULSE_OVERLAY_KEY_SET_DIVERGENT in Details.
//
// Both sides SCALAR ⇒ no coordinate grid exists, the check
// degenerates to a no-op (returns nil). Mixed-shape pairs (one
// MATRIX, one SERIES) are NOT a key-set divergence — that is a
// structural shape mismatch and the per-kind handler's shape gate
// (the E7-S7 schema-match story) owns rejecting it; the chassis-side
// key-set check stays purely on the within-shape coordinate grid so
// the canonical-code dispatch stays orthogonal.
//
// Details payload (encoding/json-friendly):
//
//   - "reference" → slot label string passed via spec.Reference.
//   - "target"    → slot label string for the diverging target.
//   - "missing"   → []string of keys present on the reference but
//     absent from the target, sorted lexicographically
//     for deterministic output.
//   - "extra"     → []string of keys present on the target but
//     absent from the reference, sorted
//     lexicographically.
//   - "code"      → string(errors.PULSE_OVERLAY_KEY_SET_DIVERGENT)
//     echoed so the dispatch parser can branch on the
//     canonical code without re-parsing the message.
//   - "index"     → spec index (-1 when the caller does not know it,
//     E7-S14 predict path passes the real value).
//
// The "first divergence wins" rule matches the existing
// LookupReference / LookupTarget single-fail behaviour — the
// orchestrator stops dispatching at the first coded error rather
// than batching every divergent target into one warning payload.
// Multi-target reporting is a future surface (predict-time
// validation can list every divergent target at once); the runtime
// stops at the first to keep the failure mode mechanically simple
// for fix-up authors.
func checkKeySetAlignment(refResp *types.Response, targetResps []*types.Response, spec types.ComposeOverlaySpec, specIdx int) error {
	refSet := extractKeySet(refResp)
	if refSet.shape == types.OverlayShapeScalar {
		// Reference is scalar — no coordinate grid to align against.
		// The check is a no-op regardless of the target shapes.
		return nil
	}
	for i, tResp := range targetResps {
		tSet := extractKeySet(tResp)
		if tSet.shape == types.OverlayShapeScalar {
			// Target is scalar against a non-scalar reference. Shape
			// mismatch is owned by the E7-S7 schema-match gate; the
			// key-set check stays orthogonal and returns nil here so
			// the canonical-code dispatch does not double-trigger.
			continue
		}
		missing, extra := symmetricKeyDifference(refSet.keys, tSet.keys)
		if len(missing) == 0 && len(extra) == 0 {
			continue
		}
		targetLabel := ""
		if i < len(spec.Targets) {
			targetLabel = spec.Targets[i]
		}
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay key set divergent: reference and target produce different per-coordinate key sets",
			map[string]any{
				"code":      string(errors.PULSE_OVERLAY_KEY_SET_DIVERGENT),
				"index":     specIdx,
				"reference": spec.Reference,
				"target":    targetLabel,
				"missing":   missing,
				"extra":     extra,
			})
	}
	return nil
}

// symmetricKeyDifference returns the sorted symmetric difference of
// two key sets — `missing` lists keys present on `ref` but absent
// from `target`, `extra` lists keys present on `target` but absent
// from `ref`. Both halves are sorted lexicographically so the
// envelope payload is deterministic for golden-file checks.
//
// Returns (nil, nil) when the two sets are key-equivalent. The
// caller short-circuits on that signal rather than allocating empty
// slices into the Details payload.
func symmetricKeyDifference(ref, target map[string]struct{}) (missing, extra []string) {
	for k := range ref {
		if _, ok := target[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range target {
		if _, ok := ref[k]; !ok {
			extra = append(extra, k)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil, nil
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
