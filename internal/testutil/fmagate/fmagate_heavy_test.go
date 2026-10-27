//go:build heavy

package fmagate

import (
	"os"
	"path/filepath"
	"testing"
)

// module writes a one-file scratch module holding src.
func module(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestFMAGate_SeesContractionAndBarrier pins the helper's two verdicts:
// an unbarriered `a + b*c` is reported, the float64(b*c) twin is not, and
// the cached probe passes.
func TestFMAGate_SeesContractionAndBarrier(t *testing.T) {
	t.Parallel()

	if err := probe(); err != nil {
		t.Fatal(err)
	}
	fused, err := run(module(t, probeSrc), []string{"build"}, "fmaprobe")
	if err != nil {
		t.Fatal(err)
	}
	if len(fused) != 1 {
		t.Errorf("`a + b*c` reported %d contractions, want 1: %q", len(fused), fused)
	}
	barriered := "package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + float64(b*c) }\n"
	clean, err := run(module(t, barriered), []string{"test", "-c"}, "fmaprobe")
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 0 {
		t.Errorf("`a + float64(b*c)` reported contractions: %q", clean)
	}
}
