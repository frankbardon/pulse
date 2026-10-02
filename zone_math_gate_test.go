package pulse_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// zoneMathHome is the one package allowed to hold epoch-day math and
// time-zone construction. Every other production file routes through it.
const zoneMathHome = "internal/temporal"

// bannedTimeSelectors are the `time` package members that conjure a
// non-UTC location (or the host's zone). With every one of them banned
// outside internal/temporal, only time.UTC can reach a `.In(` call, which
// is why `.In(` itself is deliberately NOT banned (it would also hit
// reflect.Type.In).
var bannedTimeSelectors = map[string]bool{
	"LoadLocation":           true,
	"LoadLocationFromTZData": true,
	"FixedZone":              true,
	"Local":                  true,
}

// zoneMathAllowed is the narrow allow-list: file (slash path relative to
// the module root) -> the const/var names whose declaration may carry a
// banned token. encoding.SecondsPerDay is a frozen public re-export
// (TestPublicAPIGolden) forwarding to temporal.SecondsPerDay; only that
// declaration is exempt — the rest of encoding/ is scanned like any file.
var zoneMathAllowed = map[string]map[string]bool{
	"encoding/datetime.go": {"SecondsPerDay": true},
}

// zoneMathSkipDirs are directory base names never descended into.
var zoneMathSkipDirs = map[string]bool{
	"vendor":       true,
	"testdata":     true,
	"node_modules": true,
}

// TestNoZoneMathOutsideTemporal walks every non-test Go file in the
// module (cmd/ and the internal/embeddersmoke nested module included)
// outside internal/temporal and fails on any open-coded epoch-day factor
// or zone construction: the int literal 86400, any SecondsPerDay
// identifier or selector, time.LoadLocation / LoadLocationFromTZData /
// FixedZone / Local (the `time` import alias resolved per file), and an
// import of "time/tzdata".
func TestNoZoneMathOutsideTemporal(t *testing.T) {
	fset := token.NewFileSet()
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		if d.IsDir() {
			base := d.Name()
			if rel != "." && (strings.HasPrefix(base, ".") || strings.HasPrefix(base, "_") || zoneMathSkipDirs[base]) {
				return filepath.SkipDir
			}
			if rel == zoneMathHome {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		scanned++
		for _, v := range checkZoneMath(fset, rel, src) {
			t.Error(v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d production files; the walk is not reaching the module", scanned)
	}
}

// checkZoneMath parses one source file and returns one violation message
// per banned construct. A parse failure is itself reported.
func checkZoneMath(fset *token.FileSet, filename string, src []byte) []string {
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse: %v", filename, err)}
	}
	var out []string
	report := func(pos token.Pos, construct string) {
		p := fset.Position(pos)
		out = append(out, fmt.Sprintf("%s:%d: %s outside %s — route epoch-day / zone math through %s",
			filename, p.Line, construct, zoneMathHome, zoneMathHome))
	}

	timeAliases := map[string]bool{}
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		switch path {
		case "time/tzdata":
			report(imp.Pos(), `import "time/tzdata"`)
		case "time":
			name := "time"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			switch name {
			case "_":
			case ".":
				report(imp.Pos(), `dot-import of "time" (defeats zone-constructor detection)`)
			default:
				timeAliases[name] = true
			}
		}
	}

	allowed := zoneMathAllowed[filename]
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ValueSpec:
			for _, name := range x.Names {
				if allowed[name.Name] {
					return false // the allow-listed declaration, name and value
				}
			}
		case *ast.BasicLit:
			if x.Kind == token.INT {
				if v, err := strconv.ParseInt(strings.ReplaceAll(x.Value, "_", ""), 0, 64); err == nil && v == 86400 {
					report(x.Pos(), fmt.Sprintf("int literal %s (seconds per day)", x.Value))
				}
			}
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && timeAliases[id.Name] && bannedTimeSelectors[x.Sel.Name] {
				report(x.Pos(), fmt.Sprintf("time.%s (as %s.%s)", x.Sel.Name, id.Name, x.Sel.Name))
				return false
			}
			if x.Sel.Name == "SecondsPerDay" {
				report(x.Pos(), "selector ."+x.Sel.Name)
				return false
			}
		case *ast.Ident:
			if x.Name == "SecondsPerDay" {
				report(x.Pos(), "identifier SecondsPerDay")
			}
		}
		return true
	})
	return out
}

