package descriptor

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// TestRegressionCapabilities_ParamsAreSlotKeys (#371): a built-in
// regression reads no params object — every knob is a typed
// types.RegressionSpec field — so every Param its manifest entry
// declares must be marked Slot (a model regression's own params keys,
// its declaration's, excepted) (descriptor.Param.Slot: "every
// RegressionSpec knob") and must name a top-level RegressionSpec JSON
// key. A non-slot param, or a key nested under an object (bounds'
// nonnegative, covariance's type), cannot sneak into the list.
func TestRegressionCapabilities_ParamsAreSlotKeys(t *testing.T) {
	t.Parallel()

	spec := map[string]bool{}
	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if key != "" && key != "-" && key != "params" {
			spec[key] = true
		}
	}
	caps := regressionCapabilities()
	if len(caps) != len(types.AllRegressionTypes()) {
		t.Fatalf("%d regression capabilities, %d registered types", len(caps), len(types.AllRegressionTypes()))
	}
	for _, c := range caps {
		if len(c.Params) == 0 {
			t.Errorf("%s declares no params", c.Name)
		}
		own := modelRegressionOwnParams(c.Name)
		for _, p := range c.Params {
			if !p.Slot && own[p.Name] {
				// A model regression's own params-object key (its
				// declaration's Params), the one exception.
				continue
			}
			if !p.Slot {
				t.Errorf("%s param %q lacks Slot: true (a RegressionSpec knob, never a params key)", c.Name, p.Name)
			}
			if !spec[p.Name] {
				t.Errorf("%s param %q is not a top-level types.RegressionSpec JSON key", c.Name, p.Name)
			}
		}
	}
}

// TestRegressionParamsAllowlist_FromManifest: the built-in regression
// params allowlist is the manifest Param list split by Slot — no
// params key, every declared knob listed as a slot key — so a slot key
// written under params is refused PULSE_REQUEST_UNKNOWN_FIELD.
func TestRegressionParamsAllowlist_FromManifest(t *testing.T) {
	t.Parallel()

	allow := ParamsAllowlist(nil)
	for _, c := range regressionCapabilities() {
		d, ok := allow[paramread.Key{Category: paramread.Regression, Op: c.Name}]
		if !ok || !d.Declared {
			t.Fatalf("%s: no declared allowlist entry", c.Name)
		}
		own := modelRegressionOwnParams(c.Name)
		if len(d.Keys) != len(own) {
			t.Errorf("%s: params keys %v, want %d (a built-in reads no params object; a model regression its declaration's)", c.Name, d.Keys, len(own))
		}
		for _, k := range d.Keys {
			if !own[k] {
				t.Errorf("%s: params key %q is not its declaration's", c.Name, k)
			}
		}
		if len(d.Slot) != len(c.Params)-len(own) {
			t.Errorf("%s: %d slot keys %v, want the %d declared slot params", c.Name, len(d.Slot), d.Slot, len(c.Params)-len(own))
		}
	}
}

// regressionKeyProbes are one valid request fragment per top-level
// types.RegressionSpec JSON key (type and params excluded: the entry's
// identity and the extension-only params object): the key set to a
// value a built-in that honours it accepts, with any companion key it
// needs (alpha rides a penalty, a resample knob its resample). A new
// RegressionSpec field without a probe fails
// TestRegressionCapabilities_DeclareEveryAcceptedKey.
var regressionKeyProbes = map[string]string{
	"name":             `{"name":"m"}`,
	"target":           `{}`,
	"predictors":       `{}`,
	"penalty":          `{"penalty":"l2","alpha":1}`,
	"alpha":            `{"penalty":"l2","alpha":1}`,
	"l1_ratio":         `{"penalty":"elasticnet","alpha":1,"l1_ratio":0.5}`,
	"unpenalized":      `{"penalty":"l2","alpha":1,"unpenalized":["a"]}`,
	"bounds":           `{"bounds":{"nonnegative":["a"]}}`,
	"family":           `{"family":"binomial"}`,
	"link":             `{"family":"binomial","link":"logit"}`,
	"theta":            `{"family":"negative_binomial","theta":1.5}`,
	"dispersion":       `{"family":"negative_binomial","theta":1.5,"dispersion":"estimated"}`,
	"max_iters":        `{"max_iters":10}`,
	"tol":              `{"tol":1e-6}`,
	"prior":            `{"prior":"nig"}`,
	"prior_mu":         `{"prior_mu":[0,0,0]}`,
	"prior_precision":  `{"prior_precision":1}`,
	"prior_shape":      `{"prior_shape":1}`,
	"prior_rate":       `{"prior_rate":1}`,
	"credible_level":   `{"credible_level":0.9}`,
	"resample":         `{"resample":"jackknife"}`,
	"jackknife_groups": `{"resample":"jackknife","jackknife_groups":2}`,
	"jackknife_by":     `{"resample":"jackknife","jackknife_by":"g"}`,
	"bootstrap_iters":  `{"resample":"bootstrap","bootstrap_iters":10}`,
	"rng_seed":         `{"resample":"bootstrap","rng_seed":3}`,
	"selection":        `{"selection":"forward","criterion":"aic"}`,
	"criterion":        `{"selection":"forward","criterion":"aic"}`,
	"vcov":             `{"vcov":true}`,
	"margins":          `{"margins":{}}`,
	"intercept":        `{"intercept":false}`,
	"solver":           `{"solver":"normal"}`,
	"covariance":       `{"covariance":{"type":"hc1"}}`,
	"conf_level":       `{"conf_level":0.9}`,
	"save":             `{"save":"m.pulse-model.json"}`,
	"weight":           `{"weight":{"field":"w","kind":"frequency"}}`,
	"multiplicity":     `{"multiplicity":{"method":"holm"}}`,
	"holdout":          `{"holdout":"a > 1"}`,
}

