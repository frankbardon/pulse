package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func mult(method types.MultiplicityMethod, family types.MultiplicityFamily, alpha float64) *types.Multiplicity {
	return &types.Multiplicity{Method: method, Family: family, Alpha: alpha}
}

const (
	mNone = types.MultiplicityMethodNone
	mHolm = types.MultiplicityMethodHolm
	mBH   = types.MultiplicityMethodBH
	mBonf = types.MultiplicityMethodBonferroni

	fLayer   = types.MultiplicityFamilyLayer
	fRow     = types.MultiplicityFamilyRow
	fColumn  = types.MultiplicityFamilyColumn
	fRequest = types.MultiplicityFamilyRequest
	fCompose = types.MultiplicityFamilyCompose
)

// matrixKind is an inferential MATRIX-payload overlay kind; seriesKind
// an inferential SERIES one; shareKind a descriptive MATRIX one.
const (
	matrixKind = types.OverlayKindPairwisePropZ
	seriesKind = types.OverlayKindChiSqRow
	shareKind  = types.OverlayKindShareOfRow
)

func ttest(m *types.Multiplicity) *types.Test {
	return &types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g", Multiplicity: m}
}

func tukey(m *types.Multiplicity) *types.Test {
	return &types.Test{Type: types.TEST_TUKEY_HSD, Field: "x", Multiplicity: m}
}

func ov(kind types.OverlayKind, m *types.Multiplicity) types.OverlaySpec {
	return types.OverlaySpec{Kind: kind, Multiplicity: m}
}

// wantCoded asserts err is a coded error with code and the details
// subset want.
func wantCoded(t *testing.T, err error, code errors.Code, want map[string]any) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s, got nil", code)
	}
	ce := asCoded(t, err)
	if ce.Code != code {
		t.Fatalf("code = %s, want %s (%s)", ce.Code, code, ce.Message)
	}
	for k, v := range want {
		if !reflect.DeepEqual(ce.Details[k], v) {
			t.Errorf("details[%q] = %#v, want %#v (all: %v)", k, ce.Details[k], v, ce.Details)
		}
	}
}

func TestValidateMultiplicitySpec(t *testing.T) {
	cases := []struct {
		name string
		m    *types.Multiplicity
		key  string // "" = valid
	}{
		{"nil", nil, ""},
		{"empty block", &types.Multiplicity{}, ""},
		{"every method", mult(types.MultiplicityMethodBY, fCompose, 0.1), ""},
		{"unknown method", mult("sidak", "", 0), "method"},
		{"case-sensitive method", mult("BH", "", 0), "method"},
		{"unknown family", mult(mHolm, "matrix", 0), "family"},
		{"alpha zero means unset", mult(mHolm, "", 0), ""},
		{"alpha one", mult(mHolm, "", 1), "alpha"},
		{"alpha negative", mult(mHolm, "", -0.05), "alpha"},
		{"alpha above one", mult(mHolm, "", 1.5), "alpha"},
		{"alpha inside", mult(mHolm, "", 0.999), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateMultiplicitySpec(c.m, "Options.DefaultMultiplicity")
			if c.key == "" {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
				"slot": "Options.DefaultMultiplicity", "key": c.key,
			})
		})
	}
}

