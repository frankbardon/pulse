package pulse_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict compiles every expression (U45 E2-S3, TODO #315): each
// ATTR_FORMULA, FILTER_EXPRESSION and total() argument is type-checked
// against the schema prototype (internal/exprenv) plus the request-bound
// declarations the runtime adds (coef, lookup, total, named vectors,
// embedder ExprFunctions), and an earlier attribute label or a feature
// output — names the runtime only meets on the first record — is
// declared float64, the type every derived column carries. A compile
// failure is PULSE_FORMULA_COMPILE_ERROR on both sides with the same
// message and details (exprenv.CompileError), and a valid expression is
// never refused at predict.

// formulaCompilePulse is the vexpr cohort (b1..b4 nullable f64 — b1 is
// null on row 0 —, score f64, qty u32) under an instance registering
// one embedder function, twice(float64) float64, and one lookup table,
// adj.
func formulaCompilePulse(t *testing.T) (*pulse.Pulse, []byte) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{
		ExprFunctions: []pulse.ExprFunction{{Name: "twice", Fn: func(x float64) float64 { return 2 * x }}},
		LookupTables:  map[string]pulse.LookupTable{"adj": {Rows: map[string]float64{"k": 1.5}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

func formulaCompileRequest(t *testing.T, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: "v.pulse"}
	return &req
}

// formulaCompileCases: bad marks a request both sides refuse with
// PULSE_FORMULA_COMPILE_ERROR; every other one both sides run.
var formulaCompileCases = []struct {
	name string
	body string
	bad  bool
}{
	{"syntax", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty *"}]}`, true},
	{"unknown name", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qtyy * 2"}]}`, true},
	{"unknown function", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"nosuch(qty)"}]}`, true},
	{"type mismatch", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty + \"a\""}]}`, true},
	{"filter type mismatch", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"score > \"x\""}]}`, true},
	{"filter syntax", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"score >"}]}`, true},
	{"total argument", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty / total(qty + \"a\")"}]}`, true},
	{"total result", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"total(qty) + \"a\""}]}`, true},
	{"earlier attribute typed float64", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"qty * 2"},{"type":"ATTR_FORMULA","label":"g","expression":"a + \"x\""}]}`, true},
	{"later attribute unknown", `{"attributes":[{"type":"ATTR_FORMULA","label":"g","expression":"a * 2"},{"type":"ATTR_FORMULA","label":"a","expression":"qty * 2"}]}`, true},
	{"feature output typed float64", `{"features":[{"type":"FEAT_LOG","field":"score"}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"LOG_score + \"x\""}]}`, true},
	// An embedder function is declared untyped (any arguments, any
	// result) on both sides, so its misuse surfaces at evaluation; a
	// misspelt one is an unknown name at compile.
	{"extension function misspelt", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"twicee(score)"}]}`, true},
	// A filter must yield a bool: a result the checker types otherwise is
	// refused at compile on both sides (exprenv.ResultRefusal) — it used
	// to pass predict and fail the run's first row PROCESSING_RUNTIME.
	{"filter non-bool", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"score + 1"}]}`, true},
	{"filter non-bool string", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"\"yes\""}]}`, true},
	{"filter non-bool on feature output", `{"features":[{"type":"FEAT_LOG","field":"score"}],"filterers":[{"type":"FILTER_EXPRESSION","expression":"LOG_score * 2"}]}`, true},
	{"vector as number", `{"vectors":[{"name":"battery","fields":["b*"]}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"len(battery) + \"a\""}]}`, true},

	{"valid formula", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty * 2 + score"}]}`, false},
	{"valid filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"score > 1000 && qty % 2 == 0"}]}`, false},
	{"valid total", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty / total(qty + score)"}]}`, false},
	{"valid extension function", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"twice(score) + 1"}]}`, false},
	{"valid lookup", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"lookup(\"adj\", \"k\") * qty"}]}`, false},
	{"valid earlier attribute", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"qty * 2"},{"type":"ATTR_FORMULA","label":"g","expression":"a + 1"}]}`, false},
	{"valid null earlier attribute", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"b1"},{"type":"ATTR_FORMULA","label":"g","expression":"(a ?? 0) + 1"}]}`, false},
	{"valid feature output", `{"features":[{"type":"FEAT_LOG","field":"score"}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"LOG_score * 2"}]}`, false},
	{"valid untyped filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"twice(score) > 1 ? true : false"}]}`, false},
	{"valid filter on feature output", `{"features":[{"type":"FEAT_LOG","field":"score"}],"filterers":[{"type":"FILTER_EXPRESSION","expression":"LOG_score > 1"}]}`, false},
	{"valid vector", `{"vectors":[{"name":"battery","fields":["b*"]}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"vsum(battery) + len(battery)"}]}`, false},
	{"valid set helper and let", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"let k = qty; k * 2"}]}`, false},
}

func formulaCompileBody(body string) string {
	// Every case aggregates one count, so a request is never empty.
	return body[:len(body)-1] + `,"aggregations":[{"type":"AGG_COUNT","field":"qty","label":"n"}]}`
}

// TestFormulaCompile_PredictMatchesRuntime: on the request root, each
// case's predict verdict equals the runtime's — code, message and every
// details key — and a valid expression is clean on both.
func TestFormulaCompile_PredictMatchesRuntime(t *testing.T) {
	p, data := formulaCompilePulse(t)
	for _, c := range formulaCompileCases {
		t.Run(c.name, func(t *testing.T) {
			body := formulaCompileBody(c.body)
			assertFormulaCompileParity(t, p, data, func() *types.Request { return formulaCompileRequest(t, body) }, c.bad)
		})
	}
}

// assertFormulaCompileParity: predict's verdict on mk()'s request equals
// the runtime's — both PULSE_FORMULA_COMPILE_ERROR with the same message
// and details when bad, both clean otherwise.
func assertFormulaCompileParity(t *testing.T, p *pulse.Pulse, data []byte, mk func() *types.Request, bad bool) {
	t.Helper()
	pe := predictCodedError(t, p, data, mk())
	_, err := p.Process(context.Background(), mk())
	if !bad {
		if pe != nil {
			t.Fatalf("Predict refused a valid expression: %+v", pe)
		}
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		return
	}
	if pe == nil {
		t.Fatalf("Predict passed; Process = %v", err)
	}
	ce := codeOf(t, err)
	if pe.Code != string(perr.PULSE_FORMULA_COMPILE_ERROR) || ce.Code != perr.PULSE_FORMULA_COMPILE_ERROR {
		t.Fatalf("codes: predict %s, runtime %s (%s); want both PULSE_FORMULA_COMPILE_ERROR", pe.Code, ce.Code, ce.Message)
	}
	if pe.Message != ce.Message {
		t.Fatalf("message:\n predict %q\n runtime %q", pe.Message, ce.Message)
	}
	if fmt.Sprint(pe.Details) != fmt.Sprint(ce.Details) {
		t.Fatalf("details:\n predict %v\n runtime %v", pe.Details, ce.Details)
	}
	if pe.Details["compile_error"] == "" || pe.Details["expression"] == "" {
		t.Fatalf("details lack compile_error / expression: %v", pe.Details)
	}
}

// TestFormulaCompile_EveryPredictRoot: the compile runs on the Compose
// slot and chain stage roots too, and on a facet's filterers.
func TestFormulaCompile_EveryPredictRoot(t *testing.T) {
	ctx := context.Background()
	p, _ := formulaCompilePulse(t)
	want := string(perr.PULSE_FORMULA_COMPILE_ERROR)
	bad := formulaCompileBody(`{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty + \"a\""}]}`)
	ok := formulaCompileBody(`{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty + 1"}]}`)
	for _, c := range []struct {
		body string
		bad  bool
	}{{bad, true}, {ok, false}} {
		t.Run(fmt.Sprintf("compose/bad=%v", c.bad), func(t *testing.T) {
			r := formulaCompileRequest(t, c.body)
			r.Label = "a"
			env, err := p.PredictCompose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{r}})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(env.Errors) > 0 && env.Errors[0].Code == want; got != c.bad {
				t.Fatalf("PredictCompose errors = %+v, want refused=%v", env.Errors, c.bad)
			}
		})
		t.Run(fmt.Sprintf("chain/bad=%v", c.bad), func(t *testing.T) {
			r := formulaCompileRequest(t, c.body)
			r.Cohort = nil
			env, err := p.PredictChain(ctx, &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "v.pulse"},
				Stages: []*types.ChainStage{{Name: "s", Request: r}}})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(env.Errors) > 0 && env.Errors[0].Code == want; got != c.bad {
				t.Fatalf("PredictChain errors = %+v, want refused=%v", env.Errors, c.bad)
			}
		})
	}
	t.Run("facet", func(t *testing.T) {
		env, err := p.PredictFacet(ctx, &pulse.FacetRequest{Cohort: &types.Cohort{Filename: "v.pulse"},
			Fields:    []string{"qty"},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `score > "x"`}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != want {
			t.Fatalf("PredictFacet errors = %+v, want %s", env.Errors, want)
		}
	})
}

