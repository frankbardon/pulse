package spss

// E3-S5: endianness reconciliation and malformed-file hardening.
//
// Everything here answers one question the earlier stories could not: what
// does this reader do with a file it was not built against? We have no
// real-world `.sav` corpus, so the substitute is breadth — every byte order,
// every compression mode, every cut position, every byte fill — plus an
// explicit record of which strictness choices would reject a file a real
// writer might have produced.

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Byte order
// ---------------------------------------------------------------------------

// endianTwinSpec is the fixture the byte-order tests are built from. It is
// deliberately dense with byte-ordered fields: an int32 record count, a
// float64 bias, doubles at both ends of the compressible range and one that
// must take the raw escape, a string spanning three 8-byte segments, a
// value-label set, and the record 7/16 int64 case count. A big-endian bug
// that only touched, say, the extension records would survive a thinner
// fixture.
func endianTwinSpec(bo spsstest.ByteOrder, c spsstest.Compression) spsstest.Spec {
	mi := spsstest.DefaultMachineIntegerInfoFor(bo)
	n := int64(3)
	return spsstest.Spec{
		ByteOrder:   bo,
		Compression: c,
		Vars: []spsstest.Var{
			{Name: "ID", LongName: "RespondentId"},
			{Name: "SCORE", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 3}},
			{Name: "SEX", Label: "Sex"},
			{Name: "NOTE", Width: 20},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"SEX"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Male"},
				{Value: spsstest.Num(2), Label: "Female"},
			},
		}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(0.125), spsstest.Num(1), spsstest.Text("first note")},
			{spsstest.Num(2), spsstest.Num(-99), spsstest.Num(2), spsstest.Text("")},
			{spsstest.Num(3), spsstest.Num(1e300), spsstest.SysMis(), spsstest.Text("a longer note here!!")},
		},
		MachineIntegerInfo: &mi,
		CaseCount64:        &n,
		CharacterEncoding:  "UTF-8",
	}
}

// TestByteOrder_TwinsProduceIdenticalCohorts is the acceptance criterion
// "big-endian and little-endian fixtures of identical logical content produce
// identical cohorts", checked at the two levels that matter separately.
//
// The SCHEMA half matters because field types, nullability and the
// categorical dictionary are all derived from bytes the byte order governs —
// a dictionary seeded in the wrong order would still be a valid schema. The
// ROW half matters because the data section is where a byte-order bug is
// least visible: every double still decodes to a double, just a different
// one, and 1e300 read the wrong way round is a number a test comparing only
// shapes would accept.
//
// It runs across all three compression modes, because each frames its
// doubles differently: uncompressed writes them inline, bytecode writes most
// of them as command bytes and the rest as 8-byte escapes, and ZSAV wraps
// that same stream in a zlib block index whose own offsets are byte-ordered.
func TestByteOrder_TwinsProduceIdenticalCohorts(t *testing.T) {
	for _, c := range []struct {
		name string
		mode spsstest.Compression
	}{
		{"uncompressed", spsstest.CompressionNone},
		{"bytecode", spsstest.CompressionBytecode},
		{"zsav", spsstest.CompressionZSAV},
	} {
		t.Run(c.name, func(t *testing.T) {
			le := build(t, endianTwinSpec(spsstest.LittleEndian, c.mode))
			be := build(t, endianTwinSpec(spsstest.BigEndian, c.mode))

			// A generator that ignored ByteOrder would make every
			// assertion below pass vacuously.
			if string(le) == string(be) {
				t.Fatal("the two fixtures are byte-identical; the generator did not honour ByteOrder")
			}

			leDict := mustParse(t, le)
			beDict := mustParse(t, be)
			if leDict.byteOrder != binary.ByteOrder(binary.LittleEndian) {
				t.Errorf("little-endian fixture parsed as %v", leDict.byteOrder)
			}
			if beDict.byteOrder != binary.ByteOrder(binary.BigEndian) {
				t.Errorf("big-endian fixture parsed as %v", beDict.byteOrder)
			}

			leReader := NewReaderFromBytes(le)
			beReader := NewReaderFromBytes(be)

			leSchema, err := leReader.PulseSchema()
			if err != nil {
				t.Fatalf("little-endian PulseSchema: %v", err)
			}
			beSchema, err := beReader.PulseSchema()
			if err != nil {
				t.Fatalf("big-endian PulseSchema: %v", err)
			}
			assertSchemasEqual(t, beSchema, leSchema)

			leHeader, err := leReader.ReadHeader()
			if err != nil {
				t.Fatalf("little-endian ReadHeader: %v", err)
			}
			beHeader, err := beReader.ReadHeader()
			if err != nil {
				t.Fatalf("big-endian ReadHeader: %v", err)
			}
			assertRows(t, [][]string{beHeader}, [][]string{leHeader})
			assertRows(t, readAll(t, beReader), readAll(t, leReader))

			// The strongest reading of "identical cohorts": run both
			// through the real import path and compare the WRITTEN
			// `.pulse` files byte for byte. Schema and row equality
			// each leave a gap the other does not — a dictionary
			// seeded in a different order produces equal rows and a
			// different cohort — and the encoded file closes both.
			if a, b := importToBytes(t, le), importToBytes(t, be); !bytes.Equal(a, b) {
				t.Errorf("the two byte orders produced different cohorts (%d vs %d bytes)", len(a), len(b))
			}
		})
	}
}

