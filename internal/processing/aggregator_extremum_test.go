package processing

import (
	"math"
	"testing"
	"unsafe"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestExtremumAggregator_EightBytes (memory-bounds E2-S2) pins the
// AGG_MIN / AGG_MAX state at one 8-byte word: a grouped run holds one
// per slot per bucket, and the former {value, seen, frozen} layout cost
// 24 B a slot (1.8 GB of a 50M-row retention chain's 9.6 GiB live heap).
func TestExtremumAggregator_EightBytes(t *testing.T) {
	t.Parallel()
	if got := unsafe.Sizeof(maxAggregator{}); got != 8 {
		t.Errorf("maxAggregator is %d bytes, want 8", got)
	}
	if got := unsafe.Sizeof(minAggregator{}); got != 8 {
		t.Errorf("minAggregator is %d bytes, want 8", got)
	}
}

// refExtremum is the pre-E2-S2 AGG_MAX (max=true) / AGG_MIN state, kept
// verbatim as the bit-identity oracle.
type refExtremum struct {
	max  bool
	v    float64
	seen bool
}

func (r *refExtremum) update(v float64) {
	if !r.seen || (r.max && v > r.v) || (!r.max && v < r.v) {
		r.v, r.seen = v, true
	}
}

func (r *refExtremum) merge(o *refExtremum) {
	if !o.seen {
		return
	}
	if !r.seen || (r.max && o.v > r.v) || (!r.max && o.v < r.v) {
		r.v, r.seen = o.v, true
	}
}

func (r *refExtremum) result() float64 {
	if !r.seen {
		return math.NaN()
	}
	return r.v
}

// extremumSameBits is bit equality, except that the sentinel-payload input is
// allowed to come back as any NaN (the one documented remap).
func extremumSameBits(got, want float64) bool {
	if math.Float64bits(want) == extremumKey {
		return math.IsNaN(got)
	}
	return math.Float64bits(got) == math.Float64bits(want)
}

// TestExtremumAggregator_MatchesReference (memory-bounds E2-S2): the
// 8-byte AGG_MIN / AGG_MAX return the former implementation's bits on
// every arm — streamed (UpdateRow → Finalize), streamed and merged at
// every split point (MergeOnline, both orders), and buffered
// (Aggregate) — with Components reporting the Finalize value afterwards,
// over inputs that exercise the seen flag: none, ±0, ±Inf, NaN first
// and later, and an input carrying the sentinel NaN's own bits.
func TestExtremumAggregator_MatchesReference(t *testing.T) {
	t.Parallel()
	sentinel := math.Float64frombits(extremumKey)
	negZero := math.Copysign(0, -1)
	nanPayload := math.Float64frombits(0x7FF8_0000_0000_0BAD)
	seqs := [][]float64{
		nil,
		{3},
		{math.Inf(-1)},
		{math.Inf(1)},
		{0, negZero},
		{negZero, 0},
		{nanPayload, 3, -2},
		{3, nanPayload, -2},
		{-1, 5, 2, 5, -7},
		{sentinel},
		{sentinel, 4},
		{4, sentinel, -4},
		{math.Inf(-1), math.Inf(-1)},
	}
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeF64}}}
	rec := func(v float64) *Record { return NewRecord(schema, map[string]float64{"v": v}) }
	for _, isMax := range []bool{true, false} {
		typ, key := types.AGG_MIN, "min"
		mk := func() Aggregator { a, _ := newMinAggregator(nil, nil); return a }
		if isMax {
			typ, key = types.AGG_MAX, "max"
			mk = func() Aggregator { a, _ := newMaxAggregator(nil, nil); return a }
		}
		check := func(arm string, seq []float64, a Aggregator, got float64, want float64) {
			t.Helper()
			if !extremumSameBits(got, want) {
				t.Errorf("%s %s %v: got %v (%#x), want %v (%#x)", typ, arm, seq, got, math.Float64bits(got), want, math.Float64bits(want))
			}
			comps, err := a.(MetaAggregator).Components()
			if err != nil {
				t.Fatal(err)
			}
			if c := comps[key].(float64); math.Float64bits(c) != math.Float64bits(got) {
				t.Errorf("%s %s %v: Components %s = %v, Finalize %v", typ, arm, seq, key, c, got)
			}
		}
		for _, seq := range seqs {
			ref := &refExtremum{max: isMax}
			for _, v := range seq {
				ref.update(v)
			}
			want := ref.result()

			s := mk()
			for _, v := range seq {
				if err := s.(OnlineAggregator).UpdateRow(rec(v), "v"); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := s.(OnlineAggregator).Finalize()
			check("stream", seq, s, got, want)
			again, _ := s.(OnlineAggregator).Finalize()
			if math.Float64bits(again) != math.Float64bits(got) {
				t.Errorf("%s %v: a second Finalize returned %v, the first %v", typ, seq, again, got)
			}

			for cut := 0; cut <= len(seq); cut++ {
				for _, swap := range []bool{false, true} {
					l, r := seq[:cut], seq[cut:]
					if swap {
						l, r = r, l
					}
					rl, rr := &refExtremum{max: isMax}, &refExtremum{max: isMax}
					a, b := mk(), mk()
					for _, v := range l {
						rl.update(v)
						_ = a.(OnlineAggregator).UpdateRow(rec(v), "v")
					}
					for _, v := range r {
						rr.update(v)
						_ = b.(OnlineAggregator).UpdateRow(rec(v), "v")
					}
					rl.merge(rr)
					if err := a.(MergeableAggregator).MergeOnline(b.(OnlineAggregator)); err != nil {
						t.Fatal(err)
					}
					got, _ := a.(OnlineAggregator).Finalize()
					check("merge", seq, a, got, rl.result())
				}
			}

			buf := mk()
			got, err := buf.(interface {
				aggregateValues([]float64) (float64, error)
			}).aggregateValues(append([]float64(nil), seq...))
			if err != nil {
				t.Fatal(err)
			}
			// The buffered arm folds left to right exactly as the ref.
			check("buffered", seq, buf, got, want)
		}
	}
}

