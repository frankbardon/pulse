package integration_test

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

// Self-emitting operators (#195): a value carrying its own
// Components() method and no registration ComponentsFunc is an emitter
// in its own right. pulse.New probes it exactly like a ComponentsFunc:
// it may declare ComponentSchema.Keys, must declare them, and its
// emitted keys must stay inside the declared set. A panicking or
// failing Components() on the probe instance is a coded refusal.

// selfEmitBehaviour configures what a probe instance's Components()
// does.
type selfEmitBehaviour struct {
	emit   map[string]any
	panics bool
	fails  bool
}

func (b selfEmitBehaviour) Components() (map[string]any, error) {
	if b.panics {
		panic("self-emitter boom")
	}
	if b.fails {
		return nil, stderrors.New("self-emitter failed")
	}
	return b.emit, nil
}

type selfEmitAgg struct{ selfEmitBehaviour }

func (selfEmitAgg) Aggregate(extend.Rows, string) (float64, error) { return 0, nil }

type selfEmitGrouper struct{ selfEmitBehaviour }

func (selfEmitGrouper) Group(rows extend.Rows, _ string) (map[string][]int, error) {
	idx := make([]int, rows.Len())
	for i := range idx {
		idx[i] = i
	}
	return map[string][]int{"all": idx}, nil
}

type selfEmitFilterer struct{ selfEmitBehaviour }

func (selfEmitFilterer) Build(*types.Filterer, *encoding.Schema) (extend.FilterFunc, error) {
	return func(extend.Record) (bool, error) { return true, nil }, nil
}

// selfEmitAttr is a row-local attribute doubling score and counting
// the rows above 25; it reports the count through its OWN Components()
// unless the behaviour overrides it.
type selfEmitAttr struct {
	selfEmitBehaviour
	high int
}

func (a *selfEmitAttr) Row(rec extend.Record, _ string) (float64, error) {
	v, _ := rec.NumericValue("score")
	if v > 25 {
		a.high++
	}
	return 2 * v, nil
}

func (a *selfEmitAttr) Compute(rows extend.Rows, field string) ([]float64, error) {
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

func (a *selfEmitAttr) Components() (map[string]any, error) {
	if a.emit == nil && !a.panics && !a.fails {
		return map[string]any{"n_high": a.high}, nil
	}
	return a.selfEmitBehaviour.Components()
}

func selfEmitSchema(key string) descriptor.ComponentSchema {
	return descriptor.ComponentSchema{
		Keys:         []descriptor.ComponentKey{{Name: key, Type: "int", Description: "Operator figure."}},
		Mergeability: descriptor.Mergeable,
	}
}

// selfEmitExtensions registers one self-emitting operator of category
// cat, built with behaviour b and declaring schema.
func selfEmitExtensions(cat string, b selfEmitBehaviour, schema descriptor.ComponentSchema) pulse.Extensions {
	f64 := []encoding.FieldType{encoding.FieldTypeF64}
	switch cat {
	case "aggregator":
		return pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{
			Name: "AGG_ACME_SELF", Accepts: f64, ComponentSchema: schema,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return selfEmitAgg{b}, nil
			},
		}}}
	case "grouper":
		return pulse.Extensions{Groupers: []pulse.GrouperRegistration{{
			Name: "GROUP_ACME_SELF", Accepts: f64, ComponentSchema: schema,
			Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
				return selfEmitGrouper{b}, nil
			},
		}}}
	case "filterer":
		return pulse.Extensions{Filterers: []pulse.FiltererRegistration{{
			Name: "FILTER_ACME_SELF", Accepts: f64, ComponentSchema: schema,
			Factory: func() extend.FiltererBuilder { return selfEmitFilterer{b} },
		}}}
	case "attribute":
		return pulse.Extensions{Attributes: []pulse.AttributeRegistration{{
			Name: "ATTR_ACME_SELF", Accepts: f64, Mode: pulse.AttributeModeRowLocal, ComponentSchema: schema,
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return &selfEmitAttr{selfEmitBehaviour: b}, nil
			},
		}}}
	}
	panic("unknown category " + cat)
}