// importToBytes runs one `.sav` through the shared import path and returns
// the encoded cohort.
func importToBytes(t *testing.T, raw []byte) []byte {
	t.Helper()
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "in.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	job := pio.NewImportJob(NewReader(cfg.Fs(), "in.sav"), "out.pulse")
	job.FS = cfg.Fs()
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ImportJob.Run: %v", err)
	}
	out, err := afero.ReadFile(cfg.Fs(), "out.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return out
}

// TestByteOrder_CrossCheckAgainstRecord73 covers the acceptance criterion
// "endianness is driven by the header layout code and cross-checked against
// record 7/3; a disagreement is a coded error", including every shape that is
// deliberately NOT a disagreement.
func TestByteOrder_CrossCheckAgainstRecord73(t *testing.T) {
	specFor := func(bo spsstest.ByteOrder, endianField int32) spsstest.Spec {
		spec := endianTwinSpec(bo, spsstest.CompressionNone)
		mi := *spec.MachineIntegerInfo
		mi.Endianness = endianField
		spec.MachineIntegerInfo = &mi
		return spec
	}

	t.Run("agreement parses", func(t *testing.T) {
		for _, bo := range []spsstest.ByteOrder{spsstest.LittleEndian, spsstest.BigEndian} {
			field := spsstest.EndiannessLittle
			if bo == spsstest.BigEndian {
				field = spsstest.EndiannessBig
			}
			d := mustParse(t, build(t, specFor(bo, field)))
			for _, w := range d.warnings {
				if w.Code == perr.PULSE_SPSS_ENDIANNESS_MISMATCH {
					t.Errorf("%v: an agreeing 7/3 raised %s", bo, w.Code)
				}
			}
		}
	})

	t.Run("disagreement is a coded error", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			bo    spsstest.ByteOrder
			field int32
		}{
			{"little-endian file declaring big-endian", spsstest.LittleEndian, spsstest.EndiannessBig},
			{"big-endian file declaring little-endian", spsstest.BigEndian, spsstest.EndiannessLittle},
		} {
			t.Run(tc.name, func(t *testing.T) {
				raw := build(t, specFor(tc.bo, tc.field))
				_, err := parseDictionary(raw)
				if err == nil {
					t.Fatal("parseDictionary succeeded; a contradicted byte order governs every number in the file and cannot be guessed")
				}
				ce := codedError(t, err)
				if ce.Code != perr.PULSE_SPSS_ENDIANNESS_MISMATCH {
					t.Fatalf("code = %s, want %s (%v)", ce.Code, perr.PULSE_SPSS_ENDIANNESS_MISMATCH, err)
				}
				if got := ce.Details[perr.DetailSPSSSubtype]; got != int32(extMachineInteger) {
					t.Errorf("Details[%q] = %v, want %d", perr.DetailSPSSSubtype, got, extMachineInteger)
				}
				assertDetails(t, ce, len(raw))
			})
		}
	})

	t.Run("an unfilled or out-of-range field is not a disagreement", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			field     int32
			wantWarn  bool
			bo        spsstest.ByteOrder
			wantOrder binary.ByteOrder
		}{
			// 0 is what a writer that never filled the field leaves.
			{"zero", 0, false, spsstest.LittleEndian, binary.LittleEndian},
			// A byte-swapped 1 and 2 land here rather than reading as
			// the other order. The layout code already made that case
			// unreachable, so this only has to not fail.
			{"byte-swapped one", 1 << 24, true, spsstest.LittleEndian, binary.LittleEndian},
			{"nonsense", 7, true, spsstest.BigEndian, binary.BigEndian},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := mustParse(t, build(t, specFor(tc.bo, tc.field)))
				if d.byteOrder != tc.wantOrder {
					t.Errorf("byteOrder = %v, want %v; the header layout code decides alone", d.byteOrder, tc.wantOrder)
				}
				var sawInvalid bool
				for _, w := range d.warnings {
					if w.Code == perr.PULSE_SPSS_ENDIANNESS_MISMATCH {
						t.Fatalf("an endianness field of %d raised a mismatch; only a clean statement of the other order is a contradiction", tc.field)
					}
					if w.Code == perr.PULSE_SPSS_EXTENSION_INVALID && strings.Contains(w.Message, "endianness field") {
						sawInvalid = true
					}
				}
				if sawInvalid != tc.wantWarn {
					t.Errorf("EXTENSION_INVALID warning for the endianness field = %v, want %v", sawInvalid, tc.wantWarn)
				}
			})
		}
	})

	t.Run("a file with no record 7/3 parses", func(t *testing.T) {
		spec := endianTwinSpec(spsstest.BigEndian, spsstest.CompressionNone)
		spec.MachineIntegerInfo = nil
		d := mustParse(t, build(t, spec))
		if d.byteOrder != binary.ByteOrder(binary.BigEndian) {
			t.Errorf("byteOrder = %v, want big-endian", d.byteOrder)
		}
	})
}

