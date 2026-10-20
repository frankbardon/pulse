package pulse_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Custom regressions are self-described like built-ins (U53 E1-S5): the
// manifest, the payload schema and the MCP enum list them; a feature
// profile hides one everywhere; their Purpose and Interpretation are
// validated; their target / predictor types, FieldInputs hook and
// p-value count follow the registration.

// regressionPurpose is a Purpose passing every rule.
func regressionPurpose() *descriptor.Purpose {
	return &descriptor.Purpose{
		Plain:     "A least-squares line through the target, fitted by an embedder model.",
		Intents:   []string{descx.IntentDrivers, descx.IntentRelationship},
		Questions: []string{"Which predictors move the score?", "How much does the score rise per unit of qty?"},
		UseCases:  map[descriptor.Domain]string{descriptor.DomainSurvey: "Which attributes drive satisfaction."},
		NotFor:    []descriptor.Alternative{{When: "you need the built-in fit", Use: string(types.REG_OLS)}},
		Level:     descriptor.LevelIntermediate,
	}
}

func describedRegressions() pulse.Extensions {
	regs := regressionParityRegistrations()
	regs[0].Purpose = regressionPurpose()
	regs[0].DependsOn = []string{string(types.AGG_COUNT)}
	return pulse.Extensions{Regressions: regs}
}

