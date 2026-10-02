package descriptor

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// wantZoneCapabilities is the U03 zone-participation contract, spelled
// out independently of the production table.
var wantZoneCapabilities = map[string]string{
	"GROUP_DATE":         "capable",
	"GROUP_DATE_RANGES":  "capable",
	"FILTER_DATE_RANGES": "capable",
	"ATTR_DATE_PART":     "capable",
	"FEAT_DATE_FEATURES": "capable",
	"OVERLAY_YOY":        "following",
}

// TestZoneCapabilities_ExactSet: the declaration table holds exactly the
// contract set — nothing missing, nothing extra.
func TestZoneCapabilities_ExactSet(t *testing.T) {
	if !reflect.DeepEqual(zoneCapabilities, wantZoneCapabilities) {
		t.Fatalf("zoneCapabilities = %v, want %v", zoneCapabilities, wantZoneCapabilities)
	}
}

// TestZoneCapabilities_EveryRegisteredName: every registered built-in
// operator and overlay kind reports exactly the contract value through
// ZoneCapabilityOf, and every name in the contract is a registered one.
func TestZoneCapabilities_EveryRegisteredName(t *testing.T) {
	var names []string
	for _, v := range types.AllGroupTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllFiltererTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllAttributeTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllFeatureTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllAggregationTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllWindowTypes() {
		names = append(names, string(v))
	}
	for _, v := range types.AllOverlayKinds() {
		names = append(names, string(v))
	}
	seen := map[string]bool{}
	for _, n := range names {
		seen[n] = true
		if got, want := ZoneCapabilityOf(n), wantZoneCapabilities[n]; got != want {
			t.Errorf("ZoneCapabilityOf(%s) = %q, want %q", n, got, want)
		}
		if got, want := IsZoneCapable(n), wantZoneCapabilities[n] == ZoneCapable; got != want {
			t.Errorf("IsZoneCapable(%s) = %v, want %v", n, got, want)
		}
	}
	for n := range wantZoneCapabilities {
		if !seen[n] {
			t.Errorf("contract names %s, which is not a registered operator or overlay kind", n)
		}
	}
	if ZoneCapabilityOf("AGG_EXT_CUSTOM_X") != "" || IsZoneCapable("GROUP_EXT_DATE_X") {
		t.Errorf("an unknown / extension name reported a zone capability")
	}
}

// TestManifest_ZoneKey: the manifest carries the declaration on the
// `zone` key of each operator / overlay entry and omits it elsewhere.
func TestManifest_ZoneKey(t *testing.T) {
	m := BuildManifest()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire struct {
		Components map[string][]map[string]json.RawMessage `json:"components"`
		Overlays   []map[string]json.RawMessage            `json:"overlays"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := map[string]string{}
	collect := func(nameKey string, e map[string]json.RawMessage) {
		raw, ok := e["zone"]
		if !ok {
			return
		}
		var name, zone string
		_ = json.Unmarshal(e[nameKey], &name)
		_ = json.Unmarshal(raw, &zone)
		got[name] = zone
	}
	cats := make([]string, 0, len(wire.Components))
	for c := range wire.Components {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		for _, e := range wire.Components[c] {
			collect("name", e)
		}
	}
	for _, e := range wire.Overlays {
		collect("kind", e)
	}
	if !reflect.DeepEqual(got, wantZoneCapabilities) {
		t.Fatalf("manifest zone keys = %v, want %v", got, wantZoneCapabilities)
	}
	found := false
	for _, op := range SlimManifest(m).Components.Groupers {
		if op.Name == string(types.GROUP_DATE) {
			found = true
			if op.Zone != ZoneCapable {
				t.Fatalf("SlimManifest GROUP_DATE zone = %q, want %q", op.Zone, ZoneCapable)
			}
		}
	}
	if !found {
		t.Fatalf("SlimManifest lacks GROUP_DATE")
	}
}
