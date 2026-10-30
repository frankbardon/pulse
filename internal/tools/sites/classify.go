package main

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

var updateFlagRe = regexp.MustCompile(`flag\.Bool\("(update[\w-]*)"`)

// siteClass is one path category of the checklist. The table order is
// the display order; classify decides membership.
type siteClass struct {
	ID    string
	Title string
	Hint  string
	// Gates are suites that check a site of this class but do not name
	// the operator (structure, budget, generated-tree gates). Each name
	// is resolved to its package(s) at run time and pinned to exist by
	// TestStaticGatesExist.
	Gates []string
	// Edit is false for classes that are not edited by hand (tests,
	// generated trees, goldens, history).
	Edit bool
}

var classTable = []siteClass{
	{ID: "types", Title: "Wire types + registry declaration (types/)", Edit: true,
		Hint:  "the typed constant, its All*() registry and per-type tables (Streamable, Mergeable, …); a new exported symbol regenerates the API golden",
		Gates: []string{"TestPublicAPIGolden"}},
	{ID: "registry", Title: "Engine factory registry (internal/processing/registry.go)", Edit: true,
		Hint: "the factory map entry"},
	{ID: "engine", Title: "Engine (internal/processing/)", Edit: true,
		Hint: "implementation, dispatch switches, finalizer tables"},
	{ID: "vectors", Title: "Virtual vectors / matrix rules (internal/vectors/)", Edit: true},
	{ID: "service", Title: "Orchestration (internal/service/, internal/model/)", Edit: true},
	{ID: "weighting", Title: "Weighting (weight classes, resolution, refusals)", Edit: true,
		Hint: "weight class / refusal for the operator — load .claude/reference/weighting.md"},
	{ID: "multiplicity", Title: "Multiplicity (p-value families)", Edit: true,
		Hint: "only if the operator emits p-values — load update-demand/weighting-and-multiplicity.md"},
	{ID: "capabilities", Title: "Manifest capability declaration (internal/descriptor/capabilities_*.go)", Edit: true,
		Hint: "params, outputs, ComponentSchema, streamability; regenerates the manifest + payload-schema goldens"},
	{ID: "features", Title: "Feature table (internal/descriptor/features.go)", Edit: true,
		Hint:  "op(...) row with Since + dependency edges — load .claude/reference/feature-profiles.md",
		Gates: []string{"TestFeaturesHaveSince", "TestProfileDependenciesComplete"}},
	{ID: "guidance", Title: "Guidance: Purpose / Interpretation / intents (internal/descriptor/)", Edit: true,
		Hint:  "builtinPurposes / builtinInterpretations entries, AlternativeTo routing — load .claude/reference/guided-analysis.md; regenerate docs/src/guide",
		Gates: []string{"TestSkillsCoverAllPurposes", "TestInterpretationCoversOutputs", "TestManifestGuidanceBudget"}},
	{ID: "error-owners", Title: "Error-code owners (internal/descriptor/error_owners.go)", Edit: true,
		Gates: []string{"TestErrorOwners_Complete"}},
	{ID: "descriptor", Title: "Predict / manifest / no-execute descriptor (internal/descriptor/, descriptor/)", Edit: true,
		Hint:  "predict parity with runtime; never import internal/service or internal/processing",
		Gates: []string{"TestPredictNoExecutionImports"}},
	{ID: "root", Title: "Public facade + extensions (root package)", Edit: true},
	{ID: "linalg", Title: "Kernels (linalg/)", Edit: true,
		Hint: "FMA-free kernels and the metric registry — load .claude/reference/matrix-and-vectors.md"},
	{ID: "extend", Title: "Extension authoring API (extend/)", Edit: true},
	{ID: "mcp", Title: "MCP (internal/mcp/, mcp/, mcpserve/)", Edit: true},
	{ID: "cli", Title: "CLI (cmd/, internal/cli/)", Edit: true},
	{ID: "errors", Title: "Error codes (errors/)", Edit: true,
		Gates: []string{"TestCodesHaveFixups", "TestManifestErrorCodesComplete"}},
	{ID: "other-go", Title: "Other Go packages", Edit: true},
	{ID: "skill-atomic", Title: "Atomic skill (internal/skills/op-*|tool-*|type-*.md)", Edit: true,
		Hint:  "required sections + token budget — load .claude/reference/skill-pack.md",
		Gates: []string{"TestOperatorHasAtomicSkill", "TestAtomicSkillHasRequiredSections", "TestSkillTokenBudget", "TestSkillsCoverFeatureFences"}},
	{ID: "skill-topical", Title: "Topical skills (internal/skills/*.md)", Edit: true,
		Gates: []string{"TestSkillTokenBudget", "TestSkillsCoverAllCrossReferences"}},
	{ID: "examples", Title: "Examples (internal/examples/ — _meta.operators tags)", Edit: true,
		Gates: []string{"TestEveryOperatorHasAnExampleTag"}},
	{ID: "docs", Title: "Book, hand-written (docs/src/, not guide/)", Edit: true},
	{ID: "reference", Title: "Contract prose (.claude/reference/)", Edit: true,
		Gates: []string{"TestSkillsCoverAllCrossReferences"}},
	{ID: "claude-md", Title: "CLAUDE.md / README.md", Edit: true,
		Gates: []string{"TestClaudeMdSizeBudget", "TestUpdateDemandTableCovers"}},
	{ID: "other", Title: "Other files (scripts, workflows, top-level examples)", Edit: true},
	{ID: "test", Title: "Tests that name it (rerun; extend the table-driven ones)"},
	{ID: "golden", Title: "Goldens / fixtures (testdata/) — regenerate, never hand-edit"},
	{ID: "docs-generated", Title: "Generated Analysis Guide (docs/src/guide/) — `make docs-generate`, never hand-edit",
		Gates: []string{"TestDocsGeneratedCurrent"}},
	{ID: "history", Title: "History (docs/roadmap/, CHANGELOG) — informational, not a site"},
}

