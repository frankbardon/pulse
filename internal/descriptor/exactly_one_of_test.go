package descriptor

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// exactlyOneOfSamples is a valid value for every param a built-in
// declares in an exactly_one_of group, so the parity gate can supply
// each member alone. A newly grouped param fails the gate until it has
// one here.
var exactlyOneOfSamples = map[string]map[string]any{
	string(types.WIN_EWMA):       {"alpha": 0.3, "decay": 0.7},
	string(types.FEAT_BUCKETIZE): {"boundaries": []float64{10, 20}, "quantiles": 4},
}

// exactlyOneOfExempt lists component operators whose predict refuses an
// empty params object naming an optional, ungrouped param, and why
// that refusal is not an exactly-one-of rule among params.
var exactlyOneOfExempt = map[string]string{
	string(types.AGG_WEIGHTED_MEAN): "weight_field is one way to name the weight; a slot or request weight is the other, and neither is a param",
}

// allManifestParams yields every operator-like entry's name and params
// from the manifest: the six component categories, tests, post-tests,
// regressions, matrices and synth distributions.
func allManifestParams(m *descriptor.Manifest) map[string][]descriptor.Param {
	out := map[string][]descriptor.Param{}
	c := m.Components
	for _, ops := range [][]descriptor.Operator{c.Aggregators, c.Attributes, c.Filterers, c.Groupers, c.Windows, c.Features} {
		for _, o := range ops {
			out[o.Category+" "+o.Name] = o.Params
		}
	}
	for _, t := range m.Tests {
		out["test "+t.Name] = t.Params
	}
	for _, t := range m.PostTests {
		out["post_test "+t.Name] = t.Params
	}
	for _, r := range m.Regressions {
		out["regression "+r.Name] = r.Params
	}
	for _, x := range m.Matrices {
		out["matrix "+x.Name] = x.Params
	}
	for _, d := range m.SynthDistributions {
		out["distribution "+d.Name] = d.Params
	}
	return out
}

// exactlyOneOfGroups returns op's groups in Params order: group name →
// member names in Params order.
func exactlyOneOfGroups(ps []descriptor.Param) (order []string, members map[string][]string) {
	members = map[string][]string{}
	for _, p := range ps {
		if p.ExactlyOneOf == "" {
			continue
		}
		if _, seen := members[p.ExactlyOneOf]; !seen {
			order = append(order, p.ExactlyOneOf)
		}
		members[p.ExactlyOneOf] = append(members[p.ExactlyOneOf], p.Name)
	}
	return order, members
}

// componentProbeRequest builds a one-operator request for a component
// operator over windowTestSchema: the first schema field the operator
// accepts, a date order key and a rows frame for a window, and params.
func componentProbeRequest(t *testing.T, o descriptor.Operator, params map[string]any) *types.Request {
	t.Helper()
	field := ""
	for _, f := range windowTestSchema(t).Fields {
		if slices.Contains(o.AcceptsTypes, f.Type.String()) {
			field = f.Name
			break
		}
	}
	entry := map[string]any{"type": o.Name, "field": field}
	if params != nil {
		entry["params"] = params
	}
	req := map[string]any{}
	switch o.Category {
	case "aggregator":
		req["aggregations"] = []any{entry}
	case "attribute":
		req["attributes"] = []any{entry}
	case "filterer":
		req["filterers"] = []any{entry}
	case "grouper":
		req["groups"] = []any{entry}
		req["aggregations"] = []any{map[string]any{"type": "AGG_COUNT", "field": "revenue"}}
	case "window":
		entry["order_by"] = []any{map[string]any{"field": "ts"}}
		if WindowFrameRequired(types.WindowType(o.Name)) {
			entry["frame"] = map[string]any{"mode": "rows", "preceding": 1, "following": 0}
		}
		req["windows"] = []any{entry}
	case "feature":
		req["features"] = []any{entry}
	default:
		t.Fatalf("%s: no probe request shape for category %q", o.Name, o.Category)
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var r types.Request
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("%s: probe request does not decode: %v", o.Name, err)
	}
	return &r
}

// namesParam reports whether msg names param as a word.
func namesParam(msg, param string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(param) + `\b`).MatchString(msg)
}