// regressionKeysAcceptedUnread is the ledger of top-level keys a
// built-in's predict (and runtime, which runs the same shared checks)
// accepts WITHOUT refusing yet never reads: another engine's knob that
// no rule refuses on this one. They are deliberately left out of the
// manifest Param list — declaring them would advertise a knob that
// changes nothing. A key leaving this ledger (a refusal added, or the
// engine starting to honour it) must leave it in the same change.
var regressionKeysAcceptedUnread = map[types.RegressionType][]string{
	// REG_OLS has no family / link (REG_GLM's) and no prior
	// (REG_BAYES_LINEAR's), and no rule refuses them on it.
	types.REG_OLS: {"credible_level", "family", "link", "prior", "prior_mu", "prior_precision", "prior_rate", "prior_shape"},
	// REG_GLM has no prior (REG_BAYES_LINEAR's).
	types.REG_GLM: {"credible_level", "prior", "prior_mu", "prior_precision", "prior_rate", "prior_shape"},
	// REG_BAYES_LINEAR is closed form: no iterative solver to cap.
	types.REG_BAYES_LINEAR: {"max_iters", "tol"},
}

// TestRegressionCapabilities_DeclareEveryAcceptedKey (#371): a built-in
// regression's manifest Param list names EVERY top-level RegressionSpec
// key it honours — the shared slots (name, weight, save, multiplicity)
// as much as its own knobs — so an embedder placing keys by the
// manifest can reach each one. Each key is probed through predict on
// one request (regressionKeyProbes); the declared set must equal the
// accepted set minus the accepted-but-unread ledger, and a refused key
// (REG_BAYES_LINEAR's multiplicity, resample, selection; REG_GLM's
// unpenalized / bounds) must not be declared.
func TestRegressionCapabilities_DeclareEveryAcceptedKey(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeOf(types.RegressionSpec{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
		if key == "" || key == "-" || key == "type" || key == "params" {
			continue
		}
		if _, ok := regressionKeyProbes[key]; !ok {
			t.Errorf("types.RegressionSpec key %q has no probe in regressionKeyProbes", key)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64, Description: "Outcome value"},
		{Name: "a", Type: encoding.FieldTypeF64, Description: "First predictor value"},
		{Name: "b", Type: encoding.FieldTypeF64, Description: "Second predictor value"},
		{Name: "w", Type: encoding.FieldTypeF64, Description: "Row weight value"},
		{Name: "g", Type: encoding.FieldTypeU8, Description: "Group code value"},
	}}
	data := buildTestPulseFile(t, schema)
	for _, c := range regressionCapabilities() {
		accepted := map[string]bool{}
		for key, frag := range regressionKeyProbes {
			body := map[string]any{"type": c.Name, "target": "y", "predictors": []string{"a", "b"}}
			if c.Name == string(types.REG_GLM) {
				body["family"] = "binomial"
			}
			if c.Name == string(types.REG_MMM_HILL) {
				body["params"] = map[string]any{"channels": []string{"a"}}
			}
			if c.Name == string(types.REG_MMM_BAYES) {
				body["params"] = map[string]any{"channels": []string{"a"}, "time": "g"}
			}
			var extra map[string]any
			if err := json.Unmarshal([]byte(frag), &extra); err != nil {
				t.Fatalf("probe %s: %v", key, err)
			}
			maps.Copy(body, extra)
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var spec types.RegressionSpec
			if err := json.Unmarshal(raw, &spec); err != nil {
				t.Fatalf("%s probe %s: %v", c.Name, key, err)
			}
			env := predictFromBytes(data, &types.Request{Regressions: []*types.RegressionSpec{&spec}}, nil)
			if len(env.Errors) == 0 {
				accepted[key] = true
			}
		}
		if !accepted["target"] {
			t.Fatalf("%s: the bare probe request is refused; every key probe is vacuous", c.Name)
		}
		unread := map[string]bool{}
		for _, k := range regressionKeysAcceptedUnread[types.RegressionType(c.Name)] {
			unread[k] = true
			if !accepted[k] {
				t.Errorf("%s: ledger key %q is refused now; drop it from regressionKeysAcceptedUnread", c.Name, k)
			}
		}
		declared := map[string]bool{}
		for _, p := range c.Params {
			if p.Slot {
				declared[p.Name] = true
			}
		}
		for _, k := range slices.Sorted(maps.Keys(accepted)) {
			if !unread[k] && !declared[k] {
				t.Errorf("%s accepts top-level key %q but its manifest Params do not declare it", c.Name, k)
			}
			if unread[k] && declared[k] {
				t.Errorf("%s declares %q, which the ledger says it never reads", c.Name, k)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(declared)) {
			if !accepted[k] {
				t.Errorf("%s declares %q, which its predict refuses", c.Name, k)
			}
		}
	}
}

// TestManifest_RegressionSlotParamsFollowHiddenSlots: a regression
// Param naming a gated RegressionSpec key (weight → capability:weighting,
// save → capability:model_save, multiplicity → capability:multiplicity)
// is absent from a scoped manifest that hides its capability — built-in
// and extension REG_* alike — exactly as the payload schema drops the
// key; enabled, it is listed.
func TestManifest_RegressionSlotParamsFollowHiddenSlots(t *testing.T) {
	t.Parallel()

	const ext = "REG_ACME_FIT"
	gated := []string{"weight", "save", "multiplicity"}
	inst := func(with bool) *InstanceSnapshot {
		names := []string{featProcess, string(types.REG_OLS), ext}
		var hidden []string
		for _, f := range []string{featWeighting, featModelSave, featMultiplicity} {
			if with {
				names = append(names, f)
			} else {
				hidden = append(hidden, f)
			}
		}
		snap := &ExtensionsSnapshot{
			Regressions:       []descriptor.OperatorMeta{{Name: ext}},
			RegressionDetails: map[string]ExtensionRegression{ext: {PValues: true, Scorer: true}},
		}
		return NewInstanceSnapshot(snap, FeatureSet{Enabled: names, Hidden: hidden})
	}
	names := func(ps []descriptor.Param) map[string]bool {
		out := map[string]bool{}
		for _, p := range ps {
			out[p.Name] = true
		}
		return out
	}
	for _, with := range []bool{true, false} {
		m := BuildManifestForInstance(inst(with))
		if len(m.Regressions) != 1 || len(m.Extensions.Regressions) != 1 {
			t.Fatalf("with=%v: regressions %d, extension regressions %d, want 1 each", with, len(m.Regressions), len(m.Extensions.Regressions))
		}
		for i, r := range []descriptor.RegressionMeta{m.Regressions[0], m.Extensions.Regressions[0]} {
			got := names(r.Params)
			keys := gated
			if i == 1 {
				// An extension model declares save (Scorer) and
				// multiplicity (PValues); its weight rides weight_kinds.
				keys = gated[1:]
			}
			for _, k := range keys {
				if got[k] != with {
					t.Errorf("with=%v: %s param %q listed=%v", with, r.Name, k, got[k])
				}
			}
			if got[""] {
				t.Errorf("with=%v: %s carries a nameless param (a scrubbed slot key)", with, r.Name)
			}
		}
	}
}

// modelRegressionOwnParams are model regression name's own params-object
// keys (its declaration's Params), empty for every other type.
func modelRegressionOwnParams(name string) map[string]bool {
	out := map[string]bool{}
	decl, ok := ModelRegressionDeclaration(types.RegressionType(name))
	if !ok {
		return out
	}
	for _, p := range decl.Params {
		out[p.Name] = true
	}
	return out
}
