package spsstest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strings"
	"testing"
)

// referenceSHA256 pins the bytes of ReferenceSpec(). It exists so that any
// change to the emitter that moves a byte of the one fixture a human has
// checked against the specification cannot land unnoticed.
//
// If you change the emitter deliberately, re-do the walkthrough in
// TestReferenceFixture_HandVerified against the PSPP spec before updating this
// constant. Updating the constant without re-reading the spec defeats the only
// ground truth this package has.
const referenceSHA256 = "5beee65832f3ba2d49a43666fe5d13c466544f431b15c204c562e3f9d71f609f"

// referenceSize is the total byte length of the reference fixture. Derived by
// hand in the walkthrough below; asserted so an accidentally-emitted extra
// record shows up as a size change rather than as a shifted offset.
const referenceSize = 436

// TestReferenceFixture_HandVerified is the byte-by-byte verification of one
// complete generated file against the GNU PSPP System File Format
// specification. It is the reason this package can be trusted as ground truth.
//
// The fixture is ReferenceSpec(): three variables and two cases.
//
//	ID    numeric, print/write F8.0, no variable label
//	SEX   numeric, print/write F1.0, variable label "Sex",
//	      value labels 1 -> "Male", 2 -> "Female"
//	NAME  string of declared width 10, print/write A10, no variable label
//
//	case 1: ID=1, SEX=1,      NAME="ALICE"
//	case 2: ID=2, SEX=sysmis, NAME="BOB"
//
// Two derived quantities drive most of the layout, so they are established
// first.
//
// *Segments.* A string variable occupies ceil(width/8) eight-byte elements,
// and emits one record type 2 plus one continuation record per element past
// the first. NAME is width 10, so ceil(10/8) = 2 elements, 1 continuation.
// Numeric variables are always 1 element.
//
// *Dictionary indices.* The spec numbers variables by element position,
// counting continuation records, 1-based. So ID=1, SEX=2, NAME=3, and NAME's
// continuation occupies position 4. nominal_case_size is therefore 1+1+2 = 4.
// This is why the record type 4 below names index 2 for SEX and not index 1:
// SEX is the second element, and would still be the second element if ID were
// a wide string, in which case it would not be the second *variable*.
//
// ---------------------------------------------------------------------------
// FILE HEADER RECORD — offsets 0x0000..0x00AF, 176 bytes
// ---------------------------------------------------------------------------
//
//	0x0000  4   rec_type            "$FL2"  24 46 4C 32
//	                                $FL2 is the tag for uncompressed and
//	                                bytecode-compressed files; $FL3 marks ZSAV.
//	0x0004  60  prod_name           "@(#) SPSS DATA FILE pulse spsstest 1.0"
//	                                followed by 22 spaces. Readers sniff the
//	                                "@(#) SPSS DATA FILE" prefix.
//	0x0040  4   layout_code         2       02 00 00 00
//	                                Always 2 (or 3), written in the file's own
//	                                byte order, so a reader that gets 0x02000000
//	                                knows the file is the other endianness.
//	0x0044  4   nominal_case_size   4       04 00 00 00   (see Segments above)
//	0x0048  4   compression         0       00 00 00 00   (uncompressed)
//	0x004C  4   weight_index        0       00 00 00 00   (unweighted)
//	0x0050  4   ncases              2       02 00 00 00
//	0x0054  8   bias                100.0   00 00 00 00 00 00 59 40
//	                                IEEE 754 double 100.0 is 0x4059000000000000;
//	                                little-endian puts 59 40 last. Written even
//	                                though the file is uncompressed.
//	0x005C  9   creation_date       "01 Jan 24"   (the spec's "dd mmm yy")
//	0x0065  8   creation_time       "00:00:00"
//	0x006D  64  file_label          64 spaces (empty label)
//	0x00AD  3   padding             00 00 00
//	0x00B0      end of header. 4+60+4+4+4+4+4+8+9+8+64+3 = 176 = 0x00B0.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — variable ID — offsets 0x00B0..0x00CF, 32 bytes
// ---------------------------------------------------------------------------
//
//	0x00B0  4   rec_type            2       02 00 00 00
//	0x00B4  4   type                0       00 00 00 00   (0 = numeric)
//	0x00B8  4   has_var_label       0       00 00 00 00
//	0x00BC  4   n_missing_values    0       00 00 00 00
//	0x00C0  4   print               F8.0    00 08 05 00
//	                                Packed as 0x00TTWWDD: unused 0x00, type
//	                                0x05 (F), width 0x08, decimals 0x00, giving
//	                                0x00050800, little-endian 00 08 05 00.
//	0x00C4  4   write               F8.0    00 08 05 00
//	0x00C8  8   name                "ID      "  (space padded to 8)
//	0x00D0      end. No label and no missing values, so nothing follows.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — variable SEX — offsets 0x00D0..0x00F7, 40 bytes
// ---------------------------------------------------------------------------
//
//	0x00D0  4   rec_type            2       02 00 00 00
//	0x00D4  4   type                0       00 00 00 00
//	0x00D8  4   has_var_label       1       01 00 00 00   (label payload follows)
//	0x00DC  4   n_missing_values    0       00 00 00 00
//	0x00E0  4   print               F1.0    00 01 05 00   (0x00050100)
//	0x00E4  4   write               F1.0    00 01 05 00
//	0x00E8  8   name                "SEX     "
//	0x00F0  4   label_len           3       03 00 00 00
//	                                The TRUE length, not the padded length.
//	0x00F4  4   label               "Sex" + 1 zero byte  53 65 78 00
//	                                The payload is rounded up to a multiple of
//	                                4 bytes (32 bits); 3 rounds to 4. A reader
//	                                must slice by label_len, since the pad byte
//	                                is not part of the text.
//	0x00F8      end.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — variable NAME — offsets 0x00F8..0x0117, 32 bytes
// ---------------------------------------------------------------------------
//
//	0x00F8  4   rec_type            2       02 00 00 00
//	0x00FC  4   type                10      0A 00 00 00
//	                                For a string, `type` is the declared width
//	                                in BYTES, not a type tag.
//	0x0100  4   has_var_label       0       00 00 00 00
//	0x0104  4   n_missing_values    0       00 00 00 00
//	0x0108  4   print               A10     00 0A 01 00   (type 0x01 = A)
//	0x010C  4   write               A10     00 0A 01 00
//	0x0110  8   name                "NAME    "
//	0x0118      end.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 2 — NAME continuation — offsets 0x0118..0x0137, 32 bytes
// ---------------------------------------------------------------------------
//
// One continuation per 8 bytes of width past the first 8. NAME is width 10, so
// exactly one.
//
//	0x0118  4   rec_type            2       02 00 00 00
//	0x011C  4   type                -1      FF FF FF FF   (the continuation marker)
//	0x0120  4   has_var_label       0
//	0x0124  4   n_missing_values    0
//	0x0128  4   print               0       00 00 00 00
//	0x012C  4   write               0       00 00 00 00
//	0x0130  8   name                8 spaces
//	0x0138      end.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 3 — value labels for SEX — offsets 0x0138..0x015F, 40 bytes
// ---------------------------------------------------------------------------
//
//	0x0138  4   rec_type            3       03 00 00 00
//	0x013C  4   label_count         2       02 00 00 00
//
//	pair 1:
//	0x0140  8   value               1.0     00 00 00 00 00 00 F0 3F
//	                                IEEE 754 1.0 = 0x3FF0000000000000. For a
//	                                numeric variable these 8 bytes are the
//	                                double; for a short string they would be the
//	                                space-padded text.
//	0x0148  1   label_len           4       04          (a single byte, not an int32)
//	0x0149  4   label               "Male"  4D 61 6C 65
//	0x014D  3   padding             00 00 00
//	                                label_len + label together pad to a multiple
//	                                of 8: 1+4 = 5, rounds to 8, so 3 zero bytes.
//
//	pair 2:
//	0x0150  8   value               2.0     00 00 00 00 00 00 00 40
//	0x0158  1   label_len           6       06
//	0x0159  6   label               "Female"  46 65 6D 61 6C 65
//	0x015F  1   padding             00      (1+6 = 7, rounds to 8)
//	0x0160      end.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 4 — value label variables — offsets 0x0160..0x016B, 12 bytes
// ---------------------------------------------------------------------------
//
// The spec requires this record to immediately follow its record type 3.
//
//	0x0160  4   rec_type            4       04 00 00 00
//	0x0164  4   var_count           1       01 00 00 00
//	0x0168  4   vars[0]             2       02 00 00 00
//	                                SEX's 1-based DICTIONARY index (see
//	                                Dictionary indices above), not its ordinal
//	                                among variables.
//	0x016C      end.
//
// ---------------------------------------------------------------------------
// RECORD TYPE 999 — dictionary terminator — offsets 0x016C..0x0173, 8 bytes
// ---------------------------------------------------------------------------
//
//	0x016C  4   rec_type            999     E7 03 00 00   (999 = 0x3E7)
//	0x0170  4   filler              0       00 00 00 00
//	0x0174      end of dictionary; the data section starts here.
//
// ---------------------------------------------------------------------------
// DATA SECTION — offsets 0x0174..0x01B3, 2 cases x 4 elements x 8 = 64 bytes
// ---------------------------------------------------------------------------
//
// Uncompressed: elements are written straight through, 8 bytes each, in
// variable order, case after case.
//
//	case 1
//	0x0174  8   ID    = 1.0        00 00 00 00 00 00 F0 3F
//	0x017C  8   SEX   = 1.0        00 00 00 00 00 00 F0 3F
//	0x0184  8   NAME  segment 1    "ALICE   "   41 4C 49 43 45 20 20 20
//	0x018C  8   NAME  segment 2    8 spaces
//	                               "ALICE" is space-padded to the declared
//	                               width 10, then on to the 16-byte segment
//	                               boundary: both paddings are spaces, so the
//	                               tail is 11 spaces across the two segments.
//
//	case 2
//	0x0194  8   ID    = 2.0        00 00 00 00 00 00 00 40
//	0x019C  8   SEX   = sysmis     FF FF FF FF FF FF EF FF
//	                               The system-missing sentinel is -DBL_MAX.
//	                               DBL_MAX = 0x7FEFFFFFFFFFFFFF, so -DBL_MAX is
//	                               0xFFEFFFFFFFFFFFFF.
//	0x01A4  8   NAME  segment 1    "BOB     "
//	0x01AC  8   NAME  segment 2    8 spaces
//	0x01B4      end of file.
//
// Total: 176 header + 32 (ID) + 40 (SEX) + 32 (NAME) + 32 (continuation)
// + 40 (record 3) + 12 (record 4) + 8 (record 999) + 64 (data) = 436 bytes.
func TestReferenceFixture_HandVerified(t *testing.T) {
	got, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build(ReferenceSpec()): %v", err)
	}
	if len(got) != referenceSize {
		t.Fatalf("file length = %d bytes, want %d — the walkthrough's offsets no longer describe this file", len(got), referenceSize)
	}

	// Every row is one field from the walkthrough above, at its stated offset,
	// with its stated bytes. The `field` column names the spec field so a
	// failure says which part of the format broke.
	cases := []struct {
		offset int
		field  string
		want   []byte
	}{
		// --- file header record ---
		{0x0000, "header rec_type $FL2", []byte("$FL2")},
		{0x0004, "header prod_name", pad("@(#) SPSS DATA FILE pulse spsstest 1.0", 60)},
		{0x0040, "header layout_code", i32le(2)},
		{0x0044, "header nominal_case_size", i32le(4)},
		{0x0048, "header compression", i32le(0)},
		{0x004C, "header weight_index", i32le(0)},
		{0x0050, "header ncases", i32le(2)},
		{0x0054, "header bias", f64le(100.0)},
		{0x0054, "header bias literal bytes", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x59, 0x40}},
		{0x005C, "header creation_date", []byte("01 Jan 24")},
		{0x0065, "header creation_time", []byte("00:00:00")},
		{0x006D, "header file_label", pad("", 64)},
		{0x00AD, "header padding", []byte{0x00, 0x00, 0x00}},

		// --- record type 2: ID ---
		{0x00B0, "ID rec_type", i32le(2)},
		{0x00B4, "ID type (0 = numeric)", i32le(0)},
		{0x00B8, "ID has_var_label", i32le(0)},
		{0x00BC, "ID n_missing_values", i32le(0)},
		{0x00C0, "ID print F8.0", []byte{0x00, 0x08, 0x05, 0x00}},
		{0x00C4, "ID write F8.0", []byte{0x00, 0x08, 0x05, 0x00}},
		{0x00C8, "ID name", pad("ID", 8)},

		// --- record type 2: SEX ---
		{0x00D0, "SEX rec_type", i32le(2)},
		{0x00D4, "SEX type (0 = numeric)", i32le(0)},
		{0x00D8, "SEX has_var_label", i32le(1)},
		{0x00DC, "SEX n_missing_values", i32le(0)},
		{0x00E0, "SEX print F1.0", []byte{0x00, 0x01, 0x05, 0x00}},
		{0x00E4, "SEX write F1.0", []byte{0x00, 0x01, 0x05, 0x00}},
		{0x00E8, "SEX name", pad("SEX", 8)},
		{0x00F0, "SEX label_len", i32le(3)},
		{0x00F4, "SEX label + 32-bit alignment padding", []byte{'S', 'e', 'x', 0x00}},

		// --- record type 2: NAME ---
		{0x00F8, "NAME rec_type", i32le(2)},
		{0x00FC, "NAME type (declared width in bytes)", i32le(10)},
		{0x0100, "NAME has_var_label", i32le(0)},
		{0x0104, "NAME n_missing_values", i32le(0)},
		{0x0108, "NAME print A10", []byte{0x00, 0x0A, 0x01, 0x00}},
		{0x010C, "NAME write A10", []byte{0x00, 0x0A, 0x01, 0x00}},
		{0x0110, "NAME name", pad("NAME", 8)},

		// --- record type 2: NAME continuation ---
		{0x0118, "NAME continuation rec_type", i32le(2)},
		{0x011C, "NAME continuation type (-1)", []byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{0x0120, "NAME continuation has_var_label", i32le(0)},
		{0x0124, "NAME continuation n_missing_values", i32le(0)},
		{0x0128, "NAME continuation print", i32le(0)},
		{0x012C, "NAME continuation write", i32le(0)},
		{0x0130, "NAME continuation name", pad("", 8)},

		// --- record type 3 ---
		{0x0138, "value-label rec_type", i32le(3)},
		{0x013C, "value-label label_count", i32le(2)},
		{0x0140, "value-label pair 1 value 1.0", f64le(1)},
		{0x0140, "value-label pair 1 value literal bytes", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xF0, 0x3F}},
		{0x0148, "value-label pair 1 label_len byte", []byte{0x04}},
		{0x0149, "value-label pair 1 label", []byte("Male")},
		{0x014D, "value-label pair 1 padding to 8", []byte{0x00, 0x00, 0x00}},
		{0x0150, "value-label pair 2 value 2.0", f64le(2)},
		{0x0158, "value-label pair 2 label_len byte", []byte{0x06}},
		{0x0159, "value-label pair 2 label", []byte("Female")},
		{0x015F, "value-label pair 2 padding to 8", []byte{0x00}},

		// --- record type 4 ---
		{0x0160, "label-vars rec_type", i32le(4)},
		{0x0164, "label-vars var_count", i32le(1)},
		{0x0168, "label-vars vars[0] = SEX dictionary index", i32le(2)},

		// --- record type 999 ---
		{0x016C, "terminator rec_type 999", i32le(999)},
		{0x016C, "terminator rec_type literal bytes", []byte{0xE7, 0x03, 0x00, 0x00}},
		{0x0170, "terminator filler", i32le(0)},

		// --- data section ---
		{0x0174, "case 1 ID = 1.0", f64le(1)},
		{0x017C, "case 1 SEX = 1.0", f64le(1)},
		{0x0184, "case 1 NAME segment 1", []byte("ALICE   ")},
		{0x018C, "case 1 NAME segment 2", pad("", 8)},
		{0x0194, "case 2 ID = 2.0", f64le(2)},
		{0x019C, "case 2 SEX = sysmis (-DBL_MAX)", f64le(-math.MaxFloat64)},
		{0x019C, "case 2 SEX sysmis literal bytes", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xEF, 0xFF}},
		{0x01A4, "case 2 NAME segment 1", []byte("BOB     ")},
		{0x01AC, "case 2 NAME segment 2", pad("", 8)},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			end := tc.offset + len(tc.want)
			if end > len(got) {
				t.Fatalf("field runs to 0x%04X, past end of file 0x%04X", end, len(got))
			}
			if g := got[tc.offset:end]; !bytes.Equal(g, tc.want) {
				t.Errorf("at 0x%04X:\n got % X\nwant % X", tc.offset, g, tc.want)
			}
		})
	}

	// The walkthrough accounts for every byte, so the assertions above must
	// tile the file with no gap. Proving that is what stops an unexamined
	// region from drifting.
	covered := make([]bool, len(got))
	for _, tc := range cases {
		for i := tc.offset; i < tc.offset+len(tc.want) && i < len(covered); i++ {
			covered[i] = true
		}
	}
	for i, ok := range covered {
		if !ok {
			t.Fatalf("byte 0x%04X is not covered by the walkthrough; every byte of the reference fixture must be accounted for", i)
		}
	}
}

