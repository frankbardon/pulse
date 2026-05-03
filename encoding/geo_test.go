package encoding

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestPointF64_RoundTrip(t *testing.T) {
	p := PointF64{Lat: 37.775, Lon: -122.418}
	enc := EncodePointF64(p)
	dec := DecodePointF64(enc)
	if dec != p {
		t.Errorf("round trip = %+v, want %+v", dec, p)
	}
}

func TestPointF64_Validate(t *testing.T) {
	cases := []struct {
		p    PointF64
		ok   bool
	}{
		{PointF64{0, 0}, true},
		{PointF64{90, 180}, true},
		{PointF64{-90, -180}, true},
		{PointF64{91, 0}, false},
		{PointF64{0, 181}, false},
		{PointF64{math.NaN(), 0}, false},
		{PointF64{0, math.Inf(1)}, false},
	}
	for _, tc := range cases {
		err := tc.p.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("Validate(%+v): got err=%v, want ok=%v", tc.p, err, tc.ok)
		}
	}
}

func TestParseWKTPoint(t *testing.T) {
	p, err := ParseWKTPoint("POINT(-122.418 37.775)")
	if err != nil {
		t.Fatal(err)
	}
	if p.Lat != 37.775 || p.Lon != -122.418 {
		t.Errorf("got %+v", p)
	}
}

func TestParseWKTPoint_Reject(t *testing.T) {
	bad := []string{
		"POINT(0)",
		"POINT(0, 0)",
		"POINT(0 91)",
		"foo",
		"POINT 0 0",
	}
	for _, in := range bad {
		_, err := ParseWKTPoint(in)
		if err == nil {
			t.Errorf("ParseWKTPoint(%q) accepted", in)
		}
	}
}

func TestParseWKTPolygon(t *testing.T) {
	in := "POLYGON((0 0, 10 0, 10 10, 0 10, 0 0))"
	poly, err := ParseWKTPolygon(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(poly.Ring) != 5 {
		t.Errorf("ring length = %d, want 5", len(poly.Ring))
	}
}

func TestParseWKTPolygon_Reject(t *testing.T) {
	bad := []string{
		"POLYGON((0 0, 10 0, 10 10))",                 // <4 vertices
		"POLYGON((0 0, 10 0, 10 10, 0 10))",           // not closed
		"MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0)))",        // multipolygon
		"POLYGON((0 0, 10 0, 10 10, 0 10, 0 0)(...))", // inner ring (holes)
		"foo",
	}
	for _, in := range bad {
		_, err := ParseWKTPolygon(in)
		if err == nil {
			t.Errorf("ParseWKTPolygon(%q) accepted", in)
			continue
		}
		ce, ok := err.(*errors.CodedError)
		if !ok {
			t.Errorf("ParseWKTPolygon(%q) returned non-coded error: %v", in, err)
			continue
		}
		if ce.Code != errors.PULSE_GEO_INVALID_POLYGON && ce.Code != errors.PULSE_GEO_INVALID_POINT {
			t.Errorf("ParseWKTPolygon(%q) unexpected code: %s", in, ce.Code)
		}
	}
}

func TestPolygon_Contains(t *testing.T) {
	in := "POLYGON((0 0, 10 0, 10 10, 0 10, 0 0))"
	poly, err := ParseWKTPolygon(in)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		p  PointF64
		in bool
	}{
		{PointF64{Lat: 5, Lon: 5}, true},
		{PointF64{Lat: 11, Lon: 5}, false},
		{PointF64{Lat: 5, Lon: 11}, false},
		{PointF64{Lat: -1, Lon: 5}, false},
		{PointF64{Lat: 9.9, Lon: 9.9}, true},
	}
	for _, tc := range cases {
		got := poly.Contains(tc.p)
		if got != tc.in {
			t.Errorf("Contains(%+v) = %v, want %v", tc.p, got, tc.in)
		}
	}
}

func TestHaversineMeters(t *testing.T) {
	// SF -> NYC ~ 4129 km. Tolerance 1%.
	sf := PointF64{Lat: 37.7749, Lon: -122.4194}
	nyc := PointF64{Lat: 40.7128, Lon: -74.0060}
	d := HaversineMeters(sf, nyc)
	want := 4129000.0
	if math.Abs(d-want)/want > 0.01 {
		t.Errorf("Haversine SF->NYC = %f, want ~%f", d, want)
	}
	// Identity = 0.
	if HaversineMeters(sf, sf) != 0 {
		t.Errorf("Haversine identity != 0")
	}
}

func TestCentroidUnitSphere(t *testing.T) {
	// Cluster around the north pole — centroid should be near pole.
	pts := []PointF64{
		{Lat: 89.9, Lon: 0},
		{Lat: 89.9, Lon: 90},
		{Lat: 89.9, Lon: 180},
		{Lat: 89.9, Lon: -90},
	}
	c, ok := CentroidUnitSphere(pts)
	if !ok {
		t.Fatal("expected centroid")
	}
	if c.Lat < 89.5 {
		t.Errorf("north-pole cluster centroid lat = %f, want near 90", c.Lat)
	}
}

func TestCrossesAntimeridian(t *testing.T) {
	cross := []PointF64{{Lat: 0, Lon: -179}, {Lat: 0, Lon: 179}}
	if !CrossesAntimeridian(cross) {
		t.Errorf("CrossesAntimeridian missed cross")
	}
	noCross := []PointF64{{Lat: 0, Lon: -10}, {Lat: 0, Lon: 10}}
	if CrossesAntimeridian(noCross) {
		t.Errorf("CrossesAntimeridian false-positive")
	}
}

func TestH3Cell_RoundTrip(t *testing.T) {
	c := H3Cell(0x89283082803ffff)
	enc := EncodeH3Cell(c)
	dec := DecodeH3Cell(enc)
	if dec != c {
		t.Errorf("round trip = %x, want %x", dec, c)
	}
}

func TestParseH3CellHex(t *testing.T) {
	c, err := ParseH3CellHex("89283082803ffff")
	if err != nil {
		t.Fatal(err)
	}
	if c == 0 {
		t.Error("zero cell")
	}
	if FormatH3CellHex(c) != "89283082803ffff" {
		t.Errorf("hex round trip mismatch: %s", FormatH3CellHex(c))
	}
}
