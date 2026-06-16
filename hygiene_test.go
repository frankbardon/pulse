package pulse_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readClaudeMd reads CLAUDE.md from the repo root. It locates the repo root
// via the working directory (tests run from the package directory).
func readClaudeMd(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatalf("reading CLAUDE.md: %v", err)
	}
	return string(data)
}

// findGoFiles walks the directory tree and returns all .go file paths.
func findGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && (info.Name() == ".git" || info.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking directory tree: %v", err)
	}
	return files
}

// findGoTestFiles walks the directory tree and returns all _test.go file paths.
func findGoTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && (info.Name() == ".git" || info.Name() == "vendor") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking directory tree: %v", err)
	}
	return files
}

// TestClaudeMdMentionsFormatVersion verifies that CLAUDE.md contains the
// current format_version value "1.0". This is a non-skippable CI gate.
func TestClaudeMdMentionsFormatVersion(t *testing.T) {
	content := readClaudeMd(t)
	// The format version is "1.0" as defined in descriptor/envelope.go.
	if !strings.Contains(content, "1.0") {
		t.Error("CLAUDE.md does not contain the current format_version value \"1.0\"")
	}
}

// TestClaudeMdMentionsAllEnvVars scans all Go source files for os.Getenv("PULSE_...")
// calls and verifies that each environment variable name appears in CLAUDE.md.
// This is a non-skippable CI gate.
func TestClaudeMdMentionsAllEnvVars(t *testing.T) {
	content := readClaudeMd(t)

	// Find all Go files in the repo using find (works even if files are untracked).
	goFiles := findGoFiles(t, ".")

	// Match both direct os.Getenv("PULSE_...") and string constants/literals "PULSE_..."
	// that are used as env var names.
	directPattern := regexp.MustCompile(`os\.Getenv\("(PULSE_[A-Z_]+)"\)`)
	// Also match constants assigned PULSE_ string values (e.g., const envDataDir = "PULSE_DATA_DIR").
	constPattern := regexp.MustCompile(`=\s*"(PULSE_[A-Z_]+)"`)
	seen := make(map[string]bool)

	for _, f := range goFiles {
		if strings.HasSuffix(f, "_test.go") {
			continue // Skip test files.
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		src := string(data)
		for _, m := range directPattern.FindAllStringSubmatch(src, -1) {
			seen[m[1]] = true
		}
		// Only count const-assigned PULSE_ strings if the file also uses os.Getenv.
		if strings.Contains(src, "os.Getenv") {
			for _, m := range constPattern.FindAllStringSubmatch(src, -1) {
				seen[m[1]] = true
			}
		}
	}

	if len(seen) == 0 {
		t.Fatal("found no PULSE_* environment variables in Go source; expected at least PULSE_DATA_DIR")
	}

	for envVar := range seen {
		if !strings.Contains(content, envVar) {
			t.Errorf("CLAUDE.md does not mention environment variable %s", envVar)
		}
	}
}

// TestClaudeMdMentionsAllNonSkippableGates verifies that every non-skippable
// CI gate test name appears in CLAUDE.md. The gate prefixes identify tests that
// are part of the CI contract and must be documented.
// This is a non-skippable CI gate.
func TestClaudeMdMentionsAllNonSkippableGates(t *testing.T) {
	content := readClaudeMd(t)

	// Collect all test function names matching the non-skippable gate prefixes.
	prefixes := []string{
		"TestSkillsCover",
		"TestClaudeMd",
		"TestUpdateDemand",
		"TestNoOrbit",
		"TestGoldensNot",
		"TestPredictNo",
		"TestDescriptorNo",
		"TestPerPackageCoverage",
	}

	// Find all Go test files using find (works even if files are untracked).
	testFiles := findGoTestFiles(t, ".")

	testFuncPattern := regexp.MustCompile(`func (Test\w+)\(`)
	var gates []string

	for _, f := range testFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		matches := testFuncPattern.FindAllStringSubmatch(string(data), -1)
		for _, m := range matches {
			name := m[1]
			for _, prefix := range prefixes {
				if strings.HasPrefix(name, prefix) {
					gates = append(gates, name)
					break
				}
			}
		}
	}

	if len(gates) == 0 {
		t.Fatal("found no non-skippable gate test functions")
	}

	for _, gate := range gates {
		if !strings.Contains(content, gate) {
			t.Errorf("CLAUDE.md does not mention non-skippable gate %s", gate)
		}
	}
}