// processRegressionTypeEnum is the bound MCP process schema's
// regressions[].type enum for p's instance.
func processRegressionTypeEnum(t *testing.T, p *pulse.Pulse) []string {
	t.Helper()
	bound, err := mcp.BindForInstance(paritySchema(t), pulse.ServiceForTest(p).InstanceSnapshot())
	if err != nil {
		t.Fatalf("BindForInstance: %v", err)
	}
	var req struct {
		Properties struct {
			Regressions struct {
				Items struct {
					Properties struct {
						Type struct {
							Enum []string `json:"enum"`
						} `json:"type"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"regressions"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(bound[toolmeta.ToolProcess], &req); err != nil {
		t.Fatalf("decode bound process schema: %v", err)
	}
	return req.Properties.Regressions.Items.Properties.Type.Enum
}

// TestExtensions_RegressionSelfDescribed: a registered model is listed
// in the manifest's extensions.regressions (its Purpose's intents
// projected), in the payload schema's RegressionType enum and in the
// MCP process tool's regressions[].type enum, beside the built-ins; a
// feature profile that leaves it out removes it from all three and it
// acts as never registered at runtime and in predict.
func TestExtensions_RegressionSelfDescribed(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	open := func(t *testing.T, profile *pulse.FeatureProfile, ext pulse.Extensions) *pulse.Pulse {
		t.Helper()
		p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext, FeatureProfile: profile})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		return p
	}
	names := []string{string(regParityOLS), string(regParityOLSBuf)}

	p := open(t, nil, describedRegressions())
	var listed []string
	for _, m := range p.Manifest(ctx).Extensions.Regressions {
		listed = append(listed, m.Name)
		if m.Name == string(regParityOLS) && !slices.Equal(m.Intents, []string{descx.IntentDrivers, descx.IntentRelationship}) {
			t.Errorf("%s intents = %v, want its Purpose's", m.Name, m.Intents)
		}
	}
	if !slices.Equal(listed, names) {
		t.Errorf("manifest extensions.regressions = %v, want %v", listed, names)
	}
	schemaEnum := payloadEnumWith(t, p, string(types.REG_OLS))
	mcpEnum := processRegressionTypeEnum(t, p)
	for _, n := range names {
		if !slices.Contains(schemaEnum, n) {
			t.Errorf("payload-schema RegressionType enum %v lacks %s", schemaEnum, n)
		}
		if !slices.Contains(mcpEnum, n) {
			t.Errorf("MCP regressions[].type enum %v lacks %s", mcpEnum, n)
		}
	}
	if !slices.IsSorted(schemaEnum) || !slices.Contains(schemaEnum, string(types.REG_OLS)) {
		t.Errorf("payload-schema RegressionType enum = %v, want sorted with the built-ins", schemaEnum)
	}

	// The profile keeps REG_OLS and the buffered clone, leaving the
	// streaming one out.
	hidden := open(t, &pulse.FeatureProfile{Features: []string{"capability:process", string(types.REG_OLS), string(regParityOLSBuf)}},
		describedRegressions())
	never := open(t, nil, pulse.Extensions{})
	if got := hidden.Manifest(ctx).Extensions.Regressions; len(got) != 1 || got[0].Name != string(regParityOLSBuf) {
		t.Errorf("profiled manifest extensions.regressions = %+v, want only %s", got, regParityOLSBuf)
	}
	if slices.Contains(payloadEnumWith(t, hidden, string(types.REG_OLS)), string(regParityOLS)) {
		t.Error("profile-hidden model still in the payload-schema enum")
	}
	if enum := processRegressionTypeEnum(t, hidden); slices.Contains(enum, string(regParityOLS)) || !slices.Contains(enum, string(regParityOLSBuf)) {
		t.Errorf("profiled MCP enum = %v, want the buffered clone without the hidden one", enum)
	}
	// Every built-in regression hidden, the visible model keeps its
	// ontology node; the hidden one has none.
	only := open(t, &pulse.FeatureProfile{Features: []string{"capability:process", string(regParityOLSBuf)}}, describedRegressions())
	nodes := map[string]bool{}
	for _, n := range only.Ontology().Nodes {
		nodes[n.Name] = true
	}
	if !nodes[string(regParityOLSBuf)] || nodes[string(regParityOLS)] || nodes[string(types.REG_OLS)] {
		t.Errorf("ontology nodes: %s %v, %s %v, REG_OLS %v; want only the visible model", regParityOLSBuf, nodes[string(regParityOLSBuf)],
			regParityOLS, nodes[string(regParityOLS)], nodes[string(types.REG_OLS)])
	}
	req := &types.Request{Cohort: &types.Cohort{Filename: "e2e.pulse"}, Regressions: []*types.RegressionSpec{
		{Type: regParityOLS, Name: "fit", Target: "score", Predictors: []string{"qty"}}}}
	_, hiddenErr := hidden.Process(ctx, req)
	_, neverErr := never.Process(ctx, req)
	if hiddenErr == nil || neverErr == nil {
		t.Fatalf("Process accepted the model: hidden err %v, never-registered err %v", hiddenErr, neverErr)
	}
	if h, n := mustJSON(t, hiddenErr), mustJSON(t, neverErr); h != n || hiddenErr.Error() != neverErr.Error() {
		t.Errorf("hidden model's refusal differs from a never-registered one's:\n%v\n%v", hiddenErr, neverErr)
	}
	hp, err := hidden.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	np, err := never.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hp.Valid || mustJSON(t, hp.Errors) != mustJSON(t, np.Errors) {
		t.Errorf("hidden predict errors %+v, never-registered %+v", hp.Errors, np.Errors)
	}

	// DependsOn: enabling the model needs AGG_COUNT too; an unknown
	// dependency fails every pulse.New.
	_, err = pulse.New(pulse.Options{FS: fsys, Extensions: describedRegressions(),
		FeatureProfile: &pulse.FeatureProfile{Features: []string{"capability:process", string(regParityOLS)}}})
	requireCodedError(t, err, perr.PULSE_FEATURE_PROFILE_DEPENDENCY)
	open(t, &pulse.FeatureProfile{Features: []string{"capability:process", string(regParityOLS), string(types.AGG_COUNT)}},
		describedRegressions())
	bad := describedRegressions()
	bad.Regressions[0].DependsOn = []string{"AGG_ACME_NOPE"}
	_, err = pulse.New(pulse.Options{FS: fsys, Extensions: bad})
	requireCodedError(t, err, perr.PULSE_FEATURE_PROFILE_UNKNOWN)
}

// TestExtensions_RegressionGuidanceValidated: a malformed Purpose is
// PULSE_EXTENSION_PURPOSE_INVALID naming the regression; an
// Interpretation is held to the figures a model can report and the
// declared OutputSchema keys.
func TestExtensions_RegressionGuidanceValidated(t *testing.T) {
	with := func(mut func(*pulse.RegressionRegistration)) error {
		ext := describedRegressions()
		ext.Regressions[0].OutputSchema = []pulse.RegressionOutputMeta{{Key: "half_life", JSONType: "number"}}
		mut(&ext.Regressions[0])
		_, err := pulse.New(newTestOptions(ext))
		return err
	}
	interp := func(field string) []descriptor.Interpretation {
		return []descriptor.Interpretation{{Field: field, Means: "How fast the effect decays."}}
	}
	if err := with(func(r *pulse.RegressionRegistration) {
		r.Interpretation = append(interp("outputs.half_life"), descriptor.Interpretation{Field: "p_values.*", Shared: "p-value"})
	}); err != nil {
		t.Fatalf("a valid Interpretation was refused: %v", err)
	}
	for _, tc := range []struct {
		name, part string
		mut        func(*pulse.RegressionRegistration)
	}{
		{"purpose_plain_empty", "purpose", func(r *pulse.RegressionRegistration) { r.Purpose.Plain = "" }},
		{"purpose_unknown_alternative", "purpose", func(r *pulse.RegressionRegistration) { r.Purpose.NotFor[0].Use = "REG_ACME_MISSING" }},
		{"interpretation_undeclared_output", "interpretation", func(r *pulse.RegressionRegistration) { r.Interpretation = interp("outputs.decay") }},
		{"interpretation_unreported_figure", "interpretation", func(r *pulse.RegressionRegistration) { r.Interpretation = interp("ci_lower.*") }},
		{"interpretation_map_without_pattern", "interpretation", func(r *pulse.RegressionRegistration) { r.Interpretation = interp("coefficients") }},
		{"interpretation_no_means", "interpretation", func(r *pulse.RegressionRegistration) {
			r.Interpretation = []descriptor.Interpretation{{Field: "r2"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ce := requireCodedError(t, with(tc.mut), perr.PULSE_EXTENSION_PURPOSE_INVALID)
			if ce.Details["category"] != "regression" || ce.Details["name"] != string(regParityOLS) || ce.Details["part"] != tc.part {
				t.Errorf("details = %v, want regression %s part %s", ce.Details, regParityOLS, tc.part)
			}
		})
	}
}

// TestExtensions_RegressionAcceptsTypes: without Accepts a model's
// target and predictors follow the built-in regressions' rule (a
// categorical is refused); declared Accepts narrow it. Predict and the
// runtime raise the same PULSE_FIELD_TYPE_UNSUPPORTED, and the manifest
// lists the accepted types; a malformed Accepts is refused at pulse.New.
func TestExtensions_RegressionAcceptsTypes(t *testing.T) {
	ctx := context.Background()
	ext := describedRegressions()
	ext.Regressions[1].Accepts = []encoding.FieldType{encoding.FieldTypeF64, encoding.FieldTypeDate}
	p, cohort := openE2E(t, ext)
	for _, tc := range []struct {
		name       string
		typ        types.RegressionType
		target     string
		predictors []string
	}{
		{"default_rule_categorical_predictor", regParityOLS, "score", []string{"region"}},
		{"default_rule_categorical_target", regParityOLS, "region", []string{"qty"}},
		{"declared_accepts_u32_predictor", regParityOLSBuf, "score", []string{"qty"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, &types.RegressionSpec{Type: tc.typ, Name: "fit", Target: tc.target, Predictors: tc.predictors})
			_, err := p.Process(ctx, req)
			ce := requireCodedError(t, err, perr.PULSE_FIELD_TYPE_UNSUPPORTED)
			pr, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range pr.Errors {
				found = found || e.Code == string(ce.Code) && e.Message == ce.Message
			}
			if !found {
				t.Errorf("predict errors %+v lack the runtime's %q", pr.Errors, ce.Message)
			}
		})
	}
	// Accepted on both sides: a date predictor on the declared model,
	// a u32 one under the default rule.
	processE2E(t, p, regressionRequest(cohort, &types.RegressionSpec{Type: regParityOLSBuf, Name: "fit", Target: "score", Predictors: []string{"day"}}))
	processE2E(t, p, regressionRequest(cohort, &types.RegressionSpec{Type: regParityOLS, Name: "fit", Target: "score", Predictors: []string{"qty"}}))
	for _, m := range p.Manifest(ctx).Extensions.Regressions {
		switch m.Name {
		case string(regParityOLSBuf):
			if !slices.Equal(m.AcceptsTypes, []string{"date", "f64"}) {
				t.Errorf("%s accepts_types = %v, want [date f64]", m.Name, m.AcceptsTypes)
			}
		case string(regParityOLS):
			if slices.Contains(m.AcceptsTypes, "categorical_u8") || !slices.Contains(m.AcceptsTypes, "u32") {
				t.Errorf("%s accepts_types = %v, want the built-in regressions' set", m.Name, m.AcceptsTypes)
			}
		}
	}
	for _, accepts := range [][]encoding.FieldType{
		{encoding.FieldTypeF64, encoding.FieldTypeF64},
		{encoding.FieldTypeVecF32},
		{encoding.FieldType(250)},
	} {
		bad := describedRegressions()
		bad.Regressions[0].Accepts = accepts
		_, err := pulse.New(newTestOptions(bad))
		if ce := requireCodedError(t, err, perr.PULSE_EXTENSION_PARAM_INVALID); ce.Details["reason"] != "invalid_accepts" {
			t.Errorf("Accepts %v: details = %v, want reason invalid_accepts", accepts, ce.Details)
		}
	}
}

// TestExtensions_RegressionFieldInputs: a FieldInputs hook names the
// fields a model reads beyond its target and predictors — buffered
// projection decodes them (no hook: the decode widens), and a name
// the cohort lacks is the field-reference refusal at predict and at
// runtime alike.
func TestExtensions_RegressionFieldInputs(t *testing.T) {
	ctx := context.Background()
	ext := describedRegressions()
	ext.Regressions[1].Params = []pulse.ParamMeta{{Name: "offset", JSONType: "string", Description: "an offset field"}}
	ext.Regressions[1].FieldInputs = func(raw json.RawMessage) []string {
		var p struct {
			Offset string `json:"offset"`
		}
		_ = json.Unmarshal(raw, &p)
		return []string{p.Offset}
	}
	p, cohort := openE2E(t, ext)
	reg := pulse.ServiceForTest(p).Extensions()
	spec := func(typ types.RegressionType, offset string) *types.Request {
		return regressionRequest(cohort, &types.RegressionSpec{Type: typ, Name: "fit", Target: "score",
			Predictors: []string{"day"}, Params: json.RawMessage(`{"offset":"` + offset + `"}`)})
	}
	needed := processing.NeededFields(spec(regParityOLSBuf, "amount"), paritySchema(t), reg)
	if needed.IsWide() || !needed.Has("amount") || !needed.Has("score") || !needed.Has("day") || needed.Has("region") {
		t.Errorf("hooked model's needed fields = %v, want target, predictor and the declared amount", needed)
	}
	opaque := regressionRequest(cohort, &types.RegressionSpec{Type: regParityOLS, Name: "fit", Target: "score", Predictors: []string{"day"}})
	if !processing.NeededFields(opaque, paritySchema(t), reg).IsWide() {
		t.Error("a model without a FieldInputs hook did not widen the decode")
	}
	processE2E(t, p, spec(regParityOLSBuf, "amount"))
	req := spec(regParityOLSBuf, "nope")
	_, err := p.Process(ctx, req)
	if err == nil {
		t.Fatal("Process accepted a FieldInputs name the cohort lacks")
	}
	ce := requireCodedError(t, err, perr.SERVICE_VALIDATION)
	pr, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range pr.Errors {
		found = found || e.Code == string(ce.Code) && e.Message == ce.Message
	}
	if !found {
		t.Errorf("predict errors %+v lack the runtime's %q", pr.Errors, ce.Message)
	}
}

// TestExtensions_RegressionPValueCount: predict counts a model's
// p-values off the request — one per non-intercept predictor on the
// lower_bound basis (the model names its own coefficients), or its
// explicit multiplicity list exactly — corrected when it is a member.
func TestExtensions_RegressionPValueCount(t *testing.T) {
	ctx := context.Background()
	p, cohort := openE2E(t, describedRegressions())
	for _, tc := range []struct {
		name               string
		mult               *types.RegressionMultiplicity
		total, uncorrected int
		basis              string
	}{
		{"uncorrected", nil, 2, 2, descriptor.PValueBasisLowerBound},
		{"member", &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}, 2, 0, descriptor.PValueBasisLowerBound},
		{"explicit_list", &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm, Coefficients: []string{"qty", "(intercept)", "rho"}}, 3, 0, descriptor.PValueBasisExact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, &types.RegressionSpec{Type: regParityOLS, Name: "fit", Target: "score",
				Predictors: []string{"qty", "day"}, Multiplicity: tc.mult})
			pr, err := p.Predict(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if !pr.Valid || pr.PValues == nil {
				t.Fatalf("predict: valid %v p_values %+v errors %+v", pr.Valid, pr.PValues, pr.Errors)
			}
			if pr.PValues.Total != tc.total || pr.PValues.Uncorrected != tc.uncorrected || pr.PValues.Basis != tc.basis {
				t.Errorf("p_values = %+v, want total %d uncorrected %d basis %s", *pr.PValues, tc.total, tc.uncorrected, tc.basis)
			}
		})
	}
}