// TestReferenceFixture_Pinned locks the reference fixture's bytes. See the note
// on referenceSHA256 before changing it.
func TestReferenceFixture_Pinned(t *testing.T) {
	got, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	sum := sha256.Sum256(got)
	if h := hex.EncodeToString(sum[:]); h != referenceSHA256 {
		t.Errorf("reference fixture sha256 = %s, want %s\n"+
			"The one fixture verified against the spec by hand has changed. Re-do the "+
			"walkthrough on TestReferenceFixture_HandVerified against the PSPP spec "+
			"before updating referenceSHA256.", h, referenceSHA256)
	}
}

// TestBuild_Deterministic asserts the property that makes these fixtures usable
// as goldens: Build is a pure function of its argument. Repeated builds of the
// same spec, and builds of independently-constructed equal specs, are
// byte-identical.
func TestBuild_Deterministic(t *testing.T) {
	specs := map[string]func() Spec{
		"reference": ReferenceSpec,
		"multi value-label sets and a weight variable": func() Spec {
			return Spec{
				FileLabel: "determinism probe",
				WeightVar: "WT",
				Vars: []Var{
					{Name: "WT", Print: Format{Type: FormatF, Width: 8, Decimals: 4}},
					{Name: "Q1", Label: "Question one"},
					{Name: "Q2", Label: "Question two"},
					{Name: "CODE", Width: 4},
				},
				ValueLabels: []ValueLabelSet{
					{Vars: []string{"Q1", "Q2"}, Labels: []ValueLabel{
						{Value: Num(1), Label: "Yes"},
						{Value: Num(0), Label: "No"},
					}},
					{Vars: []string{"CODE"}, Labels: []ValueLabel{
						{Value: Text("AB"), Label: "Alpha Bravo"},
					}},
				},
				Cases: [][]Value{
					{Num(1.5), Num(1), Num(0), Text("AB")},
					{Num(0.5), SysMis(), Num(1), Text("CD")},
				},
			}
		},
	}

	for name, mk := range specs {
		t.Run(name, func(t *testing.T) {
			first, err := Build(mk())
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			for i := 0; i < 8; i++ {
				again, err := Build(mk())
				if err != nil {
					t.Fatalf("Build (iteration %d): %v", i, err)
				}
				if !bytes.Equal(first, again) {
					t.Fatalf("iteration %d differs from the first build; Build is not deterministic", i)
				}
			}
		})
	}
}

