package overlayhost

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// OVERLAY_CORRESPONDENCE params and the non-negative-mass host rule,
// shared by predict (internal/descriptor) and runtime
// (internal/processing) so both raise the same code, message and
// details.

// Correspondence normalizations — which coordinates the map plots.
const (
	// CorrespondenceSymmetric plots rows and columns both in principal
	// coordinates (the default, R ca's "symmetric" map).
	CorrespondenceSymmetric = "symmetric"
	// CorrespondenceRowPrincipal plots rows in principal and columns in
	// standard coordinates (R ca's "rowprincipal").
	CorrespondenceRowPrincipal = "row_principal"
	// CorrespondenceColumnPrincipal plots rows in standard and columns in
	// principal coordinates (R ca's "colprincipal").
	CorrespondenceColumnPrincipal = "column_principal"
	// CorrespondenceDefaultDimensions is the retained dimension count
	// when params.dimensions is omitted (capped at min(r, c) − 1).
	CorrespondenceDefaultDimensions = 2
)

// correspondenceNormalizations lists the accepted normalizations in
// documented order.
var correspondenceNormalizations = []string{
	CorrespondenceSymmetric, CorrespondenceRowPrincipal, CorrespondenceColumnPrincipal,
}

// CorrespondenceParams is a decoded OVERLAY_CORRESPONDENCE params
// block. Dimensions 0 means omitted (the default, capped by the table);
// Normalization is never empty after ParseCorrespondenceParams.
type CorrespondenceParams struct {
	Dimensions    int
	Normalization string
}

// ParseCorrespondenceParams decodes spec.Params strictly: only
// `dimensions` (an integer >= 1) and `normalization` (symmetric |
// row_principal | column_principal) are accepted. Anything else — an
// unknown key, a malformed block, a non-integer or < 1 dimensions, an
// unknown normalization — is PULSE_OVERLAY_PARAM_MISSING (the overlay
// param-validation code) with details {kind, param, supported?} plus
// index when index >= 0 (predict knows it, a runtime handler does not).
func ParseCorrespondenceParams(spec *types.OverlaySpec, index int) (CorrespondenceParams, *errors.CodedError) {
	out := CorrespondenceParams{Normalization: CorrespondenceSymmetric}
	var in struct {
		Dimensions    json.RawMessage `json:"dimensions"`
		Normalization *string         `json:"normalization"`
	}
	paramread.Declare(paramread.Overlay, string(spec.Kind), &in)
	raw := bytes.TrimSpace(spec.Params)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return out, nil
	}
	if err := paramread.DecodeStrict(paramread.Overlay, string(spec.Kind), raw, &in); err != nil {
		return out, correspondenceParamRefusal(spec.Kind, index, "",
			"params must be an object with only dimensions and normalization: "+err.Error(), nil)
	}
	if len(in.Dimensions) > 0 {
		// A JSON number literal holding an integer >= 1 — never a
		// string, a fraction or null.
		lit := string(in.Dimensions)
		d, err := strconv.Atoi(lit)
		if err != nil || d < 1 {
			return out, correspondenceParamRefusal(spec.Kind, index, "dimensions",
				"dimensions must be an integer >= 1, got "+lit,
				map[string]any{"dimensions": lit})
		}
		out.Dimensions = d
	}
	if in.Normalization != nil {
		ok := false
		for _, n := range correspondenceNormalizations {
			if *in.Normalization == n {
				ok = true
			}
		}
		if !ok {
			return out, correspondenceParamRefusal(spec.Kind, index, "normalization",
				"unknown normalization "+strconv.Quote(*in.Normalization)+" (supports: symmetric, row_principal, column_principal)",
				map[string]any{"normalization": *in.Normalization, "supported": append([]string(nil), correspondenceNormalizations...)})
		}
		out.Normalization = *in.Normalization
	}
	return out, nil
}

