package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// TestHandleInspect_ShowsCohortDefaultZone: pulse_inspect reports a
// cohort's declared default zone field as top-level zone_field, read
// from the schema block; an undeclared cohort has no zone_field key.
func TestHandleInspect_ShowsCohortDefaultZone(t *testing.T) {
	t.Parallel()

	afs := afero.NewMemMapFs()
	write := func(path, zoneField string) {
		t.Helper()
		d := encoding.NewDictionary()
		if _, err := d.Add("Europe/Berlin"); err != nil {
			t.Fatal(err)
		}
		s := &encoding.Schema{Fields: []encoding.Field{
			{Name: "at", Type: encoding.FieldTypeDateTime},
			{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		}, ZoneField: zoneField}
		var b bytes.Buffer
		if err := encx.WritePreamble(&b, s); err != nil {
			t.Fatal(err)
		}
		if err := afero.WriteFile(afs, path, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("declared.pulse", "tz")
	write("plain.pulse", "")
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"declared.pulse": "tz", "plain.pulse": ""} {
		out, err := HandleInspect(context.Background(), p, InspectIn{Path: path})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]any
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		got, has := top["zone_field"]
		if has != (want != "") || (has && got != want) {
			t.Fatalf("%s: zone_field = %v (present %v), want %q", path, got, has, want)
		}
	}
}
