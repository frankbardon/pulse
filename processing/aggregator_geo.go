package processing

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CentroidResult holds the latitude/longitude of an AGG_GEO_CENTROID result.
type CentroidResult struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// BBoxResult holds the four corners of an AGG_GEO_BBOX result. Returned as
// a struct field in the response (not a new field type).
type BBoxResult struct {
	MinLat float64 `json:"min_lat"`
	MinLon float64 `json:"min_lon"`
	MaxLat float64 `json:"max_lat"`
	MaxLon float64 `json:"max_lon"`
}

// centroidAggregator computes the 3D unit-sphere centroid of point_f64
// values. Implements only Aggregator's contract via type assertion at
// dispatch time; not registered as a numeric aggregator.
type centroidAggregator struct{}

func newCentroidAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &centroidAggregator{}, nil
}

func (a *centroidAggregator) Aggregate(records []*Record, field string) (float64, error) {
	// The numeric Aggregator contract returns float64; the geo path
	// dispatches via AggregateGeoField so this contract is unused.
	return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
		"centroid aggregator must be invoked via AggregateGeoField")
}

// bboxAggregator computes the bounding box of point_f64 values.
type bboxAggregator struct{}

func newBBoxAggregator(_ *types.Aggregation, _ *encoding.Schema) (Aggregator, error) {
	return &bboxAggregator{}, nil
}

func (a *bboxAggregator) Aggregate(records []*Record, field string) (float64, error) {
	return 0, errors.NewCodedError(errors.PROCESSING_INTERNAL,
		"bbox aggregator must be invoked via AggregateGeoField")
}

// AggregateGeoField runs the geo aggregator for `agg` over records,
// reading the wide map for `field`. Returns a typed value (CentroidResult
// or BBoxResult) the orchestrator emits directly into the response row.
func AggregateGeoField(agg types.AggregationType, records []*Record, field string) (any, error) {
	pts := make([]encoding.PointF64, 0, len(records))
	for _, r := range records {
		v, ok := r.WideValue(field)
		if !ok {
			continue
		}
		p, ok := v.(encoding.PointF64)
		if !ok {
			continue
		}
		pts = append(pts, p)
	}
	switch agg {
	case types.AGG_GEO_CENTROID:
		c, ok := encoding.CentroidUnitSphere(pts)
		if !ok {
			return nil, nil
		}
		return CentroidResult{Lat: c.Lat, Lon: c.Lon}, nil
	case types.AGG_GEO_BBOX:
		if len(pts) == 0 {
			return nil, nil
		}
		if encoding.CrossesAntimeridian(pts) {
			return nil, errors.NewCodedError(errors.PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS,
				"AGG_GEO_BBOX input set crosses the antimeridian")
		}
		out := BBoxResult{
			MinLat: pts[0].Lat, MinLon: pts[0].Lon,
			MaxLat: pts[0].Lat, MaxLon: pts[0].Lon,
		}
		for _, p := range pts[1:] {
			if p.Lat < out.MinLat {
				out.MinLat = p.Lat
			}
			if p.Lat > out.MaxLat {
				out.MaxLat = p.Lat
			}
			if p.Lon < out.MinLon {
				out.MinLon = p.Lon
			}
			if p.Lon > out.MaxLon {
				out.MaxLon = p.Lon
			}
		}
		return out, nil
	default:
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			"unsupported geo aggregation",
			map[string]any{"aggregation": string(agg)})
	}
}

// IsGeoAggregation reports whether agg is an AGG_GEO_*.
func IsGeoAggregation(agg types.AggregationType) bool {
	return agg == types.AGG_GEO_CENTROID || agg == types.AGG_GEO_BBOX
}
