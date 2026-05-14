package processing

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func makePointRecord(t *testing.T, schema *encoding.Schema, field string, lat, lon float64) *Record {
	t.Helper()
	r := NewRecord(schema, map[string]float64{field: 0})
	r.SetWide(field, encoding.PointF64{Lat: lat, Lon: lon})
	return r
}

func TestGeoFilterer_Within(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "loc", Type: encoding.FieldTypePointF64},
	}}
	records := []*Record{
		makePointRecord(t, schema, "loc", 5, 5),   // inside
		makePointRecord(t, schema, "loc", 11, 5),  // outside (lat>10)
		makePointRecord(t, schema, "loc", 5, 11),  // outside (lon>10)
		makePointRecord(t, schema, "loc", -1, -1), // outside (negative)
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{
		Filterers: []*types.Filterer{{
			Type:       types.FILTER_GEO_WITHIN,
			Field:      "loc",
			Expression: "POLYGON((0 0, 10 0, 10 10, 0 10, 0 0))",
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "loc"}},
	}, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("rows = %d", len(resp.Data))
	}
	count := resp.Data[0]["AGG_COUNT_loc"].(float64)
	if count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestGeoFilterer_WithinRadiusM(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "loc", Type: encoding.FieldTypePointF64},
	}}
	// Anchor at SF; one point ~1km away, one ~100km away.
	records := []*Record{
		makePointRecord(t, schema, "loc", 37.7849, -122.4194), // ~1.1km north
		makePointRecord(t, schema, "loc", 38.5816, -121.4944), // Sacramento, ~120km
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{
		Filterers: []*types.Filterer{{
			Type:       types.FILTER_GEO_WITHIN_RADIUS_M,
			Field:      "loc",
			Expression: `{"anchor": "POINT(-122.4194 37.7749)", "radius_m": 5000}`,
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "loc"}},
	}, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	count := resp.Data[0]["AGG_COUNT_loc"].(float64)
	if count != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

func TestGeoCentroid(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "loc", Type: encoding.FieldTypePointF64},
	}}
	records := []*Record{
		makePointRecord(t, schema, "loc", 89.9, 0),
		makePointRecord(t, schema, "loc", 89.9, 90),
		makePointRecord(t, schema, "loc", 89.9, -90),
		makePointRecord(t, schema, "loc", 89.9, 180),
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_GEO_CENTROID, Field: "loc", Label: "c"}},
	}, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	c, ok := resp.Data[0]["c"].(CentroidResult)
	if !ok {
		t.Fatalf("centroid missing/typed wrong: %T", resp.Data[0]["c"])
	}
	if c.Lat < 89.5 {
		t.Errorf("centroid lat = %f, want near pole", c.Lat)
	}
}

func TestGeoBBox_Antimeridian(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "loc", Type: encoding.FieldTypePointF64},
	}}
	records := []*Record{
		makePointRecord(t, schema, "loc", 0, 179),
		makePointRecord(t, schema, "loc", 0, -179),
	}
	p := NewProcessor(schema)
	_, err := p.Process(context.Background(), &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_GEO_BBOX, Field: "loc"}},
	}, NewSliceIterator(records))
	if err == nil {
		t.Fatal("expected antimeridian rejection")
	}
	if !errors.HasCode(err, errors.PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS) {
		t.Fatalf("expected PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS, got %v", err)
	}
}

func TestH3Grouper_PointInput(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "loc", Type: encoding.FieldTypePointF64},
	}}
	records := []*Record{
		makePointRecord(t, schema, "loc", 37.7749, -122.4194),
		makePointRecord(t, schema, "loc", 37.7849, -122.4094),
		makePointRecord(t, schema, "loc", 40.7128, -74.0060), // NYC
	}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), &types.Request{
		Groups: []*types.Group{{
			Type:   types.GROUP_H3_CELL,
			Field:  "loc",
			Params: []byte(`{"resolution": 5}`),
		}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "loc"}},
	}, NewSliceIterator(records))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Data) < 2 {
		t.Errorf("expected ≥2 cell groups, got %d", len(resp.Data))
	}
}
