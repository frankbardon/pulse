package main

import (
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
)

var (
	testRepoOnce sync.Once
	testRepo     *repo
	testRepoErr  error
)

// sharedRepo opens the real tree once: the tool's whole job is to read
// it, so the tests run against it rather than a fixture.
func sharedRepo(t *testing.T) *repo {
	t.Helper()
	testRepoOnce.Do(func() { testRepo, testRepoErr = openRepo("") })
	if testRepoErr != nil {
		t.Fatal(testRepoErr)
	}
	return testRepo
}

// reportFiles is every site path a report lists that names the
// operator (the enumerator section lists registry callers instead).
func reportFiles(rep *Report) map[string]bool {
	out := map[string]bool{}
	for _, s := range rep.Sections {
		switch s.Class {
		case "enumerators", "derived", "param-enums", "oracle":
			continue // derived from the named sites; they need not name the operator
		}
		for _, site := range s.Sites {
			out[site.Path] = true
		}
	}
	return out
}

func findSite(rep *Report, p string) *Site {
	for i := range rep.Sections {
		for j := range rep.Sections[i].Sites {
			if rep.Sections[i].Sites[j].Path == p {
				return &rep.Sections[i].Sites[j]
			}
		}
	}
	return nil
}

func hasSymbol(s *Site, name string) bool {
	if s == nil {
		return false
	}
	for _, sym := range s.Symbols {
		if sym.Name == name {
			return true
		}
	}
	return false
}

func gateTests(rep *Report) map[string]bool {
	out := map[string]bool{}
	for _, g := range rep.Gates {
		for _, t := range g.Tests {
			out[t] = true
		}
	}
	return out
}

func gitGrep(t *testing.T, root string, args ...string) map[string]bool {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return map[string]bool{}
		}
		t.Fatal(err)
	}
	set := map[string]bool{}
	for _, p := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if p != "" && !skipPath(p) {
			if _, err := os.Stat(path.Join(root, p)); err == nil {
				set[p] = true
			}
		}
	}
	return set
}

func TestScanBytes_TokenBoundaries(t *testing.T) {
	t.Parallel()

	m := &matcher{
		words:  map[string][]int{"MAT_PCA": {0}, "TEST_T": {1}},
		stems:  map[string][]int{"op-mat-pca": {0}},
		quoted: map[string][]int{"normal": {2}},
	}
	src := []byte("MAT_PCA MAT_PCA_X xMAT_PCA TEST_TREND TEST_T\n" +
		"op-mat-pca.md op-mat-pca-x skills/op-mat-pca\n" +
		`normal "normal" "normality" "normal x"` + "\n")
	var got []string
	for _, h := range scanBytes("f", src, m) {
		got = append(got, string(src[h.Off:h.Off+2])+":"+string(rune('0'+h.Line)))
	}
	want := []string{"MA:1", "TE:1", "no:3", "op:2", "op:2"}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("hits %v, want %v", got, want)
	}
}

