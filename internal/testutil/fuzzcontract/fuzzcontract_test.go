package fuzzcontract

import (
	"bytes"
	stderrors "errors"
	"fmt"
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
	t.Parallel()
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

// TestFuzzContract_TestOnlyImporters keeps the helper test-only: no
// non-test file of the module imports it.
func TestFuzzContract_TestOnlyImporters(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
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
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path == importPath {
				rel, _ := filepath.Rel(root, p)
				t.Errorf("%s: non-test file imports %s", rel, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
