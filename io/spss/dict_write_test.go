package spss

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// cohortSchema reads back the schema an import actually wrote, which is
// what pio.SchemaAwareWriter hands the writer.
func cohortSchema(t *testing.T, fs afero.Fs, cohort string) *encoding.Schema {
	t.Helper()
	f, err := fs.Open(cohort)
	if err != nil {
		t.Fatalf("opening cohort %s: %v", cohort, err)
	}
	defer f.Close()
	if err := encoding.ReadHeader(f); err != nil {
		t.Fatalf("reading cohort header: %v", err)
	}
	s, err := encoding.ReadSchema(f)
	if err != nil {
		t.Fatalf("reading cohort schema: %v", err)
	}
	return s
}

// exportFixture imports a spec and resolves its sidecar, giving a test the
// exact pair a real export starts from.
func exportFixture(t *testing.T, spec spsstest.Spec) (*encoding.Schema, *SidecarResolution) {
	t.Helper()
	fs, cohort, _ := importFixture(t, spec)
	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	return cohortSchema(t, fs, cohort), res
}

// emit builds a dictionary or fails the test.
func emit(t *testing.T, req DictionaryRequest) *DictionaryPlan {
	t.Helper()
	plan, err := BuildDictionary(req)
	if err != nil {
		t.Fatalf("BuildDictionary: %v", err)
	}
	return plan
}

// reparse runs an emitted dictionary back through this package's own reader.
func reparse(t *testing.T, plan *DictionaryPlan) *dictionary {
	t.Helper()
	d, err := parseDictionary(plan.Bytes)
	if err != nil {
		t.Fatalf("the emitted dictionary does not parse: %v", err)
	}
	return d
}

// planColumn returns the emitted variable of the given final name.
func planColumn(t *testing.T, plan *DictionaryPlan, name string) ColumnPlan {
	t.Helper()
	for _, c := range plan.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the plan has no column %q", name)
	return ColumnPlan{}
}

// readVar returns the re-parsed variable of the given final name.
func readVar(t *testing.T, d *dictionary, name string) variable {
	t.Helper()
	for _, v := range d.vars {
		if v.fieldName() == name {
			return v
		}
	}
	t.Fatalf("the re-parsed dictionary has no variable %q", name)
	return variable{}
}

// referenceSchema is the two-field cohort the hand-verified walkthrough
// below describes: one numeric and one categorical with no recorded SPSS
// codes.
func referenceSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for _, s := range []string{"north", "south"} {
		if _, err := d.Add(s); err != nil {
			t.Fatalf("building dictionary: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "ID", Type: encoding.FieldTypeU32},
		{Name: "REGION", Type: encoding.FieldTypeCategoricalU8, Dictionary: d, Description: "Region"},
	}}
}

// ---------------------------------------------------------------------------
// Byte-level verification against the specification
// ---------------------------------------------------------------------------

// referenceDictionarySize is the total byte length of the emitted reference
// dictionary, derived by hand in the walkthrough below. Asserted so an
// accidentally-emitted extra record shows up as a length change rather than
// as a shifted offset.
const referenceDictionarySize = 441

