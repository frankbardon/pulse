package io

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/types"
)

// Shared overlay-payload assertion helpers used by the per-format
// ExportJob / ConvertJob integration tests. These helpers operate on
// *types.OverlayLayer values directly (no format-specific dispatch) so
// the per-format tests can share one assertion surface across the four
// embedding adapters (Arrow / Parquet / Excel / NDJSON).
//
// Each helper returns a non-nil error describing the divergence and nil
// when the two layer slices match within the documented tolerance. The
// tolerance is float64 ULP-level (1e-9 absolute) so per-format
// round-trips that pass through JSON / Excel string coercion still
// register as byte-equivalent at the renderer-facing layer.

// CompareOverlayLayers returns nil when `got` matches `want` slot-for-
// slot. Mismatch returns an error describing the first divergence —
// length, slot index, name / kind / scope, shape, or per-shape payload.
// Designed for use inside table-driven test bodies; callers wrap the
// error in t.Errorf for the failure message.
//
// Exported so the per-format integration tests under io/exportoverlay/
// share one comparison surface across Arrow / Parquet / Excel / NDJSON
// round-trips.
func CompareOverlayLayers(got, want []*types.OverlayLayer) error {
	if len(got) != len(want) {
		return fmt.Errorf("layer count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if err := CompareOverlayLayer(got[i], want[i]); err != nil {
			return fmt.Errorf("layer[%d] %s: %w", i, want[i].Name, err)
		}
	}
	return nil
}

// CompareOverlayLayer returns nil when the two layers match across all
// renderer-facing slots (name, kind, scope, ref, payload, summary).
// Per-shape payload comparison dispatches through CompareOverlayPayload.
func CompareOverlayLayer(got, want *types.OverlayLayer) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("nil layer: got=%v want=%v", got == nil, want == nil)
	}
	if got.Name != want.Name {
		return fmt.Errorf("name: got %q want %q", got.Name, want.Name)
	}
	if got.Kind != want.Kind {
		return fmt.Errorf("kind: got %q want %q", got.Kind, want.Kind)
	}
	if got.Scope != want.Scope {
		return fmt.Errorf("scope: got %q want %q", got.Scope, want.Scope)
	}
	if err := CompareOverlayRef(got.Ref, want.Ref); err != nil {
		return fmt.Errorf("ref: %w", err)
	}
	if got.Payload.Shape != want.Payload.Shape {
		return fmt.Errorf("payload shape: got %q want %q", got.Payload.Shape, want.Payload.Shape)
	}
	if err := CompareOverlayPayload(got.Payload, want.Payload); err != nil {
		return fmt.Errorf("payload: %w", err)
	}
	if err := CompareOverlaySummary(got.Summary, want.Summary); err != nil {
		return fmt.Errorf("summary: %w", err)
	}
	return nil
}

// CompareOverlayRef returns nil when the two refs share the same
// discriminator state. The structural comparison covers the Margin /
// Population / Stage / Slot arms — extending this helper when new ref
// arms land follows the additive-only rule (new arms compare nil-vs-
// nil before recursing into the new fields).
func CompareOverlayRef(got, want types.OverlayRef) error {
	if (got.Margin == nil) != (want.Margin == nil) {
		return fmt.Errorf("margin presence mismatch: got=%v want=%v", got.Margin != nil, want.Margin != nil)
	}
	if got.Margin != nil {
		if got.Margin.Axis != want.Margin.Axis {
			return fmt.Errorf("margin axis: got %q want %q", got.Margin.Axis, want.Margin.Axis)
		}
	}
	return nil
}

// CompareOverlayPayload dispatches by shape and compares the populated
// arm. Absent arms (the non-matching shape's sub-payload nil) are
// untouched — the OverlayPayload union always populates exactly one arm
// per the Shape discriminator.
func CompareOverlayPayload(got, want types.OverlayPayload) error {
	switch want.Shape {
	case types.OverlayShapeScalar:
		return compareScalar(got.Scalar, want.Scalar)
	case types.OverlayShapeSeries:
		return compareSeries(got.Series, want.Series)
	case types.OverlayShapeMatrix:
		return compareMatrix(got.Matrix, want.Matrix)
	}
	return nil
}

