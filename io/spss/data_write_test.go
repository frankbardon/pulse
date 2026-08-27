package spss

import (
	"bytes"
	"context"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// planFor emits a dictionary for a schema with no sidecar, under the given
// options, with the case count left UNKNOWN — the shape a buffering writer
// starts from.
func planFor(t *testing.T, s *encoding.Schema, opts WriterOptions) *DictionaryPlan {
	t.Helper()
	return emit(t, DictionaryRequest{
		Schema:      s,
		Cases:       -1,
		Compression: opts.Compression(),
		Options:     opts,
	})
}

// encodeCases runs a fixed list of cases through an encoder and returns the
// complete file.
func encodeCases(t *testing.T, plan *DictionaryPlan, s *encoding.Schema, cases ...Case) []byte {
	t.Helper()
	enc, err := NewDataEncoder(plan, s)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}
	for i, c := range cases {
		if err := enc.WriteCase(c); err != nil {
			t.Fatalf("WriteCase %d: %v", i, err)
		}
	}
	data, err := enc.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return joinSections(plan.Bytes, data)
}

// joinSections joins the dictionary and the data section into a file. It copies
// rather than appending in place, so a plan's own bytes are never aliased by
// the file built from them.
func joinSections(dict, data []byte) []byte {
	out := make([]byte, 0, len(dict)+len(data))
	out = append(out, dict...)
	return append(out, data...)
}

// exportCohort writes a whole cohort out as a `.sav` under the given options,
// the way a writer will: resolve the sidecar, emit the dictionary with an
// unknown case count, stream the records, patch.
func exportCohort(t *testing.T, fs afero.Fs, cohort string, opts WriterOptions) []byte {
	t.Helper()
	res, err := LoadSidecar(fs, cohort, opts)
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	s := cohortSchema(t, fs, cohort)
	plan := emit(t, DictionaryRequest{
		Schema:      s,
		Sidecar:     res,
		Cases:       -1,
		Compression: opts.Compression(),
		Options:     opts,
	})

	enc, err := NewDataEncoder(plan, s)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}
	f, err := fs.Open(cohort)
	if err != nil {
		t.Fatalf("opening cohort: %v", err)
	}
	defer f.Close()
	if err := encoding.ReadHeader(f); err != nil {
		t.Fatalf("cohort header: %v", err)
	}
	if _, err := encoding.ReadSchema(f); err != nil {
		t.Fatalf("cohort schema: %v", err)
	}
	if err := enc.WriteCohort(f); err != nil {
		t.Fatalf("WriteCohort: %v", err)
	}
	data, err := enc.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return joinSections(plan.Bytes, data)
}

// flatCases runs an emitted file back through the E3-S1 read path and returns
// the flat case bytes, whatever encoding the data section used.
func flatCases(t *testing.T, sav []byte) []byte {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("the emitted file's dictionary does not parse: %v", err)
	}
	dp, err := buildDataPlan(d)
	if err != nil {
		t.Fatalf("buildDataPlan: %v", err)
	}
	flat, _, err := readCaseData(d, sav, dp)
	if err != nil {
		t.Fatalf("the emitted data section does not read: %v", err)
	}
	return flat
}

// savRows reads an emitted file through this package's own pio.Reader.
func savRows(t *testing.T, sav []byte) ([]string, [][]string) {
	t.Helper()
	r := NewReaderFromBytes(sav)
	head, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader on the emitted file: %v", err)
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows on the emitted file: %v", err)
	}
	return head, rows
}

// reimport runs an emitted `.sav` back through the shared import path and
// returns the cohort bytes it produced.
func reimport(t *testing.T, sav []byte) []byte {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "round.sav", sav, 0644); err != nil {
		t.Fatalf("writing the emitted file: %v", err)
	}
	job := pio.NewImportJob(NewReader(fs, "round.sav"), "round.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("re-importing the emitted file: %v", err)
	}
	out, err := afero.ReadFile(fs, "round.pulse")
	if err != nil {
		t.Fatalf("reading the re-imported cohort: %v", err)
	}
	return out
}

