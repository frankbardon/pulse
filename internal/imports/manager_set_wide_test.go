package imports

import (
	"context"
	stdjson "encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
)

// TestParseColumnTypeOverrides_WideSetRungs pins the sidecar's
// string -> FieldType channel at the wide rungs. The names travel as
// plain JSON strings, so an unparsed "set_u256" is rejected as an
// unknown type and the whole managed open fails — this is the seam
// that makes a wide-set override expressible at all.
func TestParseColumnTypeOverrides_WideSetRungs(t *testing.T) {
	cases := map[string]encoding.FieldType{
		"set_u8":   encoding.FieldTypeSetU8,
		"set_u64":  encoding.FieldTypeSetU64,
		"set_u128": encoding.FieldTypeSetU128,
		"set_u256": encoding.FieldTypeSetU256,
	}
	for name, want := range cases {
		out, err := parseColumnTypeOverrides(map[string]string{"issuers": name})
		if err != nil {
			t.Errorf("parseColumnTypeOverrides(%q): %v", name, err)
			continue
		}
		if out["issuers"] != want {
			t.Errorf("parseColumnTypeOverrides(%q) = %s, want %s", name, out["issuers"], want)
		}
		// The name must survive the return trip too — the sidecar is
		// rewritten from FieldType.String() on re-open.
		if got := out["issuers"].String(); got != name {
			t.Errorf("%s.String() = %q, want %q", want, got, name)
		}
	}
}

// TestSidecar_WideSetOverrideJSONRoundTrip confirms the override map
// survives the on-disk JSON shape unchanged for the wide rungs.
func TestSidecar_WideSetOverrideJSONRoundTrip(t *testing.T) {
	in := Sidecar{
		Handle:              "issuers",
		ColumnTypeOverrides: map[string]string{"a": "set_u128", "b": "set_u256"},
	}
	raw, err := stdjson.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Sidecar
	if err := stdjson.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ColumnTypeOverrides["a"] != "set_u128" || got.ColumnTypeOverrides["b"] != "set_u256" {
		t.Fatalf("ColumnTypeOverrides = %v, want a->set_u128 b->set_u256", got.ColumnTypeOverrides)
	}
	parsed, err := parseColumnTypeOverrides(got.ColumnTypeOverrides)
	if err != nil {
		t.Fatalf("parseColumnTypeOverrides after round-trip: %v", err)
	}
	if parsed["a"] != encoding.FieldTypeSetU128 || parsed["b"] != encoding.FieldTypeSetU256 {
		t.Errorf("parsed = %v, want set_u128 / set_u256", parsed)
	}
}

// TestManager_Open_WideSetOverridePersists walks the managed path
// end-to-end: a set_u256 force_type reaches the built schema and the
// sidecar records it verbatim for the next session.
//
// Row CONTENT is deliberately not asserted — token-to-mask assembly
// past 64 bits lands in E3-S2, so the row pass cannot pack a set_u256
// cell yet. This test pins the width CHOICE and its persistence, which
// is what E3-S1 owns.
func TestManager_Open_WideSetOverridePersists(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeSetCSV(t, afs, "issuers.csv")
	res, err := m.Open(context.Background(), Spec{
		SourcePath: "issuers.csv",
		ColumnTypeOverrides: map[string]string{
			"issuers": "set_u256",
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Schema == nil {
		t.Fatal("Result.Schema is nil")
	}
	f := res.Schema.Field("issuers")
	if f == nil {
		t.Fatal("schema has no issuers field")
	}
	if f.Type != encoding.FieldTypeSetU256 {
		t.Errorf("issuers type = %s, want set_u256", f.Type)
	}
	if f.Type.ByteSize() != 32 {
		t.Errorf("set_u256 stride contribution = %d, want 32", f.Type.ByteSize())
	}

	raw, err := afero.ReadFile(afs, res.Path+SidecarSuffix)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var sc Sidecar
	if err := stdjson.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("unmarshal sidecar: %v", err)
	}
	if sc.ColumnTypeOverrides["issuers"] != "set_u256" {
		t.Errorf("sidecar.ColumnTypeOverrides = %v, want issuers->set_u256",
			sc.ColumnTypeOverrides)
	}
}