func classByID(id string) siteClass {
	for _, c := range classTable {
		if c.ID == id {
			return c
		}
	}
	panic("unknown class " + id)
}

func classOrder(id string) int {
	for i, c := range classTable {
		if c.ID == id {
			return i
		}
	}
	return len(classTable)
}

// classify maps a repo-relative path to its class ID.
func classify(p string) string {
	base := path.Base(p)
	dir := path.Dir(p)
	isGo := strings.HasSuffix(p, ".go")
	switch {
	case strings.HasPrefix(p, "docs/roadmap/") || strings.HasPrefix(base, "CHANGELOG"):
		return "history"
	case strings.HasSuffix(p, "_test.go"):
		return "test"
	case strings.Contains(p, "/testdata/") || strings.HasPrefix(p, "testdata/"):
		return "golden"
	case strings.HasPrefix(p, "docs/src/guide/") || p == "docs/src/SUMMARY.md":
		return "docs-generated"
	case strings.HasPrefix(p, "docs/"):
		return "docs"
	case strings.HasPrefix(p, ".claude/reference/"):
		return "reference"
	case p == "CLAUDE.md" || p == "README.md":
		return "claude-md"
	case strings.HasPrefix(p, "internal/skills/") && strings.HasSuffix(p, ".md"):
		if strings.HasPrefix(base, "op-") || strings.HasPrefix(base, "tool-") || strings.HasPrefix(base, "type-") {
			return "skill-atomic"
		}
		return "skill-topical"
	case strings.HasPrefix(p, "internal/examples/"):
		return "examples"
	case !isGo:
		return "other"
	case strings.HasPrefix(p, "types/"):
		return "types"
	case p == "internal/processing/registry.go":
		return "registry"
	case strings.Contains(base, "multiplicity"):
		return "multiplicity"
	case strings.HasPrefix(p, "internal/weighting/") || strings.HasPrefix(base, "weight"):
		return "weighting"
	case strings.HasPrefix(p, "internal/processing/"):
		return "engine"
	case strings.HasPrefix(p, "internal/vectors/"):
		return "vectors"
	case strings.HasPrefix(p, "internal/service/") || strings.HasPrefix(p, "internal/model/"):
		return "service"
	case strings.HasPrefix(p, "internal/descriptor/capabilities"):
		return "capabilities"
	case p == "internal/descriptor/features.go":
		return "features"
	case strings.HasPrefix(p, "internal/descriptor/") && (strings.HasPrefix(base, "purposes") ||
		strings.HasPrefix(base, "interpretations") || strings.HasPrefix(base, "intents") ||
		strings.HasPrefix(base, "glossary") || strings.HasPrefix(base, "ontology") ||
		strings.HasPrefix(base, "guidance") || strings.HasPrefix(base, "effect_size")):
		return "guidance"
	case p == "internal/descriptor/error_owners.go":
		return "error-owners"
	case strings.HasPrefix(p, "internal/descriptor/") || strings.HasPrefix(p, "descriptor/"):
		return "descriptor"
	case dir == ".":
		return "root"
	case strings.HasPrefix(p, "linalg/"):
		return "linalg"
	case strings.HasPrefix(p, "extend/"):
		return "extend"
	case strings.HasPrefix(p, "internal/mcp/") || strings.HasPrefix(p, "mcp/") || strings.HasPrefix(p, "mcpserve/"):
		return "mcp"
	case strings.HasPrefix(p, "cmd/") || strings.HasPrefix(p, "internal/cli/"):
		return "cli"
	case strings.HasPrefix(p, "errors/"):
		return "errors"
	}
	return "other-go"
}

