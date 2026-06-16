package descriptor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeMdPath returns the absolute-ish path to CLAUDE.md from the
// descriptor package's working directory. Go test working directory is
// the package directory, so CLAUDE.md sits one level up.
func claudeMdPath() string {
	return filepath.Join("..", "CLAUDE.md")
}

// readClaudeMdForComponents reads CLAUDE.md from the repo root via
// `../CLAUDE.md`. Mirrors the hygiene_test.go helper at the repo root
// (which uses os.ReadFile("CLAUDE.md") from the root pulse package).
func readClaudeMdForComponents(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(claudeMdPath())
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	return string(data)
}

func TestClaudeMdMentionsComponentsContract(t *testing.T) {
	content := readClaudeMdForComponents(t)

	// Response.Components (the struct name; reachable as a section
	// anchor too).
	if !strings.Contains(content, "Response.Components") {
		t.Error("CLAUDE.md does not mention `Response.Components` — the Output Format Contract section must surface the new struct name")
	}

	// Universal floor — accept either the bullet form or the typed
	// floor list `{n, n_null}`. "universal floor" alone is also a
	// match; embedders grep for either spelling.
	hasFloorPhrase := strings.Contains(content, "universal floor")
	hasFloorTriple := strings.Contains(content, "{n, n_null}")
	if !hasFloorPhrase && !hasFloorTriple {
		t.Error("CLAUDE.md does not mention the universal floor (`universal floor` phrase or `{n, n_null}` literal) — the Output Format Contract section must document the aggregator floor")
	}

	// ComponentSchema — both the type name and the manifest projection
	// key (`components_schemas`) are common entry points. Either
	// suffices; both is ideal but a single mention covers the contract.
	hasSchemaType := strings.Contains(content, "ComponentSchema")
	hasSchemaBlock := strings.Contains(content, "components_schemas")
	hasManifestProjection := strings.Contains(content, "ComponentsSchemas")
	if !hasSchemaType && !hasSchemaBlock && !hasManifestProjection {
		t.Error("CLAUDE.md does not mention `ComponentSchema` or `components_schemas` — the per-operator schema surface must be reachable from CLAUDE.md")
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
	content := readClaudeMdForComponents(t)

	// Extract the Update Demand section (from "## The Update Demand"
	// to the next "##"). Mirrors the extraction logic in
	// hygiene_test.go's TestUpdateDemandTableCovers.
	start := strings.Index(content, "## The Update Demand")
	if start < 0 {
		t.Fatal("CLAUDE.md does not contain '## The Update Demand' section")
	}
	rest := content[start+len("## The Update Demand"):]
	end := strings.Index(rest, "\n## ")
	var section string
	if end < 0 {
		section = rest
	} else {
		section = rest[:end]
	}

	// Row 1: Response.Components shape change. Identifying phrase:
	// "Response.Components" must appear in the table (the row's
	// "Change" column). The enforcing gate name pins the row in place.
	if !strings.Contains(section, "Response.Components") {
		t.Error("Update Demand table does not mention `Response.Components` shape — required row for the new Response slot")
	}

	// Row 2: Per-operator ComponentSchema change. Identifying phrase:
	// "ComponentSchema" plus the per-operator framing. Both
	// `ComponentSchema` and `descriptor/capabilities_*.go` should
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
