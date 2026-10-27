package statdist

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestStatdist_StudentTBitsPinned is the cross-architecture gate on the
// Student-t family behind every engine ci_lower / ci_upper and t-based
// p-value: StudentTQuantile, StudentTTwoSidedP and StudentTCDF must
// equal the pinned bits exactly on every GOARCH. They run on
// linalg/detmath and barriered products; with math.Log / Exp / Lgamma
// and contractible products they sat up to ~100 ulp apart between arm64
// and amd64. Pinned on darwin/arm64 and checked identical on
// linux/amd64 (docker); CI re-runs it on linux/amd64.
func TestStatdist_StudentTBitsPinned(t *testing.T) {
	t.Parallel()

	quantiles := []struct {
		p, df float64
		want  uint64
	}{
		{0.975, 1, 0x40296993aacc4d1b},
		{0.975, 2, 0x401135ea98e146b9},
		{0.975, 5, 0x4004908d359dff38},
		{0.975, 9, 0x400218e5dac50b24},
		{0.975, 29, 0x40005ca15bce2873},
		{0.975, 120, 0x3fffadcb8122f64e},
		{0.995, 7, 0x400bfef119582619},
		{0.95, 15, 0x3ffc0c7e879f0d5e},
		{0.025, 3, 0xc00975a66893c1a9},
		{0.9, 4.5, 0x3ff803a0529beeef},
		{0.999, 50, 0x400a175da18c4190},
		{0.6, 11, 0x3fd09c902f0ec427},
		{0.975, 10000, 0x3fff5cfbf9ae8ac6},
		{1e-10, 3, 0xc0a16389dfae9667},
	}
	for _, c := range quantiles {
		if got := StudentTQuantile(c.p, c.df); math.Float64bits(got) != c.want {
			t.Errorf("StudentTQuantile(%v, %v) = %v (%#016x), want %v (%#016x)",
				c.p, c.df, got, math.Float64bits(got), math.Float64frombits(c.want), c.want)
		}
	}
	tails := []struct {
		t, df      float64
		twoSided   uint64
		cumulative uint64
	}{
		{2.1, 5, 0x3fb6fa11a8f6cd24, 0x3fee905ee570932e},
		{0.3, 12, 0x3fe89e30ffd5bcd4, 0x3fe3b0e780152196},
		{4.7, 30, 0x3f0c75b1331216cc, 0x3fefffc7149d99dc},
		{1.96, 1000, 0x3fa9bd682a805cc7, 0x3fef3214beabfd1a},
		{-1.2, 7, 0x3fd13a1cee6b2006, 0x3fc13a1cee6b2006},
	}
	for _, c := range tails {
		if got := StudentTTwoSidedP(c.t, c.df); math.Float64bits(got) != c.twoSided {
			t.Errorf("StudentTTwoSidedP(%v, %v) = %v (%#016x), want %#016x", c.t, c.df, got, math.Float64bits(got), c.twoSided)
		}
		if got := StudentTCDF(c.t, c.df); math.Float64bits(got) != c.cumulative {
			t.Errorf("StudentTCDF(%v, %v) = %v (%#016x), want %#016x", c.t, c.df, got, math.Float64bits(got), c.cumulative)
		}
	}
}

// fmahashReport cross-compiles dir's package for GOARCH=arm64 with the
// compiler's contraction report on and returns the report lines.
func fmahashReport(t *testing.T, dir, importPath string) []string {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", os.DevNull, "-gcflags="+importPath+"=-d=fmahash=vy", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("cross-compile for arm64: %v\n%s", err, out.String())
	}
	var lines []string
	for l := range strings.SplitSeq(out.String(), "\n") {
		if strings.Contains(l, "fmahash triggered") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestStatdist_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction in the Student-t / incomplete-beta files, so
// their bits are the same on every architecture. normal.go
// (NormalQuantile) is exempt by name: it is not on the bit contract
// (doc.go). A probe first proves the flag still reports one.
func TestStatdist_NoFMAContraction(t *testing.T) {
	t.Parallel()

	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "p.go"), []byte("package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(fmahashReport(t, probe, "fmaprobe")) == 0 {
		t.Fatal("the -d=fmahash=vy probe reported no contraction for `a + b*c`: the flag changed and this gate is vacuous")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range fmahashReport(t, wd, "github.com/frankbardon/pulse/internal/statdist") {
		if !strings.Contains(l, "normal.go:") {
			got = append(got, l)
		}
	}
	if len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in statdist's Student-t path; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
