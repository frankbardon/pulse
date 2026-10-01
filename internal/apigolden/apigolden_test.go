package apigolden

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

const (
	modulePath = "github.com/frankbardon/pulse"
	goldenName = "public_api.txt"
	hashMarker = "\n// golden-hash: "
)

// TestPublicAPIGolden freezes the exported shape of every public
// (non-internal/, non-cmd/) package in the module, root aliases expanded
// into their targets' fields and method sets. Regenerate with
//
//	go test ./internal/apigolden/ -run TestPublicAPIGolden -update
//
// ONLY when you intentionally change the public surface; the diff is the
// surface delta reviewers read.
func TestPublicAPIGolden(t *testing.T) {
	pkgs, err := Load(filepath.Join("..", ".."), modulePath)
	if err != nil {
		t.Fatalf("loading public packages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no public packages loaded")
	}
	got := Render(modulePath, pkgs)

	// Determinism: a second render of the same packages is byte-identical.
	if again := Render(modulePath, pkgs); string(again) != string(got) {
		t.Fatal("Render is not deterministic across calls")
	}

	goldenPath := filepath.Join("testdata", goldenName)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, appendHash(got), 0o644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		return
	}
	content, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v (run with -update to create)", goldenPath, err)
	}
	want, _ := splitHash(content)
	if string(got) != string(want) {
		t.Errorf("public API surface changed; if intentional, regenerate with "+
			"`go test ./internal/apigolden/ -run TestPublicAPIGolden -update`.\n%s",
			lineDiff(string(want), string(got)))
	}
}

