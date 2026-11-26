package skills

import (
	"strings"
	"testing"
)

// TestSkillRoutes_Pinned pins discovery routes an agent reading cold
// needs and that a budget trim could silently drop: session-bootstrap
// names the CLI intent filter beside the MCP one (#505), and
// compose-requests routes to compose-optimize from its `## See` section,
// the section the ontology reads as routes_to edges (#557).
func TestSkillRoutes_Pinned(t *testing.T) {
	t.Parallel()

	boot, ok := Get("session-bootstrap")
	if !ok {
		t.Fatal("session-bootstrap not loadable")
	}
	if !strings.Contains(boot, "pulse examples search --intent") {
		t.Error("session-bootstrap: the CLI `pulse examples search --intent` filter is not named")
	}
	if !strings.Contains(boot, "`intent`") {
		t.Error("session-bootstrap: the `pulse_examples_search` `intent` argument is not named")
	}

	compose, ok := Get("compose-requests")
	if !ok {
		t.Fatal("compose-requests not loadable")
	}
	_, see, found := strings.Cut(compose, "\n## See\n")
	if !found {
		t.Fatal("compose-requests has no ## See section")
	}
	if !strings.Contains(see, "`compose-optimize`") {
		t.Error("compose-requests: ## See does not route to `compose-optimize`")
	}
}
