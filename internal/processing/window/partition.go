package window

// partition splits sortedIdx into contiguous groups whose partitionBy keys
// match. Empty partitionBy produces a single partition over all rows.
func partition(rows []map[string]any, sortedIdx []int, partitionBy []string) [][]int {
	if len(sortedIdx) == 0 {
		return nil
	}
	if len(partitionBy) == 0 {
		out := make([][]int, 1)
		// Copy: callers must not see the cache's slice as mutable through a partition.
		all := make([]int, len(sortedIdx))
		copy(all, sortedIdx)
		out[0] = all
		return out
	}

	var partitions [][]int
	start := 0
	for i := 1; i < len(sortedIdx); i++ {
		if !samePartition(rows, sortedIdx[i], sortedIdx[start], partitionBy) {
			seg := make([]int, i-start)
			copy(seg, sortedIdx[start:i])
			partitions = append(partitions, seg)
			start = i
		}
	}
	seg := make([]int, len(sortedIdx)-start)
	copy(seg, sortedIdx[start:])
	partitions = append(partitions, seg)
	return partitions
}

func samePartition(rows []map[string]any, a, b int, partitionBy []string) bool {
	ra, rb := rows[a], rows[b]
	for _, p := range partitionBy {
		if compareCell(ra[p], rb[p]) != 0 {
			return false
		}
	}
	return true
}
