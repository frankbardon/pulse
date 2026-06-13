package processing

import (
	"sort"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Strict structural schema match for the COMPOSE-host overlay
// dispatch. Runs AFTER the key-set alignment gate (E7-S6) and BEFORE
// the per-kind handler dispatch. Three orthogonal gates fire from
// the same entry point so the canonical-code dispatch stays
// orthogonal:
//
//  1. PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT — reference and target slot
//     disagree on host result shape (MATRIX vs SERIES, MATRIX vs
//     SCALAR, etc.). Compose overlays compare cell-for-cell at
//     byte-equal coordinates; without a shared shape there is no
//     coordinate grid.
//
//  2. PULSE_OVERLAY_SLOT_NOT_CROSSTAB — the spec's Kind requires a
//     MATRIX-shape host (per the per-kind shape-required catalog) but
//     a resolved slot is not a crosstab result. The matrix-required
//     catalog lands with the per-kind handlers in E7-S9..S12; the
//     `kindRequiresMatrix` helper is a stub returning false at S7
//     time so this gate is currently unreachable from runtime. The
//     catalog row is in place so subsequent E7 stories can wire shape
//     gating without touching this file again.
//
//  3. PULSE_OVERLAY_SCHEMA_DIVERGENT — row / column axis schemas
//     disagree across slots. The match is structural over grouper
//     kinds + types + nested depth; field names may differ across
//     slots (two slots can rename the same categorical_u32 column
//     and still align). Semantic mismatches (different cell
//     aggregators, different normalization modes) are explicit
//     non-goals for v1 — the structural match is the cheapest signal
//     downstream renderer compatibility needs.
//
// Predicate ordering inside checkSlotShapeAndSchema (cheap-to-
// expensive):
//
//   1. Shape disagreement (single-string comparison per target).
//   2. Matrix-required shape violation (single-string comparison per
//      target plus a per-kind lookup).
//   3. Schema structural divergence (per-axis kind tuple build +
//      string comparison).
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
//     (string only at this gate) so the envelope serializer renders
//     them verbatim — no hand-built JSON shaping in the error
//     payload (CLAUDE.md "Structural defense bans").
//
// Where this fits in the canonical-code matrix:
//
//   - PULSE_OVERLAY_KEY_SET_DIVERGENT (E7-S6) runs FIRST. If keys
//     agree, structure can still diverge in axis grouper kind.
//   - PULSE_OVERLAY_SCHEMA_DIVERGENT / SLOT_SHAPE_DIVERGENT /
//     SLOT_NOT_CROSSTAB (this file, E7-S7).
//   - Dict-drift / field semantic gates land in E7-S8.
//   - Per-kind handler executes AFTER all three gates pass.
//
// codeMetadata for all three codes lands in errors/fixup_metadata.go
// at minimal-row quality today; E7-S13 polishes the Message + Fixup
// catalog.

// composeOverlaySchemaShape captures the structural schema for one
// slot — the host shape plus the per-axis grouper-kind tuples. The
// per-axis tuples drop field names by design (field names are
// allowed to differ across slots per the E7-S7 acceptance
// "Field names allowed to differ; only kinds + types + depth
// compared"). For MATRIX shape both rowAxis and colAxis are
// populated; for SERIES shape rowAxis carries the series group-key
// columns (sorted) and colAxis is empty; for SCALAR shape both are
// empty.
//
// cellShape captures the per-cell payload contract — distinct
// scalar / rich families are structurally different (a scalar
// float64 cell and a WelfordTriple-bearing cell can NOT participate
// in the same per-cell COMPOSE handler). E1-S8 extends the
// structural match to reject mixed cell shapes ahead of the per-
// kind dispatch so triple-aware handlers (T_CELL, OVERLAY_Z_CELL)
// and scalar handlers stay distinct surfaces. Populated only for
// the MATRIX shape; empty for SERIES / SCALAR (the cell-shape
// probe is matrix-only — series rows carry value columns, scalars
// have no cell grid).
type composeOverlaySchemaShape struct {
	shape     types.OverlayShape
	rowAxis   []string
	colAxis   []string
	cellShape string
}

// canonical renders the schema shape as a deterministic string for
// the Details payload. Format: "<rowKinds>/<colKinds>" where each
// half is the per-axis kind tuple joined "|". Empty axis halves
// render as "" so the slash is always present, making the two-axis
// nature of the canonical form unambiguous regardless of how many
// axes are populated.
//
// Examples:
//
//	MATRIX, rows=[GROUP_CATEGORY, GROUP_DATE], cols=[GROUP_RANGE]
//	  → "GROUP_CATEGORY|GROUP_DATE/GROUP_RANGE"
//	SERIES, rows=[region, segment], cols=[]
//	  → "region|segment/"
//	SCALAR
//	  → "/"
//
// The empty-axis-trailing-slash form is intentional — the slash
// makes the row/column axis split visually obvious in MCP error
// payloads and in golden-file diff output.
func (s composeOverlaySchemaShape) canonical() string {
	return joinKindTuple(s.rowAxis) + "/" + joinKindTuple(s.colAxis)
}

func joinKindTuple(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "|" + parts[i]
	}
	return out
}

