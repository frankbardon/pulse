package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// seriesGood is a well-formed spec of a ruled SERIES kind.
func seriesGood(kind types.OverlayKind) *types.OverlaySpec {
	s := &types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup}
	switch seriesRules[kind].arm {
	case seriesRollingMean:
		s.Ref.RollingMean = &types.OverlayRollingMeanRef{}
	case seriesYoY:
		s.Ref.YoY = &types.OverlayYoYRef{}
	case seriesBaseline:
		s.Ref.BaselineIndex = &types.OverlayBaselineIndexRef{}
	case seriesSibling:
		s.Ref.Sibling = &types.OverlaySiblingRef{Field: "g", Value: "a"}
	}
	return s
}

// everyRefArm sets each OverlayRef arm, one per entry.
var everyRefArm = map[string]func(*types.OverlayRef){
	"margin":         func(r *types.OverlayRef) { r.Margin = &types.OverlayMarginRef{Axis: types.MarginAxisRow} },
	"sibling":        func(r *types.OverlayRef) { r.Sibling = &types.OverlaySiblingRef{Field: "g", Value: "a"} },
	"baseline_index": func(r *types.OverlayRef) { r.BaselineIndex = &types.OverlayBaselineIndexRef{} },
	"prior":          func(r *types.OverlayRef) { r.Prior = &types.OverlayPriorRef{} },
	"rolling_mean":   func(r *types.OverlayRef) { r.RollingMean = &types.OverlayRollingMeanRef{} },
	"yoy":            func(r *types.OverlayRef) { r.YoY = &types.OverlayYoYRef{} },
	"population":     func(r *types.OverlayRef) { r.Population = &types.OverlayPopulationRef{Cohort: "c"} },
	"stage":          func(r *types.OverlayRef) { r.Stage = &types.StageRef{Name: "s"} },
	"slot":           func(r *types.OverlayRef) { r.Slot = &types.OverlaySlotRef{Name: "s"} },
	"matrix":         func(r *types.OverlayRef) { r.Matrix = &types.OverlayMatrixRef{Name: "m"} },
}

// TestSeriesSpecRefusal_Rules: every rule passes its well-formed spec,
// refuses every foreign Ref arm (all ten are judged), the kind's own arm
// missing where required, a non-zero Prior.Lag and an empty
// Sibling.Field / Value, then a non-group scope — index only when >= 0,
// host "series" only on OVERLAY_SHARE_OF_TOTAL. A kind without a rule
// (a crosstab kind, the empty route of a hidden kind) is never refused.
func TestSeriesSpecRefusal_Rules(t *testing.T) {
	ref, scope := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED
	for _, kind := range SeriesSpecKinds() {
		rule := seriesRules[kind]
		good := seriesGood(kind)
		if cerr := SeriesSpecRefusal(good, kind, 0); cerr != nil {
			t.Fatalf("%s: well-formed spec refused: %v", kind, cerr)
		}
		wantHost := func(cerr *errors.CodedError) {
			t.Helper()
			if (cerr.Details["host"] == "series") != rule.dispatch {
				t.Fatalf("%s: host detail %v, dispatch %v", kind, cerr.Details["host"], rule.dispatch)
			}
		}
		bad := *good
		bad.Scope = types.OverlayScopeCell
		cerr := SeriesSpecRefusal(&bad, kind, 3)
		if cerr == nil || cerr.Code != scope || cerr.Details["scope"] != "cell" || cerr.Details["index"] != 3 {
			t.Fatalf("%s: scope refusal %v", kind, cerr)
		}
		wantHost(cerr)
		own := map[seriesArm]string{seriesPrior: "prior", seriesRollingMean: "rolling_mean", seriesYoY: "yoy", seriesBaseline: "baseline_index", seriesSibling: "sibling"}[rule.arm]
		for name, set := range everyRefArm {
			if name == own {
				continue
			}
			r := good.Ref
			set(&r)
			spec := *good
			spec.Ref = r
			spec.Scope = types.OverlayScopeCell // a bad Ref is judged first
			cerr := SeriesSpecRefusal(&spec, kind, -1)
			if cerr == nil || cerr.Code != ref || cerr.Details["index"] != nil || cerr.Details["kind"] != string(kind) {
				t.Fatalf("%s: foreign arm %s refusal %v", kind, name, cerr)
			}
			wantHost(cerr)
		}
		switch rule.arm {
		case seriesRollingMean, seriesYoY, seriesBaseline, seriesSibling:
			spec := *good
			spec.Ref = types.OverlayRef{}
			if cerr := SeriesSpecRefusal(&spec, kind, 0); cerr == nil || cerr.Code != ref {
				t.Fatalf("%s: missing arm refusal %v", kind, cerr)
			}
		case seriesPrior:
			spec := *good
			spec.Ref.Prior = &types.OverlayPriorRef{}
			if cerr := SeriesSpecRefusal(&spec, kind, 0); cerr != nil {
				t.Fatalf("%s: Ref.Prior lag 0 refused: %v", kind, cerr)
			}
			spec.Ref.Prior = &types.OverlayPriorRef{Lag: 2}
			if cerr := SeriesSpecRefusal(&spec, kind, 0); cerr == nil || cerr.Code != ref || cerr.Details["lag"] != 2 {
				t.Fatalf("%s: lag refusal %v", kind, cerr)
			}
		}
		if rule.arm == seriesSibling {
			spec := *good
			spec.Ref.Sibling = &types.OverlaySiblingRef{Value: "a"}
			if cerr := SeriesSpecRefusal(&spec, kind, 0); cerr == nil || cerr.Code != ref {
				t.Fatalf("%s: empty sibling field refusal %v", kind, cerr)
			}
			spec.Ref.Sibling = &types.OverlaySiblingRef{Field: "g"}
			if cerr := SeriesSpecRefusal(&spec, kind, 0); cerr == nil || cerr.Code != ref || cerr.Details["field"] != "g" {
				t.Fatalf("%s: empty sibling value refusal %v", kind, cerr)
			}
		}
	}
	for _, route := range []types.OverlayKind{types.OverlayKindChiSqMatrix, types.OverlayKindFormula, ""} {
		spec := &types.OverlaySpec{Kind: types.OverlayKindIndexVsTotal, Scope: "nowhere",
			Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}}
		if cerr := SeriesSpecRefusal(spec, route, 0); cerr != nil {
			t.Fatalf("route %q refused: %v", route, cerr)
		}
	}
}

