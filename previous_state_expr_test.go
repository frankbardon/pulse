package pulse_test

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// prevStateExprForms are expressions over a categorical column, `%s`
// standing for it: equality, membership, nil tests, string operators,
// built-ins and the nil-coalescing form — every form a categorical
// state field supports. A null operand follows the source field's nil
// rules (an evaluation error drops the row as UNKNOWN).
var prevStateExprForms = []string{
	`%s == "paid"`,
	`%s != "paid"`,
	`%s in ["free", "paid"]`,
	`%s not in ["pro"]`,
	`%s != nil`,
	`%s == nil`,
	`%s startsWith "p"`,
	`%s matches "^fr"`,
	`(%s ?? "none") == "none"`,
	`%s == "paid" || %s == "pro"`,
	`len(%s) == 3`,
	`upper(%s) == "FREE"`,
	`%s > "free"`,
}

func prevStateExpr(form, col string) string { return strings.ReplaceAll(form, "%s", col) }

// prevStateBitRequest keeps the records filter admits and reports their
// count and the sum of a per-record bit — 2^(id + 8 × wave) — so two
// results are equal iff they kept the same (subject, wave) records.
func prevStateBitRequest(cohort string, attrs []*types.Attribute, bit string, filters ...*types.Filterer) *types.Request {
	return &types.Request{
		Cohort:     &types.Cohort{Filename: cohort},
		Attributes: append(append([]*types.Attribute(nil), attrs...), &types.Attribute{Type: types.ATTR_FORMULA, Expression: bit, Label: "bit"}),
		Filterers:  filters,
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "id", Label: "n"},
			{Type: types.AGG_SUM, Field: "bit", Label: "bits"},
		},
	}
}

// prevStateKept runs req on predict (which must accept it) and at
// runtime, returning the kept records' count and bit sum.
func prevStateKept(t *testing.T, p *pulse.Pulse, what string, req func() *types.Request) (float64, float64) {
	t.Helper()
	ctx := context.Background()
	pr, err := p.Predict(ctx, req())
	if err != nil {
		t.Fatalf("%s: predict: %v", what, err)
	}
	if !pr.Valid || len(pr.Errors) > 0 {
		t.Fatalf("%s: predict refused: %+v", what, pr.Errors)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatalf("%s: process: %v", what, err)
	}
	row := resp.Data[0]
	n, _ := row["n"].(float64)
	bits, _ := row["bits"].(float64)
	return n, bits
}

// TestPreviousState_ExpressionsReadStateNames: an expression reads the
// previous-state column exactly as it reads its source categorical —
// by state name, nulls absent. For every form and each wave k, the long
// panel filtered to wave k by `form(prev)` keeps exactly the subjects
// the wide twin keeps under `form(plan_t(k-1))` (the state field the
// previous state mirrors), on predict and at runtime; over the whole
// panel (the first record of the stream has a null previous state) it
// keeps the union of the per-wave records.
func TestPreviousState_ExpressionsReadStateNames(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	const longBit = "2 ** (id + 8 * (seen - 1))"
	for _, form := range prevStateExprForms {
		e := prevStateExpr(form, "prev")
		var sumN, sumBits float64
		for k := 1; k <= 3; k++ {
			ln, lb := prevStateKept(t, p, e+" wave "+strconv.Itoa(k), func() *types.Request {
				return prevStateBitRequest("waves.pulse", prevStateAttr(), longBit,
					dayWindow(t, k), &types.Filterer{Type: types.FILTER_EXPRESSION, Expression: e})
			})
			sumN, sumBits = sumN+ln, sumBits+lb
			if k == 1 {
				continue // no wave before the first: every previous state is null
			}
			we := prevStateExpr(form, "plan_t"+strconv.Itoa(k-1))
			wn, wb := prevStateKept(t, p, we, func() *types.Request {
				return prevStateBitRequest("waves_wide.pulse", nil, "2 ** (id + 8 * "+strconv.Itoa(k-1)+")",
					&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: we})
			})
			if ln != wn || lb != wb {
				t.Errorf("%s wave %d: long kept n=%v bits=%v, wide %s kept n=%v bits=%v", e, k, ln, lb, we, wn, wb)
			}
		}
		an, ab := prevStateKept(t, p, e+" whole panel", func() *types.Request {
			return prevStateBitRequest("waves.pulse", prevStateAttr(), longBit,
				&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: e})
		})
		if an != sumN || ab != sumBits {
			t.Errorf("%s whole panel: n=%v bits=%v, per-wave union n=%v bits=%v", e, an, ab, sumN, sumBits)
		}
	}
}

