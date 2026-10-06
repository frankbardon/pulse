package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/types"
)

// TestResolveComposeReturn: the Compose-level block roots at
// ComposedResponse, admits only `overlays…` paths and always keeps
// `responses` whole — under an include allowlist, a preset and an
// exclude alike — while the overlays follow the block.
func TestResolveComposeReturn(t *testing.T) {
	responses := []returnplan.Segment{returnplan.Key("responses")}
	summary := []returnplan.Segment{returnplan.Key("overlays"), returnplan.Elem(), returnplan.Key("summary")}
	payload := []returnplan.Segment{returnplan.Key("overlays"), returnplan.Elem(), returnplan.Key("payload")}

	if plan, err := ResolveComposeReturn(&types.ComposedRequest{}, nil); plan != nil || err != nil {
		t.Fatalf("no block: plan %v err %v", plan, err)
	}
	for name, c := range map[string]struct {
		ret            *types.Return
		summary, paylo bool
	}{
		"include allowlist": {&types.Return{Include: []string{"overlays[*].summary"}}, true, false},
		"preset minimal":    {&types.Return{Preset: types.ReturnPresetMinimal}, true, false},
		"exclude":           {&types.Return{Exclude: []string{"overlays[*].payload"}}, true, false},
	} {
		plan, err := ResolveComposeReturn(&types.ComposedRequest{Return: c.ret}, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if vd := plan.Visit(responses); !vd.Keep || !vd.Whole {
			t.Errorf("%s: responses not kept whole: %+v", name, vd)
		}
		if plan.Visit(summary).Keep != c.summary || plan.Visit(payload).Keep != c.paylo {
			t.Errorf("%s: summary %v payload %v", name, plan.Visit(summary), plan.Visit(payload))
		}
	}
	for name, c := range map[string]struct {
		ret  *types.Return
		code errors.Code
	}{
		"responses path":       {&types.Return{Exclude: []string{"responses"}}, errors.PULSE_RETURN_INVALID},
		"slot path":            {&types.Return{Include: []string{"responses[*].data"}}, errors.PULSE_RETURN_INVALID},
		"response-rooted path": {&types.Return{Include: []string{"data"}}, errors.PULSE_RETURN_INVALID},
		"returned marker":      {&types.Return{Exclude: []string{"returned"}}, errors.PULSE_RETURN_INVALID},
		"unknown overlay key":  {&types.Return{Include: []string{"overlays[*].nope"}}, errors.PULSE_RETURN_PATH_UNKNOWN},
	} {
		_, err := ResolveComposeReturn(&types.ComposedRequest{Return: c.ret}, nil)
		if got := returnCode(err); got != c.code {
			t.Errorf("%s: code %s, want %s (%v)", name, got, c.code, err)
		}
	}
}
