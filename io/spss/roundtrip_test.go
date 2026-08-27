package spss

// THE ACCEPTANCE GATE for the whole `.sav` effort.
//
// Every other test in this package asserts one piece: that the bytecode
// stream decodes, that a record 7/22 binds, that a derived column folds.
// This file asserts the CLAIM those pieces were built to support — that a
// `.sav` can be read into a cohort, written back out, and read again without
// anything the source declared having quietly changed.
//
// # What "semantically identical" means here, and why
//
// The strongest claim available would be byte-identity of the emitted `.sav`
// against the source. It is deliberately NOT the invariant, because it is
// false for reasons that are correct:
//
//   - The header's prod_name identifies the program that wrote THESE bytes.
//     Claiming the source's identity would be false provenance
//     (TestBuildDictionary_ProvenanceFieldsDescribeTheseBytes).
//   - The file is always written little-endian, whatever the source was
//     (TestBuildDictionary_ByteOrderIsAlwaysLittleEndian), and always with
//     the conventional compression bias.
//   - E5-S2 emits one 3/4 pair per variable rather than sharing one record 3
//     across several variables, so a source that shared them cannot match.
//   - Record ORDER inside the type 7 block is this writer's, not the
//     source's.
//
// None of those carry a datum. So the invariant asserted here is COHORT
// identity: import(export(import(x))) is byte-identical to import(x), for
// the `.pulse` file itself — schema block, dictionary blocks, record data
// and null bitmap. That is the strongest statement that can be true, and it
// is a strong one: the `.pulse` byte layout is fixed-stride and
// deterministic, so a single field type, dictionary entry, ID, null bit or
// datum moving anywhere in the cohort changes the bytes.
//
// Two byte-level claims ARE made on the `.sav` side, and both are asserted:
//
//   - The emitted file is a FIXED POINT. Exporting the re-imported cohort a
//     second time produces the same `.sav` bytes as the first export. A
//     cycle that drifted by a little each pass would satisfy a
//     one-round-trip cohort check and still be lossy.
//   - The emitted file declares EXACTLY the source's own variables, in the
//     source's own order, with the source's own missing specifications.
//
// # No case is skipped
//
// A skipped row here is a fidelity claim that cannot be made. Every axis
// FR-62 names is in the matrix below and TestRoundTrip_MatrixCoversFR62
// fails if one is dropped, so a row cannot be quietly removed to make the
// gate green.
//
// # The known divergences, asserted rather than avoided
//
// Six behaviours legitimately make the re-imported cohort differ from a
// NAIVE expectation of the source. Each is a recorded decision, and
// TestRoundTrip_KnownDivergencesAreTheExpectedBehaviour asserts each one as
// the behaviour it is, so a silent reversion fails here:
//
//  1. Trailing spaces are trimmed on read and re-padded to the retained
//     declared width on write.
//  2. A pre-1970 (or sub-day) day-resolution column widens to `datetime`.
//  3. MOYR / QYR / WKYR stay `f64` raw seconds with the format code retained.
//  4. A DATETIME carrying fractional seconds routes to `f64` raw seconds.
//  5. Derived columns exist in the cohort and must not reach the `.sav`.
//  6. `--ignore-sidecar` cannot round-trip a cohort carrying a derived
//     `set_*` column, and says so rather than emitting a wrong file.