// numericSchema is three plain u8 columns — the smallest shape whose
// compressed stream can be written out by hand.
func numericSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "A", Type: encoding.FieldTypeU8},
		{Name: "B", Type: encoding.FieldTypeU8},
		{Name: "C", Type: encoding.FieldTypeU8},
	}}
}

// ---------------------------------------------------------------------------
// The default, and the flag
// ---------------------------------------------------------------------------

// TestWriterOptions_CompressionDefaultsToBytecode pins the default write
// mode. SPSS's own SAVE writes flag 1, so a zero-valued WriterOptions must
// too — a writer defaulting to the rarer encoding would produce files that
// are correct but unlike every other `.sav` in existence.
func TestWriterOptions_CompressionDefaultsToBytecode(t *testing.T) {
	if got := (WriterOptions{}).Compression(); got != compressionBytecode {
		t.Errorf("WriterOptions{}.Compression() = %d, want %d (bytecode)", got, compressionBytecode)
	}
	if got := (WriterOptions{Uncompressed: true}).Compression(); got != compressionNone {
		t.Errorf("WriterOptions{Uncompressed: true}.Compression() = %d, want %d (none)", got, compressionNone)
	}

	// The flag must reach the header, not merely the encoder: the header
	// flag is the ONLY thing separating a readable data section from a
	// stream of command bytes.
	for _, tc := range []struct {
		opts WriterOptions
		want int32
	}{
		{WriterOptions{}, compressionBytecode},
		{WriterOptions{Uncompressed: true}, compressionNone},
	} {
		plan := planFor(t, numericSchema(), tc.opts)
		d := reparse(t, plan)
		if d.header.compression != tc.want {
			t.Errorf("Uncompressed=%v emitted header compression %d, want %d",
				tc.opts.Uncompressed, d.header.compression, tc.want)
		}
	}
}

// TestDataEncoder_RefusesZSAV states the out-of-scope criterion as a check.
func TestDataEncoder_RefusesZSAV(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})
	plan.Compression = compressionZSAV

	_, err := NewDataEncoder(plan, s)
	if err == nil {
		t.Fatal("NewDataEncoder accepted ZSAV; emission of it is not implemented")
	}
	if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED)
	}

	// And the dictionary front-end refuses it too, so there is no order of
	// calls that produces a ZSAV header.
	if _, err := BuildDictionary(DictionaryRequest{
		Schema: s, Cases: 0, Compression: compressionZSAV,
	}); err == nil {
		t.Error("BuildDictionary accepted ZSAV")
	}
}

// ---------------------------------------------------------------------------
// Block framing, byte by byte
// ---------------------------------------------------------------------------

// TestDataEncoder_BlockFramingIsHandVerifiable writes a stream small enough
// to state in full.
//
// Three u8 columns and three cases is nine elements. Every value is a small
// whole number, so every element is one integer command — `value + bias`,
// with the conventional bias of 100 — and no command needs a payload. Nine
// commands is one full block of eight plus one, so the stream is:
//
//	block 1: 101 102 103 104 105 106 107 108
//	block 2: 109 252   0   0   0   0   0   0
//
// where 252 terminates the stream and the six zeros pad the block out. That
// is 16 bytes for 72 bytes of uncompressed case data.
func TestDataEncoder_BlockFramingIsHandVerifiable(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})
	sav := encodeCases(t, plan, s,
		Case{{Num: 1}, {Num: 2}, {Num: 3}},
		Case{{Num: 4}, {Num: 5}, {Num: 6}},
		Case{{Num: 7}, {Num: 8}, {Num: 9}},
	)

	got := sav[len(plan.Bytes):]
	want := []byte{
		101, 102, 103, 104, 105, 106, 107, 108,
		109, cmdEOF, 0, 0, 0, 0, 0, 0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("data section = % d\nwant            % d", got, want)
	}
}

