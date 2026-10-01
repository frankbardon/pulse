package synth

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// buildBlockOrderedRegionPlanCohort emits a minimal single-file .pulse
// cohort with two categorical_u8 fields, "region" and "plan", whose
// rows are deliberately BLOCK-ORDERED by region: rows [0, half) are all
// "us", rows [half, rowCount) are all "eu" — a source shape a real
// cohort sorted by region (or any other clustering key) would produce.
// "plan" alternates by row parity so the contingency table is not
// degenerate, but this test only exercises "region"'s marginal, which
// this construction fixes exactly: true P(region=us) = half / rowCount.
func buildBlockOrderedRegionPlanCohort(t *testing.T, rowCount, half int) []byte {
	t.Helper()
	if half < 0 || half > rowCount {
		t.Fatalf("half=%d out of [0, rowCount=%d] range", half, rowCount)
	}
	regionDict := encoding.NewDictionary()
	for _, v := range []string{"us", "eu"} {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add(%q): %v", v, err)
		}
	}
	planDict := encoding.NewDictionary()
	for _, v := range []string{"paid", "free"} {
		if _, err := planDict.Add(v); err != nil {
			t.Fatalf("planDict.Add(%q): %v", v, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: regionDict},
			{Name: "plan", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: planDict},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < rowCount; r++ {
		regionID := byte(0) // "us"
		if r >= half {
			regionID = byte(1) // "eu"
		}
		planID := byte(r % 2)
		buf.WriteByte(regionID)
		buf.WriteByte(planID)
	}
	return buf.Bytes()
}

// TestOldFirstNTruncation_WouldHaveMisrepresentedBlockOrderedMarginal
// demonstrates this story's bug has teeth WITHOUT reverting any
// production code: buildBlockOrderedRegionPlanCohort's own
// construction guarantees the first conditionalJointCap (10000) rows
// of a 30000-row, half=15000 fixture fall ENTIRELY inside the "us"
// block (rows [0, 15000) are all "us"; the cap is reached at row
// 10000, still inside that block). The prior capture logic at
// profileRecords ("if trackCatJoint && len(jointCatRows) <
// conditionalJointCap { append(...) }") kept exactly rows [0, 10000) —
// no random index selection, no eviction — so it would have captured
// region="us" on every one of its 10000 rows: an implied marginal of
// 100% us / 0% eu against a true marginal of 50% us / 50% eu, a 50
// percentage point divergence, wildly outside any reasonable
// tolerance. This is what TestProfile_ConditionalCategoricalReservoirSampling_BlockOrderedSourceMatchesTrueMarginal
// below asserts does NOT happen under the new Algorithm R reservoir
// sampling.
func TestOldFirstNTruncation_WouldHaveMisrepresentedBlockOrderedMarginal(t *testing.T) {
	const rowCount = 30000
	const half = 15000 // true marginal: 50% us / 50% eu
	const capN = conditionalJointCap

	if capN >= half {
		t.Fatalf("fixture invariant broken: conditionalJointCap=%d must be < half=%d "+
			"for the first %d rows to land entirely inside the \"us\" block", capN, half, capN)
	}

	// Simulate exactly what the removed first-N logic would have kept:
	// the first `capN` rows of the block-ordered source, counted by
	// region — no RNG, no eviction, matching the old
	// `len(jointCatRows) < conditionalJointCap` gate byte-for-byte in
	// spirit (it appended every row until the cap, then stopped).
	usCount := 0
	for r := 0; r < capN; r++ {
		if r < half {
			usCount++ // region == "us"
		}
	}
	oldImpliedUsMarginal := float64(usCount) / float64(capN)
	trueUsMarginal := float64(half) / float64(rowCount)

	const divergence = 0.4 // old behavior's actual divergence is 0.5; 0.4 leaves slack
	if diff := oldImpliedUsMarginal - trueUsMarginal; diff < divergence {
		t.Fatalf("old first-N truncation's implied us-marginal (%.4f) does not diverge from the "+
			"true us-marginal (%.4f) by more than %.4f — fixture no longer demonstrates the bug",
			oldImpliedUsMarginal, trueUsMarginal, divergence)
	}
	if oldImpliedUsMarginal != 1.0 {
		t.Fatalf("expected old first-N truncation to capture region=us on every one of the first "+
			"%d rows (100%%), got %.4f — fixture's block boundary may not exceed conditionalJointCap",
			capN, oldImpliedUsMarginal)
	}
}

// TestProfile_ConditionalCategoricalReservoirSampling_BlockOrderedSourceMatchesTrueMarginal
// is E7-S2's non-negotiable acceptance bar: a block-ordered source
// cohort (region sorted, exceeding conditionalJointCap) must, under
// the new Algorithm R reservoir sampling, produce a captured
// categorical-categorical contingency table whose implied "region"
// marginal matches the TRUE marginal (computed from the full,
// unsampled 30000-row fixture) within a generous statistical
// tolerance — unlike the old first-N truncation demonstrated above to
// diverge by 50 percentage points.
func TestProfile_ConditionalCategoricalReservoirSampling_BlockOrderedSourceMatchesTrueMarginal(t *testing.T) {
	const rowCount = 30000
	const half = 15000 // true marginal: 50% us / 50% eu
	data := buildBlockOrderedRegionPlanCohort(t, rowCount, half)

	prof, err := ProfileBytes(data, ProfileOptions{IncludeConditional: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if prof.Conditional == nil || len(prof.Conditional.CategoricalPairs) != 1 {
		t.Fatalf("expected exactly one categorical pair, got Conditional=%+v", prof.Conditional)
	}
	pair := prof.Conditional.CategoricalPairs[0]

	if pair.N != conditionalJointCap {
		t.Fatalf("pair.N = %d, want conditionalJointCap = %d (rowCount=%d exceeds the cap, so the "+
			"reservoir should have saturated)", pair.N, conditionalJointCap, rowCount)
	}

	usCount, total := 0, 0
	for _, c := range pair.Cells {
		total += c.Count
		if c.AValue == "us" {
			usCount += c.Count
		} else if c.AValue != "eu" {
			t.Errorf("unexpected AValue %q in a low-cardinality 2-value fixture", c.AValue)
		}
	}
	if total != pair.N {
		t.Fatalf("cell counts sum to %d, want pair.N = %d", total, pair.N)
	}

	impliedUsMarginal := float64(usCount) / float64(total)
	trueUsMarginal := float64(half) / float64(rowCount) // 0.5

	const tolerance = 0.05 // ~10 std-devs of slack at n=10000, p=0.5 (sd ~= 0.005)
	if diff := impliedUsMarginal - trueUsMarginal; diff < -tolerance || diff > tolerance {
		t.Fatalf("reservoir-sampled implied us-marginal = %.4f, true us-marginal = %.4f — "+
			"diverges by %.4f, outside tolerance %.4f (reservoir sampling should be unbiased "+
			"regardless of the source's block order)", impliedUsMarginal, trueUsMarginal, diff, tolerance)
	}
}

// TestProfile_ConditionalCategoricalReservoirSampling_Deterministic
// confirms the effort's existing determinism contract extends to the
// new reservoir sampling: the same (source, seed) must still produce
// byte-identical captured output, even though Algorithm R now draws
// from a seeded RNG. Runs the same block-ordered fixture through
// ProfileBytes twice with the same Seed and asserts the two resulting
// Profile values are deeply equal.
func TestProfile_ConditionalCategoricalReservoirSampling_Deterministic(t *testing.T) {
	const rowCount = 30000
	const half = 15000
	data := buildBlockOrderedRegionPlanCohort(t, rowCount, half)

	opts := ProfileOptions{IncludeConditional: true, Seed: 123}
	prof1, err := ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("ProfileBytes (run 1): %v", err)
	}
	prof2, err := ProfileBytes(data, opts)
	if err != nil {
		t.Fatalf("ProfileBytes (run 2): %v", err)
	}
	if !reflect.DeepEqual(prof1, prof2) {
		t.Fatalf("same (source, seed) produced divergent captured output:\nrun1=%+v\nrun2=%+v", prof1, prof2)
	}

	// Sanity: the zero-value (unset) Seed is itself a valid, deterministic
	// seed — two profiles captured with no Seed set must also agree.
	prof3, err := ProfileBytes(data, ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes (unset seed, run 1): %v", err)
	}
	prof4, err := ProfileBytes(data, ProfileOptions{IncludeConditional: true})
	if err != nil {
		t.Fatalf("ProfileBytes (unset seed, run 2): %v", err)
	}
	if !reflect.DeepEqual(prof3, prof4) {
		t.Fatalf("unset Seed (zero value) produced divergent captured output across runs:\nrun1=%+v\nrun2=%+v", prof3, prof4)
	}
}
