package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Compose predict judges every slot with the Process-predict request
// body (operator names, per-operator params, field acceptance, windows,
// tests, crosstab, overlays, labels) — the checks the runtime makes when
// it runs the slot through Process. A slot fault is located by
// details.request, as the runtime locates it.

// slotRequest decodes body into a Request over cohort.
func slotRequest(t *testing.T, cohort, body string) *types.Request {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: cohort}
	return &req
}

// composeOneSlot wraps slot as the second slot of a batch whose first
// slot is clean, so the refusal must be located at details.request = 1.
func composeOneSlot(cohort string, slot *types.Request) *types.ComposedRequest {
	return &types.ComposedRequest{Requests: []*types.Request{
		{
			Cohort:       &types.Cohort{Filename: cohort},
			Label:        "clean",
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		},
		slot,
	}}
}

// assertComposeParity runs c through Compose and PredictCompose and
// requires both to refuse with one entry: the same code and message,
// predict's located at details.request = wantSlot (-1: a batch refusal,
// not located).
func assertComposeParity(t *testing.T, p *pulse.Pulse, name string, mk func() *types.ComposedRequest, wantCode errors.Code, wantSlot int) {
	t.Helper()
	ctx := context.Background()
	_, rerr := p.Compose(ctx, mk())
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) {
		t.Errorf("%s: runtime %v, want a coded %s", name, rerr, wantCode)
		return
	}
	if ce.Code != wantCode {
		t.Errorf("%s: runtime code %s (%s), want %s", name, ce.Code, ce.Message, wantCode)
	}
	env, err := p.PredictCompose(ctx, mk())
	if err != nil {
		t.Fatalf("%s: PredictCompose: %v", name, err)
	}
	res, _ := env.Data.(*pulse.ComposePredictResult)
	if res == nil || res.Valid {
		t.Errorf("%s: predict-compose valid, runtime refused %s %q", name, ce.Code, ce.Message)
	}
	if len(env.Errors) != 1 {
		t.Errorf("%s: predict %v, want exactly one error (runtime %s %q)", name, envCodes(env), ce.Code, ce.Message)
		return
	}
	got := env.Errors[0]
	if got.Code != string(ce.Code) || got.Message != ce.Message {
		t.Errorf("%s: predict %s %q, runtime %s %q", name, got.Code, got.Message, ce.Code, ce.Message)
	}
	// Predict locates the slot (RefusalAt); the runtime locates every
	// coded slot refusal the same way (serial Compose and FailFast
	// ComposeParallel alike).
	if wantSlot >= 0 {
		if got.Details["request"] != wantSlot {
			t.Errorf("%s: predict located at %v, want request %d", name, got.Details["request"], wantSlot)
		}
		if ce.Details["request"] != wantSlot {
			t.Errorf("%s: runtime located at %v (%v), want request %d", name, ce.Details["request"], ce.Details, wantSlot)
		}
		_, perr := p.ComposeParallel(ctx, mk(), pulse.ComposeOptions{MaxWorkers: 2, FailFast: true})
		var pce *errors.CodedError
		if !stderrors.As(perr, &pce) || pce.Code != ce.Code || pce.Message != ce.Message || pce.Details["request"] != wantSlot {
			t.Errorf("%s: ComposeParallel %v (details %v), want %s located at request %d", name, perr, detailsOf(pce), ce.Code, wantSlot)
		}
	}
}

func detailsOf(ce *errors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}

func envCodes(env *descriptor.Envelope) []string {
	var out []string
	for _, e := range env.Errors {
		out = append(out, e.Code+" "+e.Message)
	}
	return out
}

