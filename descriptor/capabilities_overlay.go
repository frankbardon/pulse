package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/types"
)

// Overlay capability surface — manifest-bound declaration of every
// registered OverlayKind, the shapes / scopes / ref kinds the kind
// supports, and whether it forces the orchestrator down the buffered
// path. The manifest builder (E1-S10) calls OverlayCapabilities() and
// drops the returned slice into Manifest.Overlays so LLM clients can
// detect the catalog without inspecting the source.
//
// Per kind-catalog-v1 PRD §I-FR-I2, each entry carries:
//
//   - Kind        — the OverlayKind constant (SCREAMING_SNAKE on wire).
//   - Shapes      — supported OverlayShape values (scalar/series/matrix).
//   - Scopes      — supported OverlayScope values (cell/row/column/...).
//   - RefKinds    — OverlayRef family pointers the kind consumes
//                   (e.g. "Margin", "Sibling", "BaselineIndex"); these
//                   are the Go field-name strings on OverlayRef, not
//                   on-wire MarginAxis values. The on-wire union
//                   discriminator lives one level deeper inside the
//                   chosen pointer.
//   - Buffered    — whether the orchestrator must materialise records
//                   before evaluating the kind. Derived from
//                   types.OverlayStreamable(kind) — buffered is the
//                   inverse of streamable.
//   - Description — short human-readable summary; mirrors the prose
//                   in skills/index-vs-margin (and successor skills as
//                   later kinds land).
//
// Structural invariants (CLAUDE.md "Predict / Inspect contracts" +
// "What NOT to Do"):
//
//   - This file MUST NOT import github.com/frankbardon/pulse/service
//     or github.com/frankbardon/pulse/processing. Overlay catalog data
//     lives in types/; capability lookups go through types/ constants.
//     TestPredictNoExecutionImports gates predict.go and the rest of
//     the descriptor package follows the same convention.
//   - No fmt.Sprintf in any JSON-bearing path. The OverlayCapability
//     struct is marshalled by the manifest builder through
//     encoding/json; no string formatting happens in this file.

// OverlayCapability is the per-kind manifest entry describing one
// registered overlay catalog entry. The manifest carries one
// OverlayCapability per types.AllOverlayKinds() entry under
// Manifest.Overlays (wired in E1-S10).
//
// Field shape mirrors the kind-catalog-v1 PRD §I-FR-I2 contract:
// kind × supported shapes × supported scopes × valid ref kinds ×
// buffered flag × description.
type OverlayCapability struct {
	// Kind is the on-wire SCREAMING_SNAKE OverlayKind value.
	Kind types.OverlayKind `json:"kind"`

	// Shapes lists every OverlayShape this kind may emit. Sorted
	// alphabetically for golden stability.
	Shapes []types.OverlayShape `json:"shapes"`

	// Scopes lists every OverlayScope this kind supports. Sorted
	// alphabetically for golden stability.
	Scopes []types.OverlayScope `json:"scopes"`

	// RefKinds lists the OverlayRef union pointer-field names this
	// kind consumes (e.g. "Margin"). Sorted alphabetically for golden
	// stability. These are Go field-name strings — the on-wire
	// discriminator (MarginAxis, sibling field, etc.) lives one level
	// deeper inside the chosen pointer.
	RefKinds []string `json:"ref_kinds"`

	// Buffered reports whether the orchestrator must materialise
	// records before evaluating this kind. Derived from
	// types.OverlayStreamable(kind) — Buffered is !streamable.
	Buffered bool `json:"buffered"`

	// Description is a short human-readable summary of what the kind
	// computes. Mirrors the prose in the matching skill.
	Description string `json:"description"`
}

// OverlayCapabilities returns the canonical per-kind capability list
// for the manifest. Deterministic ordering: alphabetised by Kind via
// types.AllOverlayKinds() so the golden manifest stays stable as new
// kinds land. Buffered flags consult types.OverlayStreamable(kind) so
// the static table in types/overlay_streamability.go remains the
// single source of truth for streaming behaviour — a kind that flips
// to streamable automatically flips Buffered to false here.
//
// E1 ships a single entry — OVERLAY_INDEX_VS_MARGIN — with:
//
//   - Shapes:   [matrix]   (CELL-scoped overlay layered onto a crosstab)
//   - Scopes:   [cell]     (E1 supports CELL only; ROW/COLUMN/TOTAL land
//                          alongside the matching payload shapes)
//   - RefKinds: [Margin]   (denominator is an axis-margin slot)
//   - Buffered: true       (margins recompute from raw rows; the host
//                          crosstab path is always buffered)
//
// Later kinds drop in by extending the switch below. The
// TestStreamability_OverlaysKnown gate (types/overlay_streamability.go)
// and TestManifestOperatorsComplete-style follow-ups (descriptor) will
// enforce that every catalog entry has a row in this surface.
func OverlayCapabilities() []OverlayCapability {
	kinds := types.AllOverlayKinds()
	caps := make([]OverlayCapability, 0, len(kinds))
	for _, k := range kinds {
		streamable, _ := types.OverlayStreamable(k)
		entry := overlayCapabilityFor(k)
		entry.Buffered = !streamable
		sort.Slice(entry.Shapes, func(i, j int) bool {
			return string(entry.Shapes[i]) < string(entry.Shapes[j])
		})
		sort.Slice(entry.Scopes, func(i, j int) bool {
			return string(entry.Scopes[i]) < string(entry.Scopes[j])
		})
		sort.Strings(entry.RefKinds)
		caps = append(caps, entry)
	}
	return caps
}