// TestUpdateDemandTableCovers verifies that the Update Demand table in CLAUDE.md
// has rows covering every registered component category and contract type.
// This is a non-skippable CI gate.
func TestUpdateDemandTableCovers(t *testing.T) {
	content := readClaudeMd(t)

	// These are the component categories and contract types that must appear
	// in the Update Demand table. Each entry is checked case-insensitively.
	//
	// The "CLI leaf" row was removed in the skill-pack overhaul (E6-S1) —
	// CLI is out of scope for the atomic-skill convention, and the
	// per-leaf documentation moved to docs/src/cli/. The check is dropped
	// here so the table no longer carries a CLI leaf row.
	required := []string{
		"aggregator",
		"attribute",
		"filterer",
		"grouper",
		"error code",
		"format_version",
		"field type",
		"CI gate",
		"environment variable",
		"MCP tool",
		"feature operator",
	}

	// Extract the Update Demand section (from "## The Update Demand" to the next "##").
	start := strings.Index(content, "## The Update Demand")
	if start < 0 {
		t.Fatal("CLAUDE.md does not contain '## The Update Demand' section")
	}
	// Find the next ## heading after the Update Demand section.
	rest := content[start+len("## The Update Demand"):]
	end := strings.Index(rest, "\n## ")
	var section string
	if end < 0 {
		section = rest
	} else {
		section = rest[:end]
	}
	sectionLower := strings.ToLower(section)

	for _, req := range required {
		if !strings.Contains(sectionLower, strings.ToLower(req)) {
			t.Errorf("Update Demand table in CLAUDE.md does not mention %q", req)
		}
	}
}

// TestShardArchiveLayoutDocumented verifies that CLAUDE.md's
// "Byte-layout invariants" section documents the shard archive variant
// — specifically the zip magic `PK\x03\x04` dispatch and the reserved
// `_schema.pulse` entry name. Non-skippable CI gate (Update Demand row
// for shard archive layout enforces this).
func TestShardArchiveLayoutDocumented(t *testing.T) {
	content := readClaudeMd(t)

	start := strings.Index(content, "### Byte-layout invariants")
	if start < 0 {
		t.Fatal("CLAUDE.md does not contain '### Byte-layout invariants' section")
	}
	rest := content[start:]
	// Section ends at the next "### " heading (the next subsection of
	// "## Code Conventions") or the next "## " heading.
	end := len(rest)
	if idx := strings.Index(rest[len("### Byte-layout invariants"):], "\n### "); idx >= 0 {
		end = len("### Byte-layout invariants") + idx
	}
	if idx := strings.Index(rest[len("### Byte-layout invariants"):], "\n## "); idx >= 0 && len("### Byte-layout invariants")+idx < end {
		end = len("### Byte-layout invariants") + idx
	}
	section := rest[:end]

	// The literal magic `PK\x03\x04` must appear (the four-byte zip
	// magic — also expressible as the printable bytes "PK" plus two
	// control bytes; we accept either the escaped-byte form or the
	// literal "PK\x03\x04" string).
	if !strings.Contains(section, `PK\x03\x04`) {
		t.Error("CLAUDE.md 'Byte-layout invariants' section does not mention zip magic `PK\\x03\\x04`")
	}
	if !strings.Contains(section, "_schema.pulse") {
		t.Error("CLAUDE.md 'Byte-layout invariants' section does not mention reserved entry `_schema.pulse`")
	}
}

// TestSkillsCoverShardingTopics verifies that the cohort-schema-design
// skill carries a "Sharded" section. Non-skippable CI gate (Update
// Demand row for shard archive layout enforces this).
//
// Post-E4 simplification: the legacy monolithic contributor skill was
// retired in the skill-pack overhaul (E3-S2 split it into topical
// skills); the load-bearing assertion is now on cohort-schema-design.md
// alone, which carries the canonical sharding section preserved across
// the rewrite.
func TestSkillsCoverShardingTopics(t *testing.T) {
	schemaSkill, err := os.ReadFile(filepath.Join("skills", "cohort-schema-design.md"))
	if err != nil {
		t.Fatalf("reading skills/cohort-schema-design.md: %v", err)
	}
	if !strings.Contains(string(schemaSkill), "Sharded") {
		t.Error("skills/cohort-schema-design.md does not contain a `Sharded` section heading or callout")
	}
}

// TestPerPackageCoverageFloors is a placeholder that documents the target
// per-package coverage floors. Full coverage profiling infrastructure is not
// yet implemented; this test serves as documentation and a reminder.
//
// Target floors:
//
//	encoding/    >= 95%
//	descriptor/  >= 95%
//	skills/      >= 95%
//	errors/      >= 90%
//	processing/  >= 85%
//	io/csv/      >= 85%
//	io/tsv/      >= 85%
//	io/ndjson/   >= 85%
//	io/parquet/  >= 85%
//	io/excel/    >= 85%
//	service/     >= 80%
//	cmd/pulse/   >= 75%
//
// Aggregate across core packages >= 80%.
func TestPerPackageCoverageFloors(t *testing.T) {
	// Document the expected coverage floors. This test passes unconditionally
	// as a placeholder until coverage profiling infrastructure is added.
	floors := map[string]int{
		"encoding":   95,
		"descriptor": 95,
		"skills":     95,
		"errors":     90,
		"processing": 85,
		"io/csv":     85,
		"io/tsv":     85,
		"io/ndjson":  85,
		"io/parquet": 85,
		"io/excel":   85,
		"service":    80,
		"cmd/pulse":  75,
	}

	// Verify the floor map is non-empty (structural sanity check).
	if len(floors) != 12 {
		t.Errorf("expected 12 coverage floor entries, got %d", len(floors))
	}

	// Verify each package directory exists.
	for pkg := range floors {
		path := filepath.Join(".", pkg)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("package directory %s does not exist", pkg)
		}
	}

	t.Log("Per-package coverage floor enforcement is a placeholder. " +
		"Run 'make cover' to check coverage manually.")
}
