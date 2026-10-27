package service

import (
	"math"
	"runtime"
)

// floatRelativelyClose is the comparator for Welford-merged
// aggregators: drift is bounded by 16*1e-15 relative to magnitude (or
// 1e-12 absolute when the magnitude is zero). The 16x band scales a
// 4x relative bound up by the
// squared-delta accumulation in AGG_VARIANCE — variance amplifies
// per-merge-step rounding by O(value^2), so at 4–8 partition
// boundaries on a magnitude-20K f64 cohort the relative drift is
// ~8.8e-15 (empirically measured), well within "still within
// ULP-bounded equivalence" for the Chan-Welford recurrence. Stddev
// sits well inside this bound by construction; AGG_AVERAGE needs no
// tolerance (a binned sum over an exact n).
func floatRelativelyClose(a, b float64) bool {
	if a == b {
		return true
	}
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return math.Abs(a-b) < 1e-12
	}
	return math.Abs(a-b)/scale < 16*1e-15
}

// workerLabel formats subtest names for table-driven worker-count
// sweeps. We use "numcpu" for runtime.NumCPU() so the test name stays
// readable even when CPU count varies across CI runners.
func workerLabel(w int) string {
	if w == runtime.NumCPU() {
		return "numcpu"
	}
	switch w {
	case 1:
		return "workers_1"
	case 2:
		return "workers_2"
	case 4:
		return "workers_4"
	}
	return "workers_" + itoa(w)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// toFloat coerces an aggregator's untyped result into a float64 for
// the ULP comparators. Mirrors the shape used by shard_parity_test —
// every mergeable aggregator under test here emits either float64
// (sum/avg/stddev/var) or int64 (count); both convert cleanly.
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	default:
		return math.NaN()
	}
}
