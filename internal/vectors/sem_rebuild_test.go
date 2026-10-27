package vectors

import (
	"testing"

	"github.com/frankbardon/pulse/internal/model"
)

// TestSEMScore_RebuildChecksStoredOrders: RebuildSEMModel re-specifies
// a SEM fit's model and refuses one whose observed, inner or latent
// order differs from the stored fit's — the check a loaded SEM fit file
// passes at load (model.CheckLoadedSEM) and a bound fit at scoring.
func TestSEMScore_RebuildChecksStoredOrders(t *testing.T) {
	t.Parallel()

	good := func() *model.SEMFit {
		return &model.SEMFit{Name: "f", Model: "f =~ q1 + q2 + q3", Lavaan: true,
			Observed: []string{"q1", "q2", "q3"}, Inner: []string{"f"}, Latents: []string{"f"}}
	}
	if err := CheckSEMRebuild(good()); err != nil {
		t.Fatalf("a matching fit: %v", err)
	}
	for name, mutate := range map[string]func(*model.SEMFit){
		"observed order": func(f *model.SEMFit) { f.Observed = []string{"q2", "q1", "q3"} },
		"inner":          func(f *model.SEMFit) { f.Inner = []string{"g"} },
		"latents":        func(f *model.SEMFit) { f.Latents = nil },
		"model syntax":   func(f *model.SEMFit) { f.Model = "f =~" },
	} {
		f := good()
		mutate(f)
		if err := CheckSEMRebuild(f); err == nil {
			t.Errorf("%s: a mismatched fit rebuilds", name)
		}
	}
}
