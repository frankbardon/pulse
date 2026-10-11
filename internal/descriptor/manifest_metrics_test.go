package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/linalg/metric"
)

// manifestMetrics returns m.Matrix.Metrics keyed by name, failing when
// the matrix block is absent.
func manifestMetrics(t *testing.T, m *descriptor.Manifest) ([]string, map[string]descriptor.MetricMeta) {
	t.Helper()
	if m.Matrix == nil {
		t.Fatal("manifest has no matrix capability block")
	}
	names := make([]string, 0, len(m.Matrix.Metrics))
	set := make(map[string]descriptor.MetricMeta, len(m.Matrix.Metrics))
	for _, mm := range m.Matrix.Metrics {
		names = append(names, mm.Name)
		set[mm.Name] = mm
	}
	return names, set
}

func strs[T ~string](in []T) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = string(v)
	}
	return out
}

// TestManifestMetricsComplete: capabilities.matrix.metrics lists every
// linalg/metric registry entry, in registry order, with its kinds, poor
// fits, direction, forms, fitted flag, domain rules and the consumers
// this build registers — no more, no fewer.
func TestManifestMetricsComplete(t *testing.T) {
	m := BuildManifest()
	names, set := manifestMetrics(t, m)
	if !slices.Equal(names, metric.Names()) {
		t.Fatalf("manifest metrics %v\nregistry %v", names, metric.Names())
	}
	registered := map[string]bool{}
	for _, ops := range [][]descriptor.Operator{m.Components.Attributes, m.Components.Filterers, m.Components.Groupers} {
		for _, o := range ops {
			registered[o.Name] = true
		}
	}
	for _, mm := range m.Matrices {
		registered[mm.Name] = true
	}
	offered := func(c metric.Consumer) bool {
		if c == metric.ConsumerExpr {
			return registered["ATTR_FORMULA"] || registered["FILTER_EXPRESSION"]
		}
		return registered[string(c)]
	}
	for _, r := range metric.All() {
		e := set[r.Name()]
		if e.Description == "" || e.Description != r.Description() {
			t.Errorf("%s description %q", r.Name(), e.Description)
		}
		if e.Family != string(r.Family()) || e.Direction != string(r.Direction()) || e.Domain != string(r.Domain()) {
			t.Errorf("%s family/direction/domain = %s/%s/%s", r.Name(), e.Family, e.Direction, e.Domain)
		}
		if !slices.Equal(e.Forms, strs(r.Forms())) || !slices.Equal(e.KindsSuited, strs(r.KindsSuited())) || !slices.Equal(e.PoorFit, strs(r.PoorFit())) {
			t.Errorf("%s forms/kinds/poor = %v/%v/%v", r.Name(), e.Forms, e.KindsSuited, e.PoorFit)
		}
		if e.Fitted != r.Fitted() || e.FitStat != string(r.Fit()) || e.ZeroUndefined != r.ZeroUndefined() ||
			e.Scaled != r.Scaled() || e.Complete != r.Complete() {
			t.Errorf("%s flags = %+v", r.Name(), e)
		}
		if e.KindsSuited == nil || e.PoorFit == nil || e.Consumers == nil || e.Forms == nil {
			t.Errorf("%s carries a null list; lists are [] when empty", r.Name())
		}
		var want []string
		for _, c := range r.Consumers() {
			if offered(c) {
				want = append(want, string(c))
			}
		}
		if !slices.Equal(e.Consumers, want) && !(len(e.Consumers) == 0 && len(want) == 0) {
			t.Errorf("%s consumers %v, want %v", r.Name(), e.Consumers, want)
		}
	}
	// Today's consumers are in this build and must surface.
	if c := set["euclidean"].Consumers; !slices.Contains(c, "expr") || !slices.Contains(c, "MAT_DISTANCE") || !slices.Contains(c, "GROUP_KMEANS") {
		t.Errorf("euclidean consumers %v miss a registered consumer", c)
	}
	if c := set["chi_square"].Consumers; len(c) != 0 {
		t.Errorf("chi_square is registered but unexposed, consumers %v", c)
	}
}

// TestManifestMetrics_InstanceHidesConsumers: a feature profile that
// hides a consumer drops it from every metric's consumers; hiding
// capability:matrices drops the block.
func TestManifestMetrics_InstanceHidesConsumers(t *testing.T) {
	m := BuildManifestForInstance(scopedExcept("MAT_DISTANCE"))
	_, set := manifestMetrics(t, m)
	for name, e := range set {
		if slices.Contains(e.Consumers, "MAT_DISTANCE") {
			t.Errorf("%s lists hidden MAT_DISTANCE", name)
		}
	}
	if !slices.Contains(set["euclidean"].Consumers, "GROUP_KMEANS") {
		t.Error("hiding MAT_DISTANCE dropped GROUP_KMEANS")
	}
	m = BuildManifestForInstance(scopedExcept("ATTR_FORMULA", "FILTER_EXPRESSION"))
	_, set = manifestMetrics(t, m)
	if slices.Contains(set["cosine"].Consumers, "expr") {
		t.Error("expr listed with both expression operators hidden")
	}
	m = BuildManifestForInstance(scopedExcept("ATTR_FORMULA"))
	_, set = manifestMetrics(t, m)
	if !slices.Contains(set["cosine"].Consumers, "expr") {
		t.Error("expr dropped while FILTER_EXPRESSION is still offered")
	}
}
