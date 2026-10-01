package synth

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// thinSupportDetectorFiles is the exhaustive list of --suggest-rules
// detectors that decide whether a candidate's support is thin. It is
// written out rather than globbed so that a FOURTH detector is a
// deliberate edit to this list with the "is it the same question?"
// judgement made explicitly, instead of being swept in by a pattern.
var thinSupportDetectorFiles = []string{
	"profile_gating.go",     // gating candidates (set_null)
	"profile_comissing.go",  // co-missing blocks (null_together)
	"profile_dependency.go", // exact dependencies (set_expr)
}

// TestThinLevelSupport_EveryDetectorAsksTheSameQuestion is the guard on
// a SHARED THRESHOLD, and the thing it guards is silent.
//
// minGateLevelSupport is declared in profile_gating.go and is read by all
// three detectors above. That reuse is correct — each is asking the same
// question of the same kind of quantity, "how many rows sit behind the
// weakest cell this candidate rests on" — and it is correct for a reason
// that a future edit can invalidate without noticing: the moment one
// detector's threshold is retuned in place, the other two keep the old
// number, the constant's doc keeps describing a single tuning, and
// nothing anywhere says the three disagree.
//
// So the comparison has exactly ONE spelling (thinLevelSupport) and this
// test asserts every ThinSupport decision uses it. A fork is then a
// visible edit: the forking detector stops calling the shared helper and
// this test names it, at which point the author has to write the
// reasoning at both declarations — which is the whole ask.
//
// It is a source scan because the divergence it catches is a source-level
// one. Driving each detector to a candidate sitting exactly on the
// boundary would need three purpose-built cohort fixtures and would still
// only cover the boundary each fixture happened to reach; the scan covers
// every decision each detector can make.
func TestThinLevelSupport_EveryDetectorAsksTheSameQuestion(t *testing.T) {
	fset := token.NewFileSet()
	total := 0
	for _, name := range thinSupportDetectorFiles {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		found := 0
		ast.Inspect(file, func(n ast.Node) bool {
			var rhs ast.Expr
			switch node := n.(type) {
			case *ast.AssignStmt:
				// ev.ThinSupport = <rhs>
				if len(node.Lhs) != 1 || len(node.Rhs) != 1 {
					return true
				}
				sel, ok := node.Lhs[0].(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "ThinSupport" {
					return true
				}
				rhs = node.Rhs[0]
			case *ast.KeyValueExpr:
				// RuleEvidence{..., ThinSupport: <rhs>}
				key, ok := node.Key.(*ast.Ident)
				if !ok || key.Name != "ThinSupport" {
					return true
				}
				rhs = node.Value
			default:
				return true
			}
			found++
			call, ok := rhs.(*ast.CallExpr)
			if !ok {
				t.Errorf("%s:%d: ThinSupport is decided by an open-coded expression, not thinLevelSupport",
					name, fset.Position(rhs.Pos()).Line)
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "thinLevelSupport" {
				t.Errorf("%s:%d: ThinSupport is decided by a call to something other than thinLevelSupport — "+
					"if this detector needs its OWN threshold, FORK minGateLevelSupport and write the reasoning "+
					"at both declarations, then add the new constant here",
					name, fset.Position(rhs.Pos()).Line)
			}
			return true
		})
		if found == 0 {
			t.Errorf("%s decides no ThinSupport: either the detector lost its thin arm or it moved — "+
				"move this guard with it rather than deleting the entry", name)
		}
		total += found
	}
	if total < len(thinSupportDetectorFiles) {
		t.Fatalf("found %d ThinSupport decisions across %d detectors", total, len(thinSupportDetectorFiles))
	}
}

// TestThinLevelSupport_BoundaryIsStrictlyBelow pins what the shared
// helper means, because the scan above only proves the three detectors
// agree and not what they agree ON. Exactly minGateLevelSupport rows is
// ENOUGH; one fewer is thin.
func TestThinLevelSupport_BoundaryIsStrictlyBelow(t *testing.T) {
	cases := []struct {
		support int
		want    bool
	}{
		{0, true},
		{1, true},
		{minGateLevelSupport - 1, true},
		{minGateLevelSupport, false},
		{minGateLevelSupport + 1, false},
	}
	for _, tc := range cases {
		if got := thinLevelSupport(tc.support); got != tc.want {
			t.Errorf("thinLevelSupport(%d) = %v, want %v", tc.support, got, tc.want)
		}
	}
	// The shared-with-MinPairObservations claim is part of the constant's
	// documented reasoning, so it is asserted rather than left in prose.
	if minGateLevelSupport != MinPairObservations {
		t.Errorf("minGateLevelSupport = %d, MinPairObservations = %d — if the fork was deliberate, "+
			"write the reasoning at BOTH declarations and update this test",
			minGateLevelSupport, MinPairObservations)
	}
}