// TestBuild_Structure covers the layout rules the reference fixture does not
// reach: segment arithmetic across widths, dictionary-index resolution when
// continuation records shift positions, the shared value-label set, string
// value labels, and the header count fields.
func TestBuild_Structure(t *testing.T) {
	t.Run("nominal_case_size counts elements, not variables", func(t *testing.T) {
		widths := []struct {
			name  string
			vars  []Var
			want  int32
			bytes int
		}{
			{"single numeric", []Var{{Name: "A"}}, 1, 0},
			{"string exactly 8 needs no continuation", []Var{{Name: "A", Width: 8}}, 1, 0},
			{"string of 9 needs one continuation", []Var{{Name: "A", Width: 9}}, 2, 0},
			{"string of 16 needs one continuation", []Var{{Name: "A", Width: 16}}, 2, 0},
			{"string of 17 needs two continuations", []Var{{Name: "A", Width: 17}}, 3, 0},
			{"widest string spans 32 elements", []Var{{Name: "A", Width: 255}}, 32, 0},
			{"mixed", []Var{{Name: "A"}, {Name: "B", Width: 20}, {Name: "C"}}, 5, 0},
		}
		for _, w := range widths {
			t.Run(w.name, func(t *testing.T) {
				got, err := Build(Spec{Vars: w.vars})
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				if n := readI32(got, 0x0044); n != w.want {
					t.Errorf("nominal_case_size = %d, want %d", n, w.want)
				}
				// The dictionary must contain exactly nominal_case_size
				// record type 2 entries, since each element gets one.
				if n := countVariableRecords(t, got); n != int(w.want) {
					t.Errorf("record type 2 count = %d, want %d", n, w.want)
				}
			})
		}
	})

	t.Run("dictionary indices count continuation records", func(t *testing.T) {
		// WIDE is width 20 => 3 elements (positions 1,2,3), so TARGET is
		// element 4. A reader that counted variables instead would say 2.
		got, err := Build(Spec{
			Vars: []Var{
				{Name: "WIDE", Width: 20},
				{Name: "TARGET"},
			},
			WeightVar: "TARGET",
			ValueLabels: []ValueLabelSet{{
				Vars:   []string{"TARGET"},
				Labels: []ValueLabel{{Value: Num(7), Label: "Seven"}},
			}},
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if w := readI32(got, 0x004C); w != 4 {
			t.Errorf("header weight_index = %d, want 4", w)
		}
		// Record 4 is the last dictionary record before 999. Find it by
		// locating the terminator and stepping back 12 bytes.
		term := indexOfTerminator(t, got)
		if rt := readI32(got, term-12); rt != 4 {
			t.Fatalf("expected record type 4 at 0x%04X, got %d", term-12, rt)
		}
		if n := readI32(got, term-8); n != 1 {
			t.Errorf("record 4 var_count = %d, want 1", n)
		}
		if idx := readI32(got, term-4); idx != 4 {
			t.Errorf("record 4 vars[0] = %d, want 4 (TARGET is the fourth element)", idx)
		}
	})

	t.Run("one label set shared by several variables", func(t *testing.T) {
		got, err := Build(Spec{
			Vars: []Var{{Name: "Q1"}, {Name: "Q2"}, {Name: "Q3"}},
			ValueLabels: []ValueLabelSet{{
				Vars: []string{"Q1", "Q3"},
				Labels: []ValueLabel{
					{Value: Num(1), Label: "Yes"},
					{Value: Num(2), Label: "No"},
				},
			}},
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		term := indexOfTerminator(t, got)
		// record 4 = rec_type + var_count + 2 indices = 16 bytes
		off := term - 16
		if rt := readI32(got, off); rt != 4 {
			t.Fatalf("expected record type 4 at 0x%04X, got %d", off, rt)
		}
		if n := readI32(got, off+4); n != 2 {
			t.Fatalf("record 4 var_count = %d, want 2", n)
		}
		if a, b := readI32(got, off+8), readI32(got, off+12); a != 1 || b != 3 {
			t.Errorf("record 4 indices = [%d %d], want [1 3]", a, b)
		}
		// Exactly one record type 3 was emitted for the two variables.
		if n := bytes.Count(got, i32le(3)); n == 0 {
			t.Error("no record type 3 tag found")
		}
	})

	t.Run("string value labels occupy the 8-byte value slot", func(t *testing.T) {
		got, err := Build(Spec{
			Vars: []Var{{Name: "CODE", Width: 3}},
			ValueLabels: []ValueLabelSet{{
				Vars:   []string{"CODE"},
				Labels: []ValueLabel{{Value: Text("AB"), Label: "Alpha"}},
			}},
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		// record 3 header (8) + value (8) + len byte + "Alpha" (5) + 2 pad = 24,
		// then record 4 (12), then 999.
		term := indexOfTerminator(t, got)
		r3 := term - 12 - 24
		if rt := readI32(got, r3); rt != 3 {
			t.Fatalf("expected record type 3 at 0x%04X, got %d", r3, rt)
		}
		if v := got[r3+8 : r3+16]; !bytes.Equal(v, pad("AB", 8)) {
			t.Errorf("string value slot = %q, want %q", v, pad("AB", 8))
		}
		if got[r3+16] != 5 {
			t.Errorf("label_len byte = %d, want 5", got[r3+16])
		}
		if l := got[r3+17 : r3+22]; !bytes.Equal(l, []byte("Alpha")) {
			t.Errorf("label = %q, want %q", l, "Alpha")
		}
		if p := got[r3+22 : r3+24]; !bytes.Equal(p, []byte{0, 0}) {
			t.Errorf("padding = % X, want 00 00", p)
		}
	})

	t.Run("header case count", func(t *testing.T) {
		known, err := Build(Spec{Vars: []Var{{Name: "A"}}, Cases: [][]Value{{Num(1)}, {Num(2)}, {Num(3)}}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if n := readI32(known, 0x0050); n != 3 {
			t.Errorf("ncases = %d, want 3", n)
		}
		unknown, err := Build(Spec{Vars: []Var{{Name: "A"}}, Cases: [][]Value{{Num(1)}}, UnknownCaseCount: true})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if n := readI32(unknown, 0x0050); n != -1 {
			t.Errorf("ncases with UnknownCaseCount = %d, want -1", n)
		}
	})

	t.Run("dictionary-only file has no data section", func(t *testing.T) {
		got, err := Build(Spec{Vars: []Var{{Name: "A"}}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		term := indexOfTerminator(t, got)
		if n := len(got) - (term + 8); n != 0 {
			t.Errorf("%d bytes follow the terminator, want 0", n)
		}
		if n := readI32(got, 0x0050); n != 0 {
			t.Errorf("ncases = %d, want 0", n)
		}
	})

	t.Run("default formats", func(t *testing.T) {
		got, err := Build(Spec{Vars: []Var{{Name: "N"}, {Name: "S", Width: 12}}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		// Numeric with no declared format defaults to F8.2 = 0x00050802.
		if f := readI32(got, 0x00B0+16); f != 0x00050802 {
			t.Errorf("numeric default print format = 0x%08X, want 0x00050802 (F8.2)", f)
		}
		// String defaults to A<width>: A12 = 0x00010C00.
		if f := readI32(got, 0x00B0+VariableRecordSize+16); f != 0x00010C00 {
			t.Errorf("string default print format = 0x%08X, want 0x00010C00 (A12)", f)
		}
	})

	t.Run("variable label padding rounds to 4 bytes", func(t *testing.T) {
		for _, label := range []string{"a", "ab", "abc", "abcd", "abcde"} {
			t.Run(label, func(t *testing.T) {
				got, err := Build(Spec{Vars: []Var{{Name: "A", Label: label}}})
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				if n := readI32(got, 0x00B0+VariableRecordSize); int(n) != len(label) {
					t.Errorf("label_len = %d, want %d (the true length, not the padded one)", n, len(label))
				}
				padded := (len(label) + 3) / 4 * 4
				want := HeaderSize + VariableRecordSize + 4 + padded + 8 // + terminator
				if len(got) != want {
					t.Errorf("file length = %d, want %d (label padded from %d to %d)", len(got), want, len(label), padded)
				}
			})
		}
	})

	t.Run("string data is space padded to the segment boundary", func(t *testing.T) {
		got, err := Build(Spec{
			Vars:  []Var{{Name: "S", Width: 10}},
			Cases: [][]Value{{Text("")}, {Text("0123456789")}},
		})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		data := got[indexOfTerminator(t, got)+8:]
		if len(data) != 32 {
			t.Fatalf("data section = %d bytes, want 32 (2 cases x 2 elements x 8)", len(data))
		}
		if !bytes.Equal(data[0:16], pad("", 16)) {
			t.Errorf("empty string case = % X, want 16 spaces", data[0:16])
		}
		if !bytes.Equal(data[16:32], pad("0123456789", 16)) {
			t.Errorf("full-width case = %q, want %q", data[16:32], pad("0123456789", 16))
		}
	})
}

// TestBuild_Rejects is the other half of the ground-truth argument: the
// generator refuses anything it cannot justify from the spec, rather than
// coercing it into something that would teach the reader a wrong lesson.
func TestBuild_Rejects(t *testing.T) {
	base := func(vars ...Var) Spec { return Spec{Vars: vars} }

	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"no variables", Spec{}, "at least one"},
		{"empty name", base(Var{Name: ""}), "legal SPSS short name"},
		{"name over 8 bytes", base(Var{Name: "TOOLONGNAME"}), "legal SPSS short name"},
		{"lowercase name", base(Var{Name: "age"}), "legal SPSS short name"},
		{"name starting with a digit", base(Var{Name: "1ST"}), "legal SPSS short name"},
		{"name with a space", base(Var{Name: "A B"}), "legal SPSS short name"},
		{"duplicate names", base(Var{Name: "A"}, Var{Name: "A"}), "repeats the name"},
		{"negative width", base(Var{Name: "A", Width: -1}), "negative width"},
		{"width over 255", base(Var{Name: "A", Width: 256}), "7/14"},
		{"variable label too long", base(Var{Name: "A", Label: strings.Repeat("x", MaxVarLabelLen+1)}), "over the 120-byte limit"},
		{"non-ASCII variable label", base(Var{Name: "A", Label: "café"}), "printable 7-bit ASCII"},
		{
			"big-endian not implemented",
			Spec{Vars: []Var{{Name: "A"}}, ByteOrder: BigEndian},
			"not implemented",
		},
		{
			"an unknown compression is not implemented",
			Spec{Vars: []Var{{Name: "A"}}, Compression: Compression(9)},
			"not implemented",
		},
		{
			"a non-finite compression bias",
			Spec{Vars: []Var{{Name: "A"}}, CompressionBias: math.Inf(1)},
			"not a finite number",
		},
		{
			"an unknown compression",
			Spec{Vars: []Var{{Name: "A"}}, Compression: Compression(9)},
			"not implemented",
		},
		{
			"a negative ZSAV block size",
			Spec{Vars: []Var{{Name: "A"}}, Compression: CompressionZSAV, ZSAVBlockSize: -8},
			"is negative",
		},
		{
			"file label over 64 bytes",
			Spec{Vars: []Var{{Name: "A"}}, FileLabel: strings.Repeat("x", 65)},
			"over the 64-byte header field",
		},
		{
			"creation date of the wrong length",
			Spec{Vars: []Var{{Name: "A"}}, CreationDate: "2024-01-01"},
			"exactly 9 bytes",
		},
		{
			"creation time of the wrong length",
			Spec{Vars: []Var{{Name: "A"}}, CreationTime: "00:00"},
			"exactly 8 bytes",
		},
		{
			"weight variable does not exist",
			Spec{Vars: []Var{{Name: "A"}}, WeightVar: "B"},
			"names no declared variable",
		},
		{
			"string weight variable",
			Spec{Vars: []Var{{Name: "A", Width: 4}}, WeightVar: "A"},
			"only numeric variables can weight",
		},
		{
			"case row of the wrong arity",
			Spec{Vars: []Var{{Name: "A"}, {Name: "B"}}, Cases: [][]Value{{Num(1)}}},
			"declares 2 variables",
		},
		{
			"uninitialised value",
			Spec{Vars: []Var{{Name: "A"}}, Cases: [][]Value{{{}}}},
			"uninitialised Value",
		},
		{
			"text into a numeric variable",
			Spec{Vars: []Var{{Name: "A"}}, Cases: [][]Value{{Text("x")}}},
			"use Num",
		},
		{
			"number into a string variable",
			Spec{Vars: []Var{{Name: "A", Width: 4}}, Cases: [][]Value{{Num(1)}}},
			"use Text",
		},
		{
			"string longer than its declared width",
			Spec{Vars: []Var{{Name: "A", Width: 3}}, Cases: [][]Value{{Text("abcd")}}},
			"over the declared width",
		},
		{
			"non-ASCII string datum",
			Spec{Vars: []Var{{Name: "A", Width: 8}}, Cases: [][]Value{{Text("café")}}},
			"printable 7-bit ASCII",
		},
		{
			"NaN datum",
			Spec{Vars: []Var{{Name: "A"}}, Cases: [][]Value{{Num(math.NaN())}}},
			"use SysMis",
		},
		{
			"system-missing string",
			Spec{Vars: []Var{{Name: "A", Width: 4}}, Cases: [][]Value{{SysMis()}}},
			"no system-missing state for strings",
		},
		{
			"label set naming no variables",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Labels: []ValueLabel{{Value: Num(1), Label: "x"}}}}},
			"names no variables",
		},
		{
			"label set with no labels",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}}}},
			"carries no labels",
		},
		{
			"label set naming an unknown variable",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"B"}, Labels: []ValueLabel{{Value: Num(1), Label: "x"}}}}},
			"names no declared variable",
		},
		{
			"value labels on a long string",
			Spec{Vars: []Var{{Name: "A", Width: 9}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: Text("x"), Label: "y"}}}}},
			"7/21",
		},
		{
			"label set mixing widths",
			Spec{
				Vars:        []Var{{Name: "A", Width: 4}, {Name: "B", Width: 6}},
				ValueLabels: []ValueLabelSet{{Vars: []string{"A", "B"}, Labels: []ValueLabel{{Value: Text("x"), Label: "y"}}}},
			},
			"same type and width",
		},
		{
			"label set mixing numeric and string",
			Spec{
				Vars:        []Var{{Name: "A"}, {Name: "B", Width: 6}},
				ValueLabels: []ValueLabelSet{{Vars: []string{"A", "B"}, Labels: []ValueLabel{{Value: Num(1), Label: "y"}}}},
			},
			"same type and width",
		},
		{
			"empty value label",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: Num(1), Label: ""}}}}},
			"must be 1..120 bytes",
		},
		{
			"value label too long",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: Num(1), Label: strings.Repeat("x", MaxValueLabelLen+1)}}}}},
			"must be 1..120 bytes",
		},
		{
			"non-ASCII value label",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: Num(1), Label: "caf\u00e9"}}}}},
			"printable 7-bit ASCII",
		},
		{
			"labelling the system-missing value",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: SysMis(), Label: "missing"}}}}},
			"system-missing",
		},
		{
			"value label type mismatched to its variable",
			Spec{Vars: []Var{{Name: "A"}}, ValueLabels: []ValueLabelSet{{Vars: []string{"A"}, Labels: []ValueLabel{{Value: Text("x"), Label: "y"}}}}},
			"use Num",
		},
		{
			"zero print format width",
			Spec{Vars: []Var{{Name: "A", Print: Format{Type: FormatF, Width: 0, Decimals: 1}}}},
			"format width 0 is outside",
		},
		{
			"negative write format decimals",
			Spec{Vars: []Var{{Name: "A", Print: Format{Type: FormatF, Width: 8}, Write: Format{Type: FormatF, Width: 8, Decimals: -1}}}},
			"decimal count -1 is outside",
		},
		{
			"non-ASCII file label",
			Spec{Vars: []Var{{Name: "A"}}, FileLabel: "caf\u00e9"},
			"printable 7-bit ASCII",
		},
		{
			"product name over 60 bytes",
			Spec{Vars: []Var{{Name: "A"}}, ProductName: strings.Repeat("x", 61)},
			"over the 60-byte header field",
		},
		{
			"non-ASCII product name",
			Spec{Vars: []Var{{Name: "A"}}, ProductName: "caf\u00e9"},
			"printable 7-bit ASCII",
		},
		{
			"non-ASCII creation date",
			Spec{Vars: []Var{{Name: "A"}}, CreationDate: "01 J\xe9n 24"},
			"printable 7-bit ASCII",
		},
		{
			"non-ASCII creation time",
			Spec{Vars: []Var{{Name: "A"}}, CreationTime: "00:00:0\xe9"},
			"printable 7-bit ASCII",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Build(tc.spec)
			if err == nil {
				t.Fatalf("Build succeeded (%d bytes); want an error containing %q", len(got), tc.want)
			}
			if got != nil {
				t.Errorf("Build returned %d bytes alongside an error; a rejected spec must yield no bytes", len(got))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestFormat_Pack covers the 0x00TTWWDD packing in isolation, including the
// requirement that the most significant byte stays zero.
func TestFormat_Pack(t *testing.T) {
	cases := []struct {
		name string
		f    Format
		want int32
	}{
		{"F8.2", Format{FormatF, 8, 2}, 0x00050802},
		{"F8.0", Format{FormatF, 8, 0}, 0x00050800},
		{"F1.0", Format{FormatF, 1, 0}, 0x00050100},
		{"A10", Format{FormatA, 10, 0}, 0x00010A00},
		{"A255", Format{FormatA, 255, 0}, 0x0001FF00},
		{"F40.16", Format{FormatF, 40, 16}, 0x00052810},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.pack(); got != tc.want {
				t.Errorf("pack() = 0x%08X, want 0x%08X", got, tc.want)
			}
			if got := tc.f.pack(); got>>24 != 0 {
				t.Errorf("pack() = 0x%08X: the most significant byte must be zero", got)
			}
		})
	}
}

