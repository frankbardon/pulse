package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
)

// TestProfileCreateCLI_RefusesVectorField: `pulse profile create` over a
// cohort carrying a vec field fails with PULSE_SYNTH_VECTOR_UNSUPPORTED
// and writes no profile document.
func TestProfileCreateCLI_RefusesVectorField(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "vec.pulse")
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sch := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "emb", Type: encoding.FieldTypeVecF64},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 2}}}
	b, err := p.NewCohortBuilder(t.Context(), src, sch, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < 4; r++ {
		if err := b.Append(pulse.CohortRow{uint64(r), []float64{float64(r), 1}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "profile.json")
	var buf bytes.Buffer
	err = runProfileCLI(t, &buf, "create", "--input", src, "--output", out)
	if err == nil || !strings.Contains(err.Error()+buf.String(), "PULSE_SYNTH_VECTOR_UNSUPPORTED") {
		t.Fatalf("profile create over a vector cohort: err %v, out %s", err, buf.String())
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("a refused profile create wrote its output")
	}
}