// TestComposePredict_SlotBodyMatchesRuntime (#253, #400, #201a): a slot
// with an unknown aggregator, a grouped slot carrying an OVERLAY_FORMULA,
// and a slot crosstab cell that is unknown or decimal-unsupported are
// each refused by predict-compose with the runtime's code, message and
// slot location — they all used to predict valid.
func TestComposePredict_SlotBodyMatchesRuntime(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	axes := `"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}]`
	for _, c := range []struct {
		name, body string
		code       errors.Code
	}{
		{"unknown aggregation", `{"aggregations":[{"type":"AGG_NOPE","field":"x"}]}`, errors.PROCESSING_CONFIG},
		{"unknown group", `{"groups":[{"type":"GROUP_NOPE","field":"g"}],"aggregations":[{"type":"AGG_SUM","field":"x"}]}`, errors.PROCESSING_CONFIG},
		{"matrix overlay on grouped slot", `{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"overlays":[{"name":"o","kind":"OVERLAY_SHARE_OF_ROW","scope":"cell"}]}`, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"formula overlay on grouped slot", `{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"overlays":[{"name":"o","kind":"OVERLAY_FORMULA","scope":"group","params":{"formula":"cell"}}]}`, errors.PULSE_OVERLAY_FORMULA_INVALID_IDENT},
		{"crosstab cell unknown", `{"crosstab":{` + axes + `,"cell":{"type":"AGG_NOPE","field":"x"}}}`, errors.PROCESSING_CONFIG},
		{"crosstab margin unknown", `{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x"},"margins":{"rows":true},"margin_aggregations":[{"type":"AGG_NOPE","field":"x","label":"m"}]}}`, errors.PROCESSING_CONFIG},
		{"crosstab cell decimal", `{"crosstab":{` + axes + `,"cell":{"type":"AGG_MEDIAN","field":"t_decimal128"}}}`, errors.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL},
		{"categorical sum", `{"aggregations":[{"type":"AGG_SUM","field":"g"}]}`, errors.PULSE_FIELD_TYPE_UNSUPPORTED},
	} {
		body := c.body
		assertComposeParity(t, p, c.name, func() *types.ComposedRequest {
			return composeOneSlot(cohort, slotRequest(t, cohort, body))
		}, c.code, 1)

		// The same body at the top level: Process predict and Process
		// agree too (the crosstab cell half of #201a at the Request root).
		req := slotRequest(t, cohort, body)
		_, rerr := p.Process(context.Background(), req)
		var ce *errors.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != c.code {
			t.Errorf("%s: Process %v, want %s", c.name, rerr, c.code)
		}
		env := predictEnvelope(t, p, fs, cohort, slotRequest(t, cohort, body))
		if len(env.Errors) != 1 || env.Errors[0].Code != string(c.code) {
			t.Errorf("%s: Process predict %v, want one %s", c.name, envCodes(env), c.code)
		}
	}
}

// TestComposePredict_HiddenSlotOperator: an operator the instance's
// feature profile hides predicts inside a Compose slot exactly as a
// never-registered one, as the runtime refuses it.
func TestComposePredict_HiddenSlotOperator(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	fp, err := pulse.ParseFeatureProfile([]byte(`{"profile":"slot-hidden","written_with":"1.0.0","features":["capability:process","capability:compose","AGG_COUNT","AGG_SUM","GROUP_CATEGORY"]}`))
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: fp})
	if err != nil {
		t.Fatal(err)
	}
	assertComposeParity(t, p, "hidden AGG_AVERAGE", func() *types.ComposedRequest {
		return composeOneSlot(cohort, slotRequest(t, cohort, `{"aggregations":[{"type":"AGG_AVERAGE","field":"x"}]}`))
	}, errors.PROCESSING_CONFIG, 1)
}

// TestComposePredict_EmptyBatch (#299): an empty Compose — no slots, no
// sweep — is refused by predict with the runtime's SERVICE_VALIDATION
// and message.
func TestComposePredict_EmptyBatch(t *testing.T) {
	p, _, _ := acceptanceCohort(t)
	for _, body := range []string{`{}`, `{"requests":[]}`} {
		b := body
		assertComposeParity(t, p, "empty "+b, func() *types.ComposedRequest {
			var c types.ComposedRequest
			if err := json.Unmarshal([]byte(b), &c); err != nil {
				t.Fatal(err)
			}
			return &c
		}, errors.SERVICE_VALIDATION, -1)
	}
}