// ---------------------------------------------------------------------------
// Magic versus compression flag
// ---------------------------------------------------------------------------

// TestMagicVersusCompressionFlag records the E3-S2 routed decision: the two
// fields are cross-checked, and a disagreement is a WARNING rather than an
// error, because the compression flag is the field that describes the bytes
// and the magic is a generation label. The file still reads, both ways round.
func TestMagicVersusCompressionFlag(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     spsstest.Compression
		magic    string
		wantWarn bool
	}{
		{"$FL2 with an uncompressed section", spsstest.CompressionNone, magicSAV, false},
		{"$FL3 with a ZSAV section", spsstest.CompressionZSAV, magicZSAV, false},
		{"$FL3 carrying a bytecode section", spsstest.CompressionBytecode, magicZSAV, true},
		{"$FL2 carrying a ZSAV section", spsstest.CompressionZSAV, magicSAV, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := build(t, endianTwinSpec(spsstest.LittleEndian, tc.mode))
			copy(raw[0:4], tc.magic)

			r := NewReaderFromBytes(raw)
			rows := readAll(t, r)
			if len(rows) != 3 {
				t.Fatalf("read %d case(s), want 3; the compression flag must decide regardless of the magic", len(rows))
			}

			var saw *perr.CodedError
			for _, w := range r.Warnings() {
				if w.Code == perr.PULSE_SPSS_MAGIC_FLAG_MISMATCH {
					saw = w
				}
			}
			if (saw != nil) != tc.wantWarn {
				t.Fatalf("%s warning = %v, want %v", perr.PULSE_SPSS_MAGIC_FLAG_MISMATCH, saw != nil, tc.wantWarn)
			}
			if saw != nil && !strings.Contains(saw.Message, tc.magic) {
				t.Errorf("message %q does not name the magic it found", saw.Message)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Distinct codes for distinct damage
// ---------------------------------------------------------------------------

// TestMalformedInput_DistinctCodes is the acceptance criterion "truncated,
// zero-length, and structurally corrupt files each produce a distinct coded
// error naming what failed".
//
// Distinctness is asserted directly rather than case by case: the three
// families must not collapse onto one code, because "the import failed" and
// "the download was cut short" and "the target was never written" are three
// different things for whoever has to fix it.
func TestMalformedInput_DistinctCodes(t *testing.T) {
	ref := build(t, endianTwinSpec(spsstest.LittleEndian, spsstest.CompressionNone))
	refDict := mustParse(t, ref)

	cases := []struct {
		name     string
		in       []byte
		wantCode perr.Code
		wantMsg  string
	}{
		{
			name:     "zero length",
			in:       nil,
			wantCode: perr.PULSE_SPSS_FILE_EMPTY,
			wantMsg:  "the source is empty",
		},
		{
			name:     "truncated inside the header",
			in:       clone(ref)[:100],
			wantCode: perr.PULSE_SPSS_DICT_TRUNCATED,
			wantMsg:  "file header record is complete",
		},
		{
			name:     "truncated inside the dictionary",
			in:       clone(ref)[:refDict.dataOffset-4],
			wantCode: perr.PULSE_SPSS_DICT_TRUNCATED,
			wantMsg:  "the file ends before",
		},
		{
			name:     "truncated inside the data section",
			in:       clone(ref)[:len(ref)-4],
			wantCode: perr.PULSE_SPSS_DATA_TRUNCATED,
			wantMsg:  "case",
		},
		{
			name: "structurally corrupt: not a system file at all",
			in: func() []byte {
				b := clone(ref)
				copy(b[0:4], "%PDF")
				return b
			}(),
			wantCode: perr.PULSE_SPSS_DICT_INVALID,
			wantMsg:  "not a .sav system file",
		},
		{
			name: "structurally corrupt: no identifiable byte order",
			in: func() []byte {
				b := clone(ref)
				binary.LittleEndian.PutUint32(b[offLayoutCode:], 0xDEADBEEF)
				return b
			}(),
			wantCode: perr.PULSE_SPSS_DICT_INVALID,
			wantMsg:  "byte order cannot be determined",
		},
		{
			name: "structurally corrupt: an unknown record type",
			in: func() []byte {
				b := clone(ref)
				binary.LittleEndian.PutUint32(b[headerSize:], 55)
				return b
			}(),
			wantCode: perr.PULSE_SPSS_DICT_INVALID,
			wantMsg:  "unknown record type 55",
		},
		{
			name: "structurally corrupt: an undefined compression flag",
			in: func() []byte {
				b := clone(ref)
				binary.LittleEndian.PutUint32(b[offCompression:], 9)
				return b
			}(),
			wantCode: perr.PULSE_SPSS_DICT_INVALID,
			wantMsg:  "the format defines only 0",
		},
	}

	seen := map[perr.Code][]string{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := NewReaderFromBytes(tc.in).ReadRows(context.Background(), func([]string) error { return nil })
			if err == nil {
				t.Fatal("read without error")
			}
			ce := codedError(t, err)
			if ce.Code != tc.wantCode {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, tc.wantCode, err)
			}
			if !strings.Contains(ce.Message, tc.wantMsg) {
				t.Errorf("message = %q, want it to name what failed: %q", ce.Message, tc.wantMsg)
			}
			seen[ce.Code] = append(seen[ce.Code], tc.name)
		})
	}

	for _, family := range []perr.Code{
		perr.PULSE_SPSS_FILE_EMPTY,
		perr.PULSE_SPSS_DICT_TRUNCATED,
		perr.PULSE_SPSS_DATA_TRUNCATED,
		perr.PULSE_SPSS_DICT_INVALID,
	} {
		if len(seen[family]) == 0 {
			t.Errorf("no case produced %s; the four damage families must stay distinguishable", family)
		}
	}
}