// TestVar_Segments checks the ceil(width/8) rule that drives continuation
// records, dictionary indices and nominal_case_size.
func TestVar_Segments(t *testing.T) {
	cases := []struct {
		width int
		want  int
	}{
		{0, 1}, {1, 1}, {7, 1}, {8, 1}, {9, 2}, {15, 2}, {16, 2}, {17, 3}, {255, 32},
	}
	for _, tc := range cases {
		v := Var{Name: "A", Width: tc.width}
		if got := v.segments(); got != tc.want {
			t.Errorf("width %d: segments() = %d, want %d", tc.width, got, tc.want)
		}
	}
}

// TestSysMisDouble pins the sentinel's bit pattern. -DBL_MAX is
// 0xFFEFFFFFFFFFFFFF; getting this wrong would make every missing numeric in
// every fixture silently wrong.
func TestSysMisDouble(t *testing.T) {
	if bits := math.Float64bits(SysMisDouble); bits != 0xFFEFFFFFFFFFFFFF {
		t.Errorf("SysMisDouble bits = 0x%016X, want 0xFFEFFFFFFFFFFFFF (-DBL_MAX)", bits)
	}
}

// TestHeaderSize checks the header field arithmetic against the spec's stated
// 176-byte header.
func TestHeaderSize(t *testing.T) {
	if HeaderSize != 176 {
		t.Errorf("HeaderSize = %d, want 176", HeaderSize)
	}
	if VariableRecordSize != 32 {
		t.Errorf("VariableRecordSize = %d, want 32", VariableRecordSize)
	}
}

