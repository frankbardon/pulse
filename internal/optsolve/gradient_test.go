package optsolve

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/optblock"
	"github.com/frankbardon/pulse/internal/optgrad"
	"github.com/frankbardon/pulse/types"
)

// TestOptimize_AnalyticGradientAgreesWithFD: on every corpus block
// whose expressions all differentiate, the analytic gradient of the
// objective and of every constraint (eq and ineq alike) agrees with the
// solver's own finite differences at each start point within
// 1e-5·max(1, |∂|) — the FD stencil's error at h = cbrt(ε)·scale is
// O(h²·f”') ≈ 1e-10 relative plus O(ε/h) ≈ 1e-11, far inside that —
// and the analytic solve reaches the fd solve's optimum (each variable
// within 1e-4 of the bound width, the objective within 1e-6 relative).
func TestOptimize_AnalyticGradientAgreesWithFD(t *testing.T) {
	t.Parallel()

	checked := 0
	for k, tc := range fdCorpus {
		spec := block(t, tc.raw)
		env := optblock.Env{}
		if tc.params != nil {
			p, cerr := exprenv.NormalizeParams(tc.params)
			if cerr != nil {
				t.Fatal(cerr)
			}
			env.Params = p
		}
		c, cerr := optblock.Compile(spec, 0, env, exprenv.Bound{})
		if cerr != nil {
			t.Fatal(cerr)
		}
		if c.ObjectiveGrad == nil {
			continue
		}
		checked++
		s := &solver{spec: spec, c: c, sign: 1, draw: -1}
		a := &solver{spec: spec, c: c, sign: 1, draw: -1, analytic: map[*vm.Program]*optgrad.Program{c.Objective: c.ObjectiveGrad}}
		for j, prog := range c.Constraints {
			a.analytic[prog] = c.ConstraintGrads[j]
		}
		progs := append([]*vm.Program{c.Objective}, c.Constraints...)
		for si, x := range startPoints(4, c, optblock.EffectiveSeed(spec)) {
			if k == 10 && si == 0 {
				// The piecewise block's midpoint (0, 0) sits on the
				// ternary's and max()'s kinks, where no gradient exists.
				continue
			}
			for pi, prog := range progs {
				want := s.grad(prog, "f", 1, x)
				got := a.grad(prog, "f", 1, x)
				for j := range want {
					if math.Abs(got[j]-want[j]) > 1e-5*math.Max(1, math.Abs(want[j])) {
						t.Errorf("corpus[%d] expression %d at %v: ∂/∂%s analytic %v, fd %v", k, pi, x, c.Names[j], got[j], want[j])
					}
				}
			}
		}
		fdSpec := block(t, tc.raw)
		fdSpec.Gradient = types.OptimizeGradientFD
		fo, err := solve(t, fdSpec, tc.params, optblock.Env{}, exprenv.Bound{})
		if err != nil {
			t.Fatal(err)
		}
		ao, err := solve(t, block(t, tc.raw), tc.params, optblock.Env{}, exprenv.Bound{})
		if err != nil {
			t.Fatal(err)
		}
		if fo.Failed != ao.Failed {
			t.Fatalf("corpus[%d]: fd failed %v, analytic failed %v", k, fo.Failed, ao.Failed)
		}
		if fo.Failed {
			continue
		}
		fr, ar := fo.Result, ao.Result
		if math.Abs(fr.Objective-ar.Objective) > 1e-6*math.Max(1, math.Abs(fr.Objective)) {
			t.Errorf("corpus[%d]: objective fd %v, analytic %v", k, fr.Objective, ar.Objective)
		}
		for j := range fr.Variables {
			if w := c.Upper[j] - c.Lower[j]; math.Abs(fr.Variables[j].Value-ar.Variables[j].Value) > 1e-4*math.Max(1, w) {
				t.Errorf("corpus[%d]: %s fd %v, analytic %v", k, fr.Variables[j].Name, fr.Variables[j].Value, ar.Variables[j].Value)
			}
		}
	}
	if checked < 8 {
		t.Fatalf("only %d corpus blocks differentiate analytically; the agreement covers too little", checked)
	}
}

// TestOptimize_GradientFallbackIsPerBlock: one expression calling a
// function without a derivative rule (hill() in a constraint here)
// sends the WHOLE block to finite differences — no gradient programs,
// the function named — and the block solves to the fd bits; gradient fd
// names no fallback.
func TestOptimize_GradientFallbackIsPerBlock(t *testing.T) {
	t.Parallel()

	raw := `{"name":"g","direction":"minimize","variables":[{"name":"x","lower":0,"upper":4},{"name":"y","lower":0,"upper":4}],
		"objective":"(x - 1) * (x - 1) + (y - 2) * (y - 2)","constraints":[{"expr":"hill(x, 2, 2) + y - 1","type":"ineq"},{"expr":"x - y","type":"ineq"}],"starts":3,"rng_seed":4}`
	c := compiled(t, raw)
	if c.ObjectiveGrad != nil || c.ConstraintGrads != nil || !reflect.DeepEqual(c.GradientFallback, []string{"hill"}) {
		t.Fatalf("compiled: objective grad %v, constraint grads %v, fallback %v; want none and [hill]", c.ObjectiveGrad != nil, c.ConstraintGrads, c.GradientFallback)
	}
	enc := func(edit func(*types.OptimizeSpec)) string {
		spec := block(t, raw)
		if edit != nil {
			edit(spec)
		}
		out, err := solve(t, spec, nil, optblock.Env{}, exprenv.Bound{})
		if err != nil {
			t.Fatal(err)
		}
		return strconv.Quote(mustJSON(t, out.Result))
	}
	if a, f := enc(nil), enc(func(s *types.OptimizeSpec) { s.Gradient = types.OptimizeGradientFD }); a != f {
		t.Fatalf("fallback block differs from fd:\n%s\n%s", a, f)
	}
	fd := block(t, raw)
	fd.Gradient = types.OptimizeGradientFD
	fc, cerr := optblock.Compile(fd, 0, optblock.Env{}, exprenv.Bound{})
	if cerr != nil || fc.GradientFallback != nil || fc.ObjectiveGrad != nil {
		t.Fatalf("gradient fd: %v, fallback %v", cerr, fc)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
