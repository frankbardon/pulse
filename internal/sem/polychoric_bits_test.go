package sem

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The cross-architecture gates of the two-step ordinal numerics. The
// pinned bits below hold on amd64 and arm64 alike BY CONSTRUCTION: the
// four files are FMA-free (TestPolychoric_NoFusibleProducts, and the
// compiler's own report, TestPolychoric_NoFMAContraction) and call only
// detmath's portable kernels and the correctly rounded math.Sqrt. They
// were produced on darwin/arm64 and checked identical on linux/amd64
// (docker); CI re-runs them on linux/amd64.
//
// Never regenerate them for a refactor — the pin exists to stay green
// across one. A DELIBERATE numerical change prints the new values on
// failure (go test ./internal/sem -run TestPolychoric_GoldenBits -v);
// paste them in and say in the commit why the bits moved.

// twoStepFiles are the FMA-free sources.
var twoStepFiles = []string{"normal.go", "bvn.go", "ordinal.go", "ordinal_scores.go", "ordinal_gamma.go", "ebm.go"}

// twoStepDigest hashes every threshold, moment and correlation, then the
// casewise score vector of every row (and every pair score of the first
// 25 rows), as raw float bits.
func twoStepDigest(t *testing.T, in TwoStepInput, ss []*TwoStepStats) string {
	t.Helper()
	h := sha256.New()
	put := func(v float64) {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
		h.Write(b[:])
	}
	for _, s := range ss {
		for _, th := range s.Thresholds {
			for _, v := range th {
				put(v)
			}
		}
		for v := range s.Mean {
			put(s.Mean[v])
			put(s.Var[v])
		}
		for _, p := range s.Pairs {
			put(p.Rho)
		}
	}
	s := ss[0]
	sc := make([]float64, s.Layout().Size)
	for r, row := range in.Rows {
		if len(ss) > 1 {
			break // grouped: the statistics alone
		}
		if err := s.Scores(row, sc); err != nil {
			t.Fatal(err)
		}
		for _, v := range sc {
			put(v)
		}
		if r < 25 {
			for k := range s.Pairs {
				ps := s.PairScores(k, row)
				put(ps.Rho)
				for _, v := range ps.ThI {
					put(v)
				}
				for _, v := range ps.ThJ {
					put(v)
				}
				if !math.IsNaN(ps.VarI) {
					put(ps.VarI)
				}
				if !math.IsNaN(ps.VarJ) {
					put(ps.VarJ)
				}
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestPolychoric_NoFMAContraction asks the compiler: internal/sem
// cross-compiled for arm64 with -d=fmahash=vy prints one line per
// multiply-add it contracted, and none may name a two-step file. A
// probe first proves the flag still reports a contraction.
func TestPolychoric_NoFMAContraction(t *testing.T) {
	t.Parallel()

	build := func(dir, importPath string) string {
		cmd := exec.Command("go", "build", "-o", os.DevNull, "-gcflags="+importPath+"=-d=fmahash=vy", ".")
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOFLAGS=")
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("cross-compile for arm64: %v\n%s", err, out.String())
		}
		return out.String()
	}
	probe := t.TempDir()
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte("module fmaprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "p.go"), []byte("package fmaprobe\n\nfunc F(a, b, c float64) float64 { return a + b*c }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(build(probe, "fmaprobe"), "fmahash triggered") {
		t.Fatal("the -d=fmahash=vy probe reported no contraction for `a + b*c`: the flag changed and this gate is vacuous")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for l := range strings.SplitSeq(build(wd, "github.com/frankbardon/pulse/internal/sem"), "\n") {
		if !strings.Contains(l, "fmahash triggered") {
			continue
		}
		for _, f := range twoStepFiles {
			if strings.Contains(l, "/"+f+":") || strings.HasPrefix(l, f+":") || strings.Contains(l, " "+f+":") {
				t.Errorf("the arm64 compiler contracted a multiply-add in %s: %s", f, l)
			}
		}
	}
}
