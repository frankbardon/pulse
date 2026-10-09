package descriptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// facetChiSqSpecs is n OVERLAY_CHISQ_VS_POP specs over the facet
// fixture's categorical field — one inferential p-value each.
func facetChiSqSpecs(n int) []types.OverlaySpec {
	out := make([]types.OverlaySpec, n)
	for i := range out {
		out[i] = types.OverlaySpec{
			Name:   "chi_" + strconv.Itoa(i),
			Kind:   types.OverlayKindChiSqVsPop,
			Scope:  types.OverlayScopeGroup,
			Ref:    types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "pop.pulse"}},
			Params: json.RawMessage(`{"field":"category"}`),
		}
	}
	return out
}

func facetResult(t *testing.T, req *types.FacetRequest, opts *PredictOptions) (*descriptor.Envelope, *FacetValidationResult) {
	t.Helper()
	env := ValidateFacetWithOptions(bytes.NewReader(buildFacetOverlayPulseBytes(t)), req, opts)
	return env, env.Data.(*FacetValidationResult)
}

func advisoryCodesOf(as []descriptor.Advisory) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Code)
	}
	return out
}

// TestFacetPredict_AdvisoryManyTests: Facet predict counts one p-value
// per inferential facet overlay and raises PULSE_ADVISORY_MANY_TESTS at
// the threshold, exactly as Request predict does; under it, with a
// multiplicity block named, or with capability:multiplicity hidden it
// stays silent.
func TestFacetPredict_AdvisoryManyTests(t *testing.T) {
	th := descriptor.MultiplicityTriggerThreshold
	req := func(n int) *types.FacetRequest {
		return &types.FacetRequest{Cohort: &types.Cohort{Filename: "x.pulse"}, Fields: []string{"category"}, Overlays: facetChiSqSpecs(n)}
	}
	env, res := facetResult(t, req(th), nil)
	if len(env.Errors) != 0 {
		t.Fatalf("fixture invalid: %+v", env.Errors)
	}
	want := &descriptor.PValueCount{Total: th, Uncorrected: th, Basis: descriptor.PValueBasisExact, Threshold: th}
	if !reflect.DeepEqual(res.PValues, want) {
		t.Errorf("p_values = %+v, want %+v", res.PValues, want)
	}
	if got := advisoryCodesOf(res.Advisories); !reflect.DeepEqual(got, []string{string(errors.PULSE_ADVISORY_MANY_TESTS)}) {
		t.Fatalf("advisories = %+v, want one MANY_TESTS", res.Advisories)
	}
	if msg := res.Advisories[0].Message; !strings.HasPrefix(msg, "The facet request emits "+strconv.Itoa(th)+" ") {
		t.Errorf("message %q does not state the facet count", msg)
	}
	if hits := LintGuidanceText(res.Advisories[0].Message); len(hits) > 0 {
		t.Errorf("lint hits %+v", hits)
	}

	if _, res := facetResult(t, req(th-1), nil); res.PValues == nil || len(res.Advisories) != 0 {
		t.Errorf("under threshold: p_values %+v advisories %+v, want a count and no advisory", res.PValues, res.Advisories)
	}
	named := req(th)
	named.Overlays[0].Multiplicity = &types.Multiplicity{Method: "holm"}
	if _, res := facetResult(t, named, nil); len(res.Advisories) != 0 || res.PValues.Uncorrected != th-1 {
		t.Errorf("named block: p_values %+v advisories %+v, want uncorrected %d and no advisory", res.PValues, res.Advisories, th-1)
	}
	if _, res := facetResult(t, req(th), &PredictOptions{Instance: hideOnly(featMultiplicity)}); res.PValues != nil || len(res.Advisories) != 0 {
		t.Errorf("multiplicity hidden: p_values %+v advisories %+v, want neither", res.PValues, res.Advisories)
	}
	if _, res := facetResult(t, req(th), &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories(
		[]string{string(errors.PULSE_ADVISORY_MANY_TESTS)})}); len(res.Advisories) != 0 || res.PValues == nil {
		t.Errorf("suppressed: advisories %+v, want none (p_values kept)", res.Advisories)
	}
}

// TestFacetPredict_AdvisoryFreeOutputOmitsTheKeys: a facet request with
// no inferential overlay carries neither key.
func TestFacetPredict_AdvisoryFreeOutputOmitsTheKeys(t *testing.T) {
	env, _ := facetResult(t, &types.FacetRequest{Cohort: &types.Cohort{Filename: "x.pulse"}, Fields: []string{"category"}}, nil)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, `"advisories"`) || strings.Contains(s, `"p_values"`) {
		t.Errorf("advisory-free facet predict carries a new key: %s", s)
	}
}

