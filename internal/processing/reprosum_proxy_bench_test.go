package processing

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// naiveSumProxy is sumAggregator's UpdateRow as it was before the
// binned switch (U51 E2-S3): a naive float64 +=. It exists only to price
// the switch; sumAggregator itself now accumulates in internal/reprosum.
type naiveSumProxy struct{ sum float64 }

func (a *naiveSumProxy) UpdateRow(r *Record, field string) error {
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
	}
	return nil
}

func (a *naiveSumProxy) Finalize() (float64, error) { return a.sum, nil }

// BenchmarkReproSum_EngineProxy is the end-to-end per-row proxy for the
// archive-scale stop rule: the streaming engine's per-record aggregation
// step (interface dispatch, NumericValue's field lookup and null test,
// the accumulate) over 2^20 heap records, naive AGG_SUM against the
// real (binned) sumAggregator. The ratio of the two ns/row figures is
// what the switch to the binned kernel costs per row before decode.
func BenchmarkReproSum_EngineProxy(b *testing.B) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{{Name: "score", Type: encoding.FieldTypeF64}},
	}
	r := rand.New(rand.NewPCG(1, 2))
	records := make([]*Record, 1<<20)
	for i := range records {
		v := math.Ldexp(r.Float64(), r.IntN(20))
		if r.IntN(2) == 0 {
			v = -v
		}
		records[i] = NewRecord(schema, map[string]float64{"score": v})
	}
	run := func(b *testing.B, agg OnlineAggregator) {
		for b.Loop() {
			for _, rec := range records {
				if err := agg.UpdateRow(rec, "score"); err != nil {
					b.Fatal(err)
				}
			}
		}
		if _, err := agg.Finalize(); err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(records)), "ns/row")
	}
	b.Run("naive", func(b *testing.B) { run(b, &naiveSumProxy{}) })
	b.Run("binned", func(b *testing.B) { run(b, &sumAggregator{}) })
}
