package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/types"
)

// intLabelFields are the integer-coded field types a response label
// binding may name (augment only), one cohort column each.
var intLabelFields = []struct {
	name string
	typ  encoding.FieldType
}{
	{"a", encoding.FieldTypeU4},
	{"b", encoding.FieldTypeU8},
	{"c", encoding.FieldTypeU16},
	{"d", encoding.FieldTypeU32},
}

// intLabelsPulse is an instance over a cohort holding one nullable column
// of each integer-coded type (a u4, b u8, c u16, d u32) — rows coded 1, 2,
// a code no table lists (9; d's is 4000000000, past 2^31) and a null —
// beside a float, a u64 and a date, with tables keyed as the response
// rule judges them.
func intLabelsPulse(t *testing.T) *Pulse {
	t.Helper()
	fsys := afero.NewMemMapFs()
	fields := []encoding.Field{}
	for _, f := range intLabelFields {
		fields = append(fields, encoding.Field{Name: f.name, Type: f.typ, Nullable: true})
	}
	fields = append(fields,
		encoding.Field{Name: "x", Type: encoding.FieldTypeF64},
		encoding.Field{Name: "big", Type: encoding.FieldTypeU64},
		encoding.Field{Name: "day", Type: encoding.FieldTypeDate},
	)
	b, err := iio.NewCohortBuild(context.Background(), fsys, "c.pulse", &encoding.Schema{Fields: fields}, iio.CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{uint64(1), uint64(1), uint64(1), uint64(1), 1.0, uint64(1), int32(19000)},
		{uint64(2), uint64(2), uint64(2), uint64(2), 2.0, uint64(2), int32(19001)},
		{uint64(9), uint64(9), uint64(9), uint64(4000000000), 3.0, uint64(9), int32(19002)},
		{nil, nil, nil, nil, 4.0, uint64(3), int32(19003)},
	} {
		if err := b.Append(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fsys, Extensions: Extensions{LabelTables: map[string]LabelTable{
		"codes":   {Rows: map[string]string{"1": "One", "2": "Two"}},
		"big":     {Rows: map[string]string{"4000000000": "Four billion"}},
		"sixteen": {Rows: map[string]string{"16": "Sixteen"}},
		"text":    {Rows: map[string]string{"NE": "Northeast"}},
		"padded":  {Rows: map[string]string{"01": "One"}},
		"ordered": {Lookup: func(string) (string, bool, error) { return "", false, nil }, Order: []string{"x1"}},
		"closure": {Lookup: func(k string) (string, bool, error) {
			if k == "1" {
				return "Uno", true, nil
			}
			return "", false, nil
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func augmentOn(field, table string) []*types.LabelBinding {
	return []*types.LabelBinding{{Field: field, Table: table, Mode: types.LabelModeAugment}}
}

func intCohort() *types.Cohort { return &types.Cohort{Filename: "c.pulse"} }

func intGroupRequest(field string, labels []*types.LabelBinding) *types.Request {
	return &types.Request{
		Cohort:       intCohort(),
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: field}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		Labels:       labels,
	}
}

// stripSibling drops the augment sibling from copies of the rows, for
// comparing a labelled response's figures with the unlabelled run's.
func stripSibling(rows []map[string]any, sibling string) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, r := range rows {
		cp := map[string]any{}
		for k, v := range r {
			if k != sibling {
				cp[k] = v
			}
		}
		out[i] = cp
	}
	return out
}

// siblingsByKey maps each row's field value to its sibling cell.
func siblingsByKey(t *testing.T, rows []map[string]any, field string) map[any]any {
	t.Helper()
	out := map[any]any{}
	for _, r := range rows {
		v, ok := r[field]
		if !ok {
			continue
		}
		s, ok := r[field+"_label"]
		if !ok {
			t.Errorf("row %v: %s carries no %s_label sibling", r, field, field)
		}
		out[v] = s
	}
	return out
}

func intLabelWarnings(ws []*types.ResponseWarning) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, w := range ws {
		if w != nil {
			out[w.Code] = w.Details
		}
	}
	return out
}

// TestLabels_IntegerCodedAugmentOnResponses: an augment binding on a u4 /
// u8 / u16 / u32 field labels every response surface that carries the
// field's codes — process group keys, sample cells, a long crosstab's
// rows, chain stages and compose slots — with a "<field>_label" string
// sibling (null on a code the table lacks, one PULSE_LABEL_LOOKUP_MISS
// naming it), while the numeric cell and every figure stay exactly the
// unlabelled run's. A null cell gets no lookup. Facet tallies the field
// numerically (no value keys) and a matrix crosstab's axis keys have no
// sibling column: both warn PULSE_LABEL_NOT_APPLIED and change nothing.
func TestLabels_IntegerCodedAugmentOnResponses(t *testing.T) {
	t.Parallel()
	p := intLabelsPulse(t)
	ctx := context.Background()
	for _, f := range intLabelFields {
		miss := "9"
		if f.name == "d" {
			miss = "4000000000"
		}
		t.Run(f.name+"/process", func(t *testing.T) {
			plain, err := p.Process(ctx, intGroupRequest(f.name, nil))
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Process(ctx, intGroupRequest(f.name, augmentOn(f.name, "codes")))
			if err != nil {
				t.Fatal(err)
			}
			want := map[any]any{"1": "One", "2": "Two", miss: nil}
			if s := siblingsByKey(t, got.Data, f.name); !reflect.DeepEqual(s, want) {
				t.Errorf("group-key siblings = %v, want %v", s, want)
			}
			if !reflect.DeepEqual(stripSibling(got.Data, f.name+"_label"), plain.Data) {
				t.Errorf("labelled rows %v changed the unlabelled %v", got.Data, plain.Data)
			}
			if !reflect.DeepEqual(got.Components, plain.Components) {
				t.Errorf("labelled components changed")
			}
			w := intLabelWarnings(got.Warnings)
			if d, ok := w[string(perrors.PULSE_LABEL_LOOKUP_MISS)]; !ok || !reflect.DeepEqual(d["unresolved_values"], []string{miss}) {
				t.Errorf("lookup miss = %v (present %v), want unresolved [%s]", d, ok, miss)
			}
			if _, ok := w[string(perrors.PULSE_LABEL_NOT_APPLIED)]; ok {
				t.Errorf("a group-key binding warned not applied: %v", got.Warnings)
			}
		})
		t.Run(f.name+"/sample", func(t *testing.T) {
			res, err := p.SampleWithRequest(ctx, &types.SampleRequest{Cohort: intCohort(), N: 10, Labels: augmentOn(f.name, "codes")})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Rows) != 4 {
				t.Fatalf("sample rows = %d, want 4", len(res.Rows))
			}
			missNum := 9.0
			if f.name == "d" {
				missNum = 4000000000
			}
			for i, want := range []struct {
				val     any
				sibling any
			}{{1.0, "One"}, {2.0, "Two"}, {missNum, nil}} {
				row := res.Rows[i]
				if row[f.name] != want.val {
					t.Errorf("row %d: %s = %v (%T), want the number %v", i, f.name, row[f.name], row[f.name], want.val)
				}
				s, ok := row[f.name+"_label"]
				if !ok || s != want.sibling {
					t.Errorf("row %d: sibling = %v (present %v), want %v", i, s, ok, want.sibling)
				}
			}
			if _, ok := res.Rows[3][f.name+"_label"]; ok {
				t.Errorf("a null cell got a sibling: %v", res.Rows[3])
			}
		})
		t.Run(f.name+"/crosstab", func(t *testing.T) {
			xt := func(shape types.CrosstabShape, labels []*types.LabelBinding) *types.Response {
				resp, err := p.Process(ctx, &types.Request{Cohort: intCohort(), Labels: labels, Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.name}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "big"}},
					Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x"},
					Shape:   shape,
				}})
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			long := xt(types.CrosstabShapeLong, augmentOn(f.name, "codes"))
			if s := siblingsByKey(t, long.Data, f.name); !reflect.DeepEqual(s, map[any]any{"1": "One", "2": "Two", miss: nil}) {
				t.Errorf("long crosstab siblings = %v", s)
			}
			if plain := xt(types.CrosstabShapeLong, nil); !reflect.DeepEqual(stripSibling(long.Data, f.name+"_label"), plain.Data) {
				t.Errorf("long crosstab rows changed")
			}
			plain, matrix := xt(types.CrosstabShapeMatrix, nil), xt(types.CrosstabShapeMatrix, augmentOn(f.name, "codes"))
			if !reflect.DeepEqual(matrix.Crosstab, plain.Crosstab) {
				t.Errorf("an augment binding moved the matrix crosstab")
			}
			if d := intLabelWarnings(matrix.Warnings)[string(perrors.PULSE_LABEL_NOT_APPLIED)]; d["reason"] != "augment_on_matrix_axis" {
				t.Errorf("matrix axis warning details = %v, want reason augment_on_matrix_axis", d)
			}
		})
		t.Run(f.name+"/facet", func(t *testing.T) {
			req := func(labels []*types.LabelBinding) *types.FacetRequest {
				return &types.FacetRequest{Cohort: intCohort(), Fields: []string{f.name}, Labels: labels}
			}
			plain, err := p.FacetSchema(ctx, req(nil))
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.FacetSchema(ctx, req(augmentOn(f.name, "codes")))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Fields, plain.Fields) {
				t.Errorf("an augment binding changed the numeric facet: %v vs %v", got.Fields, plain.Fields)
			}
			if d := intLabelWarnings(got.Warnings)[string(perrors.PULSE_LABEL_NOT_APPLIED)]; d["reason"] != "field_not_in_output" {
				t.Errorf("facet warning details = %v, want reason field_not_in_output", d)
			}
		})
		t.Run(f.name+"/chain", func(t *testing.T) {
			resp, err := p.ProcessChain(ctx, &types.ChainRequest{Cohort: intCohort(), Stages: []*types.ChainStage{
				{Request: intGroupRequest(f.name, augmentOn(f.name, "codes"))},
				{Request: &types.Request{
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.name}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "total"}},
					Labels:       augmentOn(f.name, "codes"),
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			want := map[any]any{"1": "One", "2": "Two", miss: nil}
			for i, st := range resp.Stages {
				if s := siblingsByKey(t, st.Data, f.name); !reflect.DeepEqual(s, want) {
					t.Errorf("stage %d siblings = %v, want %v", i, s, want)
				}
			}
		})
		t.Run(f.name+"/compose", func(t *testing.T) {
			resp, err := p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{
				intGroupRequest(f.name, augmentOn(f.name, "codes")),
				intGroupRequest(f.name, nil),
			}})
			if err != nil {
				t.Fatal(err)
			}
			if s := siblingsByKey(t, resp.Responses[0].Data, f.name); !reflect.DeepEqual(s, map[any]any{"1": "One", "2": "Two", miss: nil}) {
				t.Errorf("compose slot siblings = %v", s)
			}
			if !reflect.DeepEqual(stripSibling(resp.Responses[0].Data, f.name+"_label"), resp.Responses[1].Data) {
				t.Errorf("the labelled slot's rows differ from the unlabelled slot's")
			}
		})
	}

	// A u32 code past 2^31 resolves through its exact decimal spelling.
	got, err := p.Process(ctx, intGroupRequest("d", augmentOn("d", "big")))
	if err != nil {
		t.Fatal(err)
	}
	if s := siblingsByKey(t, got.Data, "d"); s["4000000000"] != "Four billion" {
		t.Errorf("u32 wide-code sibling = %v", s)
	}
	res, err := p.SampleWithRequest(ctx, &types.SampleRequest{Cohort: intCohort(), N: 10, Labels: augmentOn("d", "big")})
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Rows[2]["d_label"]; s != "Four billion" {
		t.Errorf("u32 wide-code sample sibling = %v", s)
	}
	// A function-driven table (no entries to check) is looked up cell by cell.
	got, err = p.Process(ctx, intGroupRequest("b", augmentOn("b", "closure")))
	if err != nil {
		t.Fatal(err)
	}
	if s := siblingsByKey(t, got.Data, "b"); !reflect.DeepEqual(s, map[any]any{"1": "Uno", "2": nil, "9": nil}) {
		t.Errorf("closure siblings = %v", s)
	}
}

