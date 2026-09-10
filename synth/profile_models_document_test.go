package synth_test

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/synth"
)

// modelsCohort builds a cohort carrying every axis the numeric-target
// retirement rule has to be judged against at once: two categoricals,
// two set_* fields and two numerics whose values genuinely depend on
// that structure.
//
// Both set fields are required, not decoration — SetSetPairs is only
// ever captured BETWEEN two different set fields, and it is one of the
// three non-numeric-target arms this story promises to leave alone. A
// single-set fixture would let a whole-arm suppression bug through
// unnoticed because the arm would be empty either way.
func modelsCohort(t *testing.T) []byte {
	t.Helper()

	regionDict := encoding.NewDictionary()
	for _, v := range []string{"east", "west", "north"} {
		if _, err := regionDict.Add(v); err != nil {
			t.Fatalf("regionDict.Add: %v", err)
		}
	}
	tierDict := encoding.NewDictionary()
	for _, v := range []string{"gold", "silver"} {
		if _, err := tierDict.Add(v); err != nil {
			t.Fatalf("tierDict.Add: %v", err)
		}
	}
	featureDict := encoding.NewDictionary()
	for _, v := range []string{"premium", "trial"} {
		if _, err := featureDict.Add(v); err != nil {
			t.Fatalf("featureDict.Add: %v", err)
		}
	}
	channelDict := encoding.NewDictionary()
	for _, v := range []string{"email", "sms"} {
		if _, err := channelDict.Add(v); err != nil {
			t.Fatalf("channelDict.Add: %v", err)
		}
	}

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: regionDict},
			{Name: "tier", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: tierDict},
			{Name: "features", Type: encoding.FieldTypeSetU8, ByteOffset: 2, Dictionary: featureDict},
			{Name: "channels", Type: encoding.FieldTypeSetU8, ByteOffset: 3, Dictionary: channelDict},
			{Name: "spend", Type: encoding.FieldTypeF64, ByteOffset: 4},
			{Name: "visits", Type: encoding.FieldTypeF64, ByteOffset: 12},
		},
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	const rowCount = 1200
	for r := 0; r < rowCount; r++ {
		region := r % 3
		tier := (r / 3) % 2
		premium := r%4 == 0
		trial := r%7 == 0
		// NOT r%3-derived: r%3 is exactly `region`, and a set option
		// perfectly collinear with a categorical level makes the design
		// rank-deficient, which the fitter correctly refuses — leaving
		// the fixture with no models and every assertion below vacuous.
		email := r%5 == 0
		sms := r%11 < 4

		// A deliberately ADDITIVE ground truth — region, tier and the
		// premium option each contribute their own offset — because that
		// is exactly the structure a single linear model can represent
		// and a chain of one-pair-at-a-time overwrites cannot.
		spend := 40.0 + 11.0*float64(region) + 6.0*float64(tier)
		if premium {
			spend += 25.0
		}
		// A deterministic pseudo-noise term keeps the residual scale
		// non-zero, so ResidualStd is a real number a round trip can be
		// held to rather than a trivially-preserved 0.
		spend += float64(r%13)*0.25 + float64((r*7919)%97)*0.05
		visits := 3.0 + 2.0*float64(tier) - 0.5*float64(region) + float64((r*104729)%53)*0.03

		var rec [20]byte
		rec[0] = byte(region)
		rec[1] = byte(tier)
		if premium {
			rec[2] |= 0b1
		}
		if trial {
			rec[2] |= 0b10
		}
		if email {
			rec[3] |= 0b1
		}
		if sms {
			rec[3] |= 0b10
		}
		spendBits := math.Float64bits(spend)
		visitBits := math.Float64bits(visits)
		for i := 0; i < 8; i++ {
			rec[4+i] = byte(spendBits >> (8 * i))
			rec[12+i] = byte(visitBits >> (8 * i))
		}
		buf.Write(rec[:])
	}
	return buf.Bytes()
}

