package imports

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

const zoneDefaultCSV = "local,zone\n" +
	"2026-07-01 12:00,America/New_York\n" +
	"2026-07-01 12:00,Asia/Tokyo\n" +
	"2026-07-01 12:00,Europe/Berlin\n"

// TestManager_Open_ZoneFieldWrittenAndPersisted: a managed import
// declaring ZoneField writes it into the cohort's schema and records it
// on the sidecar; a bad declaration is refused by option; a .pulse
// passthrough (never re-encoded) refuses it.
func TestManager_Open_ZoneFieldWrittenAndPersisted(t *testing.T) {
	t.Parallel()

	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "src.csv", []byte(zoneDefaultCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := m.Open(context.Background(), Spec{SourcePath: "src.csv", Handle: "rows", ZoneField: "zone"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := afero.ReadFile(afs, res.Path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(b)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil || s.ZoneField != "zone" {
		t.Fatalf("managed cohort ZoneField = %v (%v), want zone", s, err)
	}
	sc, err := m.readSidecar(res.Path + SidecarSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if sc.ZoneField != "zone" {
		t.Fatalf("sidecar zone_field = %q", sc.ZoneField)
	}

	_, err = m.Open(context.Background(), Spec{SourcePath: "src.csv", Handle: "bad", ZoneField: "local"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION || ce.Details["option"] != "zone_field" {
		t.Fatalf("non-categorical zone field = %v", err)
	}

	_, err = m.Open(context.Background(), Spec{SourcePath: res.Path, ZoneField: "zone"})
	if !stderrors.As(err, &ce) || ce.Code != perr.SERVICE_VALIDATION || ce.Details["reason"] != "passthrough" {
		t.Fatalf(".pulse passthrough with a zone field = %v", err)
	}
}

// TestSidecar_ZoneFieldOmittedWhenUnset: a sidecar without a declared
// zone field marshals exactly as before the field existed.
func TestSidecar_ZoneFieldOmittedWhenUnset(t *testing.T) {
	t.Parallel()

	sc := Sidecar{Handle: "h", Format: "pulse", SourceTZField: "zone"}
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"zone_field"`) {
		t.Fatalf("unset ZoneField reached the sidecar: %s", b)
	}
	sc.ZoneField = "zone"
	if b, _ = json.Marshal(sc); !strings.Contains(string(b), `"zone_field":"zone"`) {
		t.Fatalf("set ZoneField missing from the sidecar: %s", b)
	}
}
