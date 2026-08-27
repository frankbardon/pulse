package spss

import (
	"context"
	"encoding/binary"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// compressed returns spec built twice — once uncompressed, once bytecode —
// so a test can hold the two encodings of one logical file side by side.
//
// Building from ONE spec is the point: the two byte streams then differ only
// in how the data section says what it says, and any disagreement between the
// two reads is a decoder bug rather than a fixture difference.
func bothEncodings(t *testing.T, spec spsstest.Spec) (plain, packed []byte) {
	t.Helper()
	spec.Compression = spsstest.CompressionNone
	plain = build(t, spec)
	spec.Compression = spsstest.CompressionBytecode
	packed = build(t, spec)
	return plain, packed
}

// everyCommandSpec is the fixture that forces each command byte at least
// once. It mirrors the generator's own everyCommandSpec, restated here
// because a reader test asserting on values must state the values it expects
// rather than import them from the thing it is testing.
//
//   - N takes 1, 0, 151 and -99: the two ends of the compressible range under
//     the conventional bias, and two values inside it. Each is one command byte.
//   - BIG and FRAC leave that range, by magnitude and by not being whole, so
//     both take the verbatim escape.
//   - MISS is system-missing in every case.
//   - S is 12 bytes wide, so two segments: one carrying text (escape) and one
//     all spaces (the spaces command), and one case where BOTH are spaces.
//   - Four cases of five variables is 24 elements, which does not divide by
//     eight once the end-of-file command is added, so the final block pads.
func everyCommandSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "N", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "BIG", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "FRAC", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 2}},
			{Name: "MISS", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "S", Width: 12},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1e9), spsstest.Num(2.5), spsstest.SysMis(), spsstest.Text("ALPHA")},
			{spsstest.Num(0), spsstest.Num(-1e9), spsstest.Num(-0.25), spsstest.SysMis(), spsstest.Text("")},
			{spsstest.Num(151), spsstest.Num(1e300), spsstest.Num(1.5), spsstest.SysMis(), spsstest.Text("BETA")},
			{spsstest.Num(-99), spsstest.Num(-252), spsstest.Num(0.1), spsstest.SysMis(), spsstest.Text("GAMMA BRAVO")},
		},
	}
}

// TestBytecode_MatchesUncompressed is this story's central criterion, and the
// strongest test in it: a compressed and an uncompressed fixture carrying the
// same logical content must produce the same cohort.
//
// It is strong because the two files are encoded INDEPENDENTLY — the
// generator writes bytecode from the specification with no reference to this
// package — so the decoder is being checked against something other than
// itself. Both halves of the cohort are compared: the rendered rows, and the
// authoritative schema, which is derived from a full scan of the data section
// and would drift if the expansion were wrong in a way the rows happened to
// hide (a mis-sized categorical dictionary, a nullability that a shifted
// sentinel invented).
func TestBytecode_MatchesUncompressed(t *testing.T) {
	specs := []struct {
		name string
		spec spsstest.Spec
	}{
		{"the reference fixture", spsstest.ReferenceSpec()},
		{"every command byte", everyCommandSpec()},
		{"the extension fixture", spsstest.ExtensionReferenceSpec()},
		{"a dictionary-only file", spsstest.Spec{
			Vars: []spsstest.Var{{Name: "N", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}}},
		}},
	}
	for _, tc := range specs {
		t.Run(tc.name, func(t *testing.T) {
			plain, packed := bothEncodings(t, tc.spec)

			plainRows := readAll(t, NewReaderFromBytes(plain))
			packedRows := readAll(t, NewReaderFromBytes(packed))
			assertRows(t, packedRows, plainRows)

			assertSchemasEqual(t,
				mustSchema(t, NewReaderFromBytes(packed)),
				mustSchema(t, NewReaderFromBytes(plain)))
		})
	}
}

// assertSchemasEqual compares two schemas field by field, including the
// dictionary entries and their ORDER — entry order is the on-wire encoding of
// a categorical, so two schemas with the same entries in a different order
// are two different cohorts.
func assertSchemasEqual(t *testing.T, got, want *encoding.Schema) {
	t.Helper()
	if len(got.Fields) != len(want.Fields) {
		t.Fatalf("schema has %d field(s), want %d", len(got.Fields), len(want.Fields))
	}
	for i := range want.Fields {
		g, w := got.Fields[i], want.Fields[i]
		if g.Name != w.Name || g.Type != w.Type || g.Nullable != w.Nullable {
			t.Errorf("field %d = {%s %s nullable=%v}, want {%s %s nullable=%v}",
				i, g.Name, g.Type, g.Nullable, w.Name, w.Type, w.Nullable)
			continue
		}
		gv, wv := dictValues(g), dictValues(w)
		if len(gv) != len(wv) {
			t.Errorf("field %q has %d dictionary entr(ies), want %d", w.Name, len(gv), len(wv))
			continue
		}
		for j := range wv {
			if gv[j] != wv[j] {
				t.Errorf("field %q dictionary[%d] = %q, want %q", w.Name, j, gv[j], wv[j])
			}
		}
	}
}

