package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// semOrdinalScalars are the MAT_SEM scalars only an ordinal (wlsmv) fit
// emits: the scaled-shifted test and the scaled fit indices.
var semOrdinalScalars = []string{"chi2_scaled", "scaling_factor", "shift_parameter", "chi2_baseline_scaled", "cfi_scaled", "tli_scaled", "rmsea_scaled"}

// TestSEM_ScaledFitIndicesInterpreted: every scalar a wlsmv fit adds is
// read by a MAT_SEM Interpretation (chi2_scaled_p_value inside the
// chi2_scaled entry), the estimates entry reads the x | tk threshold
// rows, and the Purpose says when to treat ratings as ordinal. Every
// such text names a capability:sem_ordinal token, so an instance hiding
// that capability renders none of it (the reading section skips an
// entry whose Means scrubs to empty).
func TestSEM_ScaledFitIndicesInterpreted(t *testing.T) {
	ins, ok := InterpretationsOf("MAT_SEM")
	if !ok {
		t.Fatal("MAT_SEM declares no Interpretation")
	}
	by := map[string]descriptor.Interpretation{}
	for _, in := range ins {
		by[in.Field] = in
	}
	hidden := proseScrubFor() // every SEM capability disabled
	for _, k := range semOrdinalScalars {
		in, ok := by["scalars."+k]
		if !ok {
			t.Errorf("scalars.%s is not interpreted", k)
			continue
		}
		if strings.TrimSpace(in.Means) == "" {
			t.Errorf("scalars.%s has no Means", k)
		}
		if got := strings.TrimSpace(hidden.Text(in.Means)); got != "" {
			t.Errorf("scalars.%s Means survives a hidden capability:sem_ordinal: %q", k, got)
		}
		for _, c := range in.Caveats {
			if got := strings.TrimSpace(hidden.Text(c)); got != "" {
				t.Errorf("scalars.%s caveat survives a hidden capability:sem_ordinal: %q", k, got)
			}
		}
	}
	if !strings.Contains(by["scalars.chi2_scaled"].Means, "chi2_scaled_p_value") {
		t.Error("scalars.chi2_scaled does not read chi2_scaled_p_value")
	}
	est := by["auxiliary.estimates"].Means
	if !strings.Contains(est, "x | tk") {
		t.Error("auxiliary.estimates does not read the threshold rows")
	}
	if strings.Contains(hidden.Text(est), "x | tk") {
		t.Error("the threshold-row sentence survives a hidden capability:sem_ordinal")
	}
	p, _ := PurposeOf("MAT_SEM")
	var ordinal string
	for _, a := range p.Assumptions {
		if strings.Contains(a, "wlsmv") {
			ordinal = a
		}
	}
	if ordinal == "" {
		t.Error("the MAT_SEM Purpose never says when to fit ratings as ordinal")
	} else if hidden.Text(ordinal) != "" {
		t.Errorf("the ordinal assumption survives a hidden capability:sem_ordinal: %q", hidden.Text(ordinal))
	}
}

// TestSEMScore_OrdinalReadingInterpreted: ATTR_SEM_SCORE's reading
// says an ordinal fit's score is an EBM score on the latent-response
// scale and that an unseen category reads null, in a caveat an instance
// hiding capability:sem_ordinal drops.
func TestSEMScore_OrdinalReadingInterpreted(t *testing.T) {
	ins, _ := InterpretationsOf("ATTR_SEM_SCORE")
	hidden := proseScrubFor()
	var found string
	for _, in := range ins {
		for _, c := range in.Caveats {
			if strings.Contains(c, "empirical Bayes modal") {
				found = c
			}
		}
	}
	if found == "" {
		t.Fatal("ATTR_SEM_SCORE never reads an ordinal fit's EBM score")
	}
	if !strings.Contains(found, "category") {
		t.Errorf("the ordinal caveat omits the unseen-category null: %q", found)
	}
	if got := hidden.Text(found); got != "" {
		t.Errorf("the ordinal caveat survives a hidden capability:sem_ordinal: %q", got)
	}
}