// TestBucketMinter_SlabsExtrema (memory-bounds E2-S2): a bucket's
// AGG_MAX / AGG_MIN slots come from one slab per type — a bucket of
// twelve AGG_MAX, one AGG_MIN and one AGG_SUM costs five allocations
// (bucket, slot slice, two slabs, the sum) where one object a slot cost
// sixteen — and every slot is its own fresh state.
func TestBucketMinter_SlabsExtrema(t *testing.T) {
	// serial: testing.AllocsPerRun panics in a parallel test
	var factories []AggregatorFactory
	for range 12 {
		factories = append(factories, newMaxAggregator)
	}
	factories = append(factories, newMinAggregator, newSumAggregator)
	minted := 0
	spec := &types.Aggregation{}
	m := NewBucketMinter(len(factories), func(i int) (OnlineAggregator, error) {
		minted++
		a, err := factories[i](spec, nil)
		if err != nil {
			return nil, err
		}
		return a.(OnlineAggregator), nil
	}, nil)
	if _, err := m.Mint(); err != nil { // the probe
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := m.Mint(); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 5 {
		t.Errorf("Mint costs %.0f allocations a bucket, want at most 5", allocs)
	}
	minted = 0
	b, err := m.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if minted != 1 {
		t.Errorf("a bucket minted %d slots through mint, want 1 (the AGG_SUM)", minted)
	}
	schema := &encoding.Schema{Fields: []encoding.Field{{Name: "v", Type: encoding.FieldTypeF64}}}
	for i, oa := range b.Aggs {
		if err := oa.UpdateRow(NewRecord(schema, map[string]float64{"v": float64(i)}), "v"); err != nil {
			t.Fatal(err)
		}
	}
	for i, oa := range b.Aggs {
		got, _ := oa.Finalize()
		if got != float64(i) {
			t.Errorf("slot %d (%T) finalized %v, want %v: slab slots share state", i, oa, got, float64(i))
		}
	}
}