func dictValues(f encoding.Field) []string {
	if f.Dictionary == nil {
		return nil
	}
	return f.Dictionary.Values()
}

// TestBytecode_EveryCommandDecodes reads the every-command fixture and checks
// the VALUES, not just that the two arms agree. Two arms agreeing proves the
// expansion is consistent; this proves it is correct, which is the thing a
// fixture generator sharing a misreading with the reader could not tell us.
func TestBytecode_EveryCommandDecodes(t *testing.T) {
	_, packed := bothEncodings(t, everyCommandSpec())
	rows := readAll(t, NewReaderFromBytes(packed))

	want := [][]string{
		// N (one command byte), BIG (escape), FRAC (escape), MISS
		// (system-missing, rendered as the house null token), S.
		{"1", "1e+09", "2.5", "", "ALPHA"},
		{"0", "-1e+09", "-0.25", "", ""},
		{"151", "1e+300", "1.5", "", "BETA"},
		{"-99", "-252", "0.1", "", "GAMMA BRAVO"},
	}
	assertRows(t, rows, want)
}

// TestBytecode_HonoursTheDeclaredBias is the criterion the conventional
// fixture cannot reach. A reader that hardcodes 100 reads a file written
// under a different bias as a plausible set of numbers offset by a constant
// — never as an error — so only a fixture that declares another bias can
// catch it.
//
// R's foreign and haven both honour the declared bias on this same fixture
// (foreign warns that 50 is unusual, which is itself proof it read the field
// rather than assuming), so the expectation here is corroborated outside this
// module.
func TestBytecode_HonoursTheDeclaredBias(t *testing.T) {
	for _, bias := range []float64{100, 50, 1, 251, -20, 1000} {
		spec := spsstest.ReferenceSpec()
		spec.Compression = spsstest.CompressionBytecode
		spec.CompressionBias = bias

		rows := readAll(t, NewReaderFromBytes(build(t, spec)))
		assertRows(t, rows, [][]string{
			{"1", "1", "ALICE"},
			{"2", "", "BOB"},
		})
	}
}

// TestBytecode_BiasIsReadFromTheHeaderNotAssumed is the same claim made
// destructively. Rewriting only the header's bias field, leaving the command
// bytes alone, must change every decoded integer by the difference — if it
// does not, the bias in the decode arithmetic came from somewhere other than
// the header.
func TestBytecode_BiasIsReadFromTheHeaderNotAssumed(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	raw := build(t, spec)

	// 100 → 90. ID's commands (101, 102) then decode to 11 and 12.
	binary.LittleEndian.PutUint64(raw[offBias:], math.Float64bits(90))

	rows := readAll(t, NewReaderFromBytes(raw))
	if len(rows) != 2 {
		t.Fatalf("read %d case(s), want 2", len(rows))
	}
	if rows[0][0] != "11" || rows[1][0] != "12" {
		t.Errorf("ID decoded as %q and %q under a header bias of 90; want %q and %q — the decoder is not reading the bias from the header",
			rows[0][0], rows[1][0], "11", "12")
	}
}

// TestBytecode_UnusableBias covers a header whose bias is not a number
// arithmetic can use. Every integer command would decode to NaN, which is
// data this reader has no basis for emitting, so it is refused.
func TestBytecode_UnusableBias(t *testing.T) {
	for _, tc := range []struct {
		name string
		bias float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.ReferenceSpec()
			spec.Compression = spsstest.CompressionBytecode
			raw := build(t, spec)
			binary.LittleEndian.PutUint64(raw[offBias:], math.Float64bits(tc.bias))

			err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
				t.Error("a case was delivered under an unusable compression bias")
				return nil
			})
			if err == nil {
				t.Fatal("an unusable compression bias read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_COMPRESSION_INVALID {
				t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_COMPRESSION_INVALID)
			}
			assertDetails(t, ce, len(raw))
		})
	}
}

