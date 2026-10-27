//go:build heavy

package service

import "testing"

// TestReturnSkipsComputation_LongArms runs TestReturnSkipsComputation's
// arms that need a cohort above the parallel-decode threshold.
func TestReturnSkipsComputation_LongArms(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump

	runReturnSkipsComputation(t, true)
}

// TestReturnSkipsComputation_MatricesLongArms runs
// TestReturnSkipsComputation_Matrices' arms that need a cohort above the
// parallel-decode threshold.
func TestReturnSkipsComputation_MatricesLongArms(t *testing.T) {
	// serial: reads deltas of process-wide internal/processing work counters, which concurrent tests bump

	runReturnSkipsComputationMatrices(t, true)
}
