package processing

import (
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// geoWithinFilterer matches records whose point_f64 field falls inside
// a polygon supplied as WKT in Filterer.Expression.
type geoWithinFilterer struct{}

func newGeoWithinFilterer() FiltererBuilder { return &geoWithinFilterer{} }

func (f *geoWithinFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	if filter.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_GEO_WITHIN requires a field")
	}
	if filter.Expression == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_GEO_WITHIN requires a WKT polygon in expression")
	}
	if schema != nil {
		if fld := schema.Field(filter.Field); fld != nil && fld.Type != encoding.FieldTypePointF64 {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"FILTER_GEO_WITHIN requires a point_f64 field",
				map[string]any{"field": filter.Field, "type": fld.Type.String()})
		}
	}
	poly, err := encoding.ParseWKTPolygon(filter.Expression)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	return func(r *Record) (bool, error) {
		v, ok := r.WideValue(field)
		if !ok {
			return false, nil
		}
		p, ok := v.(encoding.PointF64)
		if !ok {
			return false, nil
		}
		return poly.Contains(p), nil
	}, nil
}

// geoWithinRadiusFilterer matches records whose point_f64 field is within
// a haversine distance (in meters) of an anchor point. Configuration is
// shipped as JSON in Filterer.Expression to avoid extending the public
// Filterer struct in v1; the JSON shape is:
//
//	{"anchor": "POINT(lon lat)", "radius_m": 100.0}
type geoWithinRadiusFilterer struct{}

type geoRadiusParams struct {
	Anchor  string  `json:"anchor"`
	RadiusM float64 `json:"radius_m"`
}

func newGeoWithinRadiusFilterer() FiltererBuilder { return &geoWithinRadiusFilterer{} }

func (f *geoWithinRadiusFilterer) Build(filter *types.Filterer, schema *encoding.Schema) (FilterFunc, error) {
	if filter.Field == "" {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_GEO_WITHIN_RADIUS_M requires a field")
	}
	if schema != nil {
		if fld := schema.Field(filter.Field); fld != nil && fld.Type != encoding.FieldTypePointF64 {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"FILTER_GEO_WITHIN_RADIUS_M requires a point_f64 field",
				map[string]any{"field": filter.Field, "type": fld.Type.String()})
		}
	}
	var params geoRadiusParams
	if filter.Expression != "" {
		if err := json.Unmarshal([]byte(filter.Expression), &params); err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"FILTER_GEO_WITHIN_RADIUS_M expression must be JSON {anchor,radius_m}",
				map[string]any{"error": err.Error()})
		}
	} else if len(filter.Values) >= 2 {
		// Alternate spelling: values=[anchor, radius]
		params.Anchor = filter.Values[0]
		v, err := strconv.ParseFloat(filter.Values[1], 64)
		if err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				"FILTER_GEO_WITHIN_RADIUS_M radius must parse as float",
				map[string]any{"input": filter.Values[1]})
		}
		params.RadiusM = v
	} else {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_GEO_WITHIN_RADIUS_M requires anchor + radius_m")
	}
	if params.RadiusM <= 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FILTER_GEO_WITHIN_RADIUS_M radius_m must be positive")
	}
	anchor, err := encoding.ParseWKTPoint(params.Anchor)
	if err != nil {
		return nil, err
	}
	field := filter.Field
	radius := params.RadiusM
	return func(r *Record) (bool, error) {
		v, ok := r.WideValue(field)
		if !ok {
			return false, nil
		}
		p, ok := v.(encoding.PointF64)
		if !ok {
			return false, nil
		}
		return encoding.HaversineMeters(anchor, p) <= radius, nil
	}, nil
}