// TestDataEncoder_PayloadsFollowTheWholeBlock is the framing subtlety a
// writer gets wrong: a verbatim value belongs after ALL eight command bytes,
// not immediately after the command that named it.
func TestDataEncoder_PayloadsFollowTheWholeBlock(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})

	// 300 is past the integer command range (300 + 100 = 400 > 251) and
	// 0.5 is not a whole number, so both need command 253 and an 8-byte
	// payload. They sit at elements 0 and 2 of the first case, with an
	// ordinary integer command between them.
	sav := encodeCases(t, plan, s,
		Case{{Num: 300}, {Num: 7}, {Num: 0.5}},
		Case{{Num: 1}, {Num: 2}, {Num: 3}},
	)
	got := sav[len(plan.Bytes):]

	var want []byte
	want = append(want, cmdRaw, 107, cmdRaw, 101, 102, 103, cmdEOF, cmdPad)
	var buf [8]byte
	plan.ByteOrder.PutUint64(buf[:], math.Float64bits(300))
	want = append(want, buf[:]...)
	plan.ByteOrder.PutUint64(buf[:], math.Float64bits(0.5))
	want = append(want, buf[:]...)

	if !bytes.Equal(got, want) {
		t.Fatalf("data section = % d\nwant            % d", got, want)
	}
}

// TestDataEncoder_CommandChoice is the encode side of the E3-S1 command
// table: which command each kind of value earns.
func TestDataEncoder_CommandChoice(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})
	enc, err := NewDataEncoder(plan, s)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}

	elem := func(v float64) []byte {
		var b [8]byte
		plan.ByteOrder.PutUint64(b[:], math.Float64bits(v))
		return b[:]
	}

	for _, tc := range []struct {
		name string
		kind elementKind
		seg  []byte
		want byte
	}{
		{"zero is the bias itself", elemNumeric, elem(0), 100},
		{"the smallest integer command", elemNumeric, elem(-99), cmdIntMin},
		{"the largest integer command", elemNumeric, elem(151), cmdIntMax},
		{"one past the bottom needs a payload", elemNumeric, elem(-100), cmdRaw},
		{"one past the top needs a payload", elemNumeric, elem(152), cmdRaw},
		{"a fraction needs a payload", elemNumeric, elem(1.5), cmdRaw},
		{"NaN needs a payload", elemNumeric, elem(math.NaN()), cmdRaw},
		{"infinity needs a payload", elemNumeric, elem(math.Inf(1)), cmdRaw},
		{"negative zero needs a payload", elemNumeric, elem(math.Copysign(0, -1)), cmdRaw},
		{"the sysmis sentinel", elemNumeric, elem(defaultSysmis), cmdSysmis},
		{"eight spaces", elemString, []byte("        "), cmdSpaces},
		{"any other string segment", elemString, []byte("ALPHA   "), cmdRaw},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := enc.command(tc.kind, tc.seg); got != tc.want {
				t.Errorf("command = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDataEncoder_NeverEmitsACommandTheDecoderRefuses walks the emitted
// stream against the same elementKind.allows table the decoder checks it
// with. A command the decoder would reject is a file we wrote and cannot
// read.
func TestDataEncoder_NeverEmitsACommandTheDecoderRefuses(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())
	sav := exportCohort(t, fs, cohort, WriterOptions{})

	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing the emitted file: %v", err)
	}
	dp, err := buildDataPlan(d)
	if err != nil {
		t.Fatalf("buildDataPlan: %v", err)
	}

	st := newBytecodeStream(sav[d.dataOffset:], fileLocator(d.dataOffset))
	elem := 0
	seen := 0
	for {
		cmd, _, ok := st.next()
		if !ok || cmd == cmdEOF {
			break
		}
		if !dp.elemKinds[elem].allows(cmd) {
			t.Fatalf("element %d of a case got command %d, which the decoder refuses for %s",
				elem, cmd, dp.elemKinds[elem].description())
		}
		if cmd == cmdRaw {
			if _, _, ok := st.payload(); !ok {
				t.Fatalf("command 253 at element %d has no payload", elem)
			}
		}
		seen++
		elem++
		if elem == len(dp.elemKinds) {
			elem = 0
		}
	}
	if elem != 0 {
		t.Errorf("the stream ends %d element(s) into a case", elem)
	}
	if seen == 0 {
		t.Fatal("the stream produced no elements at all")
	}
}

// ---------------------------------------------------------------------------
// The two modes carry the same data
// ---------------------------------------------------------------------------

// TestDataEncoder_ModesAreByteIdenticalAfterDecoding is the acceptance
// criterion stated at the strongest level it can be: the compressed section
// and the uncompressed section are not merely equivalent, they expand to the
// SAME bytes.
func TestDataEncoder_ModesAreByteIdenticalAfterDecoding(t *testing.T) {
	fs, cohort, _ := importFixture(t, richSpec())

	packed := exportCohort(t, fs, cohort, WriterOptions{})
	plain := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true})

	if bytes.Equal(packed, plain) {
		t.Fatal("the two modes produced identical FILES; the compression flag did not reach the data section")
	}
	if len(packed) >= len(plain) {
		t.Errorf("the compressed file is %d bytes and the uncompressed %d; compression bought nothing",
			len(packed), len(plain))
	}

	if a, b := flatCases(t, packed), flatCases(t, plain); !bytes.Equal(a, b) {
		t.Fatalf("the two modes decode to different case bytes:\ncompressed:   %x\nuncompressed: %x", a, b)
	}

	// The dictionary halves differ only in the compression flag, so the
	// case counts must have been patched identically on both.
	dp, err := parseDictionary(packed)
	if err != nil {
		t.Fatalf("parsing the compressed file: %v", err)
	}
	dn, err := parseDictionary(plain)
	if err != nil {
		t.Fatalf("parsing the uncompressed file: %v", err)
	}
	if dp.header.caseCount != dn.header.caseCount {
		t.Errorf("case counts differ: compressed %d, uncompressed %d",
			dp.header.caseCount, dn.header.caseCount)
	}
}