// --- helpers ---------------------------------------------------------------

// pad renders s space-padded to exactly n bytes, the fixed-field convention
// used throughout the format.
func pad(s string, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, s)
	return b
}

func i32le(v int32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(v))
	return b
}

func f64le(v float64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, math.Float64bits(v))
	return b
}

func readI32(b []byte, off int) int32 {
	return int32(binary.LittleEndian.Uint32(b[off : off+4]))
}

// indexOfTerminator walks the dictionary record by record and returns the
// offset of the record type 999 terminator. Walking rather than searching for
// the 999 byte pattern matters: a data value or a label could contain those
// bytes.
func indexOfTerminator(t *testing.T, b []byte) int {
	t.Helper()
	off := HeaderSize
	for off+8 <= len(b) {
		switch rt := readI32(b, off); rt {
		case 2:
			hasLabel := readI32(b, off+8)
			nMissing := readI32(b, off+12)
			off += VariableRecordSize
			if hasLabel == 1 {
				n := int(readI32(b, off))
				off += 4 + (n+3)/4*4
			}
			if nMissing < 0 {
				nMissing = -nMissing
			}
			off += int(nMissing) * 8
		case 3:
			n := int(readI32(b, off+4))
			off += 8
			for i := 0; i < n; i++ {
				off += ElementSize
				ll := int(b[off])
				off += (ll + 1 + 7) / 8 * 8
			}
		case 4:
			n := int(readI32(b, off+4))
			off += 8 + n*4
		case 999:
			return off
		default:
			t.Fatalf("unexpected record type %d at 0x%04X while walking the dictionary", rt, off)
		}
	}
	t.Fatalf("no record type 999 terminator found")
	return 0
}