// intRefusalCases are the response label-binding refusals on numeric
// fields, the code each must raise on every surface (empty: accepted).
var intRefusalCases = []struct {
	name     string
	bindings []*types.LabelBinding
	want     perrors.Code
}{
	{"replace on u8", []*types.LabelBinding{{Field: "b", Table: "codes", Mode: types.LabelModeReplace}}, perrors.PULSE_LABEL_REPLACE_NUMERIC},
	{"default mode on u4", []*types.LabelBinding{{Field: "a", Table: "codes"}}, perrors.PULSE_LABEL_REPLACE_NUMERIC},
	{"replace on u32 beside a valid binding", []*types.LabelBinding{{Field: "b", Table: "codes", Mode: types.LabelModeAugment}, {Field: "d", Table: "codes", Mode: types.LabelModeReplace}}, perrors.PULSE_LABEL_REPLACE_NUMERIC},
	{"replace before an unknown table", []*types.LabelBinding{{Field: "c", Table: "no_such", Mode: types.LabelModeReplace}}, perrors.PULSE_LABEL_REPLACE_NUMERIC},
	{"augment unknown table", augmentOn("c", "no_such"), perrors.PULSE_LABEL_TABLE_UNKNOWN},
	{"text keys on u16", augmentOn("c", "text"), perrors.PULSE_LABEL_KEY_INVALID},
	{"leading zero on u8", augmentOn("b", "padded"), perrors.PULSE_LABEL_KEY_INVALID},
	{"past u4 max", augmentOn("a", "sixteen"), perrors.PULSE_LABEL_KEY_INVALID},
	{"past u8 max", augmentOn("b", "big"), perrors.PULSE_LABEL_KEY_INVALID},
	{"Lookup table Order key on u32", augmentOn("d", "ordered"), perrors.PULSE_LABEL_KEY_INVALID},
	{"within u8 max", augmentOn("b", "sixteen"), ""},
	{"Lookup-only table unchecked", augmentOn("a", "closure"), ""},
	{"float", augmentOn("x", "codes"), perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
	{"replace on float", []*types.LabelBinding{{Field: "x", Table: "codes"}}, perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
	{"u64", augmentOn("big", "codes"), perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
	{"date", augmentOn("day", "codes"), perrors.PULSE_LABEL_FIELD_NOT_CATEGORICAL},
}

func envFirst(env *descriptor.Envelope) *descriptor.EnvelopeEntry {
	if env == nil || len(env.Errors) == 0 {
		return nil
	}
	return env.Errors[0]
}

func intLabelCoded(err error) *perrors.CodedError {
	var ce *perrors.CodedError
	if stderrors.As(err, &ce) {
		return ce
	}
	return nil
}

// assertParity checks the predict entry and the runtime error agree on
// the code (want) and, when refused, on the message and details.
func assertParity(t *testing.T, surface string, pe *descriptor.EnvelopeEntry, rerr error, want perrors.Code) {
	t.Helper()
	if got := labelRefusalCode(rerr); got != want {
		t.Errorf("%s runtime code = %q, want %q", surface, got, want)
	}
	pc := perrors.Code("")
	if pe != nil {
		pc = perrors.Code(pe.Code)
	}
	if pc != want {
		t.Errorf("%s predict code = %q, want %q", surface, pc, want)
	}
	if want == "" || pe == nil {
		return
	}
	ce := intLabelCoded(rerr)
	if ce == nil {
		return
	}
	if pe.Message != ce.Message {
		t.Errorf("%s message: predict %q, runtime %q", surface, pe.Message, ce.Message)
	}
	pd := map[string]any{}
	for k, v := range pe.Details {
		if k != "stage" {
			pd[k] = v
		}
	}
	rd := map[string]any{}
	for k, v := range ce.Details {
		if k != "stage" {
			rd[k] = v
		}
	}
	if !reflect.DeepEqual(pd, rd) {
		t.Errorf("%s details: predict %v, runtime %v", surface, pd, rd)
	}
}

// TestLabels_IntegerCodedRefusalsPredictEqualsRuntime: a replace binding
// (explicit or the default mode) on a u4–u32 field is
// PULSE_LABEL_REPLACE_NUMERIC; an augment binding whose enumerable table
// holds a key no stored integer spells is PULSE_LABEL_KEY_INVALID (the
// export rule); float, u64 and date fields stay
// PULSE_LABEL_FIELD_NOT_CATEGORICAL — and predict raises each with the
// runtime's code, message and details on process, facet, chain (stage 0
// and a later stage's synthesised schema) and compose. Sample, which has
// no predict, raises the same code.
func TestLabels_IntegerCodedRefusalsPredictEqualsRuntime(t *testing.T) {
	t.Parallel()
	p := intLabelsPulse(t)
	ctx := context.Background()
	for _, tc := range intRefusalCases {
		t.Run(tc.name, func(t *testing.T) {
			field := tc.bindings[len(tc.bindings)-1].Field
			groupField := field
			if _, ok := map[string]bool{"x": true, "day": true}[field]; ok {
				groupField = "a"
			}
			req := func() *types.Request {
				r := intGroupRequest(groupField, append([]*types.LabelBinding(nil), tc.bindings...))
				return r
			}

			pr, err := p.Predict(ctx, req())
			if err != nil {
				t.Fatal(err)
			}
			var pe *descriptor.EnvelopeEntry
			if len(pr.Errors) > 0 {
				pe = pr.Errors[0]
			}
			_, rerr := p.Process(ctx, req())
			assertParity(t, "process", pe, rerr, tc.want)

			freq := func() *types.FacetRequest {
				return &types.FacetRequest{Cohort: intCohort(), Fields: []string{groupField}, Labels: append([]*types.LabelBinding(nil), tc.bindings...)}
			}
			fenv, err := p.PredictFacet(ctx, freq())
			if err != nil {
				t.Fatal(err)
			}
			_, rerr = p.FacetSchema(ctx, freq())
			assertParity(t, "facet", envFirst(fenv), rerr, tc.want)

			chain := func() *types.ChainRequest {
				return &types.ChainRequest{Cohort: intCohort(), Stages: []*types.ChainStage{{Request: req()}}}
			}
			cenv, err := p.PredictChain(ctx, chain())
			if err != nil {
				t.Fatal(err)
			}
			_, rerr = p.ProcessChain(ctx, chain())
			assertParity(t, "chain", envFirst(cenv), rerr, tc.want)

			compose := func() *types.ComposedRequest {
				return &types.ComposedRequest{Requests: []*types.Request{req()}}
			}
			kenv, err := p.PredictCompose(ctx, compose())
			if err != nil {
				t.Fatal(err)
			}
			_, rerr = p.Compose(ctx, compose())
			assertParity(t, "compose", envFirst(kenv), rerr, tc.want)

			_, rerr = p.SampleWithRequest(ctx, &types.SampleRequest{Cohort: intCohort(), N: 2, Labels: append([]*types.LabelBinding(nil), tc.bindings...)})
			if got := labelRefusalCode(rerr); got != tc.want {
				t.Errorf("sample code = %q, want %q", got, tc.want)
			}
		})
	}
}
