package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestErrorOwners_Complete: the owner table classifies every registered
// error code exactly once, names nothing else, and every owner resolves
// to a built-in feature (or is the lone shared sentinel).
func TestErrorOwners_Complete(t *testing.T) {
	registered := map[errors.Code]bool{}
	for _, c := range errors.AllCodes() {
		registered[c] = true
		if _, ok := errorOwners[c]; !ok {
			t.Errorf("error code %s has no owner classification in internal/descriptor/error_owners.go (errorOwners): add it — `shared` when it can arise on a core path", c)
		}
	}
	for c, owners := range errorOwners {
		if !registered[c] {
			t.Errorf("errorOwners classifies %s, which is not in errors.AllCodes()", c)
		}
		if len(owners) == 0 {
			t.Errorf("%s has an empty owner list", c)
			continue
		}
		if slices.Contains(owners, errorOwnerShared) {
			if len(owners) != 1 {
				t.Errorf("%s mixes the shared sentinel with feature owners %v", c, owners)
			}
			continue
		}
		seen := map[string]bool{}
		for _, o := range owners {
			if seen[o] {
				t.Errorf("%s lists owner %q twice", c, o)
			}
			seen[o] = true
			if _, ok := LookupFeature(o); !ok {
				t.Errorf("%s owner %q is not a built-in feature", c, o)
			}
		}
	}
}

// TestErrorOwners_FeatureProfileCodesShared: the feature-profile codes
// stay listed on every instance, the empty one included.
func TestErrorOwners_FeatureProfileCodesShared(t *testing.T) {
	empty := NewInstanceSnapshot(nil, FeatureSet{Hidden: FeatureNames()})
	for _, c := range errors.AllCodes() {
		if strings.HasPrefix(string(c), "PULSE_FEATURE_PROFILE_") && !ErrorCodeVisible(empty, c) {
			t.Errorf("%s is hidden on the empty instance", c)
		}
	}
}

// impliedFeatures is every feature necessarily enabled whenever f is:
// f itself plus the closure over its single-name dependency groups (a
// validated profile cannot enable f without them).
func impliedFeatures(f string) map[string]bool {
	out := map[string]bool{f: true}
	queue := []string{f}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		groups, _ := FeatureDependencies(n)
		for _, g := range groups {
			if len(g) == 1 && !out[g[0]] {
				out[g[0]] = true
				queue = append(queue, g[0])
			}
		}
	}
	return out
}

// proseFeatureTokens maps every token that names a feature in prose to
// that feature: bare operator names and the MCP tools bound to a feature.
func proseFeatureTokens() map[string]string {
	out := map[string]string{}
	for _, n := range FeatureNames() {
		if !strings.Contains(n, ":") {
			out[n] = n
		}
	}
	for _, b := range MCPToolBindings() {
		if b.Feature != "" {
			out[b.Tool] = b.Feature
		}
	}
	return out
}

func namedTokens(s string, tokens map[string]string) []string {
	var out []string
	start := -1
	for i := 0; i <= len(s); i++ {
		word := i < len(s) && isTokenByte(s[i])
		switch {
		case word && start < 0:
			start = i
		case !word && start >= 0:
			if _, ok := tokens[s[start:i]]; ok {
				out = append(out, s[start:i])
			}
			start = -1
		}
	}
	return out
}

// TestErrorMessagesNameOnlyOwners: a code's Message (never stripped at
// render time) may name a feature only when that feature is enabled on
// every instance that lists the code — i.e. implied by EVERY owner. A
// shared code's Message names no feature at all.
func TestErrorMessagesNameOnlyOwners(t *testing.T) {
	tokens := proseFeatureTokens()
	for _, c := range errors.AllCodes() {
		m, _ := errors.MetadataFor(c)
		named := namedTokens(m.Message, tokens)
		if len(named) == 0 {
			continue
		}
		owners := errorOwners[c]
		for _, tok := range named {
			feat := tokens[tok]
			for _, o := range owners {
				if o == errorOwnerShared || !impliedFeatures(o)[feat] {
					t.Errorf("%s Message names %s, which can be hidden while owner %q keeps the code listed; reword the Message (errors/fixup_metadata.go)", c, tok, o)
					break
				}
			}
		}
	}
}

