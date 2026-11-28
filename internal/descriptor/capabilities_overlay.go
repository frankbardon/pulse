package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/types"
)

// Overlay capability surface — manifest-bound declaration of every
// registered OverlayKind, the shapes / scopes / ref kinds the kind
// supports, and whether it forces the orchestrator down the buffered
// path. The manifest builder calls OverlayCapabilities() and drops the
// returned slice into Manifest.Overlays so LLM clients can detect the
// catalog without inspecting the source.
//
// Per kind-catalog-v1 PRD §I-FR-I2, each entry carries:
//
//   - Kind        — the OverlayKind constant (SCREAMING_SNAKE on wire).
//   - Shapes      — supported OverlayShape values (scalar/series/matrix).
//   - Scopes      — supported OverlayScope values (cell/row/column/...).
//   - RefKinds    — OverlayRef family arms the kind consumes, as the
//                   arm's wire key on OverlayRef (e.g. "margin",
//                   "sibling", "baseline_index") — the key a request
//                   writes under `ref`. TestManifestRefKindsAreWireKeys
//                   holds every value to an OverlayRef JSON tag. The
//                   arm's own discriminator (MarginAxis, …) lives one
//                   level deeper inside the chosen arm.
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
//     or github.com/frankbardon/pulse/internal/processing. Overlay catalog data
//     lives in types/; capability lookups go through types/ constants.
//     TestPredictNoExecutionImports gates predict.go and the rest of
//     the descriptor package follows the same convention.
//   - No fmt.Sprintf in any JSON-bearing path. The OverlayCapability
//     struct is marshalled by the manifest builder through
//     encoding/json; no string formatting happens in this file.

