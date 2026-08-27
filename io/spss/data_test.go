package spss

import (
	"context"
	stderrors "errors"

	"encoding/binary"
	"math"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// readAll drains a reader into a copied row slice. The copy is deliberate:
// ReadRows reuses its row buffer between cases, so a test that kept the slice
// would see every row equal to the last one and would pass or fail for the
// wrong reason.
func readAll(t *testing.T, r *Reader) [][]string {
	t.Helper()
	var out [][]string
	err := r.ReadRows(context.Background(), func(row []string) error {
		out = append(out, append([]string(nil), row...))
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	return out
}

func assertRows(t *testing.T, got, want [][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("read %d case(s), want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("case %d has %d column(s), want %d: %q", i, len(got[i]), len(want[i]), got[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("case %d column %d = %q, want %q", i, j, got[i][j], want[i][j])
			}
		}
	}
}

// TestReadRows_ReferenceFixture reads the one fixture whose every byte a human
// checked against the specification. It is the anchor for every other data
// test: a change that breaks the decode breaks this first.
//
// The expectations encode all three rendering rules at once — a numeric as its
// shortest round-tripping decimal, a system-missing datum as the empty string,
// and a string trimmed of the padding that carried it out to its declared
// width and then to the 8-byte segment boundary.
func TestReadRows_ReferenceFixture(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	r := NewReaderFromBytes(raw)

	cols, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	wantCols := []string{"ID", "SEX", "NAME"}
	if len(cols) != len(wantCols) {
		t.Fatalf("ReadHeader = %q, want %q", cols, wantCols)
	}
	for i := range wantCols {
		if cols[i] != wantCols[i] {
			t.Errorf("column %d = %q, want %q", i, cols[i], wantCols[i])
		}
	}

	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestReadHeader_PrefersTheLongName asserts ReadHeader goes through
// variable.fieldName rather than reading the short name directly. The record
// 7/13 long name is the variable's real name; the 8-byte short name is a
// truncated, upper-cased derivation SPSS retains for backward compatibility,
// and surfacing it would silently rename every column of a modern file.
func TestReadHeader_PrefersTheLongName(t *testing.T) {
	raw := build(t, spsstest.ExtensionReferenceSpec())
	r := NewReaderFromBytes(raw)

	cols, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	// ID and NAME declare long names in the extension fixture; SEX does
	// not, so it falls back to its short name — the mixed case is the
	// point.
	//
	// The extension fixture also declares two multiple-DICHOTOMY response
	// sets over those same variables, so each contributes a derived
	// `set_*` column after the last of its constituents (SEX for both).
	// They are ADDITIVE: RespondentId, SEX and FullName are all still
	// here, in order, which is the property E4-S4 exists to hold. The
	// fixture's multiple-CATEGORY set contributes nothing.
	want := []string{"RespondentId", "SEX", "media", "ext", "FullName"}
	if len(cols) != len(want) {
		t.Fatalf("ReadHeader = %q, want %q", cols, want)
	}
	for i := range want {
		if cols[i] != want[i] {
			t.Errorf("column %d = %q, want %q", i, cols[i], want[i])
		}
	}

	// The data section is untouched by the extension records; the two
	// derived cells are a second reading of RespondentId and SEX, whose
	// own columns are unchanged beside them. Case 1's SEX is
	// system-missing, so it contributes no bit and no evidence the row was
	// answered — $ext is over SEX alone, so that row's $ext cell is the
	// empty string and imports as null, while $media still sees
	// RespondentId present and not counted, which is an EMPTY MASK.
	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "RespondentId|SEX", "SEX", "ALICE"},
		{"2", "", setEmptySelection, "", "BOB"},
	})
}

// TestReadHeader_IsMemoised proves the second call reuses the first result
// rather than rebuilding it, and that Reset drops it so a renamed dictionary
// could never be served from a stale cache.
func TestReadHeader_IsMemoised(t *testing.T) {
	r := NewReaderFromBytes(build(t, spsstest.ReferenceSpec()))
	first, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	again, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader (second call): %v", err)
	}
	if &first[0] != &again[0] {
		t.Error("the second ReadHeader rebuilt the slice instead of returning the memoised one")
	}
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if r.header != nil {
		t.Error("Reset left the memoised header behind")
	}
}

