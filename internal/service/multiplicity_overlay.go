package service

import (
	"math"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// multiplicity_overlay.go is the overlay half of the multiple-comparison
// fold (U13): where each inferential overlay kind carries its p-values
// (the p-site table), how a layer's p-values are filed into correction
// families, and where the adjusted figures are written. The base payload
// is never touched — the adjusted figures ride the layer's own
// additive slots (OverlaySummary.PAdjusted / SignificantAdjusted, the
// parallel OverlayPayload.PAdjusted / SignificantAdjusted matrices and
// the OverlayLayer.Multiplicity echo).

// overlayPSite names where an inferential overlay kind carries its
// p-values. The placement is not uniform across the catalog, so a
// generic Summary.PValue walk would miss every cell-valued, VS_REF and
// panel kind; the table below is the one place that knows.
type overlayPSite int

const (
	// pSiteSummaryPValue: one p on the layer's Summary.PValue (SCALAR
	// kinds).
	pSiteSummaryPValue overlayPSite = iota + 1
	// pSiteSeriesPValue: one p per SERIES entry, on its
	// Summary.PValue.
	pSiteSeriesPValue
	// pSiteSeriesStatistic: one p per SERIES entry, on its
	// Summary.Statistic (the VS_REF kinds carry their p there; the
	// Statistic is never overwritten).
	pSiteSeriesStatistic
	// pSiteMatrixCell: one p per present MATRIX cell, its float64
	// Value.
	pSiteMatrixCell
	// pSiteMatrixPanel: a vector of p-values per present MATRIX cell,
	// its []float64 Value; every element joins the family.
	pSiteMatrixPanel
)

// overlayPSites is the p-site table: every overlay kind the catalog
// flags Inferential has exactly one entry
// (TestOverlayPSites_CoverEveryInferentialKind). A kind absent here
// contributes nothing to any family.
var overlayPSites = map[types.OverlayKind]overlayPSite{
	// Request (crosstab) host.
	types.OverlayKindChiSqMatrix:               pSiteSummaryPValue,
	types.OverlayKindChiSqRow:                  pSiteSeriesPValue,
	types.OverlayKindChiSqCol:                  pSiteSeriesPValue,
	types.OverlayKindFisherExactCell:           pSiteMatrixCell,
	types.OverlayKindPairwisePropZ:             pSiteMatrixCell,
	types.OverlayKindPairwiseProbitT:           pSiteMatrixCell,
	types.OverlayKindPairwiseWelchT:            pSiteMatrixCell,
	types.OverlayKindPairwiseTwoMeansZ:         pSiteMatrixCell,
	types.OverlayKindPairwiseWeightedTwoMeansZ: pSiteMatrixCell,
	// Compose host.
	types.OverlayKindPropZCell:  pSiteMatrixCell,
	types.OverlayKindTCell:      pSiteMatrixCell,
	types.OverlayKindZCell:      pSiteMatrixCell,
	types.OverlayKindPropZPanel: pSiteMatrixPanel,
	types.OverlayKindTVsRef:     pSiteSeriesStatistic,
	types.OverlayKindZVsRef:     pSiteSeriesStatistic,
	types.OverlayKindChiSqVsRef: pSiteSummaryPValue,
	// Facet host.
	types.OverlayKindChiSqVsPop: pSiteSummaryPValue,
	types.OverlayKindKSVsPop:    pSiteSummaryPValue,
}

// overlayFamilyKey keys one overlay layer's family. `layer` is local to
// the layer (its slot inside scope), so two layers never mix; `request`
// and `compose` share multFamilyKey with the tests, so a request-family
// layer pools with the request's tests and post-tests.
//
// `row` / `column` (E2-S2) will key per row / column index inside the
// layer; until then they are not collected.
func overlayFamilyKey(scope, slot string, family types.MultiplicityFamily) (string, bool) {
	switch family {
	case types.MultiplicityFamilyLayer:
		return scope + slot + "#" + string(family), true
	case types.MultiplicityFamilyRequest, types.MultiplicityFamilyCompose:
		return multFamilyKey(scope, family), true
	}
	return "", false
}

// collectOverlaySites files every member layer's p-values (index-aligned
// with its resolved slot) under its family. A non-member slot, a layer
// whose kind is not the slot's, or a kind with no p-site contributes
// nothing.
func collectOverlaySites(fams *multFamilies, scope string, slots []descx.ResolvedMultiplicity, layers []types.OverlayLayer) {
	for i, rm := range slots {
		if !rm.Member || i >= len(layers) {
			continue
		}
		layer := &layers[i]
		if string(layer.Kind) != rm.Operator {
			continue
		}
		site, ok := overlayPSites[layer.Kind]
		if !ok {
			continue
		}
		key, ok := overlayFamilyKey(scope, rm.Slot, rm.Family)
		if !ok {
			continue
		}
		collectLayerSites(fams, key, rm, layer, site)
	}
}

// collectLayerSites adds layer's p-values (placed per site) to the
// family key under rm's method, wiring each site's write to the layer's
// adjusted slots and its echo.
func collectLayerSites(fams *multFamilies, key string, rm descx.ResolvedMultiplicity, layer *types.OverlayLayer, site overlayPSite) {
	method, family, alpha := rm.Method, rm.Family, rm.Alpha
	echo := func(m int) {
		layer.Multiplicity = &types.AppliedMultiplicity{Method: method, Family: family, Alpha: alpha, M: m}
	}
	add := func(p float64, write func(adjusted float64)) {
		fams.add(key, family, method, multSite{p: p, write: func(adjusted float64, m int) {
			write(adjusted)
			echo(m)
		}})
	}
	summarySite := func(s *types.OverlaySummary, p *float64) {
		if p == nil {
			return
		}
		add(*p, func(adjusted float64) { writeSummaryAdjusted(s, adjusted, alpha) })
	}
	switch site {
	case pSiteSummaryPValue:
		if layer.Summary != nil {
			summarySite(layer.Summary, layer.Summary.PValue)
		}
	case pSiteSeriesPValue, pSiteSeriesStatistic:
		if layer.Payload.Series == nil {
			return
		}
		for e := range layer.Payload.Series.Entries {
			s := &layer.Payload.Series.Entries[e].Summary
			if site == pSiteSeriesPValue {
				summarySite(s, s.PValue)
			} else {
				summarySite(s, s.Statistic)
			}
		}
	case pSiteMatrixCell, pSiteMatrixPanel:
		collectMatrixSites(layer, alpha, site == pSiteMatrixPanel, add)
	}
}

// writeSummaryAdjusted records an adjusted p on a summary beside its raw
// figure; the flag compares against the resolved overlay alpha and is
// absent when the adjusted p is undefined.
func writeSummaryAdjusted(s *types.OverlaySummary, adjusted, alpha float64) {
	s.PAdjusted = &adjusted
	s.SignificantAdjusted = nil
	if !math.IsNaN(adjusted) {
		sig := adjusted < alpha
		s.SignificantAdjusted = &sig
	}
}

// collectMatrixSites allocates the parallel p_adjusted /
// significant_adjusted matrices on the base matrix's coordinates and
// files each present p-cell (or each panel element) as a site writing
// into them. The base matrix is read, never written.
func collectMatrixSites(layer *types.OverlayLayer, alpha float64, panel bool, add func(p float64, write func(adjusted float64))) {
	base := layer.Payload.Matrix
	if base == nil {
		return
	}
	pAdj := parallelMatrix(base, "p_adjusted")
	sigAdj := parallelMatrix(base, "significant_adjusted")
	found := false
	for r := range base.Cells {
		for c := range base.Cells[r] {
			cell := base.Cells[r][c]
			if !cell.Present {
				continue
			}
			pc, sc := &pAdj.Cells[r][c], &sigAdj.Cells[r][c]
			if !panel {
				p, ok := cell.Value.(float64)
				if !ok {
					continue
				}
				found = true
				add(p, func(adjusted float64) {
					*pc = types.MatrixCell{Value: adjusted, Present: true}
					if !math.IsNaN(adjusted) {
						*sc = types.MatrixCell{Value: adjusted < alpha, Present: true}
					}
				})
				continue
			}
			ps, ok := cell.Value.([]float64)
			if !ok {
				continue
			}
			found = true
			adjVec := make([]float64, len(ps))
			sigVec := make([]*bool, len(ps))
			*pc = types.MatrixCell{Value: adjVec, Present: true}
			*sc = types.MatrixCell{Value: sigVec, Present: true}
			for k, p := range ps {
				adjVec[k] = math.NaN()
				add(p, func(adjusted float64) {
					adjVec[k] = adjusted
					if !math.IsNaN(adjusted) {
						sig := adjusted < alpha
						sigVec[k] = &sig
					}
				})
			}
		}
	}
	if found {
		layer.Payload.PAdjusted = pAdj
		layer.Payload.SignificantAdjusted = sigAdj
	}
}

// parallelMatrix returns an all-absent matrix on base's coordinates
// (headers, keys, cell grid) labelled label. Margins and the grand total
// stay absent: the adjusted twins carry cell p-values only.
func parallelMatrix(base *types.MatrixPayload, label string) *types.MatrixPayload {
	cells := make([][]types.MatrixCell, len(base.Cells))
	for r := range base.Cells {
		cells[r] = make([]types.MatrixCell, len(base.Cells[r]))
	}
	return &types.MatrixPayload{
		RowHeader:    base.RowHeader,
		ColumnHeader: base.ColumnHeader,
		RowKeys:      base.RowKeys,
		ColumnKeys:   base.ColumnKeys,
		Cells:        cells,
		CellLabel:    label,
	}
}
