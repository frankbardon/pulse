package embeddersmoke

import (
	stderrors "errors"
	"math"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// TestMetricFlow drives the public metric registry the way an embedder
// would: resolve a metric in a form, evaluate it with a missing
// element, and recognise the coded wrong-direction refusal.
func TestMetricFlow(t *testing.T) {
	m, err := metric.Resolve("euclidean", metric.Distance)
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.Eval(metric.Distance, []float64{0, math.NaN(), 3}, []float64{0, 1, 0}, nil)
	if err != nil || math.Abs(d-math.Sqrt(13.5)) > 1e-12 {
		t.Fatalf("euclidean = %v, %v (want sqrt(9 * 3/2))", d, err)
	}
	_, err = metric.Resolve("euclidean", metric.Similarity)
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_VECTOR_METRIC_UNKNOWN {
		t.Fatalf("want PULSE_VECTOR_METRIC_UNKNOWN, got %v", err)
	}
	if len(metric.Names()) != 15 || !metric.Accepts(metric.ConsumerKMeans, "euclidean") {
		t.Fatal("registry listing changed")
	}
}

// TestVectorKindFlow: an embedder declares a vector kind with the
// public spelling and reads the metric it defaults to, consumer-aware.
func TestVectorKindFlow(t *testing.T) {
	lo, hi := 1.0, 5.0
	spec := types.VectorSpec{Name: "sat", Fields: []string{"q*"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &hi}
	if d, ok := metric.Default(metric.Kind(spec.Kind), metric.Similarity); !ok || d.Name() != "correlation" {
		t.Fatalf("scale similarity default = %q, %v", d.Name(), ok)
	}
	if d, ok := metric.DefaultFor(metric.ConsumerMatDistance, metric.Kind(types.VectorKindBinary), metric.Distance, nil); !ok || d.Name() != "cosine" {
		t.Fatalf("MAT_DISTANCE binary default = %q, %v", d.Name(), ok)
	}
	if perrors.PULSE_VECTOR_METRIC_UNSUITED == "" || len(types.AllVectorKinds()) != 4 {
		t.Fatal("kind surface changed")
	}
}