// TestDataEncoder_ModesReimportToIdenticalCohorts is the same criterion at
// the level a user meets it.
func TestDataEncoder_ModesReimportToIdenticalCohorts(t *testing.T) {
	for name, spec := range map[string]spsstest.Spec{
		"the rich fixture": richSpec(),
		"strings, dates and missing values": {
			Vars: []spsstest.Var{
				{Name: "Q1", Label: "Satisfaction", Measure: spsstest.MeasureOrdinal,
					Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
					Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(99)}}},
				{Name: "WHEN", Print: spsstest.Format{Type: spsstest.FormatType(20), Width: 11}},
				{Name: "NOTES", Width: 40, Measure: spsstest.MeasureNominal},
			},
			Cases: [][]spsstest.Value{
				{spsstest.Num(1), spsstest.Num(13166064000), spsstest.Text("first")},
				{spsstest.Num(99), spsstest.Num(13168742400), spsstest.Text("")},
				{spsstest.SysMis(), spsstest.Num(13171334400), spsstest.Text("third")},
			},
			DisplayParams:     true,
			CharacterEncoding: "UTF-8",
		},
	} {
		fs, cohort, _ := importFixture(t, spec)
		packedSav := exportCohort(t, fs, cohort, WriterOptions{})
		plainSav := exportCohort(t, fs, cohort, WriterOptions{Uncompressed: true})

		// Every case the fixture declared must be in both files. Two
		// EMPTY data sections are also "identical", and that is exactly
		// the failure this criterion would otherwise not notice.
		_, packedRows := savRows(t, packedSav)
		_, plainRows := savRows(t, plainSav)
		if len(packedRows) != len(spec.Cases) {
			t.Fatalf("%s: the compressed file carries %d case(s), want %d",
				name, len(packedRows), len(spec.Cases))
		}
		for i := range packedRows {
			if !equalStrings(packedRows[i], plainRows[i]) {
				t.Errorf("%s: row %d reads back as %v compressed and %v uncompressed",
					name, i, packedRows[i], plainRows[i])
			}
		}

		packed := reimport(t, packedSav)
		plain := reimport(t, plainSav)
		if !bytes.Equal(packed, plain) {
			t.Errorf("%s: the two modes re-import to different cohorts (%d vs %d bytes)",
				name, len(packed), len(plain))
		}
	}
}

// ---------------------------------------------------------------------------
// Values
// ---------------------------------------------------------------------------