// TestNoZoneMathOutsideTemporal_Falsification proves the checker bites on
// each banned construct and stays quiet on the look-alikes it must allow.
func TestNoZoneMathOutsideTemporal_Falsification(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		src     string
		wantHit string // "" means the source must pass
	}{
		{"int literal 86400", "p/a.go", "package p\nfunc f(x int64) int64 { return x * 86400 }\n", "int literal 86400"},
		{"hex 86400", "p/a.go", "package p\nconst d = 0x15180\n", "int literal 0x15180"},
		{"underscore 86400", "p/a.go", "package p\nconst d = 86_400\n", "int literal 86_400"},
		{"time.LoadLocation", "p/a.go", "package p\nimport \"time\"\nvar l, _ = time.LoadLocation(\"UTC\")\n", "time.LoadLocation"},
		{"time.LoadLocationFromTZData", "p/a.go", "package p\nimport \"time\"\nvar l, _ = time.LoadLocationFromTZData(\"X\", nil)\n", "time.LoadLocationFromTZData"},
		{"aliased t.FixedZone", "p/a.go", "package p\nimport t \"time\"\nvar z = t.FixedZone(\"X\", 3600)\n", "time.FixedZone (as t.FixedZone)"},
		{"aliased tm.Local", "p/a.go", "package p\nimport tm \"time\"\nvar z = tm.Local\n", "time.Local (as tm.Local)"},
		{"tzdata import", "p/a.go", "package p\nimport _ \"time/tzdata\"\n", `import "time/tzdata"`},
		{"dot import time", "p/a.go", "package p\nimport . \"time\"\nvar z = Local\n", "dot-import"},
		{"encoding.SecondsPerDay selector", "p/a.go", "package p\nimport \"github.com/frankbardon/pulse/encoding\"\nvar d = 3 * encoding.SecondsPerDay\n", "selector .SecondsPerDay"},
		{"bare SecondsPerDay ident", "p/a.go", "package p\nconst SecondsPerDay = 24 * 60 * 60\n", "identifier SecondsPerDay"},
		{"allow-list is per file", "encoding/date.go", "package encoding\nconst SecondsPerDay = 86400\n", "SecondsPerDay"},
		{"allow-list covers only the named decl", "encoding/datetime.go", "package encoding\nconst SecondsPerDay = x\nfunc f(v int64) int64 { return v / 86400 }\n", "int literal 86400"},
		{"allow-listed decl passes", "encoding/datetime.go", "package encoding\nimport \"github.com/frankbardon/pulse/internal/temporal\"\nconst SecondsPerDay = temporal.SecondsPerDay\n", ""},
		{"reflect In and time.UTC In pass", "p/a.go", "package p\nimport (\n\t\"reflect\"\n\t\"time\"\n)\nfunc f(r reflect.Type, t time.Time) (reflect.Type, time.Time) { return r.In(0), t.In(time.UTC) }\n", ""},
		{"non-time Local selector passes", "p/a.go", "package p\nimport tm \"example.com/tm\"\nimport \"time\"\nvar a, b = tm.Local, time.UTC\ntype s struct{ Local int }\nvar c = s{}.Local\n", ""},
		{"unaliased time ident on other pkg passes", "p/a.go", "package p\nimport time \"example.com/clock\"\nvar z = time.Local\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkZoneMath(token.NewFileSet(), tc.file, []byte(tc.src))
			if tc.wantHit == "" {
				if len(got) != 0 {
					t.Fatalf("want pass, got %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("want a violation containing %q, got none", tc.wantHit)
			}
			msg := got[0]
			for _, want := range []string{tc.file + ":", tc.wantHit, zoneMathHome} {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q missing %q", msg, want)
				}
			}
		})
	}
}