// ---------------------------------------------------------------------------
// Corruption sweeps
// ---------------------------------------------------------------------------

// TestCorruption_DictionarySweep is the dictionary's counterpart to E3-S1's
// data-section byte-fill fuzz and E3-S2's ZSAV cut sweep: every byte of the
// dictionary, overwritten with every one of a set of fills that between them
// cover the tag, length and sentinel values the walk branches on.
//
// The invariant is the total one — never a panic, and any failure is a coded
// error from a known family carrying an in-range offset. It is deliberately
// NOT "this mutation must fail": most single-byte edits land in a label or a
// name and produce a file that is different but perfectly readable, and a
// test demanding failure would be asserting that corruption is always
// detectable, which is false for a format with no checksums.
func TestCorruption_DictionarySweep(t *testing.T) {
	for _, mode := range []struct {
		name string
		c    spsstest.Compression
	}{
		{"uncompressed", spsstest.CompressionNone},
		{"bytecode", spsstest.CompressionBytecode},
		{"zsav", spsstest.CompressionZSAV},
	} {
		t.Run(mode.name, func(t *testing.T) {
			raw := build(t, endianTwinSpec(spsstest.LittleEndian, mode.c))
			dictEnd := mustParse(t, raw).dataOffset

			// All 256 fills, matching the bar E3-S1 set over the data
			// section. Choosing a "meaningful" subset would beg the
			// question — the values that matter are the ones the walk
			// branches on, and the branches are what is under test.
			for off := 0; off < dictEnd; off++ {
				for fill := 0; fill < 256; fill++ {
					if raw[off] == byte(fill) {
						continue
					}
					b := clone(raw)
					b[off] = byte(fill)
					assertNoPanicCodedOrFine(t, b, off, byte(fill))
				}
			}
		})
	}
}

