package descriptor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readReferenceDoc returns .claude/reference/<name>.md plus, for a split
// index, every slice under .claude/reference/<name>/ (history.md excluded),
// concatenated. The test working directory is internal/descriptor, so the
// repo root is two levels up.
func readReferenceDoc(t *testing.T, name string) string {
	t.Helper()
	root := filepath.Join("..", "..", ".claude", "reference")
	data, err := os.ReadFile(filepath.Join(root, name+".md"))
	if err != nil {
		t.Fatalf("reading %s.md: %v", name, err)
	}
	var b strings.Builder
	b.Write(data)
	slices, _ := filepath.Glob(filepath.Join(root, name, "*.md"))
	for _, sl := range slices {
		if filepath.Base(sl) == "history.md" {
			continue
		}
		raw, err := os.ReadFile(sl)
		if err != nil {
			t.Fatalf("reading %s: %v", sl, err)
		}
		b.WriteString("\n")
		b.Write(raw)
	}
	return b.String()
}

// TestComponentsContractDocumented pins the Response.Components contract in
// its long-form home, .claude/reference/response-components.md.
func TestComponentsContractDocumented(t *testing.T) {
	t.Parallel()

	content := readReferenceDoc(t, "response-components")

	// Response.Components (the struct name; reachable as a section
	// anchor too).
	if !strings.Contains(content, "Response.Components") {
		t.Error("the response-components reference does not mention `Response.Components` — it must surface the new struct name")
	}

	// Universal floor — accept either the bullet form or the typed
	// floor list `{n, n_null}`. "universal floor" alone is also a
	// match; embedders grep for either spelling.
	hasFloorPhrase := strings.Contains(content, "universal floor")
	hasFloorTriple := strings.Contains(content, "{n, n_null}")
	if !hasFloorPhrase && !hasFloorTriple {
		t.Error("the response-components reference does not mention the universal floor (`universal floor` phrase or `{n, n_null}` literal) — it must document the aggregator floor")
	}

	// ComponentSchema — both the type name and the manifest projection
	// key (`components_schemas`) are common entry points. Either
	// suffices; both is ideal but a single mention covers the contract.
	hasSchemaType := strings.Contains(content, "ComponentSchema")
	hasSchemaBlock := strings.Contains(content, "components_schemas")
	hasManifestProjection := strings.Contains(content, "ComponentsSchemas")
	if !hasSchemaType && !hasSchemaBlock && !hasManifestProjection {
		t.Error("the response-components reference does not mention `ComponentSchema` or `components_schemas` — the per-operator schema surface must be documented")
	}

	// Coexistence between Metadata and Components.Run — embedders must
	// not mistake one for the other. Accept either an explicit
	// "Metadata" + "Run" pairing in the Response.Components paragraph,
	// or a "Coexists" / "coexists" phrasing.
	hasCoexists := strings.Contains(content, "Coexists with `Response.Metadata`") ||
		strings.Contains(content, "coexists with `Response.Metadata`") ||
		strings.Contains(content, "Coexists with Response.Metadata") ||
		strings.Contains(content, "coexists with Response.Metadata")
	if !hasCoexists {
		t.Error("CLAUDE.md does not document the Metadata / Components.Run coexistence note — embedders must see that Response.Metadata stays alongside Response.Components.Run, not replaced by it")
	}
}

func TestUpdateDemandTableCoversComponents(t *testing.T) {
	t.Parallel()

	section := readReferenceDoc(t, "update-demand")

	// Row 1: Response.Components shape change. Identifying phrase:
	// "Response.Components" must appear in the table (the row's
	// "Change" column). The enforcing gate name pins the row in place.
	if !strings.Contains(section, "Response.Components") {
		t.Error("Update Demand table does not mention `Response.Components` shape — required row for the new Response slot")
	}

	// Row 2: Per-operator ComponentSchema change. Identifying phrase:
	// "ComponentSchema" plus the per-operator framing. Both
	// `ComponentSchema` and `internal/descriptor/capabilities_*.go` should
	// appear in the row body.
	hasSchemaToken := strings.Contains(section, "ComponentSchema")
	hasCapabilitiesToken := strings.Contains(section, "descriptor/capabilities_") ||
		strings.Contains(section, "capabilities_*.go")
	if !hasSchemaToken {
		t.Error("Update Demand table does not mention `ComponentSchema` — required row for per-operator schema changes")
	}
	if !hasCapabilitiesToken {
		t.Error("Update Demand table does not mention the descriptor capability files (`descriptor/capabilities_*.go` or similar) for the per-operator ComponentSchema row")
	}

	// Row 3: Extension registration ComponentSchema. The row's load-
	// bearing phrase pairs "Extension" with "ComponentSchema". Accept
	// either "Extension registration" or just "Extension" near
	// "ComponentSchema"; the discriminator from row 2 is the
	// extension framing.
	hasExtensionRow := strings.Contains(section, "Extension registration `ComponentSchema`") ||
		strings.Contains(section, "Extension registration ComponentSchema") ||
		(strings.Contains(section, "Extension") && strings.Contains(section, "docs/src/internals/extension-points.md"))
	if !hasExtensionRow {
		t.Error("Update Demand table does not mention the Extension registration ComponentSchema row (extension surface must trigger a skill + Update Demand update)")
	}
}
