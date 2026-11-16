package mcp

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// postFitStubExt registers one post-fit operator that never runs.
var postFitStubExt = pulse.Extensions{PostFits: []pulse.PostFitRegistration{{
	Name:        "POSTFIT_ACME_ROI",
	Description: "Stub.",
	Outputs:     extend.PostFitSchema{Scalars: []string{"roi"}},
	Run:         func(extend.PostFitInput) (extend.PostFitOutput, error) { return extend.PostFitOutput{}, nil },
}}}

// composePostFit is the bound compose schema's `post_fit` property (nil
// when absent).
func composePostFit(t *testing.T, inst *descx.InstanceSnapshot) map[string]any {
	t.Helper()
	raw, err := buildComposeSchemaWithExtensions(fieldClassification{}, inst)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	pf, _ := doc["properties"].(map[string]any)["post_fit"].(map[string]any)
	return pf
}

func postFitTypeEnumOf(pf map[string]any) []string {
	typ := pf["items"].(map[string]any)["properties"].(map[string]any)["type"].(map[string]any)
	var out []string
	for _, v := range typ["enum"].([]any) {
		out = append(out, v.(string))
	}
	return out
}

// TestPostFit_MCPSchemaTypeEnum: the bound compose schema's post-fit
// `type` enum is the instance's registered operators — empty without
// any, the operator once registered, empty again when a feature profile
// omits it — and the whole `post_fit` key is dropped with
// capability:compose_post_fit.
func TestPostFit_MCPSchemaTypeEnum(t *testing.T) {
	t.Parallel()

	_, bare := instanceFor(t, nil, pulse.Extensions{})
	if got := postFitTypeEnumOf(composePostFit(t, bare)); len(got) != 0 {
		t.Fatalf("a post-fit-free instance offers %v", got)
	}
	_, registered := instanceFor(t, nil, postFitStubExt)
	if got := postFitTypeEnumOf(composePostFit(t, registered)); !slices.Equal(got, []string{"POSTFIT_ACME_ROI"}) {
		t.Fatalf("registered instance offers %v", got)
	}
	slot := []string{"capability:compose", "capability:compose_fits", "REG_OLS", "AGG_SUM", descx.FeatureComposePostFit}
	_, omitted := instanceFor(t, &pulse.FeatureProfile{Features: slot}, postFitStubExt)
	if got := postFitTypeEnumOf(composePostFit(t, omitted)); len(got) != 0 {
		t.Fatalf("a profile omitting the operator offers %v", got)
	}
	_, enabled := instanceFor(t, &pulse.FeatureProfile{Features: append(slices.Clone(slot), "POSTFIT_ACME_ROI")}, postFitStubExt)
	if got := postFitTypeEnumOf(composePostFit(t, enabled)); !slices.Equal(got, []string{"POSTFIT_ACME_ROI"}) {
		t.Fatalf("a profile enabling the operator offers %v", got)
	}
	_, hidden := instanceFor(t, &pulse.FeatureProfile{Features: slot[:4]}, pulse.Extensions{})
	if pf := composePostFit(t, hidden); pf != nil {
		t.Fatalf("an instance hiding the capability describes post_fit: %v", pf)
	}
}

// TestPostFit_MCPToolsForwardPostFit: pulse_compose and pulse_predict
// {composed} forward a `post_fit` entry to the facade's rules — an
// unknown type is PULSE_POSTFIT_INVALID on both, never stripped.
func TestPostFit_MCPToolsForwardPostFit(t *testing.T) {
	t.Parallel()

	p, _ := instanceFor(t, nil, postFitStubExt)
	args := `{"requests":[{"cohort":{"filename":"imports/data.pulse"},"aggregations":[{"type":"AGG_SUM","field":"amount","label":"v"}]}],
		"post_fit":[{"label":"roi","type":"POSTFIT_ACME_NOPE"}]}`
	_, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolCompose, args)
	if ce := codedOf(t, err); ce.Code != perr.PULSE_POSTFIT_INVALID || ce.Details["reason"] != "type_unknown" {
		t.Fatalf("compose %s %v", ce.Code, ce.Details)
	}
	out, err := invokeSweepTool(t, p, Config{}, toolmeta.ToolPredict, `{"composed":`+args+`}`)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 || errs[0].(map[string]any)["code"] != string(perr.PULSE_POSTFIT_INVALID) {
		t.Fatalf("predict errors = %v", errs)
	}
}
