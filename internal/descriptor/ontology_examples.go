package descriptor

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
)

// Example → capability edges (E1-S2).
//
// An example's operators ride `_meta.operators`; everything else an
// example needs is read from the STRUCTURE of its request body through
// the closed detector table below, or — for a capability no body shape
// can show (stream, watch, shard, index, sample, labels, range_tables,
// filter_to_file) — from the optional `_meta.capabilities` list. Neither
// reads prose. The one prose-derived edge is the description mention:
// the description an example is listed and served with names an operator
// as a whole token (usually a contrast — "unlike TEST_T"), so the
// example routes_to that operator and is pruned with it.
//
// TestExamples_EdgeCoverage holds the whole library to this: every
// operator named anywhere in a body or description is joined to its
// example by an edge, and no `_meta.capabilities` entry contradicts a
// detector.

// exampleCapabilityDetector derives one capability from an example's
// request structure.
type exampleCapabilityDetector struct {
	feature string
	detect  func(ex ontologyExample, body any) bool
}

// exampleCapabilityDetectors is the CLOSED structural table. A detected
// capability is an `example requires_capability <capability>` edge; a
// `_meta.capabilities` entry naming one of these features must agree
// with its detector.
var exampleCapabilityDetectors = []exampleCapabilityDetector{
	{featCrosstab, func(_ ontologyExample, body any) bool { return jsonHasKey(body, "crosstab") }},
	{featCompose, func(_ ontologyExample, body any) bool { return rootHasKey(body, "requests") }},
	{featProcessChain, func(_ ontologyExample, body any) bool { return rootHasKey(body, "stages") }},
	{featJoins, func(_ ontologyExample, body any) bool { return jsonHasKey(body, "joins") }},
	{featFacet, func(ex ontologyExample, _ any) bool {
		return ex.Category == "facet" || slices.Contains(ex.Tags, "facet")
	}},
}

func rootHasKey(body any, key string) bool {
	root, ok := body.(map[string]any)
	if !ok {
		return false
	}
	_, ok = root[key]
	return ok
}

// decodeExampleBody decodes a body; nil when absent or malformed (a
// malformed embedded example already panics in the examples index).
func decodeExampleBody(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var body any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil
	}
	return body
}

// detectedExampleCapabilities returns the detector-table capabilities
// the example's structure exercises, in table order.
func detectedExampleCapabilities(ex ontologyExample, body any) []string {
	var out []string
	for _, d := range exampleCapabilityDetectors {
		if d.detect(ex, body) {
			out = append(out, d.feature)
		}
	}
	return out
}

