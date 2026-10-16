package processing

import (
	"context"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// vectorBuiltinCases holds one scalar expression per reserved built-in,
// over the record x=1, y=2, z=3, with its expected value. Every
// exprenv name must have a case (TestVectorBuiltins_CaseCoverage),
// so a new built-in cannot ship untested through the three consumers.
var vectorBuiltinCases = map[string]struct {
	src  string
	want float64
}{
	"vsum":       {`vsum([x, y, z, nil])`, 6}, // a nil literal element is NaN, skipped
	"vmean":      {`vmean([x, y, z])`, 2},
	"vmin":       {`vmin([z, x, y])`, 1},
	"vmax":       {`vmax([x, z, y])`, 3},
	"vsd":        {`vsd([x, y, z])`, 1},
	"vcount":     {`vcount([x, nil, z])`, 2},
	"dot":        {`dot([x, y, z], [1, 0, 1])`, 4},
	"norm":       {`norm([3, 4])`, 5},
	"dist":       {`dist([x, y], [4, 6])`, 5},
	"cosine":     {`cosine([x, 0], [2.5, 0])`, 1},
	"argmax":     {`argmax([x, z, y])`, 1},
	"argmin":     {`argmin([z, x, y])`, 1},
	"vcenter":    {`vsum(vcenter([x, y, z]))`, 0},
	"vzscore":    {`vmax(vzscore([x, y, z]))`, 1},
	"vnormalize": {`vmax(vnormalize([3, 4], "l2")) + vmax(vnormalize([x, z], "l1"))`, 0.8 + 0.75},
	"sqrt":       {`sqrt(z + 1)`, 2},
	"log":        {`log(exp(y))`, 2},
	"exp":        {`exp(0)`, 1},
	"pow":        {`pow(y, 3)`, 8},
	"log1p":      {`log1p(x)`, math.Ln2},
	// Calendar helpers over epoch days: day 1 is 1970-01-02, a Friday.
	"weekday":     {`weekday(x)`, 5},
	"month_index": {`month_index(z) - 1970 * 12`, 0},
	"date_trunc":  {`date_trunc("week", z)`, -3},        // 1970-01-04 → Monday 1969-12-29
	"to_day":      {`to_day(y * 86400 + 86399) + x`, 3}, // UTC day 2, plus 1
	"jaccard":     {`jaccard(["a", "b", "c"], ["c", "b", "d"])`, 0.5},
	"dice":        {`dice(["a", "b", "c"], ["b", "c", "d", "e"]) * 7`, 4},
	"hamming":     {`hamming(["a", "b"], ["b", "c", "d"]) + x`, 4},
	"overlap":     {`overlap(["a", "b"], ["b", "c", "d"])`, 0.5},
	// vectorTestRefs: t = {p: 1, q: 2, r: 3}; u relabels them {r: 4, p: 0, q: 0}.
	"ref": {`vsum(ref("t", "k")) + dot(ref("t", "k"), ref("u", "k")) + x`, 6 + 12 + 1},
}

// vectorTestRefs are the reference-vector tables processVector registers
// for the ref() case.
func vectorTestRefs() vectors.ReferenceTables {
	return vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t": {Labels: []string{"p", "q", "r"}, Profiles: map[string][]float64{"k": {1, 2, 3}}},
		"u": {Labels: []string{"r", "p", "q"}, Profiles: map[string][]float64{"k": {4, 0, 0}}},
	})
}

func vectorTestSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "z", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

func vectorTestRecords(schema *encoding.Schema) []*Record {
	return []*Record{NewRecord(schema, map[string]float64{"x": 1, "y": 2, "z": 3})}
}

// processVector runs req over the single x=1,y=2,z=3 record.
func processVector(t *testing.T, req *types.Request) (*types.Response, error) {
	t.Helper()
	schema := vectorTestSchema()
	proc := NewProcessorWithExtensions(schema, &ExtensionRegistry{ReferenceVectors: vectorTestRefs()})
	return proc.Process(context.Background(), req, NewSliceIterator(vectorTestRecords(schema)))
}

func closeTo(got, want float64) bool { return math.Abs(got-want) <= 1e-12 }

func TestVectorBuiltins_CaseCoverage(t *testing.T) {
	for _, name := range exprenv.Names() {
		c, ok := vectorBuiltinCases[name]
		if !ok {
			t.Errorf("reserved built-in %q has no vectorBuiltinCases entry", name)
			continue
		}
		if !strings.Contains(c.src, name+"(") {
			t.Errorf("case for %q does not call it: %s", name, c.src)
		}
	}
}