func TestNormPath(t *testing.T) {
	t.Parallel()

	pca := newOperator(categoryDef{Prefix: "MAT_", Skill: "op-mat-"}, "MAT_PCA")
	pc := newOperator(categoryDef{Prefix: "ATTR_", Skill: "op-attr-"}, "ATTR_PC_SCORE")
	tt := newOperator(categoryDef{Prefix: "TEST_", Skill: "op-test-"}, "TEST_T")
	for _, c := range []struct {
		in   string
		op   operator
		want string
	}{
		{"internal/skills/op-mat-pca.md", pca, "internal/skills/op-mat-<op>.md"},
		{"internal/examples/matrices/09_pca.json", pca, "internal/examples/matrices/NN_<op>.json"},
		{"internal/processing/matrix_pca.go", pca, "internal/processing/matrix_<op>.go"},
		{"internal/processing/attribute_pc_score.go", pc, "internal/processing/attribute_<op>.go"},
		{"internal/skills/op-attr-pc-score.md", pc, "internal/skills/op-attr-<op>.md"},
		{"internal/processing/matrix.go", pca, "internal/processing/matrix.go"},
		{"internal/processing/test_trend.go", tt, "internal/processing/test_trend.go"},
		{"internal/skills/op-test-t.md", tt, "internal/skills/op-test-<op>.md"},
		{"internal/examples/tests/03_t.json", tt, "internal/examples/tests/NN_<op>.json"},
		{"internal/examples/tests/03_trend.json", tt, "internal/examples/tests/03_trend.json"},
	} {
		if got := normPath(c.in, c.op); got != c.want {
			t.Errorf("normPath(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	for p, want := range map[string]string{
		"types/matrix.go":                            "types",
		"types/matrix_test.go":                       "test",
		"internal/processing/registry.go":            "registry",
		"internal/processing/matrix.go":              "engine",
		"internal/descriptor/capabilities_matrix.go": "capabilities",
		"internal/descriptor/features.go":            "features",
		"internal/descriptor/purposes_matrices.go":   "guidance",
		"internal/descriptor/weight_resolve.go":      "weighting",
		"internal/service/multiplicity_overlay.go":   "multiplicity",
		"internal/descriptor/predict.go":             "descriptor",
		"descriptor/testdata/manifest.json":          "golden",
		"docs/src/guide/catalog/matrix.md":           "docs-generated",
		"docs/src/SUMMARY.md":                        "docs-generated",
		"docs/src/library/matrices.md":               "docs",
		"docs/roadmap/TODO.md":                       "history",
		"internal/skills/op-mat-pca.md":              "skill-atomic",
		"internal/skills/matrix-results.md":          "skill-topical",
		"internal/examples/matrices/09_pca.json":     "examples",
		".claude/reference/weighting.md":             "reference",
		"CLAUDE.md":                                  "claude-md",
		"pulse.go":                                   "root",
		"internal/mcp/bind.go":                       "mcp",
		"internal/overlayhost/overlayhost.go":        "other-go",
		"scripts/reference/gen_rotation.R":           "other",
	} {
		if got := classify(p); got != want {
			t.Errorf("classify(%s) = %s, want %s", p, got, want)
		}
	}
}

// TestStaticGatesExist pins every hand-named structural gate to a
// real test function, so a rename fails here instead of silently
// dropping out of the checklist.
func TestStaticGatesExist(t *testing.T) {
	t.Parallel()

	idx := sharedRepo(t).testIndex()
	for _, c := range classTable {
		for _, g := range c.Gates {
			if len(idx[g]) == 0 {
				t.Errorf("class %s names gate %s, which no *_test.go declares", c.ID, g)
			}
		}
	}
	for _, g := range []string{"TestPayloadSchemaGolden", "TestPublicAPIGolden", "TestUpdateDemandTableCovers"} {
		if len(idx[g]) == 0 {
			t.Errorf("slot gate %s missing", g)
		}
	}
}

// TestManualPathsExist pins the hand-kept reading lists to real files.
func TestManualPathsExist(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	var all []string
	for _, c := range categoryTable {
		all = append(all, manualFor(c)...)
	}
	all = append(all, slotManual...)
	all = append(all, slotManualResponse...)
	for _, p := range all {
		if _, err := os.Stat(path.Join(r.root, p)); err != nil {
			t.Errorf("manual path %s: %v", p, err)
		}
	}
}

// TestCategoriesLoad pins every registry to a non-empty operator list
// whose atomic skills exist (the stem convention the matcher relies on).
func TestCategoriesLoad(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	for _, def := range categoryTable {
		c, err := r.loadCategory(def)
		if err != nil {
			t.Fatalf("%s: %v", def.Name, err)
		}
		if len(c.Ops) < 3 {
			t.Errorf("%s: only %d ops", def.Name, len(c.Ops))
		}
		for _, op := range c.Ops {
			if _, err := os.Stat(path.Join(r.root, "internal/skills", op.Skill+".md")); err != nil {
				t.Errorf("%s %s: skill stem %s does not exist", def.Name, op.Name, op.Skill)
			}
		}
	}
}

// siteIn finds a site by path inside one section class.
func siteIn(rep *Report, class, p string) *Site {
	for i := range rep.Sections {
		if rep.Sections[i].Class != class {
			continue
		}
		for j := range rep.Sections[i].Sites {
			if rep.Sections[i].Sites[j].Path == p {
				return &rep.Sections[i].Sites[j]
			}
		}
	}
	return nil
}

func symbolNote(s *Site, name string) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, sym := range s.Symbols {
		if sym.Name == name {
			return sym.Note, true
		}
	}
	return "", false
}

func TestMakeTargetCovers(t *testing.T) {
	t.Parallel()

	bench := makeTarget{name: "bench", patterns: []string{".", "./internal/service/...", "./encoding/..."}}
	for dir, want := range map[string]bool{
		".": true, "internal/service": true, "internal/service/sub": true, "encoding": true,
		"internal/processing": false, "internal/servicex": false, "types": false,
	} {
		if got := bench.covers(dir); got != want {
			t.Errorf("bench.covers(%s) = %v, want %v", dir, got, want)
		}
	}
	var names []string
	for _, mt := range sharedRepo(t).makeTargets() {
		names = append(names, mt.name)
		if mt.name == "test" && !mt.all {
			t.Error("make test runs ./... but is not marked all")
		}
	}
	if !contains(names, "bench") {
		t.Errorf("Makefile targets %v lack bench", names)
	}
}