// TestSeriesSpecRefusal_CoversSeriesKinds: every Request-slot kind that
// runs on a SERIES Process host has a rule and no rule names another
// kind — a new SERIES kind cannot skip the shared rule.
func TestSeriesSpecRefusal_CoversSeriesKinds(t *testing.T) {
	ruled := map[types.OverlayKind]bool{}
	for _, k := range SeriesSpecKinds() {
		ruled[k] = true
	}
	for _, kind := range types.AllOverlayKinds() {
		series := len(Hosts(kind)) > 0 && Accepts(Request, kind) && ShapeRefusal(types.OverlayShapeSeries, kind, kind, 0) == nil
		if series != ruled[kind] {
			t.Errorf("%s: runs on a SERIES host %v, has a rule %v", kind, series, ruled[kind])
		}
	}
}

// TestProcessSpecRefusal_Dispatch: the crosstab table on a MATRIX host,
// the SERIES table on a SERIES host, nothing on any other shape.
func TestProcessSpecRefusal_Dispatch(t *testing.T) {
	share := &types.OverlaySpec{Kind: types.OverlayKindShareOfTotal, Scope: types.OverlayScopeGroup}
	if cerr := ProcessSpecRefusal(types.OverlayShapeSeries, share, share.Kind, 0); cerr != nil {
		t.Fatalf("series: %v", cerr)
	}
	if cerr := ProcessSpecRefusal(types.OverlayShapeMatrix, share, share.Kind, 0); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {
		t.Fatalf("matrix: %v, want the crosstab rule's missing Ref.Margin", cerr)
	}
	for _, shape := range []types.OverlayShape{types.OverlayShapeScalar, ShapeMatrixResult} {
		if cerr := ProcessSpecRefusal(shape, share, share.Kind, 0); cerr != nil {
			t.Fatalf("%s: %v", shape, cerr)
		}
	}
}

// TestFacetSpecRefusal_Rules: the FACET contract in order — foreign Ref
// arm, missing Population, empty cohort, scope, Level / Within — on
// every FACET kind; a non-FACET kind and an unknown kind are not judged.
func TestFacetSpecRefusal_Rules(t *testing.T) {
	ref := errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE
	for _, kind := range []types.OverlayKind{types.OverlayKindChiSqVsPop, types.OverlayKindIndexVsPop, types.OverlayKindKSVsPop, types.OverlayKindZScoreVsPop} {
		good := types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeGroup, Ref: types.OverlayRef{Population: &types.OverlayPopulationRef{Cohort: "c"}}}
		if cerr := FacetSpecRefusal(&good, kind, 0); cerr != nil {
			t.Fatalf("%s: %v", kind, cerr)
		}
		for name, set := range everyRefArm {
			if name == "population" {
				continue
			}
			spec := good
			set(&spec.Ref)
			spec.Scope = types.OverlayScopeCell
			if cerr := FacetSpecRefusal(&spec, kind, 1); cerr == nil || cerr.Code != ref || cerr.Details["index"] != 1 {
				t.Fatalf("%s: foreign arm %s: %v", kind, name, cerr)
			}
		}
		for name, c := range map[string]struct {
			mut  func(*types.OverlaySpec)
			want errors.Code
		}{
			"population": {func(s *types.OverlaySpec) { s.Ref = types.OverlayRef{} }, ref},
			"cohort":     {func(s *types.OverlaySpec) { s.Ref.Population = &types.OverlayPopulationRef{} }, ref},
			"scope":      {func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeMatrix }, errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
			"level":      {func(s *types.OverlaySpec) { s.Level = 1 }, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
			"within":     {func(s *types.OverlaySpec) { s.Within = 2 }, errors.PULSE_OVERLAY_LEVEL_OUT_OF_RANGE},
		} {
			spec := good
			c.mut(&spec)
			if cerr := FacetSpecRefusal(&spec, kind, 0); cerr == nil || cerr.Code != c.want {
				t.Fatalf("%s: %s: %v, want %s", kind, name, cerr, c.want)
			}
		}
	}
	bad := &types.OverlaySpec{Kind: types.OverlayKindIndexVsTotal, Scope: types.OverlayScopeCell}
	for _, route := range []types.OverlayKind{types.OverlayKindIndexVsTotal, "OVERLAY_NOT_A_KIND", ""} {
		if cerr := FacetSpecRefusal(bad, route, 0); cerr != nil {
			t.Fatalf("route %q refused: %v", route, cerr)
		}
	}
}

