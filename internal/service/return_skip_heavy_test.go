//go:build heavy

package service

import "testing"

// TestReturnSkipsComputation_LongArms runs TestReturnSkipsComputation's
// arms that need a cohort above the parallel-decode threshold.
func TestReturnSkipsComputation_LongArms(t *testing.T) {
	runReturnSkipsComputation(t, true)
}

// TestReturnSkipsComputation_MatricesLongArms runs
// TestReturnSkipsComputation_Matrices' arms that need a cohort above the
// parallel-decode threshold.
func TestReturnSkipsComputation_MatricesLongArms(t *testing.T) {
	runReturnSkipsComputationMatrices(t, true)
}
