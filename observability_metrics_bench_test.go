package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/internal/obsprom"
)

// BenchmarkNewWithMetrics measures pulse.New with the stdlib Prometheus
// exporter as Options.Metrics, beside a plain New, so the cost metrics
// add to building an instance stays visible. A host may call New per
// tenant or per request; this must stay near the plain figure.
func BenchmarkNewWithMetrics(b *testing.B) {
	dir := b.TempDir()
	b.Run("metrics", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := New(Options{DataDir: dir, Metrics: obsprom.New()}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("plain", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := New(Options{DataDir: dir}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