// TestEmittedDictionary_HandVerified checks the emitted bytes against the
// GNU PSPP System File Format specification, field by field, at absolute
// offsets.
//
// It is the counterpart of internal/spsstest's TestReferenceFixture_HandVerified
// and it exists for the same reason: round-tripping through our own reader is
// necessary but NOT sufficient. A misreading of the specification shared by
// the reader and the writer passes a round trip cleanly and still produces a
// file SPSS cannot open. Only a walkthrough a reviewer can check against the
// spec without running anything catches that, so every offset below is stated
// rather than computed.
//
// The fixture is [referenceSchema] with NO sidecar: a two-field cohort
// synthesised from the `.pulse` schema alone.
//
//	ID      u32,            no description
//	REGION  categorical_u8, description "Region", dictionary {north, south}
//
// Two derived quantities drive the layout, so they come first.
//
// *Segments and element indices.* A variable occupies ceil(width/8) eight-byte
// elements — 1 for a numeric — and the spec numbers variables by ELEMENT
// position, 1-based, counting the continuation records a wide string needs.
// ID is numeric (1 element, index 1); REGION is a string of width 5, which
// is 1 element (index 2). nominal_case_size is therefore 2.
//
// *No record type 3.* REGION is a categorical, and this is the synthesised
// path, so there are NO recorded SPSS value codes for its dictionary entries.
// The writer emits it as a STRING variable holding the entry text and emits
// no value labels at all. Their absence from this walkthrough is the
// assertion: a record type 3 here would mean codes had been invented from
// dictionary positions. See dict_synth.go.
//
// ---------------------------------------------------------------------------
// FILE HEADER RECORD — offsets 0x0000..0x00AF, 176 bytes
// ---------------------------------------------------------------------------
//
//	0x0000  4   rec_type           "$FL2"  24 46 4C 32
//	                               $FL2 tags an uncompressed or bytecode file;
//	                               $FL3 marks ZSAV, which is not emitted.
//	0x0004  60  prod_name          "@(#) SPSS DATA FILE pulse" + 35 spaces.
//	                               Readers sniff the "@(#) SPSS DATA FILE"
//	                               prefix.
//	0x0040  4   layout_code        2       02 00 00 00
//	                               Written in the file's own byte order, so it
//	                               is the endianness probe. Always
//	                               little-endian here.
//	0x0044  4   nominal_case_size  2       02 00 00 00   (see Segments above)
//	0x0048  4   compression        0       00 00 00 00
//	0x004C  4   weight_index       0       00 00 00 00   (unweighted)
//	0x0050  4   ncases             0       00 00 00 00
//	0x0054  8   bias               100.0   00 00 00 00 00 00 59 40
//	                               IEEE 754 100.0 is 0x4059000000000000;
//	                               little-endian puts 59 40 last. Written even
//	                               though the file is uncompressed, as PSPP does.
//	0x005C  9   creation_date      "01 Jan 24"   (the spec's "dd mmm yy")
//	0x0065  8   creation_time      "00:00:00"
//	0x006D  64  file_label         64 spaces (no label)
//	0x00AD  3   padding            00 00 00
//	0x00B0      end. 4+60+4+4+4+4+4+8+9+8+64+3 = 176 = 0x00B0.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — variable ID — offsets 0x00B0..0x00CF, 32 bytes
// ---------------------------------------------------------------------------
//
//	0x00B0  4   rec_type           2       02 00 00 00
//	0x00B4  4   type               0       00 00 00 00   (0 = numeric)
//	0x00B8  4   has_var_label      0       00 00 00 00
//	0x00BC  4   n_missing_values   0       00 00 00 00
//	0x00C0  4   print              F10.0   00 0A 05 00
//	                               Packed 0x00TTWWDD: unused 0x00, type 0x05
//	                               (F), width 0x0A, decimals 0x00 — so
//	                               0x00050A00, little-endian 00 0A 05 00.
//	                               Width 10 holds a u32's whole range.
//	0x00C4  4   write              F10.0   00 0A 05 00
//	0x00C8  8   name               "ID      "  (space padded to 8)
//	0x00D0      end. No label and no missing values, so nothing follows.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — variable REGION — offsets 0x00D0..0x00FB, 44 bytes
// ---------------------------------------------------------------------------
//
//	0x00D0  4   rec_type           2       02 00 00 00
//	0x00D4  4   type               5       05 00 00 00
//	                               A string's `type` field IS its declared byte
//	                               width. 5 is the widest dictionary entry
//	                               ("north"/"south").
//	0x00D8  4   has_var_label      1       01 00 00 00
//	0x00DC  4   n_missing_values   0       00 00 00 00
//	0x00E0  4   print              A5      00 05 01 00   (0x00010500, type 1 = A)
//	0x00E4  4   write              A5      00 05 01 00
//	0x00E8  8   name               "REGION  "
//	0x00F0  4   label_len          6       06 00 00 00
//	                               The TRUE byte length, not the padded one.
//	0x00F4  8   label              "Region" + 2 zero bytes
//	                               52 65 67 69 6F 6E 00 00
//	                               Padded out to a multiple of 4; 6 rounds to 8.
//	0x00FC      end.
//
// ---------------------------------------------------------------------------
// RECORD 7/3 — machine integer info — offsets 0x00FC..0x012B, 48 bytes
// ---------------------------------------------------------------------------
//
//	0x00FC  4   rec_type           7       07 00 00 00
//	0x0100  4   subtype            3       03 00 00 00
//	0x0104  4   size               4       04 00 00 00
//	0x0108  4   count              8       08 00 00 00   (8 int32s follow)
//	0x010C  4   version_major      0
//	0x0110  4   version_minor      0
//	0x0114  4   version_revision   0
//	0x0118  4   machine_code       0       No reader uses these four.
//	0x011C  4   floating_point_rep 1       01 00 00 00   (1 = IEEE 754)
//	0x0120  4   compression_code   1       01 00 00 00
//	0x0124  4   endianness         2       02 00 00 00   (2 = little-endian)
//	                               THIS file's order, never the source's — the
//	                               header layout code already fixed it, and a
//	                               contradiction between the two is a hard
//	                               PULSE_SPSS_ENDIANNESS_MISMATCH on the way in.
//	0x0128  4   character_code     65001   E9 FD 00 00   (0xFDE9 = 65001, UTF-8)
//	0x012C      end.
//
// ---------------------------------------------------------------------------
// RECORD 7/4 — machine float info — offsets 0x012C..0x0153, 40 bytes
// ---------------------------------------------------------------------------
//
//	0x012C  4   rec_type           7
//	0x0130  4   subtype            4
//	0x0134  4   size               8
//	0x0138  4   count              3       (3 doubles follow)
//	0x013C  8   sysmis   -DBL_MAX  FF FF FF FF FF FF EF FF
//	0x0144  8   highest  +DBL_MAX  FF FF FF FF FF FF EF 7F
//	0x014C  8   lowest             FE FF FF FF FF FF EF FF
//	                               One ULP above -DBL_MAX. The reader adopts a
//	                               declared triple only when it is ordered
//	                               sysmis < lowest < highest, so lowest cannot
//	                               be -DBL_MAX itself.
//	0x0154      end.
//
// ---------------------------------------------------------------------------
// RECORD 7/11 — variable display parameters — offsets 0x0154..0x017B, 40 bytes
// ---------------------------------------------------------------------------
//
//	0x0154  4   rec_type           7
//	0x0158  4   subtype            11      0B 00 00 00
//	0x015C  4   size               4
//	0x0160  4   count              6       06 00 00 00
//	                               THREE int32s per PHYSICAL variable, and this
//	                               dictionary has two. The record is positional
//	                               over the record type 2 stream — a reader
//	                               applies it before the very-long-string fold —
//	                               so a segmented string needs one entry per
//	                               segment, not one per logical variable.
//	0x0164  4   ID measure         3       (3 = scale)
//	0x0168  4   ID display width   10
//	0x016C  4   ID alignment       1       (1 = right)
//	0x0170  4   REGION measure     1       (1 = nominal)
//	0x0174  4   REGION width       5
//	0x0178  4   REGION alignment   0       (0 = left)
//	0x017C      end.
//
// ---------------------------------------------------------------------------
// RECORD 7/16 — 64-bit case count — offsets 0x017C..0x019B, 32 bytes
// ---------------------------------------------------------------------------
//
//	0x017C  4   rec_type           7
//	0x0180  4   subtype            16      10 00 00 00
//	0x0184  4   size               8
//	0x0188  4   count              2
//	0x018C  8   constant           1       The spec's fixed leading field, and a
//	                                       second endianness probe.
//	0x0194  8   case_count         0       DictionaryPlan.CaseCount64Offset
//	                                       points here, so a streaming encoder
//	                                       can patch it alongside the header's
//	                                       int32 at 0x0050.
//	0x019C      end.
//
// ---------------------------------------------------------------------------
// RECORD 7/20 — character encoding — offsets 0x019C..0x01B0, 21 bytes
// ---------------------------------------------------------------------------
//
//	0x019C  4   rec_type           7
//	0x01A0  4   subtype            20      14 00 00 00
//	0x01A4  4   size               1       A byte-element text payload.
//	0x01A8  4   count              5       len("UTF-8")
//	0x01AC  5   payload            "UTF-8"
//	0x01B1      end.
//
// ---------------------------------------------------------------------------
// RECORD 999 — dictionary terminator — offsets 0x01B1..0x01B8, 8 bytes
// ---------------------------------------------------------------------------
//
//	0x01B1  4   rec_type           999     E7 03 00 00
//	0x01B5  4   filler             0
//	0x01B9      end of the dictionary section. 441 bytes.
func TestEmittedDictionary_HandVerified(t *testing.T) {
	plan := emit(t, DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: compressionNone})
	got := plan.Bytes

	if len(got) != referenceDictionarySize {
		t.Fatalf("dictionary length = %d bytes, want %d — the walkthrough's offsets no longer describe these bytes",
			len(got), referenceDictionarySize)
	}

	// Every row is one field of the walkthrough above, at its stated offset,
	// with its stated bytes. `field` names the spec field, so a failure says
	// which part of the format broke.
	cases := []struct {
		offset int
		field  string
		want   []byte
	}{
		// --- file header record ---
		{0x0000, "rec_type", []byte("$FL2")},
		{0x0004, "prod_name", []byte("@(#) SPSS DATA FILE pulse" + strings.Repeat(" ", 35))},
		{0x0040, "layout_code", []byte{0x02, 0, 0, 0}},
		{0x0044, "nominal_case_size", []byte{0x02, 0, 0, 0}},
		{0x0048, "compression", []byte{0, 0, 0, 0}},
		{0x004C, "weight_index", []byte{0, 0, 0, 0}},
		{0x0050, "ncases", []byte{0, 0, 0, 0}},
		{0x0054, "bias", []byte{0, 0, 0, 0, 0, 0, 0x59, 0x40}},
		{0x005C, "creation_date", []byte("01 Jan 24")},
		{0x0065, "creation_time", []byte("00:00:00")},
		{0x006D, "file_label", []byte(strings.Repeat(" ", 64))},
		{0x00AD, "header padding", []byte{0, 0, 0}},

		// --- record type 2, ID ---
		{0x00B0, "ID rec_type", []byte{0x02, 0, 0, 0}},
		{0x00B4, "ID type", []byte{0, 0, 0, 0}},
		{0x00B8, "ID has_var_label", []byte{0, 0, 0, 0}},
		{0x00BC, "ID n_missing_values", []byte{0, 0, 0, 0}},
		{0x00C0, "ID print F10.0", []byte{0x00, 0x0A, 0x05, 0x00}},
		{0x00C4, "ID write F10.0", []byte{0x00, 0x0A, 0x05, 0x00}},
		{0x00C8, "ID name", []byte("ID      ")},

		// --- record type 2, REGION ---
		{0x00D0, "REGION rec_type", []byte{0x02, 0, 0, 0}},
		{0x00D4, "REGION type (declared byte width)", []byte{0x05, 0, 0, 0}},
		{0x00D8, "REGION has_var_label", []byte{0x01, 0, 0, 0}},
		{0x00DC, "REGION n_missing_values", []byte{0, 0, 0, 0}},
		{0x00E0, "REGION print A5", []byte{0x00, 0x05, 0x01, 0x00}},
		{0x00E4, "REGION write A5", []byte{0x00, 0x05, 0x01, 0x00}},
		{0x00E8, "REGION name", []byte("REGION  ")},
		{0x00F0, "REGION label_len", []byte{0x06, 0, 0, 0}},
		{0x00F4, "REGION label + 32-bit padding", []byte("Region\x00\x00")},

		// --- record 7/3 ---
		{0x00FC, "7/3 rec_type", []byte{0x07, 0, 0, 0}},
		{0x0100, "7/3 subtype", []byte{0x03, 0, 0, 0}},
		{0x0104, "7/3 size", []byte{0x04, 0, 0, 0}},
		{0x0108, "7/3 count", []byte{0x08, 0, 0, 0}},
		{0x010C, "7/3 version + machine code", bytes.Repeat([]byte{0}, 16)},
		{0x011C, "7/3 floating_point_rep", []byte{0x01, 0, 0, 0}},
		{0x0120, "7/3 compression_code", []byte{0x01, 0, 0, 0}},
		{0x0124, "7/3 endianness", []byte{0x02, 0, 0, 0}},
		{0x0128, "7/3 character_code", []byte{0xE9, 0xFD, 0, 0}},

		// --- record 7/4 ---
		{0x012C, "7/4 rec_type", []byte{0x07, 0, 0, 0}},
		{0x0130, "7/4 subtype", []byte{0x04, 0, 0, 0}},
		{0x0134, "7/4 size", []byte{0x08, 0, 0, 0}},
		{0x0138, "7/4 count", []byte{0x03, 0, 0, 0}},
		{0x013C, "7/4 sysmis", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xEF, 0xFF}},
		{0x0144, "7/4 highest", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xEF, 0x7F}},
		{0x014C, "7/4 lowest", []byte{0xFE, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xEF, 0xFF}},

		// --- record 7/11 ---
		{0x0154, "7/11 rec_type", []byte{0x07, 0, 0, 0}},
		{0x0158, "7/11 subtype", []byte{0x0B, 0, 0, 0}},
		{0x015C, "7/11 size", []byte{0x04, 0, 0, 0}},
		{0x0160, "7/11 count (3 per physical variable)", []byte{0x06, 0, 0, 0}},
		{0x0164, "7/11 ID measure (scale)", []byte{0x03, 0, 0, 0}},
		{0x0168, "7/11 ID display width", []byte{0x0A, 0, 0, 0}},
		{0x016C, "7/11 ID alignment (right)", []byte{0x01, 0, 0, 0}},
		{0x0170, "7/11 REGION measure (nominal)", []byte{0x01, 0, 0, 0}},
		{0x0174, "7/11 REGION display width", []byte{0x05, 0, 0, 0}},
		{0x0178, "7/11 REGION alignment (left)", []byte{0x00, 0, 0, 0}},

		// --- record 7/16 ---
		{0x017C, "7/16 rec_type", []byte{0x07, 0, 0, 0}},
		{0x0180, "7/16 subtype", []byte{0x10, 0, 0, 0}},
		{0x0184, "7/16 size", []byte{0x08, 0, 0, 0}},
		{0x0188, "7/16 count", []byte{0x02, 0, 0, 0}},
		{0x018C, "7/16 constant 1", []byte{0x01, 0, 0, 0, 0, 0, 0, 0}},
		{0x0194, "7/16 case count", bytes.Repeat([]byte{0}, 8)},

		// --- record 7/20 ---
		{0x019C, "7/20 rec_type", []byte{0x07, 0, 0, 0}},
		{0x01A0, "7/20 subtype", []byte{0x14, 0, 0, 0}},
		{0x01A4, "7/20 size", []byte{0x01, 0, 0, 0}},
		{0x01A8, "7/20 count", []byte{0x05, 0, 0, 0}},
		{0x01AC, "7/20 payload", []byte("UTF-8")},

		// --- terminator ---
		{0x01B1, "999 rec_type", []byte{0xE7, 0x03, 0, 0}},
		{0x01B5, "999 filler", []byte{0, 0, 0, 0}},
	}
	for _, tc := range cases {
		end := tc.offset + len(tc.want)
		if end > len(got) {
			t.Errorf("%s at 0x%04X: runs past the end of the %d-byte dictionary", tc.field, tc.offset, len(got))
			continue
		}
		if g := got[tc.offset:end]; !bytes.Equal(g, tc.want) {
			t.Errorf("%s at 0x%04X:\n got % X\nwant % X", tc.field, tc.offset, g, tc.want)
		}
	}

	// The plan's patch offsets must be the ones the walkthrough names, or a
	// streaming encoder would write a case count over some other field.
	if plan.CaseCountOffset != 0x0050 {
		t.Errorf("CaseCountOffset = 0x%04X, want 0x0050 (the header ncases field)", plan.CaseCountOffset)
	}
	if plan.CaseCount64Offset != 0x0194 {
		t.Errorf("CaseCount64Offset = 0x%04X, want 0x0194 (the record 7/16 count)", plan.CaseCount64Offset)
	}

	// The absence of a record type 3 is an assertion, not an omission: a
	// synthesised categorical must not acquire invented value codes.
	if idx := indexOfRecord(got, recTypeValueLabel); idx >= 0 {
		t.Errorf("a record type 3 appears at 0x%04X; the synthesised path has no recorded SPSS codes and must not invent any", idx)
	}
}

