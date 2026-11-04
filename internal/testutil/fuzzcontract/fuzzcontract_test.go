package fuzzcontract

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
)

// fakeTB records a Fatalf instead of ending the test, so the contract's
// own failures can be asserted. Fatalf does not Goexit: Run returns
// right after reporting.
type fakeTB struct {
	testing.TB
	mu    sync.Mutex
	fatal []string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fatal = append(f.fatal, fmt.Sprintf(format, args...))
}

func (f *fakeTB) failures() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fatal...)
}

// run holds target to c over input on a fresh fake and returns its
// failure messages.
func run(t *testing.T, c Contract, input []byte, target func([]byte) error) []string {
	t.Helper()
	ft := &fakeTB{TB: t}
	c.Run(ft, input, target)
	return ft.failures()
}

func requireOneFailure(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want exactly one contract failure, got %d: %q", len(got), got)
	}
	for _, w := range want {
		if !strings.Contains(got[0], w) {
			t.Fatalf("failure %q does not mention %q", got[0], w)
		}
	}
}

func TestFuzzContract_FlagsUncodedError(t *testing.T) {
	t.Parallel()
	got := run(t, Contract{}, []byte("in-uncoded"), func([]byte) error {
		return stderrors.New("raw library fault")
	})
	requireOneFailure(t, got, "uncoded error", "raw library fault", `"in-uncoded"`)
}

func TestFuzzContract_FlagsWrappedUncodedError(t *testing.T) {
	t.Parallel()
	got := run(t, Contract{}, []byte("x"), func([]byte) error {
		return fmt.Errorf("context: %w", stderrors.New("inner"))
	})
	requireOneFailure(t, got, "uncoded error")
}

func TestFuzzContract_FlagsUnregisteredCode(t *testing.T) {
	t.Parallel()
	got := run(t, Contract{}, []byte("x"), func([]byte) error {
		return perr.NewCodedError(perr.Code("NOT_A_REGISTERED_CODE"), "bogus")
	})
	requireOneFailure(t, got, "unregistered code", "NOT_A_REGISTERED_CODE")
}

func TestFuzzContract_FlagsPanic(t *testing.T) {
	t.Parallel()
	got := run(t, Contract{}, []byte("in-panic"), func([]byte) error {
		panic("boom in the target")
	})
	requireOneFailure(t, got, "panicked", `"in-panic"`, "boom in the target")
}

func TestFuzzContract_FlagsHang(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	got := run(t, Contract{Timeout: 20 * time.Millisecond}, []byte("in-hang"), func([]byte) error {
		<-release
		return nil
	})
	requireOneFailure(t, got, "did not return within", `"in-hang"`)
}

func TestFuzzContract_PassesCodedAndNil(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"nil":     nil,
		"coded":   perr.NewCodedError(perr.SERVICE_VALIDATION, "refused"),
		"wrapped": fmt.Errorf("outer: %w", perr.NewCodedError(perr.PROCESSING_CONFIG, "refused")),
	} {
		if got := run(t, Contract{}, []byte("x"), func([]byte) error { return err }); len(got) != 0 {
			t.Fatalf("%s: want no failure, got %q", name, got)
		}
	}
}

func TestFuzzContract_FlagsOverAllocation(t *testing.T) {
	// serial: measures process-wide heap allocation (runtime.MemStats),
	// which a parallel sibling's allocations would be charged to.
	var sink []byte
	got := run(t, Contract{MaxAlloc: 1 << 20}, []byte("in-alloc"), func([]byte) error {
		sink = make([]byte, 8<<20)
		return nil
	})
	requireOneFailure(t, got, "allocated", "cap 1048576", `"in-alloc"`)
	if got := run(t, Contract{MaxAlloc: 64 << 20}, []byte("x"), func([]byte) error {
		sink = make([]byte, 1<<10)
		return nil
	}); len(got) != 0 {
		t.Fatalf("an allocation under the cap was flagged: %q", got)
	}
	_ = sink
}