// calendarFormulaCompileCases run on the calendar cohort (d date, ts
// datetime, dn nullable date, x / y f64, cat categorical): the calendar
// helpers (U45 #316) are declared through the shared env and the
// datetime guard (exprenv.CalendarGuard) runs on both sides, so a bare
// datetime as a day helper's day, a literal unknown date_trunc unit and
// an ill-typed argument are refused identically; to_day over a datetime
// and every helper over a date run.
var calendarFormulaCompileCases = []struct {
	name string
	body string
	bad  bool
}{
	{"bare datetime to weekday", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(ts)"}]}`, true},
	{"bare datetime to month_index in a filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"month_index(ts) > 0"}]}`, true},
	{"bare datetime to date_trunc in a total argument", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"x / total(date_trunc(\"month\", ts))"}]}`, true},
	{"bare datetime in a fit_on filter", `{"attributes":[{"type":"ATTR_MAHALANOBIS","label":"m","fields":["x","y"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"weekday(ts) < 6"}]}}]}`, true},
	{"literal unknown unit", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"date_trunc(\"weeks\", d)"}]}`, true},
	{"unit case-sensitive", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"date_trunc(\"Month\", d) > 0"}]}`, true},
	{"categorical day", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(cat)"}]}`, true},
	{"string instant", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"to_day(\"2024-01-01\")"}]}`, true},
	{"numeric unit", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"date_trunc(1, d)"}]}`, true},

	{"weekday over a date", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(d)"}]}`, false},
	{"to_day over a datetime", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(to_day(ts)) + month_index(to_day(ts))"}]}`, false},
	{"date_trunc over to_day", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"date_trunc(\"quarter\", to_day(ts)) <= d"}]}`, false},
	{"unit from a field", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"date_trunc(cat, d)"}]}`, false},
	{"nullable date coalesced", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"month_index(dn ?? d) - month_index(d)"}]}`, false},
	{"nullable date bare", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(dn)"}]}`, false},
	{"log1p", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"log1p(x) + log1p(y)"}]}`, false},
	{"datetime arithmetic is not guarded", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"weekday(ts / 86400)"}]}`, false},
	{"to_day in a total argument", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"x / total(to_day(ts) - d + 1)"}]}`, false},
	{"to_day in a fit_on filter", `{"attributes":[{"type":"ATTR_MAHALANOBIS","label":"m","fields":["x","y"],"params":{"fit_on":[{"type":"FILTER_EXPRESSION","expression":"weekday(to_day(ts)) < 6"}]}}]}`, false},
}

// TestFormulaCompile_CalendarPredictMatchesRuntime: the calendar
// helpers' verdicts agree at predict and at the run — code, message and
// details — under a request time zone too.
func TestFormulaCompile_CalendarPredictMatchesRuntime(t *testing.T) {
	p, data := calfPulse(t, pulse.Options{})
	for _, c := range calendarFormulaCompileCases {
		for _, zone := range []string{"", "America/New_York"} {
			t.Run(c.name+"/"+zone, func(t *testing.T) {
				body := c.body[:len(c.body)-1] + `,"time_zone":"` + zone + `","aggregations":[{"type":"AGG_COUNT","field":"x","label":"n"}]}`
				assertFormulaCompileParity(t, p, data, func() *types.Request { return calfRequest(t, body) }, c.bad)
			})
		}
	}
}

// decimalFormulaCompileCases run on the parity decimal cohort (region
// categorical, age f64, amount decimal128 scale 2): a decimal operator
// over a decimal and a number compiles on both sides (U45 #314, exact
// decimal operators), an ill-typed operand is refused by both.
var decimalFormulaCompileCases = []struct {
	name string
	body string
	bad  bool
}{
	{"decimal over string", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount / \"x\""}]}`, true},
	{"decimal plus label", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount + region"}]}`, true},
	{"decimal filter over string", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"amount > \"1\""}]}`, true},
	{"decimal modulo", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount % 2"}]}`, true},
	{"decimal total argument over string", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"age / total(amount + \"a\")"}]}`, true},
	{"decimal modulo under a comparison", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"amount % 2 > 1"}]}`, true},
	{"decimal over string under a comparison", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"amount / \"x\" > 1"}]}`, true},
	{"decimal builtin result plus label", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"abs(amount) + region"}]}`, true},
	{"decimal max over string", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"max(amount, \"a\")"}]}`, true},

	{"decimal divided by int", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount / 2"}]}`, false},
	{"decimal ratio compared", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"amount / age > 1.2"}]}`, false},
	{"decimal float escape", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"float(amount) * 1.5"}]}`, false},
	{"decimal coalesced", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"(amount ?? 0) * 1.1"}]}`, false},
	{"decimal total argument", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"amount / total(amount)"}]}`, false},
	{"decimal ternary with a number", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"(age > 30 ? amount : 0) * 2"}]}`, false},
	{"decimal coalesced with a field", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"(amount ?? age) * 2"}]}`, false},
	{"decimal laundered through a map", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"{\"a\": amount}.a > 2.5"}]}`, false},
	{"decimal abs", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"abs(amount - 5)"}]}`, false},
	{"decimal round ceil floor", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"round(amount) + ceil(amount) - floor(amount)"}]}`, false},
	{"decimal max min", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"max(amount, 1) - min(amount, age)"}]}`, false},
	{"decimal int", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"int(amount)"}]}`, false},
	{"decimal mean median sum", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"mean([amount, age]) + median([amount, 1, age]) + sum([amount, 1], # * 2)"}]}`, false},
	{"decimal sort", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"sort([amount, age])[0] > 1"}]}`, false},
	{"decimal in", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"amount in [1.37, 2.5]"}]}`, false},
	{"decimal float-domain builtins", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"sqrt(amount) + pow(amount, 2) + log(amount)"}]}`, false},
	{"decimal earlier attribute", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"amount * 2"},{"type":"ATTR_FORMULA","label":"g","expression":"a + amount"}]}`, false},
}