// TestResolveMultiplicity_Precedence pins slot → request → options →
// none per field, the surface default families, and the fall-back of
// an inherited family a surface does not offer.
func TestResolveMultiplicity_Precedence(t *testing.T) {
	type want struct {
		method types.MultiplicityMethod
		family types.MultiplicityFamily
		alpha  float64
		member bool
	}
	cases := []struct {
		name  string
		req   *types.Request
		def   *types.Multiplicity
		tests []want // Tests then PostTests
		ovs   []want
	}{
		{
			name:  "request method, surface default families",
			req:   &types.Request{Multiplicity: mult(mHolm, "", 0), Tests: []*types.Test{ttest(nil)}, Overlays: []types.OverlaySpec{ov(matrixKind, nil)}},
			tests: []want{{mHolm, fRequest, 0, true}},
			ovs:   []want{{mHolm, fLayer, 0.05, true}},
		},
		{
			name:  "options only",
			req:   &types.Request{Tests: []*types.Test{ttest(nil)}, Overlays: []types.OverlaySpec{ov(matrixKind, nil)}},
			def:   mult(mBH, fRow, 0.1),
			tests: []want{{mBH, fRequest, 0, true}}, // row is not a test family: falls back
			ovs:   []want{{mBH, fRow, 0.1, true}},
		},
		{
			name: "fields fall through independently",
			req: &types.Request{
				Multiplicity: mult("", fRequest, 0),
				Overlays:     []types.OverlaySpec{ov(matrixKind, mult("", "", 0.01))},
			},
			def: mult(mBonf, fColumn, 0.2),
			ovs: []want{{mBonf, fRequest, 0.01, true}},
		},
		{
			name:  "slot beats request beats options",
			req:   &types.Request{Multiplicity: mult(mBH, "", 0.1), Overlays: []types.OverlaySpec{ov(matrixKind, mult(mHolm, fColumn, 0.02))}},
			def:   mult(mBonf, fRow, 0.3),
			ovs:   []want{{mHolm, fColumn, 0.02, true}},
			tests: nil,
		},
		{
			name: "inherited row on a non-MATRIX kind falls back to layer",
			req:  &types.Request{Multiplicity: mult(mHolm, fRow, 0), Overlays: []types.OverlaySpec{ov(seriesKind, nil), ov(matrixKind, nil)}},
			ovs:  []want{{mHolm, fLayer, 0.05, true}, {mHolm, fRow, 0.05, true}},
		},
		{
			name:  "inherited compose outside Compose falls back",
			req:   &types.Request{Tests: []*types.Test{ttest(nil)}, Overlays: []types.OverlaySpec{ov(matrixKind, nil)}},
			def:   mult(mHolm, fCompose, 0),
			tests: []want{{mHolm, fRequest, 0, true}},
			ovs:   []want{{mHolm, fLayer, 0.05, true}},
		},
		{
			name:  "slot opt-out",
			req:   &types.Request{Multiplicity: mult(mHolm, "", 0), Tests: []*types.Test{ttest(mult(mNone, "", 0))}},
			tests: []want{{mNone, fRequest, 0, false}},
		},
		{
			name: "descriptive kind resolves but joins no family",
			req:  &types.Request{Multiplicity: mult(mHolm, "", 0), Overlays: []types.OverlaySpec{ov(shareKind, mult("", fRow, 0))}},
			ovs:  []want{{mHolm, fRow, 0.05, false}},
		},
		{
			name: "unknown kind joins no family and is not judged",
			req:  &types.Request{Multiplicity: mult(mHolm, fRow, 0), Overlays: []types.OverlaySpec{ov("OVERLAY_NOT_A_KIND", nil)}},
			ovs:  []want{{mHolm, fLayer, 0.05, false}},
		},
		{
			name:  "inherited method skips the Tukey HSD post-test",
			req:   &types.Request{Multiplicity: mult(mHolm, "", 0), Tests: []*types.Test{ttest(nil)}, PostTests: []*types.Test{tukey(nil), tukey(mult(mNone, fRequest, 0))}},
			tests: []want{{mHolm, fRequest, 0, true}, {mNone, fRequest, 0, false}, {mNone, fRequest, 0, false}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			plan, err := ResolveMultiplicity(c.req, c.def, nil)
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			got := append(append([]ResolvedMultiplicity{}, plan.Tests...), plan.PostTests...)
			if len(got) != len(c.tests) {
				t.Fatalf("tests resolved %d, want %d", len(got), len(c.tests))
			}
			for i, w := range c.tests {
				g := got[i]
				if g.Method != w.method || g.Family != w.family || g.Alpha != w.alpha || g.Member != w.member {
					t.Errorf("test %d = %+v, want %+v", i, g, w)
				}
			}
			if len(plan.Overlays) != len(c.ovs) {
				t.Fatalf("overlays resolved %d, want %d", len(plan.Overlays), len(c.ovs))
			}
			for i, w := range c.ovs {
				g := plan.Overlays[i]
				if g.Method != w.method || g.Family != w.family || g.Alpha != w.alpha || g.Member != w.member {
					t.Errorf("overlay %d = %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

// TestResolveMultiplicity_NilWhenAbsent: nothing named ⇒ no plan, the
// fast path that keeps absent configuration byte-identical.
func TestResolveMultiplicity_NilWhenAbsent(t *testing.T) {
	req := &types.Request{Tests: []*types.Test{ttest(nil)}, Overlays: []types.OverlaySpec{ov(matrixKind, nil)}}
	if plan, err := ResolveMultiplicity(req, nil, nil); err != nil || plan != nil {
		t.Fatalf("plan = %+v, err = %v; want nil, nil", plan, err)
	}
	if plan, err := ResolveMultiplicity(nil, mult(mHolm, "", 0), nil); err != nil || plan != nil {
		t.Fatalf("nil request: plan = %+v, err = %v", plan, err)
	}
	plan, err := ResolveMultiplicity(req, mult(mNone, "", 0), nil)
	if err != nil || plan == nil || plan.Active() {
		t.Fatalf("method none: plan = %+v active, err = %v; want an inactive plan", plan, err)
	}
	plan, _ = ResolveMultiplicity(req, mult(mHolm, "", 0), nil)
	if !plan.Active() {
		t.Fatal("a holm default left the plan inactive")
	}
}

// TestResolveMultiplicity_Refusals covers every PULSE_MULTIPLICITY_INVALID
// case of a standalone Request.
func TestResolveMultiplicity_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		req    *types.Request
		def    *types.Multiplicity
		slot   string
		key    string
		reason string
	}{
		{"unknown method on a test", &types.Request{Tests: []*types.Test{ttest(mult("sidak", "", 0))}}, nil, "tests[0].multiplicity", "method", multReasonUnknownMethod},
		{"unknown family on the request", &types.Request{Multiplicity: mult("", "matrix", 0)}, nil, "multiplicity", "family", multReasonUnknownFamily},
		{"alpha out of range on an overlay", &types.Request{Overlays: []types.OverlaySpec{ov(matrixKind, mult("", "", 1))}}, nil, "overlays[0].multiplicity", "alpha", multReasonAlphaRange},
		{"bad instance default", &types.Request{Tests: []*types.Test{ttest(nil)}}, mult("", "", -1), "Options.DefaultMultiplicity", "alpha", multReasonAlphaRange},
		{"alpha on a test", &types.Request{PostTests: []*types.Test{ttest(mult("", "", 0.01))}}, nil, "post_tests[0].multiplicity", "alpha", multReasonTestAlpha},
		{"layer on a test", &types.Request{Tests: []*types.Test{ttest(mult("", fLayer, 0))}}, nil, "tests[0].multiplicity", "family", multReasonSurface},
		{"row on a test", &types.Request{Tests: []*types.Test{ttest(mult("", fRow, 0))}}, nil, "tests[0].multiplicity", "family", multReasonSurface},
		{"compose on a test outside Compose", &types.Request{Tests: []*types.Test{ttest(mult("", fCompose, 0))}}, nil, "tests[0].multiplicity", "family", multReasonComposeOutside},
		{"compose on the request outside Compose", &types.Request{Multiplicity: mult(mHolm, fCompose, 0)}, nil, "multiplicity", "family", multReasonComposeOutside},
		{"compose on an overlay outside Compose", &types.Request{Overlays: []types.OverlaySpec{ov(matrixKind, mult("", fCompose, 0))}}, nil, "overlays[0].multiplicity", "family", multReasonComposeOutside},
		{"row on a SERIES kind", &types.Request{Overlays: []types.OverlaySpec{ov(seriesKind, mult("", fRow, 0))}}, nil, "overlays[0].multiplicity", "family", multReasonNotMatrix},
		{"column on a SCALAR kind", &types.Request{Overlays: []types.OverlaySpec{ov(types.OverlayKindChiSqMatrix, mult("", fColumn, 0))}}, nil, "overlays[0].multiplicity", "family", multReasonNotMatrix},
		{"explicit method on Tukey", &types.Request{PostTests: []*types.Test{tukey(mult(mHolm, "", 0))}}, nil, "post_tests[0].multiplicity", "method", multReasonTukey},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveMultiplicity(c.req, c.def, nil)
			wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
				"slot": c.slot, "key": c.key, "reason": c.reason,
			})
		})
	}
}

func TestResolveMultiplicity_Conflict(t *testing.T) {
	t.Run("tests disagree", func(t *testing.T) {
		req := &types.Request{Tests: []*types.Test{ttest(mult(mHolm, "", 0)), ttest(mult(mBH, "", 0))}}
		_, err := ResolveMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{
			"family": "request", "methods": []string{"bh", "holm"}, "slots": []string{"tests[1]", "tests[0]"},
		})
	})
	t.Run("test and request-family overlay disagree", func(t *testing.T) {
		req := &types.Request{
			Multiplicity: mult(mHolm, "", 0),
			Tests:        []*types.Test{ttest(nil)},
			Overlays:     []types.OverlaySpec{ov(matrixKind, mult(mBonf, fRequest, 0))},
		}
		_, err := ResolveMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{"family": "request"})
	})
	ok := []struct {
		name string
		req  *types.Request
	}{
		{"different families never conflict", &types.Request{
			Tests:    []*types.Test{ttest(mult(mHolm, "", 0))},
			Overlays: []types.OverlaySpec{ov(matrixKind, mult(mBH, fLayer, 0))},
		}},
		{"an opted-out member is not in the family", &types.Request{
			Tests: []*types.Test{ttest(mult(mHolm, "", 0)), ttest(mult(mNone, "", 0))},
		}},
		{"a descriptive overlay is not a member", &types.Request{
			Tests:    []*types.Test{ttest(mult(mHolm, "", 0))},
			Overlays: []types.OverlaySpec{ov(shareKind, mult(mBH, fRequest, 0))},
		}},
	}
	for _, c := range ok {
		t.Run(c.name, func(t *testing.T) {
			if _, err := ResolveMultiplicity(c.req, nil, nil); err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
		})
	}
}