func TestVectorBuiltins_InFormula(t *testing.T) {
	for name, c := range vectorBuiltinCases {
		t.Run(name, func(t *testing.T) {
			resp, err := processVector(t, &types.Request{
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: c.src, Label: "v"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			})
			if err != nil {
				t.Fatalf("%s: %v", c.src, err)
			}
			if got := resp.Data[0]["s"].(float64); !closeTo(got, c.want) {
				t.Errorf("%s = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

func TestVectorBuiltins_InFilter(t *testing.T) {
	for name, c := range vectorBuiltinCases {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				src  string
				keep float64
			}{
				{`abs((` + c.src + `) - ` + formatWant(c.want) + `) < 1e-12`, 1},
				{`abs((` + c.src + `) - ` + formatWant(c.want) + ` - 1) < 1e-12`, 0},
			} {
				resp, err := processVector(t, &types.Request{
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: tc.src}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				})
				if err != nil {
					t.Fatalf("%s: %v", tc.src, err)
				}
				if got := toFloat(resp.Data[0]["n"]); got != tc.keep {
					t.Errorf("filter %s kept %v rows, want %v", tc.src, got, tc.keep)
				}
			}
		})
	}
}

func TestVectorBuiltins_InTotal(t *testing.T) {
	for name, c := range vectorBuiltinCases {
		t.Run(name, func(t *testing.T) {
			resp, err := processVector(t, &types.Request{
				Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: `total(` + c.src + `) + 0 * x`, Label: "v"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
			})
			if err != nil {
				t.Fatalf("total(%s): %v", c.src, err)
			}
			if got := resp.Data[0]["s"].(float64); !closeTo(got, c.want) {
				t.Errorf("total(%s) = %v, want %v", c.src, got, c.want)
			}
		})
	}
}

func formatWant(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

func toFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return math.NaN()
}

// topCode is the code of the outermost CodedError in err's chain.
func topCode(err error) perr.Code {
	var ce *perr.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

// TestVectorBuiltins_Refusals: every refusal fails the run under its
// own code at the top of the chain, in each consumer — never swallowed
// as a null row or wrapped as PROCESSING_RUNTIME.
func TestVectorBuiltins_Refusals(t *testing.T) {
	cases := []struct {
		name string
		src  string // a scalar-shaped expression for formula / total
		want perr.Code
	}{
		{"literal length mismatch", `dot([x, y], [1, 2, 3])`, perr.PROCESSING_VECTOR_LENGTH_MISMATCH},
		{"runtime length mismatch", `cosine([x, y], vcenter([x, y, z]))`, perr.PROCESSING_VECTOR_LENGTH_MISMATCH},
		{"runtime dist mismatch", `dist(vzscore([x, y]), [x, y, z])`, perr.PROCESSING_VECTOR_LENGTH_MISMATCH},
		{"literal normalize mode", `vsum(vnormalize([x, y], "L2"))`, perr.PROCESSING_CONFIG},
		{"runtime normalize mode", `vsum(vnormalize([x, y], x > 0 ? "max" : "l1"))`, perr.PROCESSING_CONFIG},
		{"top-level vector (static)", `vcenter([x, y, z])`, perr.PROCESSING_CONFIG},
		{"top-level vector (runtime)", `x > 0 ? vzscore([x, y, z]) : 0`, perr.PROCESSING_CONFIG},
		{"static arity", `sqrt(x, y)`, perr.PROCESSING_CONFIG},
		{"non-numeric element", `vsum([x, "a"])`, perr.PROCESSING_RUNTIME},
		{"non-vector argument", `vmean(x)`, perr.PROCESSING_RUNTIME},
		{"non-numeric scalar", `sqrt("a")`, perr.PROCESSING_RUNTIME},
		{"non-string mode", `vsum(vnormalize([x], x))`, perr.PROCESSING_RUNTIME},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sites := map[string]*types.Request{
				"formula": {
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: c.src, Label: "v"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
				},
				"filter": {
					Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `(` + c.src + `) != -12345`}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
				},
				"total": {
					Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: `total(` + c.src + `)`, Label: "v"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
				},
			}
			for site, req := range sites {
				if site == "filter" && strings.HasPrefix(c.name, "top-level") {
					// A filter's top-level result is the comparison; its own
					// vector refusal is TestVectorBuiltins_FilterVectorResult.
					continue
				}
				_, err := processVector(t, req)
				if got := topCode(err); got != c.want {
					t.Errorf("%s %s: top code %q (err %v), want %s", site, c.src, got, err, c.want)
				}
			}
		})
	}
}

func TestVectorBuiltins_FilterVectorResult(t *testing.T) {
	for _, src := range []string{`vcenter([x, y])`, `x > 0 ? vnormalize([x, y], "l1") : false`} {
		_, err := processVector(t, &types.Request{
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: src}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		})
		if got := topCode(err); got != perr.PROCESSING_CONFIG || !strings.Contains(err.Error(), "returns a vector") {
			t.Errorf("filter %s: %v, want PROCESSING_CONFIG vector refusal", src, err)
		}
	}
}

