package processing

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/reprosum"
)

// binnedSumProxy is sumAggregator's UpdateRow with the naive += swapped
// for the binned reproducible kernel. It exists only to price the swap;
// no aggregator uses internal/reprosum yet.
type binnedSumProxy struct{ s reprosum.Sum }

func (a *binnedSumProxy) UpdateRow(r *Record, field string) error {
	if v, ok := r.NumericValue(field); ok {
		a.s.Add(v)
	}
	return nil
}

func (a *binnedSumProxy) Finalize() (float64, error) { return a.s.Float64(), nil }

// BenchmarkReproSum_EngineProxy is the end-to-end per-row proxy for the
// archive-scale stop rule: the streaming engine's per-record aggregation
// step (interface dispatch, NumericValue's field lookup and null test,
// the accumulate) over 2^20 heap records, naive AGG_SUM against the
// binned twin. The ratio of the two ns/row figures is what switching
// AGG_SUM to the binned kernel costs per row before decode.
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
	b.Run("naive", func(b *testing.B) { run(b, &sumAggregator{}) })
	b.Run("binned", func(b *testing.B) { run(b, &binnedSumProxy{}) })
}