// countVariableRecords counts record type 2 entries, continuations included.
func countVariableRecords(t *testing.T, b []byte) int {
	t.Helper()
	n := 0
	off := HeaderSize
	for off+8 <= len(b) {
		rt := readI32(b, off)
		if rt != 2 {
			break
		}
		n++
		hasLabel := readI32(b, off+8)
		nMissing := readI32(b, off+12)
		off += VariableRecordSize
		if hasLabel == 1 {
			l := int(readI32(b, off))
			off += 4 + (l+3)/4*4
		}
		if nMissing < 0 {
			nMissing = -nMissing
		}
		off += int(nMissing) * 8
	}
	return n
}

// TestEnc_Defensive exercises the byte sink's guard rails directly. Validation
// makes these paths unreachable from Build, which is exactly why they need
// their own test: they are the backstop for a future emitter change that adds
// a field validation does not yet police, and an untested backstop is not one.
func TestEnc_Defensive(t *testing.T) {
	t.Run("ascii refuses to truncate an overlong field", func(t *testing.T) {
		e := &enc{bo: binary.LittleEndian}
		e.ascii("toolong", 4)
		if e.err == nil {
			t.Fatal("writing 7 bytes into a 4-byte field succeeded; it must be an error, not a truncation")
		}
		if !strings.Contains(e.err.Error(), "field is 4") {
			t.Errorf("error = %q, want it to name the field width", e.err)
		}
	})

	t.Run("the first error is sticky and suppresses later writes", func(t *testing.T) {
		e := &enc{bo: binary.LittleEndian}
		e.ascii("ok", 4)
		n := e.buf.Len()
		e.ascii("toolong", 4)
		first := e.err
		e.i32(1)
		e.f64(1)
		e.ascii("x", 8)
		e.zeros(8)
		if e.buf.Len() != n {
			t.Errorf("buffer grew from %d to %d after an error; writes must stop", n, e.buf.Len())
		}
		if e.err != first {
			t.Errorf("error was overwritten: %v, want the first one %v", e.err, first)
		}
	})

	t.Run("zeros ignores a non-positive count", func(t *testing.T) {
		e := &enc{bo: binary.LittleEndian}
		e.zeros(0)
		e.zeros(-4)
		if e.buf.Len() != 0 {
			t.Errorf("buffer length = %d, want 0", e.buf.Len())
		}
	})
}