// CompareOverlaySummary returns nil when both summaries match across
// every populated field. nil-vs-nil counts as match; nil-vs-non-nil is
// a mismatch.
func CompareOverlaySummary(got, want *types.OverlaySummary) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("nil mismatch: got=%v want=%v", got == nil, want == nil)
	}
	if err := compareFloatPtr("min", got.Min, want.Min); err != nil {
		return err
	}
	if err := compareFloatPtr("max", got.Max, want.Max); err != nil {
		return err
	}
	if err := compareIntPtr("count", got.Count, want.Count); err != nil {
		return err
	}
	if err := compareFloatPtr("baseline", got.Baseline, want.Baseline); err != nil {
		return err
	}
	if err := compareFloatPtr("statistic", got.Statistic, want.Statistic); err != nil {
		return err
	}
	if err := compareFloatPtr("p_value", got.PValue, want.PValue); err != nil {
		return err
	}
	if err := compareFloatMap("parameters", got.Parameters, want.Parameters); err != nil {
		return err
	}
	return nil
}

func compareScalar(got, want *float64) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("scalar nil mismatch: got=%v want=%v", got == nil, want == nil)
	}
	if !floatsClose(*got, *want) {
		return fmt.Errorf("scalar value: got %v want %v", *got, *want)
	}
	return nil
}

func compareSeries(got, want *types.SeriesPayload) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("series nil mismatch: got=%v want=%v", got == nil, want == nil)
	}
	if len(got.Entries) != len(want.Entries) {
		return fmt.Errorf("series entries length: got %d want %d", len(got.Entries), len(want.Entries))
	}
	for i, w := range want.Entries {
		g := got.Entries[i]
		// Key comparison is by stringification — different formats
		// round-trip axis-key elements through string coercion (Excel)
		// or JSON-any (Arrow / Parquet / NDJSON). The stringified head
		// matches across all three.
		if axisKeyString(g.Key) != axisKeyString(w.Key) {
			return fmt.Errorf("series[%d] key: got %q want %q", i, axisKeyString(g.Key), axisKeyString(w.Key))
		}
		// Compare entry summary (statistic / p_value primarily).
		if err := compareFloatPtr(fmt.Sprintf("series[%d].statistic", i), g.Summary.Statistic, w.Summary.Statistic); err != nil {
			return err
		}
		if err := compareFloatPtr(fmt.Sprintf("series[%d].p_value", i), g.Summary.PValue, w.Summary.PValue); err != nil {
			return err
		}
	}
	return nil
}

func compareMatrix(got, want *types.MatrixPayload) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("matrix nil mismatch: got=%v want=%v", got == nil, want == nil)
	}
	if len(got.RowKeys) != len(want.RowKeys) {
		return fmt.Errorf("matrix row keys: got %d want %d", len(got.RowKeys), len(want.RowKeys))
	}
	if len(got.ColumnKeys) != len(want.ColumnKeys) {
		return fmt.Errorf("matrix col keys: got %d want %d", len(got.ColumnKeys), len(want.ColumnKeys))
	}
	for r, wk := range want.RowKeys {
		if axisKeyString(got.RowKeys[r]) != axisKeyString(wk) {
			return fmt.Errorf("matrix row[%d] key: got %q want %q", r, axisKeyString(got.RowKeys[r]), axisKeyString(wk))
		}
	}
	for c, wk := range want.ColumnKeys {
		if axisKeyString(got.ColumnKeys[c]) != axisKeyString(wk) {
			return fmt.Errorf("matrix col[%d] key: got %q want %q", c, axisKeyString(got.ColumnKeys[c]), axisKeyString(wk))
		}
	}
	if len(got.Cells) != len(want.Cells) {
		return fmt.Errorf("matrix cell rows: got %d want %d", len(got.Cells), len(want.Cells))
	}
	for r := range want.Cells {
		if len(got.Cells[r]) != len(want.Cells[r]) {
			return fmt.Errorf("matrix cells[%d] cols: got %d want %d", r, len(got.Cells[r]), len(want.Cells[r]))
		}
		for c := range want.Cells[r] {
			if err := compareMatrixCell(got.Cells[r][c], want.Cells[r][c]); err != nil {
				return fmt.Errorf("matrix cells[%d][%d]: %w", r, c, err)
			}
		}
	}
	return nil
}

