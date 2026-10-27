//go:build heavy

package processing

import "testing"

// TestComponents_ParallelMergeMatchesSerial100K is the 100K-record tier
// of TestComponents_ParallelMergeMatchesSerial.
func TestComponents_ParallelMergeMatchesSerial100K(t *testing.T) {
	t.Parallel()

	runComponentsParallelMerge(t, []int{100_000})
}
