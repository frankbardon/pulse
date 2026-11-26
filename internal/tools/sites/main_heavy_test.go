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

// TestSpecSites_Regression pins the regression-slot sites that never
// name an operator (#733): every REG_* op report and the new-op
// regression template list them in the spec section.
func TestSpecSites_Regression(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	want := []struct{ path, sym string }{
		{"internal/regadapt/adapter.go", "func AdaptRegressionFactory"},
		{"internal/processing/regression/ols_covariance.go", "func levelFields"},
		{"internal/slotfail/slotfail.go", "var RegressionCodes"},
		{"internal/descriptor/schema.go", "func enumValues"},
		{"internal/cli/strict_acceptance_test.go", "func strictAcceptanceRows"},
		{"examples/profiles/read-only-analyst.json", ""},
	}
	reports := map[string]func() (*Report, error){
		"op REG_MMM_HILL":   func() (*Report, error) { return opReport(r, "REG_MMM_HILL", options{}) },
		"op REG_OLS":        func() (*Report, error) { return opReport(r, "REG_OLS", options{}) },
		"new-op regression": func() (*Report, error) { return newOpReport(r, "regression", options{}) },
	}
	for name, build := range reports {
		t.Run(name, func(t *testing.T) {
			rep, err := build()
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range want {
				if _, ok := symbolNote(siteIn(rep, "spec", w.path), w.sym); !ok {
					t.Errorf("spec section lacks %s · %q", w.path, w.sym)
				}
			}
		})
	}
}

