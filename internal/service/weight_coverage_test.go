package service

import (
	"slices"
	"sort"
	"testing"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// Shared weight-coverage helper (weighting-inferential E1-S4). Every
// weighting harness — parity (unity / expansion / scale), reference
// fixtures — holds its row table total over the manifest's
// `weight_kinds` through assertWeightKindCoverage, so an operator whose
// class flips (internal/weighting) without a harness row, or a row for a
// kind the manifest does not advertise, fails the gate that owns it.
//
// To cover a NEW surface (rank tests, regressions, attributes, groupers,
// overlays):
//
//  1. pick its weightSurface below (manifestWeightKinds already reads
//     every manifest surface that carries weight_kinds);
//  2. collect what your row table runs as map[operator][]WeightKind —
//     one entry per (row, kind it is exercised under);
//  3. call assertWeightKindCoverage(t, surface, have, "<file / table a
//     missing row belongs in>") from a "coverage" subtest.
//
// Adding a row to an existing harness needs nothing here.

// weightSurface names one manifest slice that carries weight_kinds.
type weightSurface string

const (
	weightSurfaceAggregators weightSurface = "aggregators"
	weightSurfaceAttributes  weightSurface = "attributes"
	weightSurfaceGroupers    weightSurface = "groupers"
	weightSurfaceTests       weightSurface = "tests"
	weightSurfaceRegressions weightSurface = "regressions"
	weightSurfaceOverlays    weightSurface = "overlays"
)

// manifestWeightKinds returns the surface's operators with a non-empty
// weight_kinds, keyed by manifest name (tier-1 tests by family; post
// tests are never weighted).
func manifestWeightKinds(s weightSurface) map[string][]types.WeightKind {
	m := descx.BuildManifest()
	out := map[string][]types.WeightKind{}
	add := func(name string, kinds []types.WeightKind) {
		if len(kinds) > 0 {
			out[name] = kinds
		}
	}
	switch s {
	case weightSurfaceAggregators:
		for _, op := range m.Components.Aggregators {
			add(op.Name, op.WeightKinds)
		}
	case weightSurfaceAttributes:
		for _, op := range m.Components.Attributes {
			add(op.Name, op.WeightKinds)
		}
	case weightSurfaceGroupers:
		for _, op := range m.Components.Groupers {
			add(op.Name, op.WeightKinds)
		}
	case weightSurfaceTests:
		for _, tm := range m.Tests {
			add(tm.Family, tm.WeightKinds)
		}
	case weightSurfaceRegressions:
		for _, r := range m.Regressions {
			add(r.Name, r.WeightKinds)
		}
	case weightSurfaceOverlays:
		for _, o := range m.Overlays {
			add(string(o.Kind), o.WeightKinds)
		}
	default:
		panic("unknown weight surface " + string(s))
	}
	return out
}

// assertWeightKindCoverage: have (operator → kinds a harness runs it
// under) equals the manifest's weight_kinds for the surface, both ways —
// every advertised (operator, kind) has a row, and no row runs an
// operator under a kind the manifest does not advertise (which would be
// a refusal, or a class-table drift). where names the table to edit.
func assertWeightKindCoverage(t *testing.T, s weightSurface, have map[string][]types.WeightKind, where string) {
	t.Helper()
	want := manifestWeightKinds(s)
	if len(want) == 0 {
		t.Fatalf("the manifest advertises no weight_kinds on %s: the coverage gate is vacuous", s)
	}
	for _, name := range sortedNames(want) {
		for _, k := range want[name] {
			if !slices.Contains(have[name], k) {
				t.Errorf("%s %s advertises weight kind %q but has no row for it — add one (%s)", s, name, k, where)
			}
		}
	}
	for _, name := range sortedNames(have) {
		for _, k := range have[name] {
			if !slices.Contains(want[name], k) {
				t.Errorf("%s row %s runs under weight kind %q, which the manifest does not advertise — drop the row or fix the class table", s, name, k)
			}
		}
	}
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