// MaxCorrespondenceDimensions is the number of non-trivial dimensions
// of an rows × cols table: min(rows, cols) − 1 (0 below 2 × 2).
func MaxCorrespondenceDimensions(rows, cols int) int {
	k := min(rows, cols) - 1
	return max(k, 0)
}

// ResolveDimensions is the retained dimension count over a table with
// maxDims non-trivial dimensions: the explicit value, else the default
// capped at maxDims.
func (p CorrespondenceParams) ResolveDimensions(maxDims int) int {
	if p.Dimensions > 0 {
		return p.Dimensions
	}
	return min(CorrespondenceDefaultDimensions, maxDims)
}

// CorrespondenceDimensionsRefusal refuses an EXPLICIT dimensions above
// the table's min(rows, cols) − 1 non-trivial dimensions
// (PULSE_OVERLAY_PARAM_MISSING, details {kind, param: "dimensions",
// dimensions, max_dimensions, rows, cols} plus index when >= 0). The
// omitted default is capped instead, never refused; a table below
// 2 × 2 is left to the shape refusal.
func CorrespondenceDimensionsRefusal(kind types.OverlayKind, p CorrespondenceParams, rows, cols, index int) *errors.CodedError {
	maxDims := MaxCorrespondenceDimensions(rows, cols)
	if p.Dimensions == 0 || maxDims < 1 || p.Dimensions <= maxDims {
		return nil
	}
	return correspondenceParamRefusal(kind, index, "dimensions",
		"dimensions "+strconv.Itoa(p.Dimensions)+" exceeds the table's "+strconv.Itoa(maxDims)+
			" non-trivial dimensions (min(rows, columns) - 1 over a "+strconv.Itoa(rows)+" x "+strconv.Itoa(cols)+" table)",
		map[string]any{"dimensions": p.Dimensions, "max_dimensions": maxDims, "rows": rows, "cols": cols})
}

func correspondenceParamRefusal(kind types.OverlayKind, index int, param, why string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"kind": string(kind)}
	if param != "" {
		details["param"] = param
	}
	if index >= 0 {
		details["index"] = index
	}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_PARAM_MISSING,
		"overlay "+string(kind)+": "+why, details)
}

// NonnegativeCellRefusal refuses a non-negative-mass overlay kind over
// a crosstab whose cell aggregation is neither AGG_COUNT nor AGG_SUM —
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED, details {kind, aggregation,
// slot: "crosstab.cell"} plus index when >= 0. A nil cell or an empty
// type (not yet known) passes.
func NonnegativeCellRefusal(kind types.OverlayKind, cellType types.AggregationType, index int) *errors.CodedError {
	if cellType == "" || cellType == types.AGG_COUNT || cellType == types.AGG_SUM {
		return nil
	}
	details := map[string]any{"kind": string(kind), "aggregation": string(cellType), "slot": "crosstab.cell"}
	if index >= 0 {
		details["index"] = index
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED,
		"overlay "+string(kind)+" reads the crosstab cells as non-negative masses and needs an AGG_COUNT or AGG_SUM cell; got "+string(cellType),
		details)
}

// NonnegativeValueRefusal refuses a negative or non-finite host cell
// value (runtime only: predict reads no record) —
// PULSE_OVERLAY_NONNEGATIVE_REQUIRED, details {kind, row, column,
// value}; value is null on the wire when it is not finite. nil when v
// is a finite non-negative number.
func NonnegativeValueRefusal(kind types.OverlayKind, row, column types.AxisKey, v float64) *errors.CodedError {
	if v >= 0 && !math.IsInf(v, 1) {
		return nil
	}
	var value any = v
	if math.IsNaN(v) || math.IsInf(v, 0) {
		value = nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED,
		"overlay "+string(kind)+" reads the crosstab cells as non-negative masses; a cell is negative or not finite",
		map[string]any{"kind": string(kind), "row": row, "column": column, "value": value})
}