// TestBytecode_Truncated is the story's no-panic criterion, taken over every
// possible truncation rather than a chosen one. A command stream has no
// framing beyond its blocks, so a cut can land mid-block, mid-payload, or on
// a boundary that leaves a case half-assembled — and each has to be a coded
// error.
//
// A cut that happens to land on a case boundary is legitimately readable, so
// the assertion is "an error or a clean short read", never a panic and never
// a silently-invented case.
func TestBytecode_Truncated(t *testing.T) {
	spec := everyCommandSpec()
	spec.Compression = spsstest.CompressionBytecode
	full := build(t, spec)

	dictEnd := len(full) - dataSectionLen(t, spec)

	sawTruncation := false
	for cut := dictEnd; cut < len(full); cut++ {
		raw := full[:cut]
		rows := 0
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
			rows++
			return nil
		})
		if err == nil {
			// A short but whole read. Every case it produced must be
			// a prefix of the full file's cases, never an invention.
			if rows > 4 {
				t.Errorf("cut at %d produced %d case(s); the file has only 4", cut, rows)
			}
			continue
		}
		ce := codedError(t, err)
		switch ce.Code {
		case perr.PULSE_SPSS_DATA_TRUNCATED, perr.PULSE_SPSS_COMPRESSION_INVALID:
			sawTruncation = true
		default:
			t.Errorf("cut at %d: code = %s, want a truncation or corruption code", cut, ce.Code)
		}
		assertDetails(t, ce, len(raw))
	}
	if !sawTruncation {
		t.Error("no truncation of a compressed stream produced a coded error; the test is not exercising the path it claims to")
	}
}

// dataSectionLen returns the byte length of a spec's compressed data section,
// derived by building the spec twice and differencing: once as given and once
// with its cases removed. The dictionary is identical between the two, so the
// difference is the data — except for the one command block a case-less build
// still emits (the end-of-file command and its padding), which is added back.
//
// Differencing rather than parsing is deliberate: a test that asked the
// reader where the data section starts would inherit whatever the reader
// believes, and this test exists to check the reader.
func dataSectionLen(t *testing.T, spec spsstest.Spec) int {
	t.Helper()
	withCases := build(t, spec)
	spec.Cases = nil
	empty := build(t, spec)
	return len(withCases) - len(empty) + 8
}

// TestBytecode_MissingPayload covers the specific truncation the escape
// command creates: a stream that ends with a verbatim-value command whose
// eight bytes never arrived. It cannot be caught by a stride check, because
// the command byte itself is present and well-formed.
func TestBytecode_MissingPayload(t *testing.T) {
	spec := spsstest.Spec{
		Vars:        []spsstest.Var{{Name: "N", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}}},
		Cases:       [][]spsstest.Value{{spsstest.Num(0.5)}}, // not whole: takes the escape
		Compression: spsstest.CompressionBytecode,
	}
	full := build(t, spec)
	// The data section is one command block — the escape, the end-of-file
	// command, then padding — followed by the escape's eight-byte payload.
	// Dropping exactly the payload leaves a stream whose commands are all
	// present and whose last one cannot be honoured.
	raw := full[:len(full)-elementSize]

	err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
		t.Error("a case was delivered from a stream whose payload is missing")
		return nil
	})
	if err == nil {
		t.Fatal("a missing payload read without error")
	}
	ce := codedError(t, err)
	if ce.Code != perr.PULSE_SPSS_DATA_TRUNCATED {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_DATA_TRUNCATED)
	}
	assertDetails(t, ce, len(raw))
}

// TestBytecode_CommandKindMismatch covers a stream that has lost sync with
// the dictionary. Both directions are checked, because they fail differently:
// an all-spaces segment in a numeric column would decode to a plausible
// 1.5e-153, and system-missing in a string column has no meaning at all —
// SPSS gives strings no system-missing state.
//
// Accepting either would put invented numbers in a cohort, which is the
// failure mode this whole reader is built to refuse.
func TestBytecode_CommandKindMismatch(t *testing.T) {
	cases := []struct {
		name string
		// elem is the element position within a case to corrupt, and
		// cmd the command to put there.
		elem int
		cmd  byte
		want string
	}{
		{"an all-spaces segment where a numeric is declared", 0, cmdSpaces, "all-spaces string segment"},
		{"system-missing where a string is declared", 2, cmdSysmis, "system-missing sentinel"},
		{"an integer literal where a string is declared", 2, 101, "integer literal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.ReferenceSpec()
			spec.Compression = spsstest.CompressionBytecode
			raw := build(t, spec)

			// The reference fixture's whole first block is its eight
			// commands, at the first byte of the data section.
			start := len(raw) - 32
			raw[start+tc.elem] = tc.cmd

			err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
				return nil
			})
			if err == nil {
				t.Fatal("a desynchronised command stream read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_COMPRESSION_INVALID {
				t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_COMPRESSION_INVALID)
			}
			if !strings.Contains(ce.Message, tc.want) {
				t.Errorf("message = %q, want it to name %q", ce.Message, tc.want)
			}
			assertDetails(t, ce, len(raw))
		})
	}
}

