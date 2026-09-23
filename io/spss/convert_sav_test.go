package spss

// `pulse convert survey.sav out.sav` — the convert path's SPSS fidelity.
//
// The `.sav` writer's ROW path rebuilds a cohort from rendered text, because
// a `.sav` value is derived from things a row no longer carries. Until the
// pio.ConvertSource channel existed it rebuilt that cohort from the text
// ALONE, and two facts the source had declared were lost on the way:
//
//  1. The metadata sidecar — the only home of the code / label /
//     dictionary-ID triple and of the DERIVED-column registry. Without it
//     the writer synthesised a default dictionary, expanded a
//     multiple-dichotomy `set_*` column into member variables of its own,
//     and every one of those names collided with the constituent column
//     already in the cohort. `pulse convert x.sav out.sav` refused ANY
//     multiple-dichotomy cohort with PULSE_SPSS_NAME_COLLISION. A
//     value-labelled numeric fared worse than that: it was re-derived from
//     cell text and emitted as a STRING variable with no value labels —
//     a file that opens cleanly in SPSS and has lost its codes.
//  2. The declared schema. A `set_*` column round-trips its selections as
//     "V003|V070" tokens, so a re-inferred rung is only as wide as the
//     tokens that happened to be TICKED — a 206-member battery whose
//     respondents chose eleven distinct options re-infers narrow, and every
//     unticked member disappears from the dictionary.
//
// Both are asserted here, and both at two widths where the claim is a
// width-blind one, per the rule E3-S4 set: a claim that something does not
// depend on width is worth nothing asserted at one width.

