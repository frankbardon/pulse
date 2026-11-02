package descriptor

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/weighting"
)

// validationRoot is the repository root, which every cited test path
// is relative to.
var validationRoot = filepath.Join("..", "..")

// validationCovered is every catalog category whose built-in operators
// must all carry a validation record. Overlays are covered by their
// Inferential flag instead (validationRequired).
var validationCovered = []string{"test", "regression", "matrix"}

// validationRequired returns every built-in operator the registry must
// cover: each test, regression and matrix operator, and each
// inferential overlay kind.
func validationRequired() map[string]bool {
	req := map[string]bool{}
	for _, s := range PurposeSurfaces() {
		if slices.Contains(validationCovered, s.Category) {
			for _, n := range s.Names {
				req[n] = true
			}
		}
	}
	for _, c := range OverlayCapabilities() {
		if c.Inferential {
			req[string(c.Kind)] = true
		}
	}
	return req
}

// TestValidationRegistryCoversInferential: no inferential built-in is
// silently missing from "How Pulse is validated". An operator without
// an external reference still needs an entry, declaring the gap.
func TestValidationRegistryCoversInferential(t *testing.T) {
	t.Parallel()
	for name := range validationRequired() {
		v, ok := validationRegistry[name]
		if !ok {
			t.Errorf("%s: no validation record (add one to validationRegistry, declaring a Gap if it has no external reference)", name)
			continue
		}
		if len(v.Refs) == 0 && v.Gap == "" {
			t.Errorf("%s: a record with no reference must declare its Gap", name)
		}
	}
}

// TestValidationRegistryNamesBuiltins: every key is a built-in operator,
// so a rename or removal cannot leave a stale claim behind.
func TestValidationRegistryNamesBuiltins(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	for _, s := range PurposeSurfaces() {
		for _, n := range s.Names {
			known[n] = true
		}
	}
	for name := range validationRegistry {
		if !known[name] {
			t.Errorf("%s: validation record for an operator that is not a built-in", name)
		}
	}
}

// TestValidationReferencesExist: every cited test file exists and
// exercises the operator it is cited for, so no claim is published
// without the harness behind it (U64 E1-S3). A file exercises the
// operator when it names it — its wire name, or a Go constant in
// package types spelling it — or reads a golden under its package's
// testdata/ that does. Two shapes of harness name it only through a
// file cited beside them in the same reference and package: a runner
// that reads a table declared in that file (the weighted reference's
// runner and its generated values), and a kernel oracle that pins a
// function the routing test beside it also calls (the shared
// two-sample kernels, pinned against R once).
func TestValidationReferencesExist(t *testing.T) {
	t.Parallel()
	for _, name := range ValidationOperators() {
		v := validationRegistry[name]
		for _, ref := range v.Refs {
			if ref.Against == "" || ref.Tol == "" || len(ref.Tests) == 0 {
				t.Errorf("%s: reference %+v needs Against, Tol and at least one test file", name, ref)
			}
			checkCitedTests(t, name, ref.Tests)
		}
		checkCitedTests(t, name, v.WeightRefused)
	}
}

// checkCitedTests applies the exercise rule to the files one reference
// cites together.
func checkCitedTests(t *testing.T, name string, files []string) {
	t.Helper()
	for _, f := range files {
		if !strings.HasSuffix(f, "_test.go") {
			t.Errorf("%s: %s is not a test file", name, f)
		}
	}
	bad, err := unexercised(name, files)
	if err != nil {
		t.Errorf("%s: %v", name, err)
	}
	for _, f := range bad {
		t.Errorf("%s: cited test file %s never names %s (nor a Go constant spelling it: %v), reads no golden that does, and neither reads a table nor pins a kernel of a file cited beside it that does; cite the test that runs it",
			name, f, name, operatorSpellings()[name])
	}
}

// unexercised returns the files, cited together for op, that do not
// exercise it; an unreadable file is an error.
func unexercised(op string, files []string) ([]string, error) {
	srcs := map[string][]byte{}
	direct := map[string]bool{}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(validationRoot, filepath.FromSlash(f)))
		if err != nil {
			return nil, err
		}
		srcs[f] = src
		direct[f] = testExercises(f, src, op)
	}
	var bad []string
	for _, f := range files {
		if !direct[f] && !exercisedBeside(f, srcs[f], srcs, direct) {
			bad = append(bad, f)
		}
	}
	return bad, nil
}

// exercisedBeside reports whether f, which does not name the operator
// itself, reaches it through a file s cited beside it in the same
// package that does: f uses a top-level declaration of s (a runner
// reading s's table), or f and s both call a function the package
// declares outside its tests (a kernel oracle and the routing test
// running that kernel on the legs it reads).
func exercisedBeside(f string, src []byte, srcs map[string][]byte, direct map[string]bool) bool {
	used := identsUsed(src)
	for s, ok := range direct {
		if !ok || s == f || path.Dir(s) != path.Dir(f) {
			continue
		}
		for _, id := range topLevelDecls(srcs[s]) {
			if used[id] {
				return true
			}
		}
		sUsed := identsUsed(srcs[s])
		for _, id := range packageFuncs(path.Dir(f)) {
			if used[id] && sUsed[id] {
				return true
			}
		}
	}
	return false
}

