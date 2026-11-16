package main

import (
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixtureRepo writes files under a temp root and opens them as a repo
// (no git: the file list is the map's keys).
func fixtureRepo(t *testing.T, files map[string]string) *repo {
	t.Helper()
	root := t.TempDir()
	r := &repo{root: root, content: map[string][]byte{}, parsed: map[string]*goFile{}, fset: token.NewFileSet()}
	for p, src := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		r.files = append(r.files, p)
	}
	sort.Strings(r.files)
	return r
}

const specFixtureImports = `import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)
`

// specFixture is a miniature tree with one site (and one near miss) per
// spec rule.
var specFixture = map[string]string{
	"types/regression.go": `package types

type RegressionType string

const RegOLS RegressionType = "REG_OLS"

func AllRegressionTypes() []RegressionType { return []RegressionType{RegOLS} }

type RegressionSpec struct {
	Type  RegressionType   ` + "`json:\"type\"`" + `
	Name  string           ` + "`json:\"name\"`" + `
	Order *RegressionOrder ` + "`json:\"order,omitempty\"`" + `
}

type RegressionOrder struct {
	Gaps OrderGaps ` + "`json:\"gaps\"`" + `
}

type OrderGaps string

func AllOrderGaps() []OrderGaps { return nil }

// AllNames is a []string list, not a vocabulary of a named type.
func AllNames() []string { return nil }

type Request struct {
	Regressions []*RegressionSpec ` + "`json:\"regressions,omitempty\"`" + `
}
`,
	"internal/regorder/regorder.go": "package regorder\n\n" + specFixtureImports + `
func Check(spec *types.RegressionSpec) *errors.CodedError  { return nil }
func CheckB(spec *types.RegressionSpec) *errors.CodedError { return nil }
func CheckC(spec types.RegressionSpec) (int, *errors.CodedError) { return 0, nil }
func notChecker(spec *types.RegressionSpec) error { return nil }
`,
	"internal/regadapt/adapter.go": "package regadapt\n\n" + `import (
	"github.com/frankbardon/pulse/internal/regorder"
	"github.com/frankbardon/pulse/types"
)

func AdaptRegressionFactory() func(spec *types.RegressionSpec) {
	return func(spec *types.RegressionSpec) {
		regorder.Check(spec)
		regorder.CheckB(spec)
		regorder.CheckC(*spec)
	}
}

func twoChecks(spec *types.RegressionSpec) {
	regorder.Check(spec)
	regorder.CheckB(spec)
	regorder.Check(spec)
}
`,
	"internal/processing/regression/ols_covariance.go": "package regression\n\n" + `import "github.com/frankbardon/pulse/types"

func levelFields(spec *types.RegressionSpec) []string { return nil }
func levelCount(spec *types.RegressionSpec) []int    { return nil }
func otherFields(x *types.Request) []string          { return nil }
`,
	"internal/slotfail/slotfail.go": "package slotfail\n\n" + specFixtureImports + `
var _ types.Request

var RegressionCodes = map[errors.Code]bool{
	errors.PROCESSING_REGRESSION_A: true,
	errors.PROCESSING_REGRESSION_B: true,
	errors.PROCESSING_REGRESSION:   true,
}

var fewCodes = []errors.Code{errors.PROCESSING_REGRESSION_A, errors.PROCESSING_REGRESSION_B, errors.PROCESSING_MATRIX_A}

func refuse() []errors.Code {
	return []errors.Code{errors.PROCESSING_REGRESSION_A, errors.PROCESSING_REGRESSION_B, errors.PROCESSING_REGRESSION_C}
}
`,
	"internal/descriptor/schema.go": "package descriptor\n\n" + `import "github.com/frankbardon/pulse/types"

func enumValues() {
	_ = types.AllOrderGaps()
	_ = types.AllRegressionTypes()
}

func names() { _ = types.AllNames() }
`,
	"internal/cli/strict_acceptance_test.go": `package cli

var rows = []string{"request.regressions.order"}

var near = []string{"request.regressionsx", "composed.regressions.order"}
`,
	"examples/profiles/read-only-analyst.json": `{"features": ["capability:regression_holdout", "capability:regression_order"]}
`,
	"examples/profiles/one.json": `{"features": ["capability:regression_order"]}
`,
}

