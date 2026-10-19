package pulse

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// panicCodeToken is the code the gate hunts, spelled in two halves so
// this file's own source stays free of the literal.
const panicCodeToken = "PROCESSING" + "_PANIC"

// panicSuiteFile reports whether a repo-relative *_test.go path belongs
// to the operation panic suite — the only tests that may expect the
// panic code:
//
//   - any file whose base name starts with "panic_recovery" and ends in
//     "_test.go", in ANY package (root panic_recovery_test.go and its
//     panic_recovery_*_test.go siblings; internal/service's own
//     panic_recovery*_test.go; a CLI or MCP envelope test likewise);
//   - any test file of internal/oppanic, the shared constructor.
func panicSuiteFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := filepath.Base(rel)
	if strings.HasPrefix(base, "panic_recovery") && strings.HasSuffix(base, "_test.go") {
		return true
	}
	return filepath.Dir(rel) == "internal/oppanic"
}

// panicCodeOutsideSuite walks root and returns every *_test.go (nested
// modules included; hidden directories and node_modules skipped) that
// names the panic code but is not a panic-suite file.
func panicCodeOutsideSuite(root string) ([]string, error) {
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if panicSuiteFile(rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(src, []byte(panicCodeToken)) {
			bad = append(bad, filepath.ToSlash(rel))
		}
		return nil
	})
	return bad, err
}

// TestPanicRecoverySuiteIsolated (FR-16): no test outside the panic
// suite references the panic code. TestMain forces recovery off for
// ordinary tests, so such a test either crashes or — worse — was
// written against a recovered panic and would hide an engine bug. Put
// a test that expects the code in a panic_recovery*_test.go file.
//
// Falsified by adding a probe *_test.go naming the code outside the
// suite (reported), and by the table below.
func TestPanicRecoverySuiteIsolated(t *testing.T) {
	bad, err := panicCodeOutsideSuite(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range bad {
		t.Errorf("%s references %s outside the panic suite; move the test into a panic_recovery*_test.go file", f, panicCodeToken)
	}
}

// TestPanicRecoverySuiteIsolated_Detects pins the allowlist convention
// and that the walker reports an offending file.
func TestPanicRecoverySuiteIsolated_Detects(t *testing.T) {
	for _, c := range []struct {
		rel  string
		want bool
	}{
		{"panic_recovery_test.go", true},
		{"panic_recovery_pools_test.go", true},
		{"internal/service/panic_recovery_test.go", true},
		{"internal/service/panic_recovery_pools_test.go", true},
		{"internal/oppanic/oppanic_test.go", true},
		{"observability_test.go", false},
		{"internal/service/service_test.go", false},
		{"internal/oppanic/sub/x_test.go", false},
		{"recovery_panic_test.go", false},
	} {
		if got := panicSuiteFile(c.rel); got != c.want {
			t.Errorf("panicSuiteFile(%q) = %v, want %v", c.rel, got, c.want)
		}
	}

	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mention := "package x // " + panicCodeToken + "\n"
	write("ok_test.go", "package x\n")
	write("panic_recovery_x_test.go", mention)
	write("internal/oppanic/oppanic_test.go", mention)
	write("internal/service/stray_test.go", mention)
	write("cmd/pulse/main.go", mention)
	write(".hidden/skip_test.go", mention)
	bad, err := panicCodeOutsideSuite(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0] != "internal/service/stray_test.go" {
		t.Fatalf("offenders = %v, want [internal/service/stray_test.go]", bad)
	}
}