// decimalFormulaCohort is region (categorical), age (f64) and amount
// (decimal128, scale 2; 1.00..9.99) over 40 rows, laid out by hand: CSV
// import cannot infer a decimal128.
const decimalFormulaCohort = "decimal_formula.pulse"

func decimalFormulaPulse(t *testing.T) (*pulse.Pulse, []byte) {
	t.Helper()
	region := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east", "west"} {
		if _, err := region.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: region},
		{Name: "age", Type: encoding.FieldTypeF64, ByteOffset: 1},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, ByteOffset: 9, Precision: 18, Scale: 2},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		rec := make([]byte, schema.RecordByteSize())
		rec[0] = byte(i % 4)
		binary.LittleEndian.PutUint64(rec[1:9], math.Float64bits(float64(10+(i*7)%53)))
		enc := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(int64(100 + (i*37)%900)))
		copy(rec[9:], enc[:])
		buf.Write(rec)
	}
	data := buf.Bytes()
	fsys := afero.NewMemMapFs()
	if err := afero.WriteFile(fsys, decimalFormulaCohort, data, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	return p, data
}

func decimalFormulaRequest(t *testing.T, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: decimalFormulaCohort}
	return &req
}

// TestFormulaCompile_DecimalPredictMatchesRuntime: the decimal operators
// are declared through the shared env, so predict accepts exactly what
// the runtime runs and refuses an ill-typed decimal expression under the
// runtime's code, message and details.
func TestFormulaCompile_DecimalPredictMatchesRuntime(t *testing.T) {
	p, data := decimalFormulaPulse(t)
	for _, c := range decimalFormulaCompileCases {
		t.Run(c.name, func(t *testing.T) {
			body := c.body[:len(c.body)-1] + `,"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"}]}`
			assertFormulaCompileParity(t, p, data, func() *types.Request { return decimalFormulaRequest(t, body) }, c.bad)
		})
	}
}

