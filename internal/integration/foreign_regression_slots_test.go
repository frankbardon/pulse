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
	return typ + " does not take target or predictors (only the regression refit attributes read them); name the output column with label"
}

// TestForeignRegressionSlots_RefusedEverywhere: target / predictors on
// every built-in attribute that does not read them — and on an
// extension attribute — are refused by Process, ProcessStream and
// predict with one PROCESSING_CONFIG code, message and details (the
// field-reference walk's model.ForeignRegressionSlotRefusal); before
// U67 sixteen built-ins and every extension attribute ignored them
// silently.
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
		}{
			{"target", "x", nil},
			{"predictors", "", []string{"x", "y"}},
		} {
			t.Run(c.name+"/"+slot.name, func(t *testing.T) {
				t.Parallel()
				mk := func() *types.Request {
					var req types.Request
					if err := json.Unmarshal([]byte(c.tmpl), &req); err != nil {
						t.Fatalf("template: %v", err)
					}
					req.Cohort = &types.Cohort{Filename: cohort}
					req.Attributes[0].Target = slot.target
					req.Attributes[0].Predictors = slot.predictors
					return &req
				}
				// The slot runs without them: the refusal is theirs alone.
				clean := mk()
				clean.Attributes[0].Target, clean.Attributes[0].Predictors = "", nil
				if _, err := c.p.Process(t.Context(), clean); err != nil {
					t.Fatalf("the slot without target / predictors is refused: %v", err)
				}
				ce := assertPredictMatchesRefusal(t, c.p, fs, cohort, mk)
				if ce.Code != errors.PROCESSING_CONFIG || ce.Message != foreignRegressionMessage(c.name) {
					t.Errorf("runtime %s %q, want %s %q", ce.Code, ce.Message, errors.PROCESSING_CONFIG, foreignRegressionMessage(c.name))
				}
				if ce.Details["target"] != slot.target || ce.Details["attribute"] != c.name {
					t.Errorf("details %#v", ce.Details)
				}
			})
		}
	}
}

// TestForeignRegressionSlots_TakersUnaffected: the refit attributes
// still run with target / predictors (no general refusal on predict or
// the runtime).
func TestForeignRegressionSlots_TakersUnaffected(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	for _, typ := range []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE, types.ATTR_REG_INFLUENCE} {
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Attributes:   []*types.Attribute{{Type: typ, Target: "x", Predictors: []string{"y"}, Label: "o"}},
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
}
