package pulse

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Two-arm parity for the distinct-key slab partition gate, over the
// SAME memmap cohort E1-S2 uses.
//
// descriptor.ValidateOverlays is reached from exactly one call site
// (descriptor/predict.go), so a predict refusal never stops
// pulse.Process — the runtime twin is not belt-and-braces, it is the
// only thing that stops the wrong number being computed. These tests
// send one offending request down BOTH public entry points and require
// the same error code out of each.

// pwPartitionRequest is pwDistinctRequest with the row axis INVERTED:
// the flat `segment` outer, the fan-out `brand` inner. With
// n_within_depth=0 the slab fixes `segment` and sums across `brand`,
// so respondent 101 — who selected both brands — lands in two summed
// cells and the distinct sum over-counts them.
func pwPartitionRequest(nSource string, depth int) *Request {
	req := pwDistinctRequest("")
	req.Crosstab.Rows = []*types.Group{
		{Type: types.GROUP_CATEGORY, Field: "segment"},
		{Type: types.GROUP_SET_PER_ELEMENT, Field: "brand"},
	}
	if nSource != "" {
		params, _ := json.Marshal(map[string]any{"n_source": nSource, "n_within_depth": depth})
		req.Overlays = []types.OverlaySpec{{
			Name:   "pw",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: params,
		}}
	}
	return req
}

// pwPartitionPredictEnvelope drives the same descriptor entry point the
// `pulse predict` leaf uses (internal/cli/api.go), so the assertion is
// on the envelope a CLI caller actually sees rather than on the
// result-only wrapper Pulse.Predict returns.
func pwPartitionPredictEnvelope(t *testing.T, memFs afero.Fs, req *Request) *descriptor.Envelope {
	t.Helper()
	data, err := afero.ReadFile(memFs, pwDistinctCohort)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return descriptor.PredictFromBytes(data, req, nil)
}

func pwPartitionEnvelopeCode(env *descriptor.Envelope, code string) *descriptor.EnvelopeEntry {
	for _, e := range env.Errors {
		if e.Code == code {
			return e
		}
	}
	return nil
}

// TestPairwisePartitionGate_PredictAndProcessAgree is the acceptance
// criterion in one test: the same offending request refuses at predict
// and at process, with the same code.
func TestPairwisePartitionGate_PredictAndProcessAgree(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := pwPartitionRequest(types.PairwiseNSourceNWithinDistinct, 0)
	wantCode := string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)

	// --- predict arm -------------------------------------------------
	env := pwPartitionPredictEnvelope(t, memFs, req)
	entry := pwPartitionEnvelopeCode(env, wantCode)
	if entry == nil {
		codes := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			codes = append(codes, e.Code)
		}
		t.Fatalf("predict did not refuse with %s; codes %v", wantCode, codes)
	}
	if entry.Details["dim_index"] != 1 || entry.Details["axis"] != "row" {
		t.Errorf("predict details = %v, want dim_index 1 on the row axis", entry.Details)
	}

	// --- runtime arm -------------------------------------------------
	_, perr := p.Process(context.Background(), req)
	if perr == nil {
		t.Fatal("Process accepted a request predict refuses — a predict-only gate does not stop the wrong number")
	}
	coded, ok := perr.(*errors.CodedError)
	if !ok {
		t.Fatalf("Process error %v is not a *errors.CodedError", perr)
	}
	if !errors.HasCode(perr, errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED) {
		t.Fatalf("Process error chain does not carry %s: %v", wantCode, perr)
	}
	if string(coded.Code) != wantCode {
		t.Fatalf("Process code = %q, want %q", coded.Code, wantCode)
	}
	if string(coded.Code) != entry.Code {
		t.Fatalf("arms disagree: predict %q vs process %q", entry.Code, coded.Code)
	}
	if coded.Message != entry.Message {
		t.Errorf("arms disagree on prose:\n predict: %s\n process: %s", entry.Message, coded.Message)
	}
}