// extractSchemaShape builds the per-slot structural schema. The
// shape is inferred via the same precedence rule the stub handler
// uses (inferComposeStubShape): MATRIX when the response carries a
// non-nil Crosstab.Matrix, SERIES when it carries non-empty Data,
// SCALAR otherwise.
//
// Matrix arm: reads Matrix.RowHeader.Types and ColumnHeader.Types
// verbatim — the grouper-kind strings are what the matrix payload
// declares for downstream renderers, so reusing them keeps the
// schema-match check byte-equivalent to whatever the renderer would
// have seen for axis-kind annotation purposes.
//
// Series arm: the row axis is the sorted list of non-value column
// names from the first Data row. The value column is the FIRST
// sorted numeric column (encodeSeriesRow's contract), so the row
// axis drops exactly one entry per row. Multi-aggregator series
// rows fold subsequent numeric columns into the axis (consistent
// with the key-set encoding contract — they participate in the
// key, so they're part of the structural axis). When Data is
// empty or has no non-value columns, the row axis is empty —
// degenerate but well-defined.
//
// Scalar arm: both axes empty.
//
// Returns a SCALAR shape with empty axes for nil *Response —
// defensive guard so callers that reach this helper without going
// through the slot resolver (descriptor predict at E7-S14) still
// get a well-formed short-circuit.
func extractSchemaShape(resp *types.Response) composeOverlaySchemaShape {
	if resp == nil {
		return composeOverlaySchemaShape{shape: types.OverlayShapeScalar}
	}
	if resp.Crosstab != nil && resp.Crosstab.Matrix != nil {
		return composeOverlaySchemaShape{
			shape:     types.OverlayShapeMatrix,
			rowAxis:   append([]string(nil), resp.Crosstab.Matrix.RowHeader.Types...),
			colAxis:   append([]string(nil), resp.Crosstab.Matrix.ColumnHeader.Types...),
			cellShape: probeMatrixCellShape(resp.Crosstab.Matrix),
		}
	}
	if len(resp.Data) > 0 {
		return composeOverlaySchemaShape{
			shape:   types.OverlayShapeSeries,
			rowAxis: extractSeriesAxisFields(resp.Data),
		}
	}
	return composeOverlaySchemaShape{shape: types.OverlayShapeScalar}
}

