package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestPairwiseKernels_RecordDirection: every pairwise kernel records the
// sign of its test statistic — +1 when leg i (pair_a) is the higher, -1
// when leg j is — and swapping the legs flips it without moving p.
func TestPairwiseKernels_RecordDirection(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		k  pwKernel
		in pwInputs // leg i higher
	}{
		"prop_z":   {pairwisePropZKernel, pwInputs{p1: 0.6, p2: 0.3, nf1: 100, nf2: 100}},
		"probit_t": {pairwiseProbitTKernel, pwInputs{p1: 0.6, p2: 0.3, nf1: 100, nf2: 100}},
		"welch_t":  {pairwiseWelchTKernel, pwInputs{m1: 5, m2: 3, v1: 4, v2: 2, nf1: 30, nf2: 40}},
		"two_means_z": {pairwiseTwoMeansZKernel,
			pwInputs{m1: 5, m2: 3, v1: 4, v2: 2, nf1: 30, nf2: 40}},
		"weighted_two_means_z": {pairwiseWeightedTwoMeansZKernel(types.PairwiseNBasisKish),
			pwInputs{w1: weightedMoments{mean: 5, m2: 90, sumW: 30, sumWSq: 40}, w2: weightedMoments{mean: 3, m2: 60, sumW: 40, sumWSq: 50}}},
	}
	swap := func(in pwInputs) pwInputs {
		in.p1, in.p2 = in.p2, in.p1
		in.m1, in.m2 = in.m2, in.m1
		in.v1, in.v2 = in.v2, in.v1
		in.nf1, in.nf2 = in.nf2, in.nf1
		in.w1, in.w2 = in.w2, in.w1
		return in
	}
	for name, tc := range cases {
		hi, _, ok := tc.k(tc.in)
		lo, _, ok2 := tc.k(swap(tc.in))
		if !ok || !ok2 {
			t.Fatalf("%s: a leg order failed to score", name)
		}
		if hi.dir != 1 || lo.dir != -1 {
			t.Errorf("%s: direction = %v / %v (swapped), want +1 / -1", name, hi.dir, lo.dir)
		}
		if hi.p != lo.p {
			t.Errorf("%s: swapping the legs moved p: %v vs %v", name, hi.p, lo.p)
		}
	}
	// A tie scores direction 0 (prop-z: equal shares, defined pooled SE).
	if out, _, ok := pairwisePropZKernel(pwInputs{p1: 0.4, p2: 0.4, nf1: 50, nf2: 80}); !ok || out.dir != 0 {
		t.Errorf("tie: %+v ok=%v, want direction 0", out, ok)
	}
}

// TestPairwiseLetterName: A … Z, AA, AB, … ZZ, AAA — the spreadsheet
// sequence, so a group past 26 indexes never repeats a letter.
func TestPairwiseLetterName(t *testing.T) {
	t.Parallel()

	for k, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := PairwiseLetterName(k); got != want {
			t.Errorf("PairwiseLetterName(%d) = %q, want %q", k, got, want)
		}
	}
}

// TestPairwiseLetters_RowScopeAndInvariant: ROW scope letters the host
// rows (each column its own opposite position), and a letters part whose
// groups do not rebuild the PAIR axis is PROCESSING_INTERNAL, never a
// silent mislabel.
func TestPairwiseLetters_RowScopeAndInvariant(t *testing.T) {
	t.Parallel()

	host := pairwisePropHost() // rows A/B/C at 50/30/10 (x) and 20/20/20 (y), n = 100
	specs := []types.OverlaySpec{{
		Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow,
		Params: json.RawMessage(`{"letters":{}}`),
	}}
	layers, _, err := applyOverlays(specs, host)
	if err != nil {
		t.Fatal(err)
	}
	layer := layers[0]
	lm := layer.Payload.Parts[PairwiseLettersPart].Matrix
	got := make([][]string, 3)
	for r := 0; r < 3; r++ {
		cell := lm.Cells[r][0].Value.(types.PairwiseLetterCell)
		if want := string(rune('A' + r)); cell.Letter != want {
			t.Errorf("row %d letter %q, want %q", r, cell.Letter, want)
		}
		got[r] = cell.Above
		if y := lm.Cells[r][1].Value.(types.PairwiseLetterCell); len(y.Above) != 0 {
			t.Errorf("column y (all 20%%) row %d above %v, want none", r, y.Above)
		}
	}
	// Column x: 50 vs 30 vs 10 at n = 100 — every pair significant.
	want := [][]string{{"B", "C"}, {"C"}, {}}
	for r := range want {
		if len(got[r]) != len(want[r]) || (len(want[r]) > 0 && got[r][0] != want[r][0]) {
			t.Errorf("row %d above %v, want %v", r, got[r], want[r])
		}
	}

	// Corrupt the groups: one index moved to its own group leaves fewer
	// pairs than the PAIR axis lists.
	cell := lm.Cells[2][0].Value.(types.PairwiseLetterCell)
	cell.Group = 7
	lm.Cells[2][0].Value = cell
	err = RefreshPairwiseLetters(&layer)
	var ce *errors.CodedError
	if !asCoded(err, &ce) || ce.Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("corrupted groups: err = %v, want PROCESSING_INTERNAL", err)
	}
}
