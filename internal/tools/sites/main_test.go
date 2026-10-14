package main

import (
	"bytes"
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
		if s.Class == "enumerators" {
			continue
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

func TestOpReport_KnownSites(t *testing.T) {
	r := sharedRepo(t)
	cases := []struct {
		op    string
		sites map[string]string // path -> symbol ("" = any)
		gates []string
	}{
		{"MAT_PCA", map[string]string{
			"types/matrix.go":                                 "func AllMatrixTypes",
			"internal/processing/matrix.go":                   "var matrixFinalizers",
			"internal/descriptor/capabilities_matrix.go":      "func matrixCapabilities",
			"internal/descriptor/features.go":                 "var builtinFeatures",
			"internal/descriptor/purposes_matrices.go":        "var matrixPurposes",
			"internal/skills/op-mat-pca.md":                   "",
			"internal/examples/matrices/09_pca.json":          "",
			"internal/weighting/weighting.go":                 "var matrixClasses",
			"internal/descriptor/interpretations_matrices.go": "var matrixInterpretations",
		}, []string{"TestSkillsCoverAllMatrixOps", "TestManifestMatrixOpsComplete", "TestOperatorHasAtomicSkill", "TestFeaturesHaveSince"}},
		{"ATTR_VEC_DISTANCE", map[string]string{
			"internal/processing/registry.go":                "var attributeRegistry",
			"internal/descriptor/capabilities_attributes.go": "",
			"internal/skills/op-attr-vec-distance.md":        "",
			"linalg/metric/metric.go":                        "",
		}, []string{"TestOperatorHasAtomicSkill"}},
		{"OVERLAY_MARKOV", map[string]string{
			"types/overlay_streamability.go":              "var OverlayStreamability",
			"internal/processing/overlay_markov.go":       "",
			"internal/descriptor/capabilities_overlay.go": "func overlayCapabilityFor",
			"internal/skills/op-overlay-markov.md":        "",
		}, []string{"TestSkillsCoverAllOverlayKinds"}},
		{"AGG_SUM", map[string]string{
			"internal/processing/registry.go":                 "var aggregatorRegistry",
			"internal/descriptor/capabilities_aggregators.go": "",
			"internal/skills/op-agg-sum.md":                   "",
		}, []string{"TestOperatorHasAtomicSkill"}},
		{"normal", map[string]string{
			"internal/synth/distributions.go":    "func AllDistributions",
			"internal/skills/op-synth-normal.md": "",
		}, []string{"TestSkillsCoverAllSynthDistributions"}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			rep, err := opReport(r, tc.op, options{})
			if err != nil {
				t.Fatal(err)
			}
			for p, sym := range tc.sites {
				s := findSite(rep, p)
				if s == nil {
					t.Errorf("missing site %s", p)
					continue
				}
				if sym != "" && !hasSymbol(s, sym) {
					t.Errorf("site %s: missing symbol %q (have %+v)", p, sym, s.Symbols)
				}
			}
			gates := gateTests(rep)
			for _, g := range tc.gates {
				if !gates[g] {
					t.Errorf("missing gate %s", g)
				}
			}
			if len(rep.Manual) == 0 {
				t.Error("no manual reading list")
			}
		})
	}
}

// TestOpReport_MatchesGrep pins the acceptance criterion: with --all,
// the files a report lists are exactly the files an independent
// `git grep` finds for the operator's identifiers.
func TestOpReport_MatchesGrep(t *testing.T) {
	r := sharedRepo(t)
	for _, name := range []string{"MAT_PCA", "ATTR_VEC_DISTANCE", "OVERLAY_MARKOV", "WIN_LAG", "TEST_T"} {
		t.Run(name, func(t *testing.T) {
			rep, err := opReport(r, name, options{All: true})
			if err != nil {
				t.Fatal(err)
			}
			c, idx, err := r.findOperator(name)
			if err != nil {
				t.Fatal(err)
			}
			r.discoverConstAliases(c)
			op := c.Ops[idx]
			args := []string{"grep", "-l", "-I", "--untracked", "-w"}
			for _, w := range append([]string{op.Name}, op.Consts...) {
				args = append(args, "-e", w)
			}
			want := gitGrep(t, r.root, args...)
			for p := range gitGrep(t, r.root, "grep", "-l", "-I", "--untracked", "-E", `(^|[^A-Za-z0-9_-])`+op.Skill+`([^A-Za-z0-9_-]|$)`) {
				want[p] = true
			}
			got := reportFiles(rep)
			var missing, extra []string
			for p := range want {
				if !got[p] {
					missing = append(missing, p)
				}
			}
			for p := range got {
				if !want[p] {
					extra = append(extra, p)
				}
			}
			sort.Strings(missing)
			sort.Strings(extra)
			if len(missing)+len(extra) > 0 {
				t.Errorf("report vs git grep:\n missing %v\n extra   %v", missing, extra)
			}
			if len(got) < 10 {
				t.Errorf("only %d files for %s", len(got), name)
			}
		})
	}
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

func TestNewOpReport_Templates(t *testing.T) {
	r := sharedRepo(t)
	cases := []struct {
		cat   string
		sites map[string]string
		gates []string
	}{
		{"matrix", map[string]string{
			"types/matrix.go":                            "func AllMatrixTypes",
			"internal/processing/matrix.go":              "var matrixFinalizers",
			"internal/descriptor/capabilities_matrix.go": "func matrixCapabilities",
			"internal/descriptor/features.go":            "var builtinFeatures",
			"internal/skills/op-mat-<op>.md":             "",
			"internal/examples/matrices/NN_<op>.json":    "",
		}, []string{"TestSkillsCoverAllMatrixOps", "TestOperatorHasAtomicSkill"}},
		{"overlay", map[string]string{
			"types/overlay.go":                         "const block of OverlayKind",
			"types/overlay_streamability.go":           "var OverlayStreamability",
			"internal/descriptor/purposes_overlays.go": "var overlayPurposes",
			"internal/skills/op-overlay-<op>.md":       "",
			"internal/processing/overlay_<op>.go":      "",
		}, []string{"TestSkillsCoverAllOverlayKinds", "TestStreamability_OverlaysKnown"}},
		{"agg", map[string]string{
			"internal/processing/registry.go": "var aggregatorRegistry",
			"internal/skills/op-agg-<op>.md":  "",
		}, []string{"TestOperatorHasAtomicSkill"}},
	}
	for _, tc := range cases {
		t.Run(tc.cat, func(t *testing.T) {
			rep, err := newOpReport(r, tc.cat, options{})
			if err != nil {
				t.Fatal(err)
			}
			for p, sym := range tc.sites {
				s := findSite(rep, p)
				if s == nil {
					t.Errorf("missing template site %s", p)
					continue
				}
				if sym != "" && !hasSymbol(s, sym) {
					t.Errorf("site %s: missing symbol %q (have %+v)", p, sym, s.Symbols)
				}
			}
			gates := gateTests(rep)
			for _, g := range tc.gates {
				if !gates[g] {
					t.Errorf("missing gate %s", g)
				}
			}
			if len(rep.Regenerate) == 0 {
				t.Error("no regenerate commands")
			}
		})
	}
	if _, err := newOpReport(r, "nope", options{}); err == nil {
		t.Error("want an error for an unknown category")
	}
}

func TestSlotReport(t *testing.T) {
	if testing.Short() {
		t.Skip("type-checks the module")
	}
	r := sharedRepo(t)
	rep, err := slotReport(r, "request", "matrices", options{})
	if err != nil {
		t.Fatal(err)
	}
	for p, sym := range map[string]string{
		"types/types.go":                                      "",
		"internal/vectors/matrix.go":                          "func ResolveMatricesWith",
		"internal/descriptor/features.go":                     "var slotTokens",
		"internal/descriptor/predict_vectors.go":              "func predictMatrices",
		".claude/reference/matrix-and-vectors/matrix-slot.md": "",
	} {
		s := findSite(rep, p)
		if p == "types/types.go" {
			continue // the declaration is the summary line
		}
		if s == nil {
			t.Errorf("missing slot site %s", p)
			continue
		}
		if sym != "" && !hasSymbol(s, sym) {
			t.Errorf("site %s: missing symbol %q (have %+v)", p, sym, s.Symbols)
		}
	}
	regen := false
	for _, rg := range rep.Regenerate {
		for _, p := range rg.Paths {
			regen = regen || p == "descriptor/testdata/payload-schema.json" && rg.Command == "go test -count=1 ./descriptor/ -update"
		}
	}
	if !regen {
		t.Error("payload-schema golden not in Regenerate")
	}
	if !gateTests(rep)["TestPredict_Matrices"] {
		t.Error("missing gate TestPredict_Matrices")
	}

	// a NEW slot gets the per-slot walker template
	rep, err = slotReport(r, "request", "not_a_slot_yet", options{})
	if err != nil {
		t.Fatal(err)
	}
	for p, sym := range map[string]string{
		"types/types.go":                     "type Request",
		"internal/mcp/bind.go":               "func addRequestSlotDetail",
		"internal/descriptor/materialize.go": "func MaterializeSlotRefusal",
	} {
		if !hasSymbol(findSite(rep, p), sym) {
			t.Errorf("new-slot template: missing %s %s", p, sym)
		}
	}
	if _, err := slotReport(r, "neither", "x", options{}); err == nil {
		t.Error("want an error for a bad slot root")
	}
}

func TestScanBytes_TokenBoundaries(t *testing.T) {
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
	r := sharedRepo(t)
	var all []string
	for _, c := range categoryTable {
		all = append(all, c.Manual...)
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

func TestRun_CLI(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"op", "MAT_PCA"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "sites op MAT_PCA\n") || !strings.Contains(out.String(), "## Gates to rerun") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"--json", "new-op", "matrix"}, &out, &errb); code != 0 || !strings.Contains(out.String(), `"mode": "new-op"`) {
		t.Fatalf("json new-op: exit %d: %s", code, errb.String())
	}
	for _, args := range [][]string{nil, {"op"}, {"bogus"}, {"--nope", "op", "X"}, {"slot", "request"}} {
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2", args, code)
		}
	}
	if code := run([]string{"op", "NOT_AN_OP"}, &out, &errb); code != 1 {
		t.Errorf("unknown op exit %d, want 1", code)
	}
}