// TestVectorBuiltins_NullInputRefusalNotSwallowed: a null operand drops a
// filter row as UNKNOWN, but a length mismatch on that row still fails
// the run.
func TestVectorBuiltins_NullInputRefusalNotSwallowed(t *testing.T) {
	schema := vectorTestSchema()
	records := []*Record{NewRecordWithNulls(schema, map[string]float64{"y": 2, "z": 3}, map[string]bool{"x": true})}
	proc := NewProcessorWithExtensions(schema, &ExtensionRegistry{})
	_, err := proc.Process(context.Background(), &types.Request{
		Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `dot([x, y], vcenter([y, z, z])) > 0`}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y", Label: "n"}},
	}, NewSliceIterator(records))
	if got := topCode(err); got != perr.PROCESSING_VECTOR_LENGTH_MISMATCH {
		t.Fatalf("null-input mismatch: %v, want PROCESSING_VECTOR_LENGTH_MISMATCH", err)
	}

	// The null operand itself reads as NaN inside a literal and is skipped.
	resp, err := proc.Process(context.Background(), &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "y", Expression: `vmean([x, y, z])`, Label: "m"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "s"}},
	}, NewSliceIterator([]*Record{NewRecordWithNulls(schema, map[string]float64{"y": 2, "z": 3}, map[string]bool{"x": true})}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"].(float64); got != 2.5 {
		t.Errorf("vmean([null, 2, 3]) = %v, want 2.5", got)
	}
}

// TestVectorBuiltins_FilterPrecomputePure: a filter calling any reserved
// built-in keeps the filter-precompute fast path.
func TestVectorBuiltins_FilterPrecomputePure(t *testing.T) {
	for _, name := range exprenv.Names() {
		src := `(` + vectorBuiltinCases[name].src + `) > x`
		got, ok := exprPureInputs(src, nil)
		if !ok {
			t.Errorf("%s: filter %s left the precompute fast path", name, src)
			continue
		}
		for _, id := range got {
			if id != "x" && id != "y" && id != "z" {
				t.Errorf("%s: free identifier %q, want only fields", name, id)
			}
		}
	}
}

// TestVectorBuiltins_OverlayRuntimeNoRegistry: the overlay formula's
// nil-registry compile sees the shared built-ins too.
func TestVectorBuiltins_OverlayRuntimeNoRegistry(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindFormula}
	if _, err := compileFormulaProgramWithExtensions(spec, `sqrt(cell) + pow(cell, 2)`, map[string]any{"cell": 0.0}, nil); err != nil {
		t.Fatalf("overlay formula without a registry: %v", err)
	}
}

// TestVectorBuiltins_ExistingExpressionsUnchanged: an expression calling
// none of the new built-ins compiles and evaluates exactly as before —
// the same value, the same error text.
func TestVectorBuiltins_ExistingExpressionsUnchanged(t *testing.T) {
	resp, err := processVector(t, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: `abs(x - z) ** 2 + len([1, 2])`, Label: "v"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["s"].(float64); got != 6 {
		t.Errorf("stock expression = %v, want 6", got)
	}
	_, err = processVector(t, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "x", Expression: `[x, y]`, Label: "v"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "v", Label: "s"}},
	})
	if err == nil || topCode(err) != perr.PROCESSING_RUNTIME || !strings.Contains(err.Error(), "unsupported type []interface {}") {
		t.Errorf("literal-array formula: %v, want the unchanged PROCESSING_RUNTIME unsupported-type error", err)
	}
}

// TestVectorBuiltins_StaticRefusalsAtBuild: the refusals a parse can
// judge fail when the program is built, before any row — so a run over
// zero rows still fails.
func TestVectorBuiltins_StaticRefusalsAtBuild(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want perr.Code
	}{
		{`dot([x, y], [1, 2, 3])`, perr.PROCESSING_VECTOR_LENGTH_MISMATCH},
		{`vsum(vnormalize([x, y], "L1"))`, perr.PROCESSING_CONFIG},
		{`vzscore([x, y])`, perr.PROCESSING_CONFIG},
		{`pow(x)`, perr.PROCESSING_CONFIG},
		{`vsum([x], [y])`, perr.PROCESSING_CONFIG},
	} {
		_, err := newExprProgram(tc.src, "formula", vectorTestSchema(), nil, nil, exprenv.Options())
		if got := topCode(err); got != tc.want {
			t.Errorf("newExprProgram(%s): %v, want %s", tc.src, err, tc.want)
		}
	}
	for _, ok := range []string{`dot([x, y], [1, 2])`, `vsum(vnormalize([x, y], "l1"))`, `(`} {
		if err := vectorStaticRefusal(ok, "formula", nil); err != nil {
			t.Errorf("vectorStaticRefusal(%s) = %v, want nil", ok, err)
		}
	}
}
