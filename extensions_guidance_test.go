package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

type guidanceStubPostTest struct{}

func (guidanceStubPostTest) Run([]map[string]any) (*types.TestResult, error) {
	return &types.TestResult{}, nil
}

func guidanceStubPostTestFactory(*types.Test, *encoding.Schema) (extend.PostTest, error) {
	return guidanceStubPostTest{}, nil
}

// validExtPurpose returns a Purpose that passes every rule; each table
// case below breaks exactly one.
func validExtPurpose() *descriptor.Purpose {
	return &descriptor.Purpose{
		Plain:   "Composite brand health score for a set of respondents.",
		Intents: []string{descx.IntentMeasureConstruct, descx.IntentDescribe},
		Questions: []string{
			"How healthy is our brand this wave?",
			"Which segment rates the brand highest?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Brand health tracker across waves.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you only need the plain average of one rating", Use: "AGG_AVERAGE"},
		},
		Level: descriptor.LevelIntermediate,
	}
}

func guidanceExt(p *descriptor.Purpose) Extensions {
	return Extensions{Aggregators: []AggregatorRegistration{
		{Name: "AGG_ACME_BRAND", Description: "Stub.", Factory: featureSetStubAggFactory, Purpose: p},
	}}
}

func purposeInvalid(t *testing.T, err error) *perrors.CodedError {
	t.Helper()
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_EXTENSION_PURPOSE_INVALID {
		t.Fatalf("err = %v, want PULSE_EXTENSION_PURPOSE_INVALID", err)
	}
	return ce
}

// TestExtensions_PurposeValidAcceptedAndProjected: a valid Purpose is
// accepted, rides the snapshot, and projects ONLY its sorted intent IDs
// onto the manifest entry; an absent Purpose projects no intents.
func TestExtensions_PurposeValidAcceptedAndProjected(t *testing.T) {
	ext := guidanceExt(validExtPurpose())
	ext.Aggregators = append(ext.Aggregators, AggregatorRegistration{
		Name: "AGG_ACME_PLAIN", Description: "Stub.", Factory: featureSetStubAggFactory,
	})
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: ext})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := p.svc.ExtensionsSnapshot().PurposeOf("AGG_ACME_BRAND"); !ok {
		t.Error("snapshot does not carry the registration's Purpose")
	}
	got := map[string][]string{}
	for _, m := range p.Manifest(context.Background()).Extensions.Aggregators {
		got[m.Name] = m.Intents
	}
	if want := []string{"describe", "measure_construct"}; !reflect.DeepEqual(got["AGG_ACME_BRAND"], want) {
		t.Errorf("AGG_ACME_BRAND intents = %v, want %v", got["AGG_ACME_BRAND"], want)
	}
	if got["AGG_ACME_PLAIN"] != nil {
		t.Errorf("AGG_ACME_PLAIN (no Purpose) intents = %v, want none", got["AGG_ACME_PLAIN"])
	}
}

