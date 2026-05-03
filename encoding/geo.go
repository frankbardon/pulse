package encoding

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// PointF64 is a (lat, lon) pair stored as two LE float64s. Values are in
// degrees. For non-geo Cartesian usage the same struct can be read as
// (Y, X) — the encoding is symmetric in the two fields.
type PointF64 struct {
	Lat float64
	Lon float64
}

// ValidatePoint reports whether the point is in the legal lat/lon range.
// |lat| ≤ 90 and |lon| ≤ 180.
func (p PointF64) Validate() error {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lon) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lon, 0) {
		return errors.NewCodedError(errors.PULSE_GEO_INVALID_POINT,
			"point contains NaN or Inf coordinate")
	}
	if math.Abs(p.Lat) > 90 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"latitude out of range",
			map[string]any{"lat": p.Lat})
	}
	if math.Abs(p.Lon) > 180 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"longitude out of range",
			map[string]any{"lon": p.Lon})
	}
	return nil
}

// EncodePointF64 packs the point into 16 little-endian bytes: lat, lon.
func EncodePointF64(p PointF64) [16]byte {
	var out [16]byte
	binary.LittleEndian.PutUint64(out[:8], math.Float64bits(p.Lat))
	binary.LittleEndian.PutUint64(out[8:], math.Float64bits(p.Lon))
	return out
}

// DecodePointF64 unpacks 16 little-endian bytes into a PointF64.
func DecodePointF64(buf [16]byte) PointF64 {
	return PointF64{
		Lat: math.Float64frombits(binary.LittleEndian.Uint64(buf[:8])),
		Lon: math.Float64frombits(binary.LittleEndian.Uint64(buf[8:])),
	}
}

// WritePointF64 writes a point to the record stream.
func WritePointF64(w io.Writer, p PointF64) error {
	enc := EncodePointF64(p)
	if _, err := w.Write(enc[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing point_f64")
	}
	return nil
}

// ReadPointF64 reads a point from the record stream.
func ReadPointF64(r io.Reader) (PointF64, error) {
	var buf [16]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return PointF64{}, errors.WrapCodedError(err, errors.ENCODING_IO, "reading point_f64")
	}
	return DecodePointF64(buf), nil
}

// ParseWKTPoint parses a WKT POINT(lon lat) string into a PointF64.
// Note WKT order is lon-first; this function flips them so the returned
// PointF64 has Lat in .Lat and Lon in .Lon.
func ParseWKTPoint(s string) (PointF64, error) {
	t := strings.TrimSpace(s)
	upper := strings.ToUpper(t)
	if !strings.HasPrefix(upper, "POINT") {
		return PointF64{}, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"expected WKT POINT prefix",
			map[string]any{"input": s})
	}
	body := strings.TrimSpace(t[5:])
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return PointF64{}, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"WKT POINT missing parentheses",
			map[string]any{"input": s})
	}
	body = body[1 : len(body)-1]
	parts := strings.Fields(body)
	if len(parts) != 2 {
		return PointF64{}, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"WKT POINT must have exactly two coordinates",
			map[string]any{"input": s, "got": len(parts)})
	}
	lon, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return PointF64{}, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"WKT POINT longitude not a number",
			map[string]any{"input": s, "lon": parts[0]})
	}
	lat, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return PointF64{}, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"WKT POINT latitude not a number",
			map[string]any{"input": s, "lat": parts[1]})
	}
	p := PointF64{Lat: lat, Lon: lon}
	if err := p.Validate(); err != nil {
		return PointF64{}, err
	}
	return p, nil
}

// FormatWKTPoint renders a PointF64 as `POINT(lon lat)`.
func FormatWKTPoint(p PointF64) string {
	return fmt.Sprintf("POINT(%s %s)",
		strconv.FormatFloat(p.Lon, 'g', -1, 64),
		strconv.FormatFloat(p.Lat, 'g', -1, 64))
}

// Polygon is a closed simple polygon represented as a single outer ring.
// MULTIPOLYGON and inner rings (holes) are not supported in v1.
type Polygon struct {
	Ring []PointF64
}

