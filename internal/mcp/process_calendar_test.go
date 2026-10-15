package mcp

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// TestMCPProcessRecordRows_RenderCalendarLiterals: pulse_process record
// rows (an ungrouped windowed run) render date as YYYY-MM-DD and
// datetime as RFC 3339 UTC, as the library Process does.
func TestMCPProcessRecordRows_RenderCalendarLiterals(t *testing.T) {
	sch := &encoding.Schema{Fields: []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate, ByteOffset: 0},
		{Name: "ts", Type: encoding.FieldTypeDateTime, ByteOffset: 4},
	}}
	var buf bytes.Buffer
	for _, err := range []error{
		encx.WritePreamble(&buf, sch),
		encoding.WriteFieldValue(&buf, encoding.FieldTypeDate, 20454),
		encoding.WriteFieldValue(&buf, encoding.FieldTypeDateTime, 1767323045),
		encoding.WriteBitmap(&buf, make([]byte, sch.BitmapByteSize())),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	afs := afero.NewMemMapFs()
	if err := afero.WriteFile(afs, "cal.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: afs})
	if err != nil {
		t.Fatal(err)
	}
	out := invokeTool(t, p, Config{}, "pulse_process",
		`{"cohort":{"filename":"cal.pulse"},"windows":[{"type":"WIN_ROW_NUMBER","label":"rn","order_by":[{"field":"d"}]}]}`)
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || len(resp.Data) != 1 {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if row := resp.Data[0]; row["d"] != "2026-01-01" || row["ts"] != "2026-01-02T03:04:05Z" {
		t.Errorf("row d=%#v ts=%#v, want 2026-01-01 / 2026-01-02T03:04:05Z", row["d"], row["ts"])
	}
}