// indexOfRecord finds a top-level record tag by walking the dictionary the
// way a reader does. A naive byte search would match the same four bytes
// inside a label or a payload.
func indexOfRecord(b []byte, want int32) int {
	off := headerSize
	for off+4 <= len(b) {
		rt := int32(binary.LittleEndian.Uint32(b[off:]))
		if rt == want {
			return off
		}
		switch rt {
		case recTypeVariable:
			if off+32 > len(b) {
				return -1
			}
			hasLabel := int32(binary.LittleEndian.Uint32(b[off+8:]))
			nMissing := int32(binary.LittleEndian.Uint32(b[off+12:]))
			at := off + 32
			if hasLabel == 1 {
				n := int(binary.LittleEndian.Uint32(b[at:]))
				at += 4 + roundUp(n, 4)
			}
			if nMissing < 0 {
				nMissing = -nMissing
			}
			off = at + int(nMissing)*elementSize
		case recTypeExtension:
			size := int(binary.LittleEndian.Uint32(b[off+8:]))
			count := int(binary.LittleEndian.Uint32(b[off+12:]))
			off += 16 + size*count
		case recTypeDocument:
			n := int(binary.LittleEndian.Uint32(b[off+4:]))
			off += 8 + n*documentLineLen
		case recTypeTerminator:
			return -1
		default:
			return -1
		}
	}
	return -1
}

