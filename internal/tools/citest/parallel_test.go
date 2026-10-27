package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestParallelCheck_Fixture(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "a_test.go", fastHdr+`func TestPar(t *testing.T) {
	t.Parallel()
}

func TestParLater(tt *testing.T) {
	x := 1
	_ = x
	tt.Parallel()
}

// serial: sets an env var with t.Setenv.
func TestSerialDoc(t *testing.T) {}

func TestSerialBody(t *testing.T) {
	// serial: mutates a package-level hook.
}

func TestMissing(t *testing.T) {
	t.Run("x", func(t *testing.T) { t.Parallel() })
}

// serial:
func TestEmptyReason(t *testing.T) {}

func TestEmptyReasonBody(t *testing.T) {
	t.Parallel()
	//serial:
	// serial:   
}

func TestMain(m *testing.M) {}
func BenchmarkX(b *testing.B) {}
func FuzzX(f *testing.F) {}
func ExampleX() {}
func Testlower(t *testing.T) {}
func TestHelper(t *testing.T, n int) {}
`)
	writeFile(t, root, "b/b_heavy_test.go", heavyHdr+"func TestHeavyMissing(t *testing.T) {}\n")
	writeFile(t, root, "b/perf_test.go", "//go:build perf\n\npackage p\n\nimport \"testing\"\n\nfunc TestTaggedMissing(t *testing.T) {}\n")
	writeFile(t, root, "nested/go.mod", "module nested\n")
	writeFile(t, root, "nested/n_test.go", fastHdr+"func TestNested(t *testing.T) {}\n")
	writeFile(t, root, "b/testdata/t_test.go", fastHdr+"func TestData(t *testing.T) {}\n")

	rep, err := parallelCheck(root)
	if err != nil {
		t.Fatal(err)
	}
	if rep.parallel != 2 || rep.serial != 2 {
		t.Errorf("parallel=%d serial=%d, want 2 and 2", rep.parallel, rep.serial)
	}
	want := []string{
		"a_test.go:22: TestMissing: add t.Parallel()",
		"a_test.go:27: TestEmptyReason: \"// serial:\" has no reason",
		"a_test.go:29: TestEmptyReasonBody: \"// serial:\" has no reason",
		"b/b_heavy_test.go:7: TestHeavyMissing: add t.Parallel()",
		"b/perf_test.go:7: TestTaggedMissing: add t.Parallel()",
	}
	if len(rep.problems) != len(want) {
		t.Fatalf("problems:\n%s\nwant %d", strings.Join(rep.problems, "\n"), len(want))
	}
	for i, w := range want {
		if !strings.HasPrefix(rep.problems[i], w) {
			t.Errorf("problem %d = %q, want prefix %q", i, rep.problems[i], w)
		}
	}
	var out bytes.Buffer
	if code, err := parallelCheckMain(root, &out); code != 1 || err != nil || !strings.Contains(out.String(), "5 to fix") {
		t.Fatalf("parallelCheckMain = %d, %v:\n%s", code, err, out.String())
	}
}

// TestTestsParallelByDefault is the parallel-by-default gate: every
// top-level Test in the root module (both tiers, any build tag) calls
// t.Parallel() or carries a "// serial: <reason>" comment. The nested
// modules are out of scope (see parallelCheck).
func TestTestsParallelByDefault(t *testing.T) {
	t.Parallel()
	rep, err := parallelCheck(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.problems) > 0 {
		t.Fatalf("%d top-level test(s) neither call t.Parallel() nor say why not (go run ./internal/tools/citest parallel-check):\n%s",
			len(rep.problems), strings.Join(rep.problems, "\n"))
	}
	if rep.parallel < 1000 {
		t.Fatalf("found only %d parallel tests: the scan is broken", rep.parallel)
	}
}
