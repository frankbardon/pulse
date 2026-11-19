package descriptor

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// unionMembersFixture is a two-member union (the slot gate never opens
// a member, so the paths need not exist).
func unionMembersFixture() []types.UnionMember {
	return []types.UnionMember{
		{Cohort: &types.Cohort{Filename: "a.pulse"}, Label: "a"},
		{Cohort: &types.Cohort{Filename: "b.pulse"}, Label: "b"},
	}
}

// TestSlotRefusal_UnionHidden pins the capability:union gate on every
// root carrying the union slots: hidden, a set `union` (and a set
// `union_source_field`, alone included) is PULSE_REQUEST_UNKNOWN_FIELD
// whose valid_keys never name a union key; enabled, nothing is refused.
func TestSlotRefusal_UnionHidden(t *testing.T) {
	t.Parallel()

	hosts := []string{featProcess, featCompose, featProcessChain, featFacet, featSample, featLookup}
	hidden := scopedOnly(hosts...)
	enabled := scopedOnly(append(hosts, featUnion)...)
	roots := []struct {
		name string
		set  func(union []types.UnionMember, field string) any
	}{
		{"request", func(u []types.UnionMember, f string) any {
			return &types.Request{Union: u, UnionSourceField: f}
		}},
		{"chain", func(u []types.UnionMember, f string) any {
			return &types.ChainRequest{Union: u, UnionSourceField: f}
		}},
		{"facet", func(u []types.UnionMember, f string) any {
			return &types.FacetRequest{Union: u, UnionSourceField: f}
		}},
		{"sample", func(u []types.UnionMember, f string) any {
			return &types.SampleRequest{Union: u, UnionSourceField: f}
		}},
		{"lookup", func(u []types.UnionMember, f string) any {
			return &types.LookupRequest{Union: u, UnionSourceField: f}
		}},
		{"compose slot", func(u []types.UnionMember, f string) any {
			return &types.ComposedRequest{Requests: []*types.Request{{Union: u, UnionSourceField: f}}}
		}},
	}
	for _, r := range roots {
		for _, c := range []struct {
			name  string
			union []types.UnionMember
			field string
			want  []string
		}{
			{"union", unionMembersFixture(), "", []string{"union"}},
			{"both", unionMembersFixture(), "src", []string{"union", "union_source_field"}},
			{"source field alone", nil, "src", []string{"union_source_field"}},
		} {
			t.Run(r.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()
				ce := asCoded(t, SlotRefusal(r.set(c.union, c.field), hidden))
				if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD {
					t.Fatalf("code = %s, want PULSE_REQUEST_UNKNOWN_FIELD", ce.Code)
				}
				if !reflect.DeepEqual(ce.Details["unknown_keys"], c.want) {
					t.Errorf("unknown_keys = %v, want %v", ce.Details["unknown_keys"], c.want)
				}
				valid, _ := ce.Details["valid_keys"].([]string)
				for _, k := range valid {
					if strings.HasPrefix(k, "union") {
						t.Errorf("valid_keys names hidden %q: %v", k, valid)
					}
				}
				if err := SlotRefusal(r.set(c.union, c.field), enabled); err != nil {
					t.Errorf("enabled instance refused: %v", err)
				}
			})
		}
	}
}

// TestPayloadSchema_UnionHidden pins the payload schema's view: hidden,
// no root carries a union property and the UnionMember def drops.
func TestPayloadSchema_UnionHidden(t *testing.T) {
	t.Parallel()

	base := []string{featProcess, featCompose, featProcessChain, featFacet, featSample, featLookup}
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(append(base, featUnion)...), true},
		{"hidden", scopedOnly(base...), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			for _, tok := range []string{`"union"`, `"union_source_field"`, `"UnionMember"`} {
				if got := strings.Contains(s, tok); got != c.want {
					t.Errorf("%s present = %v, want %v", tok, got, c.want)
				}
			}
		})
	}
}

