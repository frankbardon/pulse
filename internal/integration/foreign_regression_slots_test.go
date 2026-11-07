package integration_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// foreignRegressionTakers are the attribute routes that read target /
// predictors: the four refit attributes, and ATTR_REG_PREDICT, whose
// scoring rule refuses them in its own words
// (TestRegPredict_PredictRuntimeParity).
var foreignRegressionTakers = map[types.AttributeType]bool{
	types.ATTR_REG_FITTED:    true,
	types.ATTR_REG_RESIDUAL:  true,
	types.ATTR_REG_LEVERAGE:  true,
	types.ATTR_REG_INFLUENCE: true,
	types.ATTR_REG_PREDICT:   true,
}

// foreignRegressionField is the acceptance-cohort column each
// attribute's acceptance template ($F) runs on; any other reads "y".
var foreignRegressionField = map[types.AttributeType]string{
	types.ATTR_PREVIOUS_STATE: "g",
	types.ATTR_DATE_PART:      "t_date",
	types.ATTR_SET_POPCOUNT:   "t_set_u8",
	types.ATTR_SET_HAS:        "t_set_u8",
	types.ATTR_CODE_IN:        "g",
	// Their templates already list x and y beside $F.
	types.ATTR_FACTOR_SCORE: "t_f64",
	types.ATTR_SEM_SCORE:    "t_f64",
}

// foreignRegressionMessage is the general refusal's message for typ.
func foreignRegressionMessage(typ string) string {
	return typ + " does not take target, predictors, penalty, alpha or l1_ratio (only the regression refit attributes read them); name the output column with label"
}

