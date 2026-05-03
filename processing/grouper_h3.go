package processing

import (
	"encoding/json"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/uber/h3-go/v4"
)

// h3GrouperParams configures GROUP_H3_CELL.
type h3GrouperParams struct {
	// Resolution is the target H3 resolution (0-15). Required for
	// point_f64 input; optional for h3_cell input (used for parent walk).
	Resolution *int `json:"resolution"`
}

// h3Grouper partitions records into buckets keyed by their H3 cell at
// the configured resolution. Accepts point_f64 (converts at run time)
// or h3_cell (re-bins via parent walk if resolution is coarser).
type h3Grouper struct {
	resolution *int
	fieldType  encoding.FieldType
}

func newH3Grouper(grp *types.Group, schema *encoding.Schema) (Grouper, error) {
	var params h3GrouperParams
	if len(grp.Params) > 0 {
		if err := json.Unmarshal(grp.Params, &params); err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"invalid GROUP_H3_CELL params",
				map[string]any{"error": err.Error()})
		}
	}
	var ft encoding.FieldType
	if schema != nil {
		f := schema.Field(grp.Field)
		if f == nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"GROUP_H3_CELL: field not found",
				map[string]any{"field": grp.Field})
		}
		ft = f.Type
		if ft != encoding.FieldTypePointF64 && ft != encoding.FieldTypeH3Cell {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"GROUP_H3_CELL requires point_f64 or h3_cell field",
				map[string]any{"field": grp.Field, "type": ft.String()})
		}
		if ft == encoding.FieldTypePointF64 && params.Resolution == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"GROUP_H3_CELL on point_f64 requires resolution param")
		}
	}
	if params.Resolution != nil && (*params.Resolution < 0 || *params.Resolution > 15) {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_RESOLUTION,
			"H3 resolution out of range (0..15)",
			map[string]any{"resolution": *params.Resolution})
	}
	return &h3Grouper{resolution: params.Resolution, fieldType: ft}, nil
}

// KeyForRow derives the H3 cell key for a single record. Null-typed
// rows or rows whose wide value is neither point_f64 nor h3_cell return
// ok=false to be skipped by callers.
func (g *h3Grouper) KeyForRow(r *Record, field string) (string, bool, error) {
	v, ok := r.WideValue(field)
	if !ok {
		return "", false, nil
	}
	var cell h3.Cell
	switch x := v.(type) {
	case encoding.PointF64:
		if g.resolution == nil {
			return "", false, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"GROUP_H3_CELL on point_f64 requires resolution param")
		}
		c, err := h3.LatLngToCell(h3.LatLng{Lat: x.Lat, Lng: x.Lon}, *g.resolution)
		if err != nil {
			return "", false, errors.WrapCodedError(err, errors.PULSE_GEO_INVALID_POINT,
				"H3 cell conversion failed")
		}
		cell = c
	case encoding.H3Cell:
		cell = h3.Cell(x)
		if !cell.IsValid() {
			return "", false, nil
		}
		if g.resolution != nil {
			native := cell.Resolution()
			if *g.resolution > native {
				return "", false, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_RESOLUTION,
					"requested resolution finer than cell native resolution",
					map[string]any{"requested": *g.resolution, "native": native})
			}
			if *g.resolution < native {
				p, err := cell.Parent(*g.resolution)
				if err != nil {
					return "", false, errors.WrapCodedError(err, errors.PULSE_GEO_INVALID_RESOLUTION,
						"H3 parent walk failed")
				}
				cell = p
			}
		}
	default:
		return "", false, nil
	}
	return cell.String(), true, nil
}

// Group partitions records into H3-cell buckets keyed by lowercase hex
// cell strings.
func (g *h3Grouper) Group(records []*Record, field string) (map[string][]*Record, error) {
	out := make(map[string][]*Record)
	for _, r := range records {
		key, ok, err := g.KeyForRow(r, field)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out[key] = append(out[key], r)
	}
	return out, nil
}