func scopedExcept(hidden ...string) *InstanceSnapshot {
	h := map[string]bool{}
	for _, n := range hidden {
		h[n] = true
	}
	var enabled []string
	for _, n := range FeatureNames() {
		if !h[n] {
			enabled = append(enabled, n)
		}
	}
	return NewInstanceSnapshot(nil, FeatureSet{Enabled: enabled, Hidden: hidden})
}

// TestErrorCodeVisible_AnyOwnerKeepsIt: an owned code is hidden iff
// every owner is hidden.
func TestErrorCodeVisible_AnyOwnerKeepsIt(t *testing.T) {
	c := errors.PULSE_TEST_PAIRED_LENGTH_MISMATCH // owners TEST_PAIRED_T, TEST_WILCOXON_SR
	if !ErrorCodeVisible(scopedExcept("TEST_PAIRED_T"), c) {
		t.Error("code hidden though TEST_WILCOXON_SR still owns it")
	}
	if ErrorCodeVisible(scopedExcept("TEST_PAIRED_T", "TEST_WILCOXON_SR"), c) {
		t.Error("code listed though every owner is hidden")
	}
	if !ErrorCodeVisible(nil, c) {
		t.Error("nil instance hides a code")
	}
	if !ErrorCodeVisible(scopedExcept(FeatureNames()...), errors.PROCESSING_CONFIG) {
		t.Error("shared code hidden on the empty instance")
	}
}

func lookupJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestErrorLookup_DefaultIsFull: a nil or unscoped instance renders the
// errors package unchanged, and a fully-enabled scoped one does too.
func TestErrorLookup_DefaultIsFull(t *testing.T) {
	full := scopedExcept()
	for _, inst := range []*InstanceSnapshot{nil, UnscopedInstanceSnapshot(&ExtensionsSnapshot{}), full} {
		for _, c := range errors.AllCodes() {
			want, _ := errors.Lookup(string(c))
			got, ok := ErrorLookup(inst, string(c))
			if !ok || !reflect.DeepEqual(got, want) {
				t.Fatalf("ErrorLookup(%s) = %+v, want %+v", c, got, want)
			}
		}
		for _, d := range errors.AllDomains() {
			if g, w := lookupJSON(t, ErrorsByDomain(inst, d)), lookupJSON(t, errors.ByDomain(d)); g != w {
				t.Errorf("ErrorsByDomain(%s) differs from errors.ByDomain", d)
			}
		}
		for _, q := range []string{"overlay", "AGG_MODE", "crosstab", "spss", "pulse_lookup"} {
			if g, w := lookupJSON(t, ErrorsSearch(inst, q)), lookupJSON(t, errors.Search(q)); g != w {
				t.Errorf("ErrorsSearch(%q) differs from errors.Search", q)
			}
		}
	}
}

// TestErrorLookup_HiddenCodeNotFound: a code whose every owner is hidden
// is not found, absent from its domain and from search.
func TestErrorLookup_HiddenCodeNotFound(t *testing.T) {
	inst := scopedExcept(FeatureName(FeatureKindIOFormat, "spss"))
	const code = "PULSE_SPSS_FILE_EMPTY"
	if _, ok := ErrorLookup(inst, code); ok {
		t.Errorf("%s found though io_format:spss is hidden", code)
	}
	for _, r := range ErrorsByDomain(inst, "PULSE") {
		if strings.HasPrefix(r.Code, "PULSE_SPSS_") {
			t.Errorf("ErrorsByDomain lists hidden %s", r.Code)
		}
	}
	if len(ErrorsByDomain(inst, "PULSE")) == 0 {
		t.Fatal("PULSE domain empty: the check above is vacuous")
	}
	for _, r := range ErrorsSearch(inst, "spss") {
		if strings.HasPrefix(r.Code, "PULSE_SPSS_") {
			t.Errorf("ErrorsSearch lists hidden %s", r.Code)
		}
	}
	if _, ok := ErrorLookup(nil, code); !ok {
		t.Fatalf("%s not found on the default instance", code)
	}
}