// identsUsed returns every identifier appearing in src.
func identsUsed(src []byte) map[string]bool {
	out := map[string]bool{}
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return out
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out[id.Name] = true
		}
		return true
	})
	return out
}

// topLevelDecls returns the names src declares at package level.
func topLevelDecls(src []byte) []string {
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range file.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && !strings.HasPrefix(d.Name.Name, "Test") {
				out = append(out, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.ValueSpec:
					for _, id := range sp.Names {
						out = append(out, id.Name)
					}
				case *ast.TypeSpec:
					out = append(out, sp.Name.Name)
				}
			}
		}
	}
	return out
}

// packageFuncs returns the package-level functions the package at dir
// (repo-relative) declares in its non-test files.
func packageFuncs(dir string) []string {
	pkgFuncsMu.Lock()
	defer pkgFuncsMu.Unlock()
	if cached, ok := pkgFuncs[dir]; ok {
		return cached
	}
	var out []string
	for _, file := range parseNonTestFiles(filepath.Join(validationRoot, filepath.FromSlash(dir))) {
		for _, d := range file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				out = append(out, fd.Name.Name)
			}
		}
	}
	pkgFuncs[dir] = out
	return out
}

var (
	pkgFuncsMu sync.Mutex
	pkgFuncs   = map[string][]string{}
)

// parseNonTestFiles parses every non-test .go file directly in dir;
// an unparseable file is skipped.
func parseNonTestFiles(dir string) []*ast.File {
	entries, _ := os.ReadDir(dir)
	var out []*ast.File
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, n), nil, 0); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// testExercises reports whether the test file f (repo-relative, source
// src) exercises op: the file names op by its wire name or by a Go
// constant whose value is op, or one of the golden files it reads — a
// string literal naming a file under its package's testdata/ — does.
func testExercises(f string, src []byte, op string) bool {
	if namesOperator(src, op) {
		return true
	}
	for _, g := range goldensRead(f, src) {
		if b, err := os.ReadFile(g); err == nil && namesOperator(b, op) {
			return true
		}
	}
	return false
}

// namesOperator reports whether text names op as a whole word, by its
// wire name or a Go constant spelling it.
func namesOperator(text []byte, op string) bool {
	for _, s := range append([]string{op}, operatorSpellings()[op]...) {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(s) + `\b`).Match(text) {
			return true
		}
	}
	return false
}

// operatorSpellings maps an operator's wire name to every exported Go
// constant in package types whose value it is
// (OverlayKindChiSqRow = "OVERLAY_CHISQ_ROW"), so a test naming the
// operator through its constant counts.
var operatorSpellings = sync.OnceValue(func() map[string][]string {
	out := map[string][]string{}
	for _, file := range parseNonTestFiles(filepath.Join(validationRoot, "types")) {
		for _, d := range file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, id := range vs.Names {
					if i >= len(vs.Values) || !id.IsExported() {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, err := strconv.Unquote(lit.Value)
					if err == nil && v != id.Name && !slices.Contains(out[v], id.Name) {
						out[v] = append(out[v], id.Name)
					}
				}
			}
		}
	}
	return out
})

// stringLit is a Go string literal.
var stringLit = regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"`)

