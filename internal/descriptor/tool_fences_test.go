package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestToolDescriptionFences_Valid: every MCP tool description's feature
// fences parse with the skill-pack grammar and name built-in features,
// so no description is served with its markers (RenderProseFences falls
// back to the raw text on a malformed one), and the lead sentence the
// tool skill mirrors carries no fence.
func TestToolDescriptionFences_Valid(t *testing.T) {
	t.Parallel()

	fenced := 0
	for _, m := range toolmeta.Meta() {
		if err := ValidateSkillFences(m.Description, nil); err != nil {
			t.Errorf("%s: %v", m.Name, err)
		}
		if lead, _, _ := strings.Cut(m.Description, ". "); strings.Contains(lead, "<!--") {
			t.Errorf("%s: the lead sentence carries a feature fence", m.Name)
		}
		if strings.Contains(m.Description, "<!--") {
			fenced++
		}
	}
	if fenced == 0 {
		t.Fatal("no tool description carries a feature fence: vacuous")
	}
}

// TestToolDescriptionFences_Manifest (#238): the manifest renders each
// tool description's fences for the instance before the prose scrub,
// so a matrices-hidden instance keeps the sentence a matrix clause sits
// in, while the unscoped manifest serves the fence-free full text.
func TestToolDescriptionFences_Manifest(t *testing.T) {
	t.Parallel()

	descs := func(inst *InstanceSnapshot) map[string]string {
		out := map[string]string{}
		for _, tool := range BuildManifestForInstance(inst).MCPTools {
			out[tool.Name] = tool.Description
		}
		return out
	}
	raw := map[string]string{}
	for _, m := range toolmeta.Meta() {
		raw[m.Name] = m.Description
	}

	full := descs(nil)
	for name, d := range full {
		if d != RenderProseFences(raw[name], nil) || strings.Contains(d, "<!--") {
			t.Errorf("unscoped manifest: %s is not the fence-free full text", name)
		}
	}

	noMat := descs(hideOnly(featMatrices))
	for _, c := range []struct{ tool, keep string }{
		{toolmeta.ToolExamplesSearch, "Filters, all optional and ANDed"},
		{toolmeta.ToolManifest, "Carries per-operator params"},
	} {
		if !strings.Contains(full[c.tool], c.keep) || !strings.Contains(full[c.tool], "`matrices`") {
			t.Fatalf("premise: the full %s names `matrices` beside %q", c.tool, c.keep)
		}
		got := noMat[c.tool]
		if !strings.Contains(got, c.keep) {
			t.Errorf("matrices hidden: %s lost the sentence %q:\n%s", c.tool, c.keep, got)
		}
		if strings.Contains(got, "`matrices`") || strings.Contains(got, "`vectors`") {
			t.Errorf("matrices hidden: %s still names a matrix token:\n%s", c.tool, got)
		}
	}
}
