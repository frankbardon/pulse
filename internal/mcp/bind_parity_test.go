package mcp

import (
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// bindParityRoot pairs a bound tool schema (or a property path inside
// one) with the payload-schema def it must describe.
type bindParityRoot struct {
	tool string // bound tool name
	path string // dotted path inside the bound body ("" = the body)
	def  string // payload-schema $defs name
}

// bindParityRoots lists every request body BindForInstance builds that
// a payload-schema root describes, pulse_predict's alternative roots
// included (they reuse the execution tools' bound bodies).
var bindParityRoots = []bindParityRoot{
	{toolmeta.ToolProcess, "", "Request"},
	{toolmeta.ToolCompose, "", "ComposedRequest"},
	{toolmeta.ToolProcessChain, "", "ChainRequest"},
	{toolmeta.ToolFacetSchema, "", "FacetRequest"},
	{toolmeta.ToolPredict, "", "Request"},
	{toolmeta.ToolPredict, "properties.composed", "ComposedRequest"},
	{toolmeta.ToolPredict, "properties.facet", "FacetRequest"},
	{toolmeta.ToolPredict, "properties.chain", "ChainRequest"},
}

// bindParityExclusions are the INTENTIONAL gaps: payload-schema
// properties a bound schema deliberately leaves out (or leaves opaque).
// A key is "<Def>.<property>" — the payload def that declares it, every
// site that def is reached — or "<Def>.<property>.<sub>" to name one
// site of a shared def only (the property `sub` of the def reached
// through <Def>.<property>). Every entry carries the reason it is not a
// bug; a gap without a reason is a bind fix, never an entry.
var bindParityExclusions = map[string]string{
	"Test.multiplicity.alpha":      "A test's adjusted flag reads the test's own `alpha`; the engine refuses multiplicity.alpha on a test or post-test block (multiplicity_resolve multReasonTestAlpha). The payload schema shares one Multiplicity def across every host, so it cannot omit alpha at this site alone.",
	"FacetRequest.overlays.weight": "Facets are never weighted: no facet-host overlay kind reads a weight, so advertising one would invite a key that changes nothing. The payload schema shares the OverlaySpec def with the Request host, where the weight is live.",
}

// bindParityUnboundLabels are the exclusions that hold only on an
// instance offering no label table (none registered, or
// capability:labels hidden): the labels slot's table enum would be
// empty, so the binder omits the slot — every binding there is
// PULSE_LABEL_TABLE_UNKNOWN — exactly as on an instance that registered
// no table. Where a table is offered the slot must be bound.
var bindParityUnboundLabels = map[string]string{
	"Request.labels":      "no label table offered: every binding is PULSE_LABEL_TABLE_UNKNOWN, so the slot is omitted as on a table-free instance",
	"FacetRequest.labels": "no label table offered: every binding is PULSE_LABEL_TABLE_UNKNOWN, so the slot is omitted as on a table-free instance",
}

// bindParityExtras are bound-schema properties at a path ("<tool>:<Def>
// root path>.<key>") that the payload def at that point does not
// declare, each with the reason it is not a stray key.
var bindParityExtras = map[string]string{
	toolmeta.ToolPredict + ":Request.composed": "pulse_predict's alternative root (PredictIn.Composed), checked against ComposedRequest as its own root.",
	toolmeta.ToolPredict + ":Request.facet":    "pulse_predict's alternative root (PredictIn.Facet), checked against FacetRequest as its own root.",
	toolmeta.ToolPredict + ":Request.chain":    "pulse_predict's alternative root (PredictIn.Chain), checked against ChainRequest as its own root.",
}

// TestBindForInstance_PayloadSchemaParity: every property the payload
// schema declares under a request root a bound tool describes — and,
// recursively, under every nested object that bound schema itself
// details — is present in the bound schema, unless bindParityExclusions
// names it with a reason. Checked profile-free and under a feature
// profile against that instance's own payload schema, so a property the
// profile hides must be absent from both.
func TestBindForInstance_PayloadSchemaParity(t *testing.T) {
	used := map[string]bool{}
	profiled := &pulse.FeatureProfile{Features: append(append([]string(nil), multiplicityFeatures...),
		descx.FeatureMultiplicity, "capability:labels")}
	for _, c := range []struct {
		label string
		fp    *pulse.FeatureProfile
		ext   pulse.Extensions
	}{
		{"profile-free", nil, labelExt},
		{"profile-free-no-label-table", nil, pulse.Extensions{}},
		{"profiled", profiled, labelExt},
		{"process-only", &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_SUM", "GROUP_CATEGORY"}}, labelExt},
	} {
		t.Run(c.label, func(t *testing.T) {
			_, inst := instanceFor(t, c.fp, c.ext)
			raw, err := descx.PayloadSchemaForInstance(inst)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Defs map[string]map[string]any `json:"$defs"`
			}
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			bound, err := BindForInstance(makeBindSchema(), inst)
			if err != nil {
				t.Fatal(err)
			}
			excl := bindParityExclusions
			if len(c.ext.LabelTables) == 0 || !inst.Enabled(labelsFeature) {
				excl = maps.Clone(bindParityExclusions)
				maps.Copy(excl, bindParityUnboundLabels)
			}
			var gaps, extras []string
			for _, r := range bindParityRoots {
				node := decodeBoundRequest(t, bound[r.tool])
				if r.path != "" {
					node = schemaAt(node, r.path)
				}
				if _, ok := payload.Defs[r.def]; !ok {
					// The profile hides the root. A predict alternative
					// root must then be absent; a whole tool body is not
					// mounted on the instance (mcp/gosdk scope), which is
					// not the binder's call.
					if r.path != "" && node != nil {
						extras = append(extras, r.tool+":"+r.path+" (root hidden by the profile)")
					}
					continue
				}
				if node == nil {
					gaps = append(gaps, r.tool+":"+r.path+" (bound root missing)")
					continue
				}
				w := parityWalker{defs: payload.Defs, used: used, excl: excl}
				w.walk(r.tool+":"+r.def, "", map[string]any{"$ref": "#/$defs/" + r.def}, node)
				gaps = append(gaps, w.gaps...)
				extras = append(extras, w.extras...)
			}
			sort.Strings(gaps)
			for _, g := range gaps {
				t.Errorf("payload-schema property missing from the bound schema: %s", g)
			}
			sort.Strings(extras)
			for _, e := range extras {
				t.Errorf("bound schema advertises a property the instance payload schema does not declare: %s", e)
			}
			if c.fp != nil {
				// Non-vacuous: the profile really hides request slots,
				// so "absent in both" was exercised.
				req := payload.Defs["Request"]["properties"].(map[string]any)
				for _, k := range []string{"weight", "matrices", "vectors", "joins"} {
					if _, ok := req[k]; ok {
						t.Fatalf("vacuous: profiled payload schema still declares Request.%s", k)
					}
				}
			}
		})
	}
	for k := range bindParityExclusions {
		if !used[k] {
			t.Errorf("stale bindParityExclusions entry %q: no bound schema omits it", k)
		}
	}
	for k := range bindParityUnboundLabels {
		if !used[k] {
			t.Errorf("stale bindParityUnboundLabels entry %q: no table-free instance exercised it", k)
		}
	}
	for k := range bindParityExtras {
		if !used["extra:"+k] {
			t.Errorf("stale bindParityExtras entry %q: no bound schema carries it", k)
		}
	}
}

// parityWalker compares one payload-schema node with one bound node.
type parityWalker struct {
	defs   map[string]map[string]any
	used   map[string]bool
	excl   map[string]string
	gaps   []string
	extras []string
}

// objectProps resolves a payload node to the union of its object
// branches' properties, keyed by property name, each value tagged with
// the def that declares it ("" for an inline object).
func (w *parityWalker) objectProps(node map[string]any, owner string, out map[string]propOrigin) {
	if node == nil {
		return
	}
	if ref, ok := node["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		w.objectProps(w.defs[name], name, out)
		return
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for k, v := range props {
			m, _ := v.(map[string]any)
			out[k] = propOrigin{owner: owner, schema: m}
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if arr, ok := node[key].([]any); ok {
			for _, b := range arr {
				m, _ := b.(map[string]any)
				w.objectProps(m, owner, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		w.objectProps(items, owner, out)
	}
}

type propOrigin struct {
	owner  string
	schema map[string]any
}

// boundProps is objectProps for a bound node (inline, no $refs).
func boundProps(node map[string]any, out map[string]map[string]any) {
	if node == nil {
		return
	}
	if props, ok := node["properties"].(map[string]any); ok {
		for k, v := range props {
			m, _ := v.(map[string]any)
			out[k] = m
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if arr, ok := node[key].([]any); ok {
			for _, b := range arr {
				m, _ := b.(map[string]any)
				boundProps(m, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		boundProps(items, out)
	}
}

// walk compares the payload node with the bound node at path. parent
// is the exclusion key of the property that led here ("" at a root), so
// an entry can name a property of a shared def at one site only
// ("Test.multiplicity.alpha") or at every site ("Multiplicity.alpha").
func (w *parityWalker) walk(path, parent string, payloadNode, boundNode map[string]any) {
	want := map[string]propOrigin{}
	w.objectProps(payloadNode, "", want)
	have := map[string]map[string]any{}
	boundProps(boundNode, have)
	for _, k := range sortedKeys(have) {
		if _, ok := want[k]; !ok && len(want) > 0 {
			if _, extra := bindParityExtras[path+"."+k]; extra {
				w.used["extra:"+path+"."+k] = true
				continue
			}
			w.extras = append(w.extras, path+"."+k)
		}
	}
	for _, k := range sortedKeys(want) {
		origin := want[k]
		key := origin.owner + "." + k
		if parent != "" {
			if _, ok := w.excl[parent+"."+k]; ok {
				key = parent + "." + k
			}
		}
		b, ok := have[k]
		if _, allowed := w.excl[key]; allowed {
			if !ok || len(boundPropsOf(b)) == 0 {
				w.used[key] = true
				continue
			}
		}
		if !ok {
			w.gaps = append(w.gaps, path+"."+k+" ["+key+"]")
			continue
		}
		sub := map[string]propOrigin{}
		w.objectProps(origin.schema, origin.owner, sub)
		if len(sub) == 0 {
			continue
		}
		if len(boundPropsOf(b)) == 0 {
			w.gaps = append(w.gaps, path+"."+k+" (opaque in the bound schema) ["+key+"]")
			continue
		}
		w.walk(path+"."+k, key, origin.schema, b)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func boundPropsOf(node map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	boundProps(node, out)
	return out
}
