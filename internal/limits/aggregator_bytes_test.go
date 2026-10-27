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
	t.Parallel()

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
	t.Parallel()

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

// TestCrosstabSlotBytes_TableShape (U51 E5-S9): every built-in carries
// a crosstab cell entry, components on (weighted at least unweighted)
// above off and within the extension ceiling; a slot is its state plus
// that entry plus the set-member slope; an extension is charged the
// state ceilings and crosstabCellOutputCeilingBytes either way; an
// auxiliary costs a components-on slot plus crosstabAuxSlotBytes, and
// nothing with components off. The entries are pinned against measured
// heap by internal/service TestCrosstabMemory_CellChargeTable.
func TestCrosstabSlotBytes_TableShape(t *testing.T) {
	t.Parallel()

	for _, at := range types.AllAggregationTypes() {
		b := aggregatorBytesTable[at].cell
		if b.off <= 0 || b.on < b.off || b.onWeighted < b.on || b.onWeighted > crosstabCellOutputCeilingBytes {
			t.Errorf("%s: cell entry %+v", at, b)
		}
		for _, w := range []bool{false, true} {
			out := map[bool]int64{false: b.on, true: b.onWeighted}[w]
			if got, want := CrosstabSlotBytes(at, w, false, 1, 0), AggregationStateBytes(at, w, 1)+out; got != want {
				t.Errorf("%s weighted=%v: slot %d, want %d", at, w, got, want)
			}
			if got, want := CrosstabSlotBytes(at, w, true, 1, 0), AggregationStateBytes(at, w, 1)+b.off; got != want {
				t.Errorf("%s weighted=%v components off: slot %d, want %d", at, w, got, want)
			}
			if got, want := CrosstabAuxSlotBytes(at, w, false, 1, 0), CrosstabSlotBytes(at, w, false, 1, 0)+crosstabAuxSlotBytes; got != want {
				t.Errorf("%s weighted=%v: auxiliary %d, want %d", at, w, got, want)
			}
			if got := CrosstabAuxSlotBytes(at, w, true, 1, 0); got != 0 {
				t.Errorf("%s weighted=%v: auxiliary %d with components off", at, w, got)
			}
		}
	}
	if d := CrosstabSlotBytes(types.AGG_SET_FREQUENCY, false, false, 0, 10) - CrosstabSlotBytes(types.AGG_SET_FREQUENCY, false, false, 0, 0); d != 10*(setMemberStateBytes+setMemberOutputBytes) {
		t.Errorf("set-member slope %d over 10 members", d)
	}
	const ext = types.AggregationType("AGG_ACME_TRIMMED")
	for _, off := range []bool{false, true} {
		if got, want := CrosstabSlotBytes(ext, false, off, 0, 0), int64(aggStateBytes+crosstabCellOutputCeilingBytes); got != want {
			t.Errorf("extension off=%v: slot %d, want the ceilings' %d", off, got, want)
		}
		if got, want := CrosstabSlotBytes(ext, true, off, 0, 0), int64(weightedAggStateBytes+crosstabCellOutputCeilingBytes); got != want {
			t.Errorf("extension weighted off=%v: slot %d, want the ceilings' %d", off, got, want)
		}
	}
	if CrosstabAxisKeyBytes(false) != crosstabAxisKeyBytes || CrosstabAxisKeyBytes(true) != crosstabAxisKeyOffBytes || crosstabAxisKeyOffBytes > crosstabAxisKeyBytes {
		t.Error("axis-key charges")
	}
}