import (
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// convertSav runs `pulse convert src -> dst` exactly as internal/cli does:
// one ConvertJob, then Close, then the target's warnings read AFTER Close
// because the row path encodes there.
func convertSav(t *testing.T, fs afero.Fs, src, dst string, opts WriterOptions) ([]byte, []*perr.CodedError) {
	t.Helper()
	w := NewWriter(fs, dst, opts)
	job := pio.NewConvertJob(NewReader(fs, src), w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("converting %s -> %s: %v", src, dst, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("closing the .sav writer for %s: %v", dst, err)
	}
	out, err := afero.ReadFile(fs, dst)
	if err != nil {
		t.Fatalf("reading %s: %v", dst, err)
	}
	return out, w.Warnings()
}

// ---------------------------------------------------------------------------
// The business case: a multiple-dichotomy battery converts
// ---------------------------------------------------------------------------

// TestConvert_SavToSav_MultipleDichotomySurvivesAtTwoWidths is the story:
// the same respondents select the same labels after a `.sav` -> `.sav`
// convert, at a narrow rung and at 206 constituents.
//
// The reference is a DIRECT import of the source, not a written-down
// expectation: convert must land where `pulse import` would, and the masks
// are read out of the `.pulse` bytes on both sides because a mask compared
// through a rendered cell cannot show a dropped high bit.
func TestConvert_SavToSav_MultipleDichotomySurvivesAtTwoWidths(t *testing.T) {
	for _, members := range []int{3, 206} {
		t.Run(itoa(members), func(t *testing.T) {
			want, derives := pio.SetTypeFor(members)
			if !derives {
				t.Fatalf("%d constituents do not derive a column; the case is not the one being tested", members)
			}

			fs := afero.NewMemMapFs()
			if err := afero.WriteFile(fs, "source.sav", build(t, wideSelectionSpec(members)), 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			// The reference: what `pulse import source.sav` produces.
			importSav(t, fs, "source.sav", "direct.pulse")
			if got := cohortSchema(t, fs, "direct.pulse").Field("wide"); got == nil || got.Type != want {
				t.Fatalf("the directly imported wide field = %+v, want %s", got, want)
			}

			out, _ := convertSav(t, fs, "source.sav", "out.sav", WriterOptions{})

			// The derived column is folded away and every constituent is
			// rebuilt under its own name, in order — the same claim the
			// export path makes, now reached through convert.
			emitted := savVariableNames(t, out)
			if containsFold(emitted, "wide") {
				t.Errorf("the converted file declares %q, which is a synthesised column", "wide")
			}
			if len(emitted) != members {
				t.Fatalf("the converted file declares %d variable(s), want %d — one per constituent and nothing else",
					len(emitted), members)
			}
			for i := 0; i < members; i++ {
				if emitted[i] != wideMemberName(i) {
					t.Fatalf("variable %d = %q, want %q", i, emitted[i], wideMemberName(i))
				}
			}

			// Re-import, and compare against the direct import field for
			// field and record for record.
			importSav(t, fs, "out.sav", "second.pulse")
			second := cohortSchema(t, fs, "second.pulse").Field("wide")
			if second == nil || second.Type != want {
				t.Fatalf("the re-imported wide field = %+v, want %s", second, want)
			}
			direct := cohortSchema(t, fs, "direct.pulse").Field("wide")
			if !equalStrings(direct.Dictionary.Values(), second.Dictionary.Values()) {
				t.Fatalf("the dictionary did not survive the convert cycle")
			}

			before, beforeNull := readCohortSetColumn(t, fs, "direct.pulse", "wide")
			after, afterNull := readCohortSetColumn(t, fs, "second.pulse", "wide")
			if len(before) != len(after) {
				t.Fatalf("the cycle produced %d record(s), want %d", len(after), len(before))
			}
			for i := range before {
				if beforeNull[i] != afterNull[i] {
					t.Errorf("row %d null = %v, want %v", i, afterNull[i], beforeNull[i])
					continue
				}
				if !beforeNull[i] && !before[i].Equal(after[i]) {
					t.Errorf("row %d selected %v, want %v", i, setBits(after[i]), setBits(before[i]))
				}
			}
			// Row 2 answered the battery and ticked nothing: an EMPTY mask
			// is a valid selection and must not come back as null.
			if afterNull[2] || after[2].PopCount() != 0 {
				t.Errorf("row 2 = %v (null %v), want an empty mask", setBits(after[2]), afterNull[2])
			}
		})
	}
}

// TestConvert_SavToSav_CarriesTheSidecarRatherThanSynthesising pins the
// MECHANISM behind the test above, so a future change that makes the
// collision go away by some other route (renaming the synthesised members,
// say) does not pass for the fix.
//
// A converted `.sav` must not raise PULSE_SPSS_SIDECAR_ABSENT: that warning
// is the row path telling the caller it is working from a synthesised
// default dictionary, which for a `.sav` source means the value codes and
// the derived registry were thrown away and re-guessed.
func TestConvert_SavToSav_CarriesTheSidecarRatherThanSynthesising(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "source.sav", build(t, wideSelectionSpec(206)), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	_, warnings := convertSav(t, fs, "source.sav", "out.sav", WriterOptions{})

	for _, w := range warnings {
		if w.Code == perr.PULSE_SPSS_SIDECAR_ABSENT {
			t.Fatalf("the convert raised %s (%s); the source carries a sidecar and it must reach the "+
				"intermediate cohort", w.Code, w.Message)
		}
	}
}

// TestConvert_SavToSav_ValueLabelledNumericKeepsItsCodes is the other half
// of what the sidecar carries, and the one that used to fail SILENTLY in
// the narrow case rather than loudly.
//
// GRADE is a numeric variable with value labels, so the cohort holds SPSS
// CODES in its dictionary and the sidecar holds the code / label /
// dictionary-ID triple. Re-derived from cell text it came out as a string
// variable full of "1" / "2" / "3" with no labels at all — a perfectly
// well-formed `.sav` that has lost the variable's type and its codebook.
//
// The case order (3, 2, 1, 3) is deliberately NOT the declared code order:
// a first-seen dictionary would be [3 2 1], so a carried sidecar read
// against a re-inferred dictionary would attach every label to the wrong
// code.
func TestConvert_SavToSav_ValueLabelledNumericKeepsItsCodes(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "source.sav", build(t, valueLabelledGradeSpec()), 0o644); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
	out, _ := convertSav(t, fs, "source.sav", "out.sav", WriterOptions{})

	// Still numeric, still carrying the codes in case order.
	if got := savColumn(t, out, "GRADE"); len(got) != 4 ||
		got[0] != 3 || got[1] != 2 || got[2] != 1 || got[3] != 3 {
		t.Fatalf("GRADE = %v, want [3 2 1 3] — the codes, in case order", got)
	}

	// And the codebook: every label on its own code.
	want := map[float64]string{1: "Low", 2: "Mid", 3: "High"}
	got := map[float64]string{}
	d, err := parseDictionary(out)
	if err != nil {
		t.Fatalf("parsing the converted file: %v", err)
	}
	for _, set := range d.valueLabels {
		for _, l := range set.labels {
			got[math.Float64frombits(binary.LittleEndian.Uint64(l.raw[:]))] = l.label
		}
	}
	if len(got) != len(want) {
		t.Fatalf("the converted file declares %d value label(s), want %d: %v", len(got), len(want), got)
	}
	for code, label := range want {
		if got[code] != label {
			t.Errorf("code %v is labelled %q, want %q", code, got[code], label)
		}
	}
}

// valueLabelledGradeSpec is one value-labelled numeric whose cases visit
// its codes in an order first-seen inference would not reproduce.
func valueLabelledGradeSpec() spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	return spsstest.Spec{
		Vars: []spsstest.Var{{Name: "GRADE", Print: num}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(3)}, {spsstest.Num(2)}, {spsstest.Num(1)}, {spsstest.Num(3)},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"GRADE"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Low"},
				{Value: spsstest.Num(2), Label: "Mid"},
				{Value: spsstest.Num(3), Label: "High"},
			},
		}},
	}
}

// ---------------------------------------------------------------------------
// The declared width
// ---------------------------------------------------------------------------

