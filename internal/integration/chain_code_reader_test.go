package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// U66 E3-S3 (#446). Stage 0 refuses a numeric reader over a
// categorical field (U35); a later chain stage reads the previous
// stage's outputs through a schema that types every aggregation column
// f64, so AGG_SUM over a forwarded AGG_MODE-over-categorical column
// used to sum dictionary indexes. The forwarded code column is judged
// as the type it is a code of (vectors.CodeFieldTypes over the stage's
// ChainCodeColumns), at predict and runtime alike, located
// details.stage. A packed_bool code (0/1) stays legal for SUM /
// AVERAGE (D2), as on stage 0; the code-safe aggregators stay legal.

// crStage0 forwards a categorical code (mc), a packed_bool code (mf)
// and a magnitude (s) per region.
func crStage0() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_SUM, Field: "x", Label: "s"},
		{Type: types.AGG_MODE, Field: "color", Label: "mc"},
		{Type: types.AGG_MODE, Field: "flag", Label: "mf"},
	}
}

// crChain is stage 0 (crStage0, or aggs0 when given) followed by later
// stages, no whole-chain overlay.
func crChain(aggs0 []*types.Aggregation, later ...*types.Request) *pulse.ChainRequest {
	if aggs0 == nil {
		aggs0 = crStage0()
	}
	stages := []*types.ChainStage{{Name: "s0", Request: &types.Request{Groups: ccGroups(), Aggregations: aggs0}}}
	for i, r := range later {
		stages = append(stages, &types.ChainStage{Name: fmt.Sprintf("s%d", i+1), Request: r})
	}
	return &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "cc.pulse"}, Stages: stages}
}

func crAgg(typ types.AggregationType, field string) *types.Aggregation {
	return &types.Aggregation{Type: typ, Field: field, Label: "out"}
}