// TestResolveMultiplicity_HiddenRoutesAsNeverRegistered: a hidden Tukey
// is an ordinary (never-registered) name, so an explicit method is not
// refused as Tukey — exactly as for a name no registry knows; a hidden
// MATRIX kind is not judged by the MATRIX rule.
func TestResolveMultiplicity_HiddenRoutesAsNeverRegistered(t *testing.T) {
	inst := scopedExcept("TEST_TUKEY_HSD", string(matrixKind))
	req := &types.Request{
		PostTests: []*types.Test{tukey(mult(mHolm, "", 0))},
		Overlays:  []types.OverlaySpec{ov(matrixKind, mult(mHolm, fColumn, 0))},
	}
	plan, err := ResolveMultiplicity(req, nil, inst)
	if err != nil {
		t.Fatalf("hidden names refused: %v", err)
	}
	if plan.Overlays[0].Member {
		t.Error("a hidden overlay kind joined a family")
	}
	never := &types.Request{PostTests: []*types.Test{{Type: "TEST_NOT_A_TEST", Multiplicity: mult(mHolm, "", 0)}}}
	if _, err := ResolveMultiplicity(never, nil, nil); err != nil {
		t.Fatalf("never-registered name refused: %v", err)
	}
	// Unscoped: the same request is refused on Tukey.
	_, err = ResolveMultiplicity(req, nil, nil)
	wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{"reason": multReasonTukey})
}