// exampleOverlayKinds returns the sorted, distinct `kind` of every
// element of every `overlays` array anywhere in the body (Request,
// Crosstab, Compose, ProcessChain and Facet overlays alike).
func exampleOverlayKinds(body any) []string {
	seen := map[string]struct{}{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if list, ok := t["overlays"].([]any); ok {
				for _, o := range list {
					if spec, ok := o.(map[string]any); ok {
						if k, ok := spec["kind"].(string); ok && k != "" {
							seen[k] = struct{}{}
						}
					}
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(body)
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// exampleCapabilityProblems validates `_meta.capabilities` against the
// feature table: every value must be a known NON-operator feature
// (operators belong in `_meta.operators`). Sorted by example name.
func exampleCapabilityProblems(byExample map[string][]string) []string {
	names := make([]string, 0, len(byExample))
	for n := range byExample {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		for _, c := range byExample[n] {
			kind, ok := FeatureKindOf(c)
			switch {
			case !ok:
				out = append(out, "example \""+n+"\": _meta.capabilities value \""+c+"\" is not a feature name")
			case kind == FeatureKindOperator:
				out = append(out, "example \""+n+"\": _meta.capabilities value \""+c+"\" is an operator (list it in _meta.operators)")
			}
		}
	}
	return out
}

// addExampleEdges: _meta operators / intents / capabilities, the
// structural detectors, overlay kinds and description mentions.
func (b *ontologyBuilder) addExampleEdges(src ontologySources) {
	for _, ex := range src.examples {
		id := OntologyID(descriptor.OntologyNodeExample, ex.Name)
		linked := map[string]struct{}{}
		for _, op := range ex.Operators {
			linked[op] = struct{}{}
			b.edge(b.featureNode(op, "example "+ex.Name+" operators"), id, descriptor.OntologyEdgeExemplifiedBy, "example operators")
		}
		for _, in := range ex.Intents {
			b.edge(id, OntologyID(descriptor.OntologyNodeIntent, in), descriptor.OntologyEdgeServesIntent, "example intents")
		}
		for _, c := range ex.Capabilities {
			b.edge(id, b.featureNode(c, "example "+ex.Name+" capabilities"), descriptor.OntologyEdgeRequiresCapability, "example capabilities")
		}
		body := decodeExampleBody(ex.Body)
		for _, c := range detectedExampleCapabilities(ex, body) {
			b.edge(id, b.featureNode(c, "example "+ex.Name+" detector"), descriptor.OntologyEdgeRequiresCapability, "example detector")
		}
		for _, k := range exampleOverlayKinds(body) {
			linked[k] = struct{}{}
			b.edge(b.featureNode(k, "example "+ex.Name+" overlay kind"), id, descriptor.OntologyEdgeExemplifiedBy, "example overlay kind")
		}
		for _, tok := range strings.FieldsFunc(ex.Description, notTokenRune) {
			if _, ok := linked[tok]; ok || b.featureKind[tok] != FeatureKindOperator && !b.extOperators[tok] {
				continue
			}
			linked[tok] = struct{}{}
			b.edge(id, OntologyID(descriptor.OntologyNodeOperator, tok), descriptor.OntologyEdgeRoutesTo, "example description")
		}
	}
}

// exampleEdgeCoverageProblems is the TestExamples_EdgeCoverage check over
// a built graph and the examples it was built from:
//
//   - every whole [A-Za-z0-9_] token of a body or description that is an
//     operator feature name is joined to the example by an edge (either
//     direction, any kind) — so a pruner walking edges hides the example
//     exactly when the old token scan would;
//   - every `_meta.capabilities` entry the detector table can see is
//     confirmed by its detector (a declaration never contradicts the
//     body), and every detection is an edge.
func exampleEdgeCoverageProblems(g *OntologyGraph, exs []ontologyExample, extOps map[string]bool) []string {
	var out []string
	for _, ex := range exs {
		id := OntologyID(descriptor.OntologyNodeExample, ex.Name)
		linked := map[string]bool{}
		for _, e := range g.Out(id, "") {
			linked[e.To] = true
		}
		for _, e := range g.In(id, "") {
			linked[e.From] = true
		}
		for where, text := range map[string]string{"body": string(ex.Body), "description": ex.Description} {
			for _, tok := range strings.FieldsFunc(text, notTokenRune) {
				if k, ok := FeatureKindOf(tok); (!ok || k != FeatureKindOperator) && !extOps[tok] {
					continue
				}
				if !linked[OntologyID(descriptor.OntologyNodeOperator, tok)] {
					out = append(out, "example \""+ex.Name+"\": "+where+" names "+tok+" but no edge joins them")
				}
			}
		}
		body := decodeExampleBody(ex.Body)
		detected := detectedExampleCapabilities(ex, body)
		for _, c := range detected {
			if !linked[c] {
				out = append(out, "example \""+ex.Name+"\": detected "+c+" but no edge")
			}
		}
		for _, c := range ex.Capabilities {
			for _, d := range exampleCapabilityDetectors {
				if d.feature == c && !slices.Contains(detected, c) {
					out = append(out, "example \""+ex.Name+"\": _meta.capabilities declares "+c+" but its structural detector does not fire")
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
