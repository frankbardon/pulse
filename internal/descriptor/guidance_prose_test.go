package descriptor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// The manifest guidance budget is TWO binding caps, not one total
// (PRD FR-20; decision recorded in .claude/reference/guided-analysis.md,
// Gates). The budget exists to ban prose from the default manifest, not
// to ration identifiers: a single total scales with registry size, so
// every backfill or new operator family (U09, U24, U25, U27, U28) would
// re-trip it while still carrying only intent IDs.
//
// manifestGuidancePerEntryCap bounds one entry's "intents" key + value
// (+ comma). Sized to admit an ID-only intents array and nothing more:
// the longest today is 50 bytes (two IDs); 64 admits two of the longest
// intent IDs ("distribution_shape" + "change_over_time" = 54 bytes) or
// three short ones, while a single prose-length phrase (>= 16 runes)
// beside even one ID overflows the headroom fast.
const manifestGuidancePerEntryCap = 64

// manifestGuidanceFixedCap bounds the guidance that does not scale with
// the operator registry: the top-level intents[] taxonomy (closed), the
// overlay "inferential" flags and the two virtual-skill skills[]
// entries. 930 bytes today (214 + 342 + 374); 1280 leaves room for ~18
// more inferential overlay kinds before a deliberate re-size.
const manifestGuidanceFixedCap = 1280

// guidanceEntryBytes is one manifest entry's "intents" cost.
type guidanceEntryBytes struct {
	Path  string // JSON path of the entry (with its name when it has one)
	Bytes int
}

// manifestGuidance is the guidance-attributable bytes of a compact
// manifest, split into the per-entry and fixed parts the caps bind.
type manifestGuidance struct {
	Fixed   int
	Entries []guidanceEntryBytes
}

// Total is every guidance-attributable byte.
func (g manifestGuidance) Total() int {
	n := g.Fixed
	for _, e := range g.Entries {
		n += e.Bytes
	}
	return n
}

// manifestGuidanceBytes measures the compact manifest JSON: every
// non-top-level object key "intents" is one per-entry item (so a new
// entry type is counted with no change here); the top-level intents[]
// taxonomy, every overlay "inferential" flag (the Interpretation half's
// only manifest projection) and every skills[] entry naming a reserved
// virtual skill are fixed. Each item is counted with its separating
// comma.
func manifestGuidanceBytes(t *testing.T, raw []byte) manifestGuidance {
	t.Helper()
	var root any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	size := func(v any) int {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return len(b)
	}
	virtual := map[string]bool{}
	for _, n := range skills.ReservedVirtualNames() {
		virtual[n] = true
	}
	var out manifestGuidance
	var walk func(v any, path string, top bool)
	walk = func(v any, path string, top bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				switch {
				case k == "intents" && !top:
					p := path
					if name, ok := x["name"].(string); ok {
						p += "(" + name + ")"
					} else if kind, ok := x["kind"].(string); ok {
						p += "(" + kind + ")"
					}
					out.Entries = append(out.Entries, guidanceEntryBytes{Path: p, Bytes: len(`"intents":`) + size(child) + 1})
				case k == "intents" || k == "inferential":
					out.Fixed += len(`"`+k+`":`) + size(child) + 1
				case top && k == "skills":
					entries, _ := child.([]any)
					for _, e := range entries {
						if m, ok := e.(map[string]any); ok && virtual[m["name"].(string)] {
							out.Fixed += size(e) + 1
						}
					}
				default:
					walk(child, path+"."+k, false)
				}
			}
		case []any:
			for i, e := range x {
				walk(e, path+"["+strconv.Itoa(i)+"]", false)
			}
		}
	}
	walk(root, "", true)
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out
}

// guidanceBudgetProblems reports every cap the measurement breaks.
func guidanceBudgetProblems(g manifestGuidance) []string {
	var out []string
	if g.Fixed > manifestGuidanceFixedCap {
		out = append(out, fmt.Sprintf("fixed guidance bytes (intents[] taxonomy, inferential flags, virtual skills) = %d, cap %d", g.Fixed, manifestGuidanceFixedCap))
	}
	for _, e := range g.Entries {
		if e.Bytes > manifestGuidancePerEntryCap {
			out = append(out, fmt.Sprintf("entry %s spends %d guidance bytes, per-entry cap %d: an entry carries intent IDs only", e.Path, e.Bytes, manifestGuidancePerEntryCap))
		}
	}
	return out
}

