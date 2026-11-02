package processing

import "github.com/frankbardon/pulse/internal/weighting"

// The two-sample mean kernel shared by TEST_T / TEST_WELCH /
// TEST_Z_TWO_SAMPLE and the moment overlays that fold AGG_WELFORD
// cells (OVERLAY_T_CELL, OVERLAY_Z_CELL, OVERLAY_T_VS_REF,
// OVERLAY_Z_VS_REF, the pairwise Welch t and two-means z). The tests
// and the overlays are byte-equal by contract: both sides call these
// functions on the same moments, so a change here moves both together.

// groupMeans returns two test groups' reported means (levels) and
// their difference a − b. A group accumulated about its origin
// (originShift) reports origin + shifted mean and differences through
// shiftedMeanDiff, the reading meanLeg.diff gives an AGG_WELFORD cell.
// A nil origin (moments held at the data's scale) reads the means as
// they are.
func groupMeans(a, b *weighting.Welford, oa, ob *originShift) (levelA, levelB, diff float64) {
	if oa == nil || ob == nil {
		return a.Mean, b.Mean, a.Mean - b.Mean
	}
	return oa.level(a.Mean), ob.level(b.Mean), shiftedMeanDiff(oa.origin, a.Mean, ob.origin, b.Mean)
}

// welchSatterthwaite returns the Welch variance sum va/na + vb/nb (the
// squared standard error), the Welch–Satterthwaite denominator and the
// df num² / den.
func welchSatterthwaite(va, na, vb, nb float64) (num, den, df float64) {
	num = va/na + vb/nb
	den = (va*va)/(na*na*(na-1)) + (vb*vb)/(nb*nb*(nb-1))
	return num, den, (num * num) / den
}