// TestExtensions_PurposeInvalidRules: every validity rule, broken on an
// otherwise valid Purpose, fails pulse.New with
// PULSE_EXTENSION_PURPOSE_INVALID naming the registration and the rule.
func TestExtensions_PurposeInvalidRules(t *testing.T) {
	cases := []struct {
		rule   descx.PurposeRule
		mutate func(*descriptor.Purpose)
	}{
		{descx.PurposeRulePlain, func(p *descriptor.Purpose) { p.Plain = "" }},
		{descx.PurposeRulePlain, func(p *descriptor.Purpose) { p.Plain = strings.Repeat("x", descx.PurposePlainMax+1) }},
		{descx.PurposeRuleIntents, func(p *descriptor.Purpose) { p.Intents = nil }},
		{descx.PurposeRuleQuestions, func(p *descriptor.Purpose) { p.Questions = p.Questions[:1] }},
		{descx.PurposeRuleNotFor, func(p *descriptor.Purpose) { p.NotFor = nil }},
		{descx.PurposeRuleUseCases, func(p *descriptor.Purpose) { p.UseCases = map[descriptor.Domain]string{"retail": "x"} }},
		{descx.PurposeRuleLevel, func(p *descriptor.Purpose) { p.Level = "expert" }},
		{descx.PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "AGG_ACME_MISSING" }},
		{descx.PurposeRuleAlternative, func(p *descriptor.Purpose) { p.NotFor[0].Use = "AGG_ACME_BRAND" }},
		{descx.PurposeRuleIntentUnknown, func(p *descriptor.Purpose) { p.Intents = []string{"vibes"} }},
		{descx.PurposeRuleGlossaryUnknown, func(p *descriptor.Purpose) { p.Glossary = []string{"not-a-term"} }},
		{descx.PurposeRuleJargonUnlinked, func(p *descriptor.Purpose) { p.Plain = "Brand score with a p-value." }},
	}
	for _, tc := range cases {
		t.Run(string(tc.rule), func(t *testing.T) {
			pp := validExtPurpose()
			tc.mutate(pp)
			_, err := New(Options{FS: memFsWith(t, nil), Extensions: guidanceExt(pp)})
			ce := purposeInvalid(t, err)
			if ce.Details["name"] != "AGG_ACME_BRAND" || ce.Details["category"] != "aggregator" || ce.Details["part"] != "purpose" {
				t.Errorf("details = %v, want name AGG_ACME_BRAND, category aggregator, part purpose", ce.Details)
			}
			rules, _ := ce.Details["rules"].([]string)
			found := false
			for _, r := range rules {
				found = found || r == string(tc.rule)
			}
			if !found || ce.Details["rule"] != rules[0] {
				t.Errorf("rules = %v (rule %v), want %q among them", rules, ce.Details["rule"], tc.rule)
			}
			if !strings.Contains(ce.Message, "AGG_ACME_BRAND") {
				t.Errorf("message %q does not name the registration", ce.Message)
			}
		})
	}
}

// TestExtensions_PurposeNotForResolvesAcrossExtensions: a NotFor.Use
// naming another registered extension (any category) resolves, as does
// a "<kind>:<name>" feature spelling.
func TestExtensions_PurposeNotForResolvesAcrossExtensions(t *testing.T) {
	pp := validExtPurpose()
	pp.NotFor = []descriptor.Alternative{
		{When: "you want a significance test of the score", Use: "TEST_ACME_BRAND_GAP"},
		{When: "you are generating data", Use: "capability:synth"},
	}
	ext := guidanceExt(pp)
	ext.Tests = []TestRegistration{{Name: "TEST_ACME_BRAND_GAP", Description: "Stub.", Tier: TestTierPost, PostFactory: guidanceStubPostTestFactory}}
	if _, err := New(Options{FS: memFsWith(t, nil), Extensions: ext}); err != nil {
		t.Fatalf("New: %v", err)
	}
}

// TestExtensions_InterpretationStructureOnly: a test registration's
// Interpretation is checked for structure, not output-key existence.
func TestExtensions_InterpretationStructureOnly(t *testing.T) {
	reg := func(ins []descriptor.Interpretation) Extensions {
		return Extensions{Tests: []TestRegistration{{
			Name: "TEST_ACME_GAP", Description: "Stub.", Tier: TestTierPost,
			PostFactory: guidanceStubPostTestFactory, Interpretation: ins,
		}}}
	}
	// An undeclared map key passes: existence is never probed.
	ok := []descriptor.Interpretation{{Field: "details.acme_gap", Means: "How far apart the two brand scores are."}}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: reg(ok)})
	if err != nil {
		t.Fatalf("New (valid interpretation): %v", err)
	}
	if got := p.svc.ExtensionsSnapshot().Interpretations["TEST_ACME_GAP"]; !reflect.DeepEqual(got, ok) {
		t.Errorf("snapshot interpretations = %v, want %v", got, ok)
	}

	bad := []descriptor.Interpretation{{Field: "details.acme_gap", Means: "Gap.", Bands: []descriptor.Band{{Label: "small"}}}}
	_, err = New(Options{FS: memFsWith(t, nil), Extensions: reg(bad)})
	ce := purposeInvalid(t, err)
	if ce.Details["part"] != "interpretation" || ce.Details["name"] != "TEST_ACME_GAP" || ce.Details["rule"] != string(descx.InterpretationRuleConvention) {
		t.Errorf("details = %v, want part interpretation, name TEST_ACME_GAP, rule convention", ce.Details)
	}
}