import (
	"context"
	"encoding/binary"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// The matrix
// ---------------------------------------------------------------------------

// The axes FR-62 names. They are constants because
// TestRoundTrip_MatrixCoversFR62 tallies them: a row that drops its tag, or
// an axis value that loses its last row, fails that test rather than
// silently narrowing the gate.
const (
	axisEncodingNone     = "encoding/uncompressed"
	axisEncodingBytecode = "encoding/bytecode"
	axisEncodingZSAV     = "encoding/zsav"

	axisMRDichotomy = "mrset/dichotomy"
	axisMRCategory  = "mrset/category"

	axisMissingDiscrete = "missing/discrete"
	axisMissingRange    = "missing/range"
	axisMissingRangeSc  = "missing/range_plus_discrete"

	axisCharsetNonUTF8 = "charset/non-utf8"
	axisVeryLongString = "string/very-long"

	axisLittleEndian = "byteorder/little"
	axisBigEndian    = "byteorder/big"
)

// fr62Axes is the full target. Every one of these must be carried by at
// least one row of the matrix.
var fr62Axes = []string{
	axisEncodingNone, axisEncodingBytecode, axisEncodingZSAV,
	axisMRDichotomy, axisMRCategory,
	axisMissingDiscrete, axisMissingRange, axisMissingRangeSc,
	axisCharsetNonUTF8, axisVeryLongString,
	axisLittleEndian, axisBigEndian,
}

// roundTripCase is one fixture and the FR-62 axes it carries.
type roundTripCase struct {
	name string
	spec spsstest.Spec
	axes []string
}

// kitchenSinkSpec is the fixture that carries most of the matrix in one
// file, because the interesting failures are INTERACTIONS: a very long
// string beside a response set beside a missing specification is where a
// element-index or a fold arithmetic mistake shows up, and a file per
// feature would never put them next to each other.
//
// It deliberately includes a numeric variable whose value labels sit ONLY on
// its user-missing codes (DISCRETE, labelled at 97 and 98). That is the
// shape whose labels do not travel through Variable.Categories at all — they
// live in the derived reason column — and it is the one case in this matrix
// that a Categories-only write path loses.
func kitchenSinkSpec() spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 8}
	return spsstest.Spec{
		CharacterEncoding: "UTF-8",
		FileLabel:         "Wave 3",
		Documents:         []string{"Fielded 2024-01", "Weighted to census"},
		DisplayParams:     true,
		FileAttributes:    "$@Survey('Wave 3')\n",
		VarAttributes:     "DISCRETE:$@Origin('core')\n",
		WeightVar:         "WT",
		Vars: []spsstest.Var{
			{
				Name: "DISCRETE", LongName: "income", Label: "Annual income",
				Measure: spsstest.MeasureScale, Print: num,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{
					spsstest.Num(97), spsstest.Num(98), spsstest.Num(99),
				}},
			},
			{
				Name: "RANGED", LongName: "age", Measure: spsstest.MeasureScale, Print: num,
				Missing: &spsstest.MissingValues{Range: &spsstest.MissingRange{Low: 900, High: 999}},
			},
			{
				Name: "RANGEDSC", LongName: "score", Measure: spsstest.MeasureScale, Print: num,
				Missing: &spsstest.MissingValues{
					Range:    &spsstest.MissingRange{Low: 90, High: 95},
					Discrete: []spsstest.Value{spsstest.Num(-1)},
				},
			},
			// A CODED numeric: its labels sit on ordinary values, so it maps
			// to a categorical column and its user-missing code 9 becomes a
			// dictionary entry flagged Missing rather than a sibling.
			{
				Name: "CODED", LongName: "satisfaction", Label: "Overall satisfaction",
				Measure: spsstest.MeasureOrdinal, Print: spsstest.Format{Type: spsstest.FormatF, Width: 1},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(9)}},
			},
			// A short string carrying a record type 2 string missing value,
			// and values that are SHORTER than the declared width, so the
			// trim-and-re-pad divergence is exercised.
			{
				Name: "CODE", Width: 6, Label: "Response code", Measure: spsstest.MeasureNominal,
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Text("REF")}},
			},
			{Name: "WT", Label: "Design weight", Measure: spsstest.MeasureScale,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 3}},
			// The temporal family, one variable per mapping arm.
			{Name: "WHEN", LongName: "signed_up", Print: spsstest.Format{Type: spsstest.FormatDATE, Width: 11}},
			{Name: "BORN", LongName: "born", Print: spsstest.Format{Type: spsstest.FormatADATE, Width: 10}},
			{Name: "SEEN", LongName: "last_seen", Print: spsstest.Format{Type: spsstest.FormatDATETIME, Width: 20}},
			{Name: "FRAC", LongName: "precise", Print: spsstest.Format{Type: spsstest.FormatDATETIME, Width: 22, Decimals: 2}},
			{Name: "ELAPSED", Print: spsstest.Format{Type: spsstest.FormatTIME, Width: 10}},
			{Name: "PERIOD", LongName: "fiscal_period", Print: spsstest.Format{Type: fmtMOYRFormat, Width: 6}},
			// The multiple-dichotomy members, which derive a set_* column.
			{Name: "MD1", Label: "Newspaper", Measure: spsstest.MeasureNominal,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 1}},
			{Name: "MD2", Label: "Radio", Measure: spsstest.MeasureNominal,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 1}},
			// The multiple-category members, which stay N categorical columns.
			{Name: "MC1", Label: "First choice", Measure: spsstest.MeasureNominal,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 1}},
			{Name: "MC2", Label: "Second choice", Measure: spsstest.MeasureNominal,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 1}},
			// A very long string: record 7/14 segmentation, plus 7/21 labels
			// and a 7/22 missing value it can only carry there.
			{Name: "NOTES", LongName: "comments", Width: 600, Label: "Open ended",
				Measure: spsstest.MeasureNominal},
		},
		ValueLabels: []spsstest.ValueLabelSet{
			{
				Vars: []string{"DISCRETE"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(97), Label: "Refused"},
					{Value: spsstest.Num(98), Label: "Don't know"},
				},
			},
			// Labels on values inside a RANGE specification: the range is
			// not a finite vocabulary, so these reasons are collected from
			// the data rather than enumerated from the specification, and
			// they are the harder half of the user-missing label path.
			{
				Vars: []string{"RANGED"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(950), Label: "Not asked"},
					{Value: spsstest.Num(999), Label: "No answer"},
				},
			},
			{
				Vars: []string{"CODED"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(1), Label: "Very dissatisfied"},
					{Value: spsstest.Num(5), Label: "Neutral"},
					{Value: spsstest.Num(7), Label: "Never observed"},
					{Value: spsstest.Num(9), Label: "Refused"},
				},
			},
			{
				Vars: []string{"MC1", "MC2"},
				Labels: []spsstest.ValueLabel{
					{Value: spsstest.Num(1), Label: "Acme"},
					{Value: spsstest.Num(2), Label: "Beta"},
					{Value: spsstest.Num(3), Label: "Gamma"},
				},
			},
		},
		LongStringValueLabels: []spsstest.LongStringValueLabels{{
			Var: "comments",
			Labels: []spsstest.LongStringValueLabel{
				{Value: "declined", Label: "Declined to answer"},
				{Value: "unused", Label: "A label no case carries"},
			},
		}},
		LongStringMissingValues: []spsstest.LongStringMissingValues{{
			Var: "comments", Values: []string{"declined"},
		}},
		MultipleResponseSets: []spsstest.MRSet{
			{
				Name: "$media", Kind: spsstest.MRDichotomy, Label: "Media used",
				CountedValue: "1", Vars: []string{"MD1", "MD2"}, Subtype: spsstest.SubtypeMRSets,
			},
			{
				Name: "$ranks", Kind: spsstest.MRCategory, Label: "Ranked brands",
				Vars: []string{"MC1", "MC2"}, Subtype: spsstest.SubtypeMRSets,
			},
		},
		VariableSets: []spsstest.VariableSet{{Name: "Demographics", Vars: []string{"CODE"}}},
		Cases: [][]spsstest.Value{
			{
				spsstest.Num(30000), spsstest.Num(41), spsstest.Num(72), spsstest.Num(1),
				spsstest.Text("AB"), spsstest.Num(1.5),
				spsstest.Num(savInstant(2024, 3, 4)), spsstest.Num(savInstant(1955, 6, 2)),
				spsstest.Num(savInstant(2024, 3, 4) + 3600), spsstest.Num(savInstant(2024, 3, 4) + 0.5),
				spsstest.Num(3661), spsstest.Num(13166064000),
				spsstest.Num(1), spsstest.Num(0), spsstest.Num(1), spsstest.Num(2),
				spsstest.Text(strings.Repeat("a", 300)),
			},
			{
				spsstest.Num(97), spsstest.Num(950), spsstest.Num(-1), spsstest.Num(5),
				spsstest.Text("REF"), spsstest.Num(0.5),
				spsstest.Num(savInstant(2020, 1, 1)), spsstest.Num(savInstant(1900, 12, 31)),
				spsstest.SysMis(), spsstest.Num(savInstant(2021, 5, 5) + 0.25),
				spsstest.Num(90061), spsstest.Num(13168742400),
				spsstest.Num(0), spsstest.Num(1), spsstest.Num(2), spsstest.Num(2),
				spsstest.Text("declined"),
			},
			{
				spsstest.Num(98), spsstest.Num(999), spsstest.Num(93), spsstest.Num(9),
				spsstest.Text("CD"), spsstest.Num(2.5),
				spsstest.Num(savInstant(2019, 7, 20)), spsstest.Num(savInstant(1969, 12, 31)),
				spsstest.Num(savInstant(2022, 11, 1) + 45296), spsstest.Num(savInstant(2022, 11, 1) + 0.75),
				spsstest.Num(0), spsstest.Num(13171334400),
				spsstest.Num(1), spsstest.Num(1), spsstest.Num(3), spsstest.Num(1),
				spsstest.Text(strings.Repeat("b", 600)),
			},
			{
				spsstest.Num(99), spsstest.SysMis(), spsstest.Num(91), spsstest.SysMis(),
				spsstest.Text(""), spsstest.Num(4.5),
				spsstest.Num(savInstant(2024, 12, 25)), spsstest.Num(savInstant(2001, 2, 3)),
				spsstest.Num(savInstant(2024, 12, 25) + 1), spsstest.SysMis(),
				spsstest.Num(59), spsstest.Num(13173926400),
				spsstest.Num(0), spsstest.Num(0), spsstest.Num(1), spsstest.Num(3),
				spsstest.Text("short"),
			},
			{
				spsstest.SysMis(), spsstest.Num(28), spsstest.SysMis(), spsstest.Num(7),
				spsstest.Text("EF"), spsstest.Num(5.5),
				spsstest.Num(savInstant(2023, 6, 15)), spsstest.Num(savInstant(1980, 8, 8)),
				spsstest.Num(savInstant(2023, 6, 15) + 7200), spsstest.Num(savInstant(2023, 6, 15) + 0.125),
				spsstest.Num(1), spsstest.Num(13176518400),
				spsstest.Num(1), spsstest.Num(0), spsstest.Num(2), spsstest.Num(3),
				spsstest.Text(strings.Repeat("c", 252)),
			},
		},
	}
}