// TestReadRows_WithoutReadHeader covers the caller that goes straight to the
// rows. ReadRows must resolve the dictionary itself rather than assuming a
// prior ReadHeader left one behind.
func TestReadRows_WithoutReadHeader(t *testing.T) {
	r := NewReaderFromBytes(build(t, spsstest.ReferenceSpec()))
	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestReadRows_Values is the rendering table: one fixture carrying every
// numeric and string shape whose canonical string is worth pinning.
func TestReadRows_Values(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "N", Print: spsstest.Format{Type: spsstest.FormatF, Width: 8}},
			{Name: "S", Width: 20},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(0), spsstest.Text("")},
			{spsstest.Num(-1), spsstest.Text("a")},
			{spsstest.Num(1.5), spsstest.Text("exactly eight!!")},
			{spsstest.Num(-0.125), spsstest.Text("sixteen bytes ok")},
			{spsstest.Num(1e20), spsstest.Text("the full twenty byte")},
			{spsstest.Num(1e21), spsstest.Text("trailing spaces")},
			{spsstest.Num(0.1), spsstest.Text("0")},
			{spsstest.Num(math.MaxFloat64), spsstest.Text("x")},
			{spsstest.SysMis(), spsstest.Text("after a sysmis")},
		},
	}

	r := NewReaderFromBytes(build(t, spec))
	assertRows(t, readAll(t, r), [][]string{
		// 0 renders "0", not "0e+00": shortest round-trip form.
		{"0", ""},
		{"-1", "a"},
		{"1.5", "exactly eight!!"},
		{"-0.125", "sixteen bytes ok"},
		// 'g' stays in plain notation until the exponent reaches 21,
		// so a 20-digit integer is not silently exponentiated.
		{"1e+20", "the full twenty byte"},
		{"1e+21", "trailing spaces"},
		{"0.1", "0"},
		// The largest finite double is NOT sysmis; only its negation is.
		{"1.7976931348623157e+308", "x"},
		{"", "after a sysmis"},
	})
}

// TestReadRows_SysmisIsTheHouseNullToken is the interlock with the shared
// inference path. A sysmis datum must render to something io/import.go's
// isNullToken recognises, or the column that SPSS declared missing arrives as
// a finite value of about -1.8e308 and drags the whole column to f64 with a
// wildly wrong range.
func TestReadRows_SysmisIsTheHouseNullToken(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "N"}},
		Cases: [][]spsstest.Value{{spsstest.SysMis()}, {spsstest.Num(7)}},
	}
	rows := readAll(t, NewReaderFromBytes(build(t, spec)))
	if rows[0][0] != "" {
		t.Fatalf("sysmis rendered as %q, want the empty string", rows[0][0])
	}
	// The reader must not have confused -DBL_MAX with any other extreme.
	if rows[1][0] != "7" {
		t.Errorf("the following case = %q, want %q", rows[1][0], "7")
	}
	// Guard the specific misreading: the sentinel's own literal value
	// must never be what a caller sees.
	if strings.Contains(rows[0][0], "308") {
		t.Errorf("sysmis leaked its literal double value: %q", rows[0][0])
	}
}

// TestReadRows_SysmisHonoursADeclaredSentinel proves the decode reads the
// sentinel off the dictionary rather than hardcoding -DBL_MAX. Record 7/4 is
// an override, and a file declaring an unusual sysmis must have it honoured
// on both sides — its own sentinel reads as null, and -DBL_MAX does not.
func TestReadRows_SysmisHonoursADeclaredSentinel(t *testing.T) {
	const declared = -1e100

	// The parser adopts a declared sentinel only from a coherent
	// sysmis < lowest < highest triple, so the whole triple has to move.
	mf := spsstest.MachineFloatInfo{SysMis: declared, Lowest: -1e99, Highest: 1e99}
	spec := spsstest.Spec{
		Vars:             []spsstest.Var{{Name: "N"}},
		MachineFloatInfo: &mf,
		Cases: [][]spsstest.Value{
			{spsstest.Num(declared)},
			{spsstest.Num(-math.MaxFloat64)},
		},
	}

	r := NewReaderFromBytes(build(t, spec))
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if d.sysmis != declared {
		t.Fatalf("the fixture did not take: dictionary sysmis = %v, want %v", d.sysmis, declared)
	}

	rows := readAll(t, r)
	if rows[0][0] != "" {
		t.Errorf("the declared sentinel rendered as %q, want the empty string", rows[0][0])
	}
	if rows[1][0] == "" {
		t.Error("-DBL_MAX read as null even though the file declared a different sentinel")
	}
}

