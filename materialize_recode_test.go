package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The output column `recode` (types.MaterializeRecode): a rewrite of a
// categorical column's dictionary — relabel, merge, map to null, order —
// refused statically (predict and run alike) when a key is not a source
// label, a null lands on a non-nullable column, or order is not exactly
// the recoded labels. Recode runs BEFORE the column's `type` cast.

// recodeSource writes recode.pulse: answer (categorical_u8, nullable,
// dictionary yes / no / maybe / n/a / unused — "unused" never stored),
// plan (categorical_u16, not nullable, basic / plus / pro) and n (u8),
// over 12 rows: answer cycles yes, no, maybe, n/a, null; plan cycles
// basic, plus, pro.
func recodeSource(t *testing.T, p *Pulse) {
	t.Helper()
	dict := func(labels ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, l := range labels {
			if _, err := d.Add(l); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	b, err := p.NewCohortBuilder(context.Background(), "recode.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "answer", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Description: "Survey answer to the main question",
			Dictionary: dict("yes", "no", "maybe", "n/a", "unused")},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU16, Description: "Subscription plan of the respondent",
			Dictionary: dict("basic", "plus", "pro")},
		{Name: "n", Type: encoding.FieldTypeU8, Description: "Respondent sequence number"},
	}}, CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	answers := []any{"yes", "no", "maybe", "n/a", nil}
	plans := []string{"basic", "plus", "pro"}
	for i := 0; i < 12; i++ {
		if err := b.Append(CohortRow{answers[i%len(answers)], plans[i%len(plans)], uint64(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func recodeReq(cols ...types.MaterializeOutputColumn) *Request {
	return &Request{Cohort: &types.Cohort{Filename: "recode.pulse"}, Output: &types.MaterializeOutput{Columns: cols}}
}

// recodeMap builds a recode map; a "\x00" value stands for null.
func recodeMap(kv ...string) map[string]*string {
	m := map[string]*string{}
	for i := 0; i < len(kv); i += 2 {
		if kv[i+1] == "\x00" {
			m[kv[i]] = nil
			continue
		}
		v := kv[i+1]
		m[kv[i]] = &v
	}
	return m
}

// assertCodes checks column col of rows against want (a nil want is a
// null cell): each label, and its code in the written dictionary d.
func assertCodes(t *testing.T, name string, rows []CohortRow, col int, d *encoding.Dictionary, want []any, codes []int) {
	t.Helper()
	for i, w := range want {
		got := rows[i][col]
		if got != w {
			t.Errorf("%s row %d = %#v, want %#v", name, i, got, w)
			continue
		}
		if w == nil {
			continue
		}
		if id, ok := d.IDFor(w.(string)); !ok || int(id) != codes[i] {
			t.Errorf("%s row %d label %q has code %d (%v), want %d", name, i, w, id, ok, codes[i])
		}
	}
}

// TestMaterializeRecode_RelabelMergeNullOrder: a relabel keeps every
// other label and the dictionary's order and codes; a merge collapses
// labels into the first one's entry; a map to null writes null cells and
// drops the entry; order sets the written dictionary's order (codes
// follow). Unmapped and unused labels are kept. Predict reports exactly
// the written schema, dictionaries included, and no risk.
func TestMaterializeRecode_RelabelMergeNullOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	recodeSource(t, p)
	req := recodeReq(
		types.MaterializeOutputColumn{From: "answer", As: "relabel", Recode: &types.MaterializeRecode{Map: recodeMap("yes", "Y", "no", "N")}},
		types.MaterializeOutputColumn{From: "answer", As: "merged", Recode: &types.MaterializeRecode{Map: recodeMap("maybe", "no", "n/a", "\x00", "yes", "yes")}},
		types.MaterializeOutputColumn{From: "answer", As: "ordered", Recode: &types.MaterializeRecode{
			Map: recodeMap("maybe", "no"), Order: []string{"unused", "n/a", "no", "yes"}}},
		types.MaterializeOutputColumn{From: "plan", Recode: &types.MaterializeRecode{Map: recodeMap("plus", "paid", "pro", "paid")}},
	)
	res, err := p.Materialize(ctx, req, "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings %v", res.Warnings)
	}
	schema, rows := readAll(t, p, "out.pulse")
	for i, want := range [][]string{
		{"Y", "N", "maybe", "n/a", "unused"},
		{"yes", "no", "unused"},
		{"unused", "n/a", "no", "yes"},
		{"basic", "paid"},
	} {
		if got := schema.Fields[i].Dictionary.Values(); !slices.Equal(got, want) {
			t.Errorf("%s dictionary = %v, want %v", schema.Fields[i].Name, got, want)
		}
	}
	if schema.Fields[3].Type != encoding.FieldTypeCategoricalU16 || schema.Fields[3].Nullable {
		t.Errorf("plan = %s nullable=%v, want its own categorical_u16, not nullable", schema.Fields[3].Type, schema.Fields[3].Nullable)
	}
	assertCodes(t, "relabel", rows, 0, schema.Fields[0].Dictionary,
		[]any{"Y", "N", "maybe", "n/a", nil, "Y"}, []int{0, 1, 2, 3, 0, 0})
	assertCodes(t, "merged", rows, 1, schema.Fields[1].Dictionary,
		[]any{"yes", "no", "no", nil, nil, "yes"}, []int{0, 1, 1, 0, 0, 0})
	assertCodes(t, "ordered", rows, 2, schema.Fields[2].Dictionary,
		[]any{"yes", "no", "no", "n/a", nil, "yes"}, []int{3, 2, 2, 1, 0, 3})
	assertCodes(t, "plan", rows, 3, schema.Fields[3].Dictionary,
		[]any{"basic", "paid", "paid", "basic"}, []int{0, 1, 1, 0})

	_, pres := predictMat(t, p, req)
	assertPredictedSchema(t, pres.Columns, schema)
	if len(pres.Risks) != 0 {
		t.Errorf("risks %+v, want none: a recode is settled statically", pres.Risks)
	}
}

// assertRecodeRefusal runs req and predicts it: the run refuses with
// code (details carrying want), writes nothing, and predict's first
// error is the same code, message and details.
func assertRecodeRefusal(t *testing.T, p *Pulse, fsys afero.Fs, req *Request, code errors.Code, want map[string]any) {
	t.Helper()
	_, err := p.Materialize(context.Background(), req, "bad.pulse", MaterializeOptions{})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("run error %v, want %s", err, code)
	}
	for k, v := range want {
		if ce.Details[k] != v {
			t.Errorf("details.%s = %#v, want %#v (%v)", k, ce.Details[k], v, ce.Details)
		}
	}
	assertAbsent(t, fsys, "bad.pulse")
	env, pres := predictMat(t, p, req)
	if pres.Valid || len(env.Errors) == 0 {
		t.Fatalf("predict accepted a request the run refuses with %s", code)
	}
	first := env.Errors[0]
	if first.Code != string(ce.Code) || first.Message != ce.Message || !reflect.DeepEqual(first.Details, ce.Details) {
		t.Errorf("predict first error {%s %q %v}, run {%s %q %v}", first.Code, first.Message, first.Details, ce.Code, ce.Message, ce.Details)
	}
}

// TestMaterializeRecode_UnknownLabelRefused: a recode key the source
// dictionary does not hold — the drift guard — is refused by the run
// and by predict with PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL naming the
// key (and the closest source label), before anything is written.
func TestMaterializeRecode_UnknownLabelRefused(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{})
	recodeSource(t, p)
	req := recodeReq(
		types.MaterializeOutputColumn{From: "plan"},
		types.MaterializeOutputColumn{From: "answer", As: "a", Recode: &types.MaterializeRecode{Map: recodeMap("yes", "Y", "mayb", "M", "zzz", "Z")}},
	)
	// Keys are checked in sorted order: "mayb" is the first unknown.
	assertRecodeRefusal(t, p, fsys, req, errors.PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL, map[string]any{
		"column": "a", "from": "answer", "label": "mayb", "suggestion": "maybe", "path": "output.columns[1].recode.map",
	})
}