func TestResolveComposeMultiplicity(t *testing.T) {
	slot := func(m *types.Multiplicity, tests ...*types.Test) *types.Request {
		return &types.Request{Multiplicity: m, Tests: tests}
	}
	cov := func(kind types.OverlayKind, m *types.Multiplicity) types.ComposeOverlaySpec {
		return types.ComposeOverlaySpec{Kind: kind, Reference: "request_1", Targets: []string{"request_2"}, Multiplicity: m}
	}
	panel := types.OverlayKindPropZPanel // MATRIX, inferential, Compose host

	t.Run("compose family spans slots and compose overlays", func(t *testing.T) {
		req := &types.ComposedRequest{
			Multiplicity: mult(mHolm, fCompose, 0),
			Requests:     []*types.Request{slot(nil, ttest(nil)), slot(nil, ttest(nil))},
			Overlays:     []types.ComposeOverlaySpec{cov(panel, nil)},
		}
		plan, err := ResolveComposeMultiplicity(req, nil, nil)
		if err != nil {
			t.Fatalf("unexpected refusal: %v", err)
		}
		for i, p := range plan.Requests {
			if g := p.Tests[0]; g.Family != fCompose || g.Method != mHolm || !g.Member {
				t.Errorf("slot %d test = %+v", i, g)
			}
		}
		if g := plan.Overlays[0]; g.Family != fCompose || !g.Member {
			t.Errorf("compose overlay = %+v", g)
		}
	})
	t.Run("composed request family request stays per slot; compose overlays fall back to layer", func(t *testing.T) {
		req := &types.ComposedRequest{
			Multiplicity: mult(mHolm, fRequest, 0),
			Requests:     []*types.Request{slot(nil, ttest(nil))},
			Overlays:     []types.ComposeOverlaySpec{cov(panel, nil)},
		}
		plan, err := ResolveComposeMultiplicity(req, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Requests[0].Tests[0].Family != fRequest || plan.Overlays[0].Family != fLayer {
			t.Errorf("families = %s / %s", plan.Requests[0].Tests[0].Family, plan.Overlays[0].Family)
		}
	})
	t.Run("slot request block beats the composed block", func(t *testing.T) {
		req := &types.ComposedRequest{
			Multiplicity: mult(mHolm, fCompose, 0),
			Requests:     []*types.Request{slot(mult(mBH, fRequest, 0), ttest(nil))},
		}
		plan, err := ResolveComposeMultiplicity(req, mult(mBonf, "", 0), nil)
		if err != nil {
			t.Fatal(err)
		}
		if g := plan.Requests[0].Tests[0]; g.Method != mBH || g.Family != fRequest {
			t.Errorf("test = %+v", g)
		}
	})
	t.Run("request on a compose overlay is refused", func(t *testing.T) {
		req := &types.ComposedRequest{Requests: []*types.Request{slot(nil)}, Overlays: []types.ComposeOverlaySpec{cov(panel, mult("", fRequest, 0))}}
		_, err := ResolveComposeMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{
			"slot": "overlays[0].multiplicity", "key": "family", "reason": multReasonSurface,
			"valid": []string{"layer", "row", "column", "compose"},
		})
	})
	t.Run("a slot refusal is located", func(t *testing.T) {
		req := &types.ComposedRequest{Requests: []*types.Request{slot(nil), slot(nil, ttest(mult("", fLayer, 0)))}}
		_, err := ResolveComposeMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{"slot": "tests[0].multiplicity", "request": 1})
	})
	t.Run("compose members disagree across slots", func(t *testing.T) {
		req := &types.ComposedRequest{
			Multiplicity: mult("", fCompose, 0),
			Requests:     []*types.Request{slot(mult(mHolm, "", 0), ttest(nil)), slot(mult(mBH, "", 0), ttest(nil))},
		}
		_, err := ResolveComposeMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{
			"family": "compose", "methods": []string{"bh", "holm"},
			"slots": []string{"requests[1].tests[0]", "requests[0].tests[0]"},
		})
	})
	t.Run("compose overlay disagrees with slot members", func(t *testing.T) {
		req := &types.ComposedRequest{
			Multiplicity: mult(mHolm, fCompose, 0),
			Requests:     []*types.Request{slot(nil, ttest(nil))},
			Overlays:     []types.ComposeOverlaySpec{cov(panel, mult(mBH, "", 0))},
		}
		_, err := ResolveComposeMultiplicity(req, nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_CONFLICT, map[string]any{
			"slots": []string{"overlays[0]", "requests[0].tests[0]"},
		})
	})
	t.Run("nothing named", func(t *testing.T) {
		req := &types.ComposedRequest{Requests: []*types.Request{slot(nil, ttest(nil))}, Overlays: []types.ComposeOverlaySpec{cov(panel, nil)}}
		if plan, err := ResolveComposeMultiplicity(req, nil, nil); plan != nil || err != nil {
			t.Fatalf("plan = %+v, err = %v", plan, err)
		}
	})
}