// TestDecimalFormula_ExactInFilterAndFormula: on every row of the
// decimal cohort, (amount + 0.1) - 0.1 == amount holds exactly — as a
// FILTER_EXPRESSION verdict and as an ATTR_FORMULA 1/0 summed — while
// the float() escape reproduces float64's misses (the oracle counts
// them, so the exact path is proven to differ).
func TestDecimalFormula_ExactInFilterAndFormula(t *testing.T) {
	ctx := context.Background()
	p, _ := decimalFormulaPulse(t)
	floatMisses := 0
	for i := 0; i < 40; i++ {
		f := float64(100+(i*37)%900) / 100
		if (f+0.1)-0.1 != f {
			floatMisses++
		}
	}
	if floatMisses == 0 {
		t.Fatal("float64 oracle has no miss on this cohort; the test proves nothing")
	}
	count := func(filter, formula string) (kept, summed float64) {
		t.Helper()
		resp, err := p.Process(ctx, decimalFormulaRequest(t, `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"`+filter+`"}],
			"attributes":[{"type":"ATTR_FORMULA","label":"hit","expression":"`+formula+`"}],
			"aggregations":[{"type":"AGG_COUNT","field":"age","label":"n"},{"type":"AGG_SUM","field":"hit","label":"s"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		n, _ := resp.Data[0]["n"].(float64)
		sum, _ := resp.Data[0]["s"].(float64)
		return n, sum
	}
	exact := `(amount + 0.1) - 0.1 == amount`
	if kept, summed := count(exact, exact); kept != 40 || summed != 40 {
		t.Errorf("exact: filter kept %v, formula summed %v; want 40 and 40", kept, summed)
	}
	escaped := `(float(amount) + 0.1) - 0.1 == float(amount)`
	if kept, summed := count(escaped, `amount == amount ? 1 : 0`); kept != float64(40-floatMisses) || summed != kept {
		t.Errorf("float() escape: filter kept %v, formula summed %v; want %d", kept, summed, 40-floatMisses)
	}
}
