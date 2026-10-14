package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/spf13/afero"
)

// updateFittedExamples regenerates testdata/fitted_state/examples.golden.json
// (`go test . -run TestFittedState_ExamplesGolden -update-fitted-examples`).
// The golden was first written BEFORE the fitted-state refactor; never
// regenerate it to make a refactor pass.
var updateFittedExamples = flag.Bool("update-fitted-examples", false, "rewrite the fitted-attribute example golden")

const fittedExamplesGolden = "testdata/fitted_state/examples.golden.json"

// fittedExamples are the embedded examples that exercise a fitted
// vector attribute (ATTR_MAHALANOBIS, ATTR_PC_SCORE, ATTR_FACTOR_SCORE,
// fitted ATTR_VEC_DISTANCE metrics), fit_on included.
var fittedExamples = []string{
	"12_mahalanobis.json",
	"13_pc_score.json",
	"14_factor_score.json",
	"18_vec_distance.json",
}

// TestFittedState_ExamplesGolden runs every fitted-attribute example
// end to end and pins its rows, warnings and attribute components
// byte-for-byte against the golden captured before the fitted-state
// refactor: the refactor kept every existing fit_on result's bits.
func TestFittedState_ExamplesGolden(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	buildExampleFixtures(t, p, fs, tmp)

	got := map[string]json.RawMessage{}
	for _, name := range fittedExamples {
		body, err := os.ReadFile(filepath.Join("internal", "examples", "attributes", name))
		if err != nil {
			t.Fatal(err)
		}
		var req pulse.Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		req.Cohort.DataDir = tmp
		resp, err := p.Process(context.Background(), &req)
		if err != nil {
			t.Fatalf("%s: Process: %v", name, err)
		}
		pinned := map[string]any{"data": resp.Data, "warnings": resp.Warnings}
		if resp.Components != nil {
			pinned["attributes"] = resp.Components.Attributes
		}
		raw, err := json.Marshal(pinned)
		if err != nil {
			t.Fatal(err)
		}
		got[name] = raw
	}

	if *updateFittedExamples {
		raw, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(fittedExamplesGolden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fittedExamplesGolden, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(fittedExamplesGolden)
	if err != nil {
		t.Fatalf("read %s: %v", fittedExamplesGolden, err)
	}
	var want map[string]json.RawMessage
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("golden has %d examples, ran %d", len(want), len(got))
	}
	for name, g := range got {
		var cw, cg bytes.Buffer
		if err := json.Compact(&cw, want[name]); err != nil {
			t.Fatalf("%s: golden entry: %v", name, err)
		}
		if err := json.Compact(&cg, g); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cw.Bytes(), cg.Bytes()) {
			t.Errorf("%s: output moved from the golden\n got %s\nwant %s", name, cg.Bytes(), cw.Bytes())
		}
	}
}
