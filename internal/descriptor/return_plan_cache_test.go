package descriptor

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

func exactSpellings(p *returnplan.Plan) []string {
	out := make([]string, 0, len(p.Exact))
	for _, x := range p.Exact {
		out = append(out, x.String())
	}
	return out
}

// TestResolveReturn_CacheNeverSharesExact: the per-instance plan cache
// (#219) memoizes the selection only. Two requests resolving through the
// same instance default (precision set, so Exact is derived) but with
// different count columns each get their OWN request-derived Exact set
// — before and after a cache hit, and a mutation of one never reaches
// the other — while the selection itself is shared (cache hit).
func TestResolveReturn_CacheNeverSharesExact(t *testing.T) {
	inst := (*InstanceSnapshot)(nil).WithDefaultReturn(&types.Return{
		Preset: types.ReturnPresetStandard, Precision: 3,
	})
	reqA := &types.Request{Aggregations: []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "x", Label: "n_a"},
	}}
	reqB := &types.Request{Aggregations: []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "x", Label: "n_b"},
		{Type: types.AGG_SUM, Field: "x", Label: "sum_b"},
	}}
	reqNone := &types.Request{}

	resolve := func(req *types.Request) *returnplan.Plan {
		t.Helper()
		p, err := ResolveReturn(req, inst)
		if err != nil || p == nil {
			t.Fatalf("ResolveReturn: plan=%v err=%v", p, err)
		}
		return p
	}

	a1 := resolve(reqA)
	b1 := resolve(reqB)
	none := resolve(reqNone)
	a2 := resolve(reqA)

	wantA := []string{"data[*].n_a"}
	wantB := []string{"data[*].n_b"}
	for name, c := range map[string]struct {
		p    *returnplan.Plan
		want []string
	}{"a1": {a1, wantA}, "b1": {b1, wantB}, "a2": {a2, wantA}, "none": {none, []string{}}} {
		if got := exactSpellings(c.p); !slices.Equal(got, c.want) {
			t.Errorf("%s Exact = %v, want %v", name, got, c.want)
		}
	}

	// Every request owns its plan value: mutating one request's Exact
	// never reaches another's, nor the next resolution.
	a1.Exact = append(a1.Exact, b1.Exact...)
	a1.Exact[0] = b1.Exact[0]
	if got := exactSpellings(b1); !slices.Equal(got, wantB) {
		t.Errorf("b1 Exact after mutating a1 = %v, want %v", got, wantB)
	}
	if got := exactSpellings(resolve(reqB)); !slices.Equal(got, wantB) {
		t.Errorf("re-resolved b Exact = %v, want %v", got, wantB)
	}
	if a1 == b1 || a1 == a2 {
		t.Fatal("two resolutions returned the same *Plan")
	}

	// The selection is resolved once and shared: same digest, same
	// backing Include array (the cache hit).
	if a1.Digest != b1.Digest || len(a1.Include) == 0 || &a1.Include[0] != &b1.Include[0] {
		t.Errorf("selection not shared across requests (digest %q vs %q)", a1.Digest, b1.Digest)
	}
}

// TestResolveReturn_CacheKeyedByBlock: distinct effective blocks never
// share a cached plan — a request block replaces the default and
// resolves its own selection and precision.
func TestResolveReturn_CacheKeyedByBlock(t *testing.T) {
	inst := (*InstanceSnapshot)(nil).WithDefaultReturn(&types.Return{Preset: types.ReturnPresetStandard})
	def, err := ResolveReturn(&types.Request{}, inst)
	if err != nil {
		t.Fatal(err)
	}
	own, err := ResolveReturn(&types.Request{Return: &types.Return{Preset: types.ReturnPresetMinimal, Precision: 4}}, inst)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ResolveReturn(&types.Request{}, inst)
	if err != nil {
		t.Fatal(err)
	}
	if def.Digest == own.Digest || own.Precision != 4 || own.Preset != string(types.ReturnPresetMinimal) {
		t.Errorf("request block shared the default's plan: def=%s own=%s/%d/%s", def.Digest, own.Digest, own.Precision, own.Preset)
	}
	if again.Digest != def.Digest || again.Precision != 0 {
		t.Errorf("default re-resolution = %s/%d, want %s/0", again.Digest, again.Precision, def.Digest)
	}
}
