package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// facet_params_test.go — FacetRequest.Params (expression-wires E2-S4):
// a facet's filterers read params.<name> exactly as a Request's do —
// the same values, the same refusals, predict ≡ run, the
// capability:params gate, and a template's $var landing in the block.
// Fixture: openParams (wave cycles 1..5 over 20 rows).

// facetWhere is a facet of region over the rows FILTER_EXPRESSION expr
// keeps.
func facetWhere(expr string, params types.Params) *types.FacetRequest {
	return &types.FacetRequest{
		Cohort:    paramsCohort(),
		Fields:    []string{"region"},
		Params:    params,
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
	}
}

// TestFacetParams_FilterReadsParams: `wave == params.wave` keeps that
// wave's rows, a list param drives `in`, and the result equals the
// literal filter's — across decode workers.
func TestFacetParams_FilterReadsParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	for _, workers := range []int{1, 4} {
		p := openParams(t, pulse.Options{DecodeWorkers: workers})
		cases := []struct {
			expr, literal string
			params        types.Params
			want          int64
		}{
			{"wave == params.wave", "wave == 3", types.Params{"wave": json.Number("3")}, paramsRows / 5},
			{"wave in params.waves", "wave in [2, 4]", types.Params{"waves": []any{json.Number("2"), json.Number("4")}}, 2 * paramsRows / 5},
		}
		for _, c := range cases {
			got, err := p.FacetSchema(ctx, facetWhere(c.expr, c.params))
			if err != nil {
				t.Fatalf("workers=%d %s: %v", workers, c.expr, err)
			}
			lit, err := p.FacetSchema(ctx, facetWhere(c.literal, nil))
			if err != nil {
				t.Fatalf("workers=%d %s: %v", workers, c.literal, err)
			}
			if got.FilteredRecords != c.want || lit.FilteredRecords != c.want {
				t.Fatalf("workers=%d %s: filtered = %d (literal %d), want %d", workers, c.expr, got.FilteredRecords, lit.FilteredRecords, c.want)
			}
			gb, _ := json.Marshal(got.Fields)
			lb, _ := json.Marshal(lit.Fields)
			if string(gb) != string(lb) {
				t.Fatalf("workers=%d %s: fields %s, literal %s", workers, c.expr, gb, lb)
			}
		}
	}
}

// TestFacetParams_AdditiveFilterReadsParams: an additive field's scope
// filter (the base filter with that field's clauses stripped) compiles
// through the same folding registry.
func TestFacetParams_AdditiveFilterReadsParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	req := facetWhere("wave == params.wave", types.Params{"wave": 2})
	req.Filterers = append(req.Filterers, &types.Filterer{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"west"}})
	req.AdditiveFields = []string{"region"}
	got, err := p.FacetSchema(ctx, req)
	if err != nil {
		t.Fatalf("FacetSchema: %v", err)
	}
	if got.Additive["region"] == nil || got.FilteredRecords != 2 {
		t.Fatalf("result: filtered %d, additive %+v", got.FilteredRecords, got.Additive)
	}
}

// TestFacetParams_PredictMatchesRuntime: every params refusal on a facet
// — invalid values, an undeclared read (with and without a block) and
// a read over a cohort carrying a field named params — is the same code
// and message at predict and at run.
func TestFacetParams_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := openParams(t, pulse.Options{})
	collision := facetWhere("wave == params.w", types.Params{"w": 1})
	collision.Cohort = &types.Cohort{Filename: "params_field.pulse"}
	cases := []struct {
		name string
		req  *types.FacetRequest
		code perr.Code
	}{
		{"null value", facetWhere("wave == params.w", types.Params{"w": nil}), perr.PULSE_PARAMS_INVALID},
		{"mixed list", facetWhere("wave in params.w", types.Params{"w": []any{1, "a"}}), perr.PULSE_PARAMS_INVALID},
		{"invalid name", facetWhere("wave == 1", types.Params{"a-b": 1}), perr.PULSE_PARAMS_INVALID},
		{"undeclared", facetWhere("wave == params.w", types.Params{"v": 1}), perr.PULSE_PARAMS_UNDECLARED},
		{"undeclared no params", facetWhere("wave == params.w", nil), perr.PULSE_PARAMS_UNDECLARED},
		{"field collision", collision, perr.PULSE_PARAMS_FIELD_COLLISION},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, err := p.PredictFacet(ctx, c.req)
			if err != nil {
				t.Fatalf("PredictFacet: %v", err)
			}
			_, runErr := p.FacetSchema(ctx, c.req)
			ce := requireCodedError(t, runErr, c.code)
			found := false
			for _, e := range env.Errors {
				found = found || (e.Code == string(ce.Code) && e.Message == ce.Message)
			}
			if !found {
				t.Fatalf("predict errors %+v lack the run's %s %q", env.Errors, ce.Code, ce.Message)
			}
		})
	}
	// A valid block predicts clean.
	env, err := p.PredictFacet(ctx, facetWhere("wave == params.w", types.Params{"w": 2}))
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("valid: %v %+v", err, env)
	}
}

// TestFacetParams_FeatureProfileGate: a profile with capability:facet but
// not capability:params refuses a set facet params as an unknown field
// and its payload schema has no FacetRequest params key; enabling the
// feature runs it.
func TestFacetParams_FeatureProfileGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	openParams(t, pulse.Options{FS: fsys})
	open := func(features ...string) *pulse.Pulse {
		p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: &pulse.FeatureProfile{Features: features}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return p
	}
	req := facetWhere("wave == params.w", types.Params{"w": 2})
	base := []string{"capability:process", "capability:facet", "FILTER_EXPRESSION"}
	hidden := open(base...)
	_, err := hidden.FacetSchema(ctx, req)
	ce := requireCodedError(t, err, perr.PULSE_REQUEST_UNKNOWN_FIELD)
	if fmt.Sprint(ce.Details["unknown_keys"]) != "[params]" {
		t.Fatalf("details = %v", ce.Details)
	}
	enabled := open(append(append([]string{}, base...), "capability:params")...)
	got, err := enabled.FacetSchema(ctx, req)
	if err != nil || got.FilteredRecords != paramsRows/5 {
		t.Fatalf("enabled: %v %+v", err, got)
	}
	for _, c := range []struct {
		p    *pulse.Pulse
		want bool
	}{{hidden, false}, {enabled, true}} {
		raw, err := c.p.PayloadSchema()
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if _, has := doc.Defs["FacetRequest"].Properties["params"]; has != c.want {
			t.Fatalf("payload schema FacetRequest.params present = %v, want %v", has, c.want)
		}
	}
}

// TestFacetParams_TemplateVar: a facet template passes a variable as a
// request parameter ({"$var": ...} inside params) — the typed value
// lands in FacetRequest.Params and the filter reads it.
func TestFacetParams_TemplateVar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	doc := `{"target": "facet",
  "variables": [{"name": "wave", "type": "integer", "required": true}],
  "body": {"cohort": {"filename": "params.pulse"}, "fields": ["region"],
    "params": {"wave": {"$var": "wave"}},
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave == params.wave"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "by_wave.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	p := openParams(t, pulse.Options{TemplateDirs: []string{dir}})
	r, err := p.RenderTemplate("by_wave", map[string]any{"wave": 4})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if r.Facet == nil || fmt.Sprint(r.Facet.Params["wave"]) != "4" {
		t.Fatalf("rendered facet = %+v", r.Facet)
	}
	got, err := p.FacetSchema(context.Background(), r.Facet)
	if err != nil || got.FilteredRecords != paramsRows/5 {
		t.Fatalf("FacetSchema: %v %+v", err, got)
	}
}