// TestEmittedDictionary_ByteIdenticalToTheFixtureGenerator diffs this
// writer's output against internal/spsstest's, byte for byte.
//
// # Why check against the generator rather than duplicate its knowledge
//
// There are now THREE independent encoders of this format in the tree: the
// fixture generator, this writer, and the reader. They were written from the
// specification separately and none of them shares a table with another —
// internal/spsstest says so explicitly about the bytecode command bytes, for
// the same reason. That independence is the whole value: a byte-for-byte
// agreement between two of them is evidence about the SPEC, where a
// round trip through one of them is only evidence about itself.
//
// So the generator is used here as an ORACLE and not as an implementation.
// Merging them would destroy exactly what makes this test worth running, and
// the two have opposite jobs besides: the generator's purpose is to produce
// deliberately varied and MALFORMED files, and a writer must never be able to
// produce one.
//
// The spec below is pinned to the fields this writer does not vary — product
// name, creation stamp, charset — so that a difference is a difference in
// how the two read the FORMAT, never in what they were asked to write.
func TestEmittedDictionary_ByteIdenticalToTheFixtureGenerator(t *testing.T) {
	plan := emit(t, DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: compressionNone})

	zero := int64(0)
	spec := spsstest.Spec{
		ProductName:   writerProductName,
		DisplayParams: true,
		Vars: []spsstest.Var{
			{
				Name: "ID", Measure: spsstest.MeasureScale, Align: spsstest.AlignRight,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 10},
				Write: spsstest.Format{Type: spsstest.FormatF, Width: 10},
			},
			{
				Name: "REGION", Width: 5, Label: "Region",
				Measure: spsstest.MeasureNominal, Align: spsstest.AlignLeft,
				Print: spsstest.Format{Type: spsstest.FormatA, Width: 5},
				Write: spsstest.Format{Type: spsstest.FormatA, Width: 5},
			},
		},
		MachineIntegerInfo: &spsstest.MachineIntegerInfo{
			FloatingPointRep: 1, CompressionCode: 1, Endianness: 2, CharacterCode: 65001,
		},
		MachineFloatInfo: &spsstest.MachineFloatInfo{
			SysMis:  -math.MaxFloat64,
			Highest: math.MaxFloat64,
			Lowest:  math.Nextafter(-math.MaxFloat64, 0),
		},
		CaseCount64:       &zero,
		CharacterEncoding: "UTF-8",
	}
	want := build(t, spec)
	if bytes.Equal(want, plan.Bytes) {
		return
	}

	t.Errorf("the writer and the fixture generator disagree: writer emitted %d bytes, generator %d",
		len(plan.Bytes), len(want))
	n := min(len(want), len(plan.Bytes))
	for i := 0; i < n; i++ {
		if want[i] != plan.Bytes[i] {
			lo, hi := max(0, i-16), min(n, i+32)
			t.Fatalf("first difference at 0x%04X\n   writer % X\ngenerator % X", i, plan.Bytes[lo:hi], want[lo:hi])
		}
	}
}

// ---------------------------------------------------------------------------
// The code / label / ID triple
// ---------------------------------------------------------------------------

// TestEmittedDictionary_EmitsOriginalCodesNotDictionaryPositions is the
// acceptance criterion this whole effort turns on.
//
// The source labels the values 1, 5 and 9; the cohort stores them at
// dictionary positions 0, 1 and 2. Emitting the positions would produce a
// file that opens, looks right, and silently re-points every piece of
// downstream syntax that says `IF q1 EQ 5`.
//
// Checked at the BYTE level — the eight-byte value slot of each record type 3
// pair — rather than through our own reader, because the point is what the
// file says and not what we make of it.
func TestEmittedDictionary_EmitsOriginalCodesNotDictionaryPositions(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "Q1", LongName: "Satisfaction"}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1)}, {spsstest.Num(5)}, {spsstest.Num(9)},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Yes"},
				{Value: spsstest.Num(5), Label: "Maybe"},
				{Value: spsstest.Num(9), Label: "No"},
			},
		}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})

	got := valueLabelPairs(t, plan.Bytes)
	want := []labelPair{{1, "Yes"}, {5, "Maybe"}, {9, "No"}}
	if len(got) != len(want) {
		t.Fatalf("emitted %d value-label pair(s), want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("value-label pair %d = %+v, want %+v — a positional dictionary ID reached the wire in place of the source's own code",
				i, got[i], want[i])
		}
	}

	// The same codes must reach the DATA encoder, or the labels would name
	// values no case carries.
	col := planColumn(t, plan, "Satisfaction")
	if col.Encoding != EncodeCategoricalCode {
		t.Fatalf("Satisfaction encodes as %v, want %v", col.Encoding, EncodeCategoricalCode)
	}
	for id, wantCode := range []float64{1, 5, 9} {
		if id >= len(col.Categories) {
			t.Fatalf("the plan carries %d categor(ies), want at least %d", len(col.Categories), id+1)
		}
		got := col.Categories[id]
		if got.Code != wantCode {
			t.Errorf("dictionary ID %d maps to SPSS code %v, want %v", id, got.Code, wantCode)
		}
		if !got.Known {
			t.Errorf("dictionary ID %d is not marked Known, but its code came from the sidecar's recorded triple", id)
		}
	}
}

