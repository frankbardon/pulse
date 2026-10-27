package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// ParamMeta.Example (U53 decision #11, TODO #488): an example value the
// registration probe hands the factory in place of the generic JSONType
// placeholder, so a factory accepting only certain values registers.
// pulse.New checks it is a value of the declared JSONType with the
// request params check's own value checker.

// withParams replaces the Params of every registration in ext.
func withParams(ext pulse.Extensions, params []pulse.ParamMeta) pulse.Extensions {
	for i := range ext.Aggregators {
		ext.Aggregators[i].Params = params
	}
	for i := range ext.Attributes {
		ext.Attributes[i].Params = params
	}
	for i := range ext.Filterers {
		ext.Filterers[i].Params = params
	}
	for i := range ext.Groupers {
		ext.Groupers[i].Params = params
	}
	for i := range ext.Windows {
		ext.Windows[i].Params = params
	}
	for i := range ext.Features {
		ext.Features[i].Params = params
	}
	for i := range ext.Tests {
		ext.Tests[i].Params = params
	}
	for i := range ext.MatrixOps {
		ext.MatrixOps[i].Params = params
	}
	for i := range ext.Regressions {
		ext.Regressions[i].Params = params
	}
	return ext
}

// scaleExample is strictParams with an Example on the Required scale.
func scaleExample(v any) []pulse.ParamMeta {
	ps := append([]pulse.ParamMeta(nil), strictParams...)
	ps[0].Example = v
	return ps
}

// TestExtensions_ProbeParamsUseExample: per category, a factory that
// accepts only scale == 2 (the placeholder 1 fails it, see
// TestExtensions_ProbeParamsRefusalNamesThem) registers once the
// declaration carries Example 2, and runs a conforming request.
func TestExtensions_ProbeParamsUseExample(t *testing.T) {
	t.Parallel()

	two := 2.0
	ctx := context.Background()
	for _, tc := range strictCases() {
		t.Run(tc.category, func(t *testing.T) {
			ext := withParams(onlyCategory(guardedExtensions(&two), tc.category), scaleExample(2))
			p, cohort := openE2E(t, ext)
			if _, err := p.Process(ctx, tc.request(cohort, json.RawMessage(`{"scale":2}`))); err != nil {
				t.Fatalf("Process: %v", err)
			}
		})
	}
}

// enumMode is an aggregator factory accepting only params.mode "a" or
// "b" — an enum-like Required string no generic placeholder satisfies.
func enumMode(s *types.Aggregation, sc *encoding.Schema) (extend.Aggregator, error) {
	var p struct {
		Mode string `json:"mode"`
	}
	if len(s.Params) > 0 {
		if err := json.Unmarshal(s.Params, &p); err != nil {
			return nil, err
		}
	}
	if p.Mode != "a" && p.Mode != "b" {
		return nil, fmt.Errorf("params.mode must be a or b, got %q", p.Mode)
	}
	return stubAggregatorFactory(s, sc)
}

// TestExtensions_ProbeParamsEnumExample: the enum-like factory fails
// pulse.New with the "" placeholder (FACTORY_PANIC naming
// details.probe_params) and registers with Example "a"; an optional
// param's Example is documentation only (not handed to the probe) and
// every Example is projected into the manifest.
func TestExtensions_ProbeParamsEnumExample(t *testing.T) {
	t.Parallel()

	reg := func(example any) pulse.Extensions {
		return pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{Name: "AGG_ACME_ENUM", Description: "enum mode",
			Factory: enumMode, Streamable: true, Accepts: []encoding.FieldType{encoding.FieldTypeF64},
			Params: []pulse.ParamMeta{
				{Name: "mode", JSONType: "string", Required: true, Description: "a or b.", Example: example},
				{Name: "note", JSONType: "string", Description: "Optional.", Example: "free text"},
			}}}}
	}
	_, err := pulse.New(newTestOptions(reg(nil)))
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_FACTORY_PANIC {
		t.Fatalf("pulse.New without Example = %v, want PULSE_EXTENSION_FACTORY_PANIC", err)
	}
	if got, want := ce.Details["probe_params"], map[string]any{"mode": ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("details.probe_params = %#v, want %#v", got, want)
	}
	p, err := pulse.New(newTestOptions(reg("a")))
	if err != nil {
		t.Fatalf("pulse.New with Example: %v", err)
	}
	m := p.Manifest(context.Background())
	params := m.Extensions.Aggregators[0].Params
	if params[0].Example != "a" || params[1].Example != "free text" {
		t.Fatalf("manifest params = %+v, want examples a / free text", params)
	}
}