// TestConvert_RowPath_KeepsTheDeclaredSetRung is Decision 2 stated where it
// is observable: the rung and the dictionary the SOURCE declared, not the
// one the ticked tokens need.
//
// The writer is driven exactly as ConvertJob drives it — SetConvertSource,
// WriteHeader, WriteRow, Close — with NO sidecar, so the only thing under
// test is the declared schema. The cohort's set column is therefore
// synthesised on the way out, which is what makes the width visible: the
// expansion emits one member variable per DICTIONARY ENTRY, so a preserved
// 206-entry set_u256 emits 206 variables and a re-inferred one emits only
// as many as the rows ever ticked.
//
// The control arm runs the identical rows through a writer that was told
// nothing, and asserts it does NOT reach 206 — so the assertion above is
// the carry's doing rather than the fixture's.
func TestConvert_RowPath_KeepsTheDeclaredSetRung(t *testing.T) {
	for _, members := range []int{3, 206} {
		t.Run(itoa(members), func(t *testing.T) {
			declared, ok := pio.SetTypeFor(members)
			if !ok {
				t.Fatalf("%d members has no set rung", members)
			}
			names := make([]string, members)
			for i := range names {
				names[i] = wideMemberName(i)
			}

			// Only the first two members are ever ticked, so inference sees
			// two tokens where the source declared %d.
			rows := [][]any{
				{names[0] + "|" + names[1]},
				{names[0]},
				{pio.EmptySetCell},
			}
			if inferred, _ := pio.SetTypeFor(2); inferred == declared && members != 3 {
				t.Fatalf("fixture drift: two observed tokens infer %s, the rung the source declared", inferred)
			}

			schema := &encoding.Schema{Fields: []encoding.Field{
				{Name: "wide", Type: declared, CsvColumnIdx: 0, Dictionary: dictOf(t, names...)},
			}}

			w := NewWriterToBuffer(WriterOptions{})
			w.SetConvertSource(pio.ConvertSource{Schema: schema})
			if err := w.WriteHeader([]string{"wide"}); err != nil {
				t.Fatalf("WriteHeader: %v", err)
			}
			for _, r := range rows {
				if err := w.WriteRow(r); err != nil {
					t.Fatalf("WriteRow: %v", err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			emitted := savVariableNames(t, w.Bytes())
			if len(emitted) != members {
				t.Fatalf("the converted file declares %d variable(s), want %d — one per declared dictionary "+
					"entry, including every member no respondent ticked", len(emitted), members)
			}
			for i := range names {
				if emitted[i] != names[i] {
					t.Fatalf("variable %d = %q, want %q; member order is the dictionary's bit order",
						i, emitted[i], names[i])
				}
			}

			// The control: same rows, nothing declared.
			ctl := NewWriterToBuffer(WriterOptions{})
			if err := ctl.WriteHeader([]string{"wide"}); err != nil {
				t.Fatalf("control WriteHeader: %v", err)
			}
			for _, r := range rows {
				if err := ctl.WriteRow(r); err != nil {
					t.Fatalf("control WriteRow: %v", err)
				}
			}
			if err := ctl.Close(); err != nil {
				t.Fatalf("control Close: %v", err)
			}
			if got := len(savVariableNames(t, ctl.Bytes())); got == members && members != 2 {
				t.Fatalf("the control declared %d variable(s) too; inference reaches the declared width "+
					"on this fixture, so the test proves nothing", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The unchanged path
// ---------------------------------------------------------------------------

// TestConvert_SchemalessSource_StillInfers is the other side of "be precise
// about which path is which": a source that declares nothing and carries no
// sidecar — a CSV, in practice — converts exactly as it did before, by
// inference, and says so with PULSE_SPSS_SIDECAR_ABSENT.
func TestConvert_SchemalessSource_StillInfers(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := &plainRowsReader{
		columns: []string{"age", "grade"},
		rows:    [][]string{{"31", "A"}, {"44", "B"}, {"27", "A"}},
	}
	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewConvertJob(src, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	out, err := afero.ReadFile(fs, "out.sav")
	if err != nil {
		t.Fatalf("reading out.sav: %v", err)
	}
	if got := savVariableNames(t, out); len(got) != 2 || got[0] != "age" || got[1] != "grade" {
		t.Fatalf("the converted file declares %v, want [age grade]", got)
	}
	var absent bool
	for _, warn := range w.Warnings() {
		if warn.Code == perr.PULSE_SPSS_SIDECAR_ABSENT {
			absent = true
		}
	}
	if !absent {
		t.Errorf("a schema-less source converted without %s; that warning is how a caller learns the "+
			"dictionary was synthesised", perr.PULSE_SPSS_SIDECAR_ABSENT)
	}
}

// plainRowsReader is a source that declares nothing: no PulseSchema, no
// sidecar. It is the shape of every text adapter.
type plainRowsReader struct {
	columns []string
	rows    [][]string
	at      int
}

func (r *plainRowsReader) ReadHeader() ([]string, error) { return r.columns, nil }

func (r *plainRowsReader) ReadRows(_ context.Context, fn func(row []string) error) error {
	for ; r.at < len(r.rows); r.at++ {
		if err := fn(r.rows[r.at]); err != nil {
			return err
		}
	}
	return nil
}

func (r *plainRowsReader) Reset() error { r.at = 0; return nil }
func (r *plainRowsReader) Close() error { return nil }

var (
	_ pio.Reader      = (*plainRowsReader)(nil)
	_ pio.ResetReader = (*plainRowsReader)(nil)
)