// composeSchemaLoader serves the header + schema of the named fixture
// bytes, as the facade's runtime opener would.
func composeSchemaLoader(t *testing.T, cohorts map[string][]byte) func(string) (*encoding.Schema, error) {
	t.Helper()
	return func(path string) (*encoding.Schema, error) {
		r := bytes.NewReader(cohorts[path])
		v, err := encoding.ReadHeader(r)
		if err != nil {
			return nil, err
		}
		return encoding.ReadSchema(r, v)
	}
}

func composeAdvisoryOpts(t *testing.T) *PredictOptions {
	return &PredictOptions{
		SchemaLoader: composeSchemaLoader(t, map[string][]byte{"a.pulse": advisoryCohort(t), "s.pulse": sidecarCohort(t)}),
		SidecarLoader: func(path string) (string, map[string]string) {
			if path == "s.pulse" {
				return "w", sidecarMeasures
			}
			return "", nil
		},
	}
}

func composeResult(t *testing.T, req *types.ComposedRequest, opts *PredictOptions) *ComposeValidationResult {
	t.Helper()
	return ValidateComposeWithOptions(req, opts).Data.(*ComposeValidationResult)
}

// TestComposePredict_SlotAdvisoriesAttributed: every slot's Request
// advisories reach Compose predict in slot order, each attributed to
// its slot (details.request, a "requests[i]." slot path, the slot named
// in the many-tests message) — the sidecar rules read each slot's own
// cohort sidecar.
func TestComposePredict_SlotAdvisoriesAttributed(t *testing.T) {
	th := descriptor.MultiplicityTriggerThreshold
	req := &types.ComposedRequest{Requests: []*types.Request{
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Tests: oneSampleTests(th)},
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g"}}},
		{Cohort: &types.Cohort{Filename: "s.pulse"}, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "q"}}},
	}}
	res := composeResult(t, req, composeAdvisoryOpts(t))
	want := []struct {
		code string
		req  int
		slot any
	}{
		{string(errors.PULSE_ADVISORY_MANY_TESTS), 0, nil},
		{string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS), 1, "requests[1].tests[0]"},
		{string(errors.PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC), 2, "requests[2].aggregations[0]"},
		{string(errors.PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED), 2, nil},
	}
	if len(res.Advisories) != len(want) {
		t.Fatalf("advisories = %+v, want %d", res.Advisories, len(want))
	}
	for i, w := range want {
		a := res.Advisories[i]
		if a.Code != w.code || a.Details["request"] != w.req || a.Details["slot"] != w.slot {
			t.Errorf("advisory %d = %+v, want %s on request %d at %v", i, a, w.code, w.req, w.slot)
		}
		if s, ok := w.slot.(string); ok && !strings.Contains(a.Message, s) {
			t.Errorf("advisory %d message %q does not name %s", i, a.Message, s)
		}
		if hits := LintGuidanceText(a.Message); len(hits) > 0 {
			t.Errorf("advisory %d lint hits %+v", i, hits)
		}
	}
	if !strings.HasPrefix(res.Advisories[0].Message, "The request at requests[0] emits ") {
		t.Errorf("many-tests message %q does not name its slot", res.Advisories[0].Message)
	}
	if res.PValues != nil {
		t.Errorf("p_values = %+v with no Compose-host overlay, want none", res.PValues)
	}

	// A request that fires on Request predict fires the same code on its
	// Compose slot.
	direct := predictAdvisories(t, &types.Request{Tests: []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "g"}}}, nil)
	if len(direct) != 1 || direct[0].Code != res.Advisories[1].Code || direct[0].Details["groups"] != res.Advisories[1].Details["groups"] {
		t.Errorf("Request predict %+v and its Compose slot %+v disagree", direct, res.Advisories[1])
	}

	// Without a sidecar loader the sidecar rules never fire.
	opts := composeAdvisoryOpts(t)
	opts.SidecarLoader = nil
	if got := advisoryCodesOf(composeResult(t, req, opts).Advisories); len(got) != 2 {
		t.Errorf("no sidecar loader: advisories %v, want the two schema-only codes", got)
	}
	// Suppression applies on the Compose root.
	opts = composeAdvisoryOpts(t)
	opts.Instance = (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS)})
	if got := advisoryCodesOf(composeResult(t, req, opts).Advisories); slices.Contains(got, string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS)) || len(got) != 3 {
		t.Errorf("suppressed: advisories %v", got)
	}
}

