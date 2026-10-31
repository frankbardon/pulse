package synth_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/synth"
)

// flagCohort is a packed_bool `flag` whose prevalence is 10% in segment
// "a" and 60% in "b", plus a numeric `spend` that `flag` does not
// drive. separate makes flag = 1 exactly in segment "b".
func flagCohort(t *testing.T, rows int, separate bool) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(9))
	var buf strings.Builder
	buf.WriteString("segment,spend,flag\n")
	for i := 0; i < rows; i++ {
		seg, p := "a", 0.1
		if i%2 == 1 {
			seg, p = "b", 0.6
		}
		flag := "false"
		if separate && seg == "b" || !separate && rng.Float64() < p {
			flag = "true"
		}
		fmt.Fprintf(&buf, "%s,%d,%s\n", seg, 20+rng.Intn(50), flag)
	}
	return importCSVFixture(t, buf.String(), rows)
}

func flagModel(t *testing.T, prof *synth.Profile) *synth.FieldModel {
	t.Helper()
	for i := range prof.Models {
		if prof.Models[i].Field == "flag" {
			return &prof.Models[i]
		}
	}
	t.Fatalf("no model for flag; warnings=%v", prof.Warnings)
	return nil
}

// segmentRates is P(flag | segment) read off a conditional capture.
func segmentRates(t *testing.T, data []byte) map[string]float64 {
	t.Helper()
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeConditional: true, Seed: 3})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	for _, p := range prof.Conditional.CategoricalNumericPairs {
		if p.A == "segment" && p.B == "flag" {
			out := map[string]float64{}
			for _, c := range p.Categories {
				out[c.Category] = c.Mean
			}
			return out
		}
	}
	t.Fatal("no segment x flag pair captured")
	return nil
}

// TestProfileProbit_BooleanTargetDrawsItsCapturedRates is the bernoulli-
// target fix: a packed_bool target is fitted as a probit, and the twin
// reproduces its per-segment rates. The linear-probability fit it
// replaces, read through the same probit draw, pulled both rates toward
// the prevalence (to ~0.14 and ~0.56 here).
func TestProfileProbit_BooleanTargetDrawsItsCapturedRates(t *testing.T) {
	t.Parallel()

	const rows = 10000
	source := flagCohort(t, rows, false)
	prof, err := synth.ProfileBytes(source, synth.ProfileOptions{FitModels: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	m := flagModel(t, prof)
	if m.Link != synth.ModelLinkProbit || m.ResidualStd != 1 {
		t.Errorf("flag model link %q residual_std %v, want probit and 1", m.Link, m.ResidualStd)
	}
	for _, other := range prof.Models {
		if other.Field != "flag" && other.Link != "" {
			t.Errorf("non-boolean target %q carries link %q", other.Field, other.Link)
		}
	}

	// The residual reservoir (residual correlations read it) is the
	// response residual y − Φ(η): on the 0/1 scale, centred.
	var sum float64
	var n int
	for i, present := range m.ResidualPresent {
		if !present {
			continue
		}
		r := m.Residuals[i]
		if r < -1 || r > 1 {
			t.Fatalf("probit residual %v at row %d is off the 0/1 scale: not y − Φ(η)", r, i)
		}
		sum += r
		n++
	}
	if n == 0 || sum/float64(n) > 0.02 || sum/float64(n) < -0.02 {
		t.Errorf("probit residuals over %d rows have mean %v, want ~0", n, sum/float64(max(n, 1)))
	}

	spec, warnings := synth.SpecFromProfile(prof, rows)
	for _, w := range warnings {
		if strings.Contains(w, `"flag"`) {
			t.Errorf("flag's model did not apply cleanly: %s", w)
		}
	}
	twin, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	src, tw := segmentRates(t, source), segmentRates(t, twin)
	for _, seg := range []string{"a", "b"} {
		if d := tw[seg] - src[seg]; d > 0.02 || d < -0.02 {
			t.Errorf("segment %s: twin P(flag) %.3f, source %.3f", seg, tw[seg], src[seg])
		}
	}
}

// TestProfileProbit_SeparatedTargetKeepsLinearFit covers the fallback:
// a predictor that decides the target outright has no probit MLE, so
// the linear fit is kept, the document says so, and the warning is an
// attention item.
func TestProfileProbit_SeparatedTargetKeepsLinearFit(t *testing.T) {
	t.Parallel()

	prof, err := synth.ProfileBytes(flagCohort(t, 2000, true), synth.ProfileOptions{FitModels: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if m := flagModel(t, prof); m.Link != "" {
		t.Errorf("separated flag model link %q, want the linear fit (no link)", m.Link)
	}
	found := false
	for _, w := range prof.Warnings {
		if strings.Contains(w, `model for numeric field "flag" kept its linear fit`) {
			found = true
		}
	}
	if !found {
		t.Errorf("no fallback warning for flag; warnings=%v", prof.Warnings)
	}
}

// TestSpecFromProfile_ProbitLinkOnNonBooleanTargetNotApplied guards the
// translation: a probit model only means something through the
// bernoulli draw, so on any other target it is not applied, by name.
func TestSpecFromProfile_ProbitLinkOnNonBooleanTargetNotApplied(t *testing.T) {
	t.Parallel()

	prof, err := synth.ProfileBytes(flagCohort(t, 2000, false), synth.ProfileOptions{FitModels: true, Seed: 7})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	var spend *synth.FieldModel
	for i := range prof.Models {
		if prof.Models[i].Field == "spend" {
			spend = &prof.Models[i]
		}
	}
	if spend == nil {
		t.Fatalf("fixture captured no spend model; warnings=%v", prof.Warnings)
	}
	spend.Link = synth.ModelLinkProbit
	_, warnings := synth.SpecFromProfile(prof, 100)
	if !strings.Contains(strings.Join(warnings, "\n"), `model for numeric field "spend" not applied: model link "probit" does not apply`) {
		t.Errorf("a probit link on a normal target must not apply; warnings=%v", warnings)
	}
}