// TestMaterializeRecode_StaticRefusals: a map to null on a column whose
// output is not nullable, an order naming a label the recode does not
// write, omitting one it does or naming one twice, and a recode of a
// column that is not categorical are PULSE_MATERIALIZE_OUTPUT_INVALID
// (reason + path + label) before anything is read — predict raises the
// identical refusal. A map to null on a nullable override is legal.
func TestMaterializeRecode_StaticRefusals(t *testing.T) {
	t.Parallel()

	p, fsys := matPulse(t, Options{})
	recodeSource(t, p)
	rc := func(m map[string]*string, order ...string) *types.MaterializeRecode {
		return &types.MaterializeRecode{Map: m, Order: order}
	}
	cases := []struct {
		name string
		col  types.MaterializeOutputColumn
		want map[string]any
	}{
		{"null on non-nullable", types.MaterializeOutputColumn{From: "plan", Recode: rc(recodeMap("pro", "\x00"))},
			map[string]any{"reason": "recode_null_not_nullable", "label": "pro", "path": "output.columns[0].recode.map"}},
		{"null with nullable false", types.MaterializeOutputColumn{From: "answer", Nullable: boolPtr(false), Recode: rc(recodeMap("n/a", "\x00"))},
			map[string]any{"reason": "recode_null_not_nullable", "label": "n/a"}},
		{"order names an unwritten label", types.MaterializeOutputColumn{From: "plan", Recode: rc(recodeMap("pro", "plus"), "basic", "plus", "pro")},
			map[string]any{"reason": "recode_order_unknown", "label": "pro", "path": "output.columns[0].recode.order"}},
		{"order omits a written label", types.MaterializeOutputColumn{From: "plan", Recode: rc(nil, "pro", "basic")},
			map[string]any{"reason": "recode_order_missing", "label": "plus"}},
		{"order names a label twice", types.MaterializeOutputColumn{From: "plan", Recode: rc(nil, "pro", "pro", "basic", "plus")},
			map[string]any{"reason": "recode_order_duplicate", "label": "pro"}},
		{"not categorical", types.MaterializeOutputColumn{From: "n", Recode: rc(recodeMap("1", "one"))},
			map[string]any{"reason": "recode_not_categorical", "path": "output.columns[0].recode"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertRecodeRefusal(t, p, fsys, recodeReq(c.col), errors.PULSE_MATERIALIZE_OUTPUT_INVALID, c.want)
		})
	}

	ok := recodeReq(types.MaterializeOutputColumn{From: "plan", Nullable: boolPtr(true), Recode: rc(recodeMap("pro", "\x00"))})
	if _, err := p.Materialize(context.Background(), ok, "ok.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("a map to null on a nullable override: %v", err)
	}
	_, rows := readAll(t, p, "ok.pulse")
	if rows[2][0] != nil || rows[1][0] != "plus" {
		t.Errorf("rows %v, want pro nulled", rows[:3])
	}
}

