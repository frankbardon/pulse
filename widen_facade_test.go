package pulse

import (
	"bytes"
	"context"
	"encoding/binary"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Facade coverage for Pulse.WidenSetField. The engine
// (encoding/widen_test.go) proves the byte-level rewrite and the service
// layer (service/widen_field_test.go) proves the layout dispatch; what is
// only reachable HERE is the type-NAME resolution, which is the facade's
// own contract: an unknown name must be a coded refusal, never the silent
// f64 fallback internal/cli/fieldtype.go performs for display purposes.

const widenFacadeSetOptions = 40

// writeWidenFacadeCohort writes a two-field cohort — id u32, picks set_u64 —
// carrying a dictionary of widenFacadeSetOptions entries. The set payload is
// laid out by hand (one little-endian word) rather than through PutSetMask,
// so the fixture stays an independent oracle of the narrow-rung layout.
func writeWidenFacadeCohort(t *testing.T, memFs afero.Fs, path string) []byte {
	t.Helper()

	dict := encoding.NewDictionary()
	for i := 0; i < widenFacadeSetOptions; i++ {
		if _, err := dict.Add(fmt.Sprintf("opt%02d", i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "picks", Type: encoding.FieldTypeSetU64, ByteOffset: 4, Dictionary: dict},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for r := 0; r < 4; r++ {
		var id [4]byte
		binary.LittleEndian.PutUint32(id[:], uint32(10+r))
		buf.Write(id[:])

		var word uint64
		for _, bit := range widenFacadeBitsForRow(r) {
			word |= 1 << uint(bit)
		}
		var payload [8]byte
		binary.LittleEndian.PutUint64(payload[:], word)
		buf.Write(payload[:])
	}

	data := buf.Bytes()
	if err := afero.WriteFile(memFs, path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return data
}

func widenFacadeBitsForRow(r int) []int { return []int{r, 20 + r, 39} }

func newWidenFacadePulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	memFs := afero.NewMemMapFs()
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p, memFs
}

func TestWidenSetField_FacadeWidensByTypeName(t *testing.T) {
	p, memFs := newWidenFacadePulse(t)
	before := writeWidenFacadeCohort(t, memFs, "cohort.pulse")

	rep, err := p.WidenSetField(context.Background(), "cohort.pulse", "picks", "set_u128")
	if err != nil {
		t.Fatalf("WidenSetField: %v", err)
	}
	if rep.From != encoding.FieldTypeSetU64 || rep.To != encoding.FieldTypeSetU128 {
		t.Errorf("report rungs = %s -> %s, want set_u64 -> set_u128", rep.From, rep.To)
	}
	if rep.Records != 4 {
		t.Errorf("report Records = %d, want 4", rep.Records)
	}

	// The widened cohort must still inspect as a cohort, at the new rung,
	// with the dictionary intact — the whole point of a widen is that the
	// file stays usable and bit i still means dictionary entry i.
	res, err := p.Inspect(context.Background(), "cohort.pulse")
	if err != nil {
		t.Fatalf("Inspect after widen: %v", err)
	}
	var picks *string
	for i := range res.Fields {
		if res.Fields[i].Name == "picks" {
			ty := res.Fields[i].Type
			picks = &ty
		}
	}
	if picks == nil {
		t.Fatal("picks field missing from the widened cohort's schema")
	}
	if *picks != "set_u128" {
		t.Errorf("picks type after widen = %s, want set_u128", *picks)
	}

	after, err := afero.ReadFile(memFs, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Equal(before, after) {
		t.Error("cohort bytes unchanged after a successful widen")
	}
}

// An unknown --to value must NOT resolve to some default type. The CLI's
// display-side parseFieldType falls back to f64 on an unknown name; if that
// fallback ever reached this path the user would get "f64 is not a set type"
// for a typo'd rung, or worse, a rewrite to a type they never asked for.
func TestWidenSetField_FacadeRejectsUnknownTypeName(t *testing.T) {
	p, memFs := newWidenFacadePulse(t)
	before := writeWidenFacadeCohort(t, memFs, "cohort.pulse")

	_, err := p.WidenSetField(context.Background(), "cohort.pulse", "picks", "set_u512")
	if err == nil {
		t.Fatal("widen to an unknown type name succeeded")
	}
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("unknown-type-name error %v carries no coded error", err)
	}
	if ce.Code != perrors.ENCODING_TYPE_MISMATCH {
		t.Errorf("unknown-type-name code = %s, want ENCODING_TYPE_MISMATCH", ce.Code)
	}
	if got := ce.Details["target"]; got != "set_u512" {
		t.Errorf("details[target] = %v, want set_u512", got)
	}

	after, err := afero.ReadFile(memFs, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("cohort bytes changed under a refused widen")
	}
}