// fmtMOYRFormat is the MOYR format code. It has no [spsstest.FormatType]
// constant because nothing in the emitter dispatches on it — it is a code
// this reader retains verbatim and never interprets, which is exactly the
// property the round trip is checking.
const fmtMOYRFormat spsstest.FormatType = 28

// savInstant renders a calendar date as the SPSS second count fixture
// authors write.
func savInstant(y, mo, d int) float64 {
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
	return float64(t.Unix() + spsstest.SPSSEpochOffsetSeconds)
}

// nonUTF8KitchenSink is the kitchen sink written in windows-1252, with
// non-ASCII text in every text-bearing slot the codepage can express.
//
// The very long string comes OUT: a 600-byte value is a byte width, and
// widening it under a variable-width transcode is a separate question that
// TestCharset* already owns. What this row is for is proving the whole
// dictionary survives a non-UTF-8 cycle.
func nonUTF8KitchenSink() spsstest.Spec {
	s := kitchenSinkSpec()
	s.CharacterEncoding = "windows-1252"
	s.FileLabel = "Enquête 2024"
	s.Documents = []string{"Collecté à Genève"}
	s.Vars[0].Label = "Revenu annuel"
	s.Vars[3].Label = "Satisfaction générale"
	s.ValueLabels[0].Labels[0].Label = "Refusé"
	s.ValueLabels[1].Labels[0].Label = "Très insatisfait"
	return s
}

// bigEndian restates a spec as a big-endian file, including the record 7/3
// endianness field, which must agree or the file contradicts itself.
func bigEndian(s spsstest.Spec) spsstest.Spec {
	s.ByteOrder = spsstest.BigEndian
	mi := spsstest.DefaultMachineIntegerInfoFor(spsstest.BigEndian)
	s.MachineIntegerInfo = &mi
	return s
}

func compressed(s spsstest.Spec, c spsstest.Compression) spsstest.Spec {
	s.Compression = c
	return s
}

