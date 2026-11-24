package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/tools/citest/coverfloors"
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

// readReferenceCorpus returns a .claude/reference document and, when it is a
// split index, every slice under its same-named directory, concatenated. The
// documentation gates below check the reference docs (the long-form home of
// each contract), not CLAUDE.md, which carries only what every session needs.
func readReferenceCorpus(t *testing.T, name string) string {
	t.Helper()
	var b strings.Builder
	index := filepath.Join(".claude", "reference", name+".md")
	data, err := os.ReadFile(index)
	if err != nil {
		t.Fatalf("reading %s: %v", index, err)
	}
	b.Write(data)
	slices, _ := filepath.Glob(filepath.Join(".claude", "reference", name, "*.md"))
	for _, sl := range slices {
		if filepath.Base(sl) == "history.md" {
			continue
		}
		data, err := os.ReadFile(sl)
		if err != nil {
			t.Fatalf("reading %s: %v", sl, err)
		}
		b.WriteString("\n")
		b.Write(data)
	}
	return b.String()
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

// TestClaudeMdMentionsFormatVersion verifies that CLAUDE.md contains the
// current format_version value "1.1". This is a non-skippable CI gate.
func TestClaudeMdMentionsFormatVersion(t *testing.T) {
	t.Parallel()

	content := readClaudeMd(t)
	// The format version is "1.1" as defined in descriptor/envelope.go.
	if !strings.Contains(content, "1.1") {
		t.Error("CLAUDE.md does not contain the current format_version value \"1.1\"")
	}
}

// TestEnvVarsDocumented scans all Go source files for os.Getenv("PULSE_...")
// calls and verifies that each environment variable name appears in the
// "Environment variables" table of docs/src/library/options.md.
func TestEnvVarsDocumented(t *testing.T) {
	t.Parallel()

	const envDoc = "docs/src/library/options.md"
	raw, err := os.ReadFile(envDoc)
	if err != nil {
		t.Fatalf("reading %s: %v", envDoc, err)
	}
	content := string(raw)

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
			t.Errorf("%s does not document environment variable %s", envDoc, envVar)
		}
	}
}

// TestUpdateDemandTableCovers verifies that the Update Demand reference
// (.claude/reference/update-demand.md and its slices) has trigger rows for
// every registered component category and contract type.
func TestUpdateDemandTableCovers(t *testing.T) {
	t.Parallel()

	sectionLower := strings.ToLower(readReferenceCorpus(t, "update-demand"))

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
	for _, req := range required {
		if !strings.Contains(sectionLower, strings.ToLower(req)) {
			t.Errorf("the Update Demand reference does not mention %q", req)
		}
	}
}

// TestShardArchiveLayoutDocumented verifies that the byte-layout reference
// (.claude/reference/byte-layout.md and its slices) documents the shard
// archive variant: the zip magic `PK\x03\x04` dispatch and the reserved
// `_schema.pulse` entry name.
func TestShardArchiveLayoutDocumented(t *testing.T) {
	t.Parallel()

	content := readReferenceCorpus(t, "byte-layout")
	if !strings.Contains(content, `PK\x03\x04`) {
		t.Error("the byte-layout reference does not mention zip magic `PK\\x03\\x04`")
	}
	if !strings.Contains(content, "_schema.pulse") {
		t.Error("the byte-layout reference does not mention reserved entry `_schema.pulse`")
	}
}

func TestSkillsCoverShardingTopics(t *testing.T) {
	t.Parallel()

	schemaSkill, err := os.ReadFile(filepath.Join("internal", "skills", "cohort-schema-design.md"))
	if err != nil {
		t.Fatalf("reading skills/cohort-schema-design.md: %v", err)
	}
	if !strings.Contains(string(schemaSkill), "Sharded") {
		t.Error("skills/cohort-schema-design.md does not contain a `Sharded` section heading or callout")
	}
}

// TestPerPackageCoverageFloors validates the per-package coverage floor
// table, .github/coverage-floors.txt — the ONE source of the floors.
// Measurement is `citest cover-check` (make cover; CI's coverage job), which
// fails when a listed package is below its floor or absent from the
// profile; this gate keeps the table itself sound so a typo cannot
// silently drop a package from that check: every entry names a real Go
// package directory, every floor is in 0..100, no package is listed twice.
//
// Non-skippable CI gate.
func TestPerPackageCoverageFloors(t *testing.T) {
	t.Parallel()

	floors, err := coverfloors.Load(".")
	if err != nil {
		t.Fatalf("reading %s: %v", coverfloors.File, err)
	}
	for _, p := range coverfloors.Validate(".", floors) {
		t.Error(p)
	}
}

// claudeMdSizeCeiling is the maximum size of CLAUDE.md in bytes.
//
// CLAUDE.md is loaded into every session before any code is read, so it
// carries only what every session needs: the design principles, how to find
// a change's companions, the byte-layout and output-format invariants, and
// the index of .claude/reference/. Contract long form lives in the reference
// docs, which a session loads only when its change touches that surface.
//
// The file once grew to 244,155 bytes because every Update Demand row said
// "update CLAUDE.md". It is no longer a companion; the ceiling stops it
// drifting back. Lowering the ceiling is allowed; raising it is not.
const claudeMdSizeCeiling = 16_000

func TestClaudeMdSizeBudget(t *testing.T) {
	t.Parallel()

	size := len(readClaudeMd(t))
	if size > claudeMdSizeCeiling {
		t.Errorf("CLAUDE.md is %d bytes, over the %d-byte ceiling by %d. "+
			"Do NOT raise the ceiling: move long-form prose into .claude/reference/ "+
			"(see CLAUDE.md's \"Reference Docs\" index) and leave a pointer plus the "+
			"always-load half behind. CLAUDE.md is loaded into every session's context; "+
			"a reference file is loaded only when the work requires it.",
			size, claudeMdSizeCeiling, size-claudeMdSizeCeiling)
	}
}
