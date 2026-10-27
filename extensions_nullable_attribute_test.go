package pulse_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

const (
	attrNullEcho      types.AttributeType = "ATTR_NULLTEST_ECHO"
	attrNullEchoPlain types.AttributeType = "ATTR_NULLTEST_PLAIN"
	attrNullEchoOff   types.AttributeType = "ATTR_NULLTEST_OPTOUT"
)

// nullEcho is a row_local extension attribute echoing its numeric
// source, NaN for a null source cell.
type nullEcho struct{}

func (nullEcho) Row(rec extend.Record, field string) (float64, error) {
	v, ok := rec.NumericValue(field)
	if !ok {
		return math.NaN(), nil
	}
	return v, nil
}

func (e nullEcho) Compute(rows extend.Rows, field string) ([]float64, error) {
	out := make([]float64, rows.Len())
	for i := range out {
		out[i], _ = e.Row(rows.At(i), field)
	}
	return out, nil
}

// nullableEcho opts its NaN into null (extend.NullableAttribute);
// emits is the per-instance answer.
type nullableEcho struct {
	nullEcho
	emits bool
}

func (e nullableEcho) EmitsNull() bool { return e.emits }

var _ extend.NullableAttribute = nullableEcho{}

func nullEchoExtensions() pulse.Extensions {
	reg := func(name types.AttributeType, inst extend.AttributeComputer) pulse.AttributeRegistration {
		return pulse.AttributeRegistration{
			Name: name, Mode: pulse.AttributeModeRowLocal,
			Description: "Test-only echo attribute; NaN on a null source.",
			Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
				return inst, nil
			},
			FieldInputs: func(json.RawMessage) []string { return nil },
		}
	}
	return pulse.Extensions{Attributes: []pulse.AttributeRegistration{
		reg(attrNullEcho, nullableEcho{emits: true}),
		reg(attrNullEchoPlain, nullEcho{}),
		reg(attrNullEchoOff, nullableEcho{emits: false}),
	}}
}

// TestExtensions_NullableAttributeEmitsNull pins extend.NullableAttribute
// in every parity mode (streaming, buffered, parallel decode, shard
// archive): an attribute reporting EmitsNull true has its NaN written as
// a null, so a SUM over it equals the SUM over its nullable source —
// Data and the n_null floors alike. Without the interface, or with
// EmitsNull false, NaN stays an ordinary value: n_null is 0 and the
// SUM folds the NaN (the pre-existing, byte-identical behaviour).
func TestExtensions_NullableAttributeEmitsNull(t *testing.T) {
	t.Parallel()

	large := parityLargeCohort(t.TempDir())
	byRegion := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	for _, mode := range parityModes(large) {
		t.Run(mode.name, func(t *testing.T) {
			p, path := mode.open(t, nullEchoExtensions())
			ctx := context.Background()
			run := func(req *types.Request) *types.Response {
				t.Helper()
				req.Cohort = &types.Cohort{Filename: path}
				req.Groups = byRegion
				if mode.decorate != nil {
					req = mode.decorate(req)
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				return resp
			}
			viaAttr := func(at types.AttributeType) *types.Response {
				return run(&types.Request{
					Attributes:   []*types.Attribute{{Type: at, Field: "score", Label: "derived"}},
					Aggregations: []*types.Aggregation{sumAgg("total", "derived")},
				})
			}
			oracle := run(&types.Request{Aggregations: []*types.Aggregation{sumAgg("total", "score")}})
			if oracle.Components == nil || len(oracle.Components.Aggregations) != 1 {
				t.Fatalf("oracle has no aggregation components: %s", mustJSON(t, oracle.Components))
			}
			if oracle.Components.Aggregations[0].NNull == 0 {
				t.Fatalf("oracle n_null = 0: the cohort has no null score, so the null path is untested")
			}

			got := viaAttr(attrNullEcho)
			if g, w := mustJSON(t, got.Data), mustJSON(t, oracle.Data); g != w {
				t.Errorf("EmitsNull true: Data differs from the nullable source\ngot:  %s\nwant: %s", g, w)
			}
			if g, w := mustJSON(t, got.Components.Aggregations), mustJSON(t, oracle.Components.Aggregations); g != w {
				t.Errorf("EmitsNull true: aggregation components differ from the nullable source\ngot:  %s\nwant: %s", g, w)
			}

			for _, at := range []types.AttributeType{attrNullEchoPlain, attrNullEchoOff} {
				resp := viaAttr(at)
				if n := resp.Components.Aggregations[0].NNull; n != 0 {
					t.Errorf("%s: n_null = %d, want 0 (NaN is a value without EmitsNull)", at, n)
				}
				if !anyNaN(resp.Data, "total") {
					t.Errorf("%s: no NaN total; the NaN was not folded as a value", at)
				}
			}
		})
	}
}

// anyNaN reports whether any Data row's key holds a NaN float64.
func anyNaN(rows []map[string]any, key string) bool {
	for _, r := range rows {
		if f, ok := r[key].(float64); ok && math.IsNaN(f) {
			return true
		}
	}
	return false
}