// labelPair is one decoded record type 3 pair.
type labelPair struct {
	value float64
	label string
}

// valueLabelPairs decodes every record type 3 in an emitted dictionary.
func valueLabelPairs(t *testing.T, b []byte) []labelPair {
	t.Helper()
	at := indexOfRecord(b, recTypeValueLabel)
	if at < 0 {
		t.Fatal("the emitted dictionary carries no record type 3")
	}
	off := at + 4
	count := int(binary.LittleEndian.Uint32(b[off:]))
	off += 4
	out := make([]labelPair, 0, count)
	for i := 0; i < count; i++ {
		v := math.Float64frombits(binary.LittleEndian.Uint64(b[off:]))
		off += elementSize
		n := int(b[off])
		text := string(b[off+1 : off+1+n])
		off += roundUp(n+1, elementSize)
		out = append(out, labelPair{v, text})
	}
	return out
}

// TestEmittedDictionary_UnlabelledCodesGetNoInventedLabel checks the other
// half of the triple's flags.
//
// A code the DATA carried and no record type 3 named is legal SPSS, and it
// occupies a Pulse dictionary ID like any other. Emitting a label for it
// would put a string in the file the source never had; dropping the code
// from the plan would lose the value. Both must hold at once.
func TestEmittedDictionary_UnlabelledCodesGetNoInventedLabel(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "Q1"}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1)}, {spsstest.Num(2)}, {spsstest.Num(77)},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Yes"},
				{Value: spsstest.Num(2), Label: "No"},
			},
		}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})

	pairs := valueLabelPairs(t, plan.Bytes)
	if len(pairs) != 2 {
		t.Fatalf("emitted %d value-label pair(s), want 2 — 77 was observed but never labelled: %+v", len(pairs), pairs)
	}
	for _, p := range pairs {
		if p.value == 77 {
			t.Errorf("the unlabelled code 77 acquired the label %q", p.label)
		}
	}

	col := planColumn(t, plan, "Q1")
	found := false
	for _, c := range col.Categories {
		if c.Code == 77 {
			found = true
		}
	}
	if !found {
		t.Error("the unlabelled code 77 is absent from the plan's category table; the data encoder would have nothing to write for that dictionary ID")
	}
}

// TestEmittedDictionary_DeclaredButUnobservedLabelSurvives is the mirror
// case: a label the source declared and no case used. It still occupies its
// ID, so the round trip owes it back.
func TestEmittedDictionary_DeclaredButUnobservedLabelSurvives(t *testing.T) {
	schema, res := exportFixture(t, richSpec())
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})

	var got []string
	for _, p := range valueLabelPairs(t, plan.Bytes) {
		got = append(got, p.label)
	}
	if !slicesContains(got, "Never observed") {
		t.Errorf("emitted labels %v do not include \"Never observed\", which richSpec declares for the code 7 and no case uses", got)
	}
}

func slicesContains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Round trip through this effort's own reader
// ---------------------------------------------------------------------------

// TestEmittedDictionary_ReparsesWithEverythingIntact is the round-trip
// criterion: measure levels, formats, labels, documents, attributes,
// response sets and the weight variable all come back.
func TestEmittedDictionary_ReparsesWithEverythingIntact(t *testing.T) {
	schema, res := exportFixture(t, richSpec())
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})
	d := reparse(t, plan)

	for _, w := range d.warnings {
		t.Errorf("re-parsing the emitted dictionary warned: %v", w)
	}

	t.Run("variables and their names", func(t *testing.T) {
		want := []string{"Satisfaction", "WT", "REGION", "MD1", "MD2"}
		if len(d.vars) != len(want) {
			t.Fatalf("re-parsed %d variable(s), want %d", len(d.vars), len(want))
		}
		for i, n := range want {
			if got := d.vars[i].fieldName(); got != n {
				t.Errorf("variable %d is %q, want %q", i, got, n)
			}
		}
		// The long name must survive, or every request referencing the
		// field breaks on the way back in.
		if got := d.vars[0].name; got != "Q1" {
			t.Errorf("the short name of Satisfaction came back as %q, want %q", got, "Q1")
		}
	})

	t.Run("formats and measure levels", func(t *testing.T) {
		q1 := readVar(t, d, "Satisfaction")
		if q1.print.code != fmtF || q1.print.width != 8 {
			t.Errorf("Satisfaction print format = %+v, want F8", q1.print)
		}
		if q1.display.measure != measureOrdinal {
			t.Errorf("Satisfaction measure = %v, want ordinal", q1.display.measure)
		}
		if !q1.display.hasWidth || q1.display.width != 6 {
			t.Errorf("Satisfaction display width = %d (hasWidth %v), want 6", q1.display.width, q1.display.hasWidth)
		}
		if q1.display.align != alignRight {
			t.Errorf("Satisfaction alignment = %v, want right", q1.display.align)
		}
		wt := readVar(t, d, "WT")
		if wt.write.decimals != 3 {
			t.Errorf("WT write format = %+v, want 3 decimals", wt.write)
		}
	})

	t.Run("variable labels", func(t *testing.T) {
		if got := readVar(t, d, "Satisfaction").label; got != "Overall satisfaction" {
			t.Errorf("Satisfaction label = %q", got)
		}
		md1 := readVar(t, d, "MD1")
		if md1.hasLabel {
			t.Errorf("MD1 acquired the label %q; the source declared none", md1.label)
		}
	})

	t.Run("weight, file label and documents", func(t *testing.T) {
		wt := readVar(t, d, "WT")
		if d.header.weightIndex != wt.index {
			t.Errorf("weight_index = %d, want %d (the WT element index)", d.header.weightIndex, wt.index)
		}
		if d.header.fileLabel != "Wave 3" {
			t.Errorf("file label = %q, want %q", d.header.fileLabel, "Wave 3")
		}
		if len(d.documents) != 2 {
			t.Fatalf("re-parsed %d document line(s), want 2", len(d.documents))
		}
		if got := strings.TrimRight(d.documents[0], " "); got != "Fielded 2024-01" {
			t.Errorf("document line 0 = %q", got)
		}
		if len(d.documents[0]) != documentLineLen {
			t.Errorf("document line 0 is %d bytes, want the fixed %d", len(d.documents[0]), documentLineLen)
		}
	})

	t.Run("response sets and variable sets", func(t *testing.T) {
		if len(d.mrSets) != 2 {
			t.Fatalf("re-parsed %d response set(s), want 2", len(d.mrSets))
		}
		byName := map[string]multipleResponseSet{}
		for _, s := range d.mrSets {
			byName[s.setName()] = s
		}
		brands, ok := byName["$brands"].(*mrDichotomySet)
		if !ok {
			t.Fatalf("$brands came back as %T, want a dichotomy set", byName["$brands"])
		}
		if brands.countedValue != "1" {
			t.Errorf("$brands counted value = %q, want %q", brands.countedValue, "1")
		}
		if got := strings.Join(brands.vars, ","); got != "MD1,MD2" {
			t.Errorf("$brands members = %q, want MD1,MD2 — member ORDER is answer order", got)
		}
		if _, ok := byName["$ranks"].(*mrCategorySet); !ok {
			t.Fatalf("$ranks came back as %T, want a category set", byName["$ranks"])
		}
		if len(d.variableSets) != 1 || d.variableSets[0].name != "Demographics" {
			t.Errorf("variable sets = %+v, want one named Demographics", d.variableSets)
		}
	})

	t.Run("attributes are re-emitted verbatim", func(t *testing.T) {
		for _, tc := range []struct {
			subtype int32
			want    string
		}{
			{extFileAttributes, "$@Survey('Wave 3')\n"},
			{extVarAttributes, "Q1:$@Origin('core')\n"},
		} {
			x, ok := d.rawExtension(tc.subtype)
			if !ok {
				t.Errorf("no record 7/%d in the emitted dictionary", tc.subtype)
				continue
			}
			if got := string(x.payload); got != tc.want {
				t.Errorf("record 7/%d payload = %q, want %q", tc.subtype, got, tc.want)
			}
		}
	})
}