// TestExtensions_ParamExampleTypeChecked: an Example that is not a
// value of its JSONType (or does not marshal) is refused at pulse.New
// PULSE_EXTENSION_PARAM_INVALID, reason example_type_mismatch, in every
// category; a conforming Example of each type registers.
func TestExtensions_ParamExampleTypeChecked(t *testing.T) {
	t.Parallel()

	bad := []struct {
		name, jsonType string
		example        any
		got            string
	}{
		{"string for number", "number", "1", "string"},
		{"number for string", "string", 1, "number"},
		{"string for boolean", "boolean", "true", "string"},
		{"object for array", "array", map[string]any{}, "object"},
		{"array for object", "object", []string{"x"}, "array"},
		{"raw null", "number", json.RawMessage("null"), "null"},
		{"NaN does not marshal", "number", math.NaN(), "unmarshalable"},
	}
	for _, category := range []string{"aggregator", "attribute", "filterer", "grouper", "window", "feature", "test", "matrix", "regression"} {
		t.Run(category, func(t *testing.T) {
			for _, b := range bad {
				t.Run(b.name, func(t *testing.T) {
					ps := []pulse.ParamMeta{{Name: "scale", JSONType: b.jsonType, Example: b.example}}
					_, err := pulse.New(newTestOptions(withParams(onlyCategory(strictExtensions(), category), ps)))
					var ce *perr.CodedError
					if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_EXTENSION_PARAM_INVALID {
						t.Fatalf("pulse.New = %v, want PULSE_EXTENSION_PARAM_INVALID", err)
					}
					if ce.Details["reason"] != "example_type_mismatch" || ce.Details["expected"] != b.jsonType || ce.Details["got"] != b.got || ce.Details["param_name"] != "scale" {
						t.Fatalf("details = %v", ce.Details)
					}
				})
			}
			t.Run("conforming", func(t *testing.T) {
				if category == "matrix" || category == "regression" {
					// MAT_* / REG_* project into the built-in Param shape.
					p, err := pulse.New(newTestOptions(withParams(onlyCategory(strictExtensions(), category), scaleExample(2))))
					if err != nil {
						t.Fatalf("pulse.New: %v", err)
					}
					m := p.Manifest(context.Background())
					var got any
					if category == "matrix" {
						got = m.Extensions.MatrixOps[0].Params
					} else {
						got = m.Extensions.Regressions[0].Params
					}
					raw, _ := json.Marshal(got)
					if !strings.Contains(string(raw), `"name":"scale"`) || !strings.Contains(string(raw), `"example":2`) {
						t.Fatalf("manifest %s params = %s, want scale example 2", category, raw)
					}
				}
				ps := []pulse.ParamMeta{
					{Name: "scale", JSONType: "number", Required: true, Example: 2.5},
					{Name: "b", JSONType: "boolean", Example: true},
					{Name: "s", JSONType: "string", Example: "x"},
					{Name: "a", JSONType: "array", Example: []any{1, "y"}},
					{Name: "o", JSONType: "object", Example: map[string]any{"k": 1}},
					{Name: "r", JSONType: "number", Example: json.RawMessage("3")},
				}
				if _, err := pulse.New(newTestOptions(withParams(onlyCategory(strictExtensions(), category), ps))); err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
			})
		})
	}
}
