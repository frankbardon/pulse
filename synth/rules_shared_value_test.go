package synth_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/synth"
)

// The `set` literals a rule writes are coerced to their row shape ONCE,
// at compile time, and the resulting value is SHARED by every row the
// rule fires on — that is what keeps the pass allocating nothing per row.
// For a scalar or a categorical the value is immutable and the sharing
// cannot be observed. For a `set_*` target it is a map[string]bool, and
// the sharing is safe only because of WHERE the pass runs: the rule pass
// is the last thing to touch a row, encodeRow only reads, and the
// set-option stages that DO rewrite such a map in place all run earlier.
//
// A stage added AFTER the rule pass would mutate the shared value through
// row[field] and every subsequent row would carry the mutation. The two
// tests below are the guard on that, one on the consequence and one on
// the precondition, because neither alone is enough: the consequence test
// cannot see a stage that has not been written yet, and the precondition
// test cannot see a mutation that happens somewhere else.

// TestRules_SetLiteralIsStableAcrossEveryRowItFiresOn is the CONSEQUENCE
// guard. A shared value that something mutates produces a ROW-DEPENDENT
// result from a rule that says the same thing on every row, which is
// exactly what this asserts cannot happen.
//
// The fixture deliberately uses a set_* target — the only mutable row
// shape a `set` literal can produce — and a rule gated on a categorical,
// so the generated file interleaves rows the rule wrote with rows it did
// not. An in-place mutation of the shared map would then show up as a
// mask that drifts down the file rather than one that is wrong from the
// start, which is the harder half to see by inspection.
func TestRules_SetLiteralIsStableAcrossEveryRowItFiresOn(t *testing.T) {
	const rows = 500
	spec := &synth.Spec{
		RowCount: rows,
		Fields: []synth.FieldSpec{
			{
				Name: "region", Type: "categorical_u8",
				Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{
					"values":  []any{"east", "west"},
					"weights": []any{1.0, 1.0},
				},
			},
			{
				Name: "channels", Type: "set_u8", Distribution: synth.DistSetBernoulli,
				Params: map[string]any{
					"options":     []any{"tv", "radio", "print"},
					"frequencies": []any{0.5, 0.5, 0.5},
				},
			},
		},
		Rules: []synth.RuleSpec{{
			When: `region == "east"`,
			Set:  map[string]any{"channels": []any{"tv", "print"}},
		}},
	}

	data, res, err := synth.SynthBytes(spec, synth.Options{Seed: 21})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", res.Warnings)
	}

	regions := readCategoricalField(t, data, "region")
	_, labels, _ := readSetFieldRows(t, data, "channels")
	if len(regions) != len(labels) {
		t.Fatalf("column length mismatch: %d regions, %d channel rows", len(regions), len(labels))
	}

	var fired, open int
	for i := range regions {
		if regions[i] != "east" {
			open++
			continue
		}
		fired++
		got := append([]string(nil), labels[i]...)
		want := "print,tv"
		if strings.Join(sortedCopy(got), ",") != want {
			t.Fatalf("row %d is the %d-th row the rule fired on and carries {%s}, want {%s} — "+
				"the `set` literal is SHARED across rows and something has mutated it in place",
				i, fired, strings.Join(got, ","), want)
		}
	}
	if fired == 0 || open == 0 {
		t.Fatalf("fixture degenerate: %d fired / %d open rows, want both", fired, open)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// TestDrawRow_RulePassIsTheLastStatement is the PRECONDITION guard, and
// it is a source scan because the precondition is a source-level fact:
// "nothing runs after the rule pass" is not observable from any output
// while it holds.
//
// It carries two separate loads. The obvious one is the rule layer's own
// semantics — `if gate then null else inferred` requires the pass to see
// every value generation inferred, so a pass running before the model
// stage would let the model overwrite its own rule-gated target (that
// half is also covered behaviourally by
// TestRules_GateMasksAndReplacesWhileTheInferredValueSurvives). The one
// no behavioural test can cover is the SHARED-VALUE invariant above: a
// new stage appended after the pass would be free to mutate a `set`
// literal's map through row[field], and the corruption would appear in
// every later row of every later run.
//
// So the rule is: nothing goes after `stages.rules.apply`. A stage that
// genuinely must run later has to copy any map[string]bool it intends to
// mutate, and this test is where that decision gets made explicitly
// rather than by accident.
func TestDrawRow_RulePassIsTheLastStatement(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "writer.go", nil, 0)
	if err != nil {
		t.Fatalf("parse writer.go: %v", err)
	}
	var body *ast.BlockStmt
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if ok && fn.Name.Name == "drawRow" && fn.Recv == nil {
			body = fn.Body
		}
		return body == nil
	})
	if body == nil || len(body.List) == 0 {
		t.Fatal("drawRow not found in writer.go — if it moved or was renamed, move this guard with it")
	}

	last := body.List[len(body.List)-1]
	ret, ok := last.(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		t.Fatalf("drawRow's last statement is %T, not a single-result return of the rule pass — "+
			"a stage added after the rule pass must first copy any map[string]bool it mutates, "+
			"because a `set` literal's value is shared across rows (see ruleAssign.value)", last)
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("drawRow returns %T rather than the rule pass's result", ret.Results[0])
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "apply" {
		t.Fatalf("drawRow's final call is not the rule pass's apply — see this test's doc")
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok || inner.Sel.Name != "rules" {
		t.Fatalf("drawRow's final call is on %v, want stages.rules", sel.X)
	}
}