// TestEmittedDictionary_MissingSpecificationsRoundTrip checks both homes a
// missing-value specification can have.
func TestEmittedDictionary_MissingSpecificationsRoundTrip(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "Q1", LongName: "Satisfaction",
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(98), spsstest.Num(99)}},
			},
			{
				Name: "AGE", LongName: "AgeYears",
				Missing: &spsstest.MissingValues{Range: &spsstest.MissingRange{Low: 90, High: 120}},
			},
			{Name: "CODE", LongName: "LongCode", Width: 20},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(40), spsstest.Text("ALPHA")},
			{spsstest.Num(98), spsstest.Num(95), spsstest.Text("BETA")},
		},
		LongStringMissingValues: []spsstest.LongStringMissingValues{{
			Var: "LongCode", Values: []string{"BETA"},
		}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 2, Compression: compressionNone})
	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}

	q1 := readVar(t, d, "Satisfaction")
	if q1.missing.code != 2 {
		t.Fatalf("Satisfaction n_missing_values = %d, want 2", q1.missing.code)
	}
	if got := q1.missing.numeric; len(got) != 2 || got[0] != 98 || got[1] != 99 {
		t.Errorf("Satisfaction discrete missing values = %v, want [98 99]", got)
	}

	age := readVar(t, d, "AgeYears")
	if age.missing.code != -2 {
		t.Fatalf("AgeYears n_missing_values = %d, want -2 (a lo..hi range)", age.missing.code)
	}
	if got := age.missing.numeric; len(got) != 2 || got[0] != 90 || got[1] != 120 {
		t.Errorf("AgeYears range = %v, want [90 120]", got)
	}

	// A string wider than the eight-byte slot cannot state its missing
	// values in a record type 2 at all, so the writer must move them to
	// record 7/22 — a mechanical rule, since the source does not record
	// which record they arrived on.
	if _, ok := d.rawExtension(extLongStringMissing); !ok {
		t.Fatal("no record 7/22 was emitted for a 20-byte string's missing value")
	}
	code := readVar(t, d, "LongCode")
	if got := code.missing.text; len(got) != 1 || got[0] != "BETA" {
		t.Errorf("LongCode missing values came back as %v, want [BETA]", got)
	}
}

// TestEmittedDictionary_VeryLongStringsKeepTheirSegmentation checks the
// record 7/14 half: a string past 255 bytes goes back out as the physical
// variables the source declared.
func TestEmittedDictionary_VeryLongStringsKeepTheirSegmentation(t *testing.T) {
	long := strings.Repeat("a", 300)
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "NOTES", LongName: "OpenEnded", Width: 300, Label: "Open ended"},
		},
		Cases: [][]spsstest.Value{{spsstest.Text(long)}, {spsstest.Text("short")}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 2, Compression: compressionNone})

	col := planColumn(t, plan, "OpenEnded")
	if col.Width != 300 {
		t.Errorf("OpenEnded plans width %d, want the LOGICAL 300", col.Width)
	}
	if len(col.Segments) != 2 {
		t.Fatalf("OpenEnded plans %d physical segment(s), want 2 (252 content bytes each but the last)", len(col.Segments))
	}
	if col.Segments[0].Width != 255 || col.Segments[0].Content != 252 {
		t.Errorf("segment 0 = %+v, want declared width 255 carrying 252 content bytes", col.Segments[0])
	}
	if col.Segments[1].Content != 48 {
		t.Errorf("segment 1 carries %d content byte(s), want 48 (300 - 252)", col.Segments[1].Content)
	}

	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	v := readVar(t, d, "OpenEnded")
	if v.vls == nil {
		t.Fatal("the re-parsed variable is not a folded very long string; record 7/14 did not survive")
	}
	if v.width != 300 {
		t.Errorf("re-parsed width = %d, want 300", v.width)
	}
}