// OverlayCapabilities returns the canonical per-kind capability list
// for the manifest. Deterministic ordering: alphabetised by Kind via
// types.AllOverlayKinds() so the golden manifest stays stable as new
// kinds land. Buffered flags consult types.OverlayStreamable(kind) so
// the static table in types/overlay_streamability.go remains the
// single source of truth for streaming behaviour — a kind that flips
// to streamable automatically flips Buffered to false here.
//
// OVERLAY_INDEX_VS_MARGIN is the seed entry for the catalog:
//
//   - Shapes:   [matrix]   (CELL-scoped overlay layered onto a crosstab)
//   - Scopes:   [cell]     (CELL-only today; ROW/COLUMN/TOTAL land
//     alongside the matching payload shapes)
//   - RefKinds: [margin]   (denominator is an axis-margin slot)
//   - Buffered: true       (margins recompute from raw rows; the host
//     crosstab path is always buffered)
//
// Later kinds drop in by extending the switch below.
// TestStreamability_OverlaysKnown (types/overlay_streamability.go) and
// TestManifestOperatorsComplete-style follow-ups (descriptor) enforce
// that every catalog entry has a row in this surface.
func OverlayCapabilities() []descriptor.OverlayCapability {
	kinds := types.AllOverlayKinds()
	caps := make([]descriptor.OverlayCapability, 0, len(kinds))
	for _, k := range kinds {
		streamable, _ := types.OverlayStreamable(k)
		entry := overlayCapabilityFor(k)
		entry.Description += seriesFieldNoteFor(k)
		entry.Buffered = !streamable
		entry.Zone = ZoneCapabilityOf(string(k))
		entry.Parts = overlayPartsFor(k)
		entry.Params = overlayParamsFor(k)
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

// seriesFieldNoteFor is the params.field note closing kind's
// Description on every manifest surface that lists it (Manifest.Overlays
// and the ProcessChain capability block): the host family's note, else
// "".
func seriesFieldNoteFor(k types.OverlayKind) string {
	switch {
	case seriespartition.Ordered(k) && seriespartition.AppliesCompose(k):
		return seriesFieldNote + seriesPartitionNote + seriesAxisNote + composePartitionNote
	case seriespartition.Ordered(k):
		return seriesFieldNote + seriesPartitionNote + seriesAxisNote
	case seriespartition.Applies(k):
		return seriesFieldNote + seriesPartitionNote + seriesTotalNote
	case seriesfield.Applies(k):
		return seriesFieldNote
	case seriesfield.AppliesCompose(k):
		return composeSeriesFieldNote
	case seriesfield.AppliesChain(k):
		return chainSeriesFieldNote
	}
	return ""
}

// seriesFieldNote closes the Description of every series kind that
// scores one host aggregation (internal/seriesfield): the params.field
// rule, stated once so the twelve rows cannot drift.
const seriesFieldNote = " Measure: on a SERIES host the overlay scores the aggregation whose output label " +
	"params.field names (its label, else TYPE_field)" + elementFieldNote + "; absent, aggregations[0]. " +
	"A label naming no aggregation is PULSE_OVERLAY_FIELD_UNKNOWN (predict and runtime)."

// elementFieldNote is the expanded-element clause every params.field
// note carries (seriesfield.Resolve's element-column arm).
const elementFieldNote = ", or one expanded element column \"<label>[<element label>]\" of a params.expand vector aggregation " +
	"(an element no row answered is a missing point)"

// seriesPartitionNote follows seriesFieldNote on every kind that runs
// once per partition of a composite-row host
// (internal/seriespartition): the params.partition_by rule. An ordered
// kind (seriespartition.Ordered) adds seriesAxisNote; a _TOTAL kind adds
// seriesTotalNote.
const seriesPartitionNote = " Partition: with two or more groupers the overlay runs once per partition, the rows sharing " +
	"the leading group keys params.partition_by names (default every key but the last; [] = one walk over every row); " +
	"partitions match by key equality, so a sort may interleave them. A list that is not a leading prefix of the " +
	"group keys is PULSE_OVERLAY_PARTITION_INVALID (predict and runtime)."

// seriesAxisNote closes the partition note of an ordered kind: the
// series runs down the last grouper, and predict advises when that axis
// is not a date grouper.
const seriesAxisNote = " The series runs down the last grouper; with no partition_by and a last grouper that is not a date grouper, " +
	"predict adds the advisory PULSE_ADVISORY_SERIES_AXIS_NOT_DATE."

// seriesTotalNote closes the partition note of a _TOTAL kind: each row
// is scored against its partition's total, and [] restores the grand
// total.
const seriesTotalNote = " Each row is scored against its partition's total (and, for the z-score, its partition's mean and SD); " +
	"partition_by [] restores the grand total over every row."

// composeSeriesFieldNote closes the Description of every Compose kind
// whose SERIES arm scores one slot aggregation
// (seriesfield.AppliesCompose).
const composeSeriesFieldNote = " Measure: on grouped slots the overlay keys each row on the slot's group keys alone and scores " +
	"the aggregation whose output label params.field names (its label, else TYPE_field)" + elementFieldNote + " on every slot it reads; " +
	"absent, each slot's aggregations[0]. A label a slot lacks is PULSE_OVERLAY_FIELD_UNKNOWN (details.slot; predict and runtime)."

// composePartitionNote closes the Description of a kind that also runs
// per partition on a Compose host (seriespartition.AppliesCompose;
// seriesfield.AppliesCompose holds it too): the same two rules, per slot.
const composePartitionNote = " On a compose request params.field names the aggregation each slot it reads is scored on " +
	"(PULSE_OVERLAY_FIELD_UNKNOWN carries details.slot), and the partition rule splits the first target slot's rows " +
	"(details.slot on a refusal)."

// chainSeriesFieldNote closes the Description of every whole-chain
// kind whose SERIES arm scores one stage aggregation
// (seriesfield.AppliesChain).
const chainSeriesFieldNote = " Measure: on grouped stages the overlay keys each row on the stage's group keys alone and scores " +
	"the aggregation whose output label params.field names (its label, else TYPE_field)" + elementFieldNote + " on the target and the reference stage; " +
	"absent, each stage's aggregations[0]. A label a stage lacks is PULSE_OVERLAY_FIELD_UNKNOWN (details which, stage_index; predict and runtime)."

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
// priorTestCapability is the shared entry of the three inferential
// prior-period kinds (U47 #328), around the kind's test and leg
// sentences.
func priorTestCapability(kind types.OverlayKind, test, legs string) descriptor.OverlayCapability {
	return descriptor.OverlayCapability{
		Kind:        kind,
		Inferential: true,
		Shapes:      []types.OverlayShape{types.OverlayShapeSeries},
		Scopes:      []types.OverlayScope{types.OverlayScopeGroup},
		RefKinds:    []string{"prior"},
		Description: "Tests each period against the present period before it: " + test + ". Hosts: a grouped process request (periods = the last grouper's keys, per params.partition_by partition, scored on params.field) " +
			"or a crosstab with no column grouper (periods = the rows of the implicit Total column, partitioned on the leading row keys); a crosstab with a column grouper is refused. " +
			"Legs: " + legs + ". ref.prior (lag 0) or an empty ref; level / within 0. One series entry per period: statistic (current minus prior) and p_value, " +
			"which joins the request's multiplicity family (p_adjusted / significant_adjusted). The first present period has a null statistic and no p_value; a missing period keeps its key with no statistic and does not advance the prior; " +
			"a degenerate test is null with one PULSE_OVERLAY_REF_ZERO warning per reason. A measure without the moments is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, components disabled PULSE_OVERLAY_COMPONENTS_REQUIRED (predict and runtime). " +
			"Assumes independent samples: panel respondents in both periods violate it.",
	}
}

// pairwiseDescription composes the shared OVERLAY_PAIRWISE_* manifest
// blurb around the per-kind test sentence so every OVERLAY_PAIRWISE_*
// entry stays consistent on the catalog surface.
func pairwiseDescription(test string) string {
	return "Pairwise " + test + " between the host crosstab's own row or column legs: row scope pairs the rows " +
		"within each column, column scope the columns within each row (OVERLAY_PROP_Z_PANEL pairs compose slots instead). " +
		"The payload matrix has one pair-axis entry per tested (i, j) pair, keyed by the two legs' labels, against the " +
		"host's other axis; each cell holds the pair's two-sided p-value (absent when a leg is unreadable or the test " +
		"degenerate). No ref. Optional params: pair_along_dim restricts pairs to same-bucket comparisons; p_source picks " +
		"percentage vs proportion cell values; n_source picks the sample-size leg — omitted, the cell's base (the cell " +
		"itself under normalize none, else the denominator normalize divided it by), its record count or, on a weighted " +
		"host, its N* (sum_weights under kind frequency, Kish n_eff under probability) — or cell_n_unweighted, " +
		"cell_value_weighted, cell_weight_sum, row_margin_n, column_margin_n (on a weighted host the displayed margin's " +
		"N*), n_within, and the distinct-key modes n_within_distinct, row_margin_distinct and column_margin_distinct, " +
		"admitted only on an AGG_DISTINCT_SUM or AGG_DISTINCT_COUNT cell (any other cell aggregator is " +
		"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: AGG_MODE / AGG_MODE_COUNT's distinct_count counts values, not " +
		"keys); n_within_depth pins the within-group denominator of n_within / n_within_distinct, and a fan-out grouper " +
		"below that depth is PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED for n_within_distinct (predict and runtime). " +
		"n_basis belongs to OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z alone (PULSE_OVERLAY_PARAM_MISSING elsewhere). Reads the " +
		"crosstab components: a host with components disabled is PULSE_OVERLAY_COMPONENTS_REQUIRED (predict and runtime). " +
		"p-values are raw; params.letters adds a letters part (per host cell: its letter, comparison group, and the " +
		"letters it is higher than and significantly above). Detail: skill pairwise-n-sources."
}

func overlayCapabilityFor(kind types.OverlayKind) descriptor.OverlayCapability {
	switch kind {
	case types.OverlayKindChiSqCol:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindChiSqCol,
			Inferential: true,
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
			Description: "Per-column χ² goodness-of-fit test of the host crosstab's row distribution. One series entry per " +
				"column key: summary.statistic (χ²), summary.parameters.df (rows − 1) and summary.p_value — the same " +
				"p-values as TEST_CHISQ. No ref (the host's own margins). One PULSE_OVERLAY_EXPECTED_LOW per column " +
				"holding an expected count below 5.",
		}
	case types.OverlayKindChiSqMatrix:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindChiSqMatrix,
			Inferential: true,
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
			Description: "Whole-matrix χ² independence test of the host crosstab's rows × columns. The layer summary carries " +
				"statistic, parameters.df and p_value — the same p-values as TEST_CHISQ. No ref (the host's own " +
				"margins). PULSE_OVERLAY_EXPECTED_LOW when any expected count is below 5.",
		}
	case types.OverlayKindChiSqRow:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindChiSqRow,
			Inferential: true,
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
			Description: "Per-row χ² goodness-of-fit test of the host crosstab's column distribution. One series entry per row " +
				"key: summary.statistic (χ²), summary.parameters.df (columns − 1) and summary.p_value — the same " +
				"p-values as TEST_CHISQ. No ref (the host's own margins). One PULSE_OVERLAY_EXPECTED_LOW per row " +
				"holding an expected count below 5.",
		}
	case types.OverlayKindChiSqVsPop:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindChiSqVsPop,
			Inferential: true,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// shared with INDEX_VS_POP / ZSCORE_VS_POP / KS_VS_POP. The
			// capability row declares the consumed Ref-arm so MCP /
			// manifest clients see the kind requires Ref.Population to be
			// populated; descriptor.validateOverlayChiSqVsPop gates the
			// shape at predict time.
			RefKinds: []string{"population"},
			Description: "χ² goodness-of-fit of a pulse_facet_schema field's subset distribution against a population's " +
				"(ref.population, required; its cohort in ref.population.cohort). The layer summary carries " +
				"statistic, parameters.df and p_value. The compared categories C are the subset's listed values the " +
				"population also shows plus, when the listing is complete (no discrete_top_k cut), every population " +
				"category the subset lacks, at observed 0. expected[v] = share[v] · subset_N with the population " +
				"shares renormalised over C, df = |C| − 1 — R's chisq.test(x, p, rescale.p = TRUE) over C, the same " +
				"p-values as TEST_CHISQ. A subset value the population lacks is left out, one PULSE_OVERLAY_REF_ZERO " +
				"per value. Categorical fields only: a numeric field is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE " +
				"(predict and runtime). Any expected count below 5 warns PULSE_OVERLAY_EXPECTED_LOW once. An empty " +
				"subset, subset_N = 0 or an empty population leaves statistic and p_value null with one " +
				"PULSE_OVERLAY_REF_ZERO; a single category (df 0) leaves p_value null. level / within 0.",
		}
	case types.OverlayKindChiSqVsRef:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindChiSqVsRef,
			Inferential: true,
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
			Description: "Compose overlay: whole-matrix χ² test of each target slot's crosstab against the reference slot's, " +
				"as two independent tables. Over C, the cells the reference shows (ref_cell > 0): expected = ref_cell " +
				"· target_N / ref_N (both totals over C), χ² = Σ_C (target_cell − expected)² / expected, df = |C| − 1 " +
				"— R's chisq.test(target[C], p = ref[C], rescale.p = TRUE), the same p-values as TEST_CHISQ. A target " +
				"count outside C warns PULSE_OVERLAY_REF_ZERO once per cell. payload.scalar is the p-value; the layer " +
				"summary carries statistic, parameters.df and p_value. target_N 0, ref_N 0 or every expected 0 leaves " +
				"both null with one PULSE_OVERLAY_REF_ZERO; any expected count below 5 warns " +
				"PULSE_OVERLAY_EXPECTED_LOW once. " + composeFanOutNote,
		}
	case types.OverlayKindDeltaVsBaseline:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindDeltaVsBaseline,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// BaselineIndex is the windowed positional-anchor ref family
			// shared with OVERLAY_INDEX_VS_BASELINE. The capability row
			// declares the consumed Ref-arm so MCP / manifest clients see
			// the kind requires Ref.BaselineIndex.Position to be populated;
			// descriptor.validateOverlayDeltaVsBaseline gates the shape at
			// predict time.
			RefKinds: []string{"baseline_index"},
			Description: "Per-point difference from one fixed baseline point of a SERIES host: point_value − " +
				"baseline_value, on each entry's summary.statistic, in the host value's units. " +
				"ref.baseline_index.position (required) is the 0-based baseline point; the baseline point itself " +
				"scores 0. A zero baseline is valid (no warning). A negative or out-of-range position is " +
				"PULSE_OVERLAY_REF_UNKNOWN (details baseline_index, series_length; predict and runtime). A missing " +
				"point keeps its key with no statistic. level / within 0. Additive twin of OVERLAY_INDEX_VS_BASELINE; " +
				"centred on 0.",
		}
	case types.OverlayKindDeltaVsMargin:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindDeltaVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Per-cell difference from the matching crosstab margin (ref.margin.axis row, column or grand): cell − " +
				"margin, in the cell's units. Never warns PULSE_OVERLAY_REF_ZERO; centred on 0. level / within " +
				"truncate the margin to a nested-axis prefix.",
		}
	case types.OverlayKindDeltaVsRef:
		return descriptor.OverlayCapability{
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
			Description: "Compose overlay: each target slot's value minus the reference slot's matching value, per crosstab " +
				"cell (cell scope) or per group of a grouped slot (group scope), in the target's units. A zero " +
				"reference is valid (no warning); a key the reference lacks warns PULSE_OVERLAY_REF_ZERO with " +
				"details.ref_missing true. Additive twin of OVERLAY_INDEX_VS_REF; centred on 0. The series form " +
				"streams; the crosstab form is buffered. " + composeFanOutNote,
		}
	case types.OverlayKindDeltaVsSibling:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindDeltaVsSibling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"sibling"},
			Description: "Per-group difference from one sibling group of a SERIES host: group_val − sibling_val, on " +
				"each entry's summary.statistic, in the host value's units. ref.sibling {field, value} (both " +
				"required) names a grouper field and the key value to compare against; the sibling itself scores 0. " +
				"An unknown field or unobserved value is PULSE_OVERLAY_REF_UNKNOWN with every statistic null. A zero " +
				"sibling is valid (no warning). A missing group keeps its key with no statistic. Centred on 0.",
		}
	case types.OverlayKindDeltaVsStage:
		return descriptor.OverlayCapability{
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
			// ChainRequest.Stages directly.
			RefKinds: []string{"stage"},
			Description: "Process-chain overlay: target − reference per coordinate between two stages' results, in the target " +
				"stage's shape (scalar, series or matrix). ref names the reference stage ({index} or {name}; " +
				"required); target defaults to the last stage. An unknown stage is PULSE_OVERLAY_TARGET_UNKNOWN " +
				"(target) or PULSE_OVERLAY_REFERENCE_UNKNOWN (ref). Stages of different shapes warn " +
				"PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (predict and runtime) and the layer is empty. A zero " +
				"reference is valid (no warning). Scope total; level / within 0. Additive twin of " +
				"OVERLAY_INDEX_VS_STAGE; centred on 0.",
		}
	case types.OverlayKindFisherExactCell:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindFisherExactCell,
			Inferential: true,
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
			Description: "Per-cell Fisher's exact two-sided test on the 2×2 table built from the cell, its row margin, its " +
				"column margin and the grand total; each cell holds the p-value — the same as TEST_FISHER_EXACT. No " +
				"ref (the host's own margins). PULSE_OVERLAY_EXPECTED_LOW per cell when Cochran's rule fires on its " +
				"2×2 (an expected count below 1, or 20% or more below 5).",
		}
	case types.OverlayKindPairwiseProbitT:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPairwiseProbitT,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn},
			RefKinds:    []string{},
			Description: pairwiseDescription("probit t-test (Φ⁻¹ transform of each leg's proportion, Student-t tail on df = n_i + n_j - 2)"),
		}
	case types.OverlayKindPairwisePropZ:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPairwisePropZ,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn},
			RefKinds:    []string{},
			Description: pairwiseDescription("pooled-SE two-proportion z-test (the same p-values as OVERLAY_PROP_Z_CELL and TEST_PROP_Z)"),
		}
	case types.OverlayKindPairwiseTwoMeansZ:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPairwiseTwoMeansZ,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn},
			RefKinds:    []string{},
			Description: pairwiseDescription("two-means z-test on AGG_WELFORD cells (normal-CDF tail, no df adjustment; reads each cell's {mean, variance, n} from the crosstab components). A cell whose component schema lacks mean and variance is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (predict and runtime). The test reads the sample variance m2/(n−1): an unweighted AGG_VARIANCE / AGG_STDDEV cell (population variance m2/n) is converted, so it scores as AGG_WELFORD; an extension cell must expose the sample variance. n_source and p_source are refused, PULSE_OVERLAY_PARAM_MISSING (predict and runtime): n, mean and variance all come from the triple"),
		}
	case types.OverlayKindPairwiseWeightedTwoMeansZ:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPairwiseWeightedTwoMeansZ,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn},
			RefKinds:    []string{},
			Description: pairwiseDescription("two-means z-test on AGG_WEIGHTED_MEAN or weighted AGG_AVERAGE cells (normal-CDF tail; reads each cell's weighted moments {weighted_mean, m2_weighted, sum_weights, sum_weights_sq} from the crosstab components, never the unweighted n). params.n_basis is REQUIRED, no default: weights (var = m2/(Σw−1), n = Σw; frequency weights only — on a probability-weighted cell, the weight_field sugar included, it is PROCESSING_CONFIG at predict and runtime) or kish (var = m2/(Σw−Σw²/Σw), n = n_eff = (Σw)²/Σw²). A leg whose convention is undefined (weights: Σw ≤ 1; kish: all weight on one row) or a zero-SE pair skips under one aggregated PULSE_OVERLAY_REF_ZERO warning. Any other host fires PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (at predict when the cell names another built-in aggregator or an AGG_AVERAGE no weight resolves on, at runtime off the cell components). n_source and p_source are NOT accepted (PULSE_OVERLAY_PARAM_MISSING, predict and runtime); neither is a missing or unknown n_basis. The cell aggregator is mergeable, so the crosstab stays on the fused path"),
		}
	case types.OverlayKindPairwiseWelchT:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPairwiseWelchT,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow, types.OverlayScopeColumn},
			RefKinds:    []string{},
			Description: pairwiseDescription("Welch–Satterthwaite t-test on AGG_WELFORD cells (Welch SE + Satterthwaite df, Student-t tail; reads each cell's {mean, variance, n} from the crosstab components). A cell whose component schema lacks mean and variance is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (predict and runtime). The test reads the sample variance m2/(n−1): an unweighted AGG_VARIANCE / AGG_STDDEV cell (population variance m2/n) is converted, so it scores as AGG_WELFORD; an extension cell must expose the sample variance. n_source and p_source are refused, PULSE_OVERLAY_PARAM_MISSING (predict and runtime): n, mean and variance all come from the triple"),
		}
	case types.OverlayKindFormula:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindFormula,
			Shapes: []types.OverlayShape{
				// Every shape FORMULA computes on some slot: MATRIX and
				// SERIES on Request.Overlays (a crosstab, a grouped
				// Process — overlayhost.Shapes; SERIES since U35 #432)
				// and on Compose; SCALAR on ComposedRequest.Overlays only
				// (U46 E4-S15).
				types.OverlayShapeMatrix,
				types.OverlayShapeScalar,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// Per the formula-namespace research note § 2:
				//   - MATRIX hosts ⇒ CELL scope (one value per (row,col)).
				//   - SERIES hosts ⇒ GROUP scope (one value per ordered
				//     group key).
				//   - SCALAR hosts ⇒ TOTAL scope (single grand value
				//     covering the whole result).
				// The predict gate (descriptor.validateFormulaOverlay)
				// resolves the host shape via inferRequestHostShape and
				// the per-shape variable table at types.FormulaNamespace
				// routes the right namespace into the AST walker. The
				// runtime dispatch (per-shape binder) emits the matching
				// payload shape.
				types.OverlayScopeCell,
				types.OverlayScopeGroup,
				types.OverlayScopeTotal,
			},
			// No Ref family — FORMULA sources its per-shape variable
			// namespace (cell / margin_* / sd_* / value / total / prior
			// / ref_cell / etc.) directly from the materialised host
			// payload. Compose-host FORMULA surfaces the dotted
			// `slot.<label>.{cell|value}` namespace via the per-shape
			// binder and reuses the existing ComposeOverlaySpec.Reference
			// / Targets name-resolution machinery — no new ref family
			// pointer is required (see research note
			// .planning/result-overlay-system/research/formula-namespace.md
			// § 1 "OverlayRef back-pressure status: NONE"). The locked
			// Six (Margin / Sibling / BaselineIndex / Population /
			// Stage / Slot) plus the per-shape host payload already
			// covers every variable; FORMULA does not consume any
			// OverlayRef pointer at predict or runtime.
			RefKinds: []string{},
			Description: "User-defined overlay: params.formula, an expression (the ATTR_FORMULA / FILTER_EXPRESSION language: " +
				"sqrt / log / exp / pow, the set helpers, lookup() over a registered lookup table and embedder " +
				"functions) evaluated per cell, entry or scalar over a namespace set by the host. On a request's " +
				"overlays: a crosstab host binds cell, margin_row, margin_col, margin_grand, sd_row, sd_col, " +
				"sd_grand; a SERIES host (scope group, empty ref) binds, per row, value, total (the " +
				"partition's sum), prior (the previous present row's value, null on the first) and baseline, " +
				"reachable only when params.baseline_position names a 0-based row (a non-negative integer, else " +
				"PULSE_OVERLAY_LEVEL_OUT_OF_RANGE; past the partition PULSE_OVERLAY_REF_UNKNOWN — at predict too when " +
				"every in-partition grouper is a GROUP_CATEGORY over a categorical field). On a compose request's " +
				"overlays the host is the first target slot, in the reference slot's shape: a crosstab adds ref_cell; " +
				"a series binds value, total, prior and ref_value; a scalar binds value and ref; every shape adds " +
				"`slot.<label>.cell` (crosstab) / `slot.<label>.value` for the reference and each target, aligned by " +
				"key (no figure at a coordinate gives null). The result must be a number or a bool (0 / 1), else " +
				"PULSE_OVERLAY_FORMULA_TYPE_MISMATCH. An identifier, function or slot path outside the namespace is " +
				"PULSE_OVERLAY_FORMULA_INVALID_IDENT, and a missing, empty or non-string params.formula " +
				"PULSE_OVERLAY_PARAM_MISSING (predict and runtime).",
		}
	case types.OverlayKindIndexVsBaseline:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsBaseline,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// BaselineIndex is the windowed positional-anchor ref family
			// shared with OVERLAY_DELTA_VS_BASELINE. The capability row
			// declares the consumed Ref-arm so MCP / manifest clients see
			// the kind requires Ref.BaselineIndex.Position to be populated;
			// descriptor.validateOverlayIndexVsBaseline gates the shape at
			// predict time.
			RefKinds: []string{"baseline_index"},
			Description: "Per-point index against one fixed baseline point of a SERIES host: (point_value / " +
				"baseline_value) · 100, on each entry's summary.statistic. ref.baseline_index.position (required) is " +
				"the 0-based baseline point, which scores 100. A zero or missing baseline warns " +
				"PULSE_OVERLAY_REF_ZERO once (baseline ordinal and group count in details) and every statistic is " +
				"null. A negative or out-of-range position is PULSE_OVERLAY_REF_UNKNOWN (details baseline_index, " +
				"series_length; predict and runtime). A missing point keeps its key with no statistic. level / within " +
				"0. Centred on 100.",
		}
	case types.OverlayKindIndexVsMargin:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Per-cell index against the matching crosstab margin (ref.margin.axis row, column or grand): 100 · " +
				"cell / margin. level / within truncate the margin to a nested-axis prefix.",
		}
	case types.OverlayKindIndexVsPop:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// shared with CHISQ_VS_POP / ZSCORE_VS_POP / KS_VS_POP. The
			// capability row declares the consumed Ref-arm so MCP /
			// manifest clients see the kind requires Ref.Population to be
			// populated; descriptor.validateOverlayIndexVsPop gates the
			// shape at predict time.
			RefKinds: []string{"population"},
			Description: "Per-value index of a pulse_facet_schema field's subset frequency against a population's " +
				"(ref.population, required; its cohort in ref.population.cohort): subset_freq / pop_freq · 100, one " +
				"series entry per facet value in the facet's order (count descending, ties by value) on " +
				"summary.statistic. A numeric field is scored per histogram bin, only when the facet requests a " +
				"histogram and the population carries one of the same shape; without one the layer has no entries " +
				"(use OVERLAY_KS_VS_POP). A value the population lacks or holds at 0 warns PULSE_OVERLAY_REF_ZERO " +
				"once per entry and has no statistic. level / within 0. Centred on 100.",
		}
	case types.OverlayKindIndexVsPrior:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsPrior,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Prior is the windowed-axis lag-N ref family — v1 ships lag-1
			// only via Ref.Prior or an entirely empty Ref. Listed in the
			// capability surface so MCP / manifest clients see the kind
			// consumes the Prior arm even though the v1 authoring shape
			// can leave Ref entirely empty.
			RefKinds: []string{"prior"},
			Description: "Per-point index against the previous present point of a SERIES host: (point_value " +
				"/ prior_value) · 100, on each entry's summary.statistic. ref.prior (lag 0) or an empty ref. The " +
				"first present point is null (no warning); a zero prior warns PULSE_OVERLAY_REF_ZERO and that entry " +
				"is null. A missing point keeps its key with no statistic and does not advance the prior. level / " +
				"within 0. Centred on 100.",
		}
	case types.OverlayKindDeltaVsPrior:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindDeltaVsPrior,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Same Prior arm as the index twin above, and the same v1
			// lag-1-only shape.
			RefKinds: []string{"prior"},
			Description: "Per-point difference from the previous present point of a SERIES host: point_value " +
				"− prior_value, on each entry's summary.statistic — \"how much did this change\", where " +
				"OVERLAY_INDEX_VS_PRIOR answers \"what proportion of the prior is this\". ref.prior (lag 0) or an " +
				"empty ref. The first present point is null; a zero prior is valid (no warning). A missing point " +
				"keeps its key with no statistic and does not advance the prior. level / within 0. Centred on 0.",
		}
	case types.OverlayKindIndexVsRef:
		return descriptor.OverlayCapability{
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
			Description: "Compose overlay: each target slot's value over the reference slot's matching value, (target / ref) · " +
				"scale (params.scale, default 100; 1 for a raw ratio), per crosstab cell (cell scope) or per group of " +
				"a grouped slot (group scope). A zero reference, or a key the reference lacks (details.ref_missing " +
				"true), warns PULSE_OVERLAY_REF_ZERO and that coordinate is null. Ratio twin of OVERLAY_DELTA_VS_REF; " +
				"centred on scale. The series form streams; the crosstab form is buffered. " + composeFanOutNote,
		}
	case types.OverlayKindIndexVsRollingMean:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsRollingMean,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// RollingMean is the windowed-axis ref family the kind
			// consumes, shared with OVERLAY_ZSCORE_VS_ROLLING. The empty
			// marker struct tags the family — the v1 window value lives
			// on OverlaySpec.Params["window"] per the WIN_* operator
			// convention.
			RefKinds: []string{"rolling_mean"},
			Description: "Per-point index against the mean of the W previous present points of a SERIES host" +
				":  point_value / mean(point[i-W] .. point[i-1]) · 100, on each entry's summary.statistic. " +
				"ref.rolling_mean (required, an empty object) and params.window (W, a positive integer: missing is " +
				"PULSE_OVERLAY_PARAM_MISSING, non-positive or non-integer PULSE_OVERLAY_LEVEL_OUT_OF_RANGE). A " +
				"point with fewer than params.min_periods present prior points (default W) is null (no warning); a zero rolling mean warns PULSE_OVERLAY_REF_ZERO once " +
				"per layer and those entries are null. A missing point keeps its key with no statistic and does not " +
				"enter the window. level / within 0. Centred on 100.",
		}
	case types.OverlayKindIndexVsSibling:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindIndexVsSibling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"sibling"},
			Description: "Per-group index against one sibling group of a SERIES host: (group_val / sibling_val) · " +
				"100, on each entry's summary.statistic. ref.sibling {field, value} (both required) names a grouper " +
				"field and the key value to compare against; the sibling itself scores 100. An unknown field or " +
				"unobserved value is PULSE_OVERLAY_REF_UNKNOWN, and a zero sibling PULSE_OVERLAY_REF_ZERO, each with " +
				"every statistic null. A missing group keeps its key with no statistic. Centred on 100.",
		}
	case types.OverlayKindIndexVsStage:
		return descriptor.OverlayCapability{
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
			// ChainRequest.Stages directly.
			RefKinds: []string{"stage"},
			Description: "Process-chain overlay: (target / reference) · 100 per coordinate between two stages' results, in the " +
				"target stage's shape (scalar, series or matrix). ref names the reference stage ({index} or {name}; " +
				"required); target defaults to the last stage. An unknown stage is PULSE_OVERLAY_TARGET_UNKNOWN " +
				"(target) or PULSE_OVERLAY_REFERENCE_UNKNOWN (ref). Stages of different shapes warn " +
				"PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT (predict and runtime) and the layer is empty. A zero " +
				"reference warns PULSE_OVERLAY_REF_ZERO and that coordinate is null. Scope total; level / within 0. " +
				"Centred on 100.",
		}
	case types.OverlayKindIndexVsTotal:
		return descriptor.OverlayCapability{
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
			Description: "Per-group index against the grand total of a SERIES host: (group_val / grand_total) · 100, " +
				"on each entry's summary.statistic. No ref. A zero grand total warns PULSE_OVERLAY_REF_ZERO and every " +
				"statistic is null. A missing group keeps its key with no statistic.",
		}
	case types.OverlayKindKSVsPop:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindKSVsPop,
			Inferential: true,
			Shapes: []types.OverlayShape{
				types.OverlayShapeScalar,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// shared with INDEX_VS_POP / ZSCORE_VS_POP / CHISQ_VS_POP. The
			// capability row declares the consumed Ref-arm so MCP /
			// manifest clients see the kind requires Ref.Population to be
			// populated; descriptor.validateOverlayKSVsPop gates the shape
			// at predict time.
			RefKinds: []string{"population"},
			Description: "Two-sample Kolmogorov-Smirnov test of a numeric pulse_facet_schema field's subset distribution " +
				"against a population's (ref.population, required; its cohort in ref.population.cohort): D = sup_x " +
				"|F_subset(x) − F_pop(x)|. payload.scalar is D; the layer summary carries statistic, p_value and " +
				"parameters n_subset, n_pop. The population keeps summaries, not raw values, so the CDFs come from " +
				"(1) matching histograms (same min, max and bin count on both sides; ask the facet for a histogram), " +
				"then p is R ks.test's on the bucketed samples, exact on the ties when m·n < 10000 — what TEST_KS " +
				"reports on the bucket indices; else (2) common percentile labels, with the limiting Kolmogorov p, " +
				"ks.test(exact = FALSE); else both null with one PULSE_OVERLAY_REF_ZERO whose details say which facet " +
				"options were missing. Precision is bounded by the bin width or percentile spacing. An empty subset " +
				"or population leaves both null with PULSE_OVERLAY_REF_ZERO. A categorical field is refused, " +
				"PULSE_OVERLAY_SCOPE_UNSUPPORTED (use OVERLAY_CHISQ_VS_POP). level / within 0.",
		}
	case types.OverlayKindPanelIndexVsRef:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindPanelIndexVsRef,
			Shapes: []types.OverlayShape{
				// Dual-shape: every emitted layer mirrors its (reference,
				// target[i]) pair's INDEX_VS_REF output, which is itself
				// dual-shape — MATRIX host emits MATRIX payload, SERIES
				// host emits SERIES payload. The kind inherits both shape
				// arms from the single-target sibling.
				types.OverlayShapeMatrix,
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// Mirrors OVERLAY_INDEX_VS_REF — MATRIX host carries CELL
				// scope, SERIES host carries GROUP scope.
				types.OverlayScopeCell,
				types.OverlayScopeGroup,
			},
			// COMPOSE-only multi-ref kind — Reference + Targets resolve
			// via the ComposeOverlaySpec slot-label pair. RefKinds stays
			// empty: every target is indexed against the SHARED reference
			// slot; no OverlayRef family pointer applies.
			RefKinds: []string{},
			Description: "Compose overlay: OVERLAY_INDEX_VS_REF for each target slot against one shared reference slot, one " +
				"layer per target in targets order, named `<name>__<target>` (`<kind>__<target>` with no name); each " +
				"layer equals the single-pair OVERLAY_INDEX_VS_REF for its slots. Every (reference, target) pair " +
				"shares one key space (checked before any layer is computed). More targets than " +
				"options.max_panel_targets (default 16) is PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP (details observed, " +
				"cap). The series form streams; the crosstab form is buffered. Centred on scale (default 100).",
		}
	case types.OverlayKindPropZCell:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPropZCell,
			Inferential: true,
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
			Description: "Compose overlay: per-cell two-proportion z-test of each target slot's crosstab against the reference " +
				"slot's matching cell; each cell holds the two-sided p-value. A cell is a success count and its row " +
				"margin the sample size: p = cell / row_margin per side, pooled = (target_cell + ref_cell) / " +
				"(target_row_margin + ref_row_margin), se = sqrt(pooled · (1 − pooled) · (1/n_target + 1/n_ref)), z = " +
				"(p_target − p_ref) / se — the same p-values as TEST_PROP_Z. pooled 0 or 1, or a missing or zero row " +
				"margin on either side (the cell value is never used as n), leaves the cell null with one " +
				"PULSE_OVERLAY_REF_ZERO (details.margin_missing true for a margin). " + composeFanOutNote,
		}
	case types.OverlayKindPropZPanel:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindPropZPanel,
			Inferential: true,
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
			Description: "Compose overlay: per-cell pairwise two-proportion z-tests across the reference slot and every target " +
				"slot (M = targets + 1 slots, reference first, then targets in order). Each cell holds the row-major " +
				"upper triangle of pairwise two-sided p-values without the diagonal — [(0,1), (0,2), ..., (M-2,M-1)], " +
				"M(M−1)/2 entries — each the same as OVERLAY_PROP_Z_CELL for that pair. More targets than " +
				"options.max_panel_targets (default 16) is PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP (details observed, " +
				"cap), judged first. A degenerate pair (pooled 0 or 1, a missing row margin) is null with one " +
				"PULSE_OVERLAY_REF_ZERO per (cell, pair); a cell some slot lacks is empty with one " +
				"PULSE_OVERLAY_REF_ZERO (details.ref_missing true). params.n_source picks where each slot's sample " +
				"size is read, once per slot: row_margin_value (default; the row margin value, a missing or zero " +
				"margin reading as n = 0), cell_n_unweighted (the counted cell n from the crosstab components), " +
				"row_margin_value_within (the row margin summed over a row-key prefix) or row_margin_distinct_within " +
				"(the distinct-key cardinality from the row margin components, for one respondent with several " +
				"records; every slot's cell aggregator must be the same AGG_DISTINCT_SUM or AGG_DISTINCT_COUNT, else " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, at runtime). params.n_within_depth (the within modes " +
				"only) sums each slot's row margins over the rows sharing the first depth + 1 row keys; a negative " +
				"depth or a depth beside another mode is PULSE_OVERLAY_PARAM_MISSING, a depth past a slot's row axis " +
				"the same code at runtime, and a fan-out grouper below the depth " +
				"PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED. One offending slot refuses the whole spec. A counted " +
				"mode on a slot with components disabled is PULSE_OVERLAY_COMPONENTS_REQUIRED. Any other n_source " +
				"(including the pairwise family's n_within and row_margin_n) is PULSE_OVERLAY_PARAM_MISSING naming " +
				"the valid set. Detail: skill panel-n-sources.",
		}
	case types.OverlayKindRank:
		return descriptor.OverlayCapability{
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
			Description: "Compose overlay: the 1-based rank (1 = largest, ties averaged as scipy's rankdata) of each target " +
				"slot's crosstab cell within params.population: row (its row), column (its column) or matrix (every " +
				"present cell; default). Absent cells stay absent and are not ranked. The reference slot is required " +
				"for slot resolution and key alignment but is not read. " + composeFanOutNote,
		}
	case types.OverlayKindShareOfCol:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindShareOfCol,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Per-cell share of its column (ref.margin.axis column): cell / column margin. A column's cells sum to " +
				"1 when none is missing. level / within truncate the margin to a nested-axis prefix.",
		}
	case types.OverlayKindShareOfRow:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindShareOfRow,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Per-cell share of its row (ref.margin.axis row): cell / row margin. A row's cells sum to 1 when none " +
				"is missing. level / within truncate the margin to a nested-axis prefix.",
		}
	case types.OverlayKindShareOfTotal:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindShareOfTotal,
			Shapes: []types.OverlayShape{
				// MATRIX: per-cell ratio cell / grand_total against a
				// crosstab host.
				types.OverlayShapeMatrix,
				// SERIES: per-group ratio group_val / grand_total against
				// a grouped Process host.
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				// CELL: paired with the MATRIX shape on a crosstab host.
				types.OverlayScopeCell,
				// GROUP: paired with the SERIES shape on a grouped
				// Process host.
				types.OverlayScopeGroup,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Share of the grand total. On a crosstab host, per cell: cell / grand total (cell scope; ref.margin, " +
				"its axis ignored; level / within accepted but inert). On a SERIES host, per group: " +
				"group_val / grand_total on each entry's summary.statistic, summing to 1 over a complete partition " +
				"(group scope, empty ref). A zero grand total warns PULSE_OVERLAY_REF_ZERO and every statistic is " +
				"null; a missing group keeps its key with no statistic. The series form streams; the crosstab form is " +
				"buffered.",
		}
	case types.OverlayKindCorrespondence:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindCorrespondence,
			// Descriptive: no p-value, so no multiplicity site.
			Inferential: false,
			Shapes:      []types.OverlayShape{types.OverlayShapeSeries},
			Scopes:      []types.OverlayScope{types.OverlayScopeMatrix},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Simple correspondence analysis (R's ca::ca) of the host crosstab — a perceptual map placing rows and " +
				"columns in a few shared dimensions — from the SVD of the standardized residual matrix S = D_r^-1/2 " +
				"(P - r c^T) D_c^-1/2. The primary series holds the principal inertia of each of the min(r, c) - 1 " +
				"dimensions (keys dim_1, dim_2, ...; entry parameters share, cumulative_share, singular_value); " +
				"summary.statistic is the total inertia (chi-square / n) and summary.parameters carry dimensions, " +
				"retained_share and share_dim_<k>. payload.parts: row_coordinates / column_coordinates (matrix, " +
				"points x retained dimensions, columns keyed dim_1 ...), row_quality / column_quality (series, cos^2 " +
				"summed over the retained dimensions; parameters mass, inertia), row_contribution / " +
				"column_contribution (matrix, each point's share of a dimension's inertia) and row_distance / " +
				"column_distance (matrix, points x points: the pairwise chi-square distances between profiles — " +
				"symmetric, 0 diagonal, independent of dimensions and normalization). params: dimensions (integer, " +
				"default 2 capped at min(r, c) - 1; an explicit value above it is PULSE_OVERLAY_PARAM_MISSING) and " +
				"normalization (symmetric [default: both principal] | row_principal | column_principal). Signs: per " +
				"dimension the dominant component of the column singular vector is positive. Cells must be " +
				"non-negative masses: a cell aggregation other than AGG_COUNT / AGG_SUM, or a negative cell, is " +
				"PULSE_OVERLAY_NONNEGATIVE_REQUIRED. Zero-total rows / columns are dropped with one " +
				"PULSE_OVERLAY_REF_ZERO per axis; fewer than 2 x 2 kept points is " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE. Scale-invariant: a weighted host's sum-of-weights table " +
				"is read as is under either weight kind. Descriptive (no p-value).",
		}
	case types.OverlayKindMarkov:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindMarkov,
			// Descriptive: no p-value, so no multiplicity site.
			Inferential: false,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeMatrix},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Markov-chain projection of a from-state (rows) x to-state (columns) transition crosstab. The row and " +
				"column key sets must be equal as sets (states align by key, never by position, in row order), else " +
				"PULSE_OVERLAY_MATRIX_NOT_SQUARE (predict refuses different grouper counts on the two axes; runtime " +
				"also refuses differing key sets) — except that a column-only state named in params.absorbing gets a " +
				"synthesized unit row (appended after the rows; a PULSE_OVERLAY_REF_ZERO warning with reason " +
				"absorbing_row_synthesized and summary.parameters.synthesized_rows). The primary matrix is the " +
				"row-stochastic transition matrix P (each row over its total); a zero row becomes the unit row (the " +
				"state absorbs) with a PULSE_OVERLAY_REF_ZERO warning. payload.parts: step_<n> (matrix P^n for each " +
				"params.steps entry), distribution_<n> (series initial x P^n, only with params.initial), steady_state " +
				"(series, power iteration to 1e-12 within 10,000 iterations, else the exact linear solve; only for an " +
				"irreducible aperiodic chain — otherwise the PULSE_OVERLAY_MARKOV_NOT_ERGODIC warning and no part) " +
				"and absorption_time (series over the non-absorbing states: expected steps to reach the " +
				"params.absorbing set, (I - Q)^-1 1; null with a PULSE_OVERLAY_MARKOV_NOT_ERGODIC warning for a state " +
				"that may never be absorbed; only with params.absorbing). summary.parameters: states, classes, " +
				"irreducible, period, steady_state_iterations, steady_state_converged, synthesized_rows (only when > " +
				"0). params: steps (default [1, 3, 6, 12]; distinct integers 1-1000, at most 64), absorbing (state " +
				"keys present in the host) and initial (\"margin\" = the observed row-margin distribution, or a state " +
				"-> share map over host states, normalized; omitted states start at 0); a bad or unknown param, or a " +
				"key the host lacks, is PULSE_OVERLAY_PARAM_MISSING. Cells must be non-negative masses: a cell " +
				"aggregation other than AGG_COUNT / AGG_SUM, or a negative cell, is " +
				"PULSE_OVERLAY_NONNEGATIVE_REQUIRED. Scale-invariant: a weighted host's sum-of-weights table is read " +
				"as is under either weight kind. Descriptive (no p-value).",
		}
	case types.OverlayKindRake:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindRake,
			// Descriptive: no p-value, so no multiplicity site.
			Inferential: false,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeMatrix},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Raking (iterative proportional fitting) of a crosstab to target margins, without mutating the host. " +
				"Each sweep scales every row to its target, then every column; sweeps stop once the largest relative " +
				"margin error is within params.tolerance or params.max_iter have run. The primary matrix is the " +
				"fitted table on the host keys (a zero cell stays zero: structural zeros are kept). payload.parts: " +
				"factors (matrix fitted / observed, null where the observed cell is 0). summary.parameters: " +
				"iterations, residual (the final largest relative margin error), converged (1 / 0); a fit short of " +
				"the tolerance still emits, converged 0, with the PULSE_OVERLAY_RAKE_NOT_CONVERGED warning. params: " +
				"row_targets and col_targets (required key -> value maps naming every host key of their axis and no " +
				"other, else PULSE_OVERLAY_RAKE_TARGET_MISMATCH — predict refuses a target key outside a categorical " +
				"axis's dictionary, runtime any mismatch with the observed keys), targets_as (\"shares\" default: " +
				"each axis normalized and scaled to the host grand total; \"counts\": taken as given, and row and " +
				"column totals that differ are PULSE_OVERLAY_RAKE_TARGET_MISMATCH), max_iter (default 100, 1-100,000) " +
				"and tolerance (default 1e-8, above 0 and below 1); a bad or unknown param is " +
				"PULSE_OVERLAY_PARAM_MISSING. Cells must be non-negative masses: a cell aggregation other than " +
				"AGG_COUNT / AGG_SUM, or a negative cell, is PULSE_OVERLAY_NONNEGATIVE_REQUIRED. A weighted host's " +
				"sum-of-weights table is raked as is under either weight kind. It writes no weights back to a cohort. " +
				"Descriptive (no p-value).",
		}
	case types.OverlayKindMatrixGroupSummary:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindMatrixGroupSummary,
			// Descriptive: no p-value, so no multiplicity site.
			Inferential: false,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeMatrix},
			RefKinds:    []string{"matrix"},
			Description: "Summary of a grouped matrix result across its group buckets: an overlay on a request (a process " +
				"request or a compose slot) carrying matrices and groups and no crosstab. ref.matrix.name names the " +
				"matrices entry by its effective name (name, else <TYPE>_<vector> / <TYPE>; unknown is " +
				"PULSE_OVERLAY_REFERENCE_UNKNOWN), and every bucket's primary matrix is read (an upper encoding " +
				"expanded first) after the multiplicity fold. The primary matrix is the per-cell unweighted mean " +
				"across buckets; payload.parts: min, max, range and n_groups (a matrix each). Keys are the union of " +
				"the buckets' row / column keys; a null cell or a key a bucket lacks contributes nothing, and a cell " +
				"no bucket contributes to is null (n_groups 0). An ungrouped request is " +
				"PULSE_OVERLAY_GROUPS_REQUIRED. No params; scope matrix; level / within 0. Refused on the compose, " +
				"chain and facet overlay slots and on a process-chain stage. Descriptive (no p-value).",
		}
	case types.OverlayKindStdResidual:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindStdResidual,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeMatrix},
			Scopes:      []types.OverlayScope{types.OverlayScopeCell},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Haberman adjusted standardized residual of every host crosstab cell, (O - E) / sqrt(E (1 - r/N) (1 - " +
				"c/N)) with E = r c / N — R's chisq.test()$stdres. Each cell holds its residual (about standard " +
				"normal under independence; |z| > 1.96 marks a cell driving the table's chi-square). payload.parts " +
				"carries \"expected\" (the expected counts) and \"p\" (the two-sided normal p-value of each " +
				"residual), both matrices on the host coordinates; a multiplicity block adjusts the p part into " +
				"payload.p_adjusted / significant_adjusted. No ref (the host's own margins). Weighted hosts follow " +
				"OVERLAY_CHISQ_MATRIX (the Σw table; under kind probability scaled to its Kish n_eff first). A " +
				"zero-expected cell's residual and p are null; any expected count below 5 emits one " +
				"PULSE_OVERLAY_EXPECTED_LOW. A host smaller than 2 x 2 or with a zero grand total is " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE.",
		}
	case types.OverlayKindRetention:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindRetention,
			// Descriptive: no p-value, so no multiplicity site.
			Inferential: false,
			Shapes:      []types.OverlayShape{types.OverlayShapeSeries},
			Scopes:      []types.OverlayScope{types.OverlayScopeRow},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Retention rates of a from-state (rows) x to-state (columns) crosstab: one series entry per " +
				"from-state, in row order, with summary.statistic = stay (the share of the row's total in the column " +
				"naming the same state — aligned by key, never by position; no such column = 0) and " +
				"summary.parameters switch (the share that moved to any other state), from_total (the row's total) " +
				"and, with params.absorbing, churn (the share that moved into a named absorbing state, so stay + " +
				"switch + churn = 1). A from-state named in params.absorbing is not reported. The layer " +
				"summary.parameters pool the same rates over the reported rows (stay, switch, churn with absorbing, " +
				"states, total). A row with a zero total has null rates and raises one PULSE_OVERLAY_REF_ZERO warning " +
				"(reason zero_row). params: absorbing (a non-empty array of distinct state keys present in the host, " +
				"row or column); a bad or unknown param, or a key the host lacks, is PULSE_OVERLAY_PARAM_MISSING. A " +
				"different grouper count per axis is PULSE_OVERLAY_MATRIX_NOT_SQUARE; a cell aggregation other than " +
				"AGG_COUNT / AGG_SUM, or a negative cell, is PULSE_OVERLAY_NONNEGATIVE_REQUIRED; an undefined cell is " +
				"absent (0). Scale-invariant: a weighted host's sum-of-weights table is read as is under either " +
				"weight kind. Descriptive (no p-value).",
		}
	case types.OverlayKindSymmetry:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindSymmetry,
			Inferential: true,
			Shapes:      []types.OverlayShape{types.OverlayShapeScalar},
			Scopes:      []types.OverlayScope{types.OverlayScopeMatrix},
			// No Ref family — implicit-margin like the CHISQ_* kinds.
			RefKinds: []string{},
			Description: "Test of symmetry for a from-state (rows) x to-state (columns) crosstab of PAIRED observations (the " +
				"same units measured twice): McNemar's test on a 2 x 2 table, Bowker's on k x k — R's mcnemar.test. " +
				"H0: for every pair of states, moves i -> j are as frequent as moves j -> i. chi-square = sum over " +
				"pairs i < j of (n_ij - n_ji)^2 / (n_ij + n_ji); a pair with n_ij + n_ji = 0 is dropped and df = the " +
				"number of pairs kept (k(k - 1)/2 when none is); a 2 x 2 table takes Edwards' continuity correction " +
				"(|n_12 - n_21| - 1)^2 unless params.correct is false (R's default correct = TRUE; inert on k x k). " +
				"params.exact true runs the exact binomial McNemar on a 2 x 2 instead (R binom.test(n_12, n_12 + " +
				"n_21)): statistic n_12, an exact p, parameters exact (1) and no df, the discordant odds_ratio n_12 / " +
				"n_21 with odds_ratio_ci_lower / _upper (binom.test's exact conf.int at params.conf_level, default " +
				"0.95, echoed as conf_level, mapped to p / (1 - p); n_21 = 0 makes the ratio and upper bound null), " +
				"no PULSE_OVERLAY_EXPECTED_LOW. On k x k params.exact runs the exact conditional Bowker test: given " +
				"each pair's total, n_ij ~ Binomial(n_ij + n_ji, 1/2), p = P(chi-square >= observed) by exact " +
				"convolution; statistic the uncorrected chi-square, parameters exact, states, discordant, " +
				"pairs_dropped, continuity_corrected 0 (no df, odds ratio or conf_level); past 2^22 enumeration terms " +
				"PULSE_OVERLAY_PARAM_MISSING (reason exact_too_large), an explicit conf_level reason needs_2x2 (both " +
				"runtime only). exact over a probability-weighted cell is PULSE_WEIGHT_UNSUPPORTED, on non-whole " +
				"discordant counts PULSE_OVERLAY_NONNEGATIVE_REQUIRED. With no pair kept, the statistic and p-value " +
				"are null and df is 0. payload.scalar is the chi-square; the layer summary carries statistic, p_value " +
				"and parameters df, states, discordant, pairs_dropped, continuity_corrected; a multiplicity block " +
				"adjusts the one p-value. States align by key, never by position (rows first, then column-only keys); " +
				"a state on one axis only is a row / column of zeros. A different grouper count per axis is " +
				"PULSE_OVERLAY_MATRIX_NOT_SQUARE; a cell aggregation other than AGG_COUNT / AGG_SUM, or a negative " +
				"cell, is PULSE_OVERLAY_NONNEGATIVE_REQUIRED; fewer than 2 states or a zero total is " +
				"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE; a bad or unknown param is PULSE_OVERLAY_PARAM_MISSING. " +
				"Any pair whose expected count under symmetry ((n_ij + n_ji) / 2) is below 5 emits one " +
				"PULSE_OVERLAY_EXPECTED_LOW. Weighted hosts follow OVERLAY_CHISQ_MATRIX (the sum-of-weights table; " +
				"under kind probability scaled to its Kish n_eff first).",
		}
	case types.OverlayKindTCell:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindTCell,
			Inferential: true,
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
			Description: "Compose overlay: per-cell Welch t-test of each target slot's crosstab cell against the reference " +
				"slot's matching cell; each cell holds the two-sided p-value. " + twoSampleCellInputs + " se = sqrt(var_target/n_target + var_ref/n_ref), t = (target − ref) / se, Welch–Satterthwaite df — " +
				"the same p-values as two-sample TEST_T for the same (mean, variance, n). " + composeFanOutNote,
		}
	case types.OverlayKindTVsRef:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindTVsRef,
			Inferential: true,
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
			Description: "Compose overlay: per-group Welch t-test of each target slot's group value against the reference " +
				"slot's matching group, over grouped slots; each entry's summary.statistic is the two-sided p-value. " + twoSampleSeriesInputs + " se = sqrt(var_target/n_target + var_ref/n_ref), t = (target − ref) / se, Welch–Satterthwaite df — " +
				"the same p-values as two-sample TEST_T. A group the reference lacks warns PULSE_OVERLAY_REF_ZERO " +
				"(details.ref_missing true) and is null; se 0 or n < 2 is null with the same warning. " + composeFanOutNote,
		}
	case types.OverlayKindTVsPrior:
		return priorTestCapability(types.OverlayKindTVsPrior,
			"Welch t-test on the means (Welch-Satterthwaite df; the OVERLAY_PAIRWISE_WELCH_T kernel)",
			"the Welford triple {mean, variance, n} of an AGG_WELFORD, AGG_VARIANCE or AGG_STDDEV measure (population variance converted to the sample variance; weighted: variance on w*, n = N*)")
	case types.OverlayKindZVsPrior:
		return priorTestCapability(types.OverlayKindZVsPrior,
			"two-means z-test (normal tail, no df; the OVERLAY_PAIRWISE_TWO_MEANS_Z kernel)",
			"the Welford triple {mean, variance, n} of an AGG_WELFORD, AGG_VARIANCE or AGG_STDDEV measure (population variance converted to the sample variance; weighted: variance on w*, n = N*)")
	case types.OverlayKindPropZVsPrior:
		return priorTestCapability(types.OverlayKindPropZVsPrior,
			"pooled two-proportion z-test (the OVERLAY_PAIRWISE_PROP_Z kernel)",
			"an AGG_AVERAGE measure of a 0/1 indicator: p̂ is its value, n its floor's non-null count (weighted: N* — Σw under frequency, n_eff under probability); on the crosstab host normalize must be none")
	case types.OverlayKindYoY:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindYoY,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// YoY is the windowed-axis ref family the kind consumes — first
			// consumer of the empty `Ref.YoY` marker. The empty marker
			// struct tags the family — the v1 frequency value lives on
			// OverlaySpec.Params["frequency"] (the YoY's own override) or
			// falls back to the GROUP_DATE grouper's Params["frequency"]
			// (wherever it sits in groups) per the WIN_* operator
			// convention.
			RefKinds: []string{"yoy"},
			Description: "Per-point year-over-year index of a SERIES host against the same period one year " +
				"earlier: point_value / prior_year_value · 100, on each entry's summary.statistic. ref.yoy (required, " +
				"an empty object). The host must carry exactly one GROUP_DATE grouper, anywhere in groups; none or " +
				"several is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (details groupers, date_groupers; predict and " +
				"runtime). The frequency is params.frequency, else the GROUP_DATE grouper's params.frequency: annual " +
				"looks back 1 point, quarterly 4, monthly 12, weekly 52 (calendar-week aligned, no day-of-week " +
				"realignment), daily the exact date 365 days earlier (Feb 29 against a non-leap year is null), hourly " +
				"the exact hour 365×24 hours earlier. No frequency is PULSE_OVERLAY_YOY_FREQUENCY_MISSING; an " +
				"unsupported one PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY (details frequency, supported). Over two or " +
				"more groupers the overlay runs once per combination of the other keys (the lookup never crosses a " +
				"segment); an explicit params.partition_by must name exactly the non-date keys, else " +
				"PULSE_OVERLAY_PARTITION_INVALID (reason not_non_date_keys). A point with no prior-year point is null " +
				"(no warning); a zero prior-year value warns PULSE_OVERLAY_REF_ZERO once per layer and that entry is " +
				"null. A missing point keeps its key with no statistic. level / within 0. Centred on 100.",
		}
	case types.OverlayKindZScoreVsMargin:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindZScoreVsMargin,
			Shapes: []types.OverlayShape{
				types.OverlayShapeMatrix,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeCell,
			},
			RefKinds: []string{"margin"},
			Fields:   []string{"level", "within"},
			Description: "Per-cell z-score against the matching crosstab margin slice (ref.margin.axis: a row's cells, a " +
				"column's cells, or every cell for grand): (cell − margin) / sd, sd the population SD of the slice's " +
				"cells. Unitless, centred on 0. level / within truncate the margin centroid to a nested-axis prefix; " +
				"the SD still spans the whole slice.",
		}
	case types.OverlayKindZScoreVsPop:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindZScoreVsPop,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// Population is the FACET-host comparison-population ref family
			// shared with INDEX_VS_POP / CHISQ_VS_POP / KS_VS_POP. The
			// capability row declares the consumed Ref-arm so MCP /
			// manifest clients see the kind requires Ref.Population to be
			// populated; descriptor.validateOverlayZScoreVsPop gates the
			// shape at predict time.
			RefKinds: []string{"population"},
			Description: "Per-value z-score of a pulse_facet_schema field's subset against a population (ref.population, " +
				"required; its cohort in ref.population.cohort), one series entry per facet value in the facet's " +
				"order on summary.statistic. Categorical: (subset_freq − pop_freq) / sd_pop, sd_pop the population SD " +
				"of the population's category frequencies; a value the population lacks scores with pop_freq 0 (no " +
				"warning). Numeric (only with a histogram on the facet): per bin (bin_center − pop_mean) / pop_sd. A " +
				"zero sd_pop warns PULSE_OVERLAY_REF_ZERO (per value; once per layer on a numeric field) and those " +
				"entries have no statistic. level / within 0. Centred on 0.",
		}
	case types.OverlayKindZScoreVsRolling:
		return descriptor.OverlayCapability{
			Kind: types.OverlayKindZScoreVsRolling,
			Shapes: []types.OverlayShape{
				types.OverlayShapeSeries,
			},
			Scopes: []types.OverlayScope{
				types.OverlayScopeGroup,
			},
			// RollingMean is the windowed-axis ref family the kind
			// consumes, shared with OVERLAY_INDEX_VS_ROLLING_MEAN — both
			// kinds share the same ref-arm. The empty marker struct tags
			// the family — the v1 window value lives on
			// OverlaySpec.Params["window"] per the WIN_* operator
			// convention.
			RefKinds: []string{"rolling_mean"},
			Description: "Per-point z-score against the W previous present points of a SERIES host: " +
				"(point_value − rolling_mean) / rolling_sd, the SAMPLE SD (n − 1: a window is a sample of the series, " +
				"where OVERLAY_ZSCORE_VS_TOTAL uses the population SD), on each entry's summary.statistic. " +
				"ref.rolling_mean (required, an empty object) and params.window (W, a positive integer: missing is " +
				"PULSE_OVERLAY_PARAM_MISSING, non-positive or non-integer PULSE_OVERLAY_LEVEL_OUT_OF_RANGE). A point " +
				"with fewer than params.min_periods present prior points (default W, at least 2) is null (no warning); a zero rolling SD warns " +
				"PULSE_OVERLAY_REF_ZERO once per layer and those entries are null. A missing point keeps its key with " +
				"no statistic and does not enter the window. level / within 0. Centred on 0.",
		}
	case types.OverlayKindZScoreVsTotal:
		return descriptor.OverlayCapability{
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
			Description: "Per-group z-score against the distribution of a SERIES host's group values: (group_val − " +
				"mean) / sd, sd the POPULATION SD (sqrt(M2 / N)) over the N present groups, on each entry's " +
				"summary.statistic. No ref. Zero variance (all present groups equal, or one group) warns " +
				"PULSE_OVERLAY_REF_ZERO and every statistic is null. A missing group keeps its key with no statistic " +
				"and is not counted. Centred on 0.",
		}
	case types.OverlayKindZCell:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindZCell,
			Inferential: true,
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
			Description: "Compose overlay: per-cell two-sample z-test on the means of each target slot's crosstab cell against " +
				"the reference slot's matching cell; each cell holds the two-sided p-value. " + twoSampleCellInputs + " se = sqrt(var_target/n_target + var_ref/n_ref), z = (target − ref) / se, normal tail — the same " +
				"p-values as TEST_Z_TWO_SAMPLE. " + composeFanOutNote,
		}
	case types.OverlayKindZVsRef:
		return descriptor.OverlayCapability{
			Kind:        types.OverlayKindZVsRef,
			Inferential: true,
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
			Description: "Compose overlay: per-group two-sample z-test on the means of each target slot's group value against " +
				"the reference slot's matching group, over grouped slots; each entry's summary.statistic is the " +
				"two-sided p-value. " + twoSampleSeriesInputs + " se = sqrt(var_target/n_target + var_ref/n_ref), z = (target − ref) / se, normal tail — the same " +
				"p-values as TEST_Z_TWO_SAMPLE. A group the reference lacks warns PULSE_OVERLAY_REF_ZERO " +
				"(details.ref_missing true) and is null; se 0 or n < 2 is null with the same warning. " + composeFanOutNote,
		}
	}
	return descriptor.OverlayCapability{Kind: kind}
}

