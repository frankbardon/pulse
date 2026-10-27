//go:build heavy

package processing

import (
	"testing"
)

func TestReferenceOracle_StudentizedRangeInverse(t *testing.T) {
	doc := loadReference[tukeyInverseCase](t, "studentized_range_inverse")
	got := parallelEval(len(doc.Cases), func(i int) float64 {
		k := doc.Cases[i]
		return studentizedRangeInverse(k.Alpha, int(k.K), k.DF)
	})
	c := newOracleCheck(t, "studentizedRangeInverse", oracleRelTol, oracleQuantileAbsFloor)
	for i, k := range doc.Cases {
		c.check(got[i], k.Q, "alpha=%g, k=%g, df=%g", k.Alpha, k.K, k.DF)
	}
	c.done(len(doc.Cases))
}
