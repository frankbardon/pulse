package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
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

// fittedExamplesArchExact are the examples whose whole scoring path is
// FMA-free reference arithmetic (the metric registry, linalg
// InverseSPD): byte-for-byte on every architecture. The others read a
// gonum decomposition (PC / factor scores) or a libm-backed chi-square
// tail (Mahalanobis output p), whose values may differ in the last ulps
// across architectures (matrix-and-vectors.md, Package surface); they
// are pinned byte-for-byte where the golden was captured and within
// fittedExamplesCrossArchTol elsewhere.
var fittedExamplesArchExact = map[string]bool{"18_vec_distance.json": true}

// fittedExamplesCrossArchTol bounds a non-arch-exact number's distance
// from the golden, relative to max(1, |value|). Measured drift is
// ~1e-14; any real result change is orders of magnitude larger.
const fittedExamplesCrossArchTol = 1e-12

// fittedExamplesNearDiff compares decoded JSON: every string, bool,
// null, key set and array length exactly; numbers within tol of
// max(1, |value|). "" means they agree.
func fittedExamplesNearDiff(path string, want, got any, tol float64) string {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		if !ok {
			return fmt.Sprintf("%s: want number %v, got %T", path, w, got)
		}
		if !(math.Abs(w-g) <= tol*math.Max(1, math.Max(math.Abs(w), math.Abs(g)))) {
			return fmt.Sprintf("%s: want %.17g, got %.17g", path, w, g)
		}
		return ""
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: object shape differs (want %d keys, got %T)", path, len(w), got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				return fmt.Sprintf("%s: missing key %q", path, k)
			}
			if d := fittedExamplesNearDiff(path+"."+k, wv, gv, tol); d != "" {
				return d
			}
		}
		return ""
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Sprintf("%s: array shape differs", path)
		}
		for i := range w {
			if d := fittedExamplesNearDiff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], tol); d != "" {
				return d
			}
		}
		return ""
	default:
		if want != got {
			return fmt.Sprintf("%s: want %v, got %v", path, want, got)
		}
		return ""
	}
}

// TestFittedState_ExamplesGolden runs every fitted-attribute example
// end to end and pins its rows, warnings and attribute components
// byte-for-byte against the golden captured before the fitted-state
// refactor: the refactor kept every existing fit_on result's bits.
// Cross-architecture the non-arch-exact examples compare within
// fittedExamplesCrossArchTol (fittedExamplesArchExact).
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
		// The golden pins the UNSHAPED fitted result: an example's
		// `return` block (a worked story's compact preset) is
		// presentation, and would drop the components this test pins.
		req.Return = nil
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
		if bytes.Equal(cw.Bytes(), cg.Bytes()) {
			continue
		}
		if fittedExamplesArchExact[name] {
			t.Errorf("%s: output moved from the golden\n got %s\nwant %s", name, cg.Bytes(), cw.Bytes())
			continue
		}
		var dw, dg any
		if err := json.Unmarshal(cw.Bytes(), &dw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(cg.Bytes(), &dg); err != nil {
			t.Fatal(err)
		}
		if d := fittedExamplesNearDiff(name, dw, dg, fittedExamplesCrossArchTol); d != "" {
			t.Errorf("%s: output moved from the golden: %s\n got %s\nwant %s", name, d, cg.Bytes(), cw.Bytes())
		}
	}
}
