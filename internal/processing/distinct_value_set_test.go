package processing

import (
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// distinctMapCount is the identity AGG_DISTINCT_COUNT had before the
// inline set (#723): the size of a map[float64]struct{} holding vals.
// -0 and +0 share a key; every NaN is a key of its own.
func distinctMapCount(vals []float64) int {
	m := map[float64]struct{}{}
	for _, v := range vals {
		m[v] = struct{}{}
	}
	return len(m)
}

// distinctCases are value lists over the identity edges: ±0, NaN (the
// default quiet NaN, a payload NaN and a sign-set NaN, repeated), ±Inf,
// and lists below, at and past the inline threshold, with repeats
// before and after promotion.
func distinctCases() map[string][]float64 {
	negNaN := math.Float64frombits(0xfff8000000000000)
	payloadNaN := math.Float64frombits(0x7ff8000000000001)
	negZero := math.Copysign(0, -1)
	ramp := func(n int, rep int) []float64 {
		var out []float64
		for range rep {
			for i := range n {
				out = append(out, float64(i)*0.1)
			}
		}
		return out
	}
	withEdges := func(vals []float64) []float64 {
		return append(append([]float64{}, vals...), negZero, 0, math.NaN(), negNaN, math.Inf(1), math.Inf(-1), payloadNaN, negZero, math.NaN())
	}
	return map[string][]float64{
		"empty":            nil,
		"plus_minus_zero":  {0, negZero, 0, negZero},
		"minus_zero_first": {negZero, 0},
		"nan_each_counts":  {math.NaN(), math.NaN(), negNaN, payloadNaN, math.NaN()},
		"inf":              {math.Inf(1), math.Inf(-1), math.Inf(1)},
		"mixed_small":      withEdges([]float64{1, 2, 2, 3}),
		"at_threshold":     ramp(distinctInlineMax, 3),
		"past_threshold":   ramp(distinctInlineMax+1, 3),
		"edges_promoted":   withEdges(ramp(3*distinctInlineMax, 2)),
		"edges_at_edge":    withEdges(ramp(distinctInlineMax-2, 2)),
	}
}

// TestDistinctValueSet_MatchesMapIdentity: the inline set counts every
// list exactly as the map it replaced — ±0 one value, each NaN its own —
// on the buffered path (Aggregate), the streaming path (UpdateRow +
// Finalize) and Components' cardinality.
func TestDistinctValueSet_MatchesMapIdentity(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	for name, vals := range distinctCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := distinctMapCount(vals)

			var s distinctValueSet
			for _, v := range vals {
				s.add(v)
			}
			if got := s.len(); got != want {
				t.Errorf("set: %d distinct, map identity %d", got, want)
			}

			buf := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema)
			got, err := buf.Aggregate(makeRecords(schema, "score", vals), "score")
			if err != nil {
				t.Fatal(err)
			}
			if got != float64(want) {
				t.Errorf("buffered: %v, want %d", got, want)
			}

			on := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema).(OnlineAggregator)
			for _, r := range makeRecords(schema, "score", vals) {
				if err := on.UpdateRow(r, "score"); err != nil {
					t.Fatal(err)
				}
			}
			got, err = on.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			if got != float64(want) {
				t.Errorf("streaming: %v, want %d", got, want)
			}
			c, err := on.(MetaAggregator).Components()
			if err != nil {
				t.Fatal(err)
			}
			if c["cardinality"] != want {
				t.Errorf("components cardinality %v, want %d", c["cardinality"], want)
			}
		})
	}
}

// TestDistinctValueSet_MergeOrderInvariant: splitting a list into
// partitions (at many cuts, so a small side meets a promoted one, an empty
// one, or another small one whose union promotes) and merging them in
// either order through MergeOnline gives the map identity's count —
// the shard and decode reducers' merge, order-free.
func TestDistinctValueSet_MergeOrderInvariant(t *testing.T) {
	t.Parallel()

	schema := numericSchema()
	fold := func(t *testing.T, vals []float64) OnlineAggregator {
		a := makeAggregator(t, types.AGG_DISTINCT_COUNT, "score", schema).(OnlineAggregator)
		for _, r := range makeRecords(schema, "score", vals) {
			if err := a.UpdateRow(r, "score"); err != nil {
				t.Fatal(err)
			}
		}
		return a
	}
	for name, vals := range distinctCases() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want := float64(distinctMapCount(vals))
			// Every cut on a short list; a stride on a long one (the
			// cuts still land on each side of the threshold).
			step := max(1, len(vals)/24)
			for i := 0; i <= len(vals); i += step {
				for j := i; j <= len(vals); j += step {
					parts := [][]float64{vals[:i], vals[i:j], vals[j:]}
					for _, order := range [][3]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}} {
						acc := fold(t, parts[order[0]])
						for _, k := range order[1:] {
							if err := acc.(MergeableAggregator).MergeOnline(fold(t, parts[k])); err != nil {
								t.Fatal(err)
							}
						}
						got, err := acc.Finalize()
						if err != nil {
							t.Fatal(err)
						}
						if got != want {
							t.Fatalf("cuts %d/%d order %v: %v distinct, want %v", i, j, order, got, want)
						}
					}
				}
			}
		})
	}
}

// TestDistinctValueSet_PromotesPastThreshold: the inline slice holds at
// most distinctInlineMax values and the set moves to its map on the
// next new one (a repeat does not promote); a merge whose union passes
// the threshold promotes too.
func TestDistinctValueSet_PromotesPastThreshold(t *testing.T) {
	t.Parallel()

	var s distinctValueSet
	for i := range distinctInlineMax {
		s.add(float64(i))
	}
	s.add(0)
	s.add(math.NaN())
	if s.big != nil || len(s.small) != distinctInlineMax {
		t.Fatalf("at the threshold: small %d, big %v", len(s.small), s.big != nil)
	}
	s.add(float64(distinctInlineMax))
	if s.big == nil || s.small != nil || s.len() != distinctInlineMax+2 {
		t.Fatalf("past the threshold: small %d, big %v, len %d", len(s.small), s.big != nil, s.len())
	}

	var a, b distinctValueSet
	for i := range distinctInlineMax {
		a.add(float64(i))
		b.add(float64(i + distinctInlineMax/2))
	}
	a.merge(&b)
	if want := distinctInlineMax * 3 / 2; a.big == nil || a.len() != want {
		t.Fatalf("merged union: big %v, len %d, want %d", a.big != nil, a.len(), want)
	}
	if b.len() != distinctInlineMax || b.big != nil {
		t.Fatalf("merge modified its source: len %d", b.len())
	}
}

// BenchmarkDistinctValueSet_Add folds n distinct values per set (the
// per-bucket insert cost below and past the threshold).
func BenchmarkDistinctValueSet_Add(b *testing.B) {
	for _, n := range []int{1, 4, distinctInlineMax, 4 * distinctInlineMax} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				var s distinctValueSet
				for i := range n {
					s.add(float64(i))
				}
				if s.len() != n {
					b.Fatal(s.len())
				}
			}
		})
	}
}
