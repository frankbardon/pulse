package limits

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestAggregationBytes_ExtensionKeepsCeilings (U51 E5-S7): a type the
// built-in table does not hold — an embedder extension — is charged the
// pre-table ceilings (256 B of state, 512 B weighted, and the
// components-on 576 B column even with components off: its value is
// opaque), and contributes no vector fragments.
func TestAggregationBytes_ExtensionKeepsCeilings(t *testing.T) {
	const ext = types.AggregationType("AGG_ACME_TRIMMED")
	if measured, _ := AggregationMeasured(ext); measured {
		t.Fatalf("%s has a table entry", ext)
	}
	if got := AggregationStateBytes(ext, false, 0); got != aggStateBytes {
		t.Errorf("unweighted state %d, want the %d ceiling", got, aggStateBytes)
	}
	if got := AggregationStateBytes(ext, true, 0); got != weightedAggStateBytes {
		t.Errorf("weighted state %d, want the %d ceiling", got, weightedAggStateBytes)
	}
	if got := AggregationOutputOffBytes(ext, 0); got != aggOutputBytes {
		t.Errorf("components-off output %d, want the components-on %d", got, aggOutputBytes)
	}
	if got := VectorAggregationBytes(ext, 8, 100, 100); got != 0 {
		t.Errorf("vector bytes %d for a scalar type", got)
	}
}

// TestAggregationBytes_TableShape: every built-in has an entry; a
// buffered-only built-in keeps the ceilings; a streaming built-in's
// entry is at most the ceiling it replaces unless measured above it
// (the weighted accumulators, the value-state and vector aggregators),
// and a weight-unaware one charges the same weighted. The entries
// themselves are pinned against measured heap by internal/service
// TestGroupMemory_AggregatorStateTable.
func TestAggregationBytes_TableShape(t *testing.T) {
	for _, at := range types.AllAggregationTypes() {
		measured, buffered := AggregationMeasured(at)
		if !measured {
			t.Errorf("%s: no table entry", at)
			continue
		}
		if buffered != !at.Streamable() {
			t.Errorf("%s: buffered %v, Streamable %v", at, buffered, at.Streamable())
		}
		if buffered && (AggregationStateBytes(at, false, 0) != aggStateBytes || AggregationStateBytes(at, true, 0) != weightedAggStateBytes) {
			t.Errorf("%s: a buffered-only entry must keep the ceilings", at)
		}
		if AggregationStateBytes(at, true, 0) < AggregationStateBytes(at, false, 0) {
			t.Errorf("%s: weighted state below unweighted", at)
		}
	}
	if d := AggregationStateBytes(types.AGG_VEC_SD, false, 10) - AggregationStateBytes(types.AGG_VEC_SD, false, 2); d != 8*vecStateMemberBytes {
		t.Errorf("vector state slope %d over 8 members, want %d", d, 8*vecStateMemberBytes)
	}
	if got, want := VectorAggregationBytes(types.AGG_VEC_SD, 4, 3, 5), int64(3*(vecFragmentBytes+4*vecMomentFragmentMemberBytes)+5*(vecBufferedRowBytes+4*vecBufferedRowMemberBytes)); got != want {
		t.Errorf("AGG_VEC_SD fragments %d, want %d", got, want)
	}
	if got, want := VectorAggregationBytes(types.AGG_VEC_MEAN, 4, 3, 0), int64(3*(vecFragmentBytes+4*vecFragmentMemberBytes)); got != want {
		t.Errorf("AGG_VEC_MEAN fragments %d, want %d", got, want)
	}
}
