package spsstest

import (
	"bytes"
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

// dataSection returns the bytes after the record type 999 dictionary
// terminator, found by an independent scan rather than by asking the emitter
// where it put things.
//
// The terminator is 8 bytes — the tag then a filler int32 — and it is the
// only record type 999 in a file, so searching for its tag from the end of
// the header is unambiguous enough for a fixture whose dictionary the tests
// around this one already verify byte by byte.
func dataSection(t *testing.T, raw []byte) []byte {
	t.Helper()
	var tag [4]byte
	binary.LittleEndian.PutUint32(tag[:], uint32(recTypeTerminator))
	i := bytes.LastIndex(raw, tag[:])
	if i < 0 {
		t.Fatalf("no record type 999 terminator in a %d-byte fixture", len(raw))
	}
	return raw[i+8:]
}

// TestBytecode_HandVerifiedStream walks the compressed data section of the
// reference fixture byte by byte against the specification, the same way
// TestReferenceFixture_HandVerified walks its dictionary. It is the ground
// truth for the compressed encoding: everything else in this file and every
// reader test that consumes a compressed fixture rests on these 32 bytes
// being right.
//
// ReferenceSpec() is four elements per case:
//
//	element 1  ID    numeric
//	element 2  SEX   numeric
//	element 3  NAME  string, first 8-byte segment
//	element 4  NAME  string, second 8-byte segment (width 10 → 2 segments)
//
//	case 1: ID=1, SEX=1,      NAME="ALICE"
//	case 2: ID=2, SEX=sysmis, NAME="BOB"
//
// The encoding is blocks of eight command bytes, each block followed
// immediately by the eight-byte payloads that the commands in THAT block
// asked for, in command order. Element by element:
//
//	ID=1        whole number; 1 + bias 100 = 101, inside [1,251]  → command 101 (0x65)
//	SEX=1       likewise                                          → command 101 (0x65)
//	NAME seg 1  "ALICE" padded to 16 bytes gives "ALICE   " here;
//	            not all spaces, so it takes the escape             → command 253 + payload
//	NAME seg 2  the remaining 8 bytes are all spaces               → command 254
//	ID=2        2 + 100 = 102                                      → command 102 (0x66)
//	SEX=sysmis  the system-missing sentinel                        → command 255
//	NAME seg 1  "BOB     "                                         → command 253 + payload
//	NAME seg 2  all spaces                                         → command 254
//
// That is exactly eight commands, so they fill one block, and their two
// payloads follow it. Then the stream ends:
//
//	command 252  end of file
//	commands 0×7 padding, because a block is always eight commands
//
//	0x00  8  commands   65 65 FD FE 66 FF FD FE
//	0x08  8  payload    "ALICE   "   (41 4C 49 43 45 20 20 20)
//	0x10  8  payload    "BOB     "   (42 4F 42 20 20 20 20 20)
//	0x18  8  commands   FC 00 00 00 00 00 00 00
//	0x20     end. 32 bytes, against 64 for the same cases uncompressed.
func TestBytecode_HandVerifiedStream(t *testing.T) {
	spec := ReferenceSpec()
	spec.Compression = CompressionBytecode

	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := dataSection(t, raw)

	want := []byte{
		// block 1: the eight commands of both cases
		0x65, 0x65, 0xFD, 0xFE, 0x66, 0xFF, 0xFD, 0xFE,
		// its two payloads, in command order
		'A', 'L', 'I', 'C', 'E', ' ', ' ', ' ',
		'B', 'O', 'B', ' ', ' ', ' ', ' ', ' ',
		// block 2: end of file, then the padding that fills the block
		0xFC, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("compressed data section =\n% X\nwant\n% X", got, want)
	}
	if len(got) != 32 {
		t.Errorf("compressed data section is %d bytes, want 32", len(got))
	}
}

// TestBytecode_DiffersFromUncompressedOnTheWire is the property that makes a
// compressed fixture worth anything as a test input. Two fixtures carrying
// the same logical cases must reach a reader as genuinely different bytes,
// or a decoder test proves only that the reader can read what it already
// could.
func TestBytecode_DiffersFromUncompressedOnTheWire(t *testing.T) {
	plain, err := Build(ReferenceSpec())
	if err != nil {
		t.Fatalf("Build(uncompressed): %v", err)
	}
	spec := ReferenceSpec()
	spec.Compression = CompressionBytecode
	packed, err := Build(spec)
	if err != nil {
		t.Fatalf("Build(bytecode): %v", err)
	}

	if bytes.Equal(dataSection(t, plain), dataSection(t, packed)) {
		t.Fatal("the compressed and uncompressed data sections are byte-identical; the compressed fixture is not exercising the decoder")
	}
	if len(packed) >= len(plain) {
		t.Errorf("the compressed fixture is %d bytes and the uncompressed one %d; compression did not shrink the reference fixture", len(packed), len(plain))
	}

	// The dictionary is unchanged apart from the compression flag: the
	// encoding of the data section is not a dictionary concern, and a
	// reader must parse both dictionaries identically.
	term := len(plain) - len(dataSection(t, plain))
	a := append([]byte(nil), plain[:term]...)
	b := append([]byte(nil), packed[:len(packed)-len(dataSection(t, packed))]...)
	if len(a) != len(b) {
		t.Fatalf("dictionary lengths differ: %d vs %d", len(a), len(b))
	}
	binary.LittleEndian.PutUint32(b[offCompressionField:offCompressionField+4], 0)
	if !bytes.Equal(a, b) {
		t.Error("the two fixtures differ somewhere other than the compression flag and the data section")
	}
}

// offCompressionField is the byte offset of the header's compression field:
// 4 (rec_type) + 60 (prod_name) + 4 (layout_code) + 4 (nominal_case_size).
const offCompressionField = 72

// TestBytecode_Deterministic holds the package's central promise across the
// new code path. A fixture that is not byte-stable cannot be hashed, cannot
// be diffed and cannot anchor a regression.
func TestBytecode_Deterministic(t *testing.T) {
	spec := ReferenceSpec()
	spec.Compression = CompressionBytecode
	first, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for i := 0; i < 4; i++ {
		again, err := Build(spec)
		if err != nil {
			t.Fatalf("Build (repeat %d): %v", i, err)
		}
		if !bytes.Equal(first, again) {
			t.Fatalf("repeat %d differs from the first build", i)
		}
	}
}

// TestBytecode_EveryCommandByte builds a fixture whose data section is
// guaranteed to contain each of the six commands at least once, and asserts
// that it does. It is the fixture the reader's own command-coverage test
// consumes, so this is where the claim that it covers everything is checked.
func TestBytecode_EveryCommandByte(t *testing.T) {
	spec := everyCommandSpec()
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := dataSection(t, raw)

	for _, want := range []struct {
		cmd  byte
		what string
	}{
		{cmdPad, "padding"},
		{cmdEOF, "end of file"},
		{cmdRaw, "a verbatim value"},
		{cmdSpaces, "an all-spaces segment"},
		{cmdSysMis, "system-missing"},
		{101, "an integer literal"},
	} {
		if !bytes.Contains(data, []byte{want.cmd}) {
			t.Errorf("command %d (%s) does not appear in the data section: % X", want.cmd, want.what, data)
		}
	}
}

// everyCommandSpec is the fixture that exercises every command byte the
// encoding defines. Each datum is chosen for the command it forces:
//
//   - N: 0 and 151 sit at the two ends of the compressible range under the
//     conventional bias, and -99 at the other; each takes one command byte.
//   - BIG and FRAC leave that range — one by magnitude, one by not being a
//     whole number — so both take the escape. BIG stays clear of ±DBL_MAX,
//     which SPSS reserves as its sysmis / "highest" sentinels.
//   - MISS is system-missing.
//   - S carries a value in its first segment and nothing in its second, so
//     one segment escapes and the other is the all-spaces command.
//   - The case count is chosen so the final block needs padding.
func everyCommandSpec() Spec {
	return Spec{
		Compression: CompressionBytecode,
		Vars: []Var{
			{Name: "N", Print: Format{Type: FormatF, Width: 8}},
			{Name: "BIG", Print: Format{Type: FormatF, Width: 8}},
			{Name: "FRAC", Print: Format{Type: FormatF, Width: 8, Decimals: 2}},
			{Name: "MISS", Print: Format{Type: FormatF, Width: 8}},
			{Name: "S", Width: 12},
		},
		Cases: [][]Value{
			{Num(1), Num(1e9), Num(2.5), SysMis(), Text("ALPHA")},
			{Num(0), Num(-1e9), Num(-0.25), SysMis(), Text("")},
			{Num(151), Num(1e300), Num(1.5), SysMis(), Text("BETA")},
			{Num(-99), Num(-252), Num(0.1), SysMis(), Text("GAMMA BRAVO")},
		},
	}
}

// TestBytecode_HeaderCarriesTheDeclaredBias covers the axis a bias-100
// fixture cannot: the header field a reader is required to read rather than
// assume.
func TestBytecode_HeaderCarriesTheDeclaredBias(t *testing.T) {
	for _, bias := range []float64{0, 100, 1, 200, -50} {
		spec := ReferenceSpec()
		spec.Compression = CompressionBytecode
		spec.CompressionBias = bias

		raw, err := Build(spec)
		if err != nil {
			t.Fatalf("bias %v: Build: %v", bias, err)
		}
		want := bias
		if bias == 0 {
			want = CompressionBias // the zero value means "the conventional 100"
		}
		got := math.Float64frombits(binary.LittleEndian.Uint64(raw[offBiasField:]))
		if got != want {
			t.Errorf("bias %v: header bias field = %v, want %v", bias, got, want)
		}
	}
}

// offBiasField is the byte offset of the header's flt64 bias field: the
// compression field's offset plus weight_index (4) and ncases (4).
const offBiasField = offCompressionField + 4 + 4 + 4

// TestBytecode_BiasChangesTheCommandBytes proves the bias is arithmetic and
// not decoration. The same value under two biases must reach the wire as two
// different command bytes, or a reader could hardcode 100 and pass.
func TestBytecode_BiasChangesTheCommandBytes(t *testing.T) {
	build := func(bias float64) []byte {
		t.Helper()
		spec := Spec{
			Compression:     CompressionBytecode,
			CompressionBias: bias,
			Vars:            []Var{{Name: "N", Print: Format{Type: FormatF, Width: 8}}},
			Cases:           [][]Value{{Num(7)}},
		}
		raw, err := Build(spec)
		if err != nil {
			t.Fatalf("bias %v: Build: %v", bias, err)
		}
		return dataSection(t, raw)
	}

	at100 := build(100)
	at50 := build(50)
	if at100[0] != 107 {
		t.Errorf("under bias 100 the value 7 encoded as command %d, want 107", at100[0])
	}
	if at50[0] != 57 {
		t.Errorf("under bias 50 the value 7 encoded as command %d, want 57", at50[0])
	}
}

// TestBytecode_OutOfRangeValuesEscape checks the boundary of the compressible
// range from both sides, which is where an off-by-one between a writer and a
// reader would hide. Under the conventional bias the range is exactly
// [-99, 151]: one past either end must take the escape.
func TestBytecode_OutOfRangeValuesEscape(t *testing.T) {
	cases := []struct {
		value float64
		want  byte
	}{
		{-100, cmdRaw},
		{-99, 1},
		{0, 100},
		{151, 251},
		{152, cmdRaw},
		{0.5, cmdRaw},
		{math.Inf(1), cmdRaw},
	}
	for _, tc := range cases {
		spec := Spec{
			Compression: CompressionBytecode,
			Vars:        []Var{{Name: "N", Print: Format{Type: FormatF, Width: 8}}},
			Cases:       [][]Value{{Num(tc.value)}},
		}
		raw, err := Build(spec)
		if err != nil {
			t.Fatalf("%v: Build: %v", tc.value, err)
		}
		if got := dataSection(t, raw)[0]; got != tc.want {
			t.Errorf("%v encoded as command %d, want %d", tc.value, got, tc.want)
		}
	}
}

// TestBytecode_EmptyDataSection covers the dictionary-only file. The stream
// still has to be a well-formed one: an end-of-file command, padded out to a
// whole block.
func TestBytecode_EmptyDataSection(t *testing.T) {
	spec := Spec{
		Compression: CompressionBytecode,
		Vars:        []Var{{Name: "N", Print: Format{Type: FormatF, Width: 8}}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := dataSection(t, raw)
	want := []byte{cmdEOF, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Errorf("data section = % X, want % X", got, want)
	}
}

// TestBytecode_LongStringSegments covers a string wide enough to need three
// segments, where the middle segment carries text and the last is padding.
// The per-SEGMENT decision is what a reader has to mirror; a per-VALUE one
// would emit the wrong number of elements and desynchronise every case after
// the first.
func TestBytecode_LongStringSegments(t *testing.T) {
	spec := Spec{
		Compression: CompressionBytecode,
		Vars:        []Var{{Name: "S", Width: 20}},
		Cases:       [][]Value{{Text(strings.Repeat("X", 17))}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := dataSection(t, raw)
	// Three segments: two full of X, one holding a single X then padding.
	// So three escapes, then end of file, then the block padding.
	want := []byte{cmdRaw, cmdRaw, cmdRaw, cmdEOF, 0, 0, 0, 0}
	if !bytes.Equal(data[:8], want) {
		t.Errorf("commands = % X, want % X", data[:8], want)
	}
	if n := len(data); n != 8+3*ElementSize {
		t.Errorf("data section is %d bytes, want %d (one command block plus three payloads)", n, 8+3*ElementSize)
	}
}

// TestBytecode_AllSpacesStringUsesTheSpacesCommand pins the one command with
// no numeric analogue. An empty Text value is SPSS's conventional missing
// string, so this is the common case rather than an edge one.
func TestBytecode_AllSpacesStringUsesTheSpacesCommand(t *testing.T) {
	spec := Spec{
		Compression: CompressionBytecode,
		Vars:        []Var{{Name: "S", Width: 8}},
		Cases:       [][]Value{{Text("")}, {Text("   ")}},
	}
	raw, err := Build(spec)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data := dataSection(t, raw)
	if data[0] != cmdSpaces || data[1] != cmdSpaces {
		t.Errorf("commands = % X, want both segments to be the all-spaces command %X", data[:2], cmdSpaces)
	}
}

// TestNumberCommand_RoundTrips is the emitter's half of the mirror property.
// Every command byte it produces must decode, by the spec's own arithmetic,
// back to the value that produced it — under an unconventional bias as well
// as the usual one, because that is where a rounding disagreement would show.
func TestNumberCommand_RoundTrips(t *testing.T) {
	for _, bias := range []float64{100, 1, 251, -20, 1000} {
		for v := -400.0; v <= 400.0; v++ {
			cmd, ok := numberCommand(v, bias)
			if !ok {
				continue
			}
			if cmd < cmdIntMin || cmd > cmdIntMax {
				t.Fatalf("bias %v, value %v: command %d is outside [%d,%d]", bias, v, cmd, cmdIntMin, cmdIntMax)
			}
			if back := float64(cmd) - bias; back != v {
				t.Fatalf("bias %v: value %v encoded as command %d, which decodes to %v", bias, v, cmd, back)
			}
		}
	}
}

// TestNumberCommand_RefusesAFractionalBias covers the trap a shared table
// exists to close. Under a bias of 100.5 the whole number 0 would round into
// command 100, which decodes to -0.5 — a silent corruption that a round trip
// through one implementation would never reveal, because both halves would
// round the same way. Refusing to compress is the only safe answer.
func TestNumberCommand_RefusesAFractionalBias(t *testing.T) {
	for _, v := range []float64{-99, 0, 1, 151} {
		if cmd, ok := numberCommand(v, 100.5); ok {
			t.Errorf("value %v compressed to command %d under a fractional bias; it must take the escape instead", v, cmd)
		}
	}
}