// TestCorruption_TruncationSweepAcrossModes cuts the file at every offset in
// each compression mode. E3-S2 swept ZSAV; this widens the same sweep to all
// three so no mode has a bounds check the others do not.
func TestCorruption_TruncationSweepAcrossModes(t *testing.T) {
	for _, mode := range []struct {
		name string
		c    spsstest.Compression
	}{
		{"uncompressed", spsstest.CompressionNone},
		{"bytecode", spsstest.CompressionBytecode},
		{"zsav", spsstest.CompressionZSAV},
	} {
		t.Run(mode.name, func(t *testing.T) {
			raw := build(t, endianTwinSpec(spsstest.LittleEndian, mode.c))
			for n := 0; n <= len(raw); n++ {
				assertNoPanicCodedOrFine(t, raw[:n], n, 0)
			}
		})
	}
}

// assertNoPanicCodedOrFine drives the WHOLE reader — dictionary, schema
// mapping and every case — over a mutated buffer, and asserts only that it
// neither panics nor fails with something a caller cannot classify.
//
// Running the whole reader rather than parseDictionary alone is the point:
// the dictionary walk was already fuzzed, and the failure this catches is a
// dictionary that parses into geometry the data reader then trusts.
func assertNoPanicCodedOrFine(t *testing.T, b []byte, off int, fill byte) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic at offset %d with fill 0x%02X: %v", off, fill, r)
		}
	}()

	r := NewReaderFromBytes(b)
	if _, err := r.PulseSchema(); err != nil {
		assertHardeningCoded(t, err, len(b), off, fill)
		return
	}
	err := r.ReadRows(context.Background(), func([]string) error { return nil })
	if err != nil {
		assertHardeningCoded(t, err, len(b), off, fill)
	}
}

// assertHardeningCoded asserts a failure is a *CodedError from a family the
// SPSS reader defines. A bare error here would mean an operator gets prose
// instead of a code, and `pulse errors lookup` has nothing to explain.
func assertHardeningCoded(t *testing.T, err error, size, off int, fill byte) {
	t.Helper()
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("offset %d fill 0x%02X: error is %T, not *errors.CodedError: %v", off, fill, err, err)
	}
	switch ce.Code {
	case perr.PULSE_SPSS_FILE_EMPTY,
		perr.PULSE_SPSS_DICT_INVALID,
		perr.PULSE_SPSS_DICT_TRUNCATED,
		perr.PULSE_SPSS_ENDIANNESS_MISMATCH,
		perr.PULSE_SPSS_CHARSET_UNSUPPORTED,
		perr.PULSE_SPSS_CHARSET_INVALID,
		perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED,
		perr.PULSE_SPSS_COMPRESSION_INVALID,
		perr.PULSE_SPSS_ZSAV_INVALID,
		perr.PULSE_SPSS_ZSAV_BLOCK_CORRUPT,
		perr.PULSE_SPSS_DATA_TRUNCATED,
		perr.PULSE_SPSS_CATEGORICAL_OVERFLOW,
		perr.PULSE_SPSS_VERY_LONG_STRING_INVALID:
	default:
		t.Fatalf("offset %d fill 0x%02X: code = %s, outside the SPSS reader's families: %v", off, fill, ce.Code, err)
	}
	if o, ok := ce.Details[perr.DetailSPSSOffset].(int); ok && (o < 0 || o > size) {
		t.Errorf("offset %d fill 0x%02X: Details[%q] = %d, outside 0..%d", off, fill, perr.DetailSPSSOffset, o, size)
	}
}

