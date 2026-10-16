package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
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
	{"vector as number", `{"vectors":[{"name":"battery","fields":["b*"]}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"len(battery) + \"a\""}]}`, true},

	{"valid formula", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty * 2 + score"}]}`, false},
	{"valid filter", `{"filterers":[{"type":"FILTER_EXPRESSION","expression":"score > 1000 && qty % 2 == 0"}]}`, false},
	{"valid total", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"qty / total(qty + score)"}]}`, false},
	{"valid extension function", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"twice(score) + 1"}]}`, false},
	{"valid lookup", `{"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"lookup(\"adj\", \"k\") * qty"}]}`, false},
	{"valid earlier attribute", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"qty * 2"},{"type":"ATTR_FORMULA","label":"g","expression":"a + 1"}]}`, false},
	{"valid null earlier attribute", `{"attributes":[{"type":"ATTR_FORMULA","label":"a","expression":"b1"},{"type":"ATTR_FORMULA","label":"g","expression":"(a ?? 0) + 1"}]}`, false},
	{"valid feature output", `{"features":[{"type":"FEAT_LOG","field":"score"}],"attributes":[{"type":"ATTR_FORMULA","label":"f","expression":"LOG_score * 2"}]}`, false},
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
	ctx := context.Background()
	p, data := formulaCompilePulse(t)
	for _, c := range formulaCompileCases {
		t.Run(c.name, func(t *testing.T) {
			body := formulaCompileBody(c.body)
			pe := predictCodedError(t, p, data, formulaCompileRequest(t, body))
			_, err := p.Process(ctx, formulaCompileRequest(t, body))
			if !c.bad {
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
		})
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