func TestResolveFacetMultiplicity(t *testing.T) {
	facet := func(m *types.Multiplicity) *types.FacetRequest {
		return &types.FacetRequest{Overlays: []types.OverlaySpec{ov(types.OverlayKindIndexVsPop, m)}}
	}
	if got, err := ResolveFacetMultiplicity(facet(nil), nil, nil); got != nil || err != nil {
		t.Fatalf("nothing named: %v, %v", got, err)
	}
	got, err := ResolveFacetMultiplicity(facet(nil), mult(mHolm, fRequest, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Family != fLayer || got[0].Method != mHolm {
		t.Errorf("inherited request family on a facet overlay = %+v, want layer", got[0])
	}
	for _, f := range []types.MultiplicityFamily{fRequest, fCompose, fRow} {
		_, err := ResolveFacetMultiplicity(facet(mult("", f, 0)), nil, nil)
		wantCoded(t, err, errors.PULSE_MULTIPLICITY_INVALID, map[string]any{"slot": "overlays[0].multiplicity", "key": "family"})
	}
}

// TestSlotRefusal_MultiplicityAtEveryNestingLevel: with
// capability:multiplicity hidden every `multiplicity` key — root and
// nested, on every request root — is an unknown field; enabled, none
// is.
func TestSlotRefusal_MultiplicityAtEveryNestingLevel(t *testing.T) {
	m := mult(mHolm, "", 0)
	base := []string{featProcess, featCompose, featCrosstab, featFacet, string(matrixKind), string(types.OverlayKindPropZPanel), string(types.OverlayKindIndexVsPop)}
	hidden := scopedOnly(base...)
	enabled := scopedOnly(append(base, featMultiplicity)...)
	cases := []struct {
		name string
		v    any
		path string // "" = root
		loc  map[string]any
	}{
		{"request root", &types.Request{Multiplicity: m}, "", nil},
		{"test", &types.Request{Tests: []*types.Test{ttest(m)}}, "tests[0]", nil},
		{"post-test", &types.Request{PostTests: []*types.Test{ttest(nil), ttest(m)}}, "post_tests[1]", nil},
		{"request overlay", &types.Request{Overlays: []types.OverlaySpec{ov(matrixKind, m)}}, "overlays[0]", nil},
		{"facet overlay", &types.FacetRequest{Overlays: []types.OverlaySpec{ov(types.OverlayKindIndexVsPop, m)}}, "overlays[0]", nil},
		{"composed root", &types.ComposedRequest{Multiplicity: m}, "", nil},
		{"compose overlay", &types.ComposedRequest{Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindPropZPanel, Multiplicity: m}}}, "overlays[0]", nil},
		{"compose slot test", &types.ComposedRequest{Requests: []*types.Request{{}, {Tests: []*types.Test{ttest(m)}}}}, "tests[0]", map[string]any{"request": 1}},
		{"chain stage", &types.ChainRequest{Stages: []*types.ChainStage{{Request: &types.Request{Multiplicity: m}}}}, "", map[string]any{"stage": 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := SlotRefusal(c.v, enabled); err != nil {
				t.Fatalf("enabled instance refused: %v", err)
			}
			want := map[string]any{"unknown_keys": []string{"multiplicity"}}
			if c.path != "" {
				want["path"] = c.path
			}
			for k, v := range c.loc {
				want[k] = v
			}
			wantCoded(t, SlotRefusal(c.v, hidden), errors.PULSE_REQUEST_UNKNOWN_FIELD, want)
		})
	}
}

// TestPayloadSchema_MultiplicityHidden: the instance schema drops every
// `multiplicity` property and the Multiplicity defs with capability:
// multiplicity hidden, and carries them (with the closed method /
// family enums) when it is enabled.
func TestPayloadSchema_MultiplicityHidden(t *testing.T) {
	base := []string{featProcess, featCompose, featCrosstab, featFacet, string(matrixKind)}
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(append(base, featMultiplicity)...), true},
		{"hidden", scopedOnly(base...), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			for _, tok := range []string{`"multiplicity"`, `"Multiplicity"`, `"MultiplicityMethod"`, `"MultiplicityFamily"`} {
				if got := strings.Contains(s, tok); got != c.want {
					t.Errorf("%s present = %v, want %v", tok, got, c.want)
				}
			}
			if c.want && !strings.Contains(s, `"bonferroni"`) {
				t.Error("method enum missing from the schema")
			}
		})
	}
}
