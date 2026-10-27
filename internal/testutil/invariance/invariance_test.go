package invariance

import (
	"go/parser"
	"go/token"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

const importPath = "github.com/frankbardon/pulse/internal/testutil/invariance"

// TestInvariance_TestOnlyImporters keeps the harness test-only: no
// non-test file of the module imports it (so no public package can
// reach it), and the packages whose invariance families run on it do.
func TestInvariance_TestOnlyImporters(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	users := map[string]bool{}
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
					return filepath.SkipDir // a nested module cannot import internal/
				}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if path, _ := strconv.Unquote(imp.Path.Value); path != importPath {
				continue
			}
			rel, _ := filepath.Rel(root, p)
			if !strings.HasSuffix(p, "_test.go") {
				t.Errorf("%s imports %s outside a _test.go file; the harness is test-only", rel, importPath)
				continue
			}
			users[filepath.ToSlash(filepath.Dir(rel))] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".", "internal/service"} {
		if !users[dir] {
			t.Errorf("%s runs no invariance family through the harness", dir)
		}
	}
}

type nested struct {
	F   float64
	f   float32
	S   []float64
	M   map[string]any
	P   *int
	str string
	u   uint8
	b   bool
	c   complex128
	fn  func()
}

func sample() nested {
	one := 1
	return nested{F: 0.1, f: 2, S: []float64{1, math.NaN()}, M: map[string]any{"a": []any{1.5, "x"}, "n": int64(3)},
		P: &one, str: "s", u: 4, b: true, c: complex(1, 2), fn: func() {}}
}

// TestDiff_BitExactCatchesEveryKind: every kind the walker reads reports
// a one-step change at its path, and an identical twin (NaN included)
// reports nothing.
func TestDiff_BitExactCatchesEveryKind(t *testing.T) {
	t.Parallel()

	if d := BitExact(sample(), sample()); d != nil {
		t.Fatalf("identical values differ: %v", d)
	}
	two := 2
	for _, c := range []struct {
		name string
		mut  func(*nested)
		path string
	}{
		{"one ulp", func(n *nested) { n.F = math.Nextafter(n.F, 1) }, "$.F"},
		{"unexported float32", func(n *nested) { n.f = 3 }, "$.f"},
		{"nan sign", func(n *nested) { n.S[1] = math.Float64frombits(math.Float64bits(math.NaN()) ^ 1<<63) }, "$.S[1]"},
		{"slice len", func(n *nested) { n.S = n.S[:1] }, "$.S"},
		{"nil slice vs empty", func(n *nested) { n.S = nil }, "$.S"},
		{"map value in nested any", func(n *nested) { n.M["a"] = []any{1.5000000000000002, "x"} }, "$.M[a][0]"},
		{"map key", func(n *nested) { n.M["b"] = 1 }, "$.M[b]"},
		{"dynamic type", func(n *nested) { n.M["n"] = int32(3) }, "$.M[n]"},
		{"pointer target", func(n *nested) { n.P = &two }, "$.P"},
		{"nil pointer", func(n *nested) { n.P = nil }, "$.P"},
		{"string", func(n *nested) { n.str = "t" }, "$.str"},
		{"uint", func(n *nested) { n.u = 5 }, "$.u"},
		{"bool", func(n *nested) { n.b = false }, "$.b"},
		{"complex", func(n *nested) { n.c = complex(1, 3) }, "$.c"},
	} {
		got := sample()
		c.mut(&got)
		d := BitExact(got, sample())
		if len(d) != 1 || !strings.HasPrefix(d[0], c.path+":") {
			t.Errorf("%s: diffs %q, want one at %s", c.name, d, c.path)
		}
	}
}

// TestDiff_CapsAndWithin: a rule reports at most maxDiffs paths plus a
// remainder line; Within accepts a documented tolerance and refuses
// beyond it, NaN equal to NaN, infinities exact.
func TestDiff_CapsAndWithin(t *testing.T) {
	t.Parallel()

	a, b := make([]float64, 40), make([]float64, 40)
	for i := range b {
		b[i] = 1
	}
	if d := BitExact(a, b); len(d) != maxDiffs+1 || d[maxDiffs] != "... and 28 more" {
		t.Errorf("cap: %d lines, last %q", len(d), d[len(d)-1])
	}
	rule := Within(1e-9, 0)
	if d := rule([]float64{1, math.NaN(), math.Inf(1)}, []float64{1 + 1e-12, math.NaN(), math.Inf(1)}); d != nil {
		t.Errorf("within tolerance: %v", d)
	}
	if d := rule([]float64{1}, []float64{1 + 1e-6}); len(d) != 1 {
		t.Errorf("beyond tolerance: %v", d)
	}
	if d := rule([]float64{math.Inf(1)}, []float64{math.MaxFloat64}); len(d) != 1 {
		t.Errorf("inf vs finite: %v", d)
	}
	if d := rule([]float64{math.NaN()}, []float64{0}); len(d) != 1 {
		t.Errorf("nan vs 0: %v", d)
	}
}

