package processing

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/overlayhost"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// Part names OVERLAY_CORRESPONDENCE writes into OverlayPayload.Parts.
const (
	// CorrespondencePartRowCoordinates is the rows × dimensions map
	// coordinate matrix (principal or standard per normalization).
	CorrespondencePartRowCoordinates = "row_coordinates"
	// CorrespondencePartColumnCoordinates is the columns × dimensions
	// map coordinate matrix.
	CorrespondencePartColumnCoordinates = "column_coordinates"
	// CorrespondencePartRowQuality is the per-row quality series: cos²
	// summed over the retained dimensions.
	CorrespondencePartRowQuality = "row_quality"
	// CorrespondencePartRowContribution is the rows × dimensions
	// contribution matrix: each row's share of a dimension's inertia.
	CorrespondencePartRowContribution = "row_contribution"
	// CorrespondencePartColumnQuality is the per-column quality series.
	CorrespondencePartColumnQuality = "column_quality"
	// CorrespondencePartColumnContribution is the columns × dimensions
	// contribution matrix.
	CorrespondencePartColumnContribution = "column_contribution"
)

// correspondenceDimHeader labels the synthetic dim_k column axis of
// the coordinate and contribution parts.
var correspondenceDimHeader = types.AxisHeader{Fields: []string{"dimension"}, Types: []string{"dimension"}}

// applyCorrespondence is the OVERLAY_CORRESPONDENCE handler: a simple
// correspondence analysis of the host table.
//
//	P = O / n,  r = P·1,  c = Pᵀ·1
//	S = D_r^-1/2 (P − r cᵀ) D_c^-1/2 = U Σ Vᵀ   (linalg.SVD)
//	λ_k = σ_k²                         principal inertia, k = 1 … K,
//	                                   K = min(rows, cols) − 1
//	Φ = D_r^-1/2 U,  Γ = D_c^-1/2 V    standard coordinates
//	F = Φ Σ,  G = Γ Σ                  principal coordinates
//
// — R's ca::ca. The map coordinates follow params.normalization:
// symmetric = (F, G), row_principal = (F, Γ), column_principal = (Φ, G),
// over the first params.dimensions dimensions. quality_i = Σ_{k≤d}
// F_ik² / d_i² (d_i² = the row profile's squared χ² distance to the
// centroid, Σ_j S_ij² / r_i); contribution_ik = r_i F_ik² / λ_k = U_ik².
// Columns mirror rows. Signs are linalg.SVD's: each dimension's
// (U, V) pair is flipped so the dominant component of V is positive.
//
// The table: absent cells are 0; a cell aggregation other than
// AGG_COUNT / AGG_SUM (when the host knows it) or a negative /
// non-finite cell is PULSE_OVERLAY_NONNEGATIVE_REQUIRED. Rows and
// columns whose total is 0 have no profile: they are dropped, one
// PULSE_OVERLAY_REF_ZERO warning per axis {kind, axis, dropped,
// dropped_keys, reason: "zero_margin"}, and the analysis runs on the
// rest. Fewer than 2 × 2 kept points is
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE; an explicit dimensions
// above K is PULSE_OVERLAY_PARAM_MISSING (the default 2 is capped at
// K). Every figure is scale-invariant, so a weighted host's Σw table
// is read as is — no Kish scaling under either weight kind.
//
// A dimension whose singular value is within linalg.RankTolerance of
// zero carries no information (its singular vectors are arbitrary):
// its coordinates and contributions are NaN (null on the wire) and it
// adds nothing to quality. A point whose profile sits on the centroid
// (d_i² = 0) has an undefined quality (NaN).
//
// Output: a SERIES primary of the K principal inertias (keys dim_1 …
// dim_K; Statistic = λ_k, Parameters {share, cumulative_share,
// singular_value}) and six parts on the KEPT host keys × dim_1 …
// dim_d. Summary: Statistic = total inertia Σ S_ij² (χ²/n), Parameters
// {dimensions, retained_share, share_dim_<k> for every k}.
func applyCorrespondence(spec *types.OverlaySpec, host *CrosstabHostView) (types.OverlayLayer, []types.OverlayWarning, error) {
	return applyCorrespondenceAt(spec, host, -1)
}