// TestMaterializeRecode_ComposesWithAsAndType: recode runs first, then
// the type cast reads the recoded labels — a 257-label categorical_u16
// that a merge brings to 256 narrows to categorical_u8 (refused without
// the merge), labels recoded to digits cast to u8 — and `as` only names
// the result. on_cast_error applies to a recoded column without a type
// (decision: recode / bucket may carry it), still needing a nullable
// output.
func TestMaterializeRecode_ComposesWithAsAndType(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	recodeSource(t, p)
	catSource(t, p)

	narrow := catReq(types.MaterializeOutputColumn{From: "wide257", As: "w8", Type: "categorical_u8",
		Recode: &types.MaterializeRecode{Map: recodeMap("L256", "L0")}})
	if _, err := p.Materialize(ctx, narrow, "narrow.pulse", MaterializeOptions{}); err != nil {
		t.Fatalf("merge then narrow: %v", err)
	}
	schema, rows := readAll(t, p, "narrow.pulse")
	_, src := readAll(t, p, "cat.pulse")
	if f := schema.Fields[0]; f.Name != "w8" || f.Type != encoding.FieldTypeCategoricalU8 || f.Dictionary.Count() != 256 || f.Dictionary.Values()[255] != "L255" {
		t.Fatalf("w8 = %s %s, %d labels", f.Name, f.Type, f.Dictionary.Count())
	}
	for i, r := range rows {
		want := src[i][1]
		if want == "L256" {
			want = "L0"
		}
		if r[0] != want {
			t.Fatalf("row %d = %v, want %v", i, r[0], want)
		}
	}
	// Predict truncates a dictionary to its first 100 labels; the total
	// is the written one.
	_, pres := predictMat(t, p, narrow)
	if c := pres.Columns[0]; c.Type != "categorical_u8" || c.Dictionary == nil || c.Dictionary.TotalEntries != 256 ||
		!slices.Equal(c.Dictionary.Values, schema.Fields[0].Dictionary.Values()[:len(c.Dictionary.Values)]) {
		t.Errorf("predicted w8 = %s %+v", c.Type, c.Dictionary)
	}

	numeric := recodeReq(types.MaterializeOutputColumn{From: "answer", As: "score", Type: "u8", OnCastError: "null",
		Recode: &types.MaterializeRecode{Map: recodeMap("yes", "1", "no", "0", "maybe", "0")}})
	res, err := p.Materialize(ctx, numeric, "score.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, rows = readAll(t, p, "score.pulse")
	for i, want := range []any{uint64(1), uint64(0), uint64(0), nil, nil} {
		if rows[i][0] != want {
			t.Errorf("score row %d = %#v, want %#v", i, rows[i][0], want)
		}
	}
	// "n/a" is not a number: nulled once, at row 3.
	if len(res.Warnings) != 1 || res.Warnings[0].Code != errors.PULSE_MATERIALIZE_CAST_NULLED || res.Warnings[0].Details["first_row"] != int64(3) {
		t.Errorf("warnings %v, want one CAST_NULLED at row 3", res.Warnings)
	}

	onlyRecode := recodeReq(types.MaterializeOutputColumn{From: "answer", OnCastError: "null",
		Recode: &types.MaterializeRecode{Map: recodeMap("yes", "Y")}})
	if _, err := p.Materialize(ctx, onlyRecode, "only.pulse", MaterializeOptions{}); err != nil {
		t.Errorf("on_cast_error on a recoded column without a type: %v", err)
	}
	assertRecodeRefusal(t, p, fsys, recodeReq(types.MaterializeOutputColumn{From: "plan", OnCastError: "null",
		Recode: &types.MaterializeRecode{Map: recodeMap("pro", "P")}}),
		errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_not_nullable"})
	assertRecodeRefusal(t, p, fsys, recodeReq(types.MaterializeOutputColumn{From: "plan", OnCastError: "null"}),
		errors.PULSE_MATERIALIZE_OUTPUT_INVALID, map[string]any{"reason": "on_cast_error_without_type"})
}

// TestMaterializeRecode_DuplicatedColumnsIndependent: the same source
// column written twice, recoded differently, keeps each recode to its
// own column (the plan's map is per output column).
func TestMaterializeRecode_DuplicatedColumnsIndependent(t *testing.T) {
	t.Parallel()

	p, _ := matPulse(t, Options{})
	recodeSource(t, p)
	req := recodeReq(
		types.MaterializeOutputColumn{From: "plan", As: "a", Recode: &types.MaterializeRecode{Map: recodeMap("pro", "A")}},
		types.MaterializeOutputColumn{From: "plan", As: "b", Recode: &types.MaterializeRecode{Map: recodeMap("pro", "B")}},
		types.MaterializeOutputColumn{From: "plan"},
	)
	if _, err := p.Materialize(context.Background(), req, "dup.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, rows := readAll(t, p, "dup.pulse")
	for i, r := range rows[:3] {
		want := []string{"basic", "plus", "pro"}[i]
		a, b := want, want
		if want == "pro" {
			a, b = "A", "B"
		}
		if r[0] != a || r[1] != b || r[2] != want {
			t.Errorf("row %d = %v, want %s / %s / %s", i, r, a, b, strconv.Quote(want))
		}
	}
}
