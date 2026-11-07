package integration_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestAddingOverlayRecipe_RequestRuns: the recipe's JSON request block
// (docs/src/internals/adding-overlay.md, step 6) decodes into a typed
// request and, over a registration of the recipe's kind, yields a MATRIX
// layer whose raw p grid is corrected by the request's multiplicity
// block. The recipe cannot drift from the request shape it teaches.
func TestAddingOverlayRecipe_RequestRuns(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("docs/src/internals/adding-overlay.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile("(?s)```json\n(.*?)```").FindSubmatch(doc)
	if m == nil {
		t.Fatal("recipe has no json request block")
	}
	var req *types.Request
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(m[1]), "sales.pulse", "e2e.pulse")), &req); err != nil {
		t.Fatalf("recipe request does not decode: %v", err)
	}

	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	reg := pGridRegistration()
	reg.Name = "OVERLAY_ACME_PGRID" // the recipe's kind
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{reg}}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	resp := processE2E(t, p, req)
	if len(resp.Overlays) != 1 || resp.Overlays[0].Name != "acme_pgrid" {
		t.Fatalf("overlays = %+v, want one layer named acme_pgrid", resp.Overlays)
	}
	if resp.Overlays[0].Payload.PAdjusted == nil || resp.Overlays[0].Multiplicity == nil {
		t.Errorf("multiplicity block did not correct the layer's p grid")
	}
}
