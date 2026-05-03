package synth_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// TestSynthExamples_RunAll iterates every JSON spec under examples/synth
// and asserts the synthesizer produces the requested row count without
// returning an error. Catches example bitrot and serves as a smoke test
// for every supported distribution kind.
func TestSynthExamples_RunAll(t *testing.T) {
	dir := filepath.Join("..", "examples", "synth")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 example specs, got %d", len(entries))
	}

	fs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("reading example: %v", err)
			}
			spec, err := synth.ParseSpec(raw)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			out := "/" + e.Name() + ".pulse"
			res, err := p.Synth(context.Background(), spec, out, pulse.SynthOptions{Seed: 7})
			if err != nil {
				t.Fatalf("synth: %v", err)
			}
			if res.RowsGenerated != spec.RowCount {
				t.Errorf("rows generated = %d, want %d", res.RowsGenerated, spec.RowCount)
			}
		})
	}
}