// overlayCapabilityFor returns the static per-kind capability shape
// (Shapes / Scopes / RefKinds / Description). Buffered is filled by
// OverlayCapabilities() from the streamability table so the static
// table stays the single source of truth for that bit.
//
// An unknown kind returns a zero-value entry; OverlayCapabilities()
// still records the Kind so the gap is visible in the manifest output
// rather than silently dropped — types.AllOverlayKinds() is the
// authoritative iteration surface, and the per-kind switch below is
// expected to grow in lock-step.
func overlayCapabilityFor(kind types.OverlayKind) OverlayCapability {
	switch kind {
	case types.OverlayKindChiSqMatrix:
		return OverlayCapability{
			Kind: types.OverlayKindChiSqMatrix,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeMatrix,
			},
			// No Ref family — the χ² test is implicit-margin (uses the
			// host's row / column / grand margins inline). Callers
			// supplying any Ref family pointer fail
			// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time.
			RefKinds: []string{},
			Description: "Whole-matrix χ² independence test across the host crosstab's row × column " +
				"contingency table. MATRIX scope over a MATRIX (crosstab) host with SCALAR payload — " +
				"the layer carries the chi-square statistic plus degrees of freedom and p-value via " +
				"OverlaySummary{Statistic, PValue, Parameters{\"df\"}}. First inferential overlay and " +
				"first SCALAR-shape Crosstab overlay; the Ref union is left empty (implicit-margin). " +
				"Reuses the χ² survival helper backing TEST_CHISQ. Emits " +
				"PULSE_OVERLAY_EXPECTED_LOW when any expected cell value is below 5.",
		}
	case types.OverlayKindDeltaVsMargin:
		return OverlayCapability{
			Kind: types.OverlayKindDeltaVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell additive delta against the matching axis margin: cell - margin. " +
				"CELL scope over a MATRIX (crosstab) host. Supports all three margin axes " +
				"(row / column / grand). Output preserves the host cell's units — a $-valued " +
				"cell minus a $-valued margin yields a $-valued deviation. No division and " +
				"no Welford recurrence, so PULSE_OVERLAY_REF_ZERO is never emitted; renderers " +
				"centre diverging colour ramps on baseline=0.",
		}
	case types.OverlayKindIndexVsMargin:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell index score against the matching axis margin: 100 * cell / margin. " +
				"E1 supports CELL scope over a MATRIX (crosstab) host with a Margin reference.",
		}
	case types.OverlayKindShareOfCol:
		return OverlayCapability{
			Kind: types.OverlayKindShareOfCol,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell share-of-column ratio: cell / col_margin. CELL scope over a MATRIX " +
				"(crosstab) host. Column cells sum to 1.0 in the absence of missing cells; renderers " +
				"can present the layer as a 100%-stacked vertical projection.",
		}
	case types.OverlayKindShareOfRow:
		return OverlayCapability{
			Kind: types.OverlayKindShareOfRow,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell share-of-row ratio: cell / row_margin. CELL scope over a MATRIX " +
				"(crosstab) host. Row cells sum to 1.0 in the absence of missing cells; renderers " +
				"can present the layer as a 100%-stacked horizontal projection.",
		}
	case types.OverlayKindShareOfTotal:
		return OverlayCapability{
			Kind: types.OverlayKindShareOfTotal,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell share-of-grand-total ratio: cell / grand_total. CELL scope over a MATRIX " +
				"(crosstab) host. The entire matrix sums to 1.0 in the absence of missing cells; renderers " +
				"can present the layer as a single-population share projection. Completes the share triad " +
				"(row / col / total).",
		}
	case types.OverlayKindZScoreVsMargin:
		return OverlayCapability{
			Kind: types.OverlayKindZScoreVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"Margin"},
			Description: "Per-cell standardized-margin z-score: (cell - margin) / sd where sd is the " +
				"population standard deviation of cell values within the same margin slice " +
				"(per-row cells for axis=row, per-column cells for axis=column, every matrix cell " +
				"for axis=grand). CELL scope over a MATRIX (crosstab) host. Supports all three " +
				"margin axes (row / column / grand). Output is unitless deviation; renderers centre " +
				"diverging colour ramps on baseline=0.",
		}
	}
	return OverlayCapability{Kind: kind}
}
