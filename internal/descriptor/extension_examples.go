package descriptor

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/types"
)

// Embedder examples (U10, E5-S2) — pulse.Options.Extensions.Examples.
//
// An embedder ships an fs.FS of top-level `.json` files, each shaped
// like a built-in library entry: a request body plus a `_meta` block
// (name, category, description, tags, operators, optional intents and
// capabilities). LoadExtensionExamples validates them HARD at pulse.New,
// against the FULL extended graph (every registration, before a feature
// profile hides any), so validity never depends on the profile. The
// checks, in order, each failing with PULSE_EXTENSION_EXAMPLE_INVALID
// (reason in details) unless noted:
//
//  1. layout — the root reads; every entry a regular `.json` file;
//  2. meta — a JSON object with a `_meta` block holding only the known
//     keys (examples.Parse, strict);
//  3. name — `_meta.name` lowercase letters / digits joined by - or _;
//  4. collision — a built-in example's name, or one shipped twice →
//     PULSE_EXTENSION_EXAMPLE_COLLISION;
//  5. category / description — a lowercase category token; a
//     description;
//  6. tags — every tag in examples.CanonicalTags;
//  7. intents — every intent an intent-taxonomy ID;
//  8. operators — every `_meta.operators` entry an operator node of the
//     extended graph (built-in or registered extension operator);
//  9. capabilities — every entry a non-operator feature
//     (exampleCapabilityProblems, the shipped library's rule);
//  10. body — the body decodes STRICTLY (unknown keys refused) as its
//     request root: `requests` → compose, `stages` → process chain,
//     `fields` → facet, otherwise process;
//  11. operators_body — `_meta.operators` equals the body's `"type"`
//     values (examples.DeriveOperators, the shipped library's rule);
//  12. edge_coverage — every operator the body or description names is
//     joined to the example by an edge and no declared capability
//     contradicts its structural detector (exampleEdgeCoverageProblems,
//     the TestExamples_EdgeCoverage rule).
//
// The instance graph (extendOntology → addExtensionExamples) then adds
// each example whose extension operators all survive the profile — one
// naming a hidden extension operator (dropped from the snapshot before
// the graph is built) is no node — with the edges a built-in example
// gets (E1-S2 detector table, overlay kinds, description mentions);
// pruneOntology then prunes it like a built-in. Discovery serves the
// survivors through the same search / get / stats as the shipped
// library.

// ExtensionExample is one validated embedder example.
type ExtensionExample struct {
	// Example is the served record: Body is the request with `_meta`
	// stripped, re-marshaled deterministically.
	Example      examples.Example
	Intents      []string
	Capabilities []string
	// ExtOperators are the registered extension operators the example
	// names anywhere (body, description, `_meta.operators`); the example
	// is no node of an instance that hides any of them.
	ExtOperators []string
}

// Reasons carried in PULSE_EXTENSION_EXAMPLE_* details["reason"].
const (
	ExampleReasonLayout        = "layout"
	ExampleReasonBuiltin       = "builtin"
	ExampleReasonDuplicate     = "duplicate"
	ExampleReasonMeta          = "meta"
	ExampleReasonName          = "name"
	ExampleReasonCategory      = "category"
	ExampleReasonDescription   = "description"
	ExampleReasonTags          = "tags"
	ExampleReasonIntents       = "intents"
	ExampleReasonOperators     = "operators"
	ExampleReasonCapabilities  = "capabilities"
	ExampleReasonBody          = "body"
	ExampleReasonOperatorsBody = "operators_body"
	ExampleReasonEdgeCoverage  = "edge_coverage"
)

