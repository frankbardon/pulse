package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extension_filterer_params_test.go pins #778: an extension filterer's
// FieldInputs hook receives the slot's params on both sides, so a
// filterer whose inputs depend on its params declares them — predict
// and the runtime refuse the same unknown column, and the decode
// projection keeps the column it names (a dropped one would read null
// on every row and silently filter everything out).

const paramColFilter types.FiltererType = "FILTER_TEST_MIN_COLUMN"

const paramColCohort = "paramcol.pulse"

type paramColParams struct {
	Column string  `json:"column"`
	Min    float64 `json:"min"`
}

type paramColBuilder struct{}

func (paramColBuilder) Build(spec *types.Filterer, _ *encoding.Schema) (extend.FilterFunc, error) {
	var p paramColParams
	if err := json.Unmarshal(spec.Params, &p); err != nil {
		return nil, err
	}
	return func(rec extend.Record) (bool, error) {
		v, ok := rec.NumericValue(p.Column)
		return ok && v >= p.Min, nil
	}, nil
}

// paramColPulse builds an instance with the filterer registered and a
// cohort of region (categorical) and x, y, z (f64).
func paramColPulse(t *testing.T) *Pulse {
	t.Helper()
	p, err := New(Options{FS: afero.NewMemMapFs(), Extensions: Extensions{Filterers: []FiltererRegistration{{
		Name:        paramColFilter,
		Description: "Test-only filterer keeping rows whose params.column is at least params.min.",
		Factory:     func() extend.FiltererBuilder { return paramColBuilder{} },
		Params: []ParamMeta{
			{Name: "column", JSONType: "string", Description: "Column compared."},
			{Name: "min", JSONType: "number", Description: "Lower bound, inclusive."},
		},
		FieldInputs: func(raw json.RawMessage) []string {
			var p paramColParams
			if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p.Column == "" {
				return []string{}
			}
			return []string{p.Column}
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	fields := []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "z", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Extension-filterer parity column " + fields[i].Name + "."
		off += fields[i].Type.ByteSize()
	}
	csv := "region,x,y,z\n"
	for i := 0; i < 24; i++ {
		csv += []string{"north", "south", "east", "west"}[i%4] + "," +
			jsonNumber(float64((i*7)%19)) + "," + jsonNumber(float64((i*5)%13)+0.5) + "," + jsonNumber(float64(10+(i*11)%60)) + "\n"
	}
	s := encoding.Schema{Fields: fields}
	src, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte(csv), pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	job := pio.NewImportJob(src, paramColCohort)
	job.Schema = &s
	if _, err := p.Import(context.Background(), job); err != nil {
		t.Fatalf("Import: %v", err)
	}
	return p
}

func paramColSlot(column string) *types.Filterer {
	return &types.Filterer{Type: paramColFilter, Field: "region",
		Params: json.RawMessage(`{"column":"` + column + `","min":40}`)}
}

// TestExtensionFilterer_FieldInputsReadParams: the hook's column is
// refused at predict as at runtime when the cohort lacks it, and kept
// in the projection when it has it — the count equals the same filter
// written as an expression.
func TestExtensionFilterer_FieldInputsReadParams(t *testing.T) {
	t.Parallel()
	p := paramColPulse(t)
	count := func(f *types.Filterer) *Request {
		return &Request{Cohort: &types.Cohort{Filename: paramColCohort},
			Filterers:    []*types.Filterer{f},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}}
	}
	t.Run("unknown_column", func(t *testing.T) {
		assertPredictRuntimeParityWith(t, p, func() *Request { return count(paramColSlot("nope")) }, true)
	})
	t.Run("projection_keeps_column", func(t *testing.T) {
		assertPredictRuntimeParityWith(t, p, func() *Request { return count(paramColSlot("z")) }, false)
		got := scalarOf(t, p, count(paramColSlot("z")), "n")
		want := scalarOf(t, p, count(&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: "z >= 40"}), "n")
		if got != want || want == 0 {
			t.Fatalf("extension filter kept %v rows, the expression %v", got, want)
		}
	})
}

// TestExtensionFilterer_FitOnFieldInputsReadParams: the same hook in a
// fitted attribute's params.fit_on — refused alike at predict and at
// runtime for an unknown column, and fitting on the rows the same
// expression filter selects when the column exists.
func TestExtensionFilterer_FitOnFieldInputsReadParams(t *testing.T) {
	t.Parallel()
	p := paramColPulse(t)
	fitOn := func(f string) *Request {
		return &Request{Cohort: &types.Cohort{Filename: paramColCohort},
			Attributes: []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "m", Fields: []string{"x", "y"},
				Params: json.RawMessage(`{"fit_on":[` + f + `]}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "m", Label: "s"}}}
	}
	ext := func(column string) string {
		return `{"type":"` + string(paramColFilter) + `","field":"region","params":{"column":"` + column + `","min":40}}`
	}
	t.Run("unknown_column", func(t *testing.T) {
		assertPredictRuntimeParityWith(t, p, func() *Request { return fitOn(ext("nope")) }, true)
	})
	t.Run("projection_keeps_column", func(t *testing.T) {
		got := scalarOf(t, p, fitOn(ext("z")), "s")
		want := scalarOf(t, p, fitOn(`{"type":"FILTER_EXPRESSION","expression":"z >= 40"}`), "s")
		if math.IsNaN(want) || got != want {
			t.Fatalf("fit_on extension filter sum %v, the expression %v", got, want)
		}
	})
}

// scalarOf runs req and returns the one row's numeric column key.
func scalarOf(t *testing.T, p *Pulse, req *Request, key string) float64 {
	t.Helper()
	res, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Data))
	}
	switch v := res.Data[0][key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	t.Fatalf("%s = %v (%T), not a number", key, res.Data[0][key], res.Data[0][key])
	return 0
}

// assertPredictRuntimeParityWith is assertPredictRuntimeParity for a
// request whose refusal may come with Predict's other entries (an
// extension-params advisory is never an error): it compares the first
// error only.
func assertPredictRuntimeParityWith(t *testing.T, p *Pulse, build func() *Request, refuse bool) {
	t.Helper()
	ctx := context.Background()
	res, err := p.Predict(ctx, build())
	if err != nil {
		t.Fatal(err)
	}
	_, rerr := p.Process(ctx, build())
	if !refuse {
		if len(res.Errors) != 0 || rerr != nil {
			t.Fatalf("well-formed request refused: predict %+v runtime %v", res.Errors, rerr)
		}
		return
	}
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Fatalf("runtime err = %v, want a coded refusal", rerr)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("predict accepted what the runtime refused: %s %q", ce.Code, ce.Message)
	}
	if pe := res.Errors[0]; pe.Code != string(ce.Code) || pe.Message != ce.Message {
		t.Errorf("runtime %s %q\n predict %s %q", ce.Code, ce.Message, pe.Code, pe.Message)
	}
}

// TestAggRatio_FieldOptionalAndUnchecked pins #779: AGG_RATIO never
// reads its slot's field, so a slot without one runs, a decimal128 one
// is no longer refused (at predict or at runtime), and every spelling
// answers the same ratio — in the aggregations list and a crosstab cell.
func TestAggRatio_FieldOptionalAndUnchecked(t *testing.T) {
	t.Parallel()
	fsys := parityMalformedFS(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	params := json.RawMessage(`{"numerator_field":"age","denominator_field":"age"}`)
	ratio := func(field string) *Request {
		return &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
			Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Field: field, Label: "r", Params: params}}}
	}
	var want float64
	for i, field := range []string{"region", "", "amount"} {
		res, err := p.Predict(ctx, ratio(field))
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Errors) != 0 {
			t.Fatalf("field %q: predict refused %+v", field, res.Errors)
		}
		got := scalarOf(t, p, ratio(field), "r")
		if i == 0 {
			want = got
		}
		if got != want || got != 1 {
			t.Fatalf("field %q: ratio %v, want %v (= 1)", field, got, want)
		}
	}
	cell := &Request{Cohort: &types.Cohort{Filename: parityDecimalCohort},
		Crosstab: &types.CrosstabSpec{Rows: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{Type: types.AGG_RATIO, Params: params}, Margins: types.CrosstabMargins{Grand: true}}}
	assertPredictRuntimeParityWith(t, p, func() *Request { return cell }, false)
}