// TestReadRows_StringSegments covers the geometry a string variable wider
// than one element imposes: the value is reassembled across its 8-byte
// segments, and the padding between the declared width and the segment
// boundary never reaches the caller.
func TestReadRows_StringSegments(t *testing.T) {
	cases := []struct {
		name  string
		width int
		value string
		want  string
	}{
		{"one segment, exact fit", 8, "12345678", "12345678"},
		{"one segment, short value", 8, "ab", "ab"},
		{"one segment, sub-width", 3, "xy", "xy"},
		{"two segments, exact fit", 16, "0123456789abcdef", "0123456789abcdef"},
		{"two segments, padded to the boundary", 10, "ALICE", "ALICE"},
		{"two segments, full declared width", 10, "0123456789", "0123456789"},
		{"empty value", 12, "", ""},
		{"the widest short string", 255, "wide", "wide"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := spsstest.Spec{
				Vars: []spsstest.Var{
					{Name: "S", Width: tc.width},
					// A trailing numeric proves the string's
					// segments did not swallow the next column.
					{Name: "N"},
				},
				Cases: [][]spsstest.Value{{spsstest.Text(tc.value), spsstest.Num(42)}},
			}
			rows := readAll(t, NewReaderFromBytes(build(t, spec)))
			if rows[0][0] != tc.want {
				t.Errorf("string = %q, want %q", rows[0][0], tc.want)
			}
			if rows[0][1] != "42" {
				t.Errorf("the column after the string = %q, want %q", rows[0][1], "42")
			}
		})
	}
}

// TestReadRows_ColumnOrderSurvivesContinuations is the trap a naive decode
// falls into: dictionary element indices are not ordinal variable positions,
// because a wide string consumes several elements. A reader that walked
// variables by position would read the second variable out of the first
// one's continuation bytes.
func TestReadRows_ColumnOrderSurvivesContinuations(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{
			{Name: "WIDE", Width: 30}, // 4 elements
			{Name: "A"},
			{Name: "MID", Width: 12}, // 2 elements
			{Name: "B"},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Text("first"), spsstest.Num(1), spsstest.Text("second"), spsstest.Num(2)},
			{spsstest.Text("third"), spsstest.Num(3), spsstest.Text("fourth"), spsstest.Num(4)},
		},
	}
	r := NewReaderFromBytes(build(t, spec))

	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if d.elementCount != 8 {
		t.Fatalf("the fixture did not take: elementCount = %d, want 8", d.elementCount)
	}

	assertRows(t, readAll(t, r), [][]string{
		{"first", "1", "second", "2"},
		{"third", "3", "fourth", "4"},
	})
}

// TestReadRows_TrustsTheCountedStrideNotTheHeader pins the E2-S2 handoff
// decision. nominal_case_size is a writer's claim; elementCount is counted
// from the record type 2 stream. A file whose header lies must still decode,
// because the records are what the data section was written against.
func TestReadRows_TrustsTheCountedStrideNotTheHeader(t *testing.T) {
	const offNominalCaseSize = 68

	raw := build(t, spsstest.ReferenceSpec())
	// The reference fixture is 3 variables over 4 elements (NAME is
	// width 10, so two). Claim 99 and check nothing moves.
	binary.LittleEndian.PutUint32(raw[offNominalCaseSize:offNominalCaseSize+4], uint32(99))

	r := NewReaderFromBytes(raw)
	d, err := r.loadDictionary()
	if err != nil {
		t.Fatalf("loadDictionary: %v", err)
	}
	if d.header.nominalCaseSize != 99 || d.elementCount != 4 {
		t.Fatalf("the fixture did not take: nominalCaseSize = %d, elementCount = %d",
			d.header.nominalCaseSize, d.elementCount)
	}

	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestReadRows_EmptyDataSection covers a dictionary-only file, which is legal
// and must read as zero cases rather than as an error.
func TestReadRows_EmptyDataSection(t *testing.T) {
	spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "A"}, {Name: "S", Width: 4}}}
	r := NewReaderFromBytes(build(t, spec))

	cols, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if len(cols) != 2 {
		t.Fatalf("ReadHeader = %q, want two columns", cols)
	}
	if rows := readAll(t, r); len(rows) != 0 {
		t.Errorf("read %d case(s) from an empty data section: %q", len(rows), rows)
	}
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("an empty data section warned: %v", w)
	}
}