// TestManifestExactlyOneOfComplete pins the exactly_one_of declaration
// three ways, so an operator whose runtime takes one of several params
// can never again fall silently out of Recommend's drafting (which
// drafts declared-required params and each group's first member):
//
//  1. Shape: every group, on every operator-like manifest entry, has
//     two or more members, none Required and none with a Default.
//  2. Completeness: every component operator is predicted with empty
//     params; an error that names a declared, optional param must name
//     one in an exactly_one_of group (or the operator is exempt, with a
//     reason). An undeclared one-of rule fails here.
//  3. Parity: for each declared group, no member is refused naming
//     every member, each member alone (with a valid sample) predicts
//     clean, and two members together are refused.
//
// The runtime half of parity is processing's
// TestExactlyOneOfRefusedAtRuntime.
func TestManifestExactlyOneOfComplete(t *testing.T) {
	m := BuildManifest()

	for key, ps := range allManifestParams(m) {
		order, members := exactlyOneOfGroups(ps)
		for _, g := range order {
			if len(members[g]) < 2 {
				t.Errorf("%s: exactly_one_of group %q has one member %v; declare a lone param Required", key, g, members[g])
			}
		}
		for _, p := range ps {
			if p.ExactlyOneOf != "" && (p.Required || p.Default != nil) {
				t.Errorf("%s: param %q is in group %q but is Required or defaulted", key, p.Name, p.ExactlyOneOf)
			}
		}
	}

	data := buildTestPulseFile(t, windowTestSchema(t))
	c := m.Components
	grouped := 0
	for _, ops := range [][]descriptor.Operator{c.Aggregators, c.Attributes, c.Filterers, c.Groupers, c.Windows, c.Features} {
		for _, o := range ops {
			order, members := exactlyOneOfGroups(o.Params)

			env := predictFromBytes(data, componentProbeRequest(t, o, nil), nil)
			for _, e := range env.Errors {
				for _, p := range o.Params {
					if p.Required || p.ExactlyOneOf != "" || !namesParam(e.Message, p.Name) {
						continue
					}
					if _, ok := exactlyOneOfExempt[o.Name]; ok {
						continue
					}
					t.Errorf("%s: empty params refused naming optional param %q (%s); declare its exactly_one_of group, or Required", o.Name, p.Name, e.Message)
				}
			}
			if len(order) == 0 {
				continue
			}
			grouped++

			for _, g := range order {
				ms := members[g]
				if len(ms) < 2 {
					continue // the shape check above reports it
				}
				if len(env.Errors) == 0 {
					t.Errorf("%s: no member of group %q given, yet predict accepts", o.Name, g)
				}
				for _, mem := range ms {
					if !slices.ContainsFunc(env.Errors, func(e *descriptor.EnvelopeEntry) bool { return namesParam(e.Message, mem) }) {
						t.Errorf("%s: the none-given refusal does not name %q: %+v", o.Name, mem, env.Errors)
					}
				}
				samples := exactlyOneOfSamples[o.Name]
				for _, mem := range ms {
					v, ok := samples[mem]
					if !ok {
						t.Fatalf("%s: no exactlyOneOfSamples value for %q", o.Name, mem)
					}
					one := predictFromBytes(data, componentProbeRequest(t, o, map[string]any{mem: v}), nil)
					if len(one.Errors) != 0 {
						t.Errorf("%s: %q alone is refused: %+v", o.Name, mem, one.Errors)
					}
				}
				two := predictFromBytes(data, componentProbeRequest(t, o, map[string]any{ms[0]: samples[ms[0]], ms[1]: samples[ms[1]]}), nil)
				if !slices.ContainsFunc(two.Errors, func(e *descriptor.EnvelopeEntry) bool {
					return strings.Contains(e.Message, "mutually exclusive")
				}) {
					t.Errorf("%s: %q with %q is not refused as mutually exclusive: %+v", o.Name, ms[0], ms[1], two.Errors)
				}
			}
		}
	}
	if grouped < len(exactlyOneOfSamples) {
		t.Errorf("only %d component operators declare a group; exactlyOneOfSamples lists %d", grouped, len(exactlyOneOfSamples))
	}
}