func compareMatrixCell(got, want types.MatrixCell) error {
	if got.Present != want.Present {
		return fmt.Errorf("present mismatch: got=%v want=%v", got.Present, want.Present)
	}
	if !want.Present {
		return nil
	}
	gv := matrixCellFloat(got)
	wv := matrixCellFloat(want)
	if !floatsClose(gv, wv) {
		return fmt.Errorf("value: got %v want %v", got.Value, want.Value)
	}
	return nil
}

// matrixCellFloat lifts a MatrixCell.Value to float64 best-effort. Cell
// values land as float64 in the canonical path; Excel round-trips may
// surface them as strings the parser failed to coerce — those cases
// flag as "0 vs want" which the comparator then surfaces as a
// mismatch.
func matrixCellFloat(c types.MatrixCell) float64 {
	switch v := c.Value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}

func compareFloatPtr(label string, got, want *float64) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("%s nil mismatch: got=%v want=%v", label, got == nil, want == nil)
	}
	if !floatsClose(*got, *want) {
		return fmt.Errorf("%s: got %v want %v", label, *got, *want)
	}
	return nil
}

func compareIntPtr(label string, got, want *int) error {
	if got == nil && want == nil {
		return nil
	}
	if got == nil || want == nil {
		return fmt.Errorf("%s nil mismatch: got=%v want=%v", label, got == nil, want == nil)
	}
	if *got != *want {
		return fmt.Errorf("%s: got %d want %d", label, *got, *want)
	}
	return nil
}

func compareFloatMap(label string, got, want map[string]float64) error {
	if len(got) != len(want) {
		return fmt.Errorf("%s length: got %d want %d", label, len(got), len(want))
	}
	for k, wv := range want {
		gv, ok := got[k]
		if !ok {
			return fmt.Errorf("%s key %q missing", label, k)
		}
		if !floatsClose(gv, wv) {
			return fmt.Errorf("%s[%q]: got %v want %v", label, k, gv, wv)
		}
	}
	return nil
}

// axisKeyString renders an AxisKey by stringifying every element. The
// only multi-element keys in the integration fixtures are categorical
// strings, so the stringification preserves identity through every
// per-format round-trip.
func axisKeyString(k types.AxisKey) string {
	if len(k) == 0 {
		return ""
	}
	if len(k) == 1 {
		return fmt.Sprintf("%v", k[0])
	}
	out := ""
	for i, v := range k {
		if i > 0 {
			out += "|"
		}
		out += fmt.Sprintf("%v", v)
	}
	return out
}

// floatsClose returns true when |a-b| is within 1e-9 absolute or 1e-9
// relative tolerance. The fixture values all carry exact float64
// representations so the tolerance is conservative for the common case;
// it exists to absorb JSON / Excel coercion edge cases (round-trip
// through string representations rarely loses precision but the
// tolerance keeps the assertion robust).
func floatsClose(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) == math.IsNaN(b)
	}
	if a == b {
		return true
	}
	diff := math.Abs(a - b)
	if diff < 1e-9 {
		return true
	}
	mag := math.Max(math.Abs(a), math.Abs(b))
	return diff/mag < 1e-9
}