// TestComposePredict_SlotVerdictMatchesProcessPredict is the parity
// harness: on one shared request set, the Process-predict verdict of a
// request (valid, and its error codes in order) equals the verdict
// predict-compose gives the same request as a slot (its slot-located
// errors, details.request stripped). The next request-body rule added to
// Process predict without reaching Compose slots fails here.
func TestComposePredict_SlotVerdictMatchesProcessPredict(t *testing.T) {
	p, fs, cohort := acceptanceCohort(t)
	axes := `"rows":[{"type":"GROUP_CATEGORY","field":"g"}],"columns":[{"type":"GROUP_CATEGORY","field":"subj"}]`
	bodies := []string{
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}]}`,
		`{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_AVERAGE","field":"x","label":"m"}]}`,
		`{"aggregations":[{"type":"AGG_NOPE","field":"x"}]}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"g"}]}`,
		`{"aggregations":[{"type":"AGG_PERCENTILE","field":"x","params":{"percentile":250}}]}`,
		`{"aggregations":[{"type":"AGG_MEDIAN","field":"t_decimal128"}]}`,
		`{"filterers":[{"type":"FILTER_NOPE","field":"x"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
		`{"attributes":[{"type":"ATTR_NOPE","field":"x","label":"o"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
		`{"features":[{"type":"FEAT_NOPE","field":"x"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
		`{"features":[{"type":"FEAT_LOG","field":"g"}],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
		`{"windows":[{"type":"WIN_LAG","field":"x","params":{"offset":-1}}]}`,
		`{"tests":[{"type":"TEST_NOPE","fields":["x","y"]}]}`,
		`{"groups":[{"type":"GROUP_CATEGORY","field":"g"}],"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"overlays":[{"name":"o","kind":"OVERLAY_FORMULA","scope":"cell","params":{"formula":"cell"}}]}`,
		`{"aggregations":[{"type":"AGG_SUM","field":"x","label":"s"}],"overlays":[{"name":"o","kind":"OVERLAY_NOPE","scope":"cell"}]}`,
		`{"aggregations":[{"type":"AGG_COUNT","field":"x"}],"labels":[{"field":"g","table":"no_such_table"}]}`,
		`{"crosstab":{` + axes + `,"cell":{"type":"AGG_NOPE","field":"x"}}}`,
		`{"crosstab":{` + axes + `,"cell":{"type":"AGG_MEDIAN","field":"t_decimal128"}}}`,
		`{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x"}},"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`,
		`{"crosstab":{` + axes + `,"cell":{"type":"AGG_COUNT","field":"x"}}}`,
		`{"regressions":[{"type":"REG_NOPE","target":"y","predictors":["x"]}]}`,
	}
	ctx := context.Background()
	for _, body := range bodies {
		penv := predictEnvelope(t, p, fs, cohort, slotRequest(t, cohort, body))
		pres := penv.Data.(*descriptor.PredictResult)
		want := envCodes(penv)

		cenv, err := p.PredictCompose(ctx, &types.ComposedRequest{Requests: []*types.Request{slotRequest(t, cohort, body)}})
		if err != nil {
			t.Fatal(err)
		}
		cres := cenv.Data.(*pulse.ComposePredictResult)
		var got []string
		for _, e := range cenv.Errors {
			if e.Details["request"] != 0 {
				t.Errorf("%s: compose error %s %q not located at request 0 (%v)", body, e.Code, e.Message, e.Details)
			}
			got = append(got, e.Code+" "+e.Message)
		}
		if pres.Valid != cres.Valid || !reflect.DeepEqual(want, got) {
			t.Errorf("%s:\n Process predict valid=%v %v\n compose slot    valid=%v %v", body, pres.Valid, want, cres.Valid, got)
		}
	}
}
