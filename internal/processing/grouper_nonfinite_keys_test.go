package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestGrouperKeys_NonFiniteSpelledFixed (E4-S8): a numeric bucket key of
// ±Inf, NaN or an integral magnitude of 2^63 or more has one spelling on
// every architecture. formatRoundedNumeric ran int64(v) on every
// integral value, and Go defines that conversion only inside int64's
// range: amd64 turned +Inf, -Inf and 1e20 into "-9223372036854775808",
// arm64 into ±"9223372036854775807", so GROUP_CATEGORY / GROUP_ROUNDED /
// GROUP_RANGE keys differed between the two. The expected strings are
// literals, never a platform conversion.
func TestGrouperKeys_NonFiniteSpelledFixed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want string
	}{
		{math.Inf(1), "+Inf"},
		{math.Inf(-1), "-Inf"},
		{math.NaN(), "NaN"},
		{1e20, "100000000000000000000"},
		{-1e19, "-10000000000000000000"},
		{0x1p63, "9223372036854775808"},
		// Inside int64's range the historical spelling is unchanged.
		{-0x1p63, "-9223372036854775808"},
		{0x1p62, "4611686018427387904"},
		{math.Copysign(0, -1), "0"},
		{42, "42"},
		{-2.5, "-2.5"},
	}
	for _, c := range cases {
		if got := formatRoundedNumeric(c.in); got != c.want {
			t.Errorf("formatRoundedNumeric(%v) = %q, want %q", c.in, got, c.want)
		}
	}

	schema := numericSchema()
	for _, gt := range []types.GroupType{types.GROUP_CATEGORY, types.GROUP_ROUNDED} {
		g := makeGrouper(t, gt, "score", 10, schema).(StreamableGrouper)
		for in, want := range map[float64]string{math.Inf(1): "+Inf", math.Inf(-1): "-Inf", 1e20: "100000000000000000000"} {
			key, err := g.KeyFor(NewRecord(schema, map[string]float64{"score": in}))
			if err != nil || key != want {
				t.Errorf("%s key of %v = %q (%v), want %q", gt, in, key, err, want)
			}
		}
	}
	g := makeGrouper(t, types.GROUP_RANGE, "score", 10, schema).(StreamableGrouper)
	if key, err := g.KeyFor(NewRecord(schema, map[string]float64{"score": math.Inf(-1)})); err != nil || key != "-Inf--Inf" {
		t.Errorf("GROUP_RANGE key of -Inf = %q (%v), want \"-Inf--Inf\"", key, err)
	}

	// The fixed spellings still rank by value: -Inf first, +Inf last.
	cg := makeGrouper(t, types.GROUP_CATEGORY, "score", 0, schema).(*categoryGrouper)
	lo, lok := cg.keyRank("-Inf")
	hi, hok := cg.keyRank("+Inf")
	big, bok := cg.keyRank("100000000000000000000")
	if !lok || !hok || !bok || !(lo < big && big < hi) {
		t.Errorf("keyRank(-Inf, 1e20, +Inf) = (%v %v, %v %v, %v %v), want ascending", lo, lok, big, bok, hi, hok)
	}
}
