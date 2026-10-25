package cli

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pulse "github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/mmmfixture"
)

// TestSweepRandom_MMMCLILHSSweep (mmm-estimation E2-S2): `pulse api
// compose --json` runs the seeded lhs decay sweep — its envelope `data`
// is byte for byte the library's ComposedResponse for the same batch,
// the echoed sweep_seed included, and the winning slot's decay lies
// within ±0.1 of the fixture's true decay.
func TestSweepRandom_MMMCLILHSSweep(t *testing.T) {
	dir, p, at := mmmCLIFixture(t)
	raw := at(mmmfixture.DecayLHSSweep)
	reqPath := filepath.Join(dir, "lhs.json")
	if err := os.WriteFile(reqPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runAPICLI(t, &buf, "compose", "--json", "-r", reqPath); err != nil {
		t.Fatalf("compose: %v\n%s", err, buf.String())
	}
	var env struct {
		Errors []any           `json:"errors"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil || len(env.Errors) != 0 {
		t.Fatalf("envelope (%v): %s", err, buf.String())
	}
	var req pulse.ComposedRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	lib, err := p.Compose(t.Context(), &req)
	if err != nil {
		t.Fatalf("library Compose: %v", err)
	}
	want, err := json.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, env.Data); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("CLI data differs from the library response:\n cli %s\n lib %s", got.Bytes(), want)
	}
	if len(lib.Ranking) != mmmfixture.LHSSamples || lib.SweepSeed == nil {
		t.Fatalf("ranking %d, seed %v; want %d and an echoed seed", len(lib.Ranking), lib.SweepSeed, mmmfixture.LHSSamples)
	}
	_, v, _ := strings.Cut(lib.Ranking[0].Label, "_decay=")
	if d, err := strconv.ParseFloat(v, 64); err != nil || math.Abs(d-mmmfixture.TrueDecay) > 0.1 {
		t.Fatalf("best slot %q (%v): want a decay within 0.1 of %v", lib.Ranking[0].Label, err, mmmfixture.TrueDecay)
	}
}