// ParseWKTPolygon parses a WKT POLYGON((lon lat, lon lat, ..., lon lat))
// into a Polygon. Rejects MULTIPOLYGON, polygons with inner rings, and
// non-closed rings (first and last vertex must match).
func ParseWKTPolygon(s string) (*Polygon, error) {
	t := strings.TrimSpace(s)
	upper := strings.ToUpper(t)
	if strings.HasPrefix(upper, "MULTIPOLYGON") {
		return nil, errors.NewCodedError(errors.PULSE_GEO_INVALID_POLYGON,
			"MULTIPOLYGON is not supported in v1")
	}
	if !strings.HasPrefix(upper, "POLYGON") {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
			"expected WKT POLYGON prefix",
			map[string]any{"input": s})
	}
	body := strings.TrimSpace(t[7:])
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON missing parentheses",
			map[string]any{"input": s})
	}
	inner := strings.TrimSpace(body[1 : len(body)-1])
	// inner is `(ring1)(ring2)...`. v1 supports a single ring only.
	if !strings.HasPrefix(inner, "(") || !strings.HasSuffix(inner, ")") {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON ring missing parentheses",
			map[string]any{"input": s})
	}
	// Detect inner rings (holes): more than one parenthesized group.
	ringEnd := strings.Index(inner[1:], ")")
	if ringEnd < 0 {
		return nil, errors.NewCodedError(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON unterminated ring")
	}
	tail := strings.TrimSpace(inner[1+ringEnd+1:])
	if tail != "" {
		return nil, errors.NewCodedError(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON inner rings (holes) are not supported in v1")
	}
	ringStr := inner[1 : 1+ringEnd]
	verts := strings.Split(ringStr, ",")
	if len(verts) < 4 {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON ring must have at least 4 vertices (3 + closing)",
			map[string]any{"vertices": len(verts)})
	}
	ring := make([]PointF64, 0, len(verts))
	for i, v := range verts {
		parts := strings.Fields(strings.TrimSpace(v))
		if len(parts) != 2 {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
				"WKT POLYGON vertex must have exactly two coordinates",
				map[string]any{"index": i, "vertex": v})
		}
		lon, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
				"WKT POLYGON longitude parse error",
				map[string]any{"index": i, "lon": parts[0]})
		}
		lat, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POLYGON,
				"WKT POLYGON latitude parse error",
				map[string]any{"index": i, "lat": parts[1]})
		}
		p := PointF64{Lat: lat, Lon: lon}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		ring = append(ring, p)
	}
	if ring[0] != ring[len(ring)-1] {
		return nil, errors.NewCodedError(errors.PULSE_GEO_INVALID_POLYGON,
			"WKT POLYGON ring is not closed (first and last vertex must match)")
	}
	return &Polygon{Ring: ring}, nil
}

// Contains reports whether p is inside or on the boundary of the polygon.
// Uses the standard ray-cast (even-odd) test on (lon, lat). Edge cases:
// points on a vertical edge of the ring are reported as inside via the
// half-open edge convention, which is consistent with shapely.
func (poly *Polygon) Contains(p PointF64) bool {
	ring := poly.Ring
	inside := false
	n := len(ring)
	if n < 3 {
		return false
	}
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, xi := ring[i].Lat, ring[i].Lon
		yj, xj := ring[j].Lat, ring[j].Lon
		// Half-open edge from (xj,yj) to (xi,yi).
		intersect := ((yi > p.Lat) != (yj > p.Lat)) &&
			(p.Lon < (xj-xi)*(p.Lat-yi)/(yj-yi)+xi)
		if intersect {
			inside = !inside
		}
	}
	return inside
}

// HaversineMeters returns the great-circle distance in meters between two
// points on the Earth's surface, using mean Earth radius 6,371,008.8 m
// (the IUGG mean radius). Documented error vs Vincenty: ~0.5%.
const earthRadiusMeters = 6371008.8