// goldensRead returns the files under the test package's testdata/
// tree that a string literal in src names: by its path under
// testdata/, its base name, or its base name without the extension
// (loadReference(t, "overlay_two_sample")).
func goldensRead(f string, src []byte) []string {
	dir := filepath.Join(validationRoot, filepath.FromSlash(path.Dir(f)), "testdata")
	lits := map[string]bool{}
	for _, m := range stringLit.FindAllSubmatch(src, -1) {
		if l := strings.TrimPrefix(string(m[1]), "testdata/"); l != "" {
			lits[l] = true
		}
	}
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		base := d.Name()
		if lits[filepath.ToSlash(rel)] || lits[base] || lits[strings.TrimSuffix(base, filepath.Ext(base))] {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestValidationExercisesRule pins the rule TestValidationReferencesExist
// applies: a file that names the operator, by wire name or constant,
// passes; one that only names a longer operator sharing its prefix, or
// none, fails; a golden read by base name counts.
func TestValidationExercisesRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		src  string
		op   string
		want bool
	}{
		{`req := types.Test{Type: "TEST_T"}`, "TEST_T", true},
		{`req := types.Test{Type: types.TEST_T}`, "TEST_T", true},
		{`spec := types.OverlaySpec{Kind: types.OverlayKindChiSqRow}`, "OVERLAY_CHISQ_ROW", true},
		{`req := types.Test{Type: "TEST_TREND"}`, "TEST_T", false},
		{`func TestNothing(t *testing.T) {}`, "TEST_T", false},
	}
	for _, c := range cases {
		if got := testExercises("internal/none/x_test.go", []byte(c.src), c.op); got != c.want {
			t.Errorf("%q for %s: exercises = %v, want %v", c.src, c.op, got, c.want)
		}
	}
	// strd_reference_test.go names REG_OLS itself; the processing
	// oracle reads test_kruskal_wallis.json, which names its operator.
	src := []byte(`load(t, "test_kruskal_wallis.json")`)
	if !testExercises("internal/processing/x_test.go", src, "TEST_KRUSKAL_WALLIS") {
		t.Error("a golden read by base name under the package's testdata/ does not count")
	}
	if testExercises("internal/processing/x_test.go", src, "TEST_BOX_M") {
		t.Error("a golden that never names the operator counts")
	}
	// Beside rules, on the registry's own citations: the weighted
	// runner reaches TEST_T only through the values table cited beside
	// it, and the two-sample kernel oracle reaches OVERLAY_PROP_Z_CELL
	// only through the routing test calling the same kernel.
	const kernel, routing = "internal/processing/overlay_reference_oracle_test.go", "internal/processing/overlay_weighted_contingency_test.go"
	for _, c := range []struct {
		op    string
		files []string
		bad   []string
	}{
		{"TEST_T", []string{wrVals, wrTest}, nil},
		{"TEST_T", []string{wrTest}, []string{wrTest}},
		{"OVERLAY_PROP_Z_CELL", []string{kernel, routing}, nil},
		{"OVERLAY_PROP_Z_CELL", []string{kernel}, []string{kernel}},
		{"OVERLAY_PROP_Z_CELL", []string{kernel, "internal/processing/overlay_pairwise_test.go"}, []string{kernel, "internal/processing/overlay_pairwise_test.go"}},
	} {
		bad, err := unexercised(c.op, c.files)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(bad, c.bad) {
			t.Errorf("%s over %v: unexercised %v, want %v", c.op, c.files, bad, c.bad)
		}
	}
}

// TestValidationWeightRefusalIsPermanent: a record claiming a pinned
// weight refusal names an operator the weighting table refuses
// permanently, with a stated reason; once the operator gains a weighted
// form the claim fails here instead of going stale.
func TestValidationWeightRefusalIsPermanent(t *testing.T) {
	t.Parallel()
	for name, v := range validationRegistry {
		if len(v.WeightRefused) == 0 {
			continue
		}
		if v.PostTestOnly {
			if PostTestWeightRefusal() == "" || weighting.ClassOf(name) != weighting.ClassRefuse {
				t.Errorf("%s: post-test WeightRefused without a refused class and the shared post-test reason", name)
			}
		} else if reason, ok := OperatorWeightRefusal(name); !ok || reason != weighting.RefusalReason(name) {
			t.Errorf("%s: WeightRefused, but weighting does not refuse it permanently (class %v, reason %q)",
				name, weighting.ClassOf(name), weighting.RefusalReason(name))
		}
		for _, ref := range v.Refs {
			if ref.Weighted {
				t.Errorf("%s: both a weighted reference and a weight refusal", name)
			}
		}
	}
}

// TestValidation_ScrubsHiddenNames: the instance view names nothing the
// instance hides — a reference whose description names a hidden
// operator is dropped (REG_GLM's collinearity check runs end to end
// through FEAT_ONE_HOT) — and is the registry's own text otherwise.
func TestValidation_ScrubsHiddenNames(t *testing.T) {
	t.Parallel()
	var enabled, hidden []string
	for _, n := range FeatureNames() {
		if n == "FEAT_ONE_HOT" {
			hidden = append(hidden, n)
		} else {
			enabled = append(enabled, n)
		}
	}
	all, _ := NewInstanceSnapshot(nil, FeatureSet{Enabled: FeatureNames()}).Validation("REG_GLM")
	scoped, _ := NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hidden}).Validation("REG_GLM")
	named := func(v []string) int {
		n := 0
		for _, s := range v {
			if strings.Contains(s, "FEAT_ONE_HOT") {
				n++
			}
		}
		return n
	}
	against := func(refs []descriptor.ValidationReference) []string {
		out := make([]string, len(refs))
		for i, r := range refs {
			out[i] = r.Against
		}
		return out
	}
	if named(against(all.References)) == 0 {
		t.Fatal("REG_GLM: no reference names FEAT_ONE_HOT; pick another probe")
	}
	if n := named(against(scoped.References)); n != 0 || len(scoped.References) != len(all.References)-named(against(all.References)) {
		t.Errorf("FEAT_ONE_HOT hidden: %d references still name it (%d of %d kept)", n, len(scoped.References), len(all.References))
	}
	rec, _ := ValidationRecordOf("REG_GLM")
	if len(all.References) != len(rec.Refs) || all.Gap != rec.Gap {
		t.Errorf("unscoped view differs from the record: %d / %d references", len(all.References), len(rec.Refs))
	}
}
