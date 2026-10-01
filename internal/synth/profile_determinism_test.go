package synth_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// TestProfile_ConditionalCaptureIsByteReproducible is E2-S4's
// end-to-end bar and the assertion docs/src/cli/profile-create.md's
// reproducibility claim rests on: two `profile create --conditional`
// runs over ONE cohort must serialize to byte-identical documents.
//
// The fixture is shaped to hit the only place the claim was false. It
// carries 200 distinct categories against a default TopK of 32, so 168
// of them fold into the single shared "other" accumulator inside
// computeConditionalCategoricalNumericPairs' collapse, and its numeric
// values sit around 12,690 — the magnitude at which sumSq (~1e11)
// dwarfs the variance (~1e8) enough that one last-bit difference in
// the fold surfaces as a ~1e-10 relative drift in the emitted `std`.
// A cohort whose categories all fit inside TopK would pass this test
// on the buggy tree, because every in-top-K bucket has exactly one
// source and no fold order to get wrong.
//
// Six paired runs rather than one: Go re-randomizes map iteration per
// range statement, so a single comparison can pass on a broken tree by
// luck. It cannot do so repeatedly across a 200-key map.
func TestProfile_ConditionalCaptureIsByteReproducible(t *testing.T) {
	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	data := synthManyCategoryNumeric(t, 200, 5, 97)
	if err := afero.WriteFile(fs, "/source.pulse", data, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	opts := pulse.ProfileOptions{IncludeStats: true, IncludeConditional: true}
	capture := func() []byte {
		t.Helper()
		prof, err := p.Profile(context.Background(), "/source.pulse", opts)
		if err != nil {
			t.Fatalf("profile: %v", err)
		}
		raw, err := json.Marshal(prof)
		if err != nil {
			t.Fatalf("marshal profile: %v", err)
		}
		return raw
	}

	first := capture()
	for run := 1; run < 6; run++ {
		next := capture()
		if string(next) != string(first) {
			t.Fatalf("run %d differs from run 0:\nfirst: %s\n next: %s",
				run, firstDivergence(first, next), firstDivergence(next, first))
		}
	}
}

// firstDivergence renders a short window around the first differing
// byte, so a failure names where the documents parted rather than
// dumping two multi-kilobyte blobs.
func firstDivergence(a, b []byte) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := i - 60
	if lo < 0 {
		lo = 0
	}
	hi := i + 60
	if hi > len(a) {
		hi = len(a)
	}
	return fmt.Sprintf("...%s... (offset %d)", a[lo:hi], i)
}

// synthManyCategoryNumeric writes a cohort with one categorical field
// carrying categoryCount distinct values — deliberately far above the
// default ProfileOptions.TopK of 32, so the conditional collapse has a
// genuinely multi-source "other" bucket to fold — and one numeric
// field whose values sit near 12,690, the magnitude at which the
// (sumSq - mean*sum) variance form cancels hard enough for fold order
// to reach the emitted std.
func synthManyCategoryNumeric(t *testing.T, categoryCount, rowsPerCategory int, seed int64) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	var buf strings.Builder
	buf.WriteString("region,score\n")
	for c := 0; c < categoryCount; c++ {
		for r := 0; r < rowsPerCategory; r++ {
			v := float64(rng.NormFloat64()*430.0) + 12690.0
			fmt.Fprintf(&buf, "region_%03d,%.8f\n", c, v)
		}
	}
	return importCSVFixture(t, buf.String(), categoryCount*rowsPerCategory)
}