// TestLongStringRecords_NameTheFinalName pins the ECOSYSTEM constraint.
//
// ReadStat — the C reader behind R's haven, Python's pyreadstat and most of
// what else opens a `.sav` — REFUSES to parse a file whose record 7/21 or
// 7/22 entry names a variable by its SHORT name when a long name exists.
// This reader tolerates both, long first; that tolerance is a reader's
// licence and emphatically not a writer's. A writer that spelled the short
// name here would produce files most of the ecosystem rejects.
func TestLongStringRecords_NameTheFinalName(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "CODE", LongName: "LongCodeName", Width: 20}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("ALPHA")}, {spsstest.Text("BETA")},
		},
		LongStringValueLabels: []spsstest.LongStringValueLabels{{
			Var: "LongCodeName",
			Labels: []spsstest.LongStringValueLabel{
				{Value: "ALPHA", Label: "Alpha channel"},
			},
		}},
		LongStringMissingValues: []spsstest.LongStringMissingValues{{
			Var: "LongCodeName", Values: []string{"BETA"},
		}},
	}
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 2, Compression: compressionNone})
	d := reparse(t, plan)

	for _, subtype := range []int32{extLongStringValueLabels, extLongStringMissing} {
		x, ok := d.rawExtension(subtype)
		if !ok {
			t.Errorf("no record 7/%d was emitted", subtype)
			continue
		}
		// The name is a counted string at the head of the payload: an int32
		// byte length then the bytes.
		n := int(binary.LittleEndian.Uint32(x.payload))
		got := string(x.payload[4 : 4+n])
		if got != "LongCodeName" {
			t.Errorf("record 7/%d names the variable %q, want the FINAL name %q; ReadStat refuses a file that spells the short name here",
				subtype, got, "LongCodeName")
		}
	}
}

// ---------------------------------------------------------------------------
// The routed MOYR / QYR / WKYR decision
// ---------------------------------------------------------------------------

// TestEmittedDictionary_MoyrKeepsItsFormatAndItsRawSeconds is E2-S6's
// deferred export-side question, answered.
//
// MOYR (28), QYR (29) and WKYR (30) are date-VALUED in SPSS but are not
// day-resolution, so E2-S6 declined to truncate them to a `date` and mapped
// them to f64 holding the raw SPSS seconds, keeping the format code in the
// sidecar.
//
// The export answer follows from that directly: emit the recorded format
// code unchanged over the unchanged value. Nothing was converted on the way
// in, so nothing needs converting on the way out, and the format code is the
// only thing that makes those seconds render as a month and a year rather
// than as 1.4e10. Downgrading them to a plain F would leave a column whose
// values are perfect and whose meaning is gone.
func TestEmittedDictionary_MoyrKeepsItsFormatAndItsRawSeconds(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint8
	}{
		{"MOYR", 28}, {"QYR", 29}, {"WKYR", 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.Spec{
				Vars: []spsstest.Var{{
					Name: "PERIOD", LongName: "FiscalPeriod",
					Print: spsstest.Format{Type: spsstest.FormatType(tc.code), Width: 6},
					Write: spsstest.Format{Type: spsstest.FormatType(tc.code), Width: 6},
				}},
				Cases: [][]spsstest.Value{{spsstest.Num(13166064000)}},
			}
			schema, res := exportFixture(t, spec)
			plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 1, Compression: compressionNone})

			col := planColumn(t, plan, "FiscalPeriod")
			if col.PrintFormat.Code != tc.code || col.WriteFormat.Code != tc.code {
				t.Errorf("emitted print/write format codes = %d/%d, want %d — the format is the only record of what the seconds mean",
					col.PrintFormat.Code, col.WriteFormat.Code, tc.code)
			}
			if col.Encoding != EncodeNumeric {
				t.Errorf("encoding = %v, want %v: the value is raw SPSS seconds and must pass through unconverted",
					col.Encoding, EncodeNumeric)
			}
			if col.FieldType != encoding.FieldTypeF64 {
				t.Errorf("the cohort field is %v, want f64", col.FieldType)
			}
			if got := readVar(t, reparse(t, plan), "FiscalPeriod").print.code; got != tc.code {
				t.Errorf("the re-parsed print format code = %d, want %d", got, tc.code)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sidecar resolution, seams and refusals
// ---------------------------------------------------------------------------

// TestBuildDictionary_AbsentSidecarSynthesisesAndCarriesItsWarning checks
// the E5-S1 handoff: an absent sidecar is benign, and its warning must reach
// the caller rather than being swallowed by the writer that acted on it.
func TestBuildDictionary_AbsentSidecarSynthesisesAndCarriesItsWarning(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "out.pulse", []byte("not a real cohort"), 0644); err != nil {
		t.Fatalf("writing cohort: %v", err)
	}
	res, err := LoadSidecar(fs, "out.pulse", WriterOptions{})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	plan := emit(t, DictionaryRequest{Schema: referenceSchema(t), Sidecar: res, Cases: 0, Compression: compressionNone})

	if !plan.Synthesised {
		t.Error("the plan is not marked synthesised, but no sidecar was found")
	}
	if plan.Status != SidecarStatusAbsent {
		t.Errorf("plan status = %v, want %v", plan.Status, SidecarStatusAbsent)
	}
	if len(plan.Warnings) == 0 || plan.Warnings[0].Code != perr.PULSE_SPSS_SIDECAR_ABSENT {
		t.Fatalf("plan warnings = %+v, want the resolution's PULSE_SPSS_SIDECAR_ABSENT first", plan.Warnings)
	}
	reparse(t, plan)
}

// TestBuildDictionary_NilResolutionSynthesises checks the nil-receiver rule
// [SidecarResolution.Synthesise] states: no resolution is not a licence to
// invent one.
func TestBuildDictionary_NilResolutionSynthesises(t *testing.T) {
	plan := emit(t, DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: compressionNone})
	if !plan.Synthesised {
		t.Error("a nil resolution did not select the synthesised path")
	}
	if plan.Status != SidecarStatusUnknown {
		t.Errorf("plan status = %v, want %v for a nil resolution", plan.Status, SidecarStatusUnknown)
	}
}

// TestBuildDictionary_DerivedColumnsAreNotEmittedAndAreReported is the seam
// E5-S5 stands on.
//
// The sidecar's Variables list holds only SOURCE variables; the derived
// columns — the `<var>_missing` reason siblings and the multiple-dichotomy
// `set_*` convenience columns — live in its separate Derived registry. So
// the dictionary naturally carries exactly the source's variables, and the
// cohort fields left over are reported rather than silently dropped: E5-S5
// cross-checks them against the registry, and a field that is unbound and
// NOT derived is a column about to vanish.
func TestBuildDictionary_DerivedColumnsAreNotEmittedAndAreReported(t *testing.T) {
	schema, res := exportFixture(t, richSpec())
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})

	if len(plan.UnboundFields) == 0 {
		t.Fatal("richSpec derives a set_* column from $brands, so at least one cohort field must be unbound")
	}
	derived := map[string]bool{}
	for _, dc := range res.Document.Payload.Derived {
		derived[dc.Name] = true
	}
	for _, at := range plan.UnboundFields {
		name := schema.Fields[at].Name
		if !derived[name] {
			t.Errorf("cohort field %q is unbound but is not in the derived registry; it would be dropped silently", name)
		}
	}
	for _, c := range plan.Columns {
		if derived[c.FieldName] {
			t.Errorf("the derived column %q reached the emitted dictionary", c.FieldName)
		}
	}
}