// TestForeignRegressionSlots_RefusedEverywhere: target / predictors /
// penalty / alpha / l1_ratio on every built-in attribute that does not
// read them — and on an extension attribute — are refused by Process,
// ProcessStream and predict with one PROCESSING_CONFIG code, message
// and details (the field-reference walk's
// model.ForeignRegressionSlotRefusal); before U67 sixteen built-ins and
// every extension attribute ignored them silently (penalty / alpha /
// l1_ratio even on ATTR_CODE_IN).
func TestForeignRegressionSlots_RefusedEverywhere(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ext, err := pulse.New(pulse.Options{FS: fs, Extensions: pulse.Extensions{
		Attributes: []pulse.AttributeRegistration{{
			Name:    "ATTR_ACME_ADJUSTMENT",
			Factory: stubAttributeFactory,
			Mode:    pulse.AttributeModeRowLocal,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	type slotCase struct {
		name string
		p    *pulse.Pulse
		tmpl string
	}
	var cases []slotCase
	for _, typ := range types.AllAttributeTypes() {
		if foreignRegressionTakers[typ] {
			continue
		}
		tmpl := acceptanceTemplates[string(typ)]
		if tmpl == "" {
			t.Errorf("%s: no acceptance template to run it on", typ)
			continue
		}
		field := foreignRegressionField[typ]
		if field == "" {
			field = "y"
		}
		cases = append(cases, slotCase{string(typ), p, strings.NewReplacer("$F", field, "$V", "a").Replace(tmpl)})
	}
	cases = append(cases, slotCase{"ATTR_ACME_ADJUSTMENT", ext,
		`{"attributes":[{"type":"ATTR_ACME_ADJUSTMENT","field":"x","label":"o"}],"aggregations":[{"type":"AGG_SUM","field":"o"}]}`})

	for _, c := range cases {
		for _, slot := range []struct {
			name       string
			target     string
			predictors []string
			penalty    string
			alpha      float64
			l1Ratio    float64
		}{
			{"target", "x", nil, "", 0, 0},
			{"predictors", "", []string{"x", "y"}, "", 0, 0},
			{"penalty", "", nil, "l2", 0, 0},
			{"alpha", "", nil, "", 0.5, 0},
			{"l1_ratio", "", nil, "", 0, 0.3},
		} {
			t.Run(c.name+"/"+slot.name, func(t *testing.T) {
				t.Parallel()
				mk := func() *types.Request {
					var req types.Request
					if err := json.Unmarshal([]byte(c.tmpl), &req); err != nil {
						t.Fatalf("template: %v", err)
					}
					req.Cohort = &types.Cohort{Filename: cohort}
					a := req.Attributes[0]
					a.Target, a.Predictors = slot.target, slot.predictors
					a.Penalty, a.Alpha, a.L1Ratio = slot.penalty, slot.alpha, slot.l1Ratio
					return &req
				}
				// The slot runs without them: the refusal is theirs alone.
				clean := mk()
				a := clean.Attributes[0]
				a.Target, a.Predictors, a.Penalty, a.Alpha, a.L1Ratio = "", nil, "", 0, 0
				if _, err := c.p.Process(t.Context(), clean); err != nil {
					t.Fatalf("the slot without target / predictors is refused: %v", err)
				}
				ce := assertPredictMatchesRefusal(t, c.p, fs, cohort, mk)
				if ce.Code != errors.PROCESSING_CONFIG || ce.Message != foreignRegressionMessage(c.name) {
					t.Errorf("runtime %s %q, want %s %q", ce.Code, ce.Message, errors.PROCESSING_CONFIG, foreignRegressionMessage(c.name))
				}
				if ce.Details["target"] != slot.target || ce.Details["attribute"] != c.name ||
					ce.Details["penalty"] != slot.penalty || ce.Details["alpha"] != slot.alpha || ce.Details["l1_ratio"] != slot.l1Ratio {
					t.Errorf("details %#v", ce.Details)
				}
			})
		}
	}
}

// TestForeignRegressionSlots_TakersUnaffected: the refit attributes
// still run with target / predictors, and ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL with a penalty (alpha, l1_ratio), with no general
// refusal on predict or the runtime; a penalty on ATTR_REG_LEVERAGE /
// ATTR_REG_INFLUENCE / ATTR_REG_PREDICT (and alpha or l1_ratio alone on
// ATTR_REG_PREDICT) stays their own refusal, never
// the general one.
func TestForeignRegressionSlots_TakersUnaffected(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	type takerCase struct {
		name    string
		typ     types.AttributeType
		penalty string
		alpha   float64
		l1Ratio float64
	}
	cases := []takerCase{}
	for _, typ := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE} {
		cases = append(cases, takerCase{name: string(typ), typ: typ})
	}
	for _, typ := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL} {
		cases = append(cases,
			takerCase{string(typ) + "/l2", typ, "l2", 0.5, 0},
			takerCase{string(typ) + "/elasticnet", typ, "elasticnet", 0.5, 0.3})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			req := &types.Request{
				Cohort: &types.Cohort{Filename: cohort},
				Attributes: []*types.Attribute{{Type: c.typ, Target: "x", Predictors: []string{"y"}, Label: "o",
					Penalty: c.penalty, Alpha: c.alpha, L1Ratio: c.l1Ratio}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
			}
			if _, err := p.Process(t.Context(), req); err != nil {
				t.Fatalf("runtime refused a refit slot: %v", err)
			}
			if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
				t.Fatalf("predict refused a refit slot: %+v", env.Errors)
			}
		})
	}
	for _, typ := range []types.AttributeType{types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE, types.ATTR_REG_PREDICT} {
		t.Run(string(typ)+"/own-penalty-refusal", func(t *testing.T) {
			t.Parallel()
			mk := func() *types.Request {
				a := &types.Attribute{Type: typ, Target: "x", Predictors: []string{"y"}, Label: "o", Penalty: "l2", Alpha: 0.5}
				if typ == types.ATTR_REG_PREDICT {
					a.Target, a.Predictors = "", nil
					a.Params = json.RawMessage(`{"fit":"m"}`)
				}
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Attributes:   []*types.Attribute{a},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
				}
			}
			ce := assertPredictMatchesRefusal(t, p, fs, cohort, mk)
			if ce.Message == foreignRegressionMessage(string(typ)) || !strings.Contains(ce.Message, "penal") {
				t.Errorf("want %s's own penalty refusal, got %s %q", typ, ce.Code, ce.Message)
			}
		})
	}
	// alpha or l1_ratio alone (no penalty) on ATTR_REG_PREDICT is its
	// own slot_fields refusal too, never ignored.
	for name, set := range map[string]func(*types.Attribute){
		"alpha":    func(a *types.Attribute) { a.Alpha = 0.5 },
		"l1_ratio": func(a *types.Attribute) { a.L1Ratio = 0.3 },
	} {
		t.Run(string(types.ATTR_REG_PREDICT)+"/own-"+name+"-refusal", func(t *testing.T) {
			t.Parallel()
			mk := func() *types.Request {
				a := &types.Attribute{Type: types.ATTR_REG_PREDICT, Label: "o", Params: json.RawMessage(`{"fit":"m"}`)}
				set(a)
				return &types.Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Attributes:   []*types.Attribute{a},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "o"}},
				}
			}
			ce := assertPredictMatchesRefusal(t, p, fs, cohort, mk)
			if ce.Code != errors.PULSE_MODEL_SCORE_INVALID || ce.Message == foreignRegressionMessage(string(types.ATTR_REG_PREDICT)) || !strings.Contains(ce.Message, name) {
				t.Errorf("want ATTR_REG_PREDICT's own %s refusal, got %s %q", name, ce.Code, ce.Message)
			}
		})
	}
}