// testdataOwners maps a testdata path prefix to the package directory
// whose tests regenerate it, for testdata that does not sit beside its
// writer. Longest prefix first. The root testdata/ and the story goldens
// are written by the black-box suite in internal/integration (it runs from
// the repository root); testdata/sharding has its own generators.
var testdataOwners = []struct{ prefix, dir string }{
	{"testdata/sharding/two_shards.inspect.json", "internal/cli"},
	{"testdata/sharding/two_shards.process.json", "internal/cli"},
	{"testdata/sharding/", "internal/shardfixtures"},
	{"internal/examples/testdata/", "internal/integration"},
	{"testdata/", "internal/integration"},
}

// testdataOwner returns the package directory whose tests regenerate
// testdata path p: an explicit testdataOwners entry, else the directory
// holding the testdata/ tree (the package beside it).
func testdataOwner(p string) string {
	for _, o := range testdataOwners {
		if strings.HasPrefix(p, o.prefix) {
			return o.dir
		}
	}
	if i := strings.Index(p, "/testdata/"); i >= 0 {
		return p[:i]
	}
	return "."
}

// regenFor derives the regeneration command for a golden / generated
// path: the testdata's owning package, with -update when that
// package's tests declare the flag.
func (r *repo) regenFor(p string) (cmd, note string) {
	switch {
	case strings.HasPrefix(p, "docs/src/guide/") || p == "docs/src/SUMMARY.md":
		return "make docs-generate", "TestDocsGeneratedCurrent fails until the tree is regenerated"
	case strings.HasPrefix(p, "internal/processing/testdata/reference/"):
		return "make reference", "R oracle goldens (needs Rscript; CI never runs R)"
	}
	switch path.Ext(p) {
	case ".py", ".R", ".md", ".sh":
		return "", "hand-authored fixture / generator source: edit only if the input itself changes"
	}
	dir := testdataOwner(p)
	var flags []string
	for _, f := range r.files {
		if path.Dir(f) == dir && strings.HasSuffix(f, "_test.go") {
			for _, m := range updateFlagRe.FindAllStringSubmatch(string(r.read(f)), -1) {
				if !contains(flags, m[1]) {
					flags = append(flags, m[1])
				}
			}
		}
	}
	if len(flags) > 0 {
		sort.Strings(flags)
		if len(flags) > 1 {
			note = "package update flags: -" + strings.Join(flags, ", -") + " — pick the one whose test reads the file"
		}
		return goTestCmd(dir, nil) + " -" + flags[0], note
	}
	return "", "hand-authored fixture (no -update flag in " + dir + "): edit only if the input itself changes"
}