// TestSlotReport_ComposeLevel pins the Compose-level slot sites (#560,
// #706): `slot request <name>` falls back to types.ComposedRequest and
// lists the walkers, Explain, MCP bind, strict-acceptance families,
// example profiles, limits-parity lists and the shared ResultCatalog.
func TestSlotReport_ComposeLevel(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	common := []struct{ class, path, sym string }{
		{"descriptor", "internal/descriptor/request_slots.go", "func setSlots"},
		{"compose", "internal/cli/strict_acceptance_test.go", "func strictAcceptanceRows"},
		{"compose", "examples/profiles/read-only-analyst.json", ""},
		{"compose", "internal/service/limits_parity_test.go", "func parityCases"},
		{"compose", "internal/descriptor/compose_optimize.go", "func ResultCatalog"},
	}
	for _, slot := range []string{"sweep", "optimize", "base", "banner", "fits", "post_fit"} {
		t.Run(slot, func(t *testing.T) {
			rep, err := slotReport(r, "request", slot, options{})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Target != "ComposedRequest."+slot {
				t.Fatalf("target %q, want the ComposedRequest field", rep.Target)
			}
			for _, w := range common {
				if _, ok := symbolNote(siteIn(rep, w.class, w.path), w.sym); !ok {
					t.Errorf("%s section lacks %s · %q", w.class, w.path, w.sym)
				}
			}
			if !contains(rep.Manual, ".claude/reference/update-demand/compose.md") {
				t.Errorf("manual lacks the compose update-demand slice: %v", rep.Manual)
			}
		})
	}
	// the compose form, plus the sweep-specific walker, Explain and MCP sites
	rep, err := slotReport(r, "compose", "sweep", options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct{ path, sym string }{
		{"internal/descriptor/request_reads.go", "func (readsWalker) compose"},
		{"internal/mcp/return_default.go", ""},
		{"internal/mcp/bind.go", ""},
		{"internal/guide/explain_results.go", ""},
	} {
		s := findSite(rep, w.path)
		if s == nil || w.sym != "" && !hasSymbol(s, w.sym) {
			t.Errorf("compose sweep lacks %s · %q", w.path, w.sym)
		}
	}
	if !gateTests(rep)["TestStrictInput_EveryFamilyAndRootRefusesAMisspelledKey"] {
		t.Error("the strict-acceptance gate is not rerun")
	}
	// an item-level key resolves to every struct declaring it
	rep, err = slotReport(r, "request", "for_each", options{})
	if err != nil {
		t.Fatal(err)
	}
	if findSite(rep, "internal/template/for_each.go") == nil || len(rep.Summary) < 3 {
		t.Errorf("for_each: summary %v, template site missing", rep.Summary)
	}
	// a NEW compose slot gets the ComposedRequest walker template
	rep, err = slotReport(r, "compose", "not_a_slot_yet", options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct{ path, sym string }{
		{"types/types.go", "type ComposedRequest"},
		{"internal/descriptor/request_slots.go", "func setSlots"},
		{"internal/mcp/bind.go", "func buildComposeSchemaWithExtensions"},
	} {
		if !hasSymbol(findSite(rep, w.path), w.sym) {
			t.Errorf("new compose slot template lacks %s · %q", w.path, w.sym)
		}
	}
}

// TestOpReport_ReachedThroughDeps pins the sites an operator reaches
// through what it declares or calls (#715, #453a, #453b): the shared
// series engines, the primitives its own files call, every capability
// param with its decoders and forms, and the metric-registry consumers.
func TestOpReport_ReachedThroughDeps(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	build := func(op string) *Report {
		t.Helper()
		rep, err := opReport(r, op, options{})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	// #715: the params.field / partition_by engines
	for _, op := range []string{"OVERLAY_FORMULA", "OVERLAY_T_VS_PRIOR"} {
		rep := build(op)
		for _, p := range []string{"internal/seriesfield/seriesfield.go", "internal/seriespartition/seriespartition.go"} {
			if findSite(rep, p) == nil {
				t.Errorf("op %s lacks the series engine %s", op, p)
			}
		}
	}
	rep := build("OVERLAY_SYMMETRY")
	if _, ok := symbolNote(siteIn(rep, "primitives", "internal/statdist/binomial.go"), "func BinomialTwoSidedP"); !ok {
		t.Error("OVERLAY_SYMMETRY: statdist.BinomialTwoSidedP not a shared primitive")
	}
	if !gateTests(rep)["TestBinomialTwoSidedP_ClosedForms"] {
		t.Error("OVERLAY_SYMMETRY: the statdist tests are not rerun")
	}

	// #453a: a non-enum param, its forms and their decoders
	rep = build("ATTR_VEC_DISTANCE")
	note, _ := symbolNote(siteIn(rep, "params", "internal/descriptor/capabilities_attributes.go"), "params.reference")
	for _, form := range []string{"centroid", "population", "record"} {
		if !strings.Contains(note, form) {
			t.Errorf("params.reference note %q lacks form %s", note, form)
		}
	}
	if note, _ := symbolNote(siteIn(rep, "params", "internal/vectors/compare.go"), "func comparisonReference"); !strings.Contains(note, `form {"centroid"}`) {
		t.Errorf("comparisonReference note %q: the centroid form decoder is not attributed", note)
	}
	// #453b: every metric-registry consumer site
	for _, w := range []struct{ path, sym string }{
		{"linalg/metric/metric.go", "var consumerForms"},
		{"linalg/metric/metric.go", "func Consumers"},
		{"internal/descriptor/capabilities_matrix.go", "func metricCapabilities"},
		{"internal/descriptor/manifest.go", "func assembleManifest"},
		{"internal/vectors/compare.go", "func ComparisonConsumer"},
		{"internal/processing/grouper_kmeans.go", "var kmeansMetric"},
	} {
		if _, ok := symbolNote(siteIn(rep, "metric-consumers", w.path), w.sym); !ok {
			t.Errorf("metric consumers lack %s · %s", w.path, w.sym)
		}
	}
	// a non-consumer prints a pointer, a new operator the full list
	rep = build("ATTR_ZSCORE")
	for _, s := range rep.Sections {
		if s.Class == "metric-consumers" {
			t.Error("ATTR_ZSCORE (no metric) lists the metric consumers")
		}
	}
	if !strings.Contains(strings.Join(rep.Omitted, "\n"), "metric consumers not listed") {
		t.Errorf("ATTR_ZSCORE omitted %v: no pointer to the consumer sites", rep.Omitted)
	}
	rep, err := newOpReport(r, "attribute", options{})
	if err != nil {
		t.Fatal(err)
	}
	if siteIn(rep, "metric-consumers", "internal/descriptor/manifest.go") == nil {
		t.Error("new-op attribute lacks the metric consumer sites")
	}
}

// TestPrecision_RealTree pins TODO #453 (3)-(5) on the real tree: no
// false category-norm warning for MAT_RELIABILITY, the hand-kept
// vector-aggregator subset in new-op, and oracle fixtures / generators
// attributed to the operators they serve.
func TestPrecision_RealTree(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	rep, err := opReport(r, "MAT_RELIABILITY", options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range rep.Warnings {
		if strings.Contains(w, "category-norm site missing") {
			t.Errorf("MAT_RELIABILITY: false warning %q", w)
		}
	}
	rep, err = newOpReport(r, "aggregator", options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := symbolNote(siteIn(rep, "subsets", "internal/vectors/aggregations.go"), "func VectorAggregationTypes"); !ok {
		t.Error("new-op aggregator lacks the VectorAggregationTypes subset")
	}
	oracle := func(op string) map[string]string {
		rep, err := opReport(r, op, options{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, s := range rep.Sections {
			if s.Class == "oracle" {
				for _, site := range s.Sites {
					out[site.Path] = site.Note
				}
			}
		}
		return out
	}
	ref := "internal/processing/testdata/reference/"
	sym := oracle("OVERLAY_SYMMETRY")
	if !strings.Contains(sym[ref+"binomial.json"], "shared primitive") {
		t.Errorf("OVERLAY_SYMMETRY: binomial.json not attributed through statdist (%q)", sym[ref+"binomial.json"])
	}
	for _, p := range []string{ref + "markov.json", ref + "correspondence.json", ref + "margins.json", "scripts/reference/gen_glm_irls.R"} {
		if _, ok := sym[p]; ok {
			t.Errorf("OVERLAY_SYMMETRY: %s attributed (another operator's fixture or a family name)", p)
		}
	}
	fa := oracle("MAT_FACTOR")
	for _, p := range []string{ref + "mv_rotation.json", ref + "mv_factor.json", "scripts/reference/gen_rotation.R"} {
		if _, ok := fa[p]; !ok {
			t.Errorf("MAT_FACTOR: %s not attributed", p)
		}
	}
	if _, ok := fa[ref+"normal_cdf.json"]; ok {
		t.Error("MAT_FACTOR: a shared oracle file read whole (normal_cdf.json attributed)")
	}
}

// TestReports_WholeRepoSites pins the sites no operator or slot name
// reaches, on the real tree: the every-example runners for a change with
// an example site (op and new-op), the per-case attribution inside a
// shared generator, and the ReadSchema call-site floor for a slot whose
// site files call it.
func TestReports_WholeRepoSites(t *testing.T) {
	t.Parallel()

	r := sharedRepo(t)
	gateTests := func(rep *Report, why string) map[string]bool {
		out := map[string]bool{}
		for _, g := range rep.Gates {
			if strings.HasPrefix(g.Why, why) {
				for _, n := range g.Tests {
					out[g.Package+" "+n] = true
				}
			}
		}
		return out
	}
	runners := []string{"internal/integration TestLimitsDefaultsNeverTripGoldens", "internal/integration TestExamples_RunEndToEnd"}

	op, err := opReport(r, "OVERLAY_SYMMETRY", options{})
	if err != nil {
		t.Fatal(err)
	}
	newOp, err := newOpReport(r, "overlay", options{})
	if err != nil {
		t.Fatal(err)
	}
	for name, rep := range map[string]*Report{"op": op, "new-op": newOp} {
		got := gateTests(rep, "whole-repo runners")
		for _, w := range runners {
			if !got[w] {
				t.Errorf("%s report: whole-repo runner %s missing (have %v)", name, w, got)
			}
		}
	}
	var gen *Site
	for i := range op.Sections {
		if op.Sections[i].Class != "generator-cases" {
			continue
		}
		for j := range op.Sections[i].Sites {
			if op.Sections[i].Sites[j].Path == "internal/service/testdata/weight_reference/gen_weight_reference.py" {
				gen = &op.Sections[i].Sites[j]
			}
		}
	}
	if !hasSymbol(gen, "ov_request › case ov_symmetry") {
		t.Errorf("weighted reference generator: OVERLAY_SYMMETRY not attributed to its case (site %+v)", gen)
	}

	slot, err := slotReport(r, "request", "cohort", options{})
	if err != nil {
		t.Fatal(err)
	}
	if !gateTests(slot, "call-site floors")[". TestReadSchemaCallersThreadHeaderVersion"] {
		t.Errorf("slot request cohort: the ReadSchema call-site floor is not a gate (gates %+v)", slot.Gates)
	}
	floorSite := false
	for _, sec := range slot.Sections {
		if sec.Class == "floors" {
			for _, s := range sec.Sites {
				floorSite = floorSite || s.Path == "internal/service/service.go"
			}
		}
	}
	if !floorSite {
		t.Error("slot request cohort: internal/service/service.go's ReadSchema call is not listed under the floors")
	}
}

// TestFixtureReport_KnownSites pins the real tree's fixture-cohort
// registration lists: a new cohort must reach every one of them.
func TestFixtureReport_KnownSites(t *testing.T) {
	t.Parallel()

	rep, err := fixtureReport(sharedRepo(t), "new_cohort", options{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, sec := range rep.Sections {
		if sec.Class == "fixture-lists" {
			for _, s := range sec.Sites {
				got[s.Path+" "+s.Symbols[0].Name] = s.Coverage
			}
		}
	}
	for k, cov := range map[string]string{
		"internal/integration/examples_test.go func buildExampleFixtures": "complete",
		"internal/examples/README.md ## Available cohorts":                "complete",
		"internal/examples/README.md ## One-time setup":                   "complete",
		"internal/examples/fixtures/gen.go func Generate":                 "subset",
		"request_fuzz_test.go var requestFuzzCohorts":                     "subset",
	} {
		if got[k] != cov {
			t.Errorf("%s: coverage %q, want %q (lists: %v)", k, got[k], cov, got)
		}
	}
	shell := false
	for k, cov := range got {
		if strings.HasPrefix(k, "internal/examples/fixtures/build.sh ") && cov == "complete" {
			shell = true
		}
	}
	if !shell {
		t.Errorf("fixtures/build.sh's cohort loop not listed: %v", got)
	}
}