// applyCorrespondenceAt is applyCorrespondence for the spec at index of
// Request.Overlays (-1 unknown): the MATRIX dispatcher passes it so the
// params, cell-aggregation and dimensions refusals carry the same
// details — index included — as predict's (the shared overlayhost
// refusals).
func applyCorrespondenceAt(spec *types.OverlaySpec, host *CrosstabHostView, index int) (types.OverlayLayer, []types.OverlayWarning, error) {
	params, cerr := overlayhost.ParseCorrespondenceParams(spec, index)
	if cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	if cerr := overlayhost.NonnegativeCellRefusal(spec.Kind, host.cellType, index); cerr != nil {
		return types.OverlayLayer{}, nil, cerr
	}
	payload := host.Payload()
	rowCount, colCount := host.RowCount(), host.ColumnCount()
	observed := make([][]float64, rowCount)
	rowTotals := make([]float64, rowCount)
	colTotals := make([]float64, colCount)
	for i := 0; i < rowCount; i++ {
		observed[i] = make([]float64, colCount)
		for j := 0; j < colCount; j++ {
			if i >= len(payload.Cells) || j >= len(payload.Cells[i]) || !payload.Cells[i][j].Present {
				continue // absent cell = 0
			}
			v, ok := host.CellAt(i, j)
			if !ok {
				v = math.NaN()
			}
			if cerr := overlayhost.NonnegativeValueRefusal(spec.Kind, payload.RowKeys[i], payload.ColumnKeys[j], v); cerr != nil {
				return types.OverlayLayer{}, nil, cerr
			}
			observed[i][j] = v
			rowTotals[i] += v
			colTotals[j] += v
		}
	}
	keptRows, droppedRows := splitPositive(rowTotals)
	keptCols, droppedCols := splitPositive(colTotals)
	var warnings []types.OverlayWarning
	for _, d := range []struct {
		axis    string
		dropped []int
		keys    []types.AxisKey
	}{{"row", droppedRows, payload.RowKeys}, {"column", droppedCols, payload.ColumnKeys}} {
		if len(d.dropped) == 0 {
			continue
		}
		keys := make([]types.AxisKey, len(d.dropped))
		for i, idx := range d.dropped {
			keys[i] = d.keys[idx]
		}
		warnings = append(warnings, types.OverlayWarning{
			Code: string(errors.PULSE_OVERLAY_REF_ZERO),
			Message: "overlay " + string(spec.Kind) + " dropped " + d.axis +
				"s with a zero total: a point with no mass has no profile to place on the map",
			Details: map[string]any{
				"kind":         string(spec.Kind),
				"axis":         d.axis,
				"dropped":      d.dropped,
				"dropped_keys": keys,
				"reason":       "zero_margin",
			},
		})
	}
	r, c := len(keptRows), len(keptCols)
	if r < 2 || c < 2 {
		return types.OverlayLayer{}, warnings, errors.NewCodedErrorWithDetails(
			errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE,
			"overlay "+string(spec.Kind)+" requires at least 2 rows × 2 columns with a non-zero total",
			map[string]any{"kind": string(spec.Kind), "rows": r, "cols": c})
	}
	if cerr := overlayhost.CorrespondenceDimensionsRefusal(spec.Kind, params, r, c, index); cerr != nil {
		return types.OverlayLayer{}, warnings, cerr
	}
	K := overlayhost.MaxCorrespondenceDimensions(r, c)
	d := params.ResolveDimensions(K)

	var n float64
	for _, i := range keptRows {
		n += rowTotals[i]
	}
	rowMass := make([]float64, r)
	colMass := make([]float64, c)
	for a, i := range keptRows {
		rowMass[a] = rowTotals[i] / n
	}
	for b, j := range keptCols {
		colMass[b] = colTotals[j] / n
	}
	sData := make([]float64, r*c)
	rowSS := make([]float64, r) // Σ_j S_ij²
	colSS := make([]float64, c) // Σ_i S_ij²
	var total float64
	for a, i := range keptRows {
		for b, j := range keptCols {
			e := float64(rowMass[a] * colMass[b])
			s := (observed[i][j]/n - e) / math.Sqrt(e)
			sData[a*c+b] = s
			sq := float64(s * s)
			rowSS[a] += sq
			colSS[b] += sq
			total += sq
		}
	}
	sm, err := linalg.NewMatrix(r, c, sData)
	if err != nil {
		return types.OverlayLayer{}, warnings, err
	}
	svd, err := linalg.SVD(sm)
	if err != nil {
		return types.OverlayLayer{}, warnings, err
	}
	sigma := make([]float64, K)
	for k := range sigma {
		sigma[k] = svd.Values.At(k)
	}
	tol := linalg.RankTolerance(r, c, svd.Values.At(0))
	defined := func(k int) bool { return sigma[k] > tol }

	// Primary: the K principal inertias.
	entries := make([]types.SeriesEntry, K)
	dimKeys := overlayhost.DimAxisKeys(K)
	shares := map[string]float64{}
	var cum float64
	for k := 0; k < K; k++ {
		lambda := float64(sigma[k] * sigma[k])
		share := math.NaN() // an exactly independent table has no inertia to share
		if total > 0 {
			share = lambda / total
		}
		cum += share
		shares["share_"+overlayhost.DimAxisKeyPrefix+strconv.Itoa(k+1)] = share
		entries[k] = types.SeriesEntry{Key: dimKeys[k], Summary: types.OverlaySummary{
			Statistic: &lambda,
			Parameters: map[string]float64{
				"share":            share,
				"cumulative_share": cum,
				"singular_value":   sigma[k],
			},
		}}
	}

	// Coordinates, contributions and quality over the retained dims.
	rowPrincipal := params.Normalization != overlayhost.CorrespondenceColumnPrincipal
	colPrincipal := params.Normalization != overlayhost.CorrespondenceRowPrincipal
	points := func(sv *linalg.Matrix, mass, ss []float64, principal bool) (coords, contrib [][]types.MatrixCell, quality []float64) {
		m := len(mass)
		coords = make([][]types.MatrixCell, m)
		contrib = make([][]types.MatrixCell, m)
		quality = make([]float64, m)
		for p := 0; p < m; p++ {
			coords[p] = make([]types.MatrixCell, d)
			contrib[p] = make([]types.MatrixCell, d)
			rootMass := math.Sqrt(mass[p])
			var cos2 float64
			for k := 0; k < d; k++ {
				if !defined(k) {
					coords[p][k] = types.MatrixCell{Value: math.NaN(), Present: true}
					contrib[p][k] = types.MatrixCell{Value: math.NaN(), Present: true}
					continue
				}
				u := sv.At(p, k)
				std := u / rootMass
				f := float64(std * sigma[k])
				coord := std
				if principal {
					coord = f
				}
				coords[p][k] = types.MatrixCell{Value: coord, Present: true}
				contrib[p][k] = types.MatrixCell{Value: float64(u * u), Present: true}
				cos2 += float64(f * f)
			}
			// d_p² = ss_p / mass_p, so cos² = F² / d² = F² mass / ss.
			if ss[p] > 0 {
				quality[p] = cos2 * mass[p] / ss[p]
			} else {
				quality[p] = math.NaN()
			}
		}
		return coords, contrib, quality
	}
	rowCoords, rowContrib, rowQuality := points(svd.U, rowMass, rowSS, rowPrincipal)
	colCoords, colContrib, colQuality := points(svd.V, colMass, colSS, colPrincipal)

	rowKeys := make([]types.AxisKey, r)
	for a, i := range keptRows {
		rowKeys[a] = payload.RowKeys[i]
	}
	colKeys := make([]types.AxisKey, c)
	for b, j := range keptCols {
		colKeys[b] = payload.ColumnKeys[j]
	}
	retainedKeys := overlayhost.DimAxisKeys(d)
	pointMatrix := func(header types.AxisHeader, keys []types.AxisKey, cells [][]types.MatrixCell, label string) *types.OverlayPayload {
		return &types.OverlayPayload{Shape: types.OverlayShapeMatrix, Matrix: &types.MatrixPayload{
			RowHeader:        header,
			ColumnHeader:     correspondenceDimHeader,
			RowKeys:          keys,
			ColumnKeys:       append([]types.AxisKey(nil), retainedKeys...),
			Cells:            cells,
			CellLabel:        label,
			NormalizeApplied: types.CrosstabNormalizeNone,
		}}
	}
	pointSeries := func(keys []types.AxisKey, quality, mass, ss []float64) *types.OverlayPayload {
		es := make([]types.SeriesEntry, len(keys))
		for p := range keys {
			q := quality[p]
			es[p] = types.SeriesEntry{Key: keys[p], Summary: types.OverlaySummary{
				Statistic:  &q,
				Parameters: map[string]float64{"mass": mass[p], "inertia": ss[p]},
			}}
		}
		return &types.OverlayPayload{Shape: types.OverlayShapeSeries, Series: &types.SeriesPayload{Entries: es}}
	}

	totalInertia := total
	var retained float64
	for k := 0; k < d; k++ {
		retained += shares["share_"+overlayhost.DimAxisKeyPrefix+strconv.Itoa(k+1)]
	}
	shares["dimensions"] = float64(d)
	shares["retained_share"] = retained
	layer := types.OverlayLayer{
		Name:  overlayLayerName(spec),
		Kind:  spec.Kind,
		Scope: spec.Scope,
		Ref:   spec.Ref,
		Payload: types.OverlayPayload{
			Shape:  types.OverlayShapeSeries,
			Series: &types.SeriesPayload{Entries: entries},
			Parts: map[string]*types.OverlayPayload{
				CorrespondencePartRowCoordinates:     pointMatrix(payload.RowHeader, rowKeys, rowCoords, CorrespondencePartRowCoordinates),
				CorrespondencePartColumnCoordinates:  pointMatrix(payload.ColumnHeader, colKeys, colCoords, CorrespondencePartColumnCoordinates),
				CorrespondencePartRowContribution:    pointMatrix(payload.RowHeader, rowKeys, rowContrib, CorrespondencePartRowContribution),
				CorrespondencePartColumnContribution: pointMatrix(payload.ColumnHeader, colKeys, colContrib, CorrespondencePartColumnContribution),
				CorrespondencePartRowQuality:         pointSeries(rowKeys, rowQuality, rowMass, rowSS),
				CorrespondencePartColumnQuality:      pointSeries(colKeys, colQuality, colMass, colSS),
			},
		},
		Summary: &types.OverlaySummary{Statistic: &totalInertia, Parameters: shares},
	}
	return layer, warnings, nil
}

// splitPositive partitions indices by whether totals[i] > 0.
func splitPositive(totals []float64) (kept, dropped []int) {
	for i, t := range totals {
		if t > 0 {
			kept = append(kept, i)
		} else {
			dropped = append(dropped, i)
		}
	}
	return kept, dropped
}
