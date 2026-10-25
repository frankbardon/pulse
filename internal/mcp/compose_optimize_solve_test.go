package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/optfixture"
	"github.com/spf13/afero"
)

// TestOptimize_MCPComposeReturnsBlocks (mmm-estimation E3-S2):
// pulse_compose returns the optimize blocks — its result is byte for
// byte the library's ComposedResponse for the fitted price
// optimisation, the saved-model one and the hill() budget allocation
// (`optimizations` sits outside the Compose `return`, so the MCP
// standard default never trims it).
func TestOptimize_MCPComposeReturnsBlocks(t *testing.T) {
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.NewCohortBuilder(context.Background(), optfixture.CohortFile, optfixture.Schema(), pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range optfixture.Rows() {
		if err := b.Append(pulse.CohortRow(r)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	mf, err := optfixture.ModelFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, optfixture.ModelPath, mf, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"price": optfixture.Price, "price_model": optfixture.PriceModel, "budget": optfixture.Budget} {
		t.Run(name, func(t *testing.T) {
			out := invokeTool(t, p, Config{}, "pulse_compose", string(body))
			var req pulse.ComposedRequest
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatal(err)
			}
			lib, err := p.Compose(context.Background(), &req)
			if err != nil {
				t.Fatalf("library Compose: %v", err)
			}
			if len(lib.Optimizations) != 1 || lib.Optimizations[0].Error != nil {
				t.Fatalf("library optimizations %+v", lib.Optimizations)
			}
			var got struct {
				Optimizations json.RawMessage `json:"optimizations"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(lib.Optimizations)
			if err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, got.Optimizations); err != nil {
				t.Fatalf("no optimizations in %s: %v", out, err)
			}
			if !bytes.Equal(compact.Bytes(), want) {
				t.Fatalf("MCP optimizations differ from the library's:\n mcp %s\n lib %s", compact.Bytes(), want)
			}
		})
	}
}
