package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Refusal and suggestion prose built in code names only operators the
// instance offers. Each case pins the unscoped wording byte for byte
// (the default instance is unchanged) and the scoped wording with the
// named operators hidden.

func TestZoneCapableAdvice_NamesOnlyOffered(t *testing.T) {
	var capable []string
	for n, z := range zoneCapabilities {
		if z == ZoneCapable {
			capable = append(capable, n)
		}
	}
	listed := slices.Clone(zoneCapableOperators)
	slices.Sort(capable)
	slices.Sort(listed)
	if !slices.Equal(capable, listed) {
		t.Fatalf("zoneCapableOperators %v drifted from the zone table %v", listed, capable)
	}
	if got, want := zoneCapableAdvice(nil), "only zone-capable operators (GROUP_DATE, GROUP_DATE_RANGES, FILTER_DATE_RANGES, ATTR_DATE_PART, FEAT_DATE_FEATURES) take a per-slot time zone"; got != want {
		t.Errorf("unscoped = %q, want %q", got, want)
	}
	if got, want := zoneCapableAdvice(scopedExcept("GROUP_DATE_RANGES", "FEAT_DATE_FEATURES")), "only zone-capable operators (GROUP_DATE, FILTER_DATE_RANGES, ATTR_DATE_PART) take a per-slot time zone"; got != want {
		t.Errorf("scoped = %q, want %q", got, want)
	}
	if got, want := zoneCapableAdvice(scopedExcept(zoneCapableOperators...)), "no operator this instance offers takes a per-slot time zone"; got != want {
		t.Errorf("none offered = %q, want %q", got, want)
	}
}

func TestPairwiseAdvice_NamesOnlyOffered(t *testing.T) {
	opts := func(hidden ...string) *PredictOptions { return &PredictOptions{Instance: scopedExcept(hidden...)} }
	prop, probit := string(types.OverlayKindPairwisePropZ), string(types.OverlayKindPairwiseProbitT)
	weighted := string(types.OverlayKindPairwiseWeightedTwoMeansZ)
	cases := []struct {
		name, got, want string
	}{
		{"proportion unscoped", pairwiseProportionAdvice(nil, "derive a proportion leg"),
			", or use OVERLAY_PAIRWISE_PROP_Z / OVERLAY_PAIRWISE_PROBIT_T, which derive a proportion leg"},
		{"proportion one hidden", pairwiseProportionAdvice(opts(prop), "derive a proportion leg"),
			", or use OVERLAY_PAIRWISE_PROBIT_T, which can derive a proportion leg"},
		{"proportion both hidden", pairwiseProportionAdvice(opts(prop, probit), "derive a proportion leg"), ""},
		{"n_basis unscoped", pairwiseNBasisAdvice(nil),
			"it is read only by OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z. Remove n_basis, or use that kind over an AGG_WEIGHTED_MEAN cell"},
		{"n_basis weighted hidden", pairwiseNBasisAdvice(opts(weighted)),
			"no pairwise kind this instance offers reads it. Remove n_basis"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestTrainTestSplitName_NamesOnlyOffered(t *testing.T) {
	if got := trainTestSplitName(nil); got != "FEAT_TRAIN_TEST_SPLIT" {
		t.Errorf("unscoped = %q", got)
	}
	if got := trainTestSplitName(scopedExcept("FEAT_TRAIN_TEST_SPLIT")); got != "train/test split" {
		t.Errorf("hidden = %q", got)
	}
}

func TestReasonFromCode_HintNamingHiddenFallsBack(t *testing.T) {
	c := errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL
	meta, _ := errors.MetadataFor(c)
	hint := meta.Fixups[0].Hint
	if got := reasonFromCode(c, "fallback", nil); got != hint {
		t.Errorf("unscoped = %q, want the fixup hint %q", got, hint)
	}
	if got := reasonFromCode(c, "fallback", scopedExcept("AGG_MEDIAN")); got != hint {
		t.Errorf("hint naming no hidden operator = %q, want the fixup hint", got)
	}
	if got := reasonFromCode(c, "fallback", scopedExcept("AGG_STDDEV")); got != "fallback" {
		t.Errorf("hint naming hidden AGG_STDDEV = %q, want the fallback", got)
	}
}
