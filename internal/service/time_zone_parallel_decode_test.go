package service

import (
	"math/rand/v2"
	"testing"
	"time"
)

// zoneParallelRows sits just above parallelDecodeRecordThreshold so the
// buffered Process path fans out per-segment decode.
const zoneParallelRows = parallelDecodeRecordThreshold + 3_331

// zoneParallelInstants returns zoneParallelRows seeded-random instants
// spread over 2026 — both Europe/Berlin DST changes inside, and in
// random order so every decode segment straddles many offset spans.
func zoneParallelInstants() []int64 {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	r := rand.New(rand.NewPCG(14, 4))
	out := make([]int64, zoneParallelRows)
	for i := range out {
		out[i] = start + r.Int64N(end-start)
	}
	return out
}

// zoneParallelRanges labels the two DST days and the evening before
// each, so a UTC-day bug moves rows between labels and "unmatched".
const zoneParallelRanges = `{"ranges":[` +
	`{"label":"spring_eve","start":"2026-03-28","end":"2026-03-28"},` +
	`{"label":"spring","start":"2026-03-29","end":"2026-03-29"},` +
	`{"label":"fall_eve","start":"2026-10-24","end":"2026-10-24"},` +
	`{"label":"fall","start":"2026-10-25","end":"2026-10-25"},` +
	`{"label":"summer","start":"2026-06-01","end":"2026-08-31"}]}`

func zoneParallelRangeLabel(day string) string {
	switch {
	case day == "2026-03-28":
		return "spring_eve"
	case day == "2026-03-29":
		return "spring"
	case day == "2026-10-24":
		return "fall_eve"
	case day == "2026-10-25":
		return "fall"
	case day >= "2026-06-01" && day <= "2026-08-31":
		return "summer"
	}
	return "unmatched"
}

func zoneParallelNumber(t *testing.T, v any) float64 {
	t.Helper()
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	}
	t.Fatalf("not a number: %v (%T)", v, v)
	return 0
}