// roundTripMatrix is the fixture matrix. Every FR-62 axis appears; the
// kitchen sink carries most of them at once and the remaining rows vary one
// axis of it at a time, which is what makes a failure attributable.
func roundTripMatrix() []roundTripCase {
	sink := []string{
		axisMRDichotomy, axisMRCategory,
		axisMissingDiscrete, axisMissingRange, axisMissingRangeSc,
		axisVeryLongString,
	}
	with := func(extra ...string) []string {
		return append(append([]string(nil), sink...), extra...)
	}
	return []roundTripCase{
		{"kitchen-sink/uncompressed", kitchenSinkSpec(), with(axisEncodingNone, axisLittleEndian)},
		{"kitchen-sink/bytecode", compressed(kitchenSinkSpec(), spsstest.CompressionBytecode), with(axisEncodingBytecode, axisLittleEndian)},
		{"kitchen-sink/zsav", compressed(kitchenSinkSpec(), spsstest.CompressionZSAV), with(axisEncodingZSAV, axisLittleEndian)},
		{"kitchen-sink/big-endian", bigEndian(kitchenSinkSpec()), with(axisEncodingNone, axisBigEndian)},
		{"kitchen-sink/big-endian-bytecode", bigEndian(compressed(kitchenSinkSpec(), spsstest.CompressionBytecode)), with(axisEncodingBytecode, axisBigEndian)},
		{"kitchen-sink/big-endian-zsav", bigEndian(compressed(kitchenSinkSpec(), spsstest.CompressionZSAV)), with(axisEncodingZSAV, axisBigEndian)},
		{"windows-1252", nonUTF8KitchenSink(), []string{axisCharsetNonUTF8, axisEncodingNone, axisLittleEndian}},
		{"windows-1252/zsav", compressed(nonUTF8KitchenSink(), spsstest.CompressionZSAV), []string{axisCharsetNonUTF8, axisEncodingZSAV, axisLittleEndian}},
		{"windows-1252/big-endian", bigEndian(nonUTF8KitchenSink()), []string{axisCharsetNonUTF8, axisEncodingNone, axisBigEndian}},

		// The narrow fixtures the rest of the package already reasons
		// about, carried through the whole cycle so a regression shows up
		// against a shape a human has looked at.
		{"reference", spsstest.ReferenceSpec(), []string{axisEncodingNone, axisLittleEndian}},
		{"every-extension-record", spsstest.ExtensionReferenceSpec(), []string{axisEncodingNone, axisLittleEndian, axisMRDichotomy, axisMRCategory}},
		{"every-bytecode-command", everyCommandSpec(), []string{axisEncodingBytecode, axisLittleEndian}},
		{"multi-block-zsav", multiBlockSpec(), []string{axisEncodingZSAV, axisLittleEndian}},
		{"latin-1", latin1Spec(), []string{axisCharsetNonUTF8, axisEncodingNone, axisLittleEndian}},
		{"missing-three-shapes", missingFixtureSpec(), []string{axisMissingDiscrete, axisMissingRange, axisMissingRangeSc, axisEncodingNone, axisLittleEndian}},
		{"categorical-user-missing", categoricalMissingSpec(), []string{axisMissingDiscrete, axisEncodingNone, axisLittleEndian}},
		{"multiple-dichotomy", mdSpec(), []string{axisMRDichotomy, axisEncodingNone, axisLittleEndian}},
		{"multiple-category", mcSpec(), []string{axisMRCategory, axisEncodingNone, axisLittleEndian}},
		{"both-derived-kinds", bothKindsSpec(), []string{axisMRDichotomy, axisMissingDiscrete, axisEncodingNone, axisLittleEndian}},
		{"dictionary-only", noCasesSpec(), []string{axisEncodingNone, axisLittleEndian}},
	}
}

// noCasesSpec is a legal `.sav` with a full dictionary and zero cases. It is
// its own row because a cycle that silently dropped the dictionary would
// still produce two equal EMPTY cohorts if the data section were the only
// thing compared.
func noCasesSpec() spsstest.Spec {
	s := kitchenSinkSpec()
	s.Cases = nil
	return s
}

// ---------------------------------------------------------------------------
// The cycle
// ---------------------------------------------------------------------------

// cycle is one complete import -> export -> import, plus the second export
// that proves the emitted file is a fixed point.
type cycle struct {
	fs afero.Fs

	source []byte // the fixture `.sav`
	first  []byte // cohort produced by importing source
	out    []byte // `.sav` emitted from first
	second []byte // cohort produced by importing out
	again  []byte // `.sav` emitted from second

	firstDoc  *Document
	secondDoc *Document
}

// runCycle performs the round trip through the SHARED pio jobs — the same
// dispatch `pulse import spss` and `pulse export spss` take — rather than
// driving the encoder directly. A mistake in the wiring is as much a
// fidelity loss as a mistake in the bytes, and only the job path sees it.
func runCycle(t *testing.T, spec spsstest.Spec, opts WriterOptions) *cycle {
	t.Helper()

	c := &cycle{fs: afero.NewMemMapFs(), source: build(t, spec)}
	if err := afero.WriteFile(c.fs, "source.sav", c.source, 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	c.first = importSav(t, c.fs, "source.sav", "first.pulse")
	c.firstDoc = readSidecar(t, c.fs, "first.pulse")

	c.out = exportSav(t, c.fs, "first.pulse", "out.sav", opts)
	c.second = importSav(t, c.fs, "out.sav", "second.pulse")
	c.secondDoc = readSidecar(t, c.fs, "second.pulse")

	c.again = exportSav(t, c.fs, "second.pulse", "again.sav", opts)
	return c
}

func importSav(t *testing.T, fs afero.Fs, sav, cohort string) []byte {
	t.Helper()
	job := pio.NewImportJob(NewReader(fs, sav), cohort)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("importing %s: %v", sav, err)
	}
	out, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading %s: %v", cohort, err)
	}
	return out
}

func exportSav(t *testing.T, fs afero.Fs, cohort, sav string, opts WriterOptions) []byte {
	t.Helper()
	w := NewWriter(fs, sav, opts)
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("exporting %s: %v", cohort, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the writer for %s: %v", sav, err)
	}
	out, err := afero.ReadFile(fs, sav)
	if err != nil {
		t.Fatalf("reading %s: %v", sav, err)
	}
	return out
}

// ---------------------------------------------------------------------------
// The property
// ---------------------------------------------------------------------------

// TestRoundTrip_CohortIsIdentical is the gate.
//
// For every fixture in the matrix and both emission modes: import, export,
// import — and the two cohorts must be the same bytes.
//
// The schema is diffed FIELD BY FIELD before the byte comparison, and that
// ordering is deliberate: "604 bytes versus 591" is not a diagnosis, and a
// gate whose failure message cannot be acted on gets weakened rather than
// fixed.
func TestRoundTrip_CohortIsIdentical(t *testing.T) {
	for _, tc := range roundTripMatrix() {
		for _, mode := range []struct {
			name string
			opts WriterOptions
		}{
			{"bytecode-out", WriterOptions{}},
			{"uncompressed-out", WriterOptions{Uncompressed: true}},
		} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				c := runCycle(t, tc.spec, mode.opts)

				diffSchemas(t,
					cohortSchema(t, c.fs, "first.pulse"),
					cohortSchema(t, c.fs, "second.pulse"))

				if !bytesEqual(c.first, c.second) {
					t.Errorf("the re-imported cohort is not the cohort that was exported: %d bytes in, %d bytes out",
						len(c.first), len(c.second))
				}

				// The emitted file is a fixed point. A cycle that lost a
				// little each pass would satisfy the check above on the
				// first pass and still be lossy.
				if !bytesEqual(c.out, c.again) {
					t.Errorf("the emitted .sav is not a fixed point: exporting the re-imported cohort gave %d bytes, want the same %d",
						len(c.again), len(c.out))
				}

				// The emitted file declares exactly the source's variables,
				// in the source's order. This is the derived-column check
				// and the nothing-was-dropped check at once.
				if got, want := savVariableNames(t, c.out), savVariableNames(t, c.source); !equalStrings(got, want) {
					t.Errorf("emitted variables %q, want the source's own %q", got, want)
				}
			})
		}
	}
}

