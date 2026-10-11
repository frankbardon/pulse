package embeddersmoke

import (
	stderrors "errors"
	"math"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
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