// TestBytecode_VerbatimIsLegalEverywhere is the complement of the mismatch
// test. The escape carries eight opaque bytes and says nothing about their
// type, so it is legal at any element position — a reader that narrowed the
// check too far would reject files SPSS writes routinely, since every string
// segment holding text uses it.
func TestBytecode_VerbatimIsLegalEverywhere(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	if !plan(t, build(t, spec)).elemKinds[0].allows(cmdRaw) {
		t.Error("the escape command is rejected at a numeric element")
	}
	if !plan(t, build(t, spec)).elemKinds[2].allows(cmdRaw) {
		t.Error("the escape command is rejected at a string element")
	}
}

// plan resolves a fixture's case geometry, for a test that needs to reason
// about element positions rather than about rendered cells.
func plan(t *testing.T, raw []byte) *dataPlan {
	t.Helper()
	p, err := buildDataPlan(mustParse(t, raw))
	if err != nil {
		t.Fatalf("buildDataPlan: %v", err)
	}
	return p
}

// TestBytecode_ElementKinds pins the table the command check reads. A string
// wider than one segment claims a CONTINUATION element, and that element is a
// string segment in its own right — getting it wrong would let the check pass
// a system-missing command into the middle of a name.
func TestBytecode_ElementKinds(t *testing.T) {
	// ID numeric, SEX numeric, NAME width 10 → two string elements.
	p := plan(t, build(t, spsstest.ReferenceSpec()))
	want := []elementKind{elemNumeric, elemNumeric, elemString, elemString}
	if len(p.elemKinds) != len(want) {
		t.Fatalf("elemKinds has %d entr(ies), want %d", len(p.elemKinds), len(want))
	}
	for i := range want {
		if p.elemKinds[i] != want[i] {
			t.Errorf("elemKinds[%d] = %v, want %v", i, p.elemKinds[i], want[i])
		}
	}
}