// TestReadRows_Reset is the ResetReader criterion: the infer-then-import
// sequence reads the same source twice and must see the same rows both times.
func TestReadRows_Reset(t *testing.T) {
	r := NewReaderFromBytes(build(t, spsstest.ReferenceSpec()))

	first := readAll(t, r)
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	cols, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader after Reset: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("ReadHeader after Reset = %q", cols)
	}
	assertRows(t, readAll(t, r), first)

	// And without an intervening Reset at all: ReadRows computes its
	// offsets from the dictionary every pass, so it is idempotent.
	assertRows(t, readAll(t, r), first)
}

// TestReader_SatisfiesResetReader pins the interface assertion as a behaviour
// rather than only as a compile-time var block, so a caller that dispatches on
// the optional interface is covered.
func TestReader_SatisfiesResetReader(t *testing.T) {
	var rd pio.Reader = NewReaderFromBytes(build(t, spsstest.ReferenceSpec()))
	rr, ok := rd.(pio.ResetReader)
	if !ok {
		t.Fatal("spss.Reader does not satisfy pio.ResetReader")
	}
	if err := rr.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

// TestReadRows_ContextCancellation covers the cancellation criterion on both
// axes: a context cancelled before the first case, and one cancelled part way
// through.
func TestReadRows_ContextCancellation(t *testing.T) {
	spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "N"}}}
	for i := 0; i < 64; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{spsstest.Num(float64(i))})
	}
	raw := build(t, spec)

	t.Run("cancelled before the first case", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		seen := 0
		err := NewReaderFromBytes(raw).ReadRows(ctx, func([]string) error {
			seen++
			return nil
		})
		if err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if seen != 0 {
			t.Errorf("delivered %d case(s) under an already-cancelled context", seen)
		}
	})

	t.Run("cancelled part way through", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		seen := 0
		err := NewReaderFromBytes(raw).ReadRows(ctx, func([]string) error {
			seen++
			if seen == 10 {
				cancel()
			}
			return nil
		})
		if err != context.Canceled {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if seen != 10 {
			t.Errorf("delivered %d case(s), want the pass to stop on the case after the cancel", seen)
		}
	})
}

// TestReadRows_StopIteration covers the sentinel the inference sampler
// returns to stop after its sample window. It must end the pass cleanly, not
// surface as an error.
func TestReadRows_StopIteration(t *testing.T) {
	spec := spsstest.Spec{Vars: []spsstest.Var{{Name: "N"}}}
	for i := 0; i < 20; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{spsstest.Num(float64(i))})
	}

	seen := 0
	err := NewReaderFromBytes(build(t, spec)).ReadRows(context.Background(), func([]string) error {
		seen++
		if seen == 3 {
			return pio.ErrStopIteration()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if seen != 3 {
		t.Errorf("delivered %d case(s), want 3", seen)
	}
}

// TestReadRows_CallbackErrorPropagates asserts a callback fault is surfaced
// verbatim — the import path relies on its own row errors reaching it.
func TestReadRows_CallbackErrorPropagates(t *testing.T) {
	want := stderrors.New("boom")
	err := NewReaderFromBytes(build(t, spsstest.ReferenceSpec())).
		ReadRows(context.Background(), func([]string) error { return want })
	if !stderrors.Is(err, want) {
		t.Fatalf("err = %v, want the callback's own error", err)
	}
}

// TestReadRows_FromAferoFilesystem is the afero criterion. Nothing in the
// package may touch os, so the same fixture must read identically through a
// MemMapFs.
func TestReadRows_FromAferoFilesystem(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "survey.sav", raw, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewReader(cfg.Fs(), "survey.sav")
	assertRows(t, readAll(t, r), [][]string{
		{"1", "1", "ALICE"},
		{"2", "", "BOB"},
	})
}

// TestReadRows_UnknownCompressionRefused covers the one branch of
// readCaseData that is not an encoding: a compression flag the format does
// not define.
//
// All three defined encodings are read now — uncompressed, bytecode and ZSAV
// — so PULSE_SPSS_COMPRESSION_UNSUPPORTED is no longer about any of them. It
// is reached by rewriting the header field past the range the header parse
// accepts, which means this exercises the parse's own guard first and the
// data-section refusal second. Both must fire, because a data section read
// under an encoding nobody named produces plausible numbers rather than an
// error.
//
// The seam E3-S1 left for ZSAV is gone: see zsav_test.go, where a `.zsav`
// reads as a cohort identical to its uncompressed twin.
func TestReadRows_UnknownCompressionRefused(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())
	binary.LittleEndian.PutUint32(raw[offCompression:offCompression+4], 7)

	// The header parse is the first line of defence and rejects the flag
	// outright, before any data section is reached.
	if _, err := parseDictionary(raw); err == nil {
		t.Fatal("a header declaring compression 7 parsed without error")
	}

	// readCaseData keeps its own refusal for the same flag, so a
	// dictionary reaching it by any other route still fails loudly
	// rather than falling through to a decode.
	d := mustParse(t, build(t, spsstest.ReferenceSpec()))
	d.header.compression = 7
	p, err := buildDataPlan(d)
	if err != nil {
		t.Fatalf("buildDataPlan: %v", err)
	}
	_, _, err = readCaseData(d, raw, p)
	if err == nil {
		t.Fatal("an unrecognised compression flag read without error")
	}
	ce := codedError(t, err)
	if ce.Code != perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED {
		t.Fatalf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_COMPRESSION_UNSUPPORTED)
	}
	if !strings.Contains(ce.Message, "all three are read") {
		t.Errorf("message = %q, want it to say every defined encoding is read", ce.Message)
	}
	assertDetails(t, ce, len(raw))
}

