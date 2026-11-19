package processing

import (
	"math/rand"
	"testing"
)

// BenchmarkPreviousState is ATTR_PREVIOUS_STATE over a 200,000-record
// long panel (20,000 u64 subjects × 10 time points, past 2^53) in four
// input orders, each through Compute (the check picks the path) and
// through the bucket-and-sort path alone:
//
//   - sorted: subject runs in order — Compute takes the sorted path;
//   - shuffled: the check stops within a few records — Compute pays the
//     bucket-and-sort path plus that;
//   - late_break: sorted but for the last record, which re-opens the
//     first subject — the check's worst case, a full pass before the
//     bucket-and-sort path.
//
// Measured (Apple M-series, -count=3): sorted/compute 12.7 ms, 8.1 MB,
// 164 allocs vs sorted/bucketed 30.5 ms, 17.0 MB, 160k allocs;
// shuffled/compute 59.5 ms vs shuffled/bucketed 60.0 ms (the check is
// noise; +3.2 MB for the rows slice); late_break/compute 38.5 ms vs
// late_break/bucketed 36.8 ms (the one-pass check, about 5%).
//
//	go test -run '^$' -bench BenchmarkPreviousState -benchmem ./internal/processing/
func BenchmarkPreviousState(b *testing.B) {
	s := psSortedSchema(b)
	const subjects, points = 20_000, 10
	sorted := make([]psRow, 0, subjects*points)
	rng := rand.New(rand.NewSource(485))
	for subj := 0; subj < subjects; subj++ {
		for k := 0; k < points; k++ {
			sorted = append(sorted, psRow{subj: subj, order: k, state: rng.Intn(3)})
		}
	}
	shuffled := make([]psRow, len(sorted))
	for i, j := range rng.Perm(len(sorted)) {
		shuffled[i] = sorted[j]
	}
	lateBreak := append(append([]psRow(nil), sorted...), psRow{subj: 0, order: points, state: 0})

	for _, c := range []struct {
		name string
		rows []psRow
	}{{"sorted", sorted}, {"shuffled", shuffled}, {"late_break", lateBreak}} {
		recs := make([]*Record, len(c.rows))
		for i, r := range c.rows {
			recs[i] = psSortedRecord(b, s, r)
		}
		a := psAttr(b, s, "uid")
		b.Run(c.name+"/compute", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := a.Compute(recs, "st"); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(c.name+"/bucketed", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a.computeBucketed(recs, a.reset(len(recs)))
			}
		})
	}
}