// TestDataEncoder_ValuesSurviveTheWriteAndRead reads an emitted file back
// through this package's own reader and checks the cells.
func TestDataEncoder_ValuesSurviveTheWriteAndRead(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "income", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north", "south")},
		{Name: "signed_up", Type: encoding.FieldTypeDate},
		{Name: "last_seen", Type: encoding.FieldTypeDateTime},
	}}

	// 2024-03-04 is epoch day 19786; 2024-03-04T05:06:07Z is 1709528767.
	const day = 19786
	const instant = 1709528767

	for _, mode := range []WriterOptions{{}, {Uncompressed: true}} {
		plan := planFor(t, s, mode)
		sav := encodeCases(t, plan, s,
			Case{{Num: 1}, {Num: 1234.5}, {Num: 0}, {Num: day}, {Num: instant}},
			Case{{Num: 2}, {Null: true}, {Num: 1}, {Num: day + 1}, {Num: instant + 60}},
		)

		head, rows := savRows(t, sav)
		if want := []string{"id", "income", "region", "signed_up", "last_seen"}; !equalStrings(head, want) {
			t.Fatalf("header = %v, want %v", head, want)
		}
		want := [][]string{
			{"1", "1234.5", "north", "2024-03-04", "2024-03-04T05:06:07Z"},
			{"2", "", "south", "2024-03-05", "2024-03-04T05:07:07Z"},
		}
		for i := range want {
			if i >= len(rows) {
				t.Fatalf("uncompressed=%v: got %d row(s), want %d", mode.Uncompressed, len(rows), len(want))
			}
			if !equalStrings(rows[i], want[i]) {
				t.Errorf("uncompressed=%v row %d = %v, want %v", mode.Uncompressed, i, rows[i], want[i])
			}
		}
	}
}

// TestDataEncoder_NullsBecomeTheRightMissingState pins the two shapes of
// "missing" a `.sav` has, which are not the same shape.
func TestDataEncoder_NullsBecomeTheRightMissingState(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "s", Type: encoding.FieldTypeCategoricalU8, Nullable: true,
			Dictionary: dictOf(t, "alpha")},
	}}
	plan := planFor(t, s, WriterOptions{Uncompressed: true})
	sav := encodeCases(t, plan, s, Case{{Null: true}, {Null: true}})

	flat := flatCases(t, sav)
	if len(flat) < 8 {
		t.Fatalf("the data section holds %d byte(s)", len(flat))
	}

	// A numeric null is the system-missing SENTINEL — the one missing
	// state the format has a value for.
	if got := math.Float64frombits(plan.ByteOrder.Uint64(flat[:8])); got != defaultSysmis {
		t.Errorf("a null numeric wrote %v, want the sysmis sentinel %v", got, defaultSysmis)
	}
	// A string null is BLANK. There is no string sentinel, and a blank
	// reads back as null, so the round trip closes.
	if got := flat[8:13]; !bytes.Equal(got, []byte("     ")) {
		t.Errorf("a null string wrote %q, want spaces", got)
	}

	_, rows := savRows(t, sav)
	if len(rows) != 1 || rows[0][0] != "" || rows[0][1] != "" {
		t.Errorf("the null row read back as %v, want two empty cells", rows)
	}
}

// TestDataEncoder_SetColumnKeepsItsThreeStates is the fidelity claim the
// whole set_* mapping rests on, exercised in the write direction.
//
// A multiple-dichotomy set has THREE row states — something selected, nothing
// selected, never asked — and the writer must not collapse the last two. A
// null set writes every member system-missing (no constituent carried a
// value); an EMPTY mask writes every member 0 (a constituent said "not
// this one").
func TestDataEncoder_SetColumnKeepsItsThreeStates(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "media", Type: encoding.FieldTypeSetU8, Nullable: true,
			Dictionary: dictOf(t, "tv", "web")},
	}}
	plan := planFor(t, s, WriterOptions{})
	sav := encodeCases(t, plan, s,
		Case{{Mask: 0b01}}, // tv only
		Case{{Mask: 0b11}}, // both
		Case{{Mask: 0}},    // answered, nothing selected
		Case{{Null: true}}, // never asked
	)

	head, rows := savRows(t, sav)
	setAt := -1
	for i, name := range head {
		if name == "media" {
			setAt = i
		}
	}
	if setAt < 0 {
		t.Fatalf("the re-read file has no derived set column: %v", head)
	}
	want := []string{"tv", "tv|web", setEmptySelection, ""}
	for i, w := range want {
		if i >= len(rows) {
			t.Fatalf("got %d row(s), want %d", len(rows), len(want))
		}
		if got := rows[i][setAt]; got != w {
			t.Errorf("row %d set cell = %q, want %q", i, got, w)
		}
	}
}

