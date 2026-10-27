//go:build heavy

package main

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestOpReport_KnownSites(t *testing.T) {
	t.Parallel()

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
			if !contains(rep.Manual, extensionRecipe) {
				t.Errorf("reading list lacks %s", extensionRecipe)
			}
			if tc.op == "AGG_SUM" && !contains(rep.Manual, "docs/src/internals/adding-aggregator.md") {
				t.Error("AGG_SUM reading list lacks its category recipe adding-aggregator.md")
			}
		})
	}
}

// TestOpReport_MatchesGrep pins the acceptance criterion: with --all,
// the files a report lists are exactly the files an independent
// `git grep` finds for the operator's identifiers.
func TestOpReport_MatchesGrep(t *testing.T) {
	t.Parallel()

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

func TestNewOpReport_Templates(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

func TestRun_CLI(t *testing.T) {
	t.Parallel()

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

// TestOpReport_DerivedSites pins the sites that never spell the operator:
// callers of a selective predicate (followed through further predicates)
// and the size estimates in its files plus their callers.
func TestOpReport_DerivedSites(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	cases := []struct {
		op     string
		sites  map[string]string // path -> symbol
		absent map[string]string
	}{
		// array-valued consumers: ArrayValued (names it) <- IsArrayValued <- AggregationExpands
		{"AGG_VEC_MEAN", map[string]string{
			"internal/vectors/aggregations.go":            "func AggregationOutputColumns",
			"internal/descriptor/field_refs.go":           "func fieldRefWalkPlan",
			"internal/descriptor/overlay_array_valued.go": "func scoredValueReason",
			"internal/descriptor/return_resolve.go":       "func returnOutputColumns",
		}, nil},
		// the two-pass predicate chain
		{"ATTR_VEC_DISTANCE", map[string]string{
			"internal/processing/processor.go":           "func hasTwoPassAttribute",
			"internal/processing/crosstab_fused_gate.go": "func (registryFuseFacts) AttributeSlotBound",
		}, nil},
		// memory estimates that never name an operator
		{"MAT_PCA", map[string]string{
			"internal/vectors/matrix_rules.go": "const pairMomentBytes",
			"internal/descriptor/limits.go":    "func matrixMemory",
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			rep, err := opReport(r, tc.op, options{})
			if err != nil {
				t.Fatal(err)
			}
			for p, sym := range tc.sites {
				found := false
				for i := range rep.Sections {
					if rep.Sections[i].Class != "derived" {
						continue
					}
					for j := range rep.Sections[i].Sites {
						if rep.Sections[i].Sites[j].Path == p && hasSymbol(&rep.Sections[i].Sites[j], sym) {
							found = true
						}
					}
				}
				if !found {
					t.Errorf("derived section lacks %s · %s", p, sym)
				}
			}
		})
	}
}

// TestOpReport_ParamEnums pins the enum params read from the capability
// declaration (helper-local params included) and the sites using the
// constants that spell their values.
func TestOpReport_ParamEnums(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	rep, err := opReport(r, "MAT_FACTOR", options{})
	if err != nil {
		t.Fatal(err)
	}
	decl := siteIn(rep, "param-enums", "internal/descriptor/capabilities_matrix.go")
	for param, want := range map[string]string{
		"params.rotation":   "none, varimax, promax, oblimin",
		"params.missing":    "listwise, pairwise", // a function-local shared param slice
		"params.extraction": "minres",
	} {
		note, ok := symbolNote(decl, param)
		if !ok || !strings.Contains(note, want) {
			t.Errorf("param-enums: %s note %q, want it to list %q", param, note, want)
		}
	}
	if !hasSymbol(siteIn(rep, "param-enums", "internal/processing/matrix_rotate.go"), "func rotateLoadings") {
		t.Error("param-enums: the rotation dispatch (matrix_rotate.go rotateLoadings) is missing")
	}
	// an operator with no enum params has no section
	rep, err = opReport(r, "AGG_SUM", options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rep.Sections {
		if s.Class == "param-enums" {
			t.Errorf("AGG_SUM: unexpected param-enums section %+v", s.Sites)
		}
	}
}

// TestOpReport_OracleTablesBenches pins the R oracle fixtures (read by the
// operator's tests, written by scripts/reference), the test tables naming
// it, and the benchmarks / Makefile targets of its packages.
func TestOpReport_OracleTablesBenches(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	rep, err := opReport(r, "MAT_PCA", options{})
	if err != nil {
		t.Fatal(err)
	}
	if s := siteIn(rep, "oracle", "internal/processing/testdata/reference/mv_pca.json"); s == nil || !strings.Contains(s.Note, "internal/service/matrix_pca_test.go") {
		t.Errorf("oracle: mv_pca.json read by matrix_pca_test.go missing (%+v)", s)
	}
	if s := siteIn(rep, "oracle", "scripts/reference/gen_multivariate.R"); s == nil || !strings.Contains(s.Note, "mv_pca") {
		t.Errorf("oracle: generator gen_multivariate.R missing (%+v)", s)
	}
	regen := false
	for _, rg := range rep.Regenerate {
		regen = regen || rg.Command == "make reference" && contains(rg.Paths, "internal/processing/testdata/reference/mv_pca.json")
	}
	if !regen {
		t.Error("make reference does not list mv_pca.json")
	}
	if !hasSymbol(siteIn(rep, "test-tables", "internal/service/matrix_missing_test.go"), "var operatorAuxiliary") {
		t.Error("test-tables: var operatorAuxiliary missing")
	}
	var bench, mk bool
	for _, g := range rep.Gates {
		bench = bench || g.Package == "internal/processing" && strings.Contains(g.Command, "-bench . ")
		mk = mk || strings.HasPrefix(g.Command, "make bench ")
	}
	if !bench || !mk {
		t.Errorf("bench gates: package bench %v, make bench %v", bench, mk)
	}
}

// TestNewOpReport_NextExampleNumber pins the NN_ hint to the directory's
// highest numeric prefix + 1.
func TestNewOpReport_NextExampleNumber(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	rep, err := newOpReport(r, "overlay", options{})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path.Join(r.root, "internal/examples/overlays"))
	if err != nil {
		t.Fatal(err)
	}
	maxN := 0
	for _, e := range entries {
		if m := exampleNumRe.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			maxN = max(maxN, n)
		}
	}
	want := fmt.Sprintf("next free NN_ = %02d", maxN+1)
	s := findSite(rep, "internal/examples/overlays/NN_crosstab_<op>.json")
	if s == nil || !strings.Contains(s.Note, want) {
		t.Errorf("NN_ hint: site %+v, want note containing %q", s, want)
	}
}

// TestSlotReport_ImportPathIsNotASlot pins the stdlib-collision fix: a
// slot whose json name is also a package path (`sort`) must not match
// `import "sort"` as a string literal or count an import block as a walker.
func TestSlotReport_ImportPathIsNotASlot(t *testing.T) {
	t.Parallel()

	rep, err := slotReport(sharedRepo(t), "request", "sort", options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sec := range rep.Sections {
		for _, s := range sec.Sites {
			if hasSymbol(&s, "import") {
				t.Errorf("%s: an import block listed as a slot site", s.Path)
			}
		}
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, " · import ") {
			t.Errorf("import block reported as a per-slot walker: %s", w)
		}
	}
}

// TestTypeCheck_OncePerRepo pins the typeCheck memo: every struct's view
// shares one type-checked module (the same file set), so the slot tests
// pay for one module type-check per process, not one per call.
func TestTypeCheck_OncePerRepo(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	req, err := r.typeCheck("Request")
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.typeCheck("Request")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := r.typeCheck("Response")
	if err != nil {
		t.Fatal(err)
	}
	if req.fset != again.fset || req.fset != resp.fset {
		t.Fatal("typeCheck re-type-checked the module: the views do not share one file set")
	}
	if len(req.refs) == 0 || len(resp.refs) == 0 || len(req.files) == 0 {
		t.Fatalf("empty view: %d Request refs, %d Response refs, %d files", len(req.refs), len(resp.refs), len(req.files))
	}
	if len(again.refs) != len(req.refs) {
		t.Errorf("a repeat Request view has %d refs, want %d", len(again.refs), len(req.refs))
	}
	for _, ref := range resp.refs {
		if ref.Field == "Vectors" {
			t.Errorf("Response view holds a Request-only field reference %+v", ref)
			break
		}
	}
}