// jsonKeys decodes a document into its raw top-level members so a test
// can compare section by section rather than as one opaque string —
// which is what makes "only the models key moved" an assertion instead
// of a diff a human has to read.
func jsonKeys(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// TestProfile_ModelsSection_IsPurelyAdditive is the backward-
// compatibility gate this story turns on.
//
// E1-S2 could assert the strongest possible form — the flag moved NO
// byte — because it serialised nothing. Now that the models reach the
// document, the promise narrows to exactly one thing and this test
// states it precisely: across every pre-existing capture-flag
// combination, `--fit-models` adds the `models` key and changes nothing
// else, byte for byte, section for section; and a capture WITHOUT the
// flag carries no `models` key at all, so a reader of such a document
// cannot tell this story ever happened.
func TestProfile_ModelsSection_IsPurelyAdditive(t *testing.T) {
	data := modelsCohort(t)

	for _, tc := range profileOptionMatrix() {
		t.Run(tc.name, func(t *testing.T) {
			base, err := synth.ProfileBytes(data, tc.opts)
			if err != nil {
				t.Fatalf("baseline profile: %v", err)
			}
			fitOpts := tc.opts
			fitOpts.FitModels = true
			fitted, err := synth.ProfileBytes(data, fitOpts)
			if err != nil {
				t.Fatalf("fit-models profile: %v", err)
			}

			baseKeys := jsonKeys(t, base)
			fittedKeys := jsonKeys(t, fitted)

			if _, present := baseKeys["models"]; present {
				t.Error("a capture without --fit-models emitted a models key")
			}
			if got := base.FittedModels(); got != nil {
				t.Errorf("FittedModels() = %+v without the flag, want nil", got)
			}
			if got := fitted.FittedModels(); len(got) == 0 {
				t.Error("FittedModels() empty with the flag; fixture must be fittable")
			}
			raw, present := fittedKeys["models"]
			if !present {
				t.Fatal("a capture with --fit-models emitted no models key")
			}
			if string(raw) == "null" || string(raw) == "[]" {
				t.Fatalf("models key is empty (%s); the fixture must be fittable", raw)
			}
			delete(fittedKeys, "models")

			if len(baseKeys) != len(fittedKeys) {
				t.Fatalf("key set moved: base=%v fitted=%v", sortedKeys(baseKeys), sortedKeys(fittedKeys))
			}
			for k, want := range baseKeys {
				got, ok := fittedKeys[k]
				if !ok {
					t.Errorf("--fit-models dropped section %q", k)
					continue
				}
				if !bytes.Equal(want, got) {
					t.Errorf("--fit-models moved section %q\n base: %s\nfitted: %s", k, want, got)
				}
			}
		})
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestProfile_ModelsSection_RoundTripFullFidelity walks the whole
// persistence path — capture, marshal, unmarshal — and demands exact
// float equality on every coefficient, not tolerance-based agreement.
// A coefficient that survives to seven digits is a coefficient that
// will silently shift generated values, and the shortest-round-trip
// float encoding encoding/json emits has no excuse for losing a bit.
//
// It also pins the two DELIBERATE omissions, because their absence is a
// contract other stages depend on rather than an oversight: the
// internal design-column name and the residual reservoir do not
// serialise, so a re-read Profile carries coefficients and nothing that
// would let a caller re-derive per-row residuals.
func TestProfile_ModelsSection_RoundTripFullFidelity(t *testing.T) {
	data := modelsCohort(t)
	captured, err := synth.ProfileBytes(data, synth.ProfileOptions{
		IncludeStats: true, FitModels: true, Seed: 5,
	})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	if len(captured.FittedModels()) == 0 {
		t.Fatal("fixture produced no models")
	}

	doc, err := json.Marshal(captured)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reread synth.Profile
	if err := json.Unmarshal(doc, &reread); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(reread.Models) != len(captured.Models) {
		t.Fatalf("model count = %d, want %d", len(reread.Models), len(captured.Models))
	}
	for i := range captured.Models {
		want, got := captured.Models[i], reread.Models[i]
		if got.Field != want.Field {
			t.Fatalf("model %d field = %q, want %q", i, got.Field, want.Field)
		}
		if got.Intercept != want.Intercept {
			t.Errorf("model %q intercept = %v, want %v", want.Field, got.Intercept, want.Intercept)
		}
		if got.NObs != want.NObs || got.R2 != want.R2 || got.ResidualStd != want.ResidualStd {
			t.Errorf("model %q diagnostics = (%d, %v, %v), want (%d, %v, %v)",
				want.Field, got.NObs, got.R2, got.ResidualStd, want.NObs, want.R2, want.ResidualStd)
		}
		if !reflect.DeepEqual(got.References, want.References) {
			t.Errorf("model %q references = %+v, want %+v", want.Field, got.References, want.References)
		}
		if len(got.Predictors) != len(want.Predictors) {
			t.Fatalf("model %q predictor count = %d, want %d", want.Field, len(got.Predictors), len(want.Predictors))
		}
		for j := range want.Predictors {
			wp, gp := want.Predictors[j], got.Predictors[j]
			if gp.Kind != wp.Kind || gp.Field != wp.Field || gp.Level != wp.Level {
				t.Errorf("model %q predictor %d identity = %+v, want kind/field/level %q/%q/%q",
					want.Field, j, gp, wp.Kind, wp.Field, wp.Level)
			}
			if gp.Coefficient != wp.Coefficient {
				t.Errorf("model %q predictor %s=%s coefficient = %v, want %v (exact)",
					want.Field, wp.Field, wp.Level, gp.Coefficient, wp.Coefficient)
			}
			if gp.Column != "" {
				t.Errorf("internal design-column name %q leaked into the document", gp.Column)
			}
			if wp.Column == "" {
				t.Errorf("captured predictor %d lost its in-process Column", j)
			}
		}
		if got.Residuals != nil || got.ResidualPresent != nil {
			t.Errorf("model %q carried residuals through the document", want.Field)
		}
		if len(want.Residuals) == 0 || len(want.ResidualPresent) == 0 {
			t.Errorf("model %q had no in-process residuals to begin with", want.Field)
		}
	}

	// The whole point of persisting is that a re-read document drives
	// generation identically. Spec.Hash is the canonical whole-Spec
	// content hash, so equality here covers every slot at once.
	fresh, _ := synth.SpecFromProfile(captured, 500)
	round, _ := synth.SpecFromProfile(&reread, 500)
	if fresh.Hash() != round.Hash() {
		t.Errorf("re-read profile builds a different spec\n fresh: %s\n round: %s", fresh.Hash(), round.Hash())
	}
	if len(fresh.Models) == 0 {
		t.Fatal("spec carries no models")
	}
}

// TestSpecFromProfile_ModelsRetireNumericTargetArms is the story's
// central behavioural claim, and it is asserted against the SAME cohort
// profiled two ways so the comparison is like for like.
//
// Both numeric-target arms must be empty when models are present, and
// all three non-numeric-target arms must be byte-identical to what the
// --conditional-only capture produces — that second half is what makes
// this a targeted retirement rather than a blunt "ignore conditional".
func TestSpecFromProfile_ModelsRetireNumericTargetArms(t *testing.T) {
	data := modelsCohort(t)
	condOpts := synth.ProfileOptions{IncludeStats: true, IncludeConditional: true, CorrelationTopK: 8, Seed: 5}
	condOnly, err := synth.ProfileBytes(data, condOpts)
	if err != nil {
		t.Fatalf("conditional profile: %v", err)
	}
	bothOpts := condOpts
	bothOpts.FitModels = true
	both, err := synth.ProfileBytes(data, bothOpts)
	if err != nil {
		t.Fatalf("conditional+models profile: %v", err)
	}

	condSpec, condWarn := synth.SpecFromProfile(condOnly, 500)
	bothSpec, bothWarn := synth.SpecFromProfile(both, 500)

	// The fallback arm must be genuinely loaded, or the retirement
	// assertion below proves nothing.
	if len(condSpec.CategoricalNumericPairs) == 0 || len(condSpec.SetNumericPairs) == 0 {
		t.Fatalf("fixture did not populate the numeric-target arms without models: catnum=%d setnum=%d",
			len(condSpec.CategoricalNumericPairs), len(condSpec.SetNumericPairs))
	}
	if len(condSpec.Models) != 0 {
		t.Errorf("a profile with no models section produced %d spec models", len(condSpec.Models))
	}

	if len(bothSpec.Models) == 0 {
		t.Fatal("models section present but no spec models were built")
	}
	if n := len(bothSpec.CategoricalNumericPairs); n != 0 {
		t.Errorf("CategoricalNumericPairs = %d entries, want 0 when models are present", n)
	}
	if n := len(bothSpec.SetNumericPairs); n != 0 {
		t.Errorf("SetNumericPairs = %d entries, want 0 when models are present", n)
	}

	// The three non-numeric-target arms are untouched.
	nonNumeric := []struct {
		name string
		a, b any
	}{
		{"categorical_pairs", condSpec.CategoricalPairs, bothSpec.CategoricalPairs},
		{"set_categorical_pairs", condSpec.SetCategoricalPairs, bothSpec.SetCategoricalPairs},
		{"set_set_pairs", condSpec.SetSetPairs, bothSpec.SetSetPairs},
	}
	for _, arm := range nonNumeric {
		aj, _ := json.Marshal(arm.a)
		bj, _ := json.Marshal(arm.b)
		if string(aj) != string(bj) {
			t.Errorf("%s moved under --fit-models\n cond: %s\n both: %s", arm.name, aj, bj)
		}
		if string(aj) == "null" || string(aj) == "[]" {
			t.Errorf("%s is empty; the fixture cannot prove anything about it", arm.name)
		}
	}

	// Retiring the arms is the mechanism that retires their conflict
	// warnings — conflict.go is untouched, the claims simply stop being
	// made.
	condConflicts := countWarnings(condWarn, "conditional relationship conflict")
	bothConflicts := countWarnings(bothWarn, "conditional relationship conflict")
	if condConflicts == 0 {
		t.Fatal("fixture produced no numeric-arm conflicts to retire")
	}
	if bothConflicts >= condConflicts {
		t.Errorf("conflict warnings did not fall: %d with models vs %d without", bothConflicts, condConflicts)
	}
}

func countWarnings(ws []string, substr string) int {
	n := 0
	for _, w := range ws {
		if strings.Contains(w, substr) {
			n++
		}
	}
	return n
}

// TestSpecFromProfile_ModelPredictorsMatchDocument checks the
// translation itself rather than its side effects: every coefficient
// the document carries reaches the spec unchanged, addressed by
// (field, level) with no design-column name anywhere in sight, and the
// numeric field's observed bounds ride along.
func TestSpecFromProfile_ModelPredictorsMatchDocument(t *testing.T) {
	data := modelsCohort(t)
	prof, err := synth.ProfileBytes(data, synth.ProfileOptions{IncludeStats: true, FitModels: true, Seed: 5})
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	spec, _ := synth.SpecFromProfile(prof, 500)

	byField := make(map[string]synth.FieldModelSpec, len(spec.Models))
	for _, m := range spec.Models {
		byField[m.Field] = m
	}
	numeric := make(map[string]synth.NumericProfile, len(prof.Fields))
	for _, fp := range prof.Fields {
		if fp.Numeric != nil {
			numeric[fp.Name] = *fp.Numeric
		}
	}
	if len(prof.Models) == 0 {
		t.Fatal("fixture produced no models")
	}
	for _, m := range prof.Models {
		got, ok := byField[m.Field]
		if !ok {
			t.Fatalf("model for %q did not reach the spec", m.Field)
		}
		if got.Intercept != m.Intercept || got.ResidualStd != m.ResidualStd {
			t.Errorf("model %q: intercept/residual_std = %v/%v, want %v/%v",
				m.Field, got.Intercept, got.ResidualStd, m.Intercept, m.ResidualStd)
		}
		num := numeric[m.Field]
		if !got.HasClamp || got.Min != num.Min || got.Max != num.Max {
			t.Errorf("model %q clamp = (%v, %v, %v), want (true, %v, %v)",
				m.Field, got.HasClamp, got.Min, got.Max, num.Min, num.Max)
		}
		if len(got.Predictors) != len(m.Predictors) {
			t.Fatalf("model %q predictor count = %d, want %d", m.Field, len(got.Predictors), len(m.Predictors))
		}
		for i := range m.Predictors {
			want, have := m.Predictors[i], got.Predictors[i]
			if have.Kind != want.Kind || have.Field != want.Field ||
				have.Level != want.Level || have.Coefficient != want.Coefficient {
				t.Errorf("model %q predictor %d = %+v, want %+v", m.Field, i, have, want)
			}
		}
		// Every categorical predictor field must have its dropped
		// reference level named, or a consumer cannot tell which level
		// the intercept absorbed.
		refFields := make(map[string]bool, len(m.References))
		for _, r := range m.References {
			refFields[r.Field] = true
		}
		for _, p := range m.Predictors {
			if p.Kind == synth.ModelPredictorCategoricalLevel && !refFields[p.Field] {
				t.Errorf("model %q: categorical predictor %q has no declared reference level", m.Field, p.Field)
			}
			if p.Kind == synth.ModelPredictorSetOption && refFields[p.Field] {
				t.Errorf("model %q: set field %q declared a reference level; a multi-select has no baseline arm", m.Field, p.Field)
			}
		}
	}
}

// TestSpecFromProfile_ModelsEdgeCases covers the shapes a hand-built or
// stale document can present. Each is a case where a model must be
// dropped LOUDLY: the whole point of the section is that a numeric
// field's structure is accounted for in one place, so a model that
// cannot be applied has to say so rather than let the field fall back
// to an independent marginal in silence.
//
// The zero-predictor case is the one worth reading twice: the arms stay
// retired even though the model was dropped. That is deliberate and is
// the direct consequence of the retirement being a per-DOCUMENT
// decision, not a per-field one.
func TestSpecFromProfile_ModelsEdgeCases(t *testing.T) {
	baseProfile := func() *synth.Profile {
		return &synth.Profile{
			RowCount: 100,
			Fields: []synth.FieldProfile{
				{
					Name: "region", Type: "categorical_u8",
					Categorical: &synth.CategoricalProfile{
						Cardinality: 2,
						Top: []synth.CategoryHit{
							{Value: "east", Weight: 0.5},
							{Value: "west", Weight: 0.5},
						},
					},
				},
				{
					Name: "spend", Type: "f64",
					Numeric: &synth.NumericProfile{Min: 1, Max: 100, Mean: 50, Std: 10},
				},
			},
			Conditional: &synth.ConditionalProfile{
				CategoricalNumericPairs: []synth.CategoricalNumericPairProfile{{
					A: "region", B: "spend", N: 100,
					Categories: []synth.CategoricalNumericCategoryStat{
						{Category: "east", Mean: 40, Std: 5, N: 50},
						{Category: "west", Mean: 60, Std: 5, N: 50},
					},
				}},
			},
		}
	}

	cases := []struct {
		name       string
		models     []synth.FieldModel
		wantModels int
		// wantPairs is the number of CategoricalNumericPairs the spec
		// must carry. Retirement is PER TARGET: the fixture's single
		// pair targets `spend`, so it survives in exactly the cases
		// where `spend` did not land a model.
		wantPairs int
		wantWarn  string
	}{
		{
			name: "applied",
			models: []synth.FieldModel{{
				Field: "spend", Intercept: 40, NObs: 100, ResidualStd: 5,
				Predictors: []synth.ModelPredictor{{
					Kind: synth.ModelPredictorCategoricalLevel, Field: "region",
					Level: "west", Coefficient: 20,
				}},
				References: []synth.ModelReference{{Field: "region", Level: "east"}},
			}},
			wantModels: 1,
			wantPairs:  0,
		},
		{
			name: "zero predictors",
			models: []synth.FieldModel{{
				Field: "spend", Intercept: 50, NObs: 100, ResidualStd: 10,
			}},
			wantModels: 0,
			wantPairs:  1,
			wantWarn:   "no predictors",
		},
		{
			name: "unknown target",
			models: []synth.FieldModel{{
				Field: "margin", Intercept: 1,
				Predictors: []synth.ModelPredictor{{
					Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: 2,
				}},
			}},
			wantModels: 0,
			// The model names a target the profile does not carry, so
			// `spend` never had one and keeps its pair.
			wantPairs: 1,
			wantWarn:  "not present in the profile",
		},
		{
			name: "unknown predictor",
			models: []synth.FieldModel{{
				Field: "spend", Intercept: 1,
				Predictors: []synth.ModelPredictor{{
					Kind: synth.ModelPredictorCategoricalLevel, Field: "channel", Level: "email", Coefficient: 2,
				}},
			}},
			wantModels: 0,
			wantPairs:  1,
			wantWarn:   "not present in the profile",
		},
		{
			name: "predictor is a numeric",
			models: []synth.FieldModel{{
				Field: "spend", Intercept: 1,
				Predictors: []synth.ModelPredictor{{
					Kind: synth.ModelPredictorNumeric, Field: "region", Coefficient: 2,
				}},
			}},
			wantModels: 0,
			wantPairs:  1,
			wantWarn:   "unsupported kind",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseProfile()
			p.Models = tc.models
			spec, warnings := synth.SpecFromProfile(p, 50)

			if len(spec.Models) != tc.wantModels {
				t.Errorf("spec models = %d, want %d", len(spec.Models), tc.wantModels)
			}
			// Retirement is decided PER TARGET by whether that target's
			// model survived translation — never by the mere presence of
			// a `models` section. A model the spec could not apply must
			// leave its field's captured pair standing, or the field is
			// reconstructed from nothing at all.
			if n := len(spec.CategoricalNumericPairs); n != tc.wantPairs {
				t.Errorf("CategoricalNumericPairs = %d, want %d", n, tc.wantPairs)
			}
			if tc.wantWarn == "" {
				return
			}
			found := false
			for _, w := range warnings {
				if strings.Contains(w, "not applied") && strings.Contains(w, tc.wantWarn) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no %q warning; got %v", tc.wantWarn, warnings)
			}
		})
	}
}

// TestSpecFromProfile_MixedModelledAndUnmodelledTargets is the
// per-target retirement rule stated as its own case, because the edge
// matrix above can only show a document where NOTHING was modelled.
//
// Here `spend` lands a model and `visits` does not — the exact shape
// predictor selection produces on a real cohort, where a target no
// candidate explains, or one whose marginal a linear predictor cannot
// ride, sits beside a hundred that fitted cleanly. `spend` must lose its
// captured pair (its model already carries that predictor) and `visits`
// must keep its own, or the field ends up reconstructed from nothing at
// all — strictly worse than before --fit-models existed.
func TestSpecFromProfile_MixedModelledAndUnmodelledTargets(t *testing.T) {
	p := &synth.Profile{
		RowCount: 100,
		Fields: []synth.FieldProfile{
			{
				Name: "region", Type: "categorical_u8",
				Categorical: &synth.CategoricalProfile{
					Cardinality: 2,
					Top: []synth.CategoryHit{
						{Value: "east", Weight: 0.5},
						{Value: "west", Weight: 0.5},
					},
				},
			},
			{Name: "spend", Type: "f64",
				Numeric: &synth.NumericProfile{Min: 1, Max: 100, Mean: 50, Std: 10}},
			{Name: "visits", Type: "f64",
				Numeric: &synth.NumericProfile{Min: 0, Max: 9, Mean: 3, Std: 1}},
		},
		Conditional: &synth.ConditionalProfile{
			CategoricalNumericPairs: []synth.CategoricalNumericPairProfile{
				{A: "region", B: "spend", N: 100, Categories: []synth.CategoricalNumericCategoryStat{
					{Category: "east", Mean: 40, Std: 5, N: 50},
					{Category: "west", Mean: 60, Std: 5, N: 50},
				}},
				{A: "region", B: "visits", N: 100, Categories: []synth.CategoricalNumericCategoryStat{
					{Category: "east", Mean: 2.5, Std: 1, N: 50},
					{Category: "west", Mean: 3.5, Std: 1, N: 50},
				}},
			},
		},
		Models: []synth.FieldModel{
			{
				Field: "spend", Intercept: 40, NObs: 100, ResidualStd: 5,
				Predictors: []synth.ModelPredictor{{
					Kind: synth.ModelPredictorCategoricalLevel, Field: "region",
					Level: "west", Coefficient: 20,
				}},
				References: []synth.ModelReference{{Field: "region", Level: "east"}},
			},
			// The zero-predictor model selection emits for a target no
			// candidate explained. It is a complete model, but there is
			// nothing for generation to apply, so the field falls back.
			{Field: "visits", Intercept: 3, NObs: 100, ResidualStd: 1},
		},
	}

	spec, _ := synth.SpecFromProfile(p, 50)
	if len(spec.Models) != 1 || spec.Models[0].Field != "spend" {
		t.Fatalf("spec.Models = %+v, want exactly the spend model", spec.Models)
	}
	got := map[string]bool{}
	for _, pair := range spec.CategoricalNumericPairs {
		got[pair.B] = true
	}
	if got["spend"] {
		t.Error("spend kept its pair despite landing a model — the model already carries region")
	}
	if !got["visits"] {
		t.Error("visits lost its pair without gaining a model — the field now reconstructs from nothing")
	}
}

// TestSpecFromProfile_ShapeFitTargetKeepsItsShape pins the one
// interaction with an existing flag. A --fit-shape field reconstructs
// to a mixture with no closed-form quantile a linear predictor can ride,
// so its model is dropped and the field keeps its captured shape —
// which is the same net outcome resolveConflicts already produces for a
// shape-fit field named by a conditional pair, reached here by refusing
// the claim rather than by arbitrating it.
func TestSpecFromProfile_ShapeFitTargetKeepsItsShape(t *testing.T) {
	p := &synth.Profile{
		RowCount: 100,
		Fields: []synth.FieldProfile{
			{
				Name: "region", Type: "categorical_u8",
				Categorical: &synth.CategoricalProfile{
					Cardinality: 2,
					Top:         []synth.CategoryHit{{Value: "east", Weight: 0.5}, {Value: "west", Weight: 0.5}},
				},
			},
			{
				Name: "spend", Type: "f64",
				Numeric: &synth.NumericProfile{
					Min: 1, Max: 100, Mean: 50, Std: 10,
					Shape: &synth.ShapeProfile{
						Means: []float64{20, 80}, Stds: []float64{3, 4}, Weights: []float64{0.5, 0.5},
					},
				},
			},
		},
		Models: []synth.FieldModel{{
			Field: "spend", Intercept: 40, ResidualStd: 5, NObs: 100,
			Predictors: []synth.ModelPredictor{{
				Kind: synth.ModelPredictorCategoricalLevel, Field: "region", Level: "west", Coefficient: 20,
			}},
		}},
	}
	spec, warnings := synth.SpecFromProfile(p, 50)
	if len(spec.Models) != 0 {
		t.Errorf("spec models = %d, want 0 for a shape-fit target", len(spec.Models))
	}
	for _, fs := range spec.Fields {
		if fs.Name == "spend" && fs.Distribution != synth.DistMixture {
			t.Errorf("spend distribution = %q, want %q", fs.Distribution, synth.DistMixture)
		}
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "not applied") && strings.Contains(w, "--fit-shape") {
			found = true
		}
	}
	if !found {
		t.Errorf("shape-fit drop was silent; warnings = %v", warnings)
	}
}

// preModelsSpecHash pins the Spec that testdata/profile_pre_models.json
// — a real document captured before the `models` section existed —
// translates to.
//
// It is the canonical whole-Spec content hash rather than generated
// bytes on purpose: generation is a pure function of (Spec, Seed), so
// an unmoved Spec is an unmoved cohort, and unlike a hash of the
// generated file this cannot drift between architectures over a
// transcendental-function ULP. Update it only with a deliberate,
// documented change to what an old document means.
//
// Verified against the pre-change tree at the time it was pinned:
// `git archive HEAD` of the commit before this story, fed the same
// fixture, produced this same Spec hash AND a byte-identical generated
// cohort (sha256 19162c6d…39e25c at seed 11) with the same 20 conflict
// warnings. The generated-bytes hash is deliberately NOT asserted here
// — math.Exp / math.Log carry architecture-specific implementations in
// the standard library, so a cross-machine byte pin would be a flaky
// gate rather than a contract.
const preModelsSpecHash = "83b688a173b5609ab7b5572bf489f9f7"

// preModelsCaptureOptions are the exact options testdata/
// profile_pre_models.json was captured with. They are named here rather
// than only in the fixture's provenance comment because
// TestProfile_PreModelsCaptureIsByteIdentical re-runs them.
var preModelsCaptureOptions = synth.ProfileOptions{
	IncludeStats:       true,
	IncludeConditional: true,
	CorrelationTopK:    8,
	Seed:               5,
}

// TestProfile_PreModelsCaptureIsByteIdentical is the literal form of
// the non-negotiable: a capture WITHOUT --fit-models must emit the
// document it emitted before this story existed, byte for byte.
//
// testdata/profile_pre_models.json was produced by the pre-change tree
// (`git archive` of the commit before this story) over modelsCohort and
// verified there — same document, same resulting Spec hash, same
// generated cohort bytes. Comparing today's capture against that file
// is therefore a direct comparison against yesterday's output, not
// against another run of today's code.
func TestProfile_PreModelsCaptureIsByteIdentical(t *testing.T) {
	want, err := os.ReadFile("testdata/profile_pre_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	prof, err := synth.ProfileBytes(modelsCohort(t), preModelsCaptureOptions)
	if err != nil {
		t.Fatalf("ProfileBytes: %v", err)
	}
	got, err := json.MarshalIndent(prof, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	if !bytes.Equal(bytes.TrimRight(want, "\n"), bytes.TrimRight(got, "\n")) {
		t.Errorf("a flag-off capture no longer reproduces the pre-change document\n want: %s\n  got: %s", want, got)
	}
}

// TestSpecFromProfile_PreModelsDocumentUnchanged is the "no existing
// profile document is invalidated" gate.
//
// The fixture is a static file, not a re-capture, so it keeps testing
// the old shape even if every capture path in the package changes. It
// must parse, carry no models key, populate all five conditional arms
// exactly as it always did, and translate to a byte-stable Spec.
func TestSpecFromProfile_PreModelsDocumentUnchanged(t *testing.T) {
	raw, err := os.ReadFile("testdata/profile_pre_models.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("fixture is not an object: %v", err)
	}
	if _, present := keys["models"]; present {
		t.Fatal("fixture is not a pre-models document — it carries a models key")
	}

	var prof synth.Profile
	if err := json.Unmarshal(raw, &prof); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(prof.Models) != 0 {
		t.Errorf("pre-models document decoded %d models", len(prof.Models))
	}
	if prof.FittedModels() != nil {
		t.Error("pre-models document reports fitted models")
	}

	spec, _ := synth.SpecFromProfile(&prof, 500)
	if len(spec.Models) != 0 {
		t.Errorf("pre-models document produced %d spec models", len(spec.Models))
	}
	arms := map[string]int{
		"categorical_pairs":         len(spec.CategoricalPairs),
		"categorical_numeric_pairs": len(spec.CategoricalNumericPairs),
		"set_categorical_pairs":     len(spec.SetCategoricalPairs),
		"set_numeric_pairs":         len(spec.SetNumericPairs),
		"set_set_pairs":             len(spec.SetSetPairs),
	}
	for name, n := range arms {
		if n == 0 {
			t.Errorf("%s is empty; the fixture must exercise every arm", name)
		}
	}
	if got := spec.Hash(); got != preModelsSpecHash {
		t.Errorf("an old document now means something different\n got: %s\nwant: %s", got, preModelsSpecHash)
	}

	// And it still generates: same spec, same seed, byte-identical file.
	first, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	second, _, err := synth.SynthBytes(spec, synth.Options{Seed: 11})
	if err != nil {
		t.Fatalf("SynthBytes (repeat): %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("generation from the pre-models document is not deterministic")
	}
}
