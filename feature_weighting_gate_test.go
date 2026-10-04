package pulse

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// capability:weighting at the facade: hidden, every `weight` key is an
// unknown field on every entry point at every nesting level, and
// Options.DefaultWeight fails pulse.New; AGG_WEIGHTED_MEAN's
// params.weight_field keeps working (the deliberate asymmetry).

// weightingProfile offers the request hosts and every weight-bearing
// slot family the probes set, plus capability:weighting when with.
func weightingProfile(with bool) []string {
	f := []string{
		"capability:process", "capability:compose", "capability:process_chain", "capability:crosstab",
		"AGG_COUNT", "AGG_SUM", "AGG_WEIGHTED_MEAN", "GROUP_CATEGORY", "TEST_T", "REG_OLS", "ATTR_ZSCORE",
		"OVERLAY_SHARE_OF_ROW",
	}
	if with {
		f = append(f, descx.FeatureWeighting)
	}
	return f
}

func weightingHost(t *testing.T, fsys afero.Fs, with bool) *parityHost {
	t.Helper()
	name := "weighting-off"
	if with {
		name = "weighting-on"
	}
	return newParityHostWith(t, fsys, name, parityHostConfig{profiles: map[string][]string{name: weightingProfile(with)}})
}

// nestedWeightProbes set one per-slot weight each; the key is the slot
// path the refusal names.
var nestedWeightProbes = map[string]func(r *types.Request){
	"aggregations[1]": func(r *types.Request) {
		r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "age", Label: "s", Weight: types.SlotWeightField("age")})
	},
	"crosstab.cell": func(r *types.Request) {
		requestSlotAppliers["crosstab"](r)
		r.Crosstab.Cell.Weight = types.SlotWeightField("age")
	},
	"crosstab.margin_aggregations[0]": func(r *types.Request) {
		requestSlotAppliers["crosstab"](r)
		r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "base", Weight: types.NullSlotWeight()}}
	},
	"crosstab.rows[0]": func(r *types.Request) {
		requestSlotAppliers["crosstab"](r)
		r.Crosstab.Rows[0].Weight = types.NullSlotWeight()
	},
	"crosstab.columns[0]": func(r *types.Request) {
		requestSlotAppliers["crosstab"](r)
		r.Crosstab.Columns[0].Weight = types.SlotWeightField("age")
	},
	"groups[0]": func(r *types.Request) { r.Groups[0].Weight = types.NullSlotWeight() },
	"tests[0]": func(r *types.Request) {
		r.Tests = []*types.Test{{Type: types.TEST_T, Field: "age", Weight: types.NullSlotWeight()}}
	},
	"post_tests[0]": func(r *types.Request) {
		r.PostTests = []*types.Test{{Type: types.TEST_T, Field: "n", Weight: types.NullSlotWeight()}}
	},
	"regressions[0]": func(r *types.Request) {
		r.Regressions = []*types.RegressionSpec{{Type: types.REG_OLS, Target: "age", Weight: types.NullSlotWeight()}}
	},
	"attributes[0]": func(r *types.Request) {
		r.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "age", Label: "z", Weight: types.NullSlotWeight()}}
	},
	"overlays[0]": func(r *types.Request) {
		requestSlotAppliers["crosstab"](r)
		r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow, Weight: types.SlotWeightField("age")}}
	},
}

// nestedWeightEntryPoints are requestSlotEntryPoints with the chain's
// second stage carrying the probe request itself.
func nestedWeightEntryPoints() map[string]func(h *parityHost, req *types.Request) *slotGateOutcome {
	out := map[string]func(h *parityHost, req *types.Request) *slotGateOutcome{}
	for name, run := range requestSlotEntryPoints {
		if name != "ProcessChain/stage1" {
			out[name] = run
		}
	}
	out["ProcessChain/stage1"] = func(h *parityHost, req *types.Request) *slotGateOutcome {
		_, err := h.p.ProcessChain(context.Background(), &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "base", Request: h.base()}, {Name: "probe", Request: req}},
		})
		return slotGateFromErr(err)
	}
	return out
}

