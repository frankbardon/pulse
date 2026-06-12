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
	case types.OverlayKindChiSqVsPop:
		return OverlayCapability{
			Kind: types.OverlayKindChiSqVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// (E5-S1 foundation; E5-S2 INDEX_VS_POP first consumer; E5-S3
			// ZSCORE_VS_POP second consumer; E5-S4 CHISQ_VS_POP third
			// consumer). The capability row declares the consumed Ref-arm
			// so MCP / manifest clients see the kind requires Ref.Population
			// to be populated; the per-kind validator
			// (descriptor.validateOverlayChiSqVsPop) lands in E5-S6 / E5-S7
			// (this story is the runtime handler only).
			RefKinds: []string{"Population"},
			Description: "Single scalar χ² goodness-of-fit statistic + p-value comparing the host Facet subset distribution " +
				"against the resolved population distribution. GROUP scope over a FACET (FacetSchema) host with SCALAR " +
				"payload — the layer carries the chi-square statistic plus degrees of freedom and the corresponding p-value " +
				"via OverlaySummary{Statistic, PValue, Parameters{\"df\"}}. Third FACET-host overlay in the catalog (E5-S4; " +
				"siblings: OVERLAY_INDEX_VS_POP / E5-S2, OVERLAY_ZSCORE_VS_POP / E5-S3) and the first inferential FACET-host " +
				"kind. Pairs with the MATRIX-host CHISQ family (CHISQ_MATRIX / CHISQ_ROW / CHISQ_COL) as the canonical χ² " +
				"family — the viz developer renders the SCALAR statistic as a goodness-of-fit badge near the facet header. " +
				"Math: subset_N = sum(host counts); expected[v] = pop_freq[v] * subset_N (population frequencies scaled to " +
				"subset N); chisq = Σ (observed - expected)² / expected; df = len(observed) - 1; p_value = " +
				"chiSquareSurvival(chisq, df) — the same χ² survival helper backing TEST_CHISQ and the MATRIX-host CHISQ " +
				"family, so the overlay surface produces identical p-values for the same contingency. Discrete arm only " +
				"(χ² goodness-of-fit requires categorical buckets — numeric hosts fire " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at runtime; the per-kind validator lands in E5-S6 / E5-S7 to " +
				"reject numeric hosts at predict time). Streaming finalize hook: the handler runs at the FacetSchema " +
				"post-host-finalize entry — once the per-value (value, count) map is folded into the FacetField.Discrete.Values " +
				"slice the overlay reads the already-finalized host distribution and the resolver's population view to emit " +
				"the SCALAR statistic. No second pass over records. INHERENTLY BUFFERED per PRD §2 Non-Goals (\"Streaming " +
				"overlay path for inferential kinds\") — inferential overlays as a family stay buffered regardless of host " +
				"streamability; the streamable subset is reserved for descriptive kinds (sibling streamable FACET-host kinds " +
				"are INDEX_VS_POP / ZSCORE_VS_POP, both descriptive). Low-expected-cell warning: when any expected count is " +
				"below 5 the handler emits ONE PULSE_OVERLAY_EXPECTED_LOW warning (canonical χ² low-count heuristic; the same " +
				"warning code the MATRIX-host CHISQ family and FISHER_EXACT_CELL emit — PRD FR-J1 shares the code with the " +
				"crosstab Fisher exact path). Absent-population value (host value missing from the population's discrete " +
				"payload) yields expected = 0; the goodness-of-fit term drops (matches TEST_CHISQ's expected > 0 guard) but " +
				"the low-expected warning still fires. Empty host distribution OR subset_N == 0 (every observed count is zero) " +
				"OR population is empty: NaN statistic + NaN p-value with one PULSE_OVERLAY_REF_ZERO warning — no χ² test can " +
				"be computed without observed counts. Single category (df = 0): NaN p-value alongside the statistic — " +
				"descriptive only. Ref.Population MUST be populated (the cohort name lives on Ref.Population.Cohort); any " +
				"other ref-family pointer (Margin / Sibling / BaselineIndex / Prior / RollingMean / YoY / Stage / Slot) fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time (per-kind validator lands in E5-S6 / E5-S7). " +
				"Level / Within MUST be zero (population comparison is a single-value lookup, not an axis prefix); non-zero " +
				"values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the implicit-ref family. Scope must be GROUP. " +
				"Renderer-facing shape: viz developer reads OverlayPayload.Scalar for the χ² statistic and OverlaySummary " +
				"for the (statistic, df, p-value) triple — small p-value flags \"the subset distribution diverges from the " +
				"population\". The layer-level Baseline stays unset (inferential overlays do not surface a ratio centerpoint).",
		}
	case types.OverlayKindChiSqVsRef:
		return OverlayCapability{
			Kind: types.OverlayKindChiSqVsRef,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeMatrix,
				types.OverlayScopeTotal,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union. RefKinds is empty because the on-wire
			// shape uses slot labels rather than an OverlayRef pointer.
			RefKinds: []string{},
			Description: "COMPOSE-host whole-matrix χ² test against the reference slot's matrix (E7-S9). The two matrices are " +
				"treated as two independent contingency tables and the test answers \"do the target and reference distributions " +
				"differ?\" by scaling the reference distribution to the target's grand total: expected[i,j] = ref_cell * " +
				"(target_N / ref_N); chisq = Σ (target_cell - expected)² / expected; df = (cells with expected > 0) - 1; " +
				"p_value = chiSquareSurvival(chisq, df). SCALAR payload — the layer carries the p-value on " +
				"OverlayPayload.Scalar plus OverlaySummary{Statistic, PValue, Parameters{\"df\"}}. Reuses the chiSquareSurvival " +
				"helper backing TEST_CHISQ and the MATRIX-host CHISQ family so the overlay surface produces identical p-values " +
				"for the same contingency. Degenerate inputs (target_N == 0, ref_N == 0, every expected = 0) emit NaN + NaN " +
				"with one PULSE_OVERLAY_REF_ZERO warning. Low-expected-cell warning follows the canonical χ² rule (any " +
				"expected < 5 → one PULSE_OVERLAY_EXPECTED_LOW warning per layer). Inherently buffered — inferential overlays " +
				"as a family stay buffered until a streamable-test path is plumbed (PRD §2 Non-Goals). Resolution + key " +
				"alignment + schema match + dict drift all run BEFORE the handler dispatches (E7-S5..S8) so the handler " +
				"receives finalised *Response objects for both slots with identical structure.",
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
	case types.OverlayKindDeltaVsRef:
		return OverlayCapability{
			Kind: types.OverlayKindDeltaVsRef,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
				types.OverlayScopeGroup,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union. RefKinds is empty because the on-wire
			// shape uses slot labels rather than an OverlayRef pointer.
			RefKinds: []string{},
			Description: "COMPOSE-host dual-shape additive delta of each target slot's value against the matching reference " +
				"slot value: target - ref. Dual-shape overload — the host-shape disambiguator (matrix vs series) routes the " +
				"per-coordinate / per-group dispatch: MATRIX host (E7-S9) emits per-cell deltas at (rowKey, colKey) with CELL " +
				"scope; SERIES host (E7-S10) emits per-group deltas at the host's group key with GROUP scope. Subtractive " +
				"sibling of OVERLAY_INDEX_VS_REF (E7-S9; gains the SERIES arm in the same E7-S10 batch). Output preserves the " +
				"target slot's units — a $-valued AGG_SUM target value minus a $-valued reference value yields a $-valued " +
				"deviation in the same currency (mirrors OVERLAY_DELTA_VS_MARGIN / OVERLAY_DELTA_VS_BASELINE / " +
				"OVERLAY_DELTA_VS_SIBLING). Unlike the OVERLAY_INDEX_VS_REF sibling (which divides by the reference value " +
				"and emits PULSE_OVERLAY_REF_ZERO against a zero denominator), DELTA_VS_REF performs subtraction and is " +
				"mathematically defined for every finite reference value including zero — the handler does NOT emit " +
				"PULSE_OVERLAY_REF_ZERO on a zero reference. Missing reference rows / cells (target carries a key the " +
				"reference did not surface) still emit PULSE_OVERLAY_REF_ZERO with a ref_missing=true Detail flag. Renderers " +
				"centre diverging colour ramps on baseline=0. Resolution + key alignment + schema match + dict drift all run " +
				"BEFORE the handler dispatches (E7-S5..S8). Streamable via the SERIES dispatch (the MATRIX route is forced " +
				"buffered through the slot barrier in service.Compose / service.ComposeParallel; the flag describes the " +
				"kind's INTRINSIC streaming capability via its SERIES handler, mirroring OVERLAY_INDEX_VS_REF's dual-shape " +
				"convention).",
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
	case types.OverlayKindDeltaVsStage:
		return OverlayCapability{
			Kind: types.OverlayKindDeltaVsStage,
			Shapes: []types.OverlayShape{
				// Whole-chain kinds inherit the target stage's host shape;
				// the catalog declares every shape the kind may emit so
				// MCP / manifest clients see the full surface.
				types.OverlayShapeMatrix,
				types.OverlayShapeScalar,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// Whole-chain kinds decorate the entire chain result;
				// `total` is the canonical whole-result scope (the
				// renderer surfaces the overlay as a single badge near
				// the chain's final result).
				types.OverlayScopeTotal,
			},
			// Stage is the StageRef-bearing ref family — Ref + Target slots
			// on ChainOverlaySpec each populate exactly one of Index / Name
			// per StageRef value. The capability row declares the consumed
			// Ref-arm so MCP / manifest clients see the kind addresses
			// ChainRequest.Stages directly. Per-kind validator + runtime
			// handler land in E6-S5 (this story is the dispatcher chassis).
			RefKinds: []string{"Stage"},
			Description: "Whole-chain additive-delta overlay against another ProcessChain stage's result: " +
				"target_val - ref_val per host coordinate. Second whole-chain overlay in the catalog " +
				"(E6-S2 declares the kind; E6-S5 lands the runtime handler). CHAIN scope over a CHAIN " +
				"(ProcessChain) host with a shape inherited from the target stage (scalar / series / " +
				"matrix). Sibling subtractive twin of OVERLAY_INDEX_VS_STAGE — the runtime handler " +
				"runs at the post-stage-loop barrier inside service.ProcessChain (applyChainOverlays " +
				"hook on service/chain.go) against already-materialised *Response objects for each " +
				"Stages[i]. No record re-traversal. Default Target = latest stage (Target.Index nil " +
				"AND Target.Name empty); Ref has no default — every spec MUST name a baseline stage " +
				"explicitly. Unknown stage path: Index out of range OR Name unmatched fires " +
				"PULSE_OVERLAY_TARGET_UNKNOWN (Target arm) or PULSE_OVERLAY_REFERENCE_UNKNOWN (Ref " +
				"arm) — both canonical chain-overlay missing-stage codes landed with E6-S6. " +
				"Shape-divergence rule (PRD §6 FR-F2): when " +
				"the target stage's host shape differs from the reference stage's host shape the " +
				"handler emits PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (landed with E6-S6) and " +
				"surfaces NaN across every coordinate. Unlike OVERLAY_INDEX_VS_STAGE (divides by the " +
				"reference value), DELTA_VS_STAGE performs subtraction and is mathematically defined " +
				"for every finite reference value including zero — the handler does NOT emit " +
				"PULSE_OVERLAY_REF_ZERO. Mirrors the OVERLAY_DELTA_VS_BASELINE / OVERLAY_DELTA_VS_MARGIN " +
				"/ OVERLAY_DELTA_VS_SIBLING family rule. Ref MUST populate Ref.Stage (StageRef); any " +
				"other ref-family pointer fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict " +
				"time (per-kind validator lands in E6-S5). Scope must be `total`. Level / Within MUST " +
				"be zero. Buffered — whole-chain kinds always run after every stage has finalised. " +
				"Renderers centre diverging colour ramps on baseline=0 (mirrors the rest of the DELTA " +
				"family on other host shapes).",
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
	case types.OverlayKindIndexVsPop:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// (E5-S1 foundation; E5-S2 first consumer). The capability row
			// declares the consumed Ref-arm so MCP / manifest clients see
			// the kind requires Ref.Population to be populated; the per-kind
			// validator (descriptor.validateOverlayIndexVsPop) lands in E5-S6
			// (this story is the runtime handler only).
			RefKinds: []string{"Population"},
			Description: "Per-value population-comparison index against a FACET host: subset_freq / pop_freq * 100. " +
				"GROUP scope over a FACET (FacetSchema) host with SERIES payload — one SeriesEntry per host value in " +
				"payload order, each carrying the index on Summary.Statistic. First FACET-host overlay in the catalog " +
				"(E5-S2) and the first kind to consume the Ref.Population arm of the OverlayRef discriminated union " +
				"(E5-S1 foundation — the resolver shape lives at processing.FacetPopulationView). Categorical fast " +
				"path (host.Kind == \"discrete\"): walks host.Discrete.Values in payload order (descending by count, " +
				"ties ascending by value-string — the canonical FacetSchema sort); each entry emits the per-value " +
				"subset_freq / pop_freq * 100 against FacetPopulationView.DiscreteFrequency. Numeric path " +
				"(host.Kind == \"numeric\"): walks host.Numeric.Histogram.Bins in bin-index order when the host " +
				"requested a histogram and the population view also carries a histogram with matching shape; each " +
				"entry emits per-bin subset_bin_freq / pop_bin_freq * 100. When the host carries no histogram the " +
				"layer emits zero entries (the Welford summary carries no per-value buckets the index can compare " +
				"against — percentile-bucket indexing is reserved for OVERLAY_KS_VS_POP / E5-S5). Streaming finalize " +
				"hook: the handler runs as a post-finalize fold over the host's already-materialized per-value " +
				"distribution and the resolver's already-materialized population view; no second pass over records " +
				"and no widening of the host streaming carrier — the streamable guarantee is that running this " +
				"handler against a streaming vs buffered host produces byte-identical SeriesPayload output because " +
				"the input state is identical. Zero pop_freq path: when pop_freq == 0 (value never appeared in the " +
				"population OR population has zero records altogether), the handler emits ONE " +
				"PULSE_OVERLAY_REF_ZERO warning per affected entry carrying the kind + value and SKIPS the index " +
				"entry (Statistic stays unset on that entry). Absent-population path (value missing from the " +
				"population's discrete payload) routes through the same zero-pop_freq arm. Ref.Population MUST be " +
				"populated (the cohort name lives on Ref.Population.Cohort); any other ref-family pointer (Margin / " +
				"Sibling / BaselineIndex / Prior / RollingMean / YoY / Stage / Slot) fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time (per-kind validator lands in E5-S6). " +
				"Level / Within MUST be zero (population comparison is a single-value lookup, not an axis prefix); " +
				"non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the implicit-ref family. Scope must " +
				"be GROUP. Streamable — host streaming carriers are unchanged; the overlay reads finalized state " +
				"only. Renderers centre diverging colour ramps on baseline=100 (mirrors OVERLAY_INDEX_VS_TOTAL / " +
				"OVERLAY_INDEX_VS_MARGIN / OVERLAY_INDEX_VS_PRIOR / OVERLAY_INDEX_VS_BASELINE).",
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
	case types.OverlayKindIndexVsRef:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsRef,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
				types.OverlayScopeGroup,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union. RefKinds is empty because the on-wire
			// shape uses slot labels rather than an OverlayRef pointer.
			RefKinds: []string{},
			Description: "COMPOSE-host dual-shape ratio index of each target slot's value against the matching reference " +
				"slot value: (target / ref) * scale where scale defaults to 100 (set Params[\"scale\"] = 1 for a raw " +
				"ratio). Dual-shape overload — the host-shape disambiguator (matrix vs series) routes the per-coordinate / " +
				"per-group dispatch: MATRIX host (E7-S9) emits per-cell ratios at (rowKey, colKey) with CELL scope; SERIES " +
				"host (E7-S10) emits per-group ratios at the host's group key with GROUP scope. First COMPOSE-only crosstab-" +
				"shape overlay kind in the catalog (E7-S9; SERIES arm in E7-S10) and the first kind to consume the " +
				"ComposeOverlaySpec.Reference / Targets slot-label pair across both MATRIX and SERIES slots. Ratio sibling " +
				"of OVERLAY_DELTA_VS_REF (subtractive twin; gains the SERIES arm in the same E7-S10 batch). Resolution + " +
				"key alignment + schema match + dict drift all run BEFORE the handler dispatches (E7-S5..S8) — the handler " +
				"receives finalised *Response objects for both slots with byte-aligned key sets and identical structural " +
				"schemas. Zero reference value emits PULSE_OVERLAY_REF_ZERO with NaN substitution; missing reference rows / " +
				"cells (target carries a key the reference did not surface) emit the same code with a ref_missing=true " +
				"Detail flag. Streamable via the SERIES dispatch (the MATRIX route is forced buffered through the slot " +
				"barrier in service.Compose / service.ComposeParallel; the flag describes the kind's INTRINSIC streaming " +
				"capability via its SERIES handler, mirroring OVERLAY_SHARE_OF_TOTAL's dual-shape convention). Renderers " +
				"centre diverging colour ramps on baseline=scale (default 100 mirrors the per-Request INDEX_VS_* family).",
		}
	case types.OverlayKindIndexVsRollingMean:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsRollingMean,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// RollingMean is the windowed-axis ref family the kind consumes
			// (E4-S5; E4-S6 ZSCORE_VS_ROLLING will reuse). The empty marker
			// struct tags the family — the v1 window value lives on
			// OverlaySpec.Params["window"] per the WIN_* operator convention.
			RefKinds: []string{"RollingMean"},
			Description: "Per-point windowed index against the arithmetic mean of the W immediately preceding points of " +
				"an ordered SERIES (grouped Process) host: point_value / mean(point[i-W] .. point[i-1]) * 100. GROUP " +
				"scope over a SERIES host with SERIES payload — one SeriesEntry per host group key in host order, " +
				"each carrying the index on Summary.Statistic. Fourth windowed-Process overlay in the catalog (E4-S5; " +
				"siblings: OVERLAY_INDEX_VS_PRIOR / E4-S4, OVERLAY_INDEX_VS_BASELINE / E4-S2, " +
				"OVERLAY_DELTA_VS_BASELINE / E4-S3) and the first kind to consume the Ref.RollingMean arm of the " +
				"OverlayRef discriminated union. Window-via-Params convention: the rolling window width is supplied via " +
				"OverlaySpec.Params[\"window\"] as a positive integer (mirrors the WIN_* operator convention; see " +
				"skills/window-operations.md). The Ref.RollingMean arm is an empty marker struct tagging the ref family " +
				"— the v1 window value lives entirely on Params. The handler maintains a per-group ring buffer of the W " +
				"most recently observed PRESENT values plus a Welford (count, mean, M2) trio reserved for the E4-S6 " +
				"OVERLAY_ZSCORE_VS_ROLLING handler (sibling windowed-rolling kind reuses the same ref-arm). " +
				"Window-fill semantics: the first W present ordinals all emit NaN without warning — \"window not yet " +
				"filled\" is structurally distinct from \"denominator was zero\". Absent host points (resolver reports " +
				"(0, false)) emit a present SeriesEntry whose Summary leaves Statistic unset and DO NOT advance the ring " +
				"buffer — the next present ordinal compares against the most recent W PRESENT values, not absent slots. " +
				"Zero rolling-mean value (every prior point in the window was zero) emits ONE PULSE_OVERLAY_REF_ZERO " +
				"warning per layer and surfaces NaN on the affected entries. Missing Params[\"window\"] fires " +
				"PULSE_OVERLAY_PARAM_MISSING; non-positive or non-integer window fires " +
				"PULSE_OVERLAY_LEVEL_OUT_OF_RANGE (the LEVEL_OUT_OF_RANGE code is reused for \"value out of valid " +
				"range\" semantics so the new code surface stays narrow). Ref.RollingMean MUST be populated (empty " +
				"marker is fine); any other ref-family pointer (Margin / Sibling / Prior / BaselineIndex / Population " +
				"/ Stage / Slot) fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Level / Within MUST be zero (windowed " +
				"kind — the rolling-mean carrier folds across the ordered axis without a prefix-bucket denominator); " +
				"non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the INDEX_VS_PRIOR / INDEX_VS_TOTAL " +
				"family. Buffered — the ring buffer carries the full window of present values, larger than the " +
				"single-state lag accumulator INDEX_VS_PRIOR uses, so the streaming Process pass cannot maintain it " +
				"inline with the per-record fold today. Renderers centre diverging colour ramps on baseline=100 " +
				"(mirrors OVERLAY_INDEX_VS_MARGIN / OVERLAY_INDEX_VS_TOTAL / OVERLAY_INDEX_VS_SIBLING / " +
				"OVERLAY_INDEX_VS_PRIOR / OVERLAY_INDEX_VS_BASELINE).",
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
	case types.OverlayKindIndexVsStage:
		return OverlayCapability{
			Kind: types.OverlayKindIndexVsStage,
			Shapes: []types.OverlayShape{
				// Whole-chain kinds inherit the target stage's host shape;
				// the catalog declares every shape the kind may emit so
				// MCP / manifest clients see the full surface.
				types.OverlayShapeMatrix,
				types.OverlayShapeScalar,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// Whole-chain kinds decorate the entire chain result;
				// `total` is the canonical whole-result scope (the
				// renderer surfaces the overlay as a single badge near
				// the chain's final result).
				types.OverlayScopeTotal,
			},
			// Stage is the StageRef-bearing ref family — Ref + Target slots
			// on ChainOverlaySpec each populate exactly one of Index / Name
			// per StageRef value. The capability row declares the consumed
			// Ref-arm so MCP / manifest clients see the kind addresses
			// ChainRequest.Stages directly. Per-kind validator + runtime
			// handler land in E6-S4 (this story is the dispatcher chassis).
			RefKinds: []string{"Stage"},
			Description: "Whole-chain ratio overlay against another ProcessChain stage's result: " +
				"(target_val / ref_val) * 100 per host coordinate. First whole-chain overlay in the " +
				"catalog (E6-S2 declares the kind; E6-S4 lands the runtime handler) and the first " +
				"consumer of the StageRef discriminated reference family (Ref + Target on " +
				"ChainOverlaySpec — each populates exactly one of Index / Name per StageRef value). " +
				"CHAIN scope over a CHAIN (ProcessChain) host with a shape inherited from the target " +
				"stage (scalar / series / matrix). The runtime handler runs at the post-stage-loop " +
				"barrier inside service.ProcessChain (applyChainOverlays hook on service/chain.go) " +
				"against already-materialised *Response objects for each Stages[i]. No record " +
				"re-traversal. Default Target = latest stage (Target.Index nil AND Target.Name empty); " +
				"Ref has no default — every spec MUST name a baseline stage explicitly. Unknown stage " +
				"path: Index out of range OR Name unmatched fires PULSE_OVERLAY_TARGET_UNKNOWN (Target " +
				"arm) or PULSE_OVERLAY_REFERENCE_UNKNOWN (Ref arm) — both canonical chain-overlay " +
				"missing-stage codes landed with E6-S6. " +
				"Shape-divergence rule (PRD §6 FR-F2): when the target stage's host shape differs from " +
				"the reference stage's host shape the handler emits " +
				"PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (landed with E6-S6) and surfaces NaN " +
				"across every coordinate. Zero reference value emits PULSE_OVERLAY_REF_ZERO with NaN " +
				"on the affected coordinates (mirrors the rest of the INDEX_VS_* family). Ref MUST " +
				"populate Ref.Stage (StageRef); any other ref-family pointer fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time (per-kind validator lands " +
				"in E6-S4). Scope must be `total`. Level / Within MUST be zero. Buffered — whole-chain " +
				"kinds always run after every stage has finalised. Renderers centre diverging colour " +
				"ramps on baseline=100 (mirrors the rest of the INDEX_VS_* family on other host " +
				"shapes).",
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
	case types.OverlayKindKSVsPop:
		return OverlayCapability{
			Kind: types.OverlayKindKSVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// (E5-S1 foundation; E5-S2 INDEX_VS_POP first consumer; E5-S3
			// ZSCORE_VS_POP second consumer; E5-S4 CHISQ_VS_POP third
			// consumer; E5-S5 KS_VS_POP fourth consumer). The capability row
			// declares the consumed Ref-arm so MCP / manifest clients see
			// the kind requires Ref.Population to be populated; the per-kind
			// validator (descriptor.validateOverlayKSVsPop) lands in E5-S10
			// (this story is the runtime handler only).
			RefKinds: []string{"Population"},
			Description: "Single scalar Kolmogorov-Smirnov distance D = sup_x |F_subset(x) - F_pop(x)| plus an asymptotic p-value " +
				"comparing the host Facet subset NUMERIC distribution against the resolved population NUMERIC distribution. " +
				"GROUP scope over a FACET (FacetSchema) host with SCALAR payload — the layer carries the KS statistic plus the " +
				"asymptotic p-value via OverlaySummary{Statistic, PValue, Parameters{\"n_subset\", \"n_pop\"}}. Fourth and final " +
				"FACET-host overlay in the catalog (E5-S5; siblings: OVERLAY_INDEX_VS_POP / E5-S2 descriptive, " +
				"OVERLAY_ZSCORE_VS_POP / E5-S3 descriptive, OVERLAY_CHISQ_VS_POP / E5-S4 inferential discrete-arm only). KS is the " +
				"canonical NUMERIC-arm distributional-shift indicator — it pairs with CHISQ_VS_POP as the two inferential FACET-host " +
				"kinds, partitioning the inferential FACET surface by host arm (CHISQ for categorical, KS for numeric). The viz " +
				"developer renders the SCALAR statistic as a distributional-shift indicator near the facet header. Math: " +
				"D = sup_x |F_subset(x) - F_pop(x)|; en = sqrt(n_subset * n_pop / (n_subset + n_pop)); " +
				"p_value = kolmogorovSurvival((en + 0.12 + 0.11/en) * D) — the same Kolmogorov-survival helper backing TEST_KS " +
				"(processing/test_stat.go), so the overlay and the row-test surface produce identical p-values for the same D + N. " +
				"NUMERIC ARM ONLY: KS is undefined on categorical distributions (no continuous CDF to compare). A categorical host " +
				"(no numeric payload) fires PULSE_OVERLAY_SCOPE_UNSUPPORTED at runtime; the per-kind validator (lands in E5-S10) " +
				"rejects categorical hosts at predict time so the runtime arm is belt-and-braces. Distinct from CHISQ_VS_POP which " +
				"is DISCRETE-arm only and rejects numeric hosts with PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Population-sample " +
				"availability: the S1 resolver exposes only summary state for numeric fields (Welford mean/stdev/count, optional " +
				"percentile map, optional histogram) — raw population values were folded into Welford state and discarded. The " +
				"handler picks the highest-precision reconstruction available: (1) HISTOGRAM PATH (preferred) when host AND " +
				"population each surface a histogram with matching Min/Max/bucket count — aligns bucket edges and computes " +
				"sup-CDF-difference over the bucket-fraction CDF on both sides; (2) PERCENTILE FALLBACK when both arms surface a " +
				"percentile map but histograms are absent or mismatched — turns each percentile entry into a CDF sample point and " +
				"computes sup-CDF-difference over the common percentile labels (lower precision than histograms but well-defined); " +
				"(3) WELFORD-ONLY DEGENERATE when neither histograms nor percentiles are available — emits NaN + NaN with one " +
				"PULSE_OVERLAY_REF_ZERO warning whose Details document which knobs were missing on the FacetRequest. KS precision " +
				"is bounded by histogram bucket width (path 1) or percentile-label resolution (path 2) because the population view " +
				"does not retain raw values; a future story may lift the resolver to retain raw sorted population values when a KS " +
				"overlay is requested, at which point the handler can call ksTwoSampleD on raw arms directly (additive change). " +
				"INHERENTLY BUFFERED per PRD §2 Non-Goals (\"Streaming overlay path for inferential kinds\") — the empirical-CDF " +
				"construction requires sorted values on both sides; an online folded shape would widen the streaming carrier " +
				"beyond v1's single-state lag accumulator. Degenerate inputs: empty host (n_subset == 0) OR empty population " +
				"(n_pop == 0) emits NaN + NaN with PULSE_OVERLAY_REF_ZERO; histogram arms with mismatched edges (different " +
				"Min/Max/bucket count) fall through to the percentile fallback if available, else emit PULSE_OVERLAY_REF_ZERO " +
				"carrying the mismatching shape Details. Ref.Population MUST be populated (the cohort name lives on " +
				"Ref.Population.Cohort); any other ref-family pointer (Margin / Sibling / BaselineIndex / Prior / RollingMean / " +
				"YoY / Stage / Slot) fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time. Level / Within MUST be zero " +
				"(population comparison is a single-statistic lookup, not an axis prefix); non-zero values fire " +
				"PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the implicit-ref family. Scope must be GROUP. Renderer-facing shape: " +
				"viz developer reads OverlayPayload.Scalar for the KS distance D and OverlaySummary for the (statistic, p-value) " +
				"pair — large D and small p-value flag \"the subset distribution shape diverges from the population\". The " +
				"layer-level Baseline stays unset (inferential overlays do not surface a ratio centerpoint; mirrors CHISQ_VS_POP).",
		}
	case types.OverlayKindPropZCell:
		return OverlayCapability{
			Kind: types.OverlayKindPropZCell,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union.
			RefKinds: []string{},
			Description: "COMPOSE-host per-cell two-proportion z-test against the reference slot's matching cell (E7-S9). CELL " +
				"scope over a MATRIX (crosstab) host with MATRIX payload — each cell's value is the two-sided p-value as a " +
				"float64. Cells are treated as success counts; each cell's matching row margin is treated as its sample size " +
				"(target_n on the target side, ref_n on the reference side). Math: p_target = target_cell / target_row_margin; " +
				"p_ref = ref_cell / ref_row_margin; pooled = (target_cell + ref_cell) / (target_row_margin + ref_row_margin); " +
				"se = sqrt(pooled * (1 - pooled) * (1/n_target + 1/n_ref)); z = (p_target - p_ref) / se; p_value = 2 * (1 - " +
				"Φ(|z|)). Reuses the standardNormalCDF helper backing TEST_PROP_Z so the overlay and the row-test surface " +
				"produce identical p-values for the same (success, n) pair. Degenerate inputs (pooled ∈ {0, 1}, missing row " +
				"margin, etc.) produce NaN p-values with one PULSE_OVERLAY_REF_ZERO warning per affected cell. Inherently " +
				"buffered — inferential overlays as a family stay buffered until a streamable-test path is plumbed (PRD §2 " +
				"Non-Goals).",
		}
	case types.OverlayKindPropZPanel:
		return OverlayCapability{
			Kind: types.OverlayKindPropZPanel,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			// COMPOSE-only multi-ref kind — Reference + Targets resolve
			// via the ComposeOverlaySpec slot-label pair. RefKinds stays
			// empty: the panel walks {reference, targets...} as the
			// pairwise universe, no OverlayRef family pointer applies.
			RefKinds: []string{},
			Description: "COMPOSE-host MULTI-REFERENCE per-cell pairwise two-proportion z-test across the reference slot plus " +
				"every target slot (E7-S11). CELL scope over a MATRIX (crosstab) host with MATRIX payload — each cell's Value " +
				"is the flattened upper-triangular slice of pairwise two-sided p-values across the panel's N + 1 slots " +
				"(reference at index 0; Targets[i] at index i + 1). Slot ordering: {reference, targets[0], targets[1], ..., " +
				"targets[N-1]}. Output shape — row-major upper triangle, NO diagonal: [(0,1), (0,2), ..., (0,M-1), (1,2), ..., " +
				"(M-2,M-1)] for M = N + 1 slots. Slice length is M*(M-1)/2. The diagonal is implicit (every diagonal entry " +
				"is 1.0) and the lower triangular is implicit (p[j,i] == p[i,j] — symmetric matrix). Math per pair: reuses " +
				"the same pooled-SE two-proportion z-test formula and standardNormalCDF helper backing OVERLAY_PROP_Z_CELL " +
				"and TEST_PROP_Z, so every pairwise p-value matches its corresponding single-pair output byte-for-byte. " +
				"Cap enforcement: ComposeOverlaySpec.Options.MaxPanelTargets defaults to 16 — len(spec.Targets) > cap " +
				"fires PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP at handler entry with {observed, cap} Details. Per the interview " +
				"risk paragraph \"Multi-reference combinatorics\", bumping the default 16 requires an interview update; " +
				"Options.MaxPanelTargets is the per-request override surface. Degenerate inputs (pooled ∈ {0, 1}, missing row " +
				"margin) produce NaN at the affected pair position with one PULSE_OVERLAY_REF_ZERO warning per (cell, pair) " +
				"tuple. Cells where any slot is absent surface an empty per-cell vector with one PULSE_OVERLAY_REF_ZERO " +
				"warning carrying ref_missing: true. Inherently buffered — inferential overlays stay buffered as a family " +
				"per PRD §2 Non-Goals.",
		}
	case types.OverlayKindRank:
		return OverlayCapability{
			Kind: types.OverlayKindRank,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			// COMPOSE-only kind. Population param is on Params["population"]
			// per the WIN_* operator convention.
			RefKinds: []string{},
			Fields:   []string{},
			Description: "COMPOSE-host per-cell rank of each target cell within a configurable population per Params[\"population\"] " +
				"(\"row\" | \"column\" | \"matrix\"; defaults to \"matrix\"). CELL scope over a MATRIX (crosstab) host with MATRIX " +
				"payload — each cell's value is the 1-based average-tie rank (1 = largest) computed against the population the " +
				"spec selected. Population modes: \"row\" ranks within the cell's own row across all columns; \"column\" ranks " +
				"within the cell's own column across all rows; \"matrix\" ranks across every present cell of the target matrix. " +
				"Ties take the average of their would-be ranks (canonical \"average\" tie-breaking; matches " +
				"scipy.stats.rankdata default convention). Absent cells stay absent on the overlay; they do NOT participate in " +
				"the population's denominator. The reference slot is required to anchor the resolution + key-set gates (E7-S6 " +
				"/ E7-S7) but the rank computation reads only the target cells — RANK is orthogonal to the ref-vs-target " +
				"comparison family while still reusing the same resolution + alignment pipeline. Inherently buffered — the " +
				"COMPOSE host is buffered by the slot barrier; ranking within the target matrix requires the full materialised " +
				"matrix anyway.",
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
	case types.OverlayKindTCell:
		return OverlayCapability{
			Kind: types.OverlayKindTCell,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union.
			RefKinds: []string{},
			Description: "COMPOSE-host per-cell Welch t-test against the reference slot's matching cell (E7-S9). CELL scope " +
				"over a MATRIX (crosstab) host with MATRIX payload — each cell's value is the two-sided p-value as a float64. " +
				"Each cell is treated as a sample mean; the variance defaults to 1.0 and the sample size defaults to 2 " +
				"(`Params[\"sample_size\"]` accepts an integer override; `Params[\"variance_target\"]` / " +
				"`Params[\"variance_ref\"]` / `Params[\"sample_size_target\"]` / `Params[\"sample_size_ref\"]` give finer " +
				"per-side control). Math reuses the same Welch-Satterthwaite degrees-of-freedom recurrence that backs " +
				"TEST_T two-sample: se = sqrt(var_target/n_target + var_ref/n_ref); t = (target_cell - ref_cell) / se; " +
				"df = (var_target/n_target + var_ref/n_ref)² / ((var_target/n_target)²/(n_target-1) + " +
				"(var_ref/n_ref)²/(n_ref-1)); p_value = studentTTwoSidedP(t, df). Reuses the studentTTwoSidedP helper backing " +
				"TEST_T so the overlay and the row-test surface produce identical p-values for the same (mean, variance, n) " +
				"triple. Default-variance and default-sample-size policy: v1 ships well-defined defaults so the per-cell " +
				"handler stays usable against a minimal Compose authoring surface; a future story may lift the per-cell " +
				"(mean, variance, n) triple into a richer Compose authoring surface (the crosstab cell carrier could grow " +
				"to carry sample statistics alongside the scalar mean). Inherently buffered — inferential overlays as a " +
				"family stay buffered until a streamable-test path is plumbed (PRD §2 Non-Goals).",
		}
	case types.OverlayKindTVsRef:
		return OverlayCapability{
			Kind: types.OverlayKindTVsRef,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// COMPOSE-only kind — Reference + Targets resolve via the
			// ComposeOverlaySpec slot-label pair, not via the OverlayRef
			// discriminated union.
			RefKinds: []string{},
			Description: "COMPOSE-host per-group Welch t-test against the reference slot's matching group, against a SERIES " +
				"(grouped Process) host (E7-S10). GROUP scope over a SERIES host with SERIES payload — one SeriesEntry per " +
				"host group key in host order, each carrying the two-sided p-value on Summary.Statistic. Series-shape sibling " +
				"of OVERLAY_T_CELL (the per-cell MATRIX-host Welch t-test) and twin to the SERIES arm of OVERLAY_INDEX_VS_REF " +
				"/ OVERLAY_DELTA_VS_REF (descriptive series-shape Compose-only kinds). Each per-group target / reference value " +
				"is treated as a sample mean; variance defaults to 1.0 and sample size defaults to 2 (Params[\"variance_target\"] " +
				"/ Params[\"variance_ref\"] / Params[\"sample_size_target\"] / Params[\"sample_size_ref\"] override). Math " +
				"reuses the Welch-Satterthwaite recurrence backing TEST_T (and OVERLAY_T_CELL): se = " +
				"sqrt(var_target/n_target + var_ref/n_ref); t = (target - ref) / se; df = (var_target/n_target + " +
				"var_ref/n_ref)² / ((var_target/n_target)²/(n_target-1) + (var_ref/n_ref)²/(n_ref-1)); p_value = " +
				"studentTTwoSidedP(t, df). Reuses studentTTwoSidedP so the overlay and the row-test surface produce identical " +
				"p-values for the same (mean, variance, n) triple. Missing reference rows (target row key not present in the " +
				"reference series) emit PULSE_OVERLAY_REF_ZERO with a ref_missing=true Detail flag; the affected entry's " +
				"Statistic is NaN. Degenerate inputs (se == 0, n < 2) produce NaN p-values with the same warning. Inherently " +
				"buffered — inferential overlays as a family stay buffered until a streamable-test path is plumbed (PRD §2 " +
				"Non-Goals). Resolution + key alignment + schema match + dict drift all run BEFORE the handler dispatches " +
				"(E7-S5..S8).",
		}
	case types.OverlayKindYoY:
		return OverlayCapability{
			Kind: types.OverlayKindYoY,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// YoY is the windowed-axis ref family the kind consumes (E4-S7
			// foundation; first consumer of the empty `Ref.YoY` marker). The
			// empty marker struct tags the family — the v1 frequency value
			// lives on OverlaySpec.Params["frequency"] (the YoY's own
			// override) or falls back to req.Groups[0].Params["frequency"]
			// (the canonical GROUP_DATE authoring slot) per the WIN_*
			// operator convention.
			RefKinds: []string{"YoY"},
			Description: "Per-point year-over-year ratio against the same period one year prior in an ordered SERIES " +
				"(grouped Process) host whose grouper is GROUP_DATE: point_value / prior_year_value * 100. " +
				"GROUP scope over a SERIES host with SERIES payload — one SeriesEntry per host group key in host " +
				"order, each carrying the YoY ratio on Summary.Statistic. Sixth windowed-Process overlay in the " +
				"catalog (E4-S7; siblings: OVERLAY_INDEX_VS_PRIOR / E4-S4, OVERLAY_INDEX_VS_BASELINE / E4-S2, " +
				"OVERLAY_DELTA_VS_BASELINE / E4-S3, OVERLAY_INDEX_VS_ROLLING_MEAN / E4-S5, " +
				"OVERLAY_ZSCORE_VS_ROLLING / E4-S6) and the first consumer of the empty Ref.YoY marker arm of the " +
				"OverlayRef discriminated union. The frequency value (annual | quarterly | monthly | weekly | daily | " +
				"hourly) lives on OverlaySpec.Params[\"frequency\"] (the YoY's own override) or falls back to " +
				"req.Groups[0].Params[\"frequency\"] (the canonical GROUP_DATE authoring slot). Per-frequency stride: " +
				"annual ⇒ i-1, quarterly ⇒ i-4, monthly ⇒ i-12, weekly ⇒ i-52 (calendar-week aligned; no day-of-week " +
				"realignment in v1), daily ⇒ exact-date 365-days-prior lookup against the host key index (Feb 29 in a " +
				"non-leap prior year emits NaN — no exact-key match), hourly ⇒ exact-hour 365×24-hour-prior lookup. " +
				"The host's first grouper MUST be GROUP_DATE; non-DATE hosts fire " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at both predict and runtime. Missing frequency Param fires " +
				"PULSE_OVERLAY_YOY_FREQUENCY_MISSING; out-of-set frequency value fires " +
				"PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY with {frequency, supported} Details. First-year ordinals " +
				"(coarse-frequency arms whose prior-year index lands at <0; fine-frequency arms whose prior-year " +
				"date does not match an exact host key) emit NaN without warning — \"no comparison available\" is " +
				"structurally distinct from \"denominator was zero\". Zero prior-year value emits ONE " +
				"PULSE_OVERLAY_REF_ZERO warning per layer and surfaces NaN on the affected entry. Absent host " +
				"points (resolver reports (0, false)) emit a present SeriesEntry whose Summary leaves Statistic " +
				"unset. Ref.YoY MUST be populated (empty marker is fine); any other ref-family pointer (Margin / " +
				"Sibling / Prior / BaselineIndex / RollingMean / Population / Stage / Slot) fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Level / Within MUST be zero (windowed kind — the " +
				"year-over-year lookup folds across the ordered axis without a prefix-bucket denominator); non-zero " +
				"values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the INDEX_VS_PRIOR / INDEX_VS_ROLLING_MEAN " +
				"family. Buffered — the per-frequency prior-period lookup requires the materialised host series " +
				"(coarse-frequency arms index into an arbitrary prior ordinal; fine-frequency arms walk an exact-key " +
				"index built from the full host key list). Renderers centre diverging colour ramps on baseline=100.",
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
	case types.OverlayKindZScoreVsPop:
		return OverlayCapability{
			Kind: types.OverlayKindZScoreVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// (E5-S1 foundation; E5-S2 INDEX_VS_POP first consumer; E5-S3
			// ZSCORE_VS_POP second consumer). The capability row declares
			// the consumed Ref-arm so MCP / manifest clients see the kind
			// requires Ref.Population to be populated; the per-kind
			// validator (descriptor.validateOverlayZScoreVsPop) lands in
			// E5-S6 / E5-S7 (this story is the runtime handler only).
			RefKinds: []string{"Population"},
			Description: "Per-value population-comparison z-score against a FACET host: (subset_freq - pop_freq) / sd_pop. " +
				"GROUP scope over a FACET (FacetSchema) host with SERIES payload — one SeriesEntry per host value in " +
				"payload order, each carrying the z-score on Summary.Statistic. Sibling streamable FACET-host kind to " +
				"OVERLAY_INDEX_VS_POP (E5-S2); pairs with INDEX_VS_POP as the two streamable Facet overlay kinds — the viz " +
				"developer requesting both together gets two parallel series layers from a single Facet pass. " +
				"Categorical fast path (host.Kind == \"discrete\"): walks host.Discrete.Values in payload order " +
				"(descending by count, ties ascending by value-string — the canonical FacetSchema sort); each entry emits " +
				"(subset_freq - pop_freq) / sd_pop where sd_pop is the population standard deviation across the " +
				"population's per-category frequencies (sourced from FacetPopulationView.DiscreteFrequencyStdev — the S1 " +
				"resolver's additive accessor reading the Welford-Pébaÿ accumulator that FacetSchema already folded; " +
				"single-pass, allocation-free, byte-equal to the WelfordStdDev recurrence). Numeric path (host.Kind == " +
				"\"numeric\"): walks host.Numeric.Histogram.Bins when the host requested a histogram AND the population " +
				"view carries a Welford summary; each entry emits (bin_center - pop_mean) / pop_sd against the " +
				"population's FacetNumeric.Mean / StdDev (no per-bin histogram alignment required on the population side). " +
				"Streaming finalize hook: the handler runs as a post-finalize fold over the host's already-materialized " +
				"per-value distribution + the resolver's already-materialized Welford state for the population cohort; no " +
				"second pass over records and no widening of the host streaming carrier — the streamable guarantee is " +
				"that running this handler against a streaming vs buffered host produces byte-identical SeriesPayload " +
				"output because the input state is identical. Zero sd_pop path: when the population SD is zero (single-" +
				"entry population OR every category has equal frequency on the discrete arm; constant population on the " +
				"numeric arm), the handler emits ONE PULSE_OVERLAY_REF_ZERO warning per affected entry (discrete arm — " +
				"value-level granularity) OR one layer-level warning (numeric arm — sd_pop is a single layer-scoped " +
				"denominator) and SKIPS the z-score (Statistic stays unset on affected entries). Absent-population value " +
				"on the discrete arm (value missing from the population's discrete payload) yields pop_freq=0 and the " +
				"z-score (subset_freq - 0) / sd_pop is well-defined — emitted without warning (distinct from " +
				"INDEX_VS_POP's zero-denominator arm because subtraction by zero is well-defined). Ref.Population MUST be " +
				"populated (the cohort name lives on Ref.Population.Cohort); any other ref-family pointer (Margin / " +
				"Sibling / BaselineIndex / Prior / RollingMean / YoY / Stage / Slot) fires " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE at predict time (per-kind validator lands in E5-S6 / E5-S7). " +
				"Level / Within MUST be zero (population comparison is a single-value lookup, not an axis prefix); " +
				"non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the implicit-ref family. Scope must be " +
				"GROUP. Streamable — host streaming carriers are unchanged; the overlay reads finalized state only. " +
				"Renderers centre diverging colour ramps on baseline=0 (mirrors OVERLAY_ZSCORE_VS_TOTAL / " +
				"OVERLAY_ZSCORE_VS_MARGIN / OVERLAY_ZSCORE_VS_ROLLING — every z-score family produces a centred " +
				"distribution, not a ratio).",
		}
	case types.OverlayKindZScoreVsRolling:
		return OverlayCapability{
			Kind: types.OverlayKindZScoreVsRolling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// RollingMean is the windowed-axis ref family the kind consumes
			// (sibling to E4-S5 INDEX_VS_ROLLING_MEAN — both kinds share the
			// same ref-arm). The empty marker struct tags the family — the
			// v1 window value lives on OverlaySpec.Params["window"] per the
			// WIN_* operator convention.
			RefKinds: []string{"RollingMean"},
			Description: "Per-point windowed standardized z-score against the rolling-window mean + SAMPLE standard deviation " +
				"(sqrt(M2 / (count - 1)), n-1 denominator) of the W immediately preceding points of an ordered SERIES " +
				"(grouped Process) host: zscore = (point_value - rolling_mean(W)) / rolling_sd(W). GROUP scope over a " +
				"SERIES host with SERIES payload — one SeriesEntry per host group key in host order, each carrying the " +
				"z-score on Summary.Statistic. Fifth windowed-Process overlay in the catalog (E4-S6; siblings: " +
				"OVERLAY_INDEX_VS_PRIOR / E4-S4, OVERLAY_INDEX_VS_BASELINE / E4-S2, OVERLAY_DELTA_VS_BASELINE / E4-S3, " +
				"OVERLAY_INDEX_VS_ROLLING_MEAN / E4-S5) and the second consumer of the Ref.RollingMean arm of the " +
				"OverlayRef discriminated union (sibling windowed-rolling kind to INDEX_VS_ROLLING_MEAN — both kinds " +
				"carry the window width on Params[\"window\"]). " +
				"Variance choice (SAMPLE, NOT population) — KEY CONTRAST with OVERLAY_ZSCORE_VS_TOTAL: the rolling z-score " +
				"uses SAMPLE SD (divide by count - 1) because a rolling window of W observations IS a sample of the wider " +
				"time series; OVERLAY_ZSCORE_VS_TOTAL uses POPULATION SD (sqrt(M2 / N)) because the per-group aggregation " +
				"set IS the whole population. The two surfaces are intentionally orthogonal. " +
				"Window-via-Params convention: OverlaySpec.Params[\"window\"] supplies a positive integer (mirrors the " +
				"WIN_* operator convention; see skills/window-operations.md). Carrier shape (shared with INDEX_VS_ROLLING_MEAN): " +
				"per-group ring buffer of the W most recently observed PRESENT values plus a Welford (count, mean, M2) trio. " +
				"The sibling kind reads only mean; this kind reads BOTH mean and M2. " +
				"Window-fill semantics: when the carrier count < 2 (sample variance requires at least 2 observations) the " +
				"handler emits NaN without warning — \"window not yet filled\" is structurally distinct from \"denominator " +
				"was zero\". When the host series is shorter than W (no ordinal ever sees a full window) every entry's " +
				"Statistic is NaN with no warning. " +
				"Absent-point policy (ring does not advance): an absent host ordinal (resolver reports (0, false)) emits a " +
				"present SeriesEntry whose Summary leaves Statistic unset and DOES NOT advance the ring buffer — the next " +
				"present ordinal compares against the most recent W PRESENT values, not absent slots (mirrors " +
				"OVERLAY_INDEX_VS_ROLLING_MEAN). " +
				"Zero rolling-SD path: when the rolling SD is exactly 0 at the divide step (e.g. every prior point in the " +
				"window has the same value — constant series) the handler emits ONE PULSE_OVERLAY_REF_ZERO warning per " +
				"layer and surfaces NaN on the affected entries. " +
				"Window param validation: missing Params[\"window\"] fires PULSE_OVERLAY_PARAM_MISSING; non-positive or " +
				"non-integer window fires PULSE_OVERLAY_LEVEL_OUT_OF_RANGE (the LEVEL_OUT_OF_RANGE code is reused for " +
				"\"value out of valid range\" semantics so the new code surface stays narrow). " +
				"Ref.RollingMean MUST be populated (empty marker is fine); any other ref-family pointer (Margin / Sibling / " +
				"Prior / BaselineIndex / Population / Stage / Slot) fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. " +
				"Level / Within MUST be zero (windowed kind — the rolling carrier folds across the ordered axis without a " +
				"prefix-bucket denominator); non-zero values fire PULSE_OVERLAY_LEVEL_OUT_OF_RANGE mirroring the " +
				"INDEX_VS_ROLLING_MEAN / INDEX_VS_PRIOR family. Buffered — the ring buffer carries the full window of " +
				"present values (W f64s per group plus the Welford trio), larger than the streamable INDEX_VS_PRIOR " +
				"single-state lag accumulator. Renderers centre diverging colour ramps on baseline=0 (mirrors " +
				"OVERLAY_ZSCORE_VS_TOTAL / OVERLAY_ZSCORE_VS_MARGIN — every z-score family produces a centred distribution).",
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