// TestErrorLookup_FixupStrip: a visible code keeps only the fixups that
// name no hidden feature, and a code may be left with none.
func TestErrorLookup_FixupStrip(t *testing.T) {
	full, _ := errors.Lookup("PULSE_TEST_TUKEY_REQUIRES_K_GE_3")
	if len(full.Fixups) != 1 || !strings.Contains(full.Fixups[0].Hint, "TEST_WELCH") {
		t.Fatalf("fixture drifted: %+v", full.Fixups)
	}
	inst := scopedExcept("TEST_WELCH")
	got, ok := ErrorLookup(inst, "PULSE_TEST_TUKEY_REQUIRES_K_GE_3")
	if !ok {
		t.Fatal("code hidden though TEST_TUKEY_HSD is enabled")
	}
	if got.Fixups != nil {
		t.Errorf("fixup naming hidden TEST_WELCH survived: %+v", got.Fixups)
	}
	if got.Message != full.Message {
		t.Error("Message rewritten")
	}

	// Example-only mention strips too: PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL
	// keeps its second fixup.
	got, _ = ErrorLookup(scopedExcept("AGG_MODE"), "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL")
	if len(got.Fixups) != 1 || strings.Contains(lookupJSON(t, got), "AGG_MODE") {
		t.Errorf("fixups = %+v, want only the field-swap fixup", got.Fixups)
	}
	if again, _ := errors.Lookup("PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL"); len(again.Fixups) != 2 {
		t.Error("strip wrote through to the errors package table")
	}
}

// TestErrorsSearch_NeverMatchesHiddenText: a query that only matched a
// stripped fixup finds nothing; every hit carries the query in what it
// renders.
func TestErrorsSearch_NeverMatchesHiddenText(t *testing.T) {
	const q = "TEST_WELCH"
	if len(errors.Search(q)) == 0 {
		t.Fatal("full search finds nothing: vacuous")
	}
	inst := scopedExcept(q)
	for _, r := range ErrorsSearch(inst, q) {
		if strings.Contains(lookupJSON(t, r), q) {
			t.Errorf("%s renders hidden %s", r.Code, q)
		}
		if !strings.Contains(strings.ToLower(r.Message+r.Code+lookupJSON(t, r.Fixups)), strings.ToLower(q)) {
			t.Errorf("%s matched %q on text the instance does not render", r.Code, q)
		}
	}
}

// TestManifestErrorFields_Scoped: the manifest error lists are the
// visible subset; a domain with no visible code vanishes; the full
// instance keeps them whole.
func TestManifestErrorFields_Scoped(t *testing.T) {
	m := BuildManifestForInstance(scopedExcept(FeatureNames()...))
	if m.ErrorCodesCount != len(m.ErrorCodes) {
		t.Errorf("count %d != len(codes) %d", m.ErrorCodesCount, len(m.ErrorCodes))
	}
	if m.ErrorCodesCount >= len(errors.AllCodes()) {
		t.Errorf("empty instance lists %d codes, want fewer than %d", m.ErrorCodesCount, len(errors.AllCodes()))
	}
	for _, n := range m.ErrorCodes {
		if errorOwners[errors.Code(n)][0] != errorOwnerShared {
			t.Errorf("empty instance lists owned code %s", n)
		}
	}
	if !sort.StringsAreSorted(m.ErrorCodes) {
		t.Error("codes not sorted")
	}
	if want := errorDomainsFor(m.ErrorCodes); !slices.Equal(m.ErrorDomains, want) {
		t.Errorf("domains %v, want %v", m.ErrorDomains, want)
	}

	full := BuildManifest()
	if full.ErrorCodesCount != len(errors.AllCodes()) || !slices.Equal(full.ErrorDomains, errors.AllDomains()) ||
		!slices.Equal(full.ErrorCodes, errors.SortedCodeNames()) {
		t.Error("full manifest error lists differ from the registry")
	}

	// A domain vanishes when its last code is hidden.
	if got := errorDomainsFor([]string{"PULSE_SPSS_FILE_EMPTY", "ENCODING_IO"}); !slices.Equal(got, []string{"ENCODING", "PULSE"}) {
		t.Errorf("errorDomainsFor = %v", got)
	}
	if got := errorDomainsFor([]string{"ENCODING_IO"}); !slices.Equal(got, []string{"ENCODING"}) {
		t.Errorf("errorDomainsFor = %v", got)
	}
}