// TestExtensions_PurposeFirstInvalidWins: with several broken
// registrations the error names the first in validation order
// (category order, then slice index) — deterministic.
func TestExtensions_PurposeFirstInvalidWins(t *testing.T) {
	broken := validExtPurpose()
	broken.Intents = nil
	ext := Extensions{
		Aggregators: []AggregatorRegistration{
			{Name: "AGG_ACME_OK", Description: "Stub.", Factory: featureSetStubAggFactory, Purpose: validExtPurpose()},
			{Name: "AGG_ACME_FIRST", Description: "Stub.", Factory: featureSetStubAggFactory, Purpose: broken},
		},
		Tests: []TestRegistration{{Name: "TEST_ACME_LATER", Description: "Stub.", Tier: TestTierPost,
			PostFactory: guidanceStubPostTestFactory, Purpose: broken}},
	}
	for i := 0; i < 5; i++ {
		_, err := New(Options{FS: memFsWith(t, nil), Extensions: ext})
		if ce := purposeInvalid(t, err); ce.Details["name"] != "AGG_ACME_FIRST" || ce.Details["index"] != 1 {
			t.Fatalf("details = %v, want AGG_ACME_FIRST at index 1", ce.Details)
		}
	}
}

// TestExtensions_HiddenExtensionDropsGuidance: an extension a feature
// profile hides takes its Purpose with it — the snapshot and manifest
// carry only the visible one's.
func TestExtensions_HiddenExtensionDropsGuidance(t *testing.T) {
	ext := guidanceExt(validExtPurpose())
	ext.Aggregators = append(ext.Aggregators, AggregatorRegistration{
		Name: "AGG_ACME_KEPT", Description: "Stub.", Factory: featureSetStubAggFactory, Purpose: validExtPurpose(),
	})
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: ext,
		FeatureProfile: &FeatureProfile{Features: []string{"capability:process", "AGG_ACME_KEPT"}}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	snap := p.svc.ExtensionsSnapshot()
	if _, ok := snap.PurposeOf("AGG_ACME_BRAND"); ok {
		t.Error("hidden extension's Purpose still in the snapshot")
	}
	if _, ok := snap.PurposeOf("AGG_ACME_KEPT"); !ok {
		t.Error("visible extension's Purpose missing from the snapshot")
	}
	raw, _ := json.Marshal(p.Manifest(context.Background()))
	if strings.Contains(string(raw), "AGG_ACME_BRAND") {
		t.Error("hidden extension leaks into the manifest")
	}
}

// TestExtensions_PurposeProseNeverInManifest: only an extension
// Purpose's intent IDs reach the manifest — none of its prose.
func TestExtensions_PurposeProseNeverInManifest(t *testing.T) {
	pp := validExtPurpose()
	pp.Assumptions = []string{"Respondents answered every brand item."}
	p, err := New(Options{FS: memFsWith(t, nil), Extensions: guidanceExt(pp)})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	raw, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	prose := descx.CollectProse(*pp)
	if len(prose) == 0 {
		t.Fatal("fixture has no prose to check")
	}
	for _, s := range prose {
		if utf8.RuneCountInString(s) < descx.GuidanceProseMinLen {
			continue
		}
		if strings.Contains(body, s) {
			t.Errorf("extension Purpose prose %q leaked into the manifest", s)
		}
	}
	if !strings.Contains(body, `"intents":["describe","measure_construct"]`) {
		t.Error("extension intents not projected into the manifest")
	}
}
