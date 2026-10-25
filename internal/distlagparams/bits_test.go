package distlagparams

import (
	"bytes"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDistLag_WeightBitsPinned is the cross-architecture gate on the
// WIN_DISTRIBUTED_LAG kernel: every kernel's weights (raw and
// normalised) must equal the pinned bits exactly on every GOARCH. The
// weights run on linalg/detmath; stdlib math.Exp / Log / Pow put them
// an ulp apart between amd64 and arm64 (the Weibull rows below are
// inputs where they did, and weibull_pdf shape 1.3 / scale 4.2 lag 8 is
// one where arm64's own math.* leaves detmath's bits). Pinned on
// darwin/arm64 and checked identical on linux/amd64 (docker); CI
// re-runs it on linux/amd64.
func TestDistLag_WeightBitsPinned(t *testing.T) {
	cases := []struct {
		params string
		want   []uint64
	}{
		{`{"kernel":"geometric","decay":0.7,"max_lag":8,"normalize":false}`, []uint64{
			0x3ff0000000000000, 0x3fe6666666666666, 0x3fdf5c28f5c28f5b, 0x3fd5f3b645a1cabf, 0x3fcebb98c7e2823e,
			0x3fc5835158b827f8, 0x3fbe1e3eaf6837f4, 0x3fb5152be12f5a5d, 0x3fad840a3b424b4f,
		}},
		{`{"kernel":"geometric","decay":0.35,"max_lag":8}`, []uint64{
			0x3fe4cd383ef411a0, 0x3fcd1f4ebe88e579, 0x3fb462b71ef96d6e, 0x3f9c8a3391c39933, 0x3f83fa574c6f51a3,
			0x3f6bf813d168a57e, 0x3f53940ddf62da3e, 0x3f3b68e038bd97ef, 0x3f232fd027b7ea5a,
		}},
		{`{"kernel":"weibull_cdf","shape":0.6,"scale":2.3,"max_lag":8,"normalize":false}`, []uint64{
			0x3ff0000000000000, 0x3fe171e8f8b0f503, 0x3fd984314cbf32aa, 0x3fd3ceab0a461140, 0x3fcfc2c640f5db39,
			0x3fca030d86bff52b, 0x3fc5a2c266cf6d06, 0x3fc2364c57757003, 0x3fbef4d1c70feac1,
		}},
		{`{"kernel":"weibull_cdf","shape":2,"scale":3,"max_lag":8}`, []uint64{
			0x3fd4433491b1f097, 0x3fd221b71d3d1a58, 0x3fc9fbde61cf54cb, 0x3fbdd11097f7cbfe, 0x3fab65a778e810a5,
			0x3f94285bbdf0141d, 0x3f77c0725c3bdb6a, 0x3f5668f3bf964e26, 0x3f30ee47fb3a61f6,
		}},
		{`{"kernel":"weibull_pdf","shape":1.3,"scale":4.2,"max_lag":8,"normalize":false}`, []uint64{
			0x3fc61095b74a041b, 0x3fc5a97c7662f782, 0x3fc2c7103cb20b94, 0x3fbe8c1ea8fd825f, 0x3fb7d0ea1976e194,
			0x3fb1fc369707e81f, 0x3faa795738a0c544, 0x3fa3111a36c3060c, 0x3f9af3af430ce3bc,
		}},
		{`{"kernel":"weibull_pdf","shape":0.6,"scale":1.7,"max_lag":8,"normalize":false}`, []uint64{
			0x3fcafdaa77e73ed0, 0x3fbc1d5bfdea298a, 0x3fb1a534a6d9195b, 0x3fa8226e1a5d3fcc, 0x3fa15fd909b5e8d4,
			0x3f99e74db57af780, 0x3f93cf6f148f8d0e, 0x3f8ee7cfc1ec0151, 0x3f887e087b2d1a6c,
		}},
		{`{"kernel":"weibull_pdf","shape":2,"scale":3,"max_lag":8}`, []uint64{
			0x3fc9f0a56ddbc6d7, 0x3fd2963b1e3d251e, 0x3fcffe2760a66146, 0x3fc3990633760936, 0x3fb2062671717b43,
			0x3f997c384feef946, 0x3f7c0d993aa2da6b, 0x3f5838d131f7a37d, 0x3f307c253bc3200f,
		}},
	}
	for _, c := range cases {
		spec, ref := Resolve([]byte(c.params))
		if ref != nil {
			t.Fatalf("%s: refused: %s", c.params, ref.Message)
		}
		if len(spec.Weights) != len(c.want) {
			t.Fatalf("%s: %d weights, want %d", c.params, len(spec.Weights), len(c.want))
		}
		for l, w := range spec.Weights {
			if b := math.Float64bits(w); b != c.want[l] {
				t.Errorf("%s: weight[%d] = %v (%#016x), want %v (%#016x)",
					c.params, l, w, b, math.Float64frombits(c.want[l]), c.want[l])
			}
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

// TestDistLag_NoFMAContraction asks the compiler: the package
// cross-compiled for arm64 (which contracts a*b + c into a fused
// multiply-add unless the product is an explicit float64(...)) must
// report no contraction, so the kernel's bits are the same on every
// architecture. A probe first proves the flag still reports one.
func TestDistLag_NoFMAContraction(t *testing.T) {
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
	if got := fmahashReport(t, wd, "github.com/frankbardon/pulse/internal/distlagparams"); len(got) > 0 {
		t.Errorf("the arm64 compiler contracted %d multiply-adds in distlagparams; wrap each product in float64(...):\n%s",
			len(got), strings.Join(got, "\n"))
	}
}