// ---------------------------------------------------------------------------
// Unknown extension subtypes, end to end
// ---------------------------------------------------------------------------

// TestUnknownExtensionSubtype_SkipsWithWarningEndToEnd is the acceptance
// criterion asking for this to be confirmed "end to end, not just at the
// parse unit level".
//
// End to end means through pio.ImportJob and out the other side as a written
// cohort: the parse-level test proves the dictionary walk survives, but the
// thing that would actually bite an operator is a warning raised on a
// dictionary and then dropped somewhere between the reader and the import
// report, leaving a silently degraded import. So this asserts all three
// links — the rows land, the cohort is real, and the warning arrives with its
// subtype intact.
func TestUnknownExtensionSubtype_SkipsWithWarningEndToEnd(t *testing.T) {
	spec := endianTwinSpec(spsstest.LittleEndian, spsstest.CompressionBytecode)
	spec.RawExtensions = []spsstest.RawExtension{
		{Subtype: 4242, Size: 1, Payload: []byte("nobody knows what this is")},
		{Subtype: 31337, Size: 4, Payload: []byte{1, 0, 0, 0, 2, 0, 0, 0}},
	}
	raw := build(t, spec)

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "survey.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	job := pio.NewImportJob(NewReader(cfg.Fs(), "survey.sav"), "cohort.pulse")
	job.FS = cfg.Fs()
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("ImportJob.Run: an unrecognised extension subtype must not fail an import: %v", err)
	}
	if report.RowsImported != 3 {
		t.Errorf("RowsImported = %d, want 3", report.RowsImported)
	}
	if ok, _ := afero.Exists(cfg.Fs(), "cohort.pulse"); !ok {
		t.Error("no cohort was written")
	}

	subtypes := map[int32]bool{}
	for _, w := range report.SourceWarnings {
		if w.Code != perr.PULSE_SPSS_EXTENSION_UNKNOWN {
			continue
		}
		if st, ok := w.Details[perr.DetailSPSSSubtype].(int32); ok {
			subtypes[st] = true
		}
	}
	for _, want := range []int32{4242, 31337} {
		if !subtypes[want] {
			t.Errorf("no %s warning naming subtype %d reached the import report; the diagnostic is the only thing standing between a skipped record and a silently degraded import",
				perr.PULSE_SPSS_EXTENSION_UNKNOWN, want)
		}
	}
}

// ---------------------------------------------------------------------------
// The charset escape hatch
// ---------------------------------------------------------------------------

// TestCharsetOverride_RescuesAnUndeclaredFile is the routed E3-S3 usability
// trap, tested at the level the fix lives: a file declaring no encoding at
// all and carrying 8-bit bytes fails under the strict UTF-8 default, and the
// override is what makes it importable. Before E3-S5 that override was
// reachable only from Go, so a CLI user had no recourse whatsoever.
func TestCharsetOverride_RescuesAnUndeclaredFile(t *testing.T) {
	// The generator refuses to emit a non-ASCII byte into a file that
	// declares no encoding — which is the right rule for an emitter and
	// exactly the file shape under test here — so the 8-bit byte is
	// patched in afterwards. "cafX" becomes "café" in windows-1252: the é
	// is a single 0xE9 byte, and 0xE9 alone is not valid UTF-8.
	raw, err := spsstest.Build(spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "A", Label: "cafX"}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}},
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	at := bytes.Index(raw, []byte("cafX"))
	if at < 0 {
		t.Fatal("the label is not in the emitted bytes")
	}
	raw[at+3] = 0xE9

	_, err = NewReaderFromBytes(raw).PulseSchema()
	if err == nil {
		t.Fatal("an undeclared 8-bit file decoded under the strict UTF-8 default")
	}
	if ce := codedError(t, err); ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_CHARSET_INVALID)
	}

	d, err := parseDictionaryWithCharset(raw, "windows-1252")
	if err != nil {
		t.Fatalf("with the override the same file must decode: %v", err)
	}
	if got := d.vars[0].label; got != "café" {
		t.Errorf("label = %q, want %q", got, "café")
	}
}
