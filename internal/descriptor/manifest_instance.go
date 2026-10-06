package descriptor

import (
	"reflect"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/buildinfo"
)

// BuildManifestForInstance constructs the Manifest of ONE instance: only
// what inst offers. A nil or unscoped instance yields the full registry
// (plus inst's extension projection), so the profile-free view is
// byte-identical to BuildManifest apart from the extensions it carries.
//
// On a scoped instance:
//
//   - components, tests / post_tests, regressions, overlays,
//     components_schemas and cohort_types[].compatible_* list only
//     enabled operators; extensions lists only visible registrations
//     (pulse.New already dropped the hidden ones);
//   - commands / operations are filtered through the command binding
//     table and mcp_tools through the MCP tool binding table;
//   - the facet, process_chain, join, crosstab, export and import blocks
//     are OMITTED when their capability is hidden, and the operator /
//     format lists inside a present block are filtered;
//   - synth_distributions is empty when capability:synth is hidden;
//   - every remaining prose string (descriptions, hints, rule lists) is
//     scrubbed of sentences naming a hidden operator or MCP tool.
//
// skills and the examples count / categories / tags follow the
// instance's Discovery prune (a skill or example for a hidden surface is
// absent) but are not prose-scrubbed; the error-code lists are filtered
// in assembleManifest.
func BuildManifestForInstance(inst *InstanceSnapshot) *descriptor.Manifest {
	m := assembleManifest(inst, inst.Enabled)
	hidden := hiddenProseNames(inst)
	if len(hidden) == 0 {
		return m
	}
	return scrubManifest(m, hidden)
}

// manifestDigest is the feature_set_digest the manifest carries. A
// scoped instance carries its own; a nil / unscoped one carries the
// digest a profile-free pulse.New with the same extension operators
// computes (every reached built-in plus the extension operator names,
// no behaviour switch set), so the CLI's full manifest and a default
// instance's manifest agree.
func manifestDigest(inst *InstanceSnapshot) string {
	if inst.Scoped() {
		return inst.Digest()
	}
	names := ReachedFeatureNames(buildinfo.Version())
	if ext := inst.Extensions(); ext != nil {
		for _, metas := range [][]descriptor.OperatorMeta{
			ext.Aggregators, ext.Attributes, ext.Filterers, ext.Groupers,
			ext.Windows, ext.Features, ext.Tests,
		} {
			for _, m := range metas {
				names = append(names, m.Name)
			}
		}
	}
	return FeatureSetDigest(names, FeatureBehaviour{})
}