// crRefusal is the refusal predict reports first and the runtime
// raises for req; both must exist.
func crRefusal(t *testing.T, p *pulse.Pulse, req func() *pulse.ChainRequest) (code string, details map[string]any) {
	t.Helper()
	ctx := context.Background()
	env, err := p.PredictChain(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 {
		t.Fatal("PredictChain: no error, want a refusal")
	}
	_, err = p.ProcessChain(ctx, req())
	ce := codeOf(t, err)
	if string(ce.Code) != env.Errors[0].Code || !reflect.DeepEqual(ce.Details, env.Errors[0].Details) || ce.Message != env.Errors[0].Message {
		t.Fatalf("parity: runtime %s %q %v, predict %s %q %v", ce.Code, ce.Message, ce.Details, env.Errors[0].Code, env.Errors[0].Message, env.Errors[0].Details)
	}
	return env.Errors[0].Code, env.Errors[0].Details
}

// TestChainStage_NumericReaderOverForwardedCodeRefused: every
// aggregator the table refuses over a categorical field is refused
// over a forwarded categorical code at stage >= 1 — predict and runtime
// agree on code, message and details; the mergeable ones (the chain
// gate admits them) with PULSE_FIELD_TYPE_UNSUPPORTED naming the code's
// origin type.
func TestChainStage_NumericReaderOverForwardedCodeRefused(t *testing.T) {
	t.Parallel()

	p, _ := ccFixture(t, pulse.Options{})
	want := func(typ types.AggregationType, field string, stage int) map[string]any {
		return map[string]any{"type": string(typ), "slot": "field", "field": field, "field_type": "categorical_u8",
			"accepts_types": fieldaccept.TypeNames(string(typ), fieldaccept.SlotField), "forwarded_code": true, "stage": stage}
	}
	refused := map[string]bool{}
	for _, typ := range types.AllAggregationTypes() {
		// The numeric readers: refuse a categorical, accept an f64 (a
		// set aggregator refuses the f64 column already).
		if typ.ArrayValued() || fieldaccept.Accepts(string(typ), fieldaccept.SlotField, encoding.FieldTypeCategoricalU8) ||
			!fieldaccept.Accepts(string(typ), fieldaccept.SlotField, encoding.FieldTypeF64) {
			continue
		}
		t.Run(string(typ), func(t *testing.T) {
			code, details := crRefusal(t, p, func() *pulse.ChainRequest {
				return crChain(nil, &types.Request{Groups: ccGroups(), Aggregations: []*types.Aggregation{crAgg(typ, "mc")}})
			})
			if code != string(perr.PULSE_FIELD_TYPE_UNSUPPORTED) {
				// An earlier refusal (the chain gate's not-mergeable,
				// a params refusal): parity is all there is to pin.
				return
			}
			// JSON both: accepts_types is a []string either way.
			raw, _ := json.Marshal(details)
			wb, _ := json.Marshal(want(typ, "mc", 1))
			if string(raw) != string(wb) {
				t.Fatalf("got %s, want %s", raw, wb)
			}
			refused[string(typ)] = true
		})
	}
	for _, typ := range []types.AggregationType{types.AGG_SUM, types.AGG_AVERAGE, types.AGG_MIN, types.AGG_MAX, types.AGG_STDDEV, types.AGG_VARIANCE} {
		if !refused[string(typ)] {
			t.Errorf("%s over a forwarded code was not refused PULSE_FIELD_TYPE_UNSUPPORTED", typ)
		}
	}
	// A params slot the table rows (AGG_RATIO's numerator / denominator)
	// is judged the same way.
	t.Run("AGG_RATIO/denominator_field", func(t *testing.T) {
		code, details := crRefusal(t, p, func() *pulse.ChainRequest {
			return crChain(nil, &types.Request{Groups: ccGroups(), Aggregations: []*types.Aggregation{{Type: types.AGG_RATIO, Field: "s", Label: "out",
				Params: json.RawMessage(`{"numerator_field":"s","denominator_field":"mc"}`)}}})
		})
		if code != string(perr.PULSE_FIELD_TYPE_UNSUPPORTED) || details["slot"] != "denominator_field" || details["field"] != "mc" ||
			details["field_type"] != "categorical_u8" || details["forwarded_code"] != true || details["stage"] != 1 {
			t.Fatalf("got %s %v, want PULSE_FIELD_TYPE_UNSUPPORTED denominator_field mc (categorical_u8) at stage 1", code, details)
		}
	})
	// Smart defaults stay schema-typed: an untyped slot over the f64
	// code column defaults to AGG_SUM, which is refused.
	t.Run("untyped_defaults_to_sum", func(t *testing.T) {
		code, details := crRefusal(t, p, func() *pulse.ChainRequest {
			return crChain(nil, &types.Request{Aggregations: []*types.Aggregation{{Field: "mc"}}})
		})
		if code != string(perr.PULSE_FIELD_TYPE_UNSUPPORTED) || details["type"] != string(types.AGG_SUM) || details["field"] != "mc" {
			t.Fatalf("got %s %v, want PULSE_FIELD_TYPE_UNSUPPORTED AGG_SUM over mc", code, details)
		}
	})
	// Two hops: stage 1's AGG_MODE over the forwarded code is a code
	// again; stage 2's AGG_SUM over it is refused at stage 2.
	t.Run("two_hops", func(t *testing.T) {
		code, details := crRefusal(t, p, func() *pulse.ChainRequest {
			return crChain(nil,
				&types.Request{Groups: ccGroups(), Aggregations: []*types.Aggregation{{Type: types.AGG_MODE, Field: "mc", Label: "m2"}}},
				&types.Request{Aggregations: []*types.Aggregation{crAgg(types.AGG_SUM, "m2")}})
		})
		raw, _ := json.Marshal(details)
		wb, _ := json.Marshal(want(types.AGG_SUM, "m2", 2))
		if code != string(perr.PULSE_FIELD_TYPE_UNSUPPORTED) || string(raw) != string(wb) {
			t.Fatalf("got %s %s, want PULSE_FIELD_TYPE_UNSUPPORTED %s", code, raw, wb)
		}
	})
}

// TestChainStage_CodeSafeReadersOverForwardedCodeRun: the code-safe
// aggregators (fieldaccept nonSetValue, and COUNT / NULL_COUNT) over a
// forwarded categorical code, SUM / AVERAGE over a forwarded
// packed_bool code (D2), and SUM over a forwarded magnitude run on
// both sides — and AGG_SUM over the packed_bool code sums the 0/1
// modes.
func TestChainStage_CodeSafeReadersOverForwardedCodeRun(t *testing.T) {
	t.Parallel()

	p, _ := ccFixture(t, pulse.Options{})
	cases := []struct {
		typ   types.AggregationType
		field string
	}{
		{types.AGG_COUNT, "mc"}, {types.AGG_NULL_COUNT, "mc"}, {types.AGG_DISTINCT_COUNT, "mc"},
		{types.AGG_MODE, "mc"}, {types.AGG_MODE_COUNT, "mc"},
		{types.AGG_SUM, "mf"}, {types.AGG_AVERAGE, "mf"}, {types.AGG_SUM, "s"},
	}
	for _, c := range cases {
		t.Run(string(c.typ)+"/"+c.field, func(t *testing.T) {
			ccChainRuns(t, p, func() *pulse.ChainRequest {
				return crChain(nil, &types.Request{Aggregations: []*types.Aggregation{crAgg(c.typ, c.field)}})
			})
		})
	}
	// east / north / south modal flags are true / false / true.
	out := ccChainRuns(t, p, func() *pulse.ChainRequest {
		return crChain(nil, &types.Request{Aggregations: []*types.Aggregation{crAgg(types.AGG_SUM, "mf")}})
	})
	if got := out.Final.Data[0]["out"]; got != 2.0 {
		t.Fatalf("AGG_SUM over the forwarded packed_bool modes = %v, want 2", got)
	}
}

// crPick is a mergeable extension aggregator returning the last value
// it reads — an input-valued pick when declared EmitsInputValue.
type crPick struct {
	v   float64
	set bool
}

func (a *crPick) Aggregate(rows extend.Rows, field string) (float64, error) {
	for i := 0; i < rows.Len(); i++ {
		if err := a.UpdateRow(rows.At(i), field); err != nil {
			return 0, err
		}
	}
	return a.Finalize()
}

func (a *crPick) UpdateRow(rec extend.Record, field string) error {
	if v, ok := rec.NumericValue(field); ok {
		a.v, a.set = v, true
	}
	return nil
}

func (a *crPick) Finalize() (float64, error) { return a.v, nil }

func (a *crPick) Merge(other extend.OnlineAggregator) error {
	if o, ok := other.(*crPick); ok && o.set {
		a.v, a.set = o.v, true
	}
	return nil
}

// TestChainStage_ExtensionCodeEmitterPropagates: an extension
// aggregator declared EmitsInputValue over a categorical field
// forwards a code exactly like AGG_MODE — AGG_SUM over it at stage 1 is
// refused on both sides; undeclared, its output is a magnitude and the
// sum runs.
func TestChainStage_ExtensionCodeEmitterPropagates(t *testing.T) {
	t.Parallel()

	const typ = types.AggregationType("AGG_CCTEST_LASTPICK")
	reg := func(declared bool) pulse.AggregatorRegistration {
		return pulse.AggregatorRegistration{
			Name:            typ,
			Description:     "Test aggregator returning the last value of its field.",
			EmitsInputValue: declared,
			Streamable:      true,
			Mergeable:       true,
			Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
				return &crPick{}, nil
			},
		}
	}
	req := func() *pulse.ChainRequest {
		return crChain([]*types.Aggregation{{Type: typ, Field: "color", Label: "pc"}},
			&types.Request{Aggregations: []*types.Aggregation{crAgg(types.AGG_SUM, "pc")}})
	}
	declared, _ := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg(true)}}})
	code, details := crRefusal(t, declared, req)
	if code != string(perr.PULSE_FIELD_TYPE_UNSUPPORTED) || details["field"] != "pc" || details["field_type"] != "categorical_u8" || details["stage"] != 1 {
		t.Fatalf("declared: got %s %v, want PULSE_FIELD_TYPE_UNSUPPORTED over pc (categorical_u8) at stage 1", code, details)
	}
	undeclared, _ := ccFixture(t, pulse.Options{Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg(false)}}})
	ccChainRuns(t, undeclared, req)
}