// TestRoundTrip_MatrixCoversFR62 is the gate on the gate.
//
// The acceptance criterion is not "some fixtures round trip", it is that the
// matrix reaches FR-62's full target. Without this test a row could be
// dropped to make a failure go away and nothing would say the claim had
// narrowed.
func TestRoundTrip_MatrixCoversFR62(t *testing.T) {
	seen := map[string]int{}
	known := map[string]bool{}
	for _, a := range fr62Axes {
		known[a] = true
	}
	for _, tc := range roundTripMatrix() {
		if len(tc.axes) == 0 {
			t.Errorf("matrix row %q carries no axis tags", tc.name)
		}
		for _, a := range tc.axes {
			if !known[a] {
				t.Errorf("matrix row %q tags the unknown axis %q", tc.name, a)
			}
			seen[a]++
		}
	}
	for _, a := range fr62Axes {
		if seen[a] == 0 {
			t.Errorf("no round-trip fixture carries the axis %q; FR-62's target is not reached", a)
		}
	}
}

// ---------------------------------------------------------------------------
// The original numeric codes
// ---------------------------------------------------------------------------

// TestRoundTrip_OriginalNumericCodesSurvive is the criterion stated as the
// thing a label comparison cannot say.
//
// A user-missing code that came back as the system-missing sentinel would
// still produce a `<var>_missing` sibling on re-import, still carry a plausible
// reason text, and still compare equal to the source on every label. What it
// would NOT be is 97. So the assertion reads the DOUBLE out of the emitted
// file's data section, at the element the variable occupies, and compares it
// to the double the source declared.
//
// It is done for all three missing-spec shapes at once, because the range
// shapes are where the codes are not enumerated anywhere: a member of
// 900..999 is a code the specification never lists, so the ONLY record of it
// is the datum itself.
func TestRoundTrip_OriginalNumericCodesSurvive(t *testing.T) {
	for _, mode := range []struct {
		name string
		opts WriterOptions
	}{
		{"bytecode-out", WriterOptions{}},
		{"uncompressed-out", WriterOptions{Uncompressed: true}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			c := runCycle(t, kitchenSinkSpec(), mode.opts)

			// Per column, per case: the exact double the fixture declared.
			// SysMis rows are named explicitly so a sentinel that leaked
			// into a user-missing slot cannot pass as "some missing state".
			want := map[string][]float64{
				"DISCRETE": {30000, 97, 98, 99, math.Inf(-1)},
				"RANGED":   {41, 950, 999, math.Inf(-1), 28},
				"RANGEDSC": {72, -1, 93, 91, math.Inf(-1)},
				"CODED":    {1, 5, 9, math.Inf(-1), 7},
				"WT":       {1.5, 0.5, 2.5, 4.5, 5.5},
				"PERIOD":   {13166064000, 13168742400, 13171334400, 13173926400, 13176518400},
				"ELAPSED":  {3661, 90061, 0, 59, 1},
			}
			names := make([]string, 0, len(want))
			for k := range want {
				names = append(names, k)
			}
			sort.Strings(names)

			for _, name := range names {
				got := savColumn(t, c.out, name)
				for i, w := range want[name] {
					if math.IsInf(w, -1) {
						// The source wrote SysMis here. It must come back
						// as the sentinel and not as a user-missing code.
						if !isSysmis(got[i]) {
							t.Errorf("%s case %d = %v, want the system-missing sentinel", name, i, got[i])
						}
						continue
					}
					if got[i] != w {
						t.Errorf("%s case %d = %v, want the source's own %v", name, i, got[i], w)
					}
				}
			}

			// And the same file's own reader agrees the codes are still
			// DECLARED missing — a code written back into a variable that
			// no longer declares it missing is a value, not a missing state.
			d, err := parseDictionary(c.out)
			if err != nil {
				t.Fatalf("the emitted file does not parse: %v", err)
			}
			if v := savVar(t, d, "income"); !equalFloats(v.missing.numeric, []float64{97, 98, 99}) {
				t.Errorf("income declares missing %v, want [97 98 99]", v.missing.numeric)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The missing-value specification shapes
// ---------------------------------------------------------------------------

// TestRoundTrip_MissingSpecShapesSurvive checks the SHAPE, not just the
// values: the record type 2 n_missing_values field is the sign-carrying
// discriminant, and a range re-emitted as two discrete values would name the
// same two doubles while meaning something entirely different — 900 and 999
// missing, instead of everything between them.
//
// Both endiannesses are exercised because a numeric slot is a flt64 in the
// SOURCE file's byte order while the emitted file is always little-endian.
// A verbatim re-emission of a big-endian source's slots decodes here as an
// unrelated subnormal, so the variable silently stops declaring anything
// missing — which is precisely what a values-only assertion over a
// little-endian fixture cannot see.
func TestRoundTrip_MissingSpecShapesSurvive(t *testing.T) {
	for _, order := range []struct {
		name string
		spec spsstest.Spec
	}{
		{"little-endian", kitchenSinkSpec()},
		{"big-endian", bigEndian(kitchenSinkSpec())},
	} {
		t.Run(order.name, func(t *testing.T) {
			c := runCycle(t, order.spec, WriterOptions{})

			src, err := parseDictionary(c.source)
			if err != nil {
				t.Fatalf("the fixture does not parse: %v", err)
			}
			out, err := parseDictionary(c.out)
			if err != nil {
				t.Fatalf("the emitted file does not parse: %v", err)
			}

			for _, name := range []string{"income", "age", "score", "satisfaction", "CODE", "comments"} {
				a, b := savVar(t, src, name), savVar(t, out, name)
				if a.missing.code != b.missing.code {
					t.Errorf("%s: n_missing_values = %d, want the source's own %d (the sign IS the shape)",
						name, b.missing.code, a.missing.code)
				}
				if !equalFloats(a.missing.numeric, b.missing.numeric) {
					t.Errorf("%s: numeric missing slots = %v, want %v", name, b.missing.numeric, a.missing.numeric)
				}
				if !equalStrings(a.missing.text, b.missing.text) {
					t.Errorf("%s: string missing slots = %q, want %q", name, b.missing.text, a.missing.text)
				}
			}

			// The three shapes really are all three, in the emitted file.
			for name, wantCode := range map[string]int32{
				"income": 3,  // three discrete values
				"age":    -2, // a bare range
				"score":  -3, // a range plus one discrete value
			} {
				if got := savVar(t, out, name).missing.code; got != wantCode {
					t.Errorf("%s: emitted n_missing_values = %d, want %d", name, got, wantCode)
				}
			}
		})
	}
}

// TestRoundTrip_UserMissingLabelsSurvive is the half of the numeric
// user-missing round trip that the CODES alone do not cover.
//
// A numeric variable whose value labels sit only on its missing codes is not
// a coded variable, so the import maps it to a plain f64 and moves those
// labels into the `<var>_missing` sibling's reason dictionary — the only
// place they exist in the cohort. An export reading Variable.Categories
// alone therefore emits a file that still carries 97 but no longer says it
// means "Refused", and the re-imported reason column reads as bare numerals.
//
// The cohort-identity gate catches that, but only as a dictionary diff. This
// states it where it can be read.
func TestRoundTrip_UserMissingLabelsSurvive(t *testing.T) {
	c := runCycle(t, kitchenSinkSpec(), WriterOptions{})

	out, err := parseDictionary(c.out)
	if err != nil {
		t.Fatalf("the emitted file does not parse: %v", err)
	}
	want := map[float64]string{97: "Refused", 98: "Don't know"}
	got := map[float64]string{}
	for _, set := range out.valueLabels {
		named := false
		for _, idx := range set.varIndices {
			if savVarByIndex(out, idx) == "income" {
				named = true
			}
		}
		if !named {
			continue
		}
		for _, l := range set.labels {
			got[l.numeric(out.byteOrder)] = l.label
		}
	}
	for code, label := range want {
		if got[code] != label {
			t.Errorf("the emitted file labels income=%v as %q, want the source's own %q", code, got[code], label)
		}
	}

	// And the re-imported reason column shows the label rather than the
	// numeral, which is what a consumer of the cohort actually sees.
	f := fieldOf(t, cohortSchema(t, c.fs, "second.pulse"), "income_missing")
	if f.Dictionary == nil {
		t.Fatal("income_missing carries no dictionary")
	}
	values := f.Dictionary.Values()
	if !containsString(values, "Refused") || !containsString(values, "Don't know") {
		t.Errorf("income_missing = %q, want it to carry the source's own labels", values)
	}
}

// ---------------------------------------------------------------------------
// Derived columns
// ---------------------------------------------------------------------------

// TestRoundTrip_DerivedColumnsAreNotEmittedAndDoNotPerturb is the criterion
// in its two halves.
//
// They must not reach the file: an emitted `.sav` carrying `income_missing`
// or `media` as variables would be this reader's artefacts leaking into
// somebody else's data.
//
// And they must not perturb: the cohort the round trip produces has to carry
// the SAME derived registry the first import wrote, or the columns are being
// regenerated from something other than what generated them the first time.
func TestRoundTrip_DerivedColumnsAreNotEmittedAndDoNotPerturb(t *testing.T) {
	c := runCycle(t, kitchenSinkSpec(), WriterOptions{})

	// The first import really does synthesise both kinds — otherwise this
	// test would pass by having nothing to check.
	kinds := map[string]bool{}
	for _, d := range c.firstDoc.Payload.Derived {
		kinds[d.Kind] = true
	}
	for _, kind := range []string{DerivedKindNumericMissing, DerivedKindMultipleDichotomy} {
		if !kinds[kind] {
			t.Fatalf("the fixture produced no %q derived column; the test is not exercising what it claims (registry: %+v)",
				kind, c.firstDoc.Payload.Derived)
		}
	}

	emitted := savVariableNames(t, c.out)
	for _, d := range c.firstDoc.Payload.Derived {
		if containsFold(emitted, d.Name) {
			t.Errorf("the emitted file declares %q, which is a column this reader synthesised", d.Name)
		}
	}

	// The registry survives the cycle unchanged, entry for entry.
	before, after := c.firstDoc.Payload.Derived, c.secondDoc.Payload.Derived
	if len(before) != len(after) {
		t.Fatalf("the re-imported cohort has %d derived column(s), want %d: %+v vs %+v",
			len(after), len(before), after, before)
	}
	for i := range before {
		a, b := before[i], after[i]
		if a.Name != b.Name || a.Kind != b.Kind || a.SetName != b.SetName || a.Position != b.Position {
			t.Errorf("derived[%d] = {%s %s %s @%d}, want {%s %s %s @%d}",
				i, b.Name, b.Kind, b.SetName, b.Position, a.Name, a.Kind, a.SetName, a.Position)
		}
		if !equalStrings(a.Sources, b.Sources) {
			t.Errorf("derived[%d] %q sources = %q, want %q", i, a.Name, b.Sources, a.Sources)
		}
		if len(a.Reasons) != len(b.Reasons) {
			t.Errorf("derived[%d] %q has %d reason(s), want %d", i, a.Name, len(b.Reasons), len(a.Reasons))
			continue
		}
		for j := range a.Reasons {
			ar, br := a.Reasons[j], b.Reasons[j]
			if ar.ID != br.ID || ar.Reason != br.Reason || ar.Sysmis != br.Sysmis ||
				ar.Label != br.Label || ar.Declared != br.Declared || ar.Observed != br.Observed {
				t.Errorf("derived[%d] %q reason %d = %+v, want %+v", i, a.Name, j, br, ar)
				continue
			}
			if (ar.Code == nil) != (br.Code == nil) {
				t.Errorf("derived[%d] %q reason %d: code presence changed", i, a.Name, j)
			} else if ar.Code != nil && *ar.Code != *br.Code {
				t.Errorf("derived[%d] %q reason %d: code = %v, want %v", i, a.Name, j, *br.Code, *ar.Code)
			}
		}
	}
}

// TestRoundTrip_IgnoreSidecarRefusesRatherThanLosingTheSet is divergence 6
// asserted as behaviour rather than avoided.
//
// Suppressing the sidecar suppresses the derived registry, and without it
// the `set_*` column is indistinguishable from a real cohort field — so the
// export would emit it as an SPSS variable under a name a real variable
// already has. The refusal is the correct outcome and the same cohort must
// still export cleanly WITHOUT the flag, which is what makes the refusal a
// property of the flag and not of the cohort.
func TestRoundTrip_IgnoreSidecarRefusesRatherThanLosingTheSet(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "source.sav", build(t, kitchenSinkSpec()), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	importSav(t, fs, "source.sav", "first.pulse")

	w := NewWriter(fs, "ignored.sav", WriterOptions{IgnoreSidecar: true})
	job := pio.NewExportJob("first.pulse", w)
	job.FS = fs
	_, err := job.Run(context.Background())
	ce := codedErr(t, err)
	if ce.Code != perr.PULSE_SPSS_NAME_COLLISION {
		t.Errorf("code = %s, want %s", ce.Code, perr.PULSE_SPSS_NAME_COLLISION)
	}

	if got := exportSav(t, fs, "first.pulse", "kept.sav", WriterOptions{}); len(got) == 0 {
		t.Fatal("the same cohort must export without --ignore-sidecar")
	}
}

// ---------------------------------------------------------------------------
// The known divergences
// ---------------------------------------------------------------------------

// TestRoundTrip_KnownDivergencesAreTheExpectedBehaviour states each recorded
// divergence as an assertion.
//
// They are the places a naive "the cohort must look like the source"
// expectation is wrong. Writing them down here means a future change that
// reverts one fails a test that explains what it was for, rather than
// quietly restoring a defect the divergence was chosen to route around.
func TestRoundTrip_KnownDivergencesAreTheExpectedBehaviour(t *testing.T) {
	c := runCycle(t, kitchenSinkSpec(), WriterOptions{})
	first := cohortSchema(t, c.fs, "first.pulse")
	second := cohortSchema(t, c.fs, "second.pulse")

	t.Run("pre-1970 day resolution widens to datetime", func(t *testing.T) {
		// BORN carries 1955-06-02 and 1900-12-31, which an epoch-day
		// uint32 cannot hold. Both cohorts must agree it is a datetime —
		// the widening is not a first-pass-only accommodation.
		for _, s := range []*encoding.Schema{first, second} {
			if got := fieldOf(t, s, "born").Type; got != encoding.FieldTypeDateTime {
				t.Errorf("born = %s, want datetime", got)
			}
		}
		// And the sibling day-resolution column that has no pre-1970 value
		// stays a plain date, so the widening is per column and not global.
		if got := fieldOf(t, second, "signed_up").Type; got != encoding.FieldTypeDate {
			t.Errorf("signed_up = %s, want date", got)
		}
	})

	t.Run("MOYR stays f64 raw seconds with the format code retained", func(t *testing.T) {
		if got := fieldOf(t, second, "fiscal_period").Type; got != encoding.FieldTypeF64 {
			t.Errorf("fiscal_period = %s, want f64", got)
		}
		out, err := parseDictionary(c.out)
		if err != nil {
			t.Fatalf("the emitted file does not parse: %v", err)
		}
		if got := savVar(t, out, "fiscal_period").print.code; got != uint8(fmtMOYRFormat) {
			t.Errorf("emitted print format code = %d, want the source's own %d", got, fmtMOYRFormat)
		}
	})

	t.Run("a fractional DATETIME routes to f64 raw seconds", func(t *testing.T) {
		if got := fieldOf(t, second, "precise").Type; got != encoding.FieldTypeF64 {
			t.Errorf("precise = %s, want f64 — the datetime wire form is second resolution", got)
		}
		// The whole-second sibling is the control: it DOES become a datetime.
		if got := fieldOf(t, second, "last_seen").Type; got != encoding.FieldTypeDateTime {
			t.Errorf("last_seen = %s, want datetime", got)
		}
	})

	t.Run("trailing spaces are trimmed on read and re-padded on write", func(t *testing.T) {
		// CODE declares width 6 and every value is shorter. The cohort
		// holds the TRIMMED text, and the emitted file holds it padded
		// back out to 6 — which is why the source and the emitted file's
		// data sections agree byte for byte on that column while a naive
		// string comparison against the cohort would not.
		_, rows := savRows(t, c.out)
		for i, want := range []string{"AB", "REF", "CD", "", "EF"} {
			if got := rows[i][codeColumn(t, c.out)]; got != want {
				t.Errorf("case %d CODE = %q, want the trimmed %q", i, got, want)
			}
		}
		src, err := parseDictionary(c.source)
		if err != nil {
			t.Fatalf("the fixture does not parse: %v", err)
		}
		out, err := parseDictionary(c.out)
		if err != nil {
			t.Fatalf("the emitted file does not parse: %v", err)
		}
		if got, want := savVar(t, out, "CODE").width, savVar(t, src, "CODE").width; got != want {
			t.Errorf("emitted CODE width = %d, want the source's retained %d", got, want)
		}
		if got := savText(t, c.out, "CODE", 0); got != "AB    " {
			t.Errorf("the emitted CODE datum is %q, want it re-padded to the declared width", got)
		}
	})

	t.Run("derived columns are in the cohort and not in the file", func(t *testing.T) {
		if _, ok := fieldIndex(second, "income_missing"); !ok {
			t.Error("the re-imported cohort has no income_missing sibling")
		}
		if containsFold(savVariableNames(t, c.out), "income_missing") {
			t.Error("income_missing reached the emitted .sav")
		}
	})
}

// ---------------------------------------------------------------------------
// Reading the emitted file
// ---------------------------------------------------------------------------

// savVariableNames returns the FINAL names of a file's variables, in file
// order — the long name where one is declared, else the short name.
func savVariableNames(t *testing.T, sav []byte) []string {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing a .sav: %v", err)
	}
	out := make([]string, 0, len(d.vars))
	for _, v := range d.vars {
		out = append(out, v.fieldName())
	}
	return out
}

// savVar returns the named variable of a parsed dictionary, matched on the
// final name and then on the short name.
func savVar(t *testing.T, d *dictionary, name string) variable {
	t.Helper()
	for _, v := range d.vars {
		if strings.EqualFold(v.fieldName(), name) || strings.EqualFold(v.name, name) {
			return v
		}
	}
	t.Fatalf("the file declares no variable %q", name)
	return variable{}
}

// savVarByIndex resolves a record type 4 variable index to a final name.
func savVarByIndex(d *dictionary, idx int32) string {
	for _, v := range d.vars {
		if v.index == idx {
			return v.fieldName()
		}
	}
	return ""
}

// savColumn returns one NUMERIC variable's raw doubles, one per case,
// straight out of the emitted data section.
//
// It goes to the flat case bytes rather than through this package's own
// row reader on purpose: the row reader applies the missing specification
// and would hand back a null where a user-missing code sits, which is the
// one thing this assertion exists to look at.
func savColumn(t *testing.T, sav []byte, name string) []float64 {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing a .sav: %v", err)
	}
	v := savVar(t, d, name)
	if v.width != 0 {
		t.Fatalf("%q is a string variable; savColumn reads numerics", name)
	}
	flat := flatCases(t, sav)
	stride := int(d.elementCount) * elementSize
	at := int(v.index-1) * elementSize

	out := make([]float64, 0, len(flat)/stride)
	for off := 0; off+stride <= len(flat); off += stride {
		out = append(out, math.Float64frombits(
			binary.LittleEndian.Uint64(flat[off+at:off+at+elementSize])))
	}
	return out
}

// savText returns one string variable's raw, still-padded bytes for one case.
func savText(t *testing.T, sav []byte, name string, at int) string {
	t.Helper()
	d, err := parseDictionary(sav)
	if err != nil {
		t.Fatalf("parsing a .sav: %v", err)
	}
	v := savVar(t, d, name)
	flat := flatCases(t, sav)
	stride := int(d.elementCount) * elementSize
	off := at*stride + int(v.index-1)*elementSize
	if off+v.width > len(flat) {
		t.Fatalf("case %d of %q is past the end of the data section", at, name)
	}
	return string(flat[off : off+v.width])
}

// codeColumn is the row-reader position of the CODE column, resolved from
// the header rather than hardcoded: the derived siblings shift every
// position after them, so a literal index would silently drift.
func codeColumn(t *testing.T, sav []byte) int {
	t.Helper()
	head, _ := savRows(t, sav)
	for i, h := range head {
		if h == "CODE" {
			return i
		}
	}
	t.Fatalf("the emitted file has no CODE column: %q", head)
	return -1
}

// isSysmis reports whether a double is the system-missing sentinel.
func isSysmis(v float64) bool { return v == defaultSysmis }

// ---------------------------------------------------------------------------
// Schema diffing
// ---------------------------------------------------------------------------

// diffSchemas reports every way two cohort schemas differ, rather than
// stopping at the first. A round-trip failure is usually one systematic
// mistake showing up in several columns, and seeing all of them is what
// tells them apart from several unrelated ones.
func diffSchemas(t *testing.T, want, got *encoding.Schema) {
	t.Helper()
	if len(want.Fields) != len(got.Fields) {
		t.Errorf("the re-imported cohort has %d field(s), want %d\n  in:  %q\n  out: %q",
			len(got.Fields), len(want.Fields), fieldNames(want), fieldNames(got))
	}
	n := len(want.Fields)
	if len(got.Fields) < n {
		n = len(got.Fields)
	}
	for i := 0; i < n; i++ {
		a, b := want.Fields[i], got.Fields[i]
		if a.Name != b.Name {
			t.Errorf("field %d name = %q, want %q", i, b.Name, a.Name)
		}
		if a.Type != b.Type {
			t.Errorf("field %d (%s) type = %s, want %s", i, a.Name, b.Type, a.Type)
		}
		if a.Nullable != b.Nullable {
			t.Errorf("field %d (%s) nullable = %v, want %v", i, a.Name, b.Nullable, a.Nullable)
		}
		if a.Description != b.Description {
			t.Errorf("field %d (%s) description = %q, want %q", i, a.Name, b.Description, a.Description)
		}
		switch {
		case (a.Dictionary == nil) != (b.Dictionary == nil):
			t.Errorf("field %d (%s): dictionary presence changed", i, a.Name)
		case a.Dictionary != nil:
			av, bv := a.Dictionary.Values(), b.Dictionary.Values()
			if !equalStrings(av, bv) {
				t.Errorf("field %d (%s) dictionary = %q, want %q", i, a.Name, bv, av)
			}
		}
	}
}

func fieldNames(s *encoding.Schema) []string {
	out := make([]string, 0, len(s.Fields))
	for _, f := range s.Fields {
		out = append(out, f.Name)
	}
	return out
}

func fieldIndex(s *encoding.Schema, name string) (int, bool) {
	for i, f := range s.Fields {
		if strings.EqualFold(f.Name, name) {
			return i, true
		}
	}
	return 0, false
}

func bytesEqual(a, b []byte) bool { return string(a) == string(b) }

func equalFloats(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsFold(hay []string, needle string) bool {
	for _, s := range hay {
		if strings.EqualFold(s, needle) {
			return true
		}
	}
	return false
}