// TestErrorCodes_UnionOwnedByCapability pins the union codes to
// capability:union: listed with it, hidden without it.
func TestErrorCodes_UnionOwnedByCapability(t *testing.T) {
	t.Parallel()

	base := []string{featProcess, featJoins}
	hidden := scopedOnly(base...)
	enabled := scopedOnly(append(base, featUnion)...)
	var n int
	for _, c := range errors.AllCodes() {
		if !strings.HasPrefix(string(c), "PULSE_UNION_") {
			continue
		}
		n++
		if ErrorCodeVisible(hidden, c) {
			t.Errorf("%s visible on an instance hiding capability:union", c)
		}
		if !ErrorCodeVisible(enabled, c) {
			t.Errorf("%s hidden on an instance offering capability:union", c)
		}
	}
	if n == 0 {
		t.Fatal("no PULSE_UNION_* code registered")
	}
}

// TestFeatureUnion_DependsOnUnionHosts pins capability:union's one
// host group: every root carrying the union slots.
func TestFeatureUnion_DependsOnUnionHosts(t *testing.T) {
	t.Parallel()

	for _, f := range Features() {
		if f.Name != FeatureUnion {
			continue
		}
		want := [][]string{{featProcess, featCompose, featProcessChain, featFacet, featSample, featLookup}}
		if !reflect.DeepEqual(f.DependsOn, want) {
			t.Errorf("DependsOn = %v, want %v", f.DependsOn, want)
		}
		if f.Since != BuiltinFeatureSince {
			t.Errorf("Since = %q", f.Since)
		}
		return
	}
	t.Fatal("capability:union has no feature row")
}

// TestManifest_UnionCapabilityFollowsInstance pins Manifest.Union: absent
// without capability:union, and its surfaces narrowed to the entry
// points the instance offers (predict, a core surface, always).
func TestManifest_UnionCapabilityFollowsInstance(t *testing.T) {
	t.Parallel()

	if m := BuildManifestForInstance(scopedOnly(featProcess, featSample)); m.Union != nil {
		t.Errorf("hidden: union block = %+v, want absent", m.Union)
	}
	m := BuildManifestForInstance(scopedOnly(featProcess, featSample, featUnion))
	if m.Union == nil {
		t.Fatal("enabled: union block absent")
	}
	if want := []string{"predict", "process", "sample"}; !reflect.DeepEqual(m.Union.Surfaces, want) {
		t.Errorf("surfaces = %v, want %v", m.Union.Surfaces, want)
	}
	if m.Union.DefaultSourceField != types.DefaultUnionSourceField || m.Union.MaxMembers <= 0 {
		t.Errorf("union block = %+v", m.Union)
	}
	full := BuildManifestForInstance(nil)
	if full.Union == nil || len(full.Union.Surfaces) != len(unionSurfaces) {
		t.Errorf("full registry: union block = %+v, want every surface", full.Union)
	}
}

// TestExampleDetector_UnionKey pins the `union` structural detector: a
// body carrying a union (at the root or in a Compose slot) requires
// capability:union, so an instance hiding it prunes the example; a
// body without one does not.
func TestExampleDetector_UnionKey(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		body string
		want bool
	}{
		{`{"union":[{"cohort":{"filename":"a.pulse"},"label":"a"}]}`, true},
		{`{"requests":[{"union":[{"cohort":{"filename":"a.pulse"},"label":"a"}]}]}`, true},
		{`{"cohort":{"filename":"a.pulse"},"aggregations":[{"type":"AGG_SET_UNION","field":"s"}]}`, false},
	} {
		got := slices.Contains(detectedExampleCapabilities(ontologyExample{}, decodeExampleBody([]byte(c.body))), featUnion)
		if got != c.want {
			t.Errorf("%s: capability:union detected = %v, want %v", c.body, got, c.want)
		}
	}
}