func TestSpecSection_Rules(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, specFixture)
	def, _ := findCategoryDef("regression")
	sec, ok := r.specSection(def)
	if !ok {
		t.Fatal("no spec section for the regression fixture")
	}
	if !strings.Contains(sec.Title, "types.RegressionSpec") {
		t.Errorf("title %q does not name types.RegressionSpec", sec.Title)
	}
	cases := []struct {
		rule, path, sym, note string
		want                  bool
	}{
		{"refusal pipeline", "internal/regadapt/adapter.go", "func AdaptRegressionFactory", "refusal pipeline: 3 spec checkers", true},
		{"pipeline below the minimum", "internal/regadapt/adapter.go", "func twoChecks", "", false},
		{"field-list deriver", "internal/processing/regression/ols_covariance.go", "func levelFields", "field-list deriver", true},
		{"deriver needs []string", "internal/processing/regression/ols_covariance.go", "func levelCount", "", false},
		{"deriver needs the spec", "internal/processing/regression/ols_covariance.go", "func otherFields", "", false},
		{"code table", "internal/slotfail/slotfail.go", "var RegressionCodes", "code table: 3", true},
		{"code table below the minimum", "internal/slotfail/slotfail.go", "var fewCodes", "", false},
		{"a function is not a code table", "internal/slotfail/slotfail.go", "func refuse", "", false},
		{"vocabulary registrar", "internal/descriptor/schema.go", "func enumValues", "types.AllOrderGaps (RegressionOrder.Gaps)", true},
		{"a []string list is no vocabulary", "internal/descriptor/schema.go", "func names", "", false},
		{"strict-input family", "internal/cli/strict_acceptance_test.go", "var rows", `"request.regressions.<key>"`, true},
		{"strict-input near miss", "internal/cli/strict_acceptance_test.go", "var near", "", false},
		{"capability list", "examples/profiles/read-only-analyst.json", "", "capability list: 2 features", true},
		{"one capability is no list", "examples/profiles/one.json", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			s := siteOf(sec, tc.path)
			note, found := symbolNote(s, tc.sym)
			if found != tc.want {
				t.Fatalf("%s · %q listed = %v, want %v (site %+v)", tc.path, tc.sym, found, tc.want, s)
			}
			if tc.want && !strings.Contains(note, tc.note) {
				t.Errorf("%s · %q note %q, want it to contain %q", tc.path, tc.sym, note, tc.note)
			}
		})
	}
	// the registry's own vocabulary is the Enumerators section's
	if note, _ := symbolNote(siteOf(sec, "internal/descriptor/schema.go"), "func enumValues"); strings.Contains(note, "AllRegressionTypes") {
		t.Errorf("enumValues note names the registry function: %q", note)
	}
}

func TestFindSpec(t *testing.T) {
	t.Parallel()

	r := fixtureRepo(t, specFixture)
	ti := r.typesIndex()
	cases := []struct {
		cat  string
		want specInfo
		ok   bool
	}{
		{"regression", specInfo{Struct: "RegressionSpec", Slot: "regressions", Family: "REGRESSION"}, true},
		{"matrix", specInfo{}, false}, // no registry in the fixture
		{"synth", specInfo{}, false},  // registry outside types/
	}
	for _, tc := range cases {
		t.Run(tc.cat, func(t *testing.T) {
			def, _ := findCategoryDef(tc.cat)
			got, ok := r.findSpec(def, ti)
			if ok != tc.ok || got != tc.want {
				t.Errorf("findSpec(%s) = %+v, %v; want %+v, %v", tc.cat, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestUpperSnake(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"Regression": "REGRESSION", "Matrix": "MATRIX", "PostFit": "POST_FIT"} {
		if got := upperSnake(in); got != want {
			t.Errorf("upperSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

// siteOf is the site for p in a section, or nil.
func siteOf(sec Section, p string) *Site {
	for i := range sec.Sites {
		if sec.Sites[i].Path == p {
			return &sec.Sites[i]
		}
	}
	return nil
}