// TestGoldensNotHandEdited verifies every golden under this package's
// testdata/ carries a valid // golden-hash: footer. Mirrors the same gate
// in descriptor/ (already listed by name in CLAUDE.md).
func TestGoldensNotHandEdited(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join("testdata", entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		data, stored := splitHash(content)
		if stored == "" {
			t.Errorf("%s: no golden-hash found (file may have been hand-edited)", entry.Name())
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != stored {
			t.Errorf("%s: hash mismatch (file may have been hand-edited)", entry.Name())
		}
	}
}

func TestIsPublicPackage(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		{"root", modulePath, true},
		{"nested", modulePath + "/io/csv", true},
		{"internal top", modulePath + "/internal/cli", false},
		{"internal nested", modulePath + "/io/internal/x", false},
		{"cmd", modulePath + "/cmd/pulse", false},
		{"cmd root", modulePath + "/cmd", false},
		{"test variant", modulePath + "/io [" + modulePath + "/io.test]", false},
		{"test main", modulePath + "/io.test", false},
		{"other module", "github.com/other/mod", false},
		{"prefix collision", modulePath + "x/y", false},
		{"cmdline is not cmd", modulePath + "/cmdline", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPublicPackage(modulePath, tc.path); got != tc.want {
				t.Fatalf("IsPublicPackage(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// Fixture: a public root package aliasing a type that lives in an
// internal package. apidiff sees only the alias name; the dump must see the
// target's shape.
const fixtureInternal = `package impl

type Target struct {
	Name  string ` + "`json:\"name\"`" + `
	Count int
	hidden bool
	Embedded
}

type Embedded struct{ Promoted float64 }

func (t Target) Value() int        { return 0 }
func (t *Target) Mutate(n int) error { return nil }
func (t *Target) private()          {}

type Iface interface {
	Do(x int) (string, error)
	hide()
}
`

const fixtureRoot = `package root

import "example.com/mod/internal/impl"

type Target = impl.Target
type Iface = impl.Iface
type Pair[T any] = struct{ A, B T }

const Answer = 42

var Default Target

func New(n int) *Target { return nil }
`

// renderFixture type-checks the two fixture packages (after applying
// mutate to the internal one) and renders only the ROOT package, so every
// target fact in the output arrived via alias expansion.
func renderFixture(t *testing.T, mutate func(string) string) string {
	t.Helper()
	fset := token.NewFileSet()
	check := func(path, src string, imp types.Importer) *types.Package {
		f, err := parser.ParseFile(fset, path+".go", src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		conf := types.Config{Importer: imp}
		pkg, err := conf.Check(path, fset, []*ast.File{f}, nil)
		if err != nil {
			t.Fatalf("check %s: %v", path, err)
		}
		return pkg
	}
	internalPkg := check("example.com/mod/internal/impl", mutate(fixtureInternal), importer.Default())
	rootPkg := check("example.com/mod", fixtureRoot, importerFunc(func(path string) (*types.Package, error) {
		if path == internalPkg.Path() {
			return internalPkg, nil
		}
		return importer.Default().Import(path)
	}))
	return string(Render("example.com/mod", []*types.Package{rootPkg}))
}

type importerFunc func(string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

func TestRender_AliasExpandsTargetShape(t *testing.T) {
	base := renderFixture(t, func(s string) string { return s })
	for _, want := range []string{
		"pulse: package root",
		"pulse: const Answer untyped int = 42",
		"pulse: func New(n int) *pulse.Target",
		"pulse: var Default pulse.Target",
		"pulse: type Target = pulse/internal/impl.Target",
		"pulse: type Target underlying struct",
		`pulse: type Target field Name string "json:\"name\""`,
		"pulse: type Target field Count int",
		"pulse: type Target embed pulse/internal/impl.Embedded",
		"pulse: type Target field Embedded.Promoted float64",
		"pulse: type Target method (Target) Value() int",
		"pulse: type Target method (*Target) Mutate(n int) error",
		"pulse: type Iface = pulse/internal/impl.Iface",
		"pulse: type Iface imethod Do(x int) (string, error)",
		"pulse: type Iface imethod <unexported>",
		"pulse: type Pair[T any] = struct{A T; B T}",
		"pulse: type Pair field A T",
	} {
		if !strings.Contains(base, want+"\n") {
			t.Errorf("dump missing line %q\n--- dump ---\n%s", want, base)
		}
	}
	for _, banned := range []string{"hidden", "private", "hide"} {
		if strings.Contains(base, banned) {
			t.Errorf("dump leaks unexported %q:\n%s", banned, base)
		}
	}

	// Every mutation of the INTERNAL target must change the ROOT dump.
	mutations := []struct {
		name, from, to string
	}{
		{"field type", "Count int", "Count int64"},
		{"field removed", "Count int\n", "\n"},
		{"tag changed", `json:"name"`, `json:"nm"`},
		{"method signature", "Mutate(n int) error", "Mutate(n uint) error"},
		{"receiver kind", "func (t Target) Value()", "func (t *Target) Value()"},
		{"promoted field", "Promoted float64", "Promoted float32"},
		{"interface method", "Do(x int) (string, error)", "Do(x int) string"},
		{"kind change", "type Target struct {", "type Target = struct {"},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			if !strings.Contains(fixtureInternal, m.from) {
				t.Fatalf("fixture does not contain %q", m.from)
			}
			mutated := func(s string) string { return strings.Replace(s, m.from, m.to, 1) }
			if m.name == "kind change" {
				// A struct type that becomes an alias of a struct literal
				// cannot carry methods; drop them so the fixture compiles.
				mutated = func(s string) string {
					s = strings.Replace(s, m.from, m.to, 1)
					s = strings.Replace(s, "func (t Target) Value() int        { return 0 }\n", "", 1)
					s = strings.Replace(s, "func (t *Target) Mutate(n int) error { return nil }\n", "", 1)
					return strings.Replace(s, "func (t *Target) private()          {}\n", "", 1)
				}
			}
			if got := renderFixture(t, mutated); got == base {
				t.Fatalf("mutation %q of the aliased internal type did not change the dump", m.name)
			}
		})
	}
}

func appendHash(data []byte) []byte {
	sum := sha256.Sum256(data)
	return append(append([]byte(nil), data...), []byte(hashMarker+hex.EncodeToString(sum[:])+"\n")...)
}

func splitHash(content []byte) (data []byte, hash string) {
	s := string(content)
	idx := strings.LastIndex(s, hashMarker)
	if idx < 0 {
		return content, ""
	}
	return []byte(s[:idx]), strings.TrimSpace(s[idx+len(hashMarker):])
}

// lineDiff reports lines only in want (-) or only in got (+), capped so a
// large surface change still yields a readable failure.
func lineDiff(want, got string) string {
	wantSet := map[string]bool{}
	for _, l := range strings.Split(want, "\n") {
		wantSet[l] = true
	}
	gotSet := map[string]bool{}
	for _, l := range strings.Split(got, "\n") {
		gotSet[l] = true
	}
	var b strings.Builder
	n := 0
	emit := func(sign, l string) {
		if n < 200 {
			b.WriteString(sign + " " + l + "\n")
		}
		n++
	}
	for _, l := range strings.Split(want, "\n") {
		if !gotSet[l] {
			emit("-", l)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if !wantSet[l] {
			emit("+", l)
		}
	}
	if n > 200 {
		b.WriteString("... (" + strconv.Itoa(n-200) + " more differing lines)\n")
	}
	return b.String()
}