var (
	extExampleName     = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*$`)
	extExampleCategory = regexp.MustCompile(`^[a-z][a-z0-9]*([_-][a-z0-9]+)*$`)
)

func exampleError(code errors.Code, example, reason, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["example"] = example
	details["reason"] = reason
	return errors.NewCodedErrorWithDetails(code, "extension example "+example+": "+msg, details)
}

func exampleInvalid(example, reason, msg string, details map[string]any) error {
	return exampleError(errors.PULSE_EXTENSION_EXAMPLE_INVALID, example, reason, msg, details)
}

// LoadExtensionExamples reads and validates every example in fsys
// against ext (the snapshot of EVERY registration — not the
// profile-filtered one). Nil fsys loads nothing. The examples come back
// sorted by name.
func LoadExtensionExamples(fsys fs.FS, ext *ExtensionsSnapshot) ([]ExtensionExample, error) {
	if fsys == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, exampleInvalid(".", ExampleReasonLayout, "cannot read the examples fs.FS root: "+err.Error(), nil)
	}
	extOps := map[string]bool{}
	for op := range extensionOperatorCategories(ext) {
		extOps[op] = true
	}

	var out []ExtensionExample
	seen := map[string]bool{}
	for _, e := range entries {
		file := e.Name()
		if e.IsDir() || path.Ext(file) != ".json" || !e.Type().IsRegular() {
			return nil, exampleInvalid(file, ExampleReasonLayout, "Extensions.Examples holds only top-level .json files", nil)
		}
		data, err := fs.ReadFile(fsys, file)
		if err != nil {
			return nil, exampleInvalid(file, ExampleReasonLayout, "cannot read "+file+": "+err.Error(), nil)
		}
		ex, m, err := examples.Parse(data, true)
		if err != nil {
			return nil, exampleInvalid(file, ExampleReasonMeta, err.Error(), map[string]any{"file": file})
		}
		if !extExampleName.MatchString(m.Name) {
			return nil, exampleInvalid(file, ExampleReasonName, fmt.Sprintf("_meta.name %q must be lowercase letters and digits joined by - or _", m.Name), map[string]any{"file": file, "name": m.Name})
		}
		if _, builtin := examples.Get(m.Name); builtin {
			return nil, exampleError(errors.PULSE_EXTENSION_EXAMPLE_COLLISION, m.Name, ExampleReasonBuiltin, "the name is a built-in example; embedder examples never override or shadow the shipped library", map[string]any{"file": file})
		}
		if seen[m.Name] {
			return nil, exampleError(errors.PULSE_EXTENSION_EXAMPLE_COLLISION, m.Name, ExampleReasonDuplicate, "the name is shipped more than once", map[string]any{"file": file})
		}
		seen[m.Name] = true
		if err := checkExtensionExampleMeta(m); err != nil {
			return nil, err
		}
		if err := checkExtensionExampleBody(m, ex.Body); err != nil {
			return nil, err
		}
		out = append(out, ExtensionExample{
			Example:      *ex,
			Intents:      slices.Clone(m.Intents),
			Capabilities: slices.Clone(m.Capabilities),
			ExtOperators: namedExtensionOperators(ex, extOps),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Example.Name < out[j].Example.Name })
	if len(out) == 0 {
		return nil, nil
	}

	// The full extended graph: base + every registration + every example.
	full := &ExtensionsSnapshot{}
	if ext != nil {
		cp := *ext
		full = &cp
	}
	full.Examples = out
	g := extendOntology(BaseOntology(), full)
	for _, ex := range out {
		for _, op := range ex.Example.Operators {
			if !g.Has(OntologyID(descriptor.OntologyNodeOperator, op)) {
				return nil, exampleInvalid(ex.Example.Name, ExampleReasonOperators, fmt.Sprintf("_meta.operators names %q, which is no built-in or registered extension operator", op), map[string]any{"operator": op})
			}
		}
		if p := exampleEdgeCoverageProblems(g, []ontologyExample{ex.ontologyExample()}, extOps); len(p) > 0 {
			return nil, exampleInvalid(ex.Example.Name, ExampleReasonEdgeCoverage, strings.Join(p, "; ")+" — reference the operator through a \"type\" or overlays[].kind, or drop the mention", map[string]any{"problems": p})
		}
	}
	return out, nil
}

// checkExtensionExampleMeta runs checks 5–7 and 9 (no graph needed).
func checkExtensionExampleMeta(m examples.Meta) error {
	if !extExampleCategory.MatchString(m.Category) {
		return exampleInvalid(m.Name, ExampleReasonCategory, fmt.Sprintf("_meta.category %q must be a lowercase token", m.Category), map[string]any{"category": m.Category})
	}
	if strings.TrimSpace(m.Description) == "" {
		return exampleInvalid(m.Name, ExampleReasonDescription, "_meta.description is required", nil)
	}
	for _, tag := range m.Tags {
		if !examples.IsCanonicalTag(tag) {
			return exampleInvalid(m.Name, ExampleReasonTags, fmt.Sprintf("tag %q is not in the canonical example taxonomy", tag), map[string]any{"tag": tag})
		}
	}
	for _, id := range m.Intents {
		if !IsIntent(id) {
			return exampleInvalid(m.Name, ExampleReasonIntents, fmt.Sprintf("intent %q is not an intent-taxonomy ID", id), map[string]any{"intent": id})
		}
	}
	if p := exampleCapabilityProblems(map[string][]string{m.Name: m.Capabilities}); len(p) > 0 {
		return exampleInvalid(m.Name, ExampleReasonCapabilities, strings.Join(p, "; "), map[string]any{"capabilities": m.Capabilities})
	}
	return nil
}

// checkExtensionExampleBody runs checks 10–11.
func checkExtensionExampleBody(m examples.Meta, body json.RawMessage) error {
	root, target := exampleRequestRoot(body)
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		var se *json.SyntaxError
		msg := err.Error()
		if stderrors.As(err, &se) {
			msg = "malformed JSON: " + msg
		}
		return exampleInvalid(m.Name, ExampleReasonBody, "the body does not parse as a "+root+" request: "+msg, map[string]any{"root": root})
	}
	derived := examples.DeriveOperators(body)
	declared := slices.Sorted(slices.Values(m.Operators))
	if !slices.Equal(derived, declared) {
		return exampleInvalid(m.Name, ExampleReasonOperatorsBody, fmt.Sprintf("_meta.operators %v must equal the body's \"type\" operators %v", declared, derived), map[string]any{"declared": declared, "derived": derived})
	}
	return nil
}

// exampleRequestRoot picks the request root a body decodes as, by its
// top-level keys (the structural detectors' root rule).
func exampleRequestRoot(body json.RawMessage) (string, any) {
	var top map[string]json.RawMessage
	_ = json.Unmarshal(body, &top)
	switch {
	case top["requests"] != nil:
		return "compose", &types.ComposedRequest{}
	case top["stages"] != nil:
		return "process_chain", &types.ChainRequest{}
	case top["fields"] != nil:
		return "facet", &types.FacetRequest{}
	}
	return "process", &types.Request{}
}

// namedExtensionOperators returns the sorted extension operators ex
// names in its body, description or `_meta.operators`.
func namedExtensionOperators(ex *examples.Example, extOps map[string]bool) []string {
	seen := map[string]bool{}
	for _, op := range ex.Operators {
		if extOps[op] {
			seen[op] = true
		}
	}
	for _, text := range []string{string(ex.Body), ex.Description} {
		for _, tok := range strings.FieldsFunc(text, notTokenRune) {
			if extOps[tok] {
				seen[tok] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for op := range seen {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// ontologyExample is the graph's view of the example.
func (e ExtensionExample) ontologyExample() ontologyExample {
	return ontologyExample{
		Name:         e.Example.Name,
		Category:     e.Example.Category,
		Description:  e.Example.Description,
		Operators:    e.Example.Operators,
		Intents:      e.Intents,
		Capabilities: e.Capabilities,
		Tags:         e.Example.Tags,
		Body:         e.Example.Body,
	}
}

// addExtensionExamples adds the embedder examples whose extension
// operators the graph carries, with the edges a built-in example gets
// (addExampleEdges) plus every built-in atomic skill's `## See`
// `tags=[…]` edge into them. It returns the kept examples, which the
// embedder skills' See tag edges also reach.
func (b *ontologyBuilder) addExtensionExamples(in []ExtensionExample) []ontologyExample {
	if len(in) == 0 {
		return nil
	}
	var kept []ontologyExample
	for _, e := range in {
		ok := true
		for _, op := range e.ExtOperators {
			if !b.has(OntologyID(descriptor.OntologyNodeOperator, op)) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		b.node(descriptor.OntologyNodeExample, e.Example.Name)
		kept = append(kept, e.ontologyExample())
	}
	if len(kept) == 0 {
		return nil
	}
	b.addExampleEdges(ontologySources{examples: kept})
	b.addSeeEdges(ontologySources{skills: skills.List(), skillBody: skills.Raw, examples: kept})
	return kept
}