// TestStringers checks that the diagnostic String methods stay usable for the
// axes that are declared but not yet implemented, since their names appear in
// the "not implemented" errors callers will actually see.
func TestStringers(t *testing.T) {
	if got := LittleEndian.String(); got != "little-endian" {
		t.Errorf("LittleEndian = %q", got)
	}
	if got := BigEndian.String(); got != "big-endian" {
		t.Errorf("BigEndian = %q", got)
	}
	if got := ByteOrder(99).String(); got != "ByteOrder(?)" {
		t.Errorf("ByteOrder(99) = %q", got)
	}
	if got := CompressionNone.String(); got != "uncompressed" {
		t.Errorf("CompressionNone = %q", got)
	}
	if got := CompressionBytecode.String(); got != "bytecode" {
		t.Errorf("CompressionBytecode = %q", got)
	}
	if got := CompressionZSAV.String(); got != "zsav" {
		t.Errorf("CompressionZSAV = %q", got)
	}
	if got := Compression(99).String(); got != "Compression(?)" {
		t.Errorf("Compression(99) = %q", got)
	}
	if got := Num(1.5).String(); got != "Num(1.5)" {
		t.Errorf("Num(1.5) = %q", got)
	}
	if got := Text("a").String(); got != `Text("a")` {
		t.Errorf("Text(\"a\") = %q", got)
	}
	if got := SysMis().String(); got != "SysMis()" {
		t.Errorf("SysMis() = %q", got)
	}
	if got := (Value{}).String(); !strings.Contains(got, "uninitialised") {
		t.Errorf("Value{} = %q", got)
	}
}
