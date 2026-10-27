package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
)

// vectorCLICohort writes a cohort carrying a 3-element 1..5 scale vector
// whose first two elements move together, integer-valued so the fixture
// bytes are the same on every architecture.
func vectorCLICohort(t *testing.T, path string, rows int) {
	t.Helper()
	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sch := encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "rate", Type: encoding.FieldTypeVecF64, Nullable: true},
	}, Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 3, Kind: encoding.VectorKindScale,
		HasScale: true, ScaleMin: 1, ScaleMax: 5, Labels: []string{"a", "b", "c"}}}}
	b, err := p.NewCohortBuilder(t.Context(), path, sch, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rows; r++ {
		var cell any
		if r%10 != 9 {
			x := float64(r%5 + 1)
			cell = []float64{x, x, float64((r/5)%5 + 1)}
		}
		if err := b.Append(pulse.CohortRow{uint64(r), cell}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestSynthCLI_VectorFieldEverySurface: `pulse profile create` captures
// a vector field, `synth from-profile` regenerates it as a tagged top-up
// (augment) with a fidelity report carrying the vector arm, and `synth
// from-schema` generates one from a hand-written spec — the four
// surfaces that refused a vector before #450.
func TestSynthCLI_VectorFieldEverySurface(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "vec.pulse")
	vectorCLICohort(t, src, 400)

	profile := filepath.Join(dir, "profile.json")
	var buf bytes.Buffer
	if err := runProfileCLI(t, &buf, "create", "--input", src, "--output", profile); err != nil {
		t.Fatalf("profile create over a vector cohort: %v (%s)", err, buf.String())
	}
	raw, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Fields []struct {
			Name   string `json:"name"`
			Vector *struct {
				Dim          int               `json:"dim"`
				Kind         string            `json:"kind"`
				Elements     []json.RawMessage `json:"elements"`
				Correlations []struct {
					A   int     `json:"a"`
					B   int     `json:"b"`
					Rho float64 `json:"rho"`
				} `json:"correlations"`
			} `json:"vector"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	vp := doc.Fields[1].Vector
	if vp == nil || vp.Dim != 3 || vp.Kind != "scale" || len(vp.Elements) != 3 {
		t.Fatalf("profile document vector section %s", raw)
	}
	if len(vp.Correlations) == 0 || vp.Correlations[0].A != 1 || vp.Correlations[0].B != 2 || vp.Correlations[0].Rho < 0.999 {
		t.Fatalf("element 1 and 2 are identical; captured correlations %+v", vp.Correlations)
	}

	p, err := newPulse(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	topped := filepath.Join(dir, "topped.pulse")
	report := filepath.Join(dir, "fidelity.json")
	if err := runSynthCLI(t, &buf, "from-profile", "--profile", profile, "--source", src, "--output", topped,
		"--rows", "200", "--seed", "3", "--fidelity-report", report); err != nil {
		t.Fatalf("synth from-profile --source: %v", err)
	}
	if n, err := p.CountRecords(t.Context(), topped); err != nil || n != 600 {
		t.Fatalf("top-up rows %d (%v), want 600", n, err)
	}
	ins, err := p.Inspect(t.Context(), topped)
	if err != nil {
		t.Fatalf("inspect topped-up cohort: %v", err)
	}
	if len(ins.Fields) != 3 || ins.Fields[1].Type != "vec_f64" || ins.Fields[1].Vector == nil {
		t.Fatalf("topped-up schema %+v", ins.Fields)
	}
	rep, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var fr struct {
		VectorFields []struct {
			Field    string            `json:"field"`
			Elements []json.RawMessage `json:"elements"`
		} `json:"vector_fields"`
	}
	if err := json.Unmarshal(rep, &fr); err != nil {
		t.Fatal(err)
	}
	if len(fr.VectorFields) != 1 || fr.VectorFields[0].Field != "rate" || len(fr.VectorFields[0].Elements) != 3 {
		t.Fatalf("fidelity report vector section: %s", rep)
	}

	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(`{"row_count": 25, "fields": [
		{"name": "emb", "type": "vec_f32", "distribution": "normal", "params": {"mean": 0, "std": 1},
		 "vector": {"dim": 8, "kind": "measure", "correlations": [{"a": 1, "b": 2, "rho": 0.5}]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fromSchema := filepath.Join(dir, "schema.pulse")
	if err := runSynthCLI(t, &buf, "from-schema", "--spec", spec, "--output", fromSchema, "--seed", "1"); err != nil {
		t.Fatalf("synth from-schema with a vector field: %v", err)
	}
	if n, err := p.CountRecords(t.Context(), fromSchema); err != nil || n != 25 {
		t.Fatalf("from-schema rows %d (%v)", n, err)
	}
}
