package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"

	"github.com/frankbardon/pulse/internal/weighting"
	"github.com/frankbardon/pulse/types"
)

// TestManifest_WeightAware: every manifest operator family carries
// weight_kinds straight off the shared class table (internal/weighting
// for operators, the overlay class table for overlays — the tables the
// resolver and the engine read), weight_aware is set exactly where
// weight_kinds is non-empty, every built-in aggregator carries a class,
// and every aware aggregator advertises both kinds. A post-test never
// carries a kind.
func TestManifest_WeightAware(t *testing.T) {
	m := BuildManifest()
	both := []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}
	seen := map[string]bool{}
	for _, op := range m.Components.Aggregators {
		seen[op.Name] = true
		if weighting.ClassOf(op.Name) == weighting.ClassNone {
			t.Errorf("%s has no weight class", op.Name)
		}
		if weighting.ClassOf(op.Name) == weighting.ClassAware && !reflect.DeepEqual(op.WeightKinds, both) {
			t.Errorf("%s: aware aggregator weight_kinds = %v, want both", op.Name, op.WeightKinds)
		}
	}
	for _, at := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE, types.AGG_WEIGHTED_MEAN, types.AGG_VARIANCE, types.AGG_STDDEV, types.AGG_WELFORD} {
		if !seen[string(at)] || !weighting.IsAware(string(at)) {
			t.Errorf("%s must be a weight-aware manifest aggregator", at)
		}
	}
	for _, cat := range [][]descriptor.Operator{m.Components.Aggregators, m.Components.Attributes, m.Components.Groupers,
		m.Components.Filterers, m.Components.Windows, m.Components.Features} {
		for _, op := range cat {
			if want := weighting.KindsOf(op.Name); !reflect.DeepEqual(op.WeightKinds, want) {
				t.Errorf("%s: weight_kinds = %v, class table says %v", op.Name, op.WeightKinds, want)
			}
			if op.WeightAware != (len(op.WeightKinds) > 0) {
				t.Errorf("%s: weight_aware = %v with weight_kinds %v", op.Name, op.WeightAware, op.WeightKinds)
			}
		}
	}
	for _, tm := range m.Tests {
		if want := weighting.KindsOf(tm.Family); !reflect.DeepEqual(tm.WeightKinds, want) {
			t.Errorf("test %s: weight_kinds = %v, want %v", tm.Name, tm.WeightKinds, want)
		}
	}
	for _, tm := range m.PostTests {
		if tm.WeightKinds != nil {
			t.Errorf("post-test %s carries weight_kinds %v", tm.Name, tm.WeightKinds)
		}
	}
	for _, r := range m.Regressions {
		if want := weighting.KindsOf(r.Name); !reflect.DeepEqual(r.WeightKinds, want) {
			t.Errorf("regression %s: weight_kinds = %v, want %v", r.Name, r.WeightKinds, want)
		}
	}
	var twin bool
	for _, o := range m.Overlays {
		if want := OverlayWeightKinds(o.Kind); !reflect.DeepEqual(o.WeightKinds, want) {
			t.Errorf("overlay %s: weight_kinds = %v, want %v", o.Kind, o.WeightKinds, want)
		}
		if o.Kind == types.OverlayKindPairwiseWeightedTwoMeansZ {
			twin = reflect.DeepEqual(o.WeightKinds, both)
		}
	}
	if !twin {
		t.Error("OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z must advertise both kinds")
	}
}

// TestManifest_WeightKindsFollowClassFlip: a class flip reaches every
// family's weight_kinds — a frequency-only stub test / regression /
// attribute / grouper advertises ["frequency"] and an aware one both —
// so a later story's flip cannot leave the manifest stale.
func TestManifest_WeightKindsFollowClassFlip(t *testing.T) {
	freq := []types.WeightKind{types.WeightKindFrequency}
	flips := map[string]weighting.Class{
		string(types.TEST_MANN_WHITNEY_U): weighting.ClassFrequencyOnly,
		string(types.REG_BAYES_LINEAR):    weighting.ClassFrequencyOnly,
		string(types.ATTR_ZSCORE):         weighting.ClassFrequencyOnly,
		string(types.GROUP_QUANTILE):      weighting.ClassFrequencyOnly,
	}
	for op, c := range flips {
		defer weighting.OverrideClassForTest(op, c)()
	}
	m := BuildManifest()
	found := 0
	check := func(name string, kinds []types.WeightKind) {
		if _, ok := flips[name]; ok {
			found++
			if !reflect.DeepEqual(kinds, freq) {
				t.Errorf("%s: weight_kinds = %v, want [frequency]", name, kinds)
			}
		}
	}
	for _, tm := range m.Tests {
		check(tm.Name, tm.WeightKinds)
	}
	for _, r := range m.Regressions {
		check(r.Name, r.WeightKinds)
	}
	for _, op := range append(append([]descriptor.Operator{}, m.Components.Attributes...), m.Components.Groupers...) {
		check(op.Name, op.WeightKinds)
		if _, ok := flips[op.Name]; ok && !op.WeightAware {
			t.Errorf("%s: weight_aware not stamped", op.Name)
		}
	}
	if found != len(flips) {
		t.Fatalf("found %d of %d flipped operators in the manifest", found, len(flips))
	}
}