// TestHiddenWeighting_NestedWeightRefusedEverywhere: every per-slot
// weight, every entry point, Go struct and JSON-decoded — refused with
// the slot path, its valid_keys free of `weight`; the instance that
// offers weighting refuses none as an unknown field.
func TestHiddenWeighting_NestedWeightRefusedEverywhere(t *testing.T) {
	fsys := parityFS(t)
	off, on := weightingHost(t, fsys, false), weightingHost(t, fsys, true)
	on.base = off.base
	ran := 0
	for path, set := range nestedWeightProbes {
		for name, run := range nestedWeightEntryPoints() {
			build := func() *types.Request {
				req := withCohort(off)
				set(req)
				return req
			}
			t.Run(path+"/"+name, func(t *testing.T) {
				ran++
				if open := run(on, build()); open != nil && open.code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
					t.Fatalf("weighting enabled refuses %s: %+v", path, open)
				}
				raw, err := json.Marshal(build())
				if err != nil {
					t.Fatal(err)
				}
				var decoded types.Request
				if err := json.Unmarshal(raw, &decoded); err != nil {
					t.Fatal(err)
				}
				for _, req := range []*types.Request{build(), &decoded} {
					got := run(off, req)
					if got == nil || got.code != perr.PULSE_REQUEST_UNKNOWN_FIELD {
						t.Fatalf("want PULSE_REQUEST_UNKNOWN_FIELD, got %+v", got)
					}
					if keys := toStrings(got.details["unknown_keys"]); !slices.Equal(keys, []string{"weight"}) {
						t.Errorf("unknown_keys = %v", keys)
					}
					if got.details["path"] != path {
						t.Errorf("path = %v, want %s", got.details["path"], path)
					}
					if slices.Contains(toStrings(got.details["valid_keys"]), "weight") {
						t.Errorf("valid_keys lists weight")
					}
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no cell ran")
	}
}

// TestHiddenWeighting_DefaultWeightRefusedAtNew: an instance default
// weight under a profile without capability:weighting fails pulse.New
// with the dependency code, naming the option; with the capability (or
// without a profile) it is accepted.
func TestHiddenWeighting_DefaultWeightRefusedAtNew(t *testing.T) {
	fsys := afero.NewMemMapFs()
	dw := &types.WeightSpec{Field: "w"}
	if _, err := New(Options{FS: fsys, DefaultWeight: dw}); err != nil {
		t.Fatalf("profile-free: %v", err)
	}
	if _, err := New(Options{FS: fsys, DefaultWeight: dw, FeatureProfile: &FeatureProfile{Features: weightingProfile(true)}}); err != nil {
		t.Fatalf("weighting enabled: %v", err)
	}
	if _, err := New(Options{FS: fsys, FeatureProfile: &FeatureProfile{Features: weightingProfile(false)}}); err != nil {
		t.Fatalf("weighting hidden, no default: %v", err)
	}

	_, err := New(Options{FS: fsys, DefaultWeight: dw, FeatureProfile: &FeatureProfile{Features: weightingProfile(false)}})
	out := slotGateFromErr(err)
	if out == nil || out.code != perr.PULSE_FEATURE_PROFILE_DEPENDENCY {
		t.Fatalf("want PULSE_FEATURE_PROFILE_DEPENDENCY, got %v", err)
	}
	wantUnmet := []map[string]any{{"option": "Options.DefaultWeight", "requires_any_of": []string{descx.FeatureWeighting}}}
	if !reflect.DeepEqual(out.details["unmet"], wantUnmet) || !reflect.DeepEqual(out.details["options"], []string{"Options.DefaultWeight"}) {
		t.Errorf("details = %v", out.details)
	}

	// From a file: the path rides the details like every profile fault.
	raw, _ := json.Marshal(FeatureProfile{Features: weightingProfile(false)})
	if err := afero.WriteFile(fsys, "fp.json", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = New(Options{FS: fsys, DefaultWeight: dw, FeatureProfileFile: "fp.json"})
	if out := slotGateFromErr(err); out == nil || out.code != perr.PULSE_FEATURE_PROFILE_DEPENDENCY || out.details["path"] != "fp.json" {
		t.Errorf("file profile: %v", err)
	}
}

// TestHiddenWeighting_WeightedMeanParamUngated: AGG_WEIGHTED_MEAN's
// params.weight_field is the operator's own parameter, not the weight
// surface — it runs, and answers, identically with weighting hidden.
func TestHiddenWeighting_WeightedMeanParamUngated(t *testing.T) {
	fsys := parityFS(t)
	off, on := weightingHost(t, fsys, false), weightingHost(t, fsys, true)
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: parityCohort},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_WEIGHTED_MEAN, Field: "age", Label: "wm",
				Params: json.RawMessage(`{"weight_field":"age"}`)}},
		}
	}
	want, err := on.p.Process(context.Background(), req())
	if err != nil {
		t.Fatalf("weighting enabled: %v", err)
	}
	got, err := off.p.Process(context.Background(), req())
	if err != nil {
		t.Fatalf("weighting hidden refuses weight_field: %v", err)
	}
	if len(got.Data) == 0 || !reflect.DeepEqual(got.Data, want.Data) {
		t.Errorf("weighted mean differs with weighting hidden:\n got %v\nwant %v", got.Data, want.Data)
	}
}

// TestHiddenWeighting_ErrorCodesFollowOwners: hidden weighting lists the
// extension weight-awareness code nowhere; the two codes
// AGG_WEIGHTED_MEAN's weight_field reaches stay listed while it is on.
func TestHiddenWeighting_ErrorCodesFollowOwners(t *testing.T) {
	newP := func(features []string) *Pulse {
		p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: features}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	listed := func(p *Pulse, code perr.Code) bool {
		_, ok := p.ErrorLookup(string(code))
		return ok
	}
	on, off := newP(weightingProfile(true)), newP(weightingProfile(false))
	bare := newP([]string{"capability:process", "AGG_SUM"})
	for _, c := range []perr.Code{perr.PULSE_WEIGHT_INVALID_ROWS, perr.PULSE_WEIGHT_UNSUPPORTED, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE} {
		if !listed(on, c) {
			t.Errorf("weighting enabled: %s not listed", c)
		}
		if listed(bare, c) {
			t.Errorf("no weighting, no AGG_WEIGHTED_MEAN: %s listed", c)
		}
	}
	if listed(off, perr.PULSE_EXTENSION_NOT_WEIGHT_AWARE) {
		t.Error("weighting hidden: PULSE_EXTENSION_NOT_WEIGHT_AWARE listed")
	}
	for _, c := range []perr.Code{perr.PULSE_WEIGHT_INVALID_ROWS, perr.PULSE_WEIGHT_UNSUPPORTED} {
		if !listed(off, c) {
			t.Errorf("weighting hidden with AGG_WEIGHTED_MEAN: %s not listed", c)
		}
	}
}