// TestReadRows_Truncated covers a data section that ends mid-case. Every
// truncation inside one case must be caught: the stride is the only framing
// the format has, so a short tail cannot be told from a valid case by any
// other means.
func TestReadRows_Truncated(t *testing.T) {
	full := build(t, spsstest.ReferenceSpec())
	// The reference fixture is 4 elements per case, so 32 bytes.
	const stride = 32

	for cut := 1; cut < stride; cut++ {
		raw := full[:len(full)-cut]
		err := NewReaderFromBytes(raw).ReadRows(context.Background(), func([]string) error {
			t.Errorf("cut %d: a case was delivered from a truncated data section", cut)
			return nil
		})
		if err == nil {
			t.Fatalf("cut %d: a truncated data section read without error", cut)
		}
		ce := codedError(t, err)
		if ce.Code != perr.PULSE_SPSS_DATA_TRUNCATED {
			t.Fatalf("cut %d: code = %s, want %s", cut, ce.Code, perr.PULSE_SPSS_DATA_TRUNCATED)
		}
		assertDetails(t, ce, len(raw))
	}
}

// TestReadRows_CaseCountMismatch covers the declared-versus-actual warning.
// The whole cases present are still read: discarding rows the file plainly
// contains to honour a writer's miscount would lose data.
func TestReadRows_CaseCountMismatch(t *testing.T) {
	t.Run("the header over-declares", func(t *testing.T) {
		raw := build(t, spsstest.ReferenceSpec())
		binary.LittleEndian.PutUint32(raw[offCaseCount:offCaseCount+4], uint32(9))

		r := NewReaderFromBytes(raw)
		assertRows(t, readAll(t, r), [][]string{
			{"1", "1", "ALICE"},
			{"2", "", "BOB"},
		})

		w := r.Warnings()
		if len(w) != 1 {
			t.Fatalf("got %d warning(s), want 1: %v", len(w), w)
		}
		if w[0].Code != perr.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH {
			t.Fatalf("code = %s, want %s", w[0].Code, perr.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH)
		}
		if got := w[0].Details[perr.DetailSPSSDeclaredCases]; got != int64(9) {
			t.Errorf("Details[%q] = %v, want 9", perr.DetailSPSSDeclaredCases, got)
		}
		if got := w[0].Details[perr.DetailSPSSActualCases]; got != 2 {
			t.Errorf("Details[%q] = %v, want 2", perr.DetailSPSSActualCases, got)
		}
		if !strings.Contains(w[0].Message, "the file header") {
			t.Errorf("message = %q, want it to name the header as the source", w[0].Message)
		}
	})

	t.Run("the record 7/16 count wins over the header", func(t *testing.T) {
		spec := spsstest.ReferenceSpec()
		wrong := int64(41)
		spec.CaseCount64 = &wrong

		r := NewReaderFromBytes(build(t, spec))
		if rows := readAll(t, r); len(rows) != 2 {
			t.Fatalf("read %d case(s), want 2", len(rows))
		}
		w := r.Warnings()
		if len(w) != 1 || w[0].Code != perr.PULSE_SPSS_DATA_CASE_COUNT_MISMATCH {
			t.Fatalf("warnings = %v, want one case-count mismatch", w)
		}
		if got := w[0].Details[perr.DetailSPSSDeclaredCases]; got != int64(41) {
			t.Errorf("Details[%q] = %v, want 41", perr.DetailSPSSDeclaredCases, got)
		}
		if !strings.Contains(w[0].Message, "7/16") {
			t.Errorf("message = %q, want it to name record 7/16 as the source", w[0].Message)
		}
	})

	t.Run("an unknown count is not a declaration", func(t *testing.T) {
		spec := spsstest.ReferenceSpec()
		spec.UnknownCaseCount = true

		r := NewReaderFromBytes(build(t, spec))
		if rows := readAll(t, r); len(rows) != 2 {
			t.Fatalf("read %d case(s), want 2", len(rows))
		}
		if w := r.Warnings(); len(w) != 0 {
			t.Errorf("an ncases of -1 warned: %v", w)
		}
	})

	t.Run("a matching count is silent", func(t *testing.T) {
		r := NewReaderFromBytes(build(t, spsstest.ReferenceSpec()))
		readAll(t, r)
		if w := r.Warnings(); len(w) != 0 {
			t.Errorf("a correct case count warned: %v", w)
		}
	})

	t.Run("warnings do not accumulate across passes", func(t *testing.T) {
		raw := build(t, spsstest.ReferenceSpec())
		binary.LittleEndian.PutUint32(raw[offCaseCount:offCaseCount+4], uint32(9))

		r := NewReaderFromBytes(raw)
		readAll(t, r)
		readAll(t, r)
		if err := r.Reset(); err != nil {
			t.Fatalf("Reset: %v", err)
		}
		readAll(t, r)
		if w := r.Warnings(); len(w) != 1 {
			t.Errorf("got %d warning(s) after three passes, want 1: %v", len(w), w)
		}
	})
}