func marshalManifest(t *testing.T, m *descriptor.Manifest) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return b
}

// pendingProseTypes are guidance types whose registry does not exist
// yet; a new guidance type lists itself here until its registry appends
// a source to proseSources.
var pendingProseTypes = map[reflect.Type]string{}

// TestGuidanceProseSourcesComplete proves GuidanceProse sweeps every
// guidance type that carries prose: each has a proseSource, or is
// listed pending (and a pending type with a source is stale).
func TestGuidanceProseSourcesComplete(t *testing.T) {
	t.Parallel()

	guidanceTypes := []reflect.Type{
		reflect.TypeOf(descriptor.Purpose{}),
		reflect.TypeOf(descriptor.Interpretation{}),
		reflect.TypeOf(descriptor.Term{}),
		reflect.TypeOf(descriptor.Intent{}),
	}
	sourced := map[reflect.Type]bool{}
	for _, s := range proseSources {
		sourced[s.typ] = true
		if len(s.values()) == 0 {
			t.Errorf("prose source for %s yields no values", s.typ)
		}
	}
	for _, typ := range guidanceTypes {
		_, pending := pendingProseTypes[typ]
		switch {
		case sourced[typ] && pending:
			t.Errorf("%s has a prose source but is still listed in pendingProseTypes — delete the pending entry", typ)
		case !sourced[typ] && !pending:
			t.Errorf("%s carries guidance prose but no proseSource feeds GuidanceProse", typ)
		}
	}
	// Every built-in Plain is swept (registry-driven: a new purpose is
	// covered with no change to the gate).
	all := map[string]bool{}
	for _, p := range GuidanceProse() {
		all[p.Text] = true
	}
	for name, p := range BuiltinPurposes() {
		if !all[p.Plain] {
			t.Errorf("purpose %s Plain not swept by GuidanceProse", name)
		}
	}
	for name, ins := range BuiltinInterpretations() {
		for _, in := range ins {
			if in.Means != "" && !all[in.Means] {
				t.Errorf("interpretation %s.%s Means not swept by GuidanceProse", name, in.Field)
			}
		}
	}
	for _, k := range SharedInterpretationKeys() {
		if in, _ := SharedInterpretation(k); !all[in.Means] {
			t.Errorf("shared interpretation %s Means not swept by GuidanceProse", k)
		}
	}
}

// TestCollectProse pins which fields the reflective walker treats as
// prose, including every Interpretation prose field so the E4 registry
// is covered the moment it is plugged in.
func TestCollectProse(t *testing.T) {
	t.Parallel()

	lo := 0.2
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"purpose", descriptor.Purpose{
			Plain: "plain", Intents: []string{"describe"}, Questions: []string{"q?"},
			UseCases:    map[descriptor.Domain]string{descriptor.DomainOps: "ops", descriptor.DomainSurvey: "survey"},
			NotFor:      []descriptor.Alternative{{When: "when", Use: "AGG_MEDIAN"}},
			Assumptions: []string{"assume"}, Level: descriptor.LevelBasic, Glossary: []string{"mean"},
		}, []string{"plain", "q?", "ops", "survey", "when", "assume"}},
		{"interpretation", descriptor.Interpretation{
			Field: "details.effect_size.cohens_d", Means: "means",
			Bands:      []descriptor.Band{{Min: &lo, Label: "small"}},
			Convention: "Cohen (1988)", Sign: map[string]string{"+": "up", "-": "down"},
			Caveats: []string{"caveat"}, Shared: "p-value",
		}, []string{"means", "small", "Cohen (1988)", "down", "up", "caveat"}},
		{"term", descriptor.Term{
			ID: "p-value", Short: "short", WhyCare: "why", SeeAlso: []string{"alpha"}, Forms: []string{"p value"},
		}, []string{"short", "why"}},
		{"intent", descriptor.Intent{
			ID: "describe", Label: "label", Sounds: []string{"sound"},
			Shapes: []descriptor.Shape{{Roles: []descriptor.Role{{Name: "outcome", Kinds: []descriptor.FieldKind{descriptor.FieldKindNumeric}}}}},
		}, []string{"label", "sound"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CollectProse(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CollectProse = %q, want %q", got, tc.want)
			}
		})
	}
}