// TestValidate_RefusesUnprovableFamilies: a family with no Check, no
// group, an empty group, or a parallel cell that names no arm is
// refused; the presets validate.
func TestValidate_RefusesUnprovableFamilies(t *testing.T) {
	t.Parallel()

	run := func(*testing.T, Knobs) Result[int] { return Result[int]{} }
	check := func(*testing.T, Knobs, int) {}
	ok := Family[int]{Name: "ok", Run: run, Check: check,
		Groups: []Group{ArchiveGroup(), DecodeGroup("decode", BigFile), {Name: "buf", Ref: Cell{Knobs: Knobs{Layout: File}}, Cells: []Cell{BufferedCell()}}}}
	if err := Validate(ok); err != nil {
		t.Fatalf("presets: %v", err)
	}
	noArm := ArchiveGroup()
	noArm.Cells[1].Arm = ""
	noDecodeArm := DecodeGroup("decode", BigFile)
	noDecodeArm.Cells[0].Arm = ""
	for name, f := range map[string]Family[int]{
		"no check":      {Name: "x", Run: run, Groups: ok.Groups},
		"no run":        {Name: "x", Check: check, Groups: ok.Groups},
		"no group":      {Name: "x", Run: run, Check: check},
		"empty group":   {Name: "x", Run: run, Check: check, Groups: []Group{{Name: "g", Ref: Cell{}}}},
		"shard no arm":  {Name: "x", Run: run, Check: check, Groups: []Group{noArm}},
		"decode no arm": {Name: "x", Run: run, Check: check, Groups: []Group{noDecodeArm}},
	} {
		if Validate(f) == nil {
			t.Errorf("%s: validated", name)
		}
	}
}

// TestVerdict_ArmAndAnswer: a cell fails on a wrong arm alone, on a
// differing answer alone, and passes when both hold; nil rule is
// BitExact.
func TestVerdict_ArmAndAnswer(t *testing.T) {
	t.Parallel()

	cell := Cell{Knobs: Knobs{Layout: Archive, ShardWorkers: 3}, Arm: observe.ArmShardParallel}
	ref := Result[float64]{Answer: 1}
	if d := Verdict(nil, cell, ref, Result[float64]{Answer: 1, Arm: observe.ArmShardParallel}); d != nil {
		t.Errorf("equal: %v", d)
	}
	if d := Verdict(nil, cell, ref, Result[float64]{Answer: 1, Arm: observe.ArmStreaming}); len(d) != 1 || !strings.Contains(d[0], "arm") {
		t.Errorf("serial arm accepted: %v", d)
	}
	if d := Verdict(nil, cell, ref, Result[float64]{Answer: math.Nextafter(1, 2), Arm: observe.ArmShardParallel}); len(d) != 1 {
		t.Errorf("one ulp accepted: %v", d)
	}
	if d := Verdict(Within(1e-12, 0), cell, ref, Result[float64]{Answer: math.Nextafter(1, 2), Arm: observe.ArmShardParallel}); d != nil {
		t.Errorf("tolerance rule ignored: %v", d)
	}
}

// TestLayoutFree_BlanksOnlyLayoutFacts: the cohort path and shard count
// are blanked on a copy; the input and every other figure are kept.
func TestLayoutFree_BlanksOnlyLayoutFacts(t *testing.T) {
	t.Parallel()

	resp := &types.Response{
		Metadata:   &types.ResponseMetadata{TotalRows: 5, CohortFile: "a.pulse"},
		Components: &types.ResponseComponents{Run: &types.RunComponents{TotalRecords: 5, ShardCount: 3}},
	}
	got := LayoutFree(resp)
	if got.Metadata.CohortFile != "" || got.Components.Run.ShardCount != 0 {
		t.Errorf("layout facts kept: %+v %+v", got.Metadata, got.Components.Run)
	}
	if got.Metadata.TotalRows != 5 || got.Components.Run.TotalRecords != 5 {
		t.Errorf("figures lost: %+v %+v", got.Metadata, got.Components.Run)
	}
	if resp.Metadata.CohortFile != "a.pulse" || resp.Components.Run.ShardCount != 3 {
		t.Error("input modified")
	}
	if LayoutFree(nil) != nil || LayoutFree(&types.Response{}).Components != nil {
		t.Error("nil handling")
	}
}