// TestWarnings_CarriesTheDictionarysOwn proves the accessor is the union of
// both channels rather than only the data section's. Every extension warning
// E2-S3 raises had no way out of the package before this.
func TestWarnings_CarriesTheDictionarysOwn(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.RawExtensions = []spsstest.RawExtension{
		{Subtype: 4242, Size: 1, Payload: []byte("nobody knows what this is")},
	}

	r := NewReaderFromBytes(build(t, spec))
	if w := r.Warnings(); len(w) != 0 {
		t.Errorf("Warnings triggered a parse before anything was read: %v", w)
	}

	readAll(t, r)
	w := r.Warnings()
	if len(w) != 1 {
		t.Fatalf("got %d warning(s), want the one unknown subtype: %v", len(w), w)
	}
	if w[0].Code != perr.PULSE_SPSS_EXTENSION_UNKNOWN {
		t.Errorf("code = %s, want %s", w[0].Code, perr.PULSE_SPSS_EXTENSION_UNKNOWN)
	}
}

// TestReader_CloseIsIdempotent is the Close criterion. Two calls must both
// succeed, and a path-backed reader must still be usable afterwards because
// its bytes are recoverable; a bytes-backed one is not, and says so.
func TestReader_CloseIsIdempotent(t *testing.T) {
	raw := build(t, spsstest.ReferenceSpec())

	t.Run("twice over a bytes-backed reader", func(t *testing.T) {
		r := NewReaderFromBytes(raw)
		readAll(t, r)
		if err := r.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("Close (second call): %v", err)
		}
		if r.header != nil || r.dataWarnings != nil {
			t.Error("Close left derived state behind")
		}
		if _, err := r.ReadHeader(); err == nil {
			t.Error("a closed bytes-backed reader still read a header")
		}
	})

	t.Run("twice over a path-backed reader", func(t *testing.T) {
		cfg := fs.NewMemMap()
		if err := afero.WriteFile(cfg.Fs(), "survey.sav", raw, 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		r := NewReader(cfg.Fs(), "survey.sav")
		readAll(t, r)
		if err := r.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := r.Close(); err != nil {
			t.Fatalf("Close (second call): %v", err)
		}
		// Re-readable: init goes back to the filesystem.
		assertRows(t, readAll(t, r), [][]string{
			{"1", "1", "ALICE"},
			{"2", "", "BOB"},
		})
	})
}

