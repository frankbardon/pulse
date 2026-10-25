package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mmmfixture"
	"github.com/spf13/afero"
)

// TestSweepRandom_MMMMCPLHSSweep (mmm-estimation E2-S2): pulse_compose
// runs the seeded lhs decay sweep — its result is byte for byte the
// library's ComposedResponse for the same batch (the request's own
// slot `return` wins over the MCP standard default), the echoed
// sweep_seed included, and the winning slot's decay lies within ±0.1
// of the fixture's true decay.
func TestSweepRandom_MMMMCPLHSSweep(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), mmmfixture.CohortFile, mmmfixture.Schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range mmmfixture.Rows() {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	out := invokeTool(t, p, Config{}, "pulse_compose", string(mmmfixture.DecayLHSSweep))
	var req pulse.ComposedRequest
	if err := json.Unmarshal(mmmfixture.DecayLHSSweep, &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(context.Background(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	want, err := json.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("MCP result differs from the library response:\n mcp %s\n lib %s", got.Bytes(), want)
	}
	if len(lib.Ranking) != mmmfixture.LHSSamples || lib.SweepSeed == nil {
		t.Fatalf("ranking %d, seed %v; want %d and an echoed seed", len(lib.Ranking), lib.SweepSeed, mmmfixture.LHSSamples)
	}
	_, v, _ := strings.Cut(lib.Ranking[0].Label, "_decay=")
	if d, err := strconv.ParseFloat(v, 64); err != nil || math.Abs(d-mmmfixture.TrueDecay) > 0.1 {
		t.Fatalf("best slot %q (%v): want a decay within 0.1 of %v", lib.Ranking[0].Label, err, mmmfixture.TrueDecay)
	}
}