func TestFuzzContract_CorpusEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "good")
	want := []byte("a\x00\"b\xff")
	if err := os.WriteFile(good, []byte("go test fuzz v1\n[]byte("+strconv.Quote(string(want))+")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := CorpusEntry(t, good); !bytes.Equal(got, want) {
		t.Fatalf("CorpusEntry = %q, want %q", got, want)
	}
	bad := filepath.Join(dir, "bad")
	if err := os.WriteFile(bad, []byte("go test fuzz v1\nstring(\"x\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ft := &fakeTB{TB: t}
	if got := CorpusEntry(ft, bad); got != nil {
		t.Fatalf("a malformed corpus file returned %q", got)
	}
	requireOneFailure(t, ft.failures(), "not a one-[]byte")
}

func TestFuzzContract_CapsInput(t *testing.T) {
	t.Parallel()
	var seen int
	got := run(t, Contract{MaxInput: 8}, []byte(strings.Repeat("a", 100)), func(in []byte) error {
		seen = len(in)
		return nil
	})
	if len(got) != 0 || seen != 8 {
		t.Fatalf("want the target to see 8 bytes and no failure, saw %d, failures %q", seen, got)
	}
	seen = 0
	run(t, Contract{}, make([]byte, DefaultMaxInput+10), func(in []byte) error {
		seen = len(in)
		return nil
	})
	if seen != DefaultMaxInput {
		t.Fatalf("zero Contract: want the target to see %d bytes, saw %d", DefaultMaxInput, seen)
	}
}

func TestFuzzContract_RequireCode(t *testing.T) {
	t.Parallel()
	ft := &fakeTB{TB: t}
	RequireCode(ft, []byte("x"), string(perr.SERVICE_VALIDATION))
	if got := ft.failures(); len(got) != 0 {
		t.Fatalf("registered code flagged: %q", got)
	}
	RequireCode(ft, []byte("x"), "PROCESS_ERROR")
	requireOneFailure(t, ft.failures(), "unregistered code", "PROCESS_ERROR")
}

func TestFuzzContract_LimitsAreTight(t *testing.T) {
	t.Parallel()
	l := Limits()
	if l.RequestTimeout <= 0 || l.MaxGroups <= 0 || l.MaxCrosstabCells <= 0 || l.MaxEstimatedMemory <= 0 ||
		l.MaxMatrixDim <= 0 || l.MaxComposeSlots <= 0 || l.MaxChainStages <= 0 || l.MaxJoinBuildRows <= 0 ||
		l.MaxDerivedColumns <= 0 {
		t.Fatalf("every fuzz limit must be a positive cap (0 = default, -1 = unlimited): %+v", l)
	}
}

const importPath = "github.com/frankbardon/pulse/internal/testutil/fuzzcontract"

// walkModule calls visit on every .go file of the module (no nested
// module, testdata, vendor or dot directory), with its root-relative
// path.
func walkModule(t *testing.T, visit func(p, rel string) error) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor") {
				return filepath.SkipDir
			}
			if p != root {
				if _, err := os.Stat(filepath.Join(p, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		return visit(p, rel)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFuzzContract_TestOnlyImporters keeps the helper test-only: no
// non-test file of the module imports it.
func TestFuzzContract_TestOnlyImporters(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	walkModule(t, func(p, rel string) error {
		if strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == importPath {
				t.Errorf("%s: non-test file imports %s", rel, importPath)
			}
		}
		return nil
	})
}

// TestFuzzContract_FuzzBodiesRunAlone keeps Contract.MaxAlloc sound.
// The figure is process-wide (runtime.MemStats.TotalAlloc), so it holds
// only while nothing else runs: `go test` reaches a package's fuzz
// targets after all its tests have finished, and a -fuzz worker process
// runs one input at a time. A fuzz body calling t.Parallel would run
// seeds concurrently and charge one seed's allocation to another — so
// no f.Fuzz callback in a file using this package may call Parallel.
func TestFuzzContract_FuzzBodiesRunAlone(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	walkModule(t, func(p, rel string) error {
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if !bytes.Contains(src, []byte(importPath)) {
			return nil
		}
		f, err := parser.ParseFile(fset, p, src, 0)
		if err != nil {
			return err
		}
		for _, line := range fuzzBodyParallelCalls(fset, f) {
			t.Errorf("%s:%d: a fuzz body calls Parallel; Contract.MaxAlloc measures process-wide allocation", rel, line)
		}
		return nil
	})
}

// fuzzBodyParallelCalls returns the lines of every X.Parallel() call
// inside a function literal passed to an X.Fuzz(...) call in f.
func fuzzBodyParallelCalls(fset *token.FileSet, f *ast.File) []int {
	var lines []int
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Fuzz" {
			return true
		}
		for _, arg := range call.Args {
			lit, ok := arg.(*ast.FuncLit)
			if !ok {
				continue
			}
			ast.Inspect(lit.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Parallel" && len(c.Args) == 0 {
						lines = append(lines, fset.Position(c.Pos()).Line)
					}
				}
				return true
			})
		}
		return true
	})
	return lines
}

func TestFuzzContract_FuzzBodyParallelDetector(t *testing.T) {
	t.Parallel()
	const src = `package x
func FuzzA(f *testing.F) { f.Fuzz(func(t *testing.T, in []byte) { t.Parallel() }) }
func FuzzB(f *testing.F) { f.Fuzz(func(t *testing.T, in []byte) {}) }
func TestC(t *testing.T) { t.Parallel() }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x_test.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := fuzzBodyParallelCalls(fset, f); len(got) != 1 || got[0] != 2 {
		t.Fatalf("want the one Parallel inside a fuzz body (line 2), got %v", got)
	}
}