// extractSeriesAxisFields returns the sorted list of non-value
// column names from a series Response.Data. Mirrors the
// encodeSeriesRow value-column rule (E7-S6): the FIRST sorted
// numeric column is the value, subsequent numeric columns
// participate in the structural axis alongside non-numeric columns.
//
// Returns nil when the rows are empty OR when the first row has no
// columns. The structural-match gate treats nil as the empty axis
// — a degenerate series slot still has a well-defined zero-length
// axis tuple and the comparator treats two nil-axes slots as
// schema-equivalent.
func extractSeriesAxisFields(rows []map[string]any) []string {
	if len(rows) == 0 {
		return nil
	}
	first := rows[0]
	if len(first) == 0 {
		return nil
	}
	cols := make([]string, 0, len(first))
	for k := range first {
		cols = append(cols, k)
	}
	sort.Strings(cols)
	// Drop the first numeric column — that's the value column per
	// encodeSeriesRow. Subsequent numeric columns stay (they
	// participate in the canonical key as multi-aggregator
	// distinguishers).
	out := make([]string, 0, len(cols))
	droppedValue := false
	for _, col := range cols {
		v := first[col]
		if _, ok := coerceNumericValue(v); ok && !droppedValue {
			droppedValue = true
			continue
		}
		out = append(out, col)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// probeMatrixCellShape returns the canonical cell-shape token for
// a matrix-shape slot — "scalar" for numeric cell payloads,
// "welford_triple" for AGG_WELFORD's Rich payload, and a Rich-
// family type-name token for any other Rich shape so two slots
// that happen to share a future-extended struct still compare
// byte-equal while a scalar / triple mismatch (or any other rich
// / scalar mismatch) trips the SCHEMA_DIVERGENT gate.
//
// Cell-shape probe walks the matrix in row-major order and returns
// the canonical token derived from the first Present cell. Returns
// "" when the matrix is empty / every cell is absent — the
// downstream gate treats two empty-cell slots as cell-shape-
// equivalent (degenerate match) so a zero-row matrix slot does
// not trip the gate against another zero-row matrix slot.
//
// E1-S8 admits scalar (float64 / float32 / int / int64 / uint32
// / uint64) and processing.WelfordTriple as the two canonical
// cell shapes. Future Rich payloads (e.g. map[string]int for
// AGG_SET_FREQUENCY, []string for AGG_SET_UNION) fall through to
// the type-name branch so the canonical string stays orthogonal
// between unrelated Rich families — two slots emitting the same
// map[string]int cell still compare equal; one emitting
// map[string]int + another emitting []string diverge.
func probeMatrixCellShape(m *types.MatrixPayload) string {
	if m == nil {
		return ""
	}
	for _, row := range m.Cells {
		for _, cell := range row {
			if !cell.Present {
				continue
			}
			return canonicalCellShape(cell.Value)
		}
	}
	return ""
}

// canonicalCellShape maps a MatrixCell.Value to its canonical
// cell-shape token. Scalar numeric kinds collapse to "scalar" so
// the per-aggregator numeric width (float32 vs float64 vs int)
// never trips the gate. WelfordTriple is the named carve-out for
// the AGG_WELFORD rich payload — the E1 stat-test-overlay-parity
// epic depends on triple-aware handlers binding to this token.
// Unrelated Rich families fall through to type-name branches so
// the gate stays orthogonal across them without dragging in
// reflect.
func canonicalCellShape(v any) string {
	switch v.(type) {
	case nil:
		return ""
	case float64, float32, int, int64, int32, uint32, uint64, uint, uint8, uint16:
		return "scalar"
	case WelfordTriple:
		return "welford_triple"
	case map[string]int:
		return "map[string]int"
	case map[string]int64:
		return "map[string]int64"
	case map[string]float64:
		return "map[string]float64"
	case []string:
		return "[]string"
	}
	// Final fallback: a single unknown sentinel. Two slots both
	// landing here are treated as cell-shape-equivalent (the gate
	// stays permissive for unknown Rich shapes pending an extended
	// canonical-token catalog). Future Rich families should be
	// added to the switch above so they get distinct tokens.
	return "unknown"
}

// cellShapesEqual reports whether two cell-shape tokens are
// structurally identical. Empty-token (one slot is empty / all
// cells absent) compares equal against any other token so a
// zero-cell matrix slot does not trip the gate against a populated
// slot of any shape — the gate has no rich/scalar evidence to act
// on. Non-empty tokens must compare byte-for-byte.
func cellShapesEqual(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	return a == b
}

// kindRequiresMatrix reports whether the given OverlayKind requires
// a MATRIX-shape host. The Compose-only matrix-required catalog
// lands kind-by-kind with the per-kind handlers. E7-S9 registered
// the first six entries; E7-S10 lifts OVERLAY_INDEX_VS_REF and
// OVERLAY_DELTA_VS_REF out of the matrix-required set because they
// now accept dual-shape (MATRIX or SERIES) hosts — the per-handler
// shape dispatch routes the in-kind arm. OVERLAY_T_VS_REF is the
// SERIES-shape sibling of OVERLAY_T_CELL and stays matrix-NOT-required.
//
// Matrix-required today:
//   - OVERLAY_PROP_Z_CELL, OVERLAY_PROP_Z_PANEL, OVERLAY_T_CELL,
//     OVERLAY_CHISQ_VS_REF, OVERLAY_RANK — per-cell / whole-matrix
//     arithmetic is undefined against SERIES or SCALAR slots.
//
// When this returns true the PULSE_OVERLAY_SLOT_NOT_CROSSTAB gate
// fires loud at runtime against a non-MATRIX reference OR a
// non-MATRIX target; the canonical-code dispatch carries
// {target_label, required_shape="MATRIX", observed_shape} Details.
// Dual-shape kinds (OVERLAY_INDEX_VS_REF / OVERLAY_DELTA_VS_REF) and
// series-shape kinds (OVERLAY_T_VS_REF, future E7-S11..S12) stay
// false here; their per-slot shape gating fires through the more
// general PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT path (still ensures
// reference + target slots share the same host shape).
func kindRequiresMatrix(kind types.OverlayKind) bool {
	return KindRequiresMatrix(kind)
}

// KindRequiresMatrix is the exported sibling of the package-internal
// kindRequiresMatrix predicate. The descriptor-side compose validator
// (descriptor.ValidateCompose, E7-S14) needs to read the catalog
// without dragging in processing's full overlay machinery, but the
// per-helper sync test (TestKindRequiresMatrixCompose_MatchesProcessing
// in descriptor/compose_test.go) pins the two surfaces in lockstep so
// a new matrix-required kind cannot land here without an accompanying
// descriptor-side row.
//
// Body intentionally duplicated against the internal predicate so the
// exported surface can be lifted in isolation when the catalog grows;
// the package-internal call sites continue to read the lowercase form.
func KindRequiresMatrix(kind types.OverlayKind) bool {
	switch kind {
	case types.OverlayKindPropZCell,
		types.OverlayKindPropZPanel,
		types.OverlayKindTCell,
		types.OverlayKindChiSqVsRef,
		types.OverlayKindRank:
		return true
	}
	return false
}

// checkSlotShapeAndSchema is the entry point the compose dispatch
// calls per spec. Walks `targetResps` against `refResp` once,
// firing the three gates in cheap-to-expensive order. Returns the
// first failing target as a coded error; nil when all gates pass.
//
// Order of failure modes (per target):
//
//  1. Reference vs target host shape disagrees ⇒
//     PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT.
//  2. Spec.Kind requires MATRIX but target is non-MATRIX (or
//     reference is non-MATRIX) ⇒ PULSE_OVERLAY_SLOT_NOT_CROSSTAB.
//  3. Per-axis grouper-kind tuples differ ⇒
//     PULSE_OVERLAY_SCHEMA_DIVERGENT.
//
// Details payload (encoding/json-friendly, no fmt.Sprintf):
//
//	SHAPE_DIVERGENT:
//	  - "code": "PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT"
//	  - "index": spec index
//	  - "reference": reference slot label
//	  - "target_label": target slot label
//	  - "reference_shape": "matrix" / "series" / "scalar"
//	  - "target_shape": same enum
//
//	SLOT_NOT_CROSSTAB:
//	  - "code": "PULSE_OVERLAY_SLOT_NOT_CROSSTAB"
//	  - "index": spec index
//	  - "kind": spec Kind (so the renderer can surface the kind that
//	    requires MATRIX)
//	  - "required_shape": "MATRIX" (uppercase per acceptance)
//	  - "target_label": offending slot label (or "" for the
//	    reference)
//	  - "observed_shape": "series" / "scalar"
//
//	SCHEMA_DIVERGENT:
//	  - "code": "PULSE_OVERLAY_SCHEMA_DIVERGENT"
//	  - "index": spec index
//	  - "reference": reference slot label
//	  - "target_label": target slot label
//	  - "reference_schema": canonical kind-tuple string
//	    ("<rowKinds>/<colKinds>") from extractSchemaShape.canonical
//	  - "target_schema": same form for the target slot
func checkSlotShapeAndSchema(refResp *types.Response, targetResps []*types.Response, spec types.ComposeOverlaySpec, specIdx int) error {
	refSchema := extractSchemaShape(refResp)

	// SLOT_NOT_CROSSTAB applies to the reference slot too — when the
	// spec.Kind requires MATRIX and the reference is non-MATRIX we
	// fail loud rather than letting the per-kind handler explode
	// later. The reference's own "target_label" entry is the literal
	// "reference" string so the MCP fix-up surface can render the
	// matrix-required failure regardless of whether the reference or
	// a target violated the contract.
	if kindRequiresMatrix(spec.Kind) && refSchema.shape != types.OverlayShapeMatrix {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_INTERNAL,
			"compose overlay kind requires a MATRIX-shape host but the reference slot is not a crosstab",
			map[string]any{
				"code":           string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
				"index":          specIdx,
				"kind":           string(spec.Kind),
				"required_shape": "MATRIX",
				"target_label":   "reference",
				"observed_shape": string(refSchema.shape),
			})
	}

	for i, tResp := range targetResps {
		tSchema := extractSchemaShape(tResp)
		targetLabel := ""
		if i < len(spec.Targets) {
			targetLabel = spec.Targets[i]
		}

		// Gate 1: SLOT_SHAPE_DIVERGENT.
		if refSchema.shape != tSchema.shape {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"compose overlay slot shape divergent: reference and target produce different host result shapes",
				map[string]any{
					"code":            string(errors.PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT),
					"index":           specIdx,
					"reference":       spec.Reference,
					"target_label":    targetLabel,
					"reference_shape": string(refSchema.shape),
					"target_shape":    string(tSchema.shape),
				})
		}

		// Gate 2: SLOT_NOT_CROSSTAB. Only fires when the kind catalog
		// declares MATRIX-required AND the target is non-MATRIX. The
		// reference arm above caught the symmetric case; this one
		// catches a non-MATRIX target paired with a MATRIX reference.
		// Today kindRequiresMatrix returns false everywhere so this
		// branch is unreachable at runtime; E7-S9..S12 flip the bit.
		if kindRequiresMatrix(spec.Kind) && tSchema.shape != types.OverlayShapeMatrix {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"compose overlay kind requires a MATRIX-shape host but a target slot is not a crosstab",
				map[string]any{
					"code":           string(errors.PULSE_OVERLAY_SLOT_NOT_CROSSTAB),
					"index":          specIdx,
					"kind":           string(spec.Kind),
					"required_shape": "MATRIX",
					"target_label":   targetLabel,
					"observed_shape": string(tSchema.shape),
				})
		}

		// Gate 3: SCHEMA_DIVERGENT — axis-kind arm. Compare per-axis
		// kind tuples. Field names allowed to differ —
		// extractSchemaShape returns kind tuples only for the matrix
		// arm and column-name tuples for the series arm. The series-
		// arm comparison is intentionally over column names because
		// the response side has no grouper-kind annotation for series
		// rows — the row axis schema "match" is structural over the
		// same column names a renderer would key off.
		if !axisKindsEqual(refSchema.rowAxis, tSchema.rowAxis) ||
			!axisKindsEqual(refSchema.colAxis, tSchema.colAxis) {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"compose overlay schema divergent: reference and target produce structurally different axis schemas",
				map[string]any{
					"code":             string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT),
					"index":            specIdx,
					"reference":        spec.Reference,
					"target_label":     targetLabel,
					"reference_schema": refSchema.canonical(),
					"target_schema":    tSchema.canonical(),
				})
		}

		// Gate 3.5: SCHEMA_DIVERGENT — cell-shape arm (E1-S8).
		// Reuses the canonical SCHEMA_DIVERGENT code; same coded-
		// error envelope, distinct Details payload. Fires when the
		// reference and a target slot carry structurally different
		// per-cell payloads (e.g. one slot's cells are scalar float64
		// while the other slot's cells are processing.WelfordTriple).
		// Empty-token short-circuit (cellShapesEqual) keeps zero-cell
		// matrix slots from tripping the gate against populated slots
		// — the gate has no rich/scalar evidence to act on. Surfaces
		// the canonical cell-shape tokens on dedicated reference_cell_
		// shape / target_cell_shape Detail entries so the Details
		// payload visibly distinguishes a cell-shape mismatch from an
		// axis-kind mismatch; reference_schema / target_schema echo
		// the axis canonical strings (guaranteed byte-equal here —
		// the axis-kind arm already accepted).
		if !cellShapesEqual(refSchema.cellShape, tSchema.cellShape) {
			return errors.NewCodedErrorWithDetails(
				errors.PROCESSING_INTERNAL,
				"compose overlay schema divergent: reference and target produce structurally different cell shapes",
				map[string]any{
					"code":                 string(errors.PULSE_OVERLAY_SCHEMA_DIVERGENT),
					"index":                specIdx,
					"reference":            spec.Reference,
					"target_label":         targetLabel,
					"reference_schema":     refSchema.canonical(),
					"target_schema":        tSchema.canonical(),
					"reference_cell_shape": refSchema.cellShape,
					"target_cell_shape":    tSchema.cellShape,
				})
		}
	}
	return nil
}

// axisKindsEqual reports whether two per-axis kind tuples are
// structurally identical. Length must match AND every position
// must compare equal byte-for-byte. Nil and empty slices compare
// equal — both are the "no axis" shape and the gate treats them
// uniformly. The compare is intentionally NOT order-insensitive:
// nested grouper depth is part of the structural schema, and a
// (GROUP_CATEGORY, GROUP_DATE) row axis is structurally distinct
// from a (GROUP_DATE, GROUP_CATEGORY) row axis even though both
// share the same kind set.
func axisKindsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