// TestReadRows_ManyCases exercises the stride arithmetic past the point where
// an off-by-one in the offset walk would still land inside the buffer, and
// pins that the reader does not materialise a second copy of the file: the
// row buffer is one slice, reused.
func TestReadRows_ManyCases(t *testing.T) {
	const n = 5000
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "I"}, {Name: "S", Width: 10}},
	}
	for i := 0; i < n; i++ {
		spec.Cases = append(spec.Cases, []spsstest.Value{
			spsstest.Num(float64(i)),
			spsstest.Text(strings.Repeat("x", i%10)),
		})
	}

	r := NewReaderFromBytes(build(t, spec))

	seen := 0
	var rowPtr *string
	err := r.ReadRows(context.Background(), func(row []string) error {
		if rowPtr == nil {
			rowPtr = &row[0]
		} else if &row[0] != rowPtr {
			t.Fatalf("case %d got a fresh row slice; the buffer is meant to be reused", seen)
		}
		if want := strconv.Itoa(seen); row[0] != want {
			t.Fatalf("case %d numeric = %q, want %q", seen, row[0], want)
		}
		if row[1] != strings.Repeat("x", seen%10) {
			t.Fatalf("case %d string = %q", seen, row[1])
		}
		seen++
		return nil
	})
	if err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	if seen != n {
		t.Errorf("read %d case(s), want %d", seen, n)
	}
}

// TestBuildDataPlan_RejectsImpossibleGeometry covers the defensive bounds
// check. No well-formed file can reach it — elementCount is counted from the
// same record stream the variable indices come from — but a decode that
// trusted a hand-mutated dictionary would index past the end of a case.
func TestBuildDataPlan_RejectsImpossibleGeometry(t *testing.T) {
	cases := []struct {
		name string
		d    *dictionary
	}{
		{
			name: "a case with no elements",
			d: &dictionary{
				byteOrder:    binary.LittleEndian,
				elementCount: 0,
				vars:         []variable{{name: "A", index: 1, segments: 1}},
			},
		},
		{
			name: "a variable extending past the case",
			d: &dictionary{
				byteOrder:    binary.LittleEndian,
				elementCount: 1,
				vars:         []variable{{name: "A", index: 1, segments: 4, width: 30}},
			},
		},
		{
			name: "a variable starting past the case",
			d: &dictionary{
				byteOrder:    binary.LittleEndian,
				elementCount: 2,
				vars:         []variable{{name: "A", index: 9, segments: 1}},
			},
		},
		{
			name: "a width wider than the elements holding it",
			d: &dictionary{
				byteOrder:    binary.LittleEndian,
				elementCount: 2,
				vars:         []variable{{name: "A", index: 1, segments: 1, width: 30}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildDataPlan(tc.d)
			if err == nil {
				t.Fatal("impossible geometry accepted")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_DICT_INVALID {
				t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_DICT_INVALID)
			}
		})
	}
}

// FuzzReadRows extends the never-a-panic invariant across the dictionary
// boundary into the data decode. The dictionary fuzzer proves the walk is
// safe; this proves the offsets that walk produces are used safely too. For
// ANY input, ReadRows either succeeds or returns a coded PULSE_SPSS_* error,
// and every row it delivers has exactly one entry per header column.
//
// E3-S5 widened it in three ways, each for a failure it could not previously
// reach. PulseSchema is exercised BEFORE the rows, because the schema mapping
// is where geometry derived from the dictionary — a case stride, a case
// count, a decoded-size hint — is first used, and that is the shape of fault
// the corruption sweep actually found (a record 7/16 count large enough to
// overflow the decoded-size multiplication into a negative capacity). The
// seed corpus spans both byte orders and all three compression modes, since
// each derives its geometry differently. And the zero-length input is no
// longer skipped: NewReaderFromBytes(nil) reports PULSE_SPSS_FILE_EMPTY now,
// so it is an ordinary member of the invariant rather than an exception to it.
func FuzzReadRows(f *testing.F) {
	seeds := []spsstest.Spec{
		spsstest.ReferenceSpec(),
		spsstest.ExtensionReferenceSpec(),
		{Vars: []spsstest.Var{{Name: "A"}}, Cases: [][]spsstest.Value{{spsstest.Num(1)}}},
		{
			Vars: []spsstest.Var{{Name: "WIDE", Width: 30}, {Name: "N"}},
			Cases: [][]spsstest.Value{
				{spsstest.Text("x"), spsstest.SysMis()},
				{spsstest.Text(strings.Repeat("y", 30)), spsstest.Num(-1)},
			},
		},
	}
	for _, bo := range []spsstest.ByteOrder{spsstest.LittleEndian, spsstest.BigEndian} {
		for _, c := range []spsstest.Compression{
			spsstest.CompressionNone, spsstest.CompressionBytecode, spsstest.CompressionZSAV,
		} {
			seeds = append(seeds, endianTwinSpec(bo, c))
		}
	}
	for _, spec := range seeds {
		b, err := spsstest.Build(spec)
		if err != nil {
			f.Fatalf("spsstest.Build: %v", err)
		}
		f.Add(b)
	}
	f.Add([]byte(nil))
	f.Add(make([]byte, headerSize))

	f.Fuzz(func(t *testing.T, in []byte) {
		r := NewReaderFromBytes(in)
		if _, err := r.PulseSchema(); err != nil {
			assertSPSSCoded(t, err, len(in))
			return
		}
		cols, err := r.ReadHeader()
		if err != nil {
			assertSPSSCoded(t, err, len(in))
			return
		}
		err = r.ReadRows(context.Background(), func(row []string) error {
			if len(row) != len(cols) {
				t.Fatalf("row has %d value(s) but the header has %d column(s)", len(row), len(cols))
			}
			return nil
		})
		if err != nil {
			assertSPSSCoded(t, err, len(in))
		}
	})
}

// TestReadRows_EmptyInputIsCoded pins the zero-byte input on both
// constructors. E2-S4 left NewReaderFromBytes(nil) reporting "no source
// configured" — a plain error a caller could not switch on, and one that
// described the READER rather than the file. Both paths now report
// PULSE_SPSS_FILE_EMPTY, which is a claim about the source and is distinct
// from PULSE_SPSS_DICT_TRUNCATED: a truncated file stopped part way through
// a record, an empty one never had a first record.
func TestReadRows_EmptyInputIsCoded(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T) *Reader
	}{
		{"nil byte slice", func(*testing.T) *Reader { return NewReaderFromBytes(nil) }},
		{"empty byte slice", func(*testing.T) *Reader { return NewReaderFromBytes([]byte{}) }},
		{"zero-length file", func(t *testing.T) *Reader {
			cfg := fs.NewMemMap()
			if err := afero.WriteFile(cfg.Fs(), "empty.sav", nil, 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			return NewReader(cfg.Fs(), "empty.sav")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.make(t).ReadRows(context.Background(), func([]string) error { return nil })
			if err == nil {
				t.Fatal("an empty source read without error")
			}
			ce := codedError(t, err)
			if ce.Code != perr.PULSE_SPSS_FILE_EMPTY {
				t.Fatalf("code = %s, want %s (%v)", ce.Code, perr.PULSE_SPSS_FILE_EMPTY, err)
			}
			assertDetails(t, ce, 0)
		})
	}
}