// TestComposeSlotRefusals: reference first (alone), then no Targets
// (alone), then one refusal per empty or unknown target.
func TestComposeSlotRefusals(t *testing.T) {
	known := func(l string) bool { return l == "a" || l == "b" }
	for name, c := range map[string]struct {
		ref     string
		targets []string
		codes   []errors.Code
	}{
		"ok":                {"a", []string{"b"}, nil},
		"reference empty":   {"", nil, []errors.Code{errors.PULSE_OVERLAY_REFERENCE_UNKNOWN}},
		"reference unknown": {"zz", []string{"zz"}, []errors.Code{errors.PULSE_OVERLAY_REFERENCE_UNKNOWN}},
		"no targets":        {"a", nil, []errors.Code{errors.PULSE_OVERLAY_TARGET_UNKNOWN}},
		"targets":           {"a", []string{"", "b", "zz"}, []errors.Code{errors.PULSE_OVERLAY_TARGET_UNKNOWN, errors.PULSE_OVERLAY_TARGET_UNKNOWN}},
	} {
		got := ComposeSlotRefusals(&types.ComposeOverlaySpec{Kind: types.OverlayKindIndexVsRef, Reference: c.ref, Targets: c.targets}, 4, known)
		if len(got) != len(c.codes) {
			t.Fatalf("%s: %d refusals %v, want %v", name, len(got), got, c.codes)
		}
		for i, cerr := range got {
			if cerr.Code != c.codes[i] || cerr.Details["index"] != 4 || cerr.Details["kind"] != string(types.OverlayKindIndexVsRef) {
				t.Fatalf("%s: refusal %d = %v", name, i, cerr)
			}
		}
	}
}

// TestResolveStageRef: Index and Name both set is refused (the runtime
// used to run it on Index), neither set defaults a Target to the last
// stage and refuses a Ref, an out-of-range Index and an unknown Name
// refuse under the arm's code.
func TestResolveStageRef(t *testing.T) {
	one, nine := 1, 9
	named := map[string]int{"s0": 0, "s1": 1}
	for name, c := range map[string]struct {
		ref    types.StageRef
		target bool
		idx    int
		code   errors.Code
	}{
		"index":            {types.StageRef{Index: &one}, false, 1, ""},
		"name":             {types.StageRef{Name: "s0"}, true, 0, ""},
		"target default":   {types.StageRef{}, true, 1, ""},
		"ref neither":      {types.StageRef{}, false, 0, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
		"ref both":         {types.StageRef{Index: &one, Name: "s1"}, false, 0, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
		"target both":      {types.StageRef{Index: &one, Name: "s1"}, true, 0, errors.PULSE_OVERLAY_TARGET_UNKNOWN},
		"index range":      {types.StageRef{Index: &nine}, false, 0, errors.PULSE_OVERLAY_REFERENCE_UNKNOWN},
		"target name miss": {types.StageRef{Name: "zz"}, true, 0, errors.PULSE_OVERLAY_TARGET_UNKNOWN},
	} {
		idx, cerr := ResolveStageRef(c.ref, 2, named, c.target, 3)
		if c.code == "" {
			if cerr != nil || idx != c.idx {
				t.Fatalf("%s: (%d, %v), want %d", name, idx, cerr, c.idx)
			}
			continue
		}
		if cerr == nil || cerr.Code != c.code || cerr.Details["index"] != 3 {
			t.Fatalf("%s: %v, want %s", name, cerr, c.code)
		}
	}
	if _, cerr := ResolveStageRef(types.StageRef{}, 0, nil, true, 0); cerr == nil {
		t.Fatal("default Target over no stages resolved")
	}
}
