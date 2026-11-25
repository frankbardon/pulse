package main

import (
	"strings"
	"testing"
)

const refDir = "internal/processing/testdata/reference/"

// precisionFixture reproduces the three false or imprecise answers of
// TODO #453 (3)-(5): a category-norm file that names most operators only
// in comments, a hand-kept operator subset, and oracle fixtures and
// generators shared by several operators.
var precisionFixture = map[string]string{
	"internal/descriptor/predict_return_sizes.go": `package descriptor

// sizes walks the shapes: MAT_A has eigenvectors, MAT_B unrotated,
// MAT_C a variance decomposition.
func sizes(t string) int {
	if t == "MAT_A" {
		return 1
	}
	return 0
}
`,
	"internal/vectors/aggregations.go": `package vectors

import "github.com/frankbardon/pulse/types"

func VectorAggregationTypes() []types.AggregationType {
	return []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM, types.AGG_VEC_SD}
}

var everyOne = []types.AggregationType{types.AGG_VEC_MEAN, types.AGG_VEC_SUM, types.AGG_VEC_SD, types.AGG_SUM, types.AGG_COUNT}

var single = []types.AggregationType{types.AGG_SUM}

var keyed = map[types.AggregationType]bool{types.AGG_SUM: true, types.AGG_COUNT: true}
`,
	"internal/vectors/aggregations_test.go": `package vectors

var testOnly = []string{"AGG_VEC_MEAN", "AGG_VEC_SUM"}
`,
	refDir + "symmetry.json": "{}\n",
	refDir + "markov.json":   "{}\n",
	refDir + "binomial.json": "{}\n",
	refDir + "margins.json":  "{}\n",
	"internal/descriptor/predict_overlay_test.go": `package descriptor

var layers = map[string]layer{
	OverlayKindSymmetry: {Name: "symmetry"},
	OverlayKindMarkov:   {Name: "markov"},
}

var bodies = []string{` + "`" + `{"margins":{"rows":true},"kind":"OVERLAY_SYMMETRY"}` + "`" + `}
`,
	"internal/statdist/binomial_test.go": `package statdist

func TestReferenceOracle_Binomial(t *testing.T) { _ = loadReference(t, "binomial") }
`,
	"scripts/reference/gen_symmetry.R": `# Writes symmetry.json and binomial.json
write_golden("symmetry", list(a = 1))
write_golden("binomial", list(b = 2))
`,
	"scripts/reference/gen_glm.R": `fit <- glm(y ~ x, family = "binomial")
write_golden("glm", list(fit = fit))
`,
	"scripts/reference/gen_reference.R": `# The OVERLAY_SYMMETRY golden (symmetry.json)
source(file.path(script_dir, "gen_symmetry.R"), local = FALSE)
`,
}

func TestCodeOps_CommentsAreNoNorm(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, precisionFixture)
	p := "internal/descriptor/predict_return_sizes.go"
	src := string(r.read(p))
	def := categoryDef{Prefix: "MAT_", Skill: "op-mat-"}
	c := &category{categoryDef: def}
	for _, n := range []string{"MAT_A", "MAT_B", "MAT_C", "MAT_D"} {
		c.Ops = append(c.Ops, newOperator(def, n))
	}
	a := &catAnalysis{c: c, fileOps: map[string]map[int]bool{p: {}}, actual: map[string]map[int]string{p: {}}, byOp: make([]map[string][]hit, 4)}
	for i, n := range []string{"MAT_A", "MAT_B", "MAT_C"} {
		a.fileOps[p][i] = true
		a.actual[p][i] = p
		a.byOp[i] = map[string][]hit{}
		for off := 0; ; {
			j := strings.Index(src[off:], n)
			if j < 0 {
				break
			}
			a.byOp[i][p] = append(a.byOp[i][p], hit{Path: p, Op: i, Off: off + j})
			off += j + len(n)
		}
	}
	if got := len(a.fileOps[p]); !a.isEvery(got) {
		t.Fatalf("fixture: %d/4 ops should look like a norm by raw count", got)
	}
	if got := a.codeOps(r, p); got != 1 || a.isEvery(got) {
		t.Errorf("codeOps = %d, want 1 (MAT_B / MAT_C appear only in a comment): no category-norm warning", got)
	}
}

func TestSubsetSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, precisionFixture)
	def := categoryDef{Prefix: "AGG_", Skill: "op-agg-"}
	c := &category{categoryDef: def}
	for _, n := range []string{"AGG_VEC_MEAN", "AGG_VEC_SUM", "AGG_VEC_SD", "AGG_SUM", "AGG_COUNT", "AGG_MAX"} {
		c.Ops = append(c.Ops, newOperator(def, n))
	}
	sec, ok := r.subsetSection(c)
	if !ok {
		t.Fatal("no subset section for the fixture")
	}
	checkRules(t, sec, []ruleCase{
		{"a hand-kept subset", "internal/vectors/aggregations.go", "func VectorAggregationTypes", "lists 3/6: AGG_VEC_MEAN, AGG_VEC_SD, AGG_VEC_SUM", true},
		{"a map keyed by a subset", "internal/vectors/aggregations.go", "var keyed", "lists 2/6", true},
		{"an every-op list is the enumerators' job", "internal/vectors/aggregations.go", "var everyOne", "", false},
		{"one operator is no list", "internal/vectors/aggregations.go", "var single", "", false},
	})
	if siteOf(sec, "internal/vectors/aggregations_test.go") != nil {
		t.Error("a test table listed as a hand-kept subset")
	}
}

func TestOracleSection_Precise(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, precisionFixture)
	shared := "internal/descriptor/predict_overlay_test.go"
	src := string(r.read(shared))
	var hs []hit
	for _, n := range []string{"OverlayKindSymmetry", "OVERLAY_SYMMETRY"} {
		hs = append(hs, hit{Path: shared, Off: strings.Index(src, n)})
	}
	testDecls := map[string][]string{shared: {"var layers", "var bodies"}}
	prim := map[string][]string{"internal/statdist/binomial_test.go": {"func TestReferenceOracle_Binomial"}}
	sec, fixtures := r.oracleSection(testDecls, map[string][]hit{shared: hs}, prim, nil)
	has := func(p string) *Site {
		for i := range sec.Sites {
			if sec.Sites[i].Path == p {
				return &sec.Sites[i]
			}
		}
		return nil
	}
	for _, c := range []struct {
		path, note string
		want       bool
	}{
		{refDir + "symmetry.json", "read by " + shared, true},
		{refDir + "markov.json", "", false},  // a sibling row of the shared table
		{refDir + "margins.json", "", false}, // a JSON key inside a request body
		{refDir + "binomial.json", "a shared primitive's test", true},
		{"scripts/reference/gen_symmetry.R", "writes binomial, symmetry", true},
		{"scripts/reference/gen_glm.R", "", false}, // "binomial" as a family name is no write
		{"scripts/reference/gen_reference.R", "driver: sources gen_symmetry.R", true},
	} {
		s := has(c.path)
		if (s != nil) != c.want {
			t.Errorf("%s listed = %v, want %v (%+v)", c.path, s != nil, c.want, s)
			continue
		}
		if s != nil && !strings.Contains(s.Note, c.note) {
			t.Errorf("%s note %q, want it to contain %q", c.path, s.Note, c.note)
		}
	}
	if len(fixtures) != 2 {
		t.Errorf("fixtures to regenerate %v, want symmetry and binomial", fixtures)
	}
}