// TestNewReader_NoSourceIsCoded covers the one shape that is a caller fault
// rather than a file fault: a Reader with neither a filesystem nor bytes.
// It is still a coded error, so every failure out of this package is one a
// caller can switch on.
func TestNewReader_NoSourceIsCoded(t *testing.T) {
	err := (&Reader{}).ReadRows(context.Background(), func([]string) error { return nil })
	if err == nil {
		t.Fatal("a sourceless reader read without error")
	}
	ce := codedError(t, err)
	if ce.Code != perr.DATA_FILE {
		t.Fatalf("code = %s, want %s (%v)", ce.Code, perr.DATA_FILE, err)
	}
}

// assertSPSSCoded asserts an error is a coded member of the PULSE_SPSS_*
// family carrying an in-range offset. A caller that cannot switch on the code
// cannot tell "this is not a system file" from "this transfer was cut short".
//
// It delegates the family membership and the offset range check to
// assertHardeningCoded, so there is one list of codes the reader is allowed
// to fail with rather than two that can drift apart. The extra obligation it
// adds is the byte offset in the MESSAGE: everything reaching this helper
// came from a byte-addressed read, so a message that does not say where is a
// diagnostic an operator cannot act on.
func assertSPSSCoded(t *testing.T, err error, size int) {
	t.Helper()
	assertHardeningCoded(t, err, size, 0, 0)
	ce := codedError(t, err)
	if !strings.Contains(ce.Message, "byte offset") &&
		ce.Code != perr.PULSE_SPSS_CHARSET_UNSUPPORTED &&
		ce.Code != perr.PULSE_SPSS_CHARSET_INVALID {
		t.Errorf("message %q does not name a byte offset", ce.Message)
	}
}