// filterSlice keeps the elements keep accepts, in order, in a fresh
// slice. A nil input stays nil so an absent list keeps marshalling as
// it did.
func filterSlice[T any](in []T, keep func(T) bool) []T {
	if in == nil {
		return nil
	}
	out := make([]T, 0, len(in))
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func filterNames(in []string, on func(string) bool) []string {
	return filterSlice(in, on)
}

func filterOps(in []descriptor.Operator, on func(string) bool) []descriptor.Operator {
	return filterSlice(in, func(o descriptor.Operator) bool { return on(o.Name) })
}

// filterTests keeps the tier-1 / tier-2 entries whose TEST_* family is
// enabled: one feature row covers both tiers of a test.
func filterTests(in []descriptor.TestMeta, on func(string) bool) []descriptor.TestMeta {
	return filterSlice(in, func(t descriptor.TestMeta) bool {
		if t.Family != "" {
			return on(t.Family)
		}
		return on(t.Name)
	})
}

func filterMatrices(in []descriptor.MatrixMeta, on func(string) bool) []descriptor.MatrixMeta {
	return filterSlice(in, func(m descriptor.MatrixMeta) bool { return on(m.Name) })
}

func filterRegressions(in []descriptor.RegressionMeta, on func(string) bool) []descriptor.RegressionMeta {
	return filterSlice(in, func(r descriptor.RegressionMeta) bool { return on(r.Name) })
}

func filterOverlays(in []descriptor.OverlayCapability, on func(string) bool) []descriptor.OverlayCapability {
	return filterSlice(in, func(o descriptor.OverlayCapability) bool { return on(string(o.Kind)) })
}

// filterCommands keeps a command / operation unless its binding names a
// feature the instance does not offer. Core and ungated bindings always
// stay (TestCommandBindings_Complete keeps every entry bound).
func filterCommands(in []descriptor.Command, on func(string) bool) []descriptor.Command {
	return filterSlice(in, func(c descriptor.Command) bool {
		b, ok := CommandBindingOf(c.Name)
		return !ok || b.Feature == "" || on(b.Feature)
	})
}

// filterMCPTools keeps a tool unless its binding names a feature the
// instance does not offer.
func filterMCPTools(in []descriptor.MCPTool, on func(string) bool) []descriptor.MCPTool {
	return filterSlice(in, func(t descriptor.MCPTool) bool {
		b, ok := MCPToolBindingOf(t.Name)
		return !ok || b.Feature == "" || on(b.Feature)
	})
}

// hiddenProseNames is the token set the prose scrub removes: every
// hidden operator name (kind-prefixed capability / io_format / mcp_extra
// spellings never appear in prose as such) plus every MCP tool whose
// owning feature is not enabled. Empty on a nil / unscoped instance.
func hiddenProseNames(inst *InstanceSnapshot) map[string]struct{} {
	if !inst.Scoped() {
		return nil
	}
	out := map[string]struct{}{}
	for _, n := range inst.HiddenNames() {
		if !strings.Contains(n, ":") {
			out[n] = struct{}{}
		}
	}
	for _, b := range mcpToolBindings {
		if b.Feature != "" && !inst.Enabled(b.Feature) {
			out[b.Tool] = struct{}{}
		}
	}
	return out
}

// manifestScrubSkip names the top-level Manifest fields the prose scrub
// leaves untouched: skills and examples are rendered by the instance's
// Discovery (visibleSkills, ExampleStats), the error lists are filtered
// by their own rule.
var manifestScrubSkip = map[string]bool{
	"Skills":            true,
	"ExamplesCount":     true,
	"ExampleCategories": true,
	"ExampleTags":       true,
	"ErrorCodesCount":   true,
	"ErrorDomains":      true,
	"ErrorCodes":        true,
	// Intent IDs name no operator; the instance ontology prunes them
	// (instanceIntentIDs).
	"Intents": true,
}

// scrubManifest returns a deep copy of m in which no string outside the
// skipped fields names a hidden token: a []string element (or map key)
// that IS a hidden name is dropped, and every other string loses the
// sentences that mention one (an element left empty is dropped too).
// m itself — which shares backing arrays with the capability tables —
// is never written.
func scrubManifest(m *descriptor.Manifest, hidden map[string]struct{}) *descriptor.Manifest {
	src := reflect.ValueOf(m).Elem()
	out := reflect.New(src.Type()).Elem()
	for i := range src.NumField() {
		if manifestScrubSkip[src.Type().Field(i).Name] {
			out.Field(i).Set(src.Field(i))
			continue
		}
		out.Field(i).Set(scrubValue(src.Field(i), hidden))
	}
	return out.Addr().Interface().(*descriptor.Manifest)
}

func scrubValue(v reflect.Value, hidden map[string]struct{}) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(scrubValue(v.Elem(), hidden))
		return out
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				return v // not a plain data struct: copy as is
			}
		}
		out := reflect.New(v.Type()).Elem()
		for i := range v.NumField() {
			out.Field(i).Set(scrubValue(v.Field(i), hidden))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), 0, v.Len())
		for i := range v.Len() {
			e := v.Index(i)
			if e.Kind() == reflect.String {
				s := e.String()
				if _, h := hidden[s]; h {
					continue
				}
				r := redactProse(s, hidden)
				if r == "" && s != "" {
					continue
				}
				ne := reflect.New(e.Type()).Elem()
				ne.SetString(r)
				out = reflect.Append(out, ne)
				continue
			}
			out = reflect.Append(out, scrubValue(e, hidden))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			if k.Kind() == reflect.String {
				if _, h := hidden[k.String()]; h {
					continue
				}
			}
			out.SetMapIndex(k, scrubValue(iter.Value(), hidden))
		}
		return out
	case reflect.String:
		out := reflect.New(v.Type()).Elem()
		out.SetString(redactProse(v.String(), hidden))
		return out
	default:
		return v
	}
}

// redactProse drops every sentence of s that mentions a hidden token. A
// token is a maximal run of [A-Za-z0-9_], so OVERLAY_INDEX_VS_REF does
// not match inside OVERLAY_PANEL_INDEX_VS_REF and a family wildcard such
// as FILTER_SET_* names no single operator. Sentences end at ". ".
// A string mentioning no hidden token is returned unchanged.
func redactProse(s string, hidden map[string]struct{}) string {
	if !mentionsHidden(s, hidden) {
		return s
	}
	var kept []string
	for _, sentence := range splitSentences(s) {
		if !mentionsHidden(sentence, hidden) {
			kept = append(kept, sentence)
		}
	}
	return strings.Join(kept, " ")
}

func splitSentences(s string) []string {
	var out []string
	start := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' {
			if seg := strings.TrimSpace(s[start : i+1]); seg != "" {
				out = append(out, seg)
			}
			start = i + 2
		}
	}
	if seg := strings.TrimSpace(s[start:]); seg != "" {
		out = append(out, seg)
	}
	return out
}

func mentionsHidden(s string, hidden map[string]struct{}) bool {
	start := -1
	for i := 0; i <= len(s); i++ {
		word := i < len(s) && isTokenByte(s[i])
		switch {
		case word && start < 0:
			start = i
		case !word && start >= 0:
			if _, h := hidden[s[start:i]]; h {
				return true
			}
			start = -1
		}
	}
	return false
}

func isTokenByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}