// TestDataEncoder_NegativeZeroSurvivesCompression is why the integer command
// is verified rather than trusted: -0.0 is a whole number in the compressible
// range whose integer command decodes to +0.0, a different double.
func TestDataEncoder_NegativeZeroSurvivesCompression(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "v", Type: encoding.FieldTypeF64},
	}}
	plan := planFor(t, s, WriterOptions{})
	sav := encodeCases(t, plan, s, Case{{Num: math.Copysign(0, -1)}})

	flat := flatCases(t, sav)
	if len(flat) != 8 {
		t.Fatalf("the data section decodes to %d byte(s), want 8", len(flat))
	}
	if got := plan.ByteOrder.Uint64(flat); got != math.Float64bits(math.Copysign(0, -1)) {
		t.Errorf("-0.0 came back as %v (bits %#x)", math.Float64frombits(got), got)
	}
}

// ---------------------------------------------------------------------------
// The case count
// ---------------------------------------------------------------------------

// TestDataEncoder_PatchesBothCaseCounts is the buffering criterion's trap: a
// `.sav` states its case count twice, and patching one leaves a file whose
// two counts disagree.
func TestDataEncoder_PatchesBothCaseCounts(t *testing.T) {
	s := numericSchema()

	t.Run("a plan that declared a count carries both", func(t *testing.T) {
		plan := emit(t, DictionaryRequest{Schema: s, Cases: 1, Compression: compressionBytecode})
		if plan.CaseCount64Offset < 0 {
			t.Fatal("a plan built with a known count emitted no record 7/16")
		}
		sav := encodeCases(t, plan, s,
			Case{{Num: 1}, {Num: 2}, {Num: 3}},
			Case{{Num: 4}, {Num: 5}, {Num: 6}},
		)
		d, err := parseDictionary(sav)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if d.header.caseCount != 2 {
			t.Errorf("the header declares %d case(s), want 2", d.header.caseCount)
		}
		if !d.hasCaseCount64 || d.caseCount64 != 2 {
			t.Errorf("record 7/16 declares %d (present=%v), want 2",
				d.caseCount64, d.hasCaseCount64)
		}
	})

	t.Run("a plan that declared none carries only the header", func(t *testing.T) {
		plan := planFor(t, s, WriterOptions{})
		if plan.CaseCount64Offset != -1 {
			t.Fatal("a plan built with Cases: -1 emitted a record 7/16, which cannot state an unknown count")
		}
		sav := encodeCases(t, plan, s, Case{{Num: 1}, {Num: 2}, {Num: 3}})
		d, err := parseDictionary(sav)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if d.header.caseCount != 1 {
			t.Errorf("the header declares %d case(s), want 1", d.header.caseCount)
		}
		if d.hasCaseCount64 {
			t.Error("a record 7/16 appeared out of nowhere")
		}
	})

	// The declared count and what the section holds must agree, or the
	// reader warns PULSE_SPSS_DATA_CASE_COUNT_MISMATCH on every file we
	// write.
	t.Run("the reader raises no count mismatch", func(t *testing.T) {
		fs, cohort, _ := importFixture(t, richSpec())
		sav := exportCohort(t, fs, cohort, WriterOptions{})
		r := NewReaderFromBytes(sav)
		if _, err := r.ReadHeader(); err != nil {
			t.Fatalf("ReadHeader: %v", err)
		}
		if err := r.ReadRows(context.Background(), func([]string) error { return nil }); err != nil {
			t.Fatalf("ReadRows: %v", err)
		}
		for _, w := range r.Warnings() {
			if w.Code == perr.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH {
				t.Errorf("the emitted file warns on its own case count: %v", w)
			}
		}
	})
}

