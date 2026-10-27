package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// Extension attribute components (U27 E2-S4): an AttributeRegistration
// with a ComponentSchema + ComponentsFunc emits one
// Response.Components.Attributes entry — the orchestrator's floor plus
// the emitter's keys — declares it in the manifest, and is kept off the
// merge arms (ProcessChain refuses it, at runtime and in predict).

const (
	attrHighCounted = types.AttributeType("ATTR_ACME_HIGH")
	attrHighPlain   = types.AttributeType("ATTR_ACME_HIGH_PLAIN")
)

// highAttr doubles score and counts the rows above 25.
type highAttr struct{ high int }

func (a *highAttr) Row(rec extend.Record, _ string) (float64, error) {
	v, _ := rec.NumericValue("score")
	if v > 25 {
		a.high++
	}
	return 2 * v, nil
}

func (a *highAttr) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		v, err := a.Row(rows.At(i), field)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func highSchema() descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys:         []descriptor.ComponentKey{{Name: "n_high", Type: "int", Description: "Rows scoring above 25."}},
		Mergeability: descriptor.Mergeable,
	}
}

func highRegistration(name types.AttributeType, counted bool) pulse.AttributeRegistration {
	reg := pulse.AttributeRegistration{
		Name: name,
		Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
			return &highAttr{}, nil
		},
		Mode:    pulse.AttributeModeRowLocal,
		Accepts: []encoding.FieldType{encoding.FieldTypeF64},
	}
	if counted {
		reg.ComponentSchema = highSchema()
		reg.ComponentsFunc = func(inst extend.AttributeComputer) (map[string]any, error) {
			return map[string]any{"n_high": inst.(*highAttr).high}, nil
		}
	}
	return reg
}

func TestExtensions_AttributeComponentSchemaParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, path := brandScoreCohort(t, pulse.Extensions{Attributes: []pulse.AttributeRegistration{
		highRegistration(attrHighCounted, true),
		highRegistration(attrHighPlain, false),
	}})
	req := func(at types.AttributeType) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Attributes:   []*types.Attribute{{Type: at, Field: "score"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "sum"}},
		}
	}

	t.Run("runtime entry", func(t *testing.T) {
		resp, err := p.Process(ctx, req(attrHighCounted))
		if err != nil {
			t.Fatal(err)
		}
		// Rows 10..50: three above 25. The label is the effective
		// (default) one, never empty.
		want := []types.AttributeComponents{{Label: "ATTR_ACME_HIGH_score", Type: attrHighCounted, N: 5,
			Operator: map[string]any{"n_high": 3}}}
		if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
			t.Fatalf("Components.Attributes = %+v, want %+v", resp.Components, want)
		}
		if _, ok := resp.Data[0]["sum"]; !ok {
			t.Fatalf("data row lost: %+v", resp.Data)
		}
		plain, err := p.Process(ctx, req(attrHighPlain))
		if err != nil {
			t.Fatal(err)
		}
		if plain.Components == nil || plain.Components.Attributes != nil {
			t.Errorf("a registration without a ComponentsFunc emitted %+v", plain.Components)
		}
	})

	t.Run("manifest", func(t *testing.T) {
		m := p.Manifest(ctx)
		if got := m.ComponentsSchemas.Attributes[string(attrHighCounted)]; !reflect.DeepEqual(got, highSchema()) {
			t.Errorf("components_schemas.attributes[%s] = %+v", attrHighCounted, got)
		}
		if _, ok := m.ComponentsSchemas.Attributes[string(attrHighPlain)]; ok {
			t.Errorf("an undeclared attribute is in components_schemas.attributes")
		}
		if _, ok := m.ComponentsSchemas.Attributes[string(types.ATTR_SCALE_SCORE)]; !ok {
			t.Errorf("the built-in ATTR_SCALE_SCORE schema is missing beside the extension")
		}
	})

	t.Run("kept off the merge arms", func(t *testing.T) {
		chain := func(at types.AttributeType) *types.ChainRequest {
			return &types.ChainRequest{
				Cohort: &types.Cohort{Filename: path},
				Stages: []*types.ChainStage{{Name: "s0", Request: &types.Request{
					Attributes:   []*types.Attribute{{Type: at, Field: "score", Label: "h"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "h", Label: "sum_h"}},
				}}},
			}
		}
		if _, err := p.ProcessChain(ctx, chain(attrHighPlain)); err != nil {
			t.Fatalf("a row-local attribute without components is refused in a chain: %v", err)
		}
		_, err := p.ProcessChain(ctx, chain(attrHighCounted))
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_CHAIN_NOT_MERGEABLE {
			t.Fatalf("component-emitting attribute chain: err = %v, want PULSE_CHAIN_NOT_MERGEABLE", err)
		}
		env, err := p.PredictChain(ctx, chain(attrHighCounted))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_CHAIN_NOT_MERGEABLE) {
			t.Errorf("predict admits what runtime refuses: %+v", env.Errors)
		}
		env, err = p.PredictChain(ctx, chain(attrHighPlain))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Errorf("predict refuses what runtime admits: %+v", env.Errors)
		}
	})
}

// TestExtensions_AttributeComponentSchemaRefusals: the attribute
// declaration follows the shared ComponentSchema rules at pulse.New —
// an emitter without keys, keys without an emitter, and an emitter
// whose keys stray from the schema are each refused.
func TestExtensions_AttributeComponentSchemaRefusals(t *testing.T) {
	t.Parallel()

	emit := func(extend.AttributeComputer) (map[string]any, error) {
		return map[string]any{"n_high": 0}, nil
	}
	cases := []struct {
		name string
		edit func(*pulse.AttributeRegistration)
		code perr.Code
	}{
		{"emitter without schema", func(r *pulse.AttributeRegistration) { r.ComponentsFunc = emit },
			perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA},
		{"schema without emitter", func(r *pulse.AttributeRegistration) { r.ComponentSchema = highSchema() },
			perr.PULSE_EXTENSION_PARAM_INVALID},
		{"emitted key undeclared", func(r *pulse.AttributeRegistration) {
			r.ComponentSchema = highSchema()
			r.ComponentsFunc = func(extend.AttributeComputer) (map[string]any, error) {
				return map[string]any{"n_high": 0, "extra": 1}, nil
			}
		}, perr.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := highRegistration(attrHighPlain, false)
			c.edit(&reg)
			_, err := pulse.New(newTestOptions(pulse.Extensions{Attributes: []pulse.AttributeRegistration{reg}}))
			assertCodedError(t, err, c.code)
		})
	}
}
