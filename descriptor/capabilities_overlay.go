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

	// Fields lists the OverlaySpec slot names beyond Kind / Scope /
	// Ref / Params that the kind's runtime honours. Sorted
	// alphabetically for golden stability. E2-S11 introduces "level"
	// and "within" — the share / index / delta / zscore family honours
	// both (prefix-axis denominator dispatch) while the χ² / Fisher
	// inferential family leaves the list empty (Level / Within must
	// stay zero; non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE).
	// SHARE_OF_TOTAL declares the fields for renderer-facing parity
	// with the rest of the share family even though the grand-axis
	// denominator makes the slots inert at runtime — the predict gate
	// still accepts in-range values without behavior change.
	Fields []string `json:"fields,omitempty"`

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
		sort.Strings(entry.Fields)
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
	case types.OverlayKindChiSqCol:
		return OverlayCapability{
			Kind: types.OverlayKindChiSqCol,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeColumn,
			},
			// No Ref family — the per-column χ² goodness-of-fit test is
			// implicit-margin (uses the host's row / column / grand
			// margins inline). Callers supplying any Ref family pointer
			// fail PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict
			// time.
			RefKinds: []string{},
			Description: "Per-column χ² goodness-of-fit test across the host crosstab's row distribution. " +
				"COLUMN scope over a MATRIX (crosstab) host with SERIES payload — one entry per column tuple " +
				"carrying the column's χ² statistic, degrees of freedom (rows - 1), and p-value via " +
				"OverlaySummary{Statistic, PValue, Parameters{\"df\"}}. Mechanical column-axis twin of " +
				"OVERLAY_CHISQ_ROW; mirrors the SeriesPayload entries plumbing pattern (Entries[i].Key == " +
				"host ColumnKeys[i] element-for-element). The Ref union is left empty (implicit-margin). " +
				"Reuses the χ² survival helper backing TEST_CHISQ. Emits PULSE_OVERLAY_EXPECTED_LOW once " +
				"per offending column when any expected cell value in the column is below 5.",
		}
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
	case types.OverlayKindChiSqRow:
		return OverlayCapability{
			Kind: types.OverlayKindChiSqRow,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeRow,
			},
			// No Ref family — the per-row χ² goodness-of-fit test is
			// implicit-margin (uses the host's row / column / grand
			// margins inline). Callers supplying any Ref family pointer
			// fail PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict
			// time.
			RefKinds: []string{},
			Description: "Per-row χ² goodness-of-fit test across the host crosstab's column distribution. " +
				"ROW scope over a MATRIX (crosstab) host with SERIES payload — one entry per row tuple " +
				"carrying the row's χ² statistic, degrees of freedom (cols - 1), and p-value via " +
				"OverlaySummary{Statistic, PValue, Parameters{\"df\"}}. First SERIES-shape Crosstab overlay; " +
				"establishes the SeriesPayload entries plumbing pattern. The Ref union is left empty " +
				"(implicit-margin). Reuses the χ² survival helper backing TEST_CHISQ. Emits " +
				"PULSE_OVERLAY_EXPECTED_LOW once per offending row when any expected cell value in the row " +
				"is below 5.",
		}
	case types.OverlayKindDeltaVsBaseline:
		return OverlayCapability{
			Kind: types.OverlayKindDeltaVsBaseline,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// BaselineIndex is the windowed positional-anchor ref family
			// (E4-S1 foundation; E4-S3 consumer, sibling to E4-S2
			// INDEX_VS_BASELINE). The capability row declares the consumed
			// Ref-arm so MCP / manifest clients see the kind requires
			// Ref.BaselineIndex.Position to be populated; the per-kind
			// validator (descriptor.validateOverlayDeltaVsBaseline) gates
			// the shape at predict time.
			RefKinds: []string{"BaselineIndex"},
			Description: "Per-point additive delta against a single fixed positional baseline of an ordered SERIES " +
				"(grouped Process) host: point_value - baseline_value. GROUP scope over a SERIES host with " +
				"SERIES payload — one SeriesEntry per host group key in host order, each carrying the delta on " +
				"Summary.Statistic. Absolute-difference sibling of OVERLAY_INDEX_VS_BASELINE (E4-S2) and third " +
				"windowed-Process kind in the catalog (E4-S3). Like its sibling it consumes the " +
				"Ref.BaselineIndex.Position arm of the OverlayBaselineIndexRef union (E4-S1 foundation). The " +
				"baseline is resolved ONCE up front via processing.ResolveBaselineIndex and every present point " +
				"subtracts it. The first present point at the baseline ordinal yields 0.0 (self-vs-self). Output " +
				"preserves the host cell's units — a $-valued AGG_SUM point minus a $-valued baseline yields a " +
				"$-valued deviation in the same currency (mirrors OVERLAY_DELTA_VS_MARGIN / OVERLAY_DELTA_VS_SIBLING). " +
				"Unlike the OVERLAY_INDEX_VS_BASELINE twin (which divides by the baseline and rejects zero with " +
				"PULSE_OVERLAY_REF_ZERO), DELTA_VS_BASELINE performs subtraction and is mathematically defined for " +
				"every finite baseline value including zero — the handler does NOT emit PULSE_OVERLAY_REF_ZERO; a " +
				"zero baseline simply yields delta = point - 0 = point (the raw host value passes through). " +
				"Negative or out-of-range Position values fire PULSE_OVERLAY_REF_UNKNOWN at both predict and " +
				"runtime with {baseline_index, series_length} Details. Absent host points (resolver reports " +
				"(0, false)) emit a present SeriesEntry whose Summary leaves Statistic unset. Ref.BaselineIndex " +
				"MUST be populated; any other ref-family pointer (Margin / Sibling / Prior / Population / Stage / " +
				"Slot) fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Level / Within MUST be zero (the baseline " +
				"is a single fixed positional anchor, not an axis prefix); non-zero values fire " +
				"PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the INDEX_VS_BASELINE / INDEX_VS_PRIOR / INDEX_VS_TOTAL " +
				"family. Buffered — resolving a single positional baseline requires the materialised host series. " +
				"Renderers centre diverging colour ramps on baseline=0 (mirrors OVERLAY_DELTA_VS_MARGIN / " +
				"OVERLAY_DELTA_VS_SIBLING / OVERLAY_ZSCORE_VS_*).",
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
			Fields:   []string{"level", "within"},
			Description: "Per-cell additive delta against the matching axis margin: cell - margin. " +
				"CELL scope over a MATRIX (crosstab) host. Supports all three margin axes " +
				"(row / column / grand). Output preserves the host cell's units — a $-valued " +
				"cell minus a $-valued margin yields a $-valued deviation. No division and " +
				"no Welford recurrence, so PULSE_OVERLAY_REF_ZERO is never emitted; renderers " +
				"centre diverging colour ramps on baseline=0. Honours OverlaySpec Level / " +
				"Within slots for nested-axis denominator truncation (E2-S11).",
		}
	case types.OverlayKindDeltaVsSibling:
		return OverlayCapability{
			Kind: types.OverlayKindDeltaVsSibling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"Sibling"},
			Description: "Per-group additive delta against a sibling group named in Ref.Sibling: group_val - sibling_val per host group key. " +
				"GROUP scope over a SERIES (grouped Process) host with SERIES payload — one SeriesEntry per host group key in host order, each " +
				"carrying the delta on Summary.Statistic. The sibling is identified by (Field, Value): Field names a grouper Field on the host, " +
				"Value names the specific axis-key value to compare against. The sibling group itself emits 0 (self-vs-self under additive " +
				"subtraction). Buffered (sibling resolution requires the full materialised SeriesPayload — the streaming Process pass cannot " +
				"resolve a (Field, Value) lookup against the per-group accumulators until they are finalised). Output preserves the host cell's " +
				"units — a $-valued AGG_SUM group minus a $-valued sibling AGG_SUM group yields a $-valued deviation in the same currency. " +
				"Unknown sibling (Field not on host OR Value not observed) emits PULSE_OVERLAY_REF_UNKNOWN with NaN statistics across every " +
				"present entry. DELTA does NOT emit PULSE_OVERLAY_REF_ZERO when sibling resolves to a zero value — subtraction by zero is well-" +
				"defined and just recovers the host's raw value. Absent host groups surface a present SeriesEntry whose Summary leaves Statistic " +
				"unset and do NOT participate in the delta computation. Renderers centre diverging colour ramps on baseline=0.",
		}
	case types.OverlayKindFisherExactCell:
		return OverlayCapability{
			Kind: types.OverlayKindFisherExactCell,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			// No Ref family — the per-cell 2×2 Fisher's exact test is
			// implicit-margin (uses the host's row + column margins +
			// grand total inline). Callers supplying any Ref family
			// pointer fail PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
			// at predict time. Mirrors the CHISQ_* implicit-margin
			// contract.
			RefKinds: []string{},
			Description: "Per-cell Fisher's exact two-sided test against a 2×2 contingency table built from " +
				"the host cell, its row margin, its column margin, and the grand total. CELL scope over a " +
				"MATRIX (crosstab) host with MATRIX payload — each cell's value is the exact two-sided " +
				"p-value as a float64. The Ref union is left empty (implicit-margin: row + col margins " +
				"resolve from the buffered crosstab host view). Reuses the lgamma-backed hypergeometric " +
				"primitive backing TEST_FISHER_EXACT (processing/test_fisher.go) via the shared " +
				"fisherExactTwoSided helper. Canonical low-count contingency overlay (PRD § 4.C FR-C2) — " +
				"emits PULSE_OVERLAY_EXPECTED_LOW per cell when the Cochran rule fires on the 2×2 (any " +
				"expected < 1 OR ≥ 20% of expected counts < 5), flagging cells where the cheaper χ² " +
				"approximation would be unreliable and Fisher's exact is structurally required.",
		}
	case types.OverlayKindIndexVsBaseline:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsBaseline,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// BaselineIndex is the windowed positional-anchor ref family
			// (E4-S1 foundation; E4-S2 first consumer). The capability row
			// declares the consumed Ref-arm so MCP / manifest clients see
			// the kind requires Ref.BaselineIndex.Position to be populated;
			// the per-kind validator
			// (descriptor.validateOverlayIndexVsBaseline) gates the shape
			// at predict time.
			RefKinds: []string{"BaselineIndex"},
			Description: "Per-point ratio index against a single fixed positional baseline of an ordered SERIES " +
				"(grouped Process) host: (point_value / baseline_value) * 100. GROUP scope over a SERIES host with " +
				"SERIES payload — one SeriesEntry per host group key in host order, each carrying the index on " +
				"Summary.Statistic. Second windowed-Process overlay in the catalog (E4-S2; the first windowed kind " +
				"was OVERLAY_INDEX_VS_PRIOR / E4-S4) and the first kind to consume the Ref.BaselineIndex.Position " +
				"arm of the OverlayBaselineIndexRef union (E4-S1 foundation). The baseline is resolved ONCE up " +
				"front via processing.ResolveBaselineIndex and every present point divides by it. The first present " +
				"point at the baseline ordinal yields 100.0 (self-vs-self). Zero baseline (resolved baseline_value " +
				"is 0 — includes the absent-baseline case where host.ValueAt returns (0, false) at the baseline " +
				"ordinal) emits ONE PULSE_OVERLAY_REF_ZERO warning carrying the baseline ordinal and the host " +
				"group count; every entry's Statistic is NaN. Negative or out-of-range Position values fire " +
				"PULSE_OVERLAY_REF_UNKNOWN at both predict and runtime with {baseline_index, series_length} " +
				"Details. Absent host points (resolver reports (0, false)) emit a present SeriesEntry whose " +
				"Summary leaves Statistic unset. Ref.BaselineIndex MUST be populated; any other ref-family " +
				"pointer (Margin / Sibling / Prior / Population / Stage / Slot) fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Level / Within MUST be zero (the baseline is a " +
				"single fixed positional anchor, not an axis prefix); non-zero values fire " +
				"PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the INDEX_VS_PRIOR / INDEX_VS_TOTAL family. " +
				"Buffered — resolving a single positional baseline requires the materialised host series. " +
				"Renderers centre diverging colour ramps on baseline=100.",
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
			Fields:   []string{"level", "within"},
			Description: "Per-cell index score against the matching axis margin: 100 * cell / margin. " +
				"E1 supports CELL scope over a MATRIX (crosstab) host with a Margin reference. " +
				"Honours OverlaySpec Level / Within slots for nested-axis denominator truncation " +
				"(E2-S11).",
		}
	case types.OverlayKindIndexVsPrior:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsPrior,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Prior is the windowed-axis lag-N ref family (E4-S4 ships
			// lag-1 only via Ref.Prior or an entirely empty Ref). Listed
			// in the capability surface so MCP / manifest clients see the
			// kind consumes the Prior arm even though the v1 authoring
			// shape can leave Ref entirely empty.
			RefKinds: []string{"Prior"},
			Description: "Per-point windowed index against the immediately preceding point of an ordered SERIES " +
				"(grouped Process) host: (point_value / prior_value) * 100. GROUP scope over a SERIES host with " +
				"SERIES payload — one SeriesEntry per host group key in host order, each carrying the index on " +
				"Summary.Statistic. First streamable windowed-Process overlay in the catalog (E4-S4) and the " +
				"first kind to consume the Ref.Prior arm of the OverlayRef discriminated union. The single-state " +
				"lag carrier is one f64 carried alongside the per-group accumulators inside the streaming Process " +
				"fold (no second pass over records); the post-host finalize is the divide step. First present " +
				"point emits NaN (no prior available — not a zero-denominator path, so no warning). Zero prior " +
				"value emits PULSE_OVERLAY_REF_ZERO with NaN on the affected entry. Absent host points emit a " +
				"present SeriesEntry whose Summary leaves Statistic unset and do NOT advance the lag carrier " +
				"— the next present point compares against the most recent PRESENT value. Ref accepts either " +
				"Ref.Prior (with Lag zero or unset for v1; non-zero Lag is reserved for future window-N priors) " +
				"or an entirely empty Ref (the implicit-default authoring shape); any other ref-family pointer " +
				"fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Level / Within MUST be zero (windowed kind — " +
				"the lag carrier folds across the ordered axis without a prefix-bucket denominator); non-zero " +
				"values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE. Renderers centre diverging colour ramps on " +
				"baseline=100.",
		}
	case types.OverlayKindIndexVsSibling:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsSibling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"Sibling"},
			Description: "Per-group ratio index against a sibling group named in Ref.Sibling: (group_val / sibling_val) * 100.0 per host group key. " +
				"GROUP scope over a SERIES (grouped Process) host with SERIES payload — one SeriesEntry per host group key in host order, each " +
				"carrying the index on Summary.Statistic. The sibling is identified by (Field, Value): Field names a grouper Field on the host, " +
				"Value names the specific axis-key value to compare against. The sibling group itself emits 100.0 (self-vs-self under the ratio " +
				"scaling). Buffered (sibling resolution requires the full materialised SeriesPayload — the streaming Process pass cannot resolve " +
				"a (Field, Value) lookup against the per-group accumulators until they are finalised). Unknown sibling (Field not on host OR " +
				"Value not observed) emits PULSE_OVERLAY_REF_UNKNOWN with NaN statistics across every present entry. Zero sibling value (legitimate " +
				"group with a zero post-filter sum) emits PULSE_OVERLAY_REF_ZERO with NaN statistics — division by zero is undefined and the same " +
				"PULSE_OVERLAY_REF_ZERO contract used by the SERIES INDEX_VS_TOTAL / SHARE_OF_TOTAL kinds applies. Absent host groups surface a " +
				"present SeriesEntry whose Summary leaves Statistic unset and do NOT participate in the index computation. Renderers centre " +
				"diverging colour ramps on baseline=100.",
		}
	case types.OverlayKindIndexVsTotal:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsTotal,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// No Ref family — INDEX_VS_TOTAL is implicit-grand-total (the
			// denominator is the host series' own grand total). Callers
			// supplying any Ref family pointer (Margin / Sibling /
			// BaselineIndex / Population / Stage / Slot) fail
			// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time —
			// mirrors the CHISQ_* implicit-margin contract.
			RefKinds: []string{},
			Description: "Per-group index score against the grand total of a SERIES (grouped Process) host: " +
				"(group_val / grand_total) * 100.0 per host group key. GROUP scope over a SERIES host " +
				"with SERIES payload — one SeriesEntry per host group key in host order, each carrying " +
				"the index on Summary.Statistic. First streamable overlay in the catalog — the grand-total " +
				"accumulator is one f64 carried alongside the per-group accumulators inside the streaming " +
				"Process fold (no second pass over records); the post-host finalize divides each group " +
				"value by the running grand total. The Ref union is left empty (implicit-grand-total); " +
				"zero grand_total emits PULSE_OVERLAY_REF_ZERO with NaN statistics. Absent host groups " +
				"surface a present SeriesEntry whose Summary leaves Statistic unset.",
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
			Fields:   []string{"level", "within"},
			Description: "Per-cell share-of-column ratio: cell / col_margin. CELL scope over a MATRIX " +
				"(crosstab) host. Column cells sum to 1.0 in the absence of missing cells; renderers " +
				"can present the layer as a 100%-stacked vertical projection. Honours OverlaySpec " +
				"Level / Within slots for nested-axis denominator truncation (E2-S11).",
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
			Fields:   []string{"level", "within"},
			Description: "Per-cell share-of-row ratio: cell / row_margin. CELL scope over a MATRIX " +
				"(crosstab) host. Row cells sum to 1.0 in the absence of missing cells; renderers " +
				"can present the layer as a 100%-stacked horizontal projection. Honours OverlaySpec " +
				"Level / Within slots for nested-axis denominator truncation (E2-S11).",
		}
	case types.OverlayKindShareOfTotal:
		return OverlayCapability{
			Kind: types.OverlayKindShareOfTotal,
			Shapes: []types.OverlayShape{
				// MATRIX (E2-S3): per-cell ratio cell / grand_total
				// against a crosstab host.
				types.OverlayShapeMatrix,
				// SERIES (E3-S3): per-group ratio group_val /
				// grand_total against a grouped Process host.
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// CELL: paired with the MATRIX shape on a crosstab host.
				types.OverlayScopeCell,
				// GROUP: paired with the SERIES shape on a grouped
				// Process host (E3-S3).
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"Margin"},
			Fields:   []string{"level", "within"},
			Description: "Share-of-grand-total ratio. Dual-shape overload: MATRIX dispatch (E2-S3) emits per-cell " +
				"cell / grand_total against a CELL-scoped crosstab host, completing the matrix share triad " +
				"(row / col / total) — the spec must populate Ref.Margin (grand-axis-locked even though the " +
				"handler ignores the axis value) and declares the level / within fields for renderer-facing parity " +
				"with the rest of the share family (the grand-axis denominator makes both slots inert at runtime, " +
				"E2-S11). SERIES dispatch (E3-S3) emits per-group group_val / grand_total against a GROUP-scoped " +
				"grouped Process host with scale 1.0 (no ×100 — sibling to INDEX_VS_TOTAL but with the SHARE " +
				"scaling so cells over a complete partition sum to 1.0 within ULP) — Ref MUST be empty " +
				"(implicit-grand-total). Sibling SERIES kind to OVERLAY_INDEX_VS_TOTAL — shares the same " +
				"computeSeriesGrandTotal accumulator so a request carrying both overlays folds the grand-total " +
				"only once in the streaming pass. Zero grand_total emits PULSE_OVERLAY_REF_ZERO with NaN " +
				"statistics on every present entry; absent host groups surface a present SeriesEntry whose " +
				"Summary leaves Statistic unset. Streamable via the SERIES dispatch (the MATRIX route is forced " +
				"buffered through canFuseCrosstab's overlays-force-buffered arm).",
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
			Fields:   []string{"level", "within"},
			Description: "Per-cell standardized-margin z-score: (cell - margin) / sd where sd is the " +
				"population standard deviation of cell values within the same margin slice " +
				"(per-row cells for axis=row, per-column cells for axis=column, every matrix cell " +
				"for axis=grand). CELL scope over a MATRIX (crosstab) host. Supports all three " +
				"margin axes (row / column / grand). Output is unitless deviation; renderers centre " +
				"diverging colour ramps on baseline=0. The margin centroid honours OverlaySpec " +
				"Level / Within slots for nested-axis denominator truncation; the SD denominator " +
				"continues to fold over the full per-axis slice (E2-S11).",
		}
	case types.OverlayKindZScoreVsTotal:
		return OverlayCapability{
			Kind: types.OverlayKindZScoreVsTotal,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// No Ref family — ZSCORE_VS_TOTAL is implicit-grand-total (the
			// centerpoint is the host series' own grand-total mean).
			// Callers supplying any Ref family pointer (Margin / Sibling /
			// BaselineIndex / Population / Stage / Slot) fail
			// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time —
			// mirrors the INDEX_VS_TOTAL / SHARE_OF_TOTAL SERIES contract.
			RefKinds: []string{},
			Description: "Per-group standardized z-score against the host series' grand-total distribution: " +
				"(group_val - mean) / sd per host group key, where mean = Σ group_val / N and " +
				"sd = sqrt(M2 / N) (population variance) folded across the N present per-group " +
				"aggregated values. GROUP scope over a SERIES (grouped Process) host with SERIES " +
				"payload — one SeriesEntry per host group key in host order, each carrying the " +
				"z-score on Summary.Statistic. Third and final streamable overlay in the E3 " +
				"grouped-Process subset (sibling to OVERLAY_INDEX_VS_TOTAL and the SERIES dispatch " +
				"of OVERLAY_SHARE_OF_TOTAL) — the Welford accumulator (count + mean + M2) is three " +
				"f64s carried alongside the per-group accumulators inside the streaming Process " +
				"fold (no second pass over records); the post-host finalize emits " +
				"(group_val - mean) / sd per group. Population variance (not sample) because " +
				"_VS_TOTAL implies the host's per-group aggregation set IS the whole population " +
				"being standardised. The Ref union is left empty (implicit-grand-total); " +
				"zero variance (every present group equal, or only a single present group) emits " +
				"PULSE_OVERLAY_REF_ZERO with NaN statistics on every present entry. Absent host " +
				"groups surface a present SeriesEntry whose Summary leaves Statistic unset and do " +
				"NOT contribute to the Welford accumulator. Renderers centre diverging colour " +
				"ramps on baseline=0.",
		}
	}
	return OverlayCapability{Kind: kind}
}