// TestComposePredict_HostOverlayManyTests: inferential Compose-host
// overlays count one p-value each on a lower_bound basis (their extent
// is cohort-free) and raise PULSE_ADVISORY_MANY_TESTS — "at least N" —
// at the threshold, with no slot attribution; a Compose-level block or
// a hidden capability:multiplicity silences it.
func TestComposePredict_HostOverlayManyTests(t *testing.T) {
	th := descriptor.MultiplicityTriggerThreshold
	req := func() *types.ComposedRequest {
		r := &types.ComposedRequest{Requests: []*types.Request{
			seriesGroupedSumRequest("a", "x", "tag"),
			seriesGroupedSumRequest("b", "x", "tag"),
		}}
		for i := 0; i < th; i++ {
			r.Overlays = append(r.Overlays, types.ComposeOverlaySpec{
				Name: "z_" + strconv.Itoa(i), Kind: types.OverlayKindZVsRef, Reference: "a", Targets: []string{"b"},
			})
		}
		// One descriptive overlay, which carries no p-value.
		r.Overlays = append(r.Overlays, types.ComposeOverlaySpec{Name: "delta", Kind: types.OverlayKindDeltaVsRef, Reference: "a", Targets: []string{"b"}})
		return r
	}
	res := composeResult(t, req(), nil)
	want := &descriptor.PValueCount{Total: th, Uncorrected: th, Basis: descriptor.PValueBasisLowerBound, Threshold: th}
	if !reflect.DeepEqual(res.PValues, want) {
		t.Errorf("p_values = %+v, want %+v", res.PValues, want)
	}
	if len(res.Advisories) != 1 || res.Advisories[0].Code != string(errors.PULSE_ADVISORY_MANY_TESTS) {
		t.Fatalf("advisories = %+v, want one host MANY_TESTS", res.Advisories)
	}
	a := res.Advisories[0]
	if _, ok := a.Details["request"]; ok || a.Details["basis"] != descriptor.PValueBasisLowerBound ||
		!strings.HasPrefix(a.Message, "The compose request's overlays emit at least "+strconv.Itoa(th)+" ") {
		t.Errorf("host advisory = %+v, want an unattributed lower_bound \"at least\" note", a)
	}
	if hits := LintGuidanceText(a.Message); len(hits) > 0 {
		t.Errorf("lint hits %+v", hits)
	}

	named := req()
	named.Multiplicity = &types.Multiplicity{Method: "holm"}
	if res := composeResult(t, named, nil); len(res.Advisories) != 0 || res.PValues == nil || res.PValues.Uncorrected != 0 {
		t.Errorf("compose block: p_values %+v advisories %+v, want all corrected and silent", res.PValues, res.Advisories)
	}
	if res := composeResult(t, req(), &PredictOptions{Instance: hideOnly(featMultiplicity)}); res.PValues != nil || len(res.Advisories) != 0 {
		t.Errorf("multiplicity hidden: p_values %+v advisories %+v, want neither", res.PValues, res.Advisories)
	}
	if res := composeResult(t, req(), &PredictOptions{Instance: (*InstanceSnapshot)(nil).WithSuppressedAdvisories(
		[]string{string(errors.PULSE_ADVISORY_MANY_TESTS)})}); len(res.Advisories) != 0 {
		t.Errorf("suppressed: advisories %+v, want none", res.Advisories)
	}
}

// TestChainValidationResult_CarriesNoAdvisories: Chain predict is
// untouched by the advisory roots — its result keeps exactly its prior
// keys and a stage that fires on Request predict raises nothing there.
func TestChainValidationResult_CarriesNoAdvisories(t *testing.T) {
	var keys []string
	rt := reflect.TypeFor[ChainValidationResult]()
	for i := 0; i < rt.NumField(); i++ {
		keys = append(keys, strings.Split(rt.Field(i).Tag.Get("json"), ",")[0])
	}
	if want := []string{"valid", "request", "schema_info", "stage_schemas", "overlays_schema_divergence"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("ChainValidationResult keys = %v, want %v", keys, want)
	}
	env := ValidateChain(bytes.NewReader(advisoryCohort(t)), &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "a.pulse"},
		Stages: []*types.ChainStage{{Request: &types.Request{Tests: append(oneSampleTests(descriptor.MultiplicityTriggerThreshold),
			&types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"})}}},
	})
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); strings.Contains(s, `"advisories"`) || strings.Contains(s, `"p_values"`) {
		t.Errorf("chain predict carries an advisory key: %s", s)
	}
}
