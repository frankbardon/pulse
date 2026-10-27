package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestOverlayCapabilities_PartsFromTable: every kind's manifest Parts is
// exactly its overlayPartShapes entry (name, shape, when), sorted by
// name; a kind without an entry declares none and its JSON carries no
// "parts" key.
func TestOverlayCapabilities_PartsFromTable(t *testing.T) {
	t.Parallel()

	seen := map[types.OverlayKind]bool{}
	for _, c := range OverlayCapabilities() {
		seen[c.Kind] = true
		decl := overlayPartShapes[c.Kind]
		if len(c.Parts) != len(decl) {
			t.Errorf("%s: %d parts, want %d (%+v)", c.Kind, len(c.Parts), len(decl), c.Parts)
			continue
		}
		for i, p := range c.Parts {
			d, ok := decl[p.Name]
			if !ok || d.Shape != p.Shape || d.When != p.When {
				t.Errorf("%s part %q: %+v, table %+v (declared %v)", c.Kind, p.Name, p, d, ok)
			}
			if i > 0 && c.Parts[i-1].Name >= p.Name {
				t.Errorf("%s parts not sorted: %q before %q", c.Kind, c.Parts[i-1].Name, p.Name)
			}
		}
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if has := strings.Contains(string(raw), `"parts"`); has != (len(decl) > 0) {
			t.Errorf("%s: JSON parts key present = %v, want %v", c.Kind, has, len(decl) > 0)
		}
	}
	for k := range overlayPartShapes {
		if !seen[k] {
			t.Errorf("overlayPartShapes kind %s missing from OverlayCapabilities", k)
		}
	}
}

// TestOverlayCapabilities_MarkovConditionalParts: the templated and
// conditional Markov parts carry their condition.
func TestOverlayCapabilities_MarkovConditionalParts(t *testing.T) {
	t.Parallel()

	want := map[string]struct {
		shape types.OverlayShape
		when  string
	}{
		"absorption_time":  {types.OverlayShapeSeries, "params.absorbing"},
		"distribution_<n>": {types.OverlayShapeSeries, "params.initial"},
		"steady_state":     {types.OverlayShapeSeries, "irreducible aperiodic"},
		"step_<n>":         {types.OverlayShapeMatrix, "params.steps"},
	}
	for _, c := range OverlayCapabilities() {
		if c.Kind != types.OverlayKindMarkov {
			continue
		}
		if len(c.Parts) != len(want) {
			t.Fatalf("markov parts %+v, want %d", c.Parts, len(want))
		}
		for _, p := range c.Parts {
			w, ok := want[p.Name]
			if !ok || p.Shape != w.shape || (w.when == "") != (p.When == "") || !strings.Contains(p.When, w.when) {
				t.Errorf("markov part %+v, want %+v", p, w)
			}
		}
		return
	}
	t.Fatal("OVERLAY_MARKOV missing from OverlayCapabilities")
}