// TestPairwisePartitionGate_FanOutInPrefixRuns is the false-refusal
// guard on the real cohort: the FILED shape (fan-out OUTER, inside the
// fixed prefix) must still run and still produce a layer. Refusing it
// would refuse the request that motivated the whole mode.
func TestPairwisePartitionGate_FanOutInPrefixRuns(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// pwDistinctRequest puts GROUP_SET_PER_ELEMENT at dim 0, which
	// n_within_depth=0 fixes.
	req := pwDistinctRequest(types.PairwiseNSourceNWithinDistinct)

	env := pwPartitionPredictEnvelope(t, memFs, req)
	if e := pwPartitionEnvelopeCode(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); e != nil {
		t.Fatalf("predict refused the in-prefix fan-out: %s", e.Message)
	}

	resp, perr := p.Process(context.Background(), req)
	if perr != nil {
		t.Fatalf("Process refused the in-prefix fan-out: %v", perr)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
}

// TestPairwisePartitionGate_NWithinAndMarginsUnaffected pins the
// non-gated families on the OFFENDING axis shape: plain n_within (its
// record counts are additive) and EVERY margin mode, record-count and
// distinct-key alike. The distinct margin modes are the load-bearing
// entries — they read the same distinct-key figure n_within_distinct
// does, off the same admitted cell aggregator, and are refused two
// tests up when the slab sums it. A margin does not sum it: it
// accumulates over the raw records that reached the margin key, once
// each, so it is exact under a fan-out grouper and gating it would
// refuse a correct request.
func TestPairwisePartitionGate_NWithinAndMarginsUnaffected(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, nSource := range []string{
		types.PairwiseNSourceNWithin,
		types.PairwiseNSourceRowMarginN,
		types.PairwiseNSourceColumnMarginN,
		types.PairwiseNSourceRowMarginDistinct,
		types.PairwiseNSourceColumnMarginDistinct,
		types.PairwiseNSourceCellNUnweighted,
	} {
		nSource := nSource
		t.Run(nSource, func(t *testing.T) {
			req := pwPartitionRequest(nSource, 0)
			env := pwPartitionPredictEnvelope(t, memFs, req)
			if e := pwPartitionEnvelopeCode(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); e != nil {
				t.Fatalf("predict gated %s: %s", nSource, e.Message)
			}
			if _, perr := p.Process(context.Background(), req); perr != nil {
				t.Fatalf("Process gated %s: %v", nSource, perr)
			}
		})
	}
}

// TestPairwisePartitionGate_RaisingDepthClearsIt proves the primary
// fixup actually works end to end: moving the fan-out dim inside the
// fixed prefix turns the refusal into a run.
func TestPairwisePartitionGate_RaisingDepthClearsIt(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// depth=0 refuses (brand at dim 1 is summed across)...
	if _, perr := p.Process(context.Background(), pwPartitionRequest(types.PairwiseNSourceNWithinDistinct, 0)); perr == nil {
		t.Fatal("depth=0 must refuse")
	}
	// ...depth=1 fixes both dims, so nothing is summed across at all.
	req := pwPartitionRequest(types.PairwiseNSourceNWithinDistinct, 1)
	if _, perr := p.Process(context.Background(), req); perr != nil {
		t.Fatalf("depth=1 must clear the refusal, got %v", perr)
	}
}

// TestPairwisePartitionGate_FixupsReachable checks the operator-facing
// half of the contract: the new code resolves through the errors
// catalogue with a Message and at least one actionable Fixup, so
// `pulse errors lookup` is useful on it.
func TestPairwisePartitionGate_FixupsReachable(t *testing.T) {
	meta, ok := errors.MetadataFor(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)
	if !ok {
		t.Fatal("no codeMetadata entry for PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED")
	}
	if strings.TrimSpace(meta.Message) == "" {
		t.Error("metadata Message is empty")
	}
	if len(meta.Fixups) == 0 && !meta.FixupNotApplicable {
		t.Error("metadata carries no Fixup and is not marked FixupNotApplicable")
	}
	if !strings.Contains(meta.Message, "n_within_distinct") {
		t.Errorf("metadata Message does not name the mode: %q", meta.Message)
	}
}