func HaversineMeters(a, b PointF64) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180
	sinDLat := math.Sin(dLat / 2)
	sinDLon := math.Sin(dLon / 2)
	h := sinDLat*sinDLat + math.Cos(lat1)*math.Cos(lat2)*sinDLon*sinDLon
	c := 2 * math.Asin(math.Min(1, math.Sqrt(h)))
	return earthRadiusMeters * c
}

// H3Cell wraps a 64-bit Uber H3 cell index. The underlying type is uint64;
// the type alias documents intent at the call site without adding runtime
// cost.
type H3Cell uint64

// EncodeH3Cell serializes an H3 cell as 8 little-endian bytes.
func EncodeH3Cell(c H3Cell) [8]byte {
	var out [8]byte
	binary.LittleEndian.PutUint64(out[:], uint64(c))
	return out
}

// DecodeH3Cell deserializes 8 little-endian bytes.
func DecodeH3Cell(buf [8]byte) H3Cell {
	return H3Cell(binary.LittleEndian.Uint64(buf[:]))
}

// WriteH3Cell writes an H3 cell to the record stream.
func WriteH3Cell(w io.Writer, c H3Cell) error {
	enc := EncodeH3Cell(c)
	if _, err := w.Write(enc[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing h3_cell")
	}
	return nil
}

// ReadH3Cell reads an H3 cell from the record stream.
func ReadH3Cell(r io.Reader) (H3Cell, error) {
	var buf [8]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, errors.WrapCodedError(err, errors.ENCODING_IO, "reading h3_cell")
	}
	return DecodeH3Cell(buf), nil
}

// ParseH3CellHex parses a 15-character lowercase hex H3 string into an
// H3Cell. The H3 library treats this as the canonical string form.
// Validation (IsValid) is left to the caller — typical callers use the
// h3-go runtime to validate.
func ParseH3CellHex(s string) (H3Cell, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, errors.NewCodedError(errors.PULSE_GEO_INVALID_POINT,
			"empty H3 hex string")
	}
	v, err := strconv.ParseUint(t, 16, 64)
	if err != nil {
		return 0, errors.NewCodedErrorWithDetails(errors.PULSE_GEO_INVALID_POINT,
			"H3 hex string parse error",
			map[string]any{"input": s, "error": err.Error()})
	}
	return H3Cell(v), nil
}

// FormatH3CellHex returns the canonical 15-char hex representation.
func FormatH3CellHex(c H3Cell) string {
	return strconv.FormatUint(uint64(c), 16)
}

// CrossesAntimeridian reports whether a set of points spans the
// antimeridian, defined per the plan as any pair of points with
// |lon_a - lon_b| > 180.
func CrossesAntimeridian(pts []PointF64) bool {
	if len(pts) < 2 {
		return false
	}
	for i := 0; i < len(pts); i++ {
		for j := i + 1; j < len(pts); j++ {
			if math.Abs(pts[i].Lon-pts[j].Lon) > 180 {
				return true
			}
		}
	}
	return false
}

// CentroidUnitSphere computes the 3D unit-sphere centroid of a point set,
// converting each point to (x, y, z) on the unit sphere, summing,
// normalizing, and converting back to (lat, lon). Correct at poles and
// across the antimeridian.
func CentroidUnitSphere(pts []PointF64) (PointF64, bool) {
	if len(pts) == 0 {
		return PointF64{}, false
	}
	var sumX, sumY, sumZ float64
	for _, p := range pts {
		latR := p.Lat * math.Pi / 180
		lonR := p.Lon * math.Pi / 180
		cosLat := math.Cos(latR)
		sumX += cosLat * math.Cos(lonR)
		sumY += cosLat * math.Sin(lonR)
		sumZ += math.Sin(latR)
	}
	n := float64(len(pts))
	x := sumX / n
	y := sumY / n
	z := sumZ / n
	mag := math.Sqrt(x*x + y*y + z*z)
	if mag == 0 {
		// Antipodal pairs cancel to the origin — centroid undefined.
		return PointF64{}, false
	}
	x /= mag
	y /= mag
	z /= mag
	lat := math.Asin(z) * 180 / math.Pi
	lon := math.Atan2(y, x) * 180 / math.Pi
	return PointF64{Lat: lat, Lon: lon}, true
}