// TestSetCaseCount_WritesBothOrRefuses covers the patch helper on its own.
func TestSetCaseCount_WritesBothOrRefuses(t *testing.T) {
	s := numericSchema()

	plan := emit(t, DictionaryRequest{Schema: s, Cases: 0, Compression: compressionNone})
	if err := plan.SetCaseCount(7); err != nil {
		t.Fatalf("SetCaseCount: %v", err)
	}
	if plan.CaseCount != 7 {
		t.Errorf("plan.CaseCount = %d, want 7", plan.CaseCount)
	}
	d, err := parseDictionary(plan.Bytes)
	if err != nil {
		t.Fatalf("parsing the patched dictionary: %v", err)
	}
	if d.header.caseCount != 7 || d.caseCount64 != 7 {
		t.Errorf("patched counts are header=%d, 7/16=%d; want 7 and 7",
			d.header.caseCount, d.caseCount64)
	}

	if err := plan.SetCaseCount(-1); err == nil {
		t.Error("SetCaseCount accepted -1; an unknown count is declared by emitting none, not by patching one in")
	}
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// TestDataEncoder_RefusesRatherThanDegrades is the governing principle as a
// table: every one of these could be written as SOMETHING, and every one of
// those somethings is a plausible wrong answer.
func TestDataEncoder_RefusesRatherThanDegrades(t *testing.T) {
	textSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north", "south")},
	}}

	for _, tc := range []struct {
		name   string
		schema *encoding.Schema
		mutate func(*DictionaryPlan)
		c      Case
	}{
		{
			name:   "a dictionary ID the plan records no value for",
			schema: textSchema,
			c:      Case{{Num: 9}},
		},
		{
			name:   "a categorical whose SPSS code was never recorded",
			schema: textSchema,
			mutate: func(p *DictionaryPlan) {
				p.Columns[0].Encoding = EncodeCategoricalCode
			},
			c: Case{{Num: 0}},
		},
		{
			name:   "a value that collapsed two source values onto one ID",
			schema: textSchema,
			mutate: func(p *DictionaryPlan) {
				p.Columns[0].Categories[0].Ambiguous = true
			},
			c: Case{{Num: 0}},
		},
		{
			// The plan is corrupted AFTER the width recomputation that
			// would have widened the variable to fit, which is the only way
			// to reach putText's bound: applyCharsetWrite measures the
			// encoded values and sizes the variable from them, so nothing
			// a caller can hand the encoder overflows. The guard stays
			// because the alternative to refusing is truncating.
			name:   "a value wider than the variable it goes in",
			schema: textSchema,
			mutate: func(p *DictionaryPlan) {
				p.Columns[0].Categories[0].Text = strings.Repeat("x", 99)
				p.Columns[0].Categories[0].Encoded = []byte(strings.Repeat("x", 99))
			},
			c: Case{{Num: 0}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := planFor(t, tc.schema, WriterOptions{})
			if tc.mutate != nil {
				tc.mutate(plan)
			}
			enc, err := NewDataEncoder(plan, tc.schema)
			if err != nil {
				t.Fatalf("NewDataEncoder: %v", err)
			}
			err = enc.WriteCase(tc.c)
			if err == nil {
				t.Fatal("the encoder wrote a value it has no honest form for")
			}
			if ce := codedErr(t, err); ce.Code != perr.PULSE_SPSS_EXPORT_UNSUPPORTED {
				t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_EXPORT_UNSUPPORTED)
			}
		})
	}
}

// TestDataEncoder_RefusesAMalformedCase covers the shape faults.
func TestDataEncoder_RefusesAMalformedCase(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})
	enc, err := NewDataEncoder(plan, s)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}

	if err := enc.WriteCase(Case{{Num: 1}}); err == nil {
		t.Error("a case of the wrong length was accepted")
	}
	if _, err := enc.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := enc.WriteCase(Case{{Num: 1}, {Num: 2}, {Num: 3}}); err == nil {
		t.Error("a case was accepted after Finish")
	}
	if _, err := enc.Finish(); err == nil {
		t.Error("Finish ran twice")
	}
}

