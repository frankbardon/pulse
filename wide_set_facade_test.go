package pulse

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Facade-level coverage for a cohort carrying a WIDE set column
// (set_u256). Every layer below already proves its own half —
// encoding/set_mask_test.go the word order, descriptor's inspect tests
// the dictionary truncation, service/index_*_test.go the sidecar — but
// nothing exercised Pulse.InspectEnvelope or Pulse.BuildIndex/Lookup
// against a real wide-set .pulse through the public facade, so the
// stride arithmetic that a 32-byte column forces on every neighbouring
// field's byte offset was only ever checked one layer down.
//
// The failure this guards is silent in exactly the way the effort's
// other wide-set defects were: a rung whose ByteSize is wrong reads the
// NEIGHBOURING column's bytes as the key, and a point lookup then
// returns a well-formed row for the wrong record.

const (
	wideSetOptionCount = 206
	wideSetRowCount    = 4
)

// writeWideSetCohort writes a two-field single-file cohort — a u32 "id"
// at offset 0 and a set_u256 "battery" at offset 4 over a 206-entry
// dictionary — mirroring the motivating SPSS multiple-dichotomy battery.
// Row r selects bits {r, 64+r, 200+r}: one below the old 64-bit ceiling,
// one immediately above it, one near the rung's 256-bit capacity.
func writeWideSetCohort(t *testing.T, memFs afero.Fs, path string) {
	t.Helper()

	dict := encoding.NewDictionary()
	for i := 0; i < wideSetOptionCount; i++ {
		if _, err := dict.Add(fmt.Sprintf("q%03d", i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "battery", Type: encoding.FieldTypeSetU256, ByteOffset: 4, Dictionary: dict},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < wideSetRowCount; r++ {
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], uint32(100+r))
		buf.Write(id[:])

		// The 32-byte payload is laid out by hand, four little-endian
		// words with words[0] = bits 0-63, rather than through
		// PutSetMask / FieldType.ByteSize. The fixture must be an
		// INDEPENDENT oracle: deriving the width from the same
		// production function the decoder uses would let a wrong
		// set_u256 stride stay self-consistent and invisible here.
		var words [4]uint64
		for _, bit := range wideSetBitsForRow(r) {
			words[bit/64] |= 1 << uint(bit%64)
		}
		payload := make([]byte, 32)
		for w := 0; w < 4; w++ {
			binary.LittleEndian.PutUint64(payload[w*8:w*8+8], words[w])
		}
		buf.Write(payload)
	}

	if err := afero.WriteFile(memFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func wideSetBitsForRow(r int) []int { return []int{r, 64 + r, 200 + r} }

func wideSetLabelsForRow(r int) []string {
	bits := wideSetBitsForRow(r)
	out := make([]string, len(bits))
	for i, b := range bits {
		out[i] = fmt.Sprintf("q%03d", b)
	}
	return out
}

// TestInspectEnvelope_WideSetCohort pins the no-execute read of a
// set_u256 cohort at the facade: the declared type name round-trips, the
// record count derives from a stride that includes the 32-byte column,
// and the 206-entry dictionary truncates to DefaultDictionaryLimit
// unless FullDict is asked for. InspectEnvelope (not Inspect) is the
// only way to reach the FullDict knob, per the predict-inspect contract.
func TestInspectEnvelope_WideSetCohort(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writeWideSetCohort(t, memFs, "battery.pulse")

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	env, err := p.InspectEnvelope(context.Background(), "battery.pulse", nil)
	if err != nil {
		t.Fatalf("InspectEnvelope: %v", err)
	}
	res, ok := env.Data.(*descriptor.InspectResult)
	if !ok {
		t.Fatalf("envelope data = %T, want *descriptor.InspectResult", env.Data)
	}
	if res.RecordCount != wideSetRowCount {
		t.Errorf("RecordCount = %d, want %d", res.RecordCount, wideSetRowCount)
	}

	battery := findInspectField(t, res, "battery")
	if battery.Type != "set_u256" {
		t.Errorf("battery.Type = %q, want %q", battery.Type, "set_u256")
	}
	if battery.Dictionary == nil {
		t.Fatalf("battery carries no dictionary")
	}
	if battery.Dictionary.TotalEntries != wideSetOptionCount {
		t.Errorf("TotalEntries = %d, want %d", battery.Dictionary.TotalEntries, wideSetOptionCount)
	}
	if !battery.Dictionary.Truncated {
		t.Errorf("Truncated = false, want true for a %d-entry dictionary", wideSetOptionCount)
	}
	if got := len(battery.Dictionary.Values); got != descriptor.DefaultDictionaryLimit {
		t.Errorf("truncated dictionary len = %d, want %d", got, descriptor.DefaultDictionaryLimit)
	}

	fullEnv, err := p.InspectEnvelope(context.Background(), "battery.pulse", &descriptor.InspectOptions{FullDict: true})
	if err != nil {
		t.Fatalf("InspectEnvelope(FullDict): %v", err)
	}
	fullRes, ok := fullEnv.Data.(*descriptor.InspectResult)
	if !ok {
		t.Fatalf("FullDict envelope data = %T, want *descriptor.InspectResult", fullEnv.Data)
	}
	full := findInspectField(t, fullRes, "battery")
	if full.Dictionary == nil {
		t.Fatalf("FullDict battery carries no dictionary")
	}
	if got := len(full.Dictionary.Values); got != wideSetOptionCount {
		t.Errorf("FullDict dictionary len = %d, want %d", got, wideSetOptionCount)
	}
}

// findInspectField returns the named field from an inspect result, or
// fails the test. Kept separate so both the truncated and the FullDict
// arm read the same way.
func findInspectField(t *testing.T, res *descriptor.InspectResult, name string) *descriptor.InspectField {
	t.Helper()
	for i := range res.Fields {
		if res.Fields[i].Name == name {
			return res.Fields[i]
		}
	}
	t.Fatalf("no %q field in inspect result", name)
	return nil
}

// TestBuildIndexAndLookup_WideSetCohort walks the point-lookup facade
// end to end over a cohort whose stride includes a 32-byte set column:
// BuildIndex on the non-set "id" key, then Lookup for a middle row. The
// returned row must carry the CORRECT id and the set column's labels
// from every word — including bit 64+r and bit 200+r, which a 64-bit
// read would drop without erroring.
func TestBuildIndexAndLookup_WideSetCohort(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writeWideSetCohort(t, memFs, "battery.pulse")

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := p.BuildIndex(context.Background(), "battery.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	idxPath := encoding.SidecarIndexPath("battery.pulse", []string{"id"})
	exists, err := afero.Exists(memFs, idxPath)
	if err != nil {
		t.Fatalf("afero.Exists: %v", err)
	}
	if !exists {
		t.Fatalf("sidecar index not written at %q", idxPath)
	}

	const wantRow = 2
	res, err := p.Lookup(context.Background(), &LookupRequest{
		Cohort: &types.Cohort{Filename: "battery.pulse"},
		Field:  "id",
		Value:  fmt.Sprintf("%d", 100+wantRow),
	})
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("len(Rows) = %d, want 1", len(res.Rows))
	}
	row := res.Rows[0]

	if got, ok := row["id"].(float64); !ok || int(got) != 100+wantRow {
		t.Errorf("row[id] = %v (%T), want %d", row["id"], row["id"], 100+wantRow)
	}

	labels, ok := row["battery"].([]string)
	if !ok {
		t.Fatalf("row[battery] = %v (%T), want []string", row["battery"], row["battery"])
	}
	want := wideSetLabelsForRow(wantRow)
	if len(labels) != len(want) {
		t.Fatalf("battery labels = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("battery labels = %v, want %v", labels, want)
		}
	}
}