func TestExtensions_SelfEmitterProbe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		b      selfEmitBehaviour
		schema descriptor.ComponentSchema
		code   perr.Code // "" = accepted
		reason string
	}{
		{"declared keys accepted", selfEmitBehaviour{emit: map[string]any{"figure": 1}},
			selfEmitSchema("figure"), "", ""},
		{"floor keys tolerated", selfEmitBehaviour{emit: map[string]any{"figure": 1, "n": 2, "n_null": 0}},
			selfEmitSchema("figure"), "", ""},
		{"undeclared key refused", selfEmitBehaviour{emit: map[string]any{"figure": 1, "stray": 2}},
			selfEmitSchema("figure"), perr.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH, "components_schema_mismatch"},
		{"no declared keys refused", selfEmitBehaviour{emit: map[string]any{"figure": 1}},
			descriptor.ComponentSchema{}, perr.PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA, "self_emitter_without_schema_keys"},
		{"panicking Components refused", selfEmitBehaviour{panics: true},
			selfEmitSchema("figure"), perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
		{"failing Components refused", selfEmitBehaviour{fails: true},
			selfEmitSchema("figure"), perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
	}
	for _, cat := range []string{"aggregator", "grouper", "filterer", "attribute"} {
		for _, c := range cases {
			t.Run(cat+"/"+c.name, func(t *testing.T) {
				_, err := pulse.New(newTestOptions(selfEmitExtensions(cat, c.b, c.schema)))
				if c.code == "" {
					if err != nil {
						t.Fatalf("pulse.New refused a coherent self-emitter: %v", err)
					}
					return
				}
				assertCodedError(t, err, c.code)
				var ce *perr.CodedError
				stderrors.As(err, &ce)
				if ce.Details["category"] != cat {
					t.Errorf("details.category = %v, want %s", ce.Details["category"], cat)
				}
				if c.reason != "" && ce.Details["reason"] != c.reason {
					t.Errorf("details.reason = %v, want %s", ce.Details["reason"], c.reason)
				}
				if c.code == perr.PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH && ce.Details["emitter"] != "Components()" {
					t.Errorf("details.emitter = %v, want Components()", ce.Details["emitter"])
				}
			})
		}
	}
}

// TestExtensions_SchemaWithoutAnyEmitterStillRefused: a value with no
// Components() method and no ComponentsFunc still cannot declare keys.
func TestExtensions_SchemaWithoutAnyEmitterStillRefused(t *testing.T) {
	t.Parallel()

	reg := highRegistration(attrHighPlain, false)
	reg.ComponentSchema = highSchema()
	_, err := pulse.New(newTestOptions(pulse.Extensions{Attributes: []pulse.AttributeRegistration{reg}}))
	assertCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID)
	var ce *perr.CodedError
	stderrors.As(err, &ce)
	if ce.Details["reason"] != "schema_without_emitter" {
		t.Errorf("details.reason = %v, want schema_without_emitter", ce.Details["reason"])
	}
}

// TestExtensions_SelfEmittingAttributeParity: a self-emitting attribute
// that declares its keys emits its Components entry at runtime (the
// adapter adopts the method, as it does for the other categories), and
// predict and runtime agree it stays off the chain merge arm.
func TestExtensions_SelfEmittingAttributeParity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const name = types.AttributeType("ATTR_ACME_SELF")
	p, path := brandScoreCohort(t, selfEmitExtensions("attribute", selfEmitBehaviour{}, selfEmitSchema("n_high")))

	resp, err := p.Process(ctx, &types.Request{
		Cohort:       &types.Cohort{Filename: path},
		Attributes:   []*types.Attribute{{Type: name, Field: "score"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "sum"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []types.AttributeComponents{{Label: "ATTR_ACME_SELF_score", Type: name, N: 5,
		Operator: map[string]any{"n_high": 3}}}
	if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
		t.Fatalf("Components.Attributes = %+v, want %+v", resp.Components, want)
	}

	chain := &types.ChainRequest{
		Cohort: &types.Cohort{Filename: path},
		Stages: []*types.ChainStage{{Name: "s0", Request: &types.Request{
			Attributes:   []*types.Attribute{{Type: name, Field: "score", Label: "h"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "h", Label: "sum_h"}},
		}}},
	}
	_, err = p.ProcessChain(ctx, chain)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_CHAIN_NOT_MERGEABLE {
		t.Fatalf("runtime chain: err = %v, want PULSE_CHAIN_NOT_MERGEABLE", err)
	}
	env, err := p.PredictChain(ctx, chain)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_CHAIN_NOT_MERGEABLE) {
		t.Errorf("predict admits what runtime refuses: %+v", env.Errors)
	}
}
