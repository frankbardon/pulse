package processing

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/frankbardon/pulse/linalg/metric"
)

// TestKMeans_SqDistIsRegistrySqEuclidean: GROUP_KMEANS resolves its
// distance through linalg/metric (the Euclidean family only) and, over
// the finite complete points it compares, gives exactly the bits of the
// FMA-free left-to-right sum it computed before the registry — so the
// determinism contract and every pinned partition are unchanged.
func TestKMeans_SqDistIsRegistrySqEuclidean(t *testing.T) {
	if kmeansMetric.Name() != "sq_euclidean" || !metric.Accepts(metric.ConsumerKMeans, kmeansMetric.Name()) {
		t.Fatalf("k-means metric %q", kmeansMetric.Name())
	}
	plain := func(a, b []float64) float64 {
		var s float64
		for j := range a {
			d := a[j] - b[j]
			s += float64(d * d)
		}
		return s
	}
	rng := rand.New(rand.NewPCG(27, 7))
	for trial := 0; trial < 2000; trial++ {
		p := 1 + rng.IntN(12)
		a, b := make([]float64, p), make([]float64, p)
		for j := range a {
			a[j] = rng.NormFloat64() * math.Pow(10, float64(rng.IntN(7)-3))
			b[j] = rng.NormFloat64() * math.Pow(10, float64(rng.IntN(7)-3))
		}
		if got, want := sqDist(a, b), plain(a, b); math.Float64bits(got) != math.Float64bits(want) {
			t.Fatalf("trial %d: sqDist %v != plain sum %v (bits differ)", trial, got, want)
		}
	}
}
