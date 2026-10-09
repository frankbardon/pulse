package descriptor

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/descriptor"
)

// Guidance prose (Purpose, Interpretation, glossary and intent text) is
// served only on demand — pulse.Glossary / pulse.Intents, the glossary
// and intents virtual skills — and never inlined into a default
// payload. The budget gates (TestManifestGuidanceBudget here, its
// runtime twin in the root package) enumerate every declared prose
// string through GuidanceProse and assert none appears in a default
// manifest, Response or PredictResult.

// GuidanceProseMinLen is the shortest prose string (in runes) the prose
// ban checks. Shorter strings — intent labels such as "Describe data",
// band labels such as "small" — are generic phrases that legitimately
// collide with ordinary manifest descriptions, so banning them would
// flag coincidences rather than leaked guidance.
const GuidanceProseMinLen = 16

// ProseString is one declared guidance prose string and where it came
// from (e.g. "purpose:AGG_AVERAGE", "glossary:p-value").
type ProseString struct {
	Source string
	Text   string
}

// proseSource feeds one guidance registry into GuidanceProse. typ is the
// guidance type the registry holds, so TestGuidanceProseSourcesComplete
// can prove every guidance type is swept.
//
// The Interpretation registry feeds two sources: per-operator entries
// keyed "interpretation:<operator>.<field>" and the shared rule sets
// keyed "interpretation-shared:<key>".
type proseSource struct {
	typ    reflect.Type
	values func() map[string]any
}

var proseSources = []proseSource{
	{reflect.TypeOf(descriptor.Purpose{}), func() map[string]any {
		out := map[string]any{}
		for k, p := range BuiltinPurposes() {
			out["purpose:"+k] = p
		}
		return out
	}},
	{reflect.TypeOf(descriptor.Interpretation{}), func() map[string]any {
		out := map[string]any{}
		for name, ins := range BuiltinInterpretations() {
			for _, in := range ins {
				out["interpretation:"+name+"."+in.Field] = in
			}
		}
		return out
	}},
	{reflect.TypeOf(descriptor.Interpretation{}), func() map[string]any {
		out := map[string]any{}
		for _, k := range SharedInterpretationKeys() {
			in, _ := SharedInterpretation(k)
			out["interpretation-shared:"+k] = in
		}
		return out
	}},
	{reflect.TypeOf(descriptor.Term{}), func() map[string]any {
		out := map[string]any{}
		for _, t := range Glossary() {
			out["glossary:"+t.ID] = t
		}
		return out
	}},
	{reflect.TypeOf(descriptor.Intent{}), func() map[string]any {
		out := map[string]any{}
		for _, in := range Intents() {
			out["intent:"+in.ID] = in
		}
		return out
	}},
}

// proseIdentifierFields are guidance struct fields holding identifiers,
// enums or surface spellings rather than prose; the walker skips them.
// Every other string reachable from a guidance value is prose, so a new
// prose field is swept with no change here.
var proseIdentifierFields = map[string]bool{
	"ID": true, "Intents": true, "Use": true, "Glossary": true,
	"SeeAlso": true, "Forms": true, "Field": true, "Shared": true,
	"Level": true, "Name": true, "Kinds": true, "KnownAs": true,
}

// GuidanceProse returns every declared guidance prose string of at
// least GuidanceProseMinLen runes, sorted by source then text.
func GuidanceProse() []ProseString {
	var out []ProseString
	for _, src := range proseSources {
		for key, v := range src.values() {
			for _, s := range CollectProse(v) {
				if utf8.RuneCountInString(s) >= GuidanceProseMinLen {
					out = append(out, ProseString{Source: key, Text: s})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Text < out[j].Text
	})
	return out
}

// CollectProse returns every prose string reachable from v — string
// fields, slice elements and map values — skipping identifier fields
// (proseIdentifierFields). Map keys are never prose. Order follows
// declaration order with map values sorted.
func CollectProse(v any) []string {
	var out []string
	walkProse(reflect.ValueOf(v), &out)
	return out
}

func walkProse(v reflect.Value, out *[]string) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			walkProse(v.Elem(), out)
		}
	case reflect.String:
		if v.String() != "" {
			*out = append(*out, v.String())
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			walkProse(v.Index(i), out)
		}
	case reflect.Map:
		var vals []string
		for _, k := range v.MapKeys() {
			var sub []string
			walkProse(v.MapIndex(k), &sub)
			vals = append(vals, sub...)
		}
		sort.Strings(vals)
		*out = append(*out, vals...)
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() || proseIdentifierFields[f.Name] {
				continue
			}
			walkProse(v.Field(i), out)
		}
	}
}

// LeakedGuidanceProse returns every GuidanceProse string that appears
// in payload, either verbatim or in its JSON-escaped spelling (so a
// string carrying an apostrophe, '<' or '&' is still caught after
// encoding/json escaped it).
func LeakedGuidanceProse(payload []byte) []ProseString {
	body := string(payload)
	var out []ProseString
	for _, p := range GuidanceProse() {
		if strings.Contains(body, p.Text) {
			out = append(out, p)
			continue
		}
		enc, err := json.Marshal(p.Text)
		if err == nil && strings.Contains(body, string(enc[1:len(enc)-1])) {
			out = append(out, p)
		}
	}
	return out
}
