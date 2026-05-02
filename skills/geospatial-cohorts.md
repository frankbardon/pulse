---
name: geospatial-cohorts
description: point_f64 / h3_cell usage, centroid algorithm, antimeridian behavior, radius filter (meters), polygon WKT format, H3 resolution table
type: guide
applies_to: process, compose, predict, inspect
---

# Geospatial Cohorts (point_f64, h3_cell)

<skill_overview>
Pulse exposes two native geo types: `point_f64` (a packed lat/lon pair) and `h3_cell` (an Uber H3 index as `uint64`). This skill explains when to use each, the spatial filterers and groupers, and the v1 behavior at edge cases (antimeridian, polar, polygon shape limits).
</skill_overview>

## Type choice

| If you have… | Use… |
|---|---|
| Raw GPS / lat-lon pairs | `point_f64` |
| Pre-bucketed H3 indices from upstream | `h3_cell` |
| Polygon or multi-geometry shapes | **not yet** — wait for variable-length field support |

Polygons / WKB / multi-geometries are out of scope in v1. Store WKB as bytes only after Pulse adds variable-length field types.

## `point_f64` — packed (lat, lon) f64

Stored as 16 bytes: two little-endian f64s. Internally Pulse stores `(lat, lon)` in that order. WKT POINT, however, is `POINT(lon lat)` — note the order flip on import/export. Pulse handles this in coercion; users who write WKT directly must use lon-first.

Validation: `|lat| ≤ 90`, `|lon| ≤ 180`, no NaN/Inf. Out-of-range → `PULSE_GEO_INVALID_POINT`.

## `h3_cell` — Uber H3 index

Stored as 8 bytes: a `uint64` H3 cell index in canonical bit layout. `pulse inspect` surfaces the native resolution (the H3 resolution at which the cell was generated) when the import recorded it; otherwise the resolution is omitted.

H3 string form is 15 lowercase hex characters: `"89283082803ffff"`.

### H3 resolution table

| Resolution | Avg cell area | Edge length |
|---|---|---|
| 0  | ~4,250,547 km² | ~1107 km |
| 5  | ~252.9 km² | ~8.5 km |
| 7  | ~5.16 km² | ~1.22 km |
| 9  | ~0.105 km² (~10 ha) | ~174 m |
| 11 | ~2,150 m² | ~25 m |
| 13 | ~43.9 m² | ~3.5 m |
| 15 | ~0.895 m² | ~50 cm |

Pick the resolution that matches your analytical bucket. Resolution 9 is a common compromise for urban analytics.

## Spatial filterers

### `FILTER_GEO_WITHIN`

Polygon-containment filter. Polygon supplied as **WKT** in `Filterer.Expression`.

```json
{
  "type": "FILTER_GEO_WITHIN",
  "field": "location",
  "expression": "POLYGON((-122.5 37.7, -122.4 37.7, -122.4 37.8, -122.5 37.8, -122.5 37.7))"
}
```

v1 limits:

- POLYGON only — `MULTIPOLYGON` rejected.
- Single outer ring — inner rings (holes) rejected.
- Ring must be **closed**: first vertex equals last vertex.
- Algorithm: ray-cast (even-odd) point-in-polygon.

Errors → `PULSE_GEO_INVALID_POLYGON`.

### `FILTER_GEO_WITHIN_RADIUS_M`

Distance filter. Distance argument is **in meters** — the field name carries the unit so there is no ambiguity.

Configuration is shipped as JSON in `Filterer.Expression`:

```json
{
  "type": "FILTER_GEO_WITHIN_RADIUS_M",
  "field": "location",
  "expression": "{\"anchor\": \"POINT(-122.4194 37.7749)\", \"radius_m\": 5000}"
}
```

Distance: haversine formula on a 6,371,008.8 m mean Earth radius. Error vs Vincenty: ~0.5%. Adequate for cohort-level filtering; not adequate for survey-grade work.

