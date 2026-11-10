package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestOrderedCategorical_PredictMatchesRuntime (U66 E9-S2, #718): the
// ordered-categorical rule of AGG_MIN / AGG_MAX / AGG_MEDIAN /
// AGG_PERCENTILE (internal/orderedcat) refuses the same request with
// the same code, message and details on both sides — at every
// aggregation slot (top level, grouped, crosstab cell, crosstab margin
// aggregation) — and both sides run what neither refuses. Absent
// params keep today's refusal of a categorical field.
func TestOrderedCategorical_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	shapes := map[string]string{
		"top":     `{"aggregations":[$A]}`,
		"grouped": `{"groups":[{"type":"GROUP_CATEGORY","field":"subj"}],"aggregations":[$A]}`,
		"cell":    `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":$A}}`,
		"margin":  `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":{"type":"AGG_COUNT","field":"x","label":"n"},"margins":{"rows":true},"margin_aggregations":[$A]}}`,
		// E9-S3: no margin requested. The buffered arm (an AGG_MEDIAN
		// cell forces it; so does a non-online or decimal auxiliary
		// beside an AGG_COUNT cell) still constructs every margin
		// aggregation, so it refuses exactly as predict does even with
		// nowhere for a figure to land.
		"margin unobserved":            `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":{"type":"AGG_MEDIAN","field":"x","label":"n"},"margin_aggregations":[$A]}}`,
		"margin unobserved count cell": `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"subj"}],"columns":[{"type":"GROUP_CATEGORY","field":"t_categorical_u8"}],"cell":{"type":"AGG_COUNT","field":"x","label":"n"},"margin_aggregations":[$A]}}`,
	}
	cases := []struct {
		name, agg string
		code      errors.Code // "" = both sides run
		reason    string
	}{
		{"median ordered", `{"type":"AGG_MEDIAN","field":"g","label":"a","params":{"ordered":true}}`, "", ""},
		{"min levels", `{"type":"AGG_MIN","field":"g","label":"a","params":{"levels":["b","a"]}}`, "", ""},
		{"percentile levels", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"percentile":75,"levels":["b"]}}`, "", ""},
		{"absent params", `{"type":"AGG_MAX","field":"g","label":"a"}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED, ""},
		{"ordered false", `{"type":"AGG_MEDIAN","field":"g","label":"a","params":{"ordered":false}}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED, ""},
		{"numeric field", `{"type":"AGG_MEDIAN","field":"x","label":"a","params":{"ordered":true}}`, errors.PROCESSING_CONFIG, "ordered_needs_categorical"},
		{"decimal min", `{"type":"AGG_MIN","field":"t_decimal128","label":"a","params":{"ordered":true}}`, errors.PROCESSING_CONFIG, "ordered_needs_categorical"},
		{"decimal median", `{"type":"AGG_MEDIAN","field":"t_decimal128","label":"a","params":{"ordered":true}}`, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL, ""},
		{"unknown level", `{"type":"AGG_MAX","field":"g","label":"a","params":{"levels":["a","zz"]}}`, errors.PROCESSING_CONFIG, "unknown_level"},
		{"duplicate level", `{"type":"AGG_PERCENTILE","field":"g","label":"a","params":{"percentile":10,"levels":["a","b","a"]}}`, errors.PROCESSING_CONFIG, "duplicate_level"},
		{"empty levels", `{"type":"AGG_MIN","field":"g","label":"a","params":{"levels":[]}}`, errors.PROCESSING_CONFIG, "empty_levels"},
		{"invalid params", `{"type":"AGG_MEDIAN","field":"g","label":"a","params":{"ordered":"yes"}}`, errors.PROCESSING_CONFIG, "invalid_params"},
	}
	for shapeName, shape := range shapes {
		for _, c := range cases {
			body := strings.Replace(shape, "$A", c.agg, 1)
			mk := func() *types.Request {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatalf("%s %s: %v", shapeName, c.name, err)
				}
				req.Cohort = &types.Cohort{Filename: cohort}
				return &req
			}
			_, rerr := p.Process(ctx, mk())
			env := predictEnvelope(t, p, fs, cohort, mk())
			if c.code == "" {
				if rerr != nil || len(env.Errors) != 0 {
					t.Errorf("%s %s: runtime %v, predict %+v; want both to run", shapeName, c.name, rerr, env.Errors)
				}
				continue
			}
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) {
				t.Errorf("%s %s: runtime %v, want %s %s", shapeName, c.name, rerr, c.code, c.reason)
				continue
			}
			if len(env.Errors) != 1 {
				t.Errorf("%s %s: predict errors %+v, want exactly the runtime's", shapeName, c.name, env.Errors)
				continue
			}
			sameEntry(t, env, rerr)
		}
	}
}

// TestOrderedCategorical_CodeRefusals: an ordered slot's figure is a
// dictionary index, so the categorical-code rules fire on it exactly as
// on AGG_MODE over a categorical — a series overlay scoring it (Process
// host), a matrix overlay reading a code cell, and a later chain
// stage's numeric reader over the column it forwards — predict and
// runtime alike; the same operator over the numeric x still scores.
func TestOrderedCategorical_CodeRefusals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, data := ccFixture(t, pulse.Options{})
	levels := json.RawMessage(`{"levels":["green","blue","red"]}`)
	ordered := func(r *pulse.Request) *pulse.Request {
		for _, a := range r.Aggregations {
			if a.Field == "color" {
				a.Params = levels
			}
		}
		if r.Crosstab != nil && r.Crosstab.Cell.Field == "color" {
			r.Crosstab.Cell.Params = levels
		}
		return r
	}
	parity := func(t *testing.T, req func() *pulse.Request, slot string) {
		t.Helper()
		pe := predictCodedError(t, p, data, req())
		if pe == nil || pe.Details["reason"] != "categorical_code" || pe.Details["field"] != "color" {
			t.Fatalf("Predict = %+v, want reason categorical_code over color", pe)
		}
		if slot != "" && pe.Details["slot"] != slot {
			t.Fatalf("Predict slot %v, want %s", pe.Details["slot"], slot)
		}
		_, err := p.Process(ctx, req())
		ce := codeOf(t, err)
		if string(ce.Code) != pe.Code || ce.Message != pe.Message || !reflect.DeepEqual(ce.Details, pe.Details) {
			t.Fatalf("parity: runtime %s %q %v, predict %s %q %v", ce.Code, ce.Message, ce.Details, pe.Code, pe.Message, pe.Details)
		}
	}
	for _, op := range []types.AggregationType{types.AGG_MIN, types.AGG_MAX, types.AGG_MEDIAN} {
		t.Run(string(op)+"/series", func(t *testing.T) {
			parity(t, func() *pulse.Request {
				return ordered(ccProcess(op, "color", types.OverlayKindShareOfTotal, "m", nil))
			}, "aggregations[0]")
			if _, err := p.Process(ctx, ccProcess(op, "x", types.OverlayKindShareOfTotal, "m", nil)); err != nil {
				t.Fatalf("%s over x scored: %v", op, err)
			}
		})
		t.Run(string(op)+"/cell", func(t *testing.T) {
			kind := types.OverlayKindIndexVsMargin
			parity(t, func() *pulse.Request {
				return ordered(ccCrosstab("flag", op, "color", ccMatrixSpec(kind)))
			}, "crosstab.cell")
			if _, err := p.Process(ctx, ccCrosstab("flag", op, "x", ccMatrixSpec(kind))); err != nil {
				t.Fatalf("%s cell over x: %v", op, err)
			}
		})
	}
	// Chain: a stage-0 ordered MIN forwards a code; AGG_SUM over it at
	// stage 1 is refused forwarded_code, on both sides.
	t.Run("chain", func(t *testing.T) {
		code, details := crRefusal(t, p, func() *pulse.ChainRequest {
			return crChain([]*types.Aggregation{{Type: types.AGG_MIN, Field: "color", Label: "lo", Params: levels}},
				&types.Request{Aggregations: []*types.Aggregation{crAgg(types.AGG_SUM, "lo")}})
		})
		if code != string(errors.PULSE_FIELD_TYPE_UNSUPPORTED) || details["forwarded_code"] != true || details["field"] != "lo" {
			t.Fatalf("got %s %v, want PULSE_FIELD_TYPE_UNSUPPORTED forwarded_code over lo", code, details)
		}
	})
}