// TestDataEncoder_RefusesATruncatedCohort keeps a damaged cohort from
// becoming a short final case.
func TestDataEncoder_RefusesATruncatedCohort(t *testing.T) {
	s := numericSchema()
	plan := planFor(t, s, WriterOptions{})
	enc, err := NewDataEncoder(plan, s)
	if err != nil {
		t.Fatalf("NewDataEncoder: %v", err)
	}
	// Two whole records of three u8 fields, then one stray byte.
	if err := enc.WriteCohort(bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7})); err == nil {
		t.Fatal("a cohort ending part way through a record was accepted")
	}
	if enc.Cases() != 2 {
		t.Errorf("Cases() = %d after the fault, want the 2 whole records", enc.Cases())
	}
}

// TestWriteCohort_ReadsEveryStorageType walks a schema carrying every
// `.pulse` type the writer can be handed, so the cohort read is exercised on
// the bit-packed, decimal and set paths rather than only on the numeric one.
func TestWriteCohort_ReadsEveryStorageType(t *testing.T) {
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "b", Type: encoding.FieldTypePackedBool},
		{Name: "n4", Type: encoding.FieldTypeU4},
		{Name: "n8", Type: encoding.FieldTypeU8},
		{Name: "n16", Type: encoding.FieldTypeU16},
		{Name: "n32", Type: encoding.FieldTypeU32},
		{Name: "n64", Type: encoding.FieldTypeU64},
		{Name: "f32", Type: encoding.FieldTypeF32},
		{Name: "f64", Type: encoding.FieldTypeF64},
		{Name: "dec", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "dt", Type: encoding.FieldTypeDateTime},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north")},
		{Name: "set", Type: encoding.FieldTypeSetU16, Dictionary: dictOf(t, "tv", "web")},
	}}

	// One record, written the way the cohort writer would.
	var rec bytes.Buffer
	if err := encoding.WriteBit(&rec, 0, true); err != nil {
		t.Fatalf("WriteBit: %v", err)
	}
	if err := encoding.WriteNibble(&rec, false, 5); err != nil {
		t.Fatalf("WriteNibble: %v", err)
	}
	for _, w := range []struct {
		ft  encoding.FieldType
		val uint64
	}{
		{encoding.FieldTypeU8, 8},
		{encoding.FieldTypeU16, 16},
		{encoding.FieldTypeU32, 32},
		{encoding.FieldTypeU64, 64},
		{encoding.FieldTypeF32, uint64(math.Float32bits(1.5))},
		{encoding.FieldTypeF64, math.Float64bits(2.25)},
	} {
		if err := encoding.WriteFieldValue(&rec, w.ft, w.val); err != nil {
			t.Fatalf("WriteFieldValue %v: %v", w.ft, err)
		}
	}
	if err := encoding.WriteDecimal128(&rec, encoding.NewDecimal128FromInt(12345)); err != nil {
		t.Fatalf("WriteDecimal128: %v", err)
	}
	for _, w := range []struct {
		ft  encoding.FieldType
		val uint64
	}{
		{encoding.FieldTypeDate, 19786},
		{encoding.FieldTypeDateTime, ^uint64(86400) + 1},
		{encoding.FieldTypeCategoricalU8, 0},
		{encoding.FieldTypeSetU16, 0b10},
	} {
		if err := encoding.WriteFieldValue(&rec, w.ft, w.val); err != nil {
			t.Fatalf("WriteFieldValue %v: %v", w.ft, err)
		}
	}

	c := NewCase(s)
	if err := readCohortCase(bytes.NewReader(rec.Bytes()), s, c); err != nil {
		t.Fatalf("readCohortCase: %v", err)
	}
	want := []float64{1, 5, 8, 16, 32, 64, 1.5, 2.25, 123.45, 19786, -86400, 0, 0b10}
	for i, w := range want {
		if c[i].Num != w {
			t.Errorf("%s: Num = %v, want %v", s.Fields[i].Name, c[i].Num, w)
		}
	}
	// A set carries its mask at full width beside the float echo.
	if c[12].Mask != 0b10 {
		t.Errorf("set Mask = %b, want 10", c[12].Mask)
	}
	// A datetime is SIGNED: the day before the epoch is not 1.8e19.
	if c[10].Num != -86400 {
		t.Errorf("datetime Num = %v, want -86400", c[10].Num)
	}
}