## Spatial aggregators

### `AGG_GEO_CENTROID`

Computes the 3D unit-sphere centroid:

1. Convert each point to `(x, y, z)` on the unit sphere.
2. Sum and normalize.
3. Convert back to `(lat, lon)`.

Result type: `{lat: f64, lon: f64}` struct in the response row (not a new field type).

This algorithm is correct at the poles and across the antimeridian. It is the only safe centroid for global cohorts.

If the input set is exactly antipodal (sums to the origin) the centroid is undefined and the aggregation returns no result for that group.

### `AGG_GEO_BBOX`

Computes the four-tuple `(min_lat, min_lon, max_lat, max_lon)` of the input set.

Antimeridian rule: if any pair of points has `|lon_a - lon_b| > 180`, the input crosses the antimeridian. A flat min/max bbox is ambiguous in that case, so Pulse rejects with `PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS`. Recovery: split the cohort by hemisphere, or use `AGG_GEO_CENTROID` instead.

## Spatial groupers

### `GROUP_H3_CELL`

Buckets records into H3 cells.

```json
{
  "type": "GROUP_H3_CELL",
  "field": "location",
  "params": {"resolution": 9}
}
```

Input behavior:

- `point_f64`: resolution **required**. Convert each point to a cell at the requested resolution at run time.
- `h3_cell`: resolution **optional**. If supplied:
  - Lower than the cell's native resolution → walk to that parent.
  - Higher than the native resolution → `PULSE_GEO_INVALID_RESOLUTION` (parent walk only goes coarser).
  - Equal → no-op.

Out-of-range resolution (not in `[0, 15]`) → `PULSE_GEO_INVALID_RESOLUTION`.

## Coercion

### Importer

- **Decimal-pair → point**: two existing `f64` columns (lat, lon) merge into one `point_f64` via importer config.
- **WKT → point**: source column contains `"POINT(lon lat)"` strings.
- **Hex → h3**: source column contains 15-char hex strings; parsed as `uint64`.

### Exporter

| Format | Decimal128 | point_f64 | h3_cell |
|---|---|---|---|
| CSV / TSV / NDJSON / JSON-array | canonical decimal string | `POINT(lon lat)` | 15-char hex |
| Arrow / Parquet | native `Decimal128(p, s)` | `FixedSizeBinary(16)` + extension metadata | `UInt64` + extension metadata |
| Excel | number cell w/ scale-driven format | `"lon, lat"` text | text |

## Algorithm notes

### Haversine distance

Implementation uses the IUGG mean Earth radius **6,371,008.8 m**. For points across very long arcs the algorithm degrades gracefully — error vs WGS-84 ellipsoid (Vincenty) is bounded at ~0.5%. Document this trade-off in your output if cohort distance sensitivity matters.

### Centroid: unit-sphere math

```
For each point (lat, lon):
    x_i = cos(lat) * cos(lon)
    y_i = cos(lat) * sin(lon)
    z_i = sin(lat)

x_mean = mean(x_i)
y_mean = mean(y_i)
z_mean = mean(z_i)

mag = sqrt(x_mean² + y_mean² + z_mean²)

if mag == 0: undefined (antipodal cluster)

x_norm = x_mean / mag
y_norm = y_mean / mag
z_norm = z_mean / mag

centroid_lat = asin(z_norm)
centroid_lon = atan2(y_norm, x_norm)
```

This is correct at all latitudes and longitudes. A naive lat/lon mean is wrong for any cohort that spans the antimeridian or is near a pole; the unit-sphere mean is right.

## v1 deferred items

- Polygons as a stored field type (waiting on variable-length).
- `MULTIPOLYGON` and inner rings in `FILTER_GEO_WITHIN`.
- WGS-84 ellipsoid / Vincenty distance.
- H3 K-ring / disk aggregations.
- Spatial joins.
- Reverse geocoding / cell-to-name lookups.