// TestBuildDictionary_CaseCountIsPatchable checks the streaming seam: a
// writer that does not know its case count up front may emit the dictionary
// with -1 and patch both counts once the last case is written.
func TestBuildDictionary_CaseCountIsPatchable(t *testing.T) {
	unknown := emit(t, DictionaryRequest{Schema: referenceSchema(t), Cases: -1, Compression: compressionNone})
	if unknown.CaseCount64Offset != -1 {
		t.Errorf("an unknown case count emitted a record 7/16 at 0x%04X; there is nothing yet to state",
			unknown.CaseCount64Offset)
	}
	d := reparse(t, unknown)
	if d.header.caseCount != -1 {
		t.Errorf("header ncases = %d, want -1 for an unknown count", d.header.caseCount)
	}

	known := emit(t, DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: compressionNone})
	binary.LittleEndian.PutUint32(known.Bytes[known.CaseCountOffset:], uint32(int32(7)))
	binary.LittleEndian.PutUint64(known.Bytes[known.CaseCount64Offset:], uint64(int64(7)))
	patched := reparse(t, known)
	if patched.header.caseCount != 7 {
		t.Errorf("patched header ncases = %d, want 7", patched.header.caseCount)
	}
	if !patched.hasCaseCount64 || patched.caseCount64 != 7 {
		t.Errorf("patched record 7/16 count = %d (present %v), want 7", patched.caseCount64, patched.hasCaseCount64)
	}
}

// TestBuildDictionary_RefusesWhatItCannotWrite checks the guard rails.
func TestBuildDictionary_RefusesWhatItCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  DictionaryRequest
		want perr.Code
	}{
		{
			name: "no schema",
			req:  DictionaryRequest{Cases: 0},
			want: perr.DATA_FILE,
		},
		{
			name: "ZSAV is out of scope for this effort",
			req:  DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: compressionZSAV},
			want: perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED,
		},
		{
			name: "a compression flag the format does not define",
			req:  DictionaryRequest{Schema: referenceSchema(t), Cases: 0, Compression: 9},
			want: perr.PULSE_SPSS_COMPRESSION_INVALID,
		},
		{
			name: "a cohort with no fields",
			req:  DictionaryRequest{Schema: &encoding.Schema{}, Cases: 0},
			want: perr.PULSE_SPSS_DICT_INVALID,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildDictionary(tc.req)
			if got := codeOf(t, err); got != tc.want {
				t.Errorf("code = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestBuildDictionary_SidecarThatDoesNotDescribeTheCohortIsRefused covers
// the one binding fault a fresh document should never show.
//
// The fingerprint pins the cohort's bytes, so a variable the schema has no
// field for means the document is not about this cohort. There is no partial
// application: a dictionary half from a stranger's file is the failure mode
// the whole staleness rule exists to prevent.
func TestBuildDictionary_SidecarThatDoesNotDescribeTheCohortIsRefused(t *testing.T) {
	_, res := exportFixture(t, richSpec())
	other := &encoding.Schema{Fields: []encoding.Field{{Name: "unrelated", Type: encoding.FieldTypeF64}}}

	_, err := BuildDictionary(DictionaryRequest{Schema: other, Sidecar: res, Cases: 3})
	if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_INVALID {
		t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_INVALID", got)
	}
}

// TestBuildDictionary_ByteOrderIsAlwaysLittleEndian pins the one place this
// writer deliberately does not reproduce the source.
//
// Byte order carries no information about the DATA — every value is
// identical either way — so re-emitting a big-endian source's order would
// buy nothing and cost compatibility with the many tools that only ever meet
// little-endian files. The record 7/3 endianness field must agree, or the
// file states two orders and fails the reader's cross-check.
func TestBuildDictionary_ByteOrderIsAlwaysLittleEndian(t *testing.T) {
	spec := richSpec()
	spec.ByteOrder = spsstest.BigEndian
	schema, res := exportFixture(t, spec)
	if res.Document.Payload.Source.ByteOrder != "big" {
		t.Fatalf("the fixture's recorded byte order is %q, want big — the test is not exercising what it claims",
			res.Document.Payload.Source.ByteOrder)
	}

	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})
	if plan.ByteOrder != binary.ByteOrder(binary.LittleEndian) {
		t.Errorf("plan byte order = %v, want little-endian", plan.ByteOrder)
	}
	d := reparse(t, plan)
	for _, w := range d.warnings {
		t.Errorf("re-parsing warned: %v", w)
	}
	if d.byteOrder != binary.ByteOrder(binary.LittleEndian) {
		t.Errorf("the emitted file reads back as %v, want little-endian", d.byteOrder)
	}
	if d.machineInteger.present && d.machineInteger.endianness != writerEndiannessCode {
		t.Errorf("record 7/3 endianness = %d, want %d; a contradiction with the layout code is a hard error on the way in",
			d.machineInteger.endianness, writerEndiannessCode)
	}
}

// TestBuildDictionary_ProvenanceFieldsDescribeTheseBytes checks the second
// and third deliberate departures from verbatim.
func TestBuildDictionary_ProvenanceFieldsDescribeTheseBytes(t *testing.T) {
	spec := richSpec()
	spec.ProductName = "@(#) SPSS DATA FILE IBM SPSS Statistics 29"
	schema, res := exportFixture(t, spec)
	plan := emit(t, DictionaryRequest{Schema: schema, Sidecar: res, Cases: 3, Compression: compressionNone})
	d := reparse(t, plan)

	if strings.Contains(d.header.productName, "IBM SPSS") {
		t.Errorf("prod_name = %q; it identifies the program that wrote THESE bytes, and claiming the source's identity would be false provenance",
			d.header.productName)
	}
	if !strings.Contains(d.header.productName, "pulse") {
		t.Errorf("prod_name = %q, want it to name pulse", d.header.productName)
	}
	// The source declared cp1252; these bytes are UTF-8 until E5-S4 teaches
	// the writer to transcode, and record 7/20 must describe the bytes.
	if d.charsetName != writerCharsetName {
		t.Errorf("record 7/20 declares %q, want %q — the declaration follows the bytes", d.charsetName, writerCharsetName)
	}
	if got := res.Document.Payload.Charset.DeclaredName; got == "" {
		t.Error("the fixture recorded no source charset; the test is not exercising what it claims")
	}
}
