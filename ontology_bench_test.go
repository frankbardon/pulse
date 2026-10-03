package pulse

import (
	"os"
	"testing"

	"github.com/spf13/afero"
)

// BenchmarkNew_Ontology measures pulse.New, which builds the instance
// ontology eagerly: a profile-free instance shares the process-wide base
// graph (built once, outside the loop), a profiled one prunes it. No
// gate — the U10 target is "a few ms" per profiled instance.
func BenchmarkNew_Ontology(b *testing.B) {
	for _, name := range []string{"", "minimal", "survey-crosstab"} {
		var fp *FeatureProfile
		if name != "" {
			data, err := os.ReadFile(featureSetFixtureDir + name + ".json")
			if err != nil {
				b.Fatal(err)
			}
			if fp, err = ParseFeatureProfile(data); err != nil {
				b.Fatal(err)
			}
		}
		label := name
		if label == "" {
			label = "profile-free"
		}
		b.Run(label, func(b *testing.B) {
			// Warm the base graph so every iteration measures the
			// per-instance cost.
			if _, err := New(Options{FS: afero.NewMemMapFs()}); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: fp}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