// TestPreviousState_FormulaReadsStateNames: an ATTR_FORMULA reads the
// column by name too — the per-wave sum of `prev == "paid"` (null
// coalesced) equals the wide twin's on plan_t(k-1).
func TestPreviousState_FormulaReadsStateNames(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	mk := func(cohort, col string, attrs []*types.Attribute, filters ...*types.Filterer) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Attributes: append(append([]*types.Attribute(nil), attrs...),
				&types.Attribute{Type: types.ATTR_FORMULA, Expression: `(` + col + ` ?? "none") in ["paid", "pro"] ? 1 : 0`, Label: "hit"}),
			Filterers:    filters,
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "hit", Label: "hits"}},
		}
	}
	for k := 2; k <= 3; k++ {
		long, err := p.Process(ctx, mk("waves.pulse", "prev", prevStateAttr(), dayWindow(t, k)))
		if err != nil {
			t.Fatal(err)
		}
		wide, err := p.Process(ctx, mk("waves_wide.pulse", "plan_t"+strconv.Itoa(k-1), nil))
		if err != nil {
			t.Fatal(err)
		}
		lh, wh := long.Data[0]["hits"], wide.Data[0]["hits"]
		if lh != wh || lh == 0.0 {
			t.Errorf("wave %d: long hits %v, wide %v", k, lh, wh)
		}
	}
}

// TestPreviousState_UpgradeFromFreeFilter: a filter `prev == "free"`
// in wave 2 keeps exactly the from-free transitions — the previous ×
// state crosstab equals the wide plan_t1 × plan_t2 crosstab under
// `plan_t1 == "free"`, cell for cell and label for label.
func TestPreviousState_UpgradeFromFreeFilter(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	req := transitionCrosstab("waves.pulse", "prev", "plan", prevStateAttr())
	req.Filterers = []*types.Filterer{dayWindow(t, 2), {Type: types.FILTER_EXPRESSION, Expression: `prev == "free"`}}
	long, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	wreq := transitionCrosstab("waves_wide.pulse", "plan_t1", "plan_t2", nil)
	wreq.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `plan_t1 == "free"`}}
	wide, err := p.Process(ctx, wreq)
	if err != nil {
		t.Fatal(err)
	}
	lm, wm := long.Crosstab.Matrix, wide.Crosstab.Matrix
	if !reflect.DeepEqual(lm.RowKeys, []types.AxisKey{{"free"}}) {
		t.Fatalf("row keys %v, want [free]", lm.RowKeys)
	}
	if !reflect.DeepEqual(lm.RowKeys, wm.RowKeys) || !reflect.DeepEqual(lm.ColumnKeys, wm.ColumnKeys) || !reflect.DeepEqual(lm.Cells, wm.Cells) {
		t.Errorf("long %v x %v %v, wide %v x %v %v", lm.RowKeys, lm.ColumnKeys, lm.Cells, wm.RowKeys, wm.ColumnKeys, wm.Cells)
	}
}

// TestPreviousState_ExpressionRefusalParity: an expression the source
// field refuses (a string in arithmetic) is refused on the previous-state
// column too, with the same code on predict and at runtime.
func TestPreviousState_ExpressionRefusalParity(t *testing.T) {
	t.Parallel()

	p, fs := prevStateWavesLongAndWide(t, pulse.Options{})
	mk := func() *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "waves.pulse"},
			Attributes:   prevStateAttr(),
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `prev + 1 > 2`}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "plan"}},
		}
	}
	if ce := assertPredictMatchesRefusal(t, p, fs, "waves.pulse", mk); ce.Code != errors.PULSE_FORMULA_COMPILE_ERROR {
		t.Errorf("code %s (%s), want %s", ce.Code, ce.Message, errors.PULSE_FORMULA_COMPILE_ERROR)
	}
}

// TestPreviousState_FitOnReadsStateNames: a fitted attribute's
// params.fit_on expression reads the column by name as well — a
// Mahalanobis fit on `prev == "paid"` equals the fit on the same rows
// picked by their row number (x), on predict and at runtime.
func TestPreviousState_FitOnReadsStateNames(t *testing.T) {
	t.Parallel()

	p, _ := prevStateWavesLongAndWide(t, pulse.Options{})
	ctx := context.Background()
	// The long panel's row i is wave i/8 of subject i%8; its previous
	// state is the subject's wave before.
	var rows []string
	for wave := 1; wave < 3; wave++ {
		for s, w := range prevStateWaves {
			if w[wave-1] == "paid" {
				rows = append(rows, strconv.Itoa(wave*len(prevStateWaves)+s))
			}
		}
	}
	mk := func(fitOn string) func() *types.Request {
		return func() *types.Request {
			return &types.Request{
				Cohort: &types.Cohort{Filename: "waves.pulse"},
				Attributes: append(prevStateAttr(), &types.Attribute{
					Type: types.ATTR_MAHALANOBIS, Label: "d2", Fields: []string{"x", "id"},
					Params: []byte(`{"fit_on":[{"type":"FILTER_EXPRESSION","expression":` + strconv.Quote(fitOn) + `}]}`),
				}),
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d2", Label: "sum"}, {Type: types.AGG_COUNT, Field: "d2", Label: "n"}},
			}
		}
	}
	byName, byRow := mk(`prev == "paid"`), mk(`x in [`+strings.Join(rows, ", ")+`]`)
	for _, req := range []func() *types.Request{byName, byRow} {
		pr, err := p.Predict(ctx, req())
		if err != nil || !pr.Valid || len(pr.Errors) > 0 {
			t.Fatalf("predict: %v %+v", err, pr)
		}
	}
	a, err := p.Process(ctx, byName())
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.Process(ctx, byRow())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.Data, b.Data) || a.Data[0]["n"] != 24.0 {
		t.Errorf("fit on prev == \"paid\" %v, on its rows %v", a.Data, b.Data)
	}
}
