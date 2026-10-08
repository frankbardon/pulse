package descriptor

import (
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
)

// slotOwningCapabilities derives, from gatedSlots alone, every
// capability that owns a request / response slot: hiding it (and
// nothing else) hides at least one slot the full instance shows.
func slotOwningCapabilities(t *testing.T) []string {
	t.Helper()
	full := scopedExcept()
	var out []string
	for _, name := range FeatureNames() {
		f, _ := LookupFeature(name)
		if f.Kind != FeatureKindCapability {
			continue
		}
		inst := scopedExcept(name)
		owns := false
		for _, slots := range gatedSlots {
			for _, g := range slots {
				if g.visible(full) && !g.visible(inst) {
					owns = true
				}
			}
		}
		if owns {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		t.Fatal("no capability owns a gated slot: the probe is broken")
	}
	return out
}

// TestSlotTokens_EveryRequestSlotCapability: every capability owning a
// gated slot has a slotTokens entry (possibly empty), and no entry names
// a capability that owns none — so a new slot capability without a
// declared token set fails here.
func TestSlotTokens_EveryRequestSlotCapability(t *testing.T) {
	owners := slotOwningCapabilities(t)
	for _, c := range owners {
		if _, ok := SlotTokensOf(c); !ok {
			t.Errorf("%s owns a request slot but has no slotTokens entry (features.go); add one, empty if no wire token is safe to scrub", c)
		}
	}
	for c := range requestSlotCapabilities {
		if _, ok := slotTokens[c]; !ok {
			t.Errorf("request-slot capability %s has no slotTokens entry", c)
		}
	}
	if got := SlotTokenCapabilities(); !reflect.DeepEqual(got, owners) {
		t.Errorf("slotTokens keys = %v, want exactly the slot-owning capabilities %v", got, owners)
	}
}

// TestSlotTokens_Shape: tokens are whole scrub tokens, never a feature
// or MCP tool name, unique across entries; a non-empty entry declares
// its topic; every homonym key is a declared token.
func TestSlotTokens_Shape(t *testing.T) {
	seen := map[string]string{}
	for _, c := range SlotTokenCapabilities() {
		set := slotTokens[c]
		if len(set.tokens) > 0 && set.topic == nil {
			t.Errorf("%s declares tokens but no topic", c)
		}
		for _, tok := range set.tokens {
			for i := range len(tok) {
				if !isTokenByte(tok[i]) {
					t.Errorf("%s token %q is not a whole [A-Za-z0-9_] run", c, tok)
					break
				}
			}
			if _, ok := LookupFeature(tok); ok {
				t.Errorf("%s token %q is a feature name", c, tok)
			}
			for _, b := range mcpToolBindings {
				if b.Tool == tok {
					t.Errorf("%s token %q is an MCP tool", c, tok)
				}
			}
			if prev, dup := seen[tok]; dup {
				t.Errorf("token %q declared by both %s and %s", tok, prev, c)
			}
			seen[tok] = c
		}
	}
	for tok := range slotTokenHomonyms {
		if _, ok := seen[tok]; !ok {
			t.Errorf("homonym key %q is not a declared slot token", tok)
		}
	}
}

// TestHiddenProseNames_SlotTokens: a hidden slot capability adds its
// tokens to the scrub set; an enabled one (and an unscoped instance)
// adds none.
func TestHiddenProseNames_SlotTokens(t *testing.T) {
	hidden := hiddenProseNames(scopedExcept(FeatureMultiplicity))
	for _, tok := range []string{"multiplicity", "p_adjusted", "significant_adjusted"} {
		if _, ok := hidden[tok]; !ok {
			t.Errorf("hiding %s does not scrub %q", FeatureMultiplicity, tok)
		}
	}
	if _, ok := hidden["matrices"]; ok {
		t.Error("an enabled capability:matrices still scrubs its tokens")
	}
	if got := hiddenProseNames(scopedExcept()); len(got) != 0 {
		t.Errorf("a full scoped instance scrubs %v", got)
	}
	if got := hiddenProseNames(nil); got != nil {
		t.Errorf("an unscoped instance scrubs %v", got)
	}
}

// TestProseScrub_SlotTokens: hiding capability:multiplicity drops the
// sentences naming its tokens; a sentence about pulse_lookup's
// same-spelled duplicate-key mode stays, even beside a dropped one.
func TestProseScrub_SlotTokens(t *testing.T) {
	s := NewProseScrub(scopedExcept(FeatureMultiplicity))
	in := "Runs a test. Set multiplicity on the request to adjust. Read payload.p_adjusted beside the raw p. The end."
	if got, want := s.Text(in), "Runs a test. The end."; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	lookup := "Point lookup. multiplicity controls duplicate-key handling: 'assert_unique' (default; errors with PULSE_LOOKUP_AMBIGUOUS on >1 match). Set multiplicity to holm to adjust p-values."
	if got, want := s.Text(lookup), "Point lookup. multiplicity controls duplicate-key handling: 'assert_unique' (default; errors with PULSE_LOOKUP_AMBIGUOUS on >1 match)."; got != want {
		t.Errorf("homonym sentence: Text = %q, want %q", got, want)
	}
	if full := NewProseScrub(scopedExcept()); full.Text(in) != in {
		t.Error("an instance offering capability:multiplicity scrubbed its prose")
	}
}

// TestScrubManifest_LineAware: the manifest scrub redacts each line on
// its own (ProseScrub.Text), so a hidden token in one paragraph never
// takes the sentence a paragraph break glued to it.
func TestScrubManifest_LineAware(t *testing.T) {
	m := &descriptor.Manifest{MCPTools: []descriptor.MCPTool{{
		Name:        "pulse_x",
		Description: "Keeps this. Lists `matrices` here.\n\nKept after the break. Also kept.",
	}}}
	got := scrubManifest(m, hiddenProseNames(scopedExcept(featMatrices, "MAT_COVARIANCE", "MAT_CORRELATION")))
	if want := "Keeps this.\n\nKept after the break. Also kept."; got.MCPTools[0].Description != want {
		t.Errorf("Description = %q, want %q", got.MCPTools[0].Description, want)
	}
	hidden := hiddenProseNames(scopedExcept(featMatrices, "MAT_COVARIANCE", "MAT_CORRELATION"))
	list := scrubValue(reflect.ValueOf([]string{"Rule one.\n\nNames `vectors` here.", "matrices", "Untouched."}), hidden).Interface().([]string)
	if want := []string{"Rule one.", "Untouched."}; !reflect.DeepEqual(list, want) {
		t.Errorf("list = %q, want %q", list, want)
	}
}