// twoSampleParamsRule is the explicit-inputs sentence the four Compose
// two-sample kinds share (twosample.Inputs; U46 E4-S16).
const twoSampleParamsRule = "A scalar side REQUIRES its variance and sample size in params — variance_target / variance_ref " +
	"and sample_size_target / sample_size_ref, or the symmetric variance / sample_size for both sides, each a finite " +
	"number > 0; there is no default (PULSE_OVERLAY_PARAM_MISSING on predict and runtime, details.side and " +
	"details.params naming what is missing)."

// twoSampleCellInputs is OVERLAY_T_CELL / OVERLAY_Z_CELL's inputs rule.
const twoSampleCellInputs = "A cell whose components carry mean and variance (AGG_WELFORD, AGG_VARIANCE, AGG_STDDEV, or an " +
	"extension declaring both keys) reads its mean, variance and n from the crosstab components with no params; " +
	"the variance is the SAMPLE variance m2/(n-1): an unweighted AGG_VARIANCE / AGG_STDDEV cell's population variance is " +
	"converted (n < 2: undefined), an extension cell is read as is and must expose it; " +
	"any other cell — or any cell of a slot built with components disabled — is a scalar mean. " + twoSampleParamsRule

// composeFanOutNote closes the fan-out kinds' descriptions
// (overlayhost.ComposeFanOutKind, U66 #443).
const composeFanOutNote = "Several targets fan out: one layer per target in targets order, each the single-target " +
	"layer for its (reference, target) pair, named `<name>__<target>` (one target keeps the spec's name), capped " +
	"by options.max_panel_targets (default 16; PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP); a multiplicity block maps onto every layer."

// twoSampleSeriesInputs is OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF's inputs
// rule.
const twoSampleSeriesInputs = "The scored measure (params.field, else aggregations[0]) must be a mean or carry the " +
	"moments: AGG_AVERAGE / AGG_WEIGHTED_MEAN (or an AGG_VEC_MEAN element column) is a scalar mean; AGG_WELFORD, " +
	"AGG_VARIANCE, AGG_STDDEV or an extension whose component schema declares mean and variance reads its mean, variance " +
	"and n from the slot's per-group components with no params (an unweighted AGG_VARIANCE / AGG_STDDEV population " +
	"variance converted to the sample variance; PULSE_OVERLAY_COMPONENTS_REQUIRED when that slot's components are " +
	"disabled); any other measure is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE (details.measure_type, reason " +
	"measure_not_mean), and a mean on one side with moments on the other is PULSE_OVERLAY_SCHEMA_DIVERGENT (reason " +
	"mixed_measure_read). " + twoSampleParamsRule + " Over crosstab slots the kind is " +
	"PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE — compare cells with the _CELL twin."