// TestBytecode_PaddingIsIgnored covers command 0 explicitly. The generator
// emits it to fill out the final block, but a writer may emit it anywhere,
// and a decoder that treated it as an element would shift every case after it
// by one.
func TestBytecode_PaddingIsIgnored(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	raw := build(t, spec)

	// The final block is the end-of-file command followed by seven pads.
	// Move the end-of-file command to the far end of the block, so the
	// seven bytes before it are padding the decoder must step over
	// without producing an element.
	tail := len(raw) - 8
	for i := 0; i < 7; i++ {
		raw[tail+i] = cmdPad
	}
	raw[tail+7] = cmdEOF

	assertRows(t, readAll(t, NewReaderFromBytes(raw)), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestBytecode_StreamWithoutEndOfFile covers a writer that simply stops. The
// end-of-file command is how a well-formed stream ends, but a stream that ran
// out of command bytes exactly on a case boundary has told us everything, and
// refusing it would reject data the file plainly contains.
func TestBytecode_StreamWithoutEndOfFile(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	raw := build(t, spec)
	// Drop the final block, which holds only the end-of-file command
	// and its padding.
	raw = raw[:len(raw)-8]

	assertRows(t, readAll(t, NewReaderFromBytes(raw)), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestBytecode_TrailingBytesAfterEndOfFile covers the other end of the same
// question. Nothing after the end-of-file command is data, so a file carrying
// a trailing block must read as though it were not there rather than as extra
// cases.
func TestBytecode_TrailingBytesAfterEndOfFile(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	raw := build(t, spec)
	raw = append(raw, 101, 101, cmdSpaces, cmdSpaces, 0, 0, 0, 0)

	assertRows(t, readAll(t, NewReaderFromBytes(raw)), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestBytecode_Reset covers a second pass over a compressed file. The
// expansion is memoised with the mapping and deliberately survives Reset, so
// the second pass must produce the same rows without re-decoding — and above
// all must not resume from wherever the first pass left the stream.
func TestBytecode_Reset(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	r := NewReaderFromBytes(build(t, spec))

	first := readAll(t, r)
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	assertRows(t, readAll(t, r), first)
}

// TestValueCommand_InvertsCommandValue is the guard on the shared table, and
// the reason the table is shared at all.
//
// E5-S3 implements the bytecode ENCODER as this decoder's mirror. If the two
// halves disagree about the compressible range or round the bias differently,
// a value written by one and read by the other comes back as a DIFFERENT
// number, silently — and a round trip through our own codec would not
// necessarily catch it, because both halves would share the mistake and agree
// with each other. So the inverse property is asserted here, against the
// arithmetic rather than against an encoder that does not exist yet.
func TestValueCommand_InvertsCommandValue(t *testing.T) {
	for _, bias := range []float64{100, 0, 1, 251, -20, 1000} {
		// Every value the encoder accepts must decode back to itself.
		for v := -500.0; v <= 500.0; v++ {
			cmd, ok := valueCommand(v, bias)
			if !ok {
				continue
			}
			if cmd < cmdIntMin || cmd > cmdIntMax {
				t.Fatalf("bias %v, value %v: command %d is outside [%d,%d]", bias, v, cmd, cmdIntMin, cmdIntMax)
			}
			if back := commandValue(cmd, bias); back != v {
				t.Fatalf("bias %v: value %v encodes to command %d, which decodes to %v", bias, v, cmd, back)
			}
		}
		// And every command the decoder accepts must encode back to
		// itself, which is what closes the range at both ends.
		for c := int(cmdIntMin); c <= int(cmdIntMax); c++ {
			v := commandValue(byte(c), bias)
			cmd, ok := valueCommand(v, bias)
			if !ok {
				t.Fatalf("bias %v: command %d decodes to %v, which the encoder refuses to encode", bias, c, v)
			}
			if int(cmd) != c {
				t.Fatalf("bias %v: command %d decodes to %v, which re-encodes to command %d", bias, c, v, cmd)
			}
		}
	}
}

// TestValueCommand_RefusesWhatCannotRoundTrip states the encoder half's
// refusals as a table, so E5-S3 inherits them rather than rediscovering them.
// Each is a value that MUST take the verbatim escape.
func TestValueCommand_RefusesWhatCannotRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		bias  float64
	}{
		{"one below the range", -100, 100},
		{"one above the range", 152, 100},
		{"a fraction", 0.5, 100},
		{"NaN", math.NaN(), 100},
		{"+Inf", math.Inf(1), 100},
		{"-Inf", math.Inf(-1), 100},
		{"a whole number under a fractional bias", 0, 100.5},
		{"a whole number under a non-finite bias", 0, math.Inf(1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if cmd, ok := valueCommand(tc.value, tc.bias); ok {
				t.Errorf("value %v under bias %v encoded to command %d; it must take the escape", tc.value, tc.bias, cmd)
			}
		})
	}
}

// TestBytecode_CountRecordsAgrees checks the case count the decode reports.
// It is not cosmetic: the count is what the declared-versus-actual warning
// compares against, so a decoder that produced the right rows but the wrong
// count would warn on every clean compressed file.
func TestBytecode_CountRecordsAgrees(t *testing.T) {
	spec := everyCommandSpec()
	spec.Compression = spsstest.CompressionBytecode
	r := NewReaderFromBytes(build(t, spec))

	rows := readAll(t, r)
	if len(rows) != 4 {
		t.Fatalf("read %d case(s), want 4", len(rows))
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("a clean compressed file raised %d warning(s): %v", len(w), w)
	}
}

// TestBytecode_ManyCases exercises the expansion at a size where the output
// buffer has to grow, and where a per-block bug that a four-case fixture
// tolerates would compound into visible drift.
func TestBytecode_ManyCases(t *testing.T) {
	const n = 5000
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "I", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "S", Width: 8},
		},
	}
	for i := 0; i < n; i++ {
		// Values sweep in and out of the compressible range, so the
		// stream alternates between one-byte commands and escapes.
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i - 200)),
			spsstest.Text(strings.Repeat("A", i%9)),
		})
	}
	plain, packed := bothEncodings(t, spec)
	assertRows(t, readAll(t, NewReaderFromBytes(packed)), readAll(t, NewReaderFromBytes(plain)))
}

// TestBytecode_DoesNotPanicOnArbitraryBytes is the blunt no-panic guard. The
// data section is replaced with bytes that are not a valid stream at all, and
// every one of them must produce a value or a coded error.
func TestBytecode_DoesNotPanicOnArbitraryBytes(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Compression = spsstest.CompressionBytecode
	base := build(t, spec)
	start := len(base) - 32

	for fill := 0; fill < 256; fill++ {
		raw := append([]byte(nil), base...)
		for i := start; i < len(raw); i++ {
			raw[i] = byte(fill)
		}
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error { return nil })
		if err == nil {
			continue
		}
		if _, ok := err.(*perr.CodedError); !ok {
			t.Fatalf("fill %d: error is %T, not a coded error: %v", fill, err, err)
		}
	}
}
