package cli

// `pulse convert` from a 206-option `.sav` to every writable target.
//
// The story's last criterion is narrow and specific: a wide-set column is
// HANDLED or REPORTED, never silently dropped. The third outcome is the
// one that matters — a target that emits a perfectly well-formed file with
// 206 columns and no set column at all, or with a set column holding only
// the low 64 bits, is the silent failure the whole effort is about, and
// nothing downstream of a convert would ever show it.
//
// So each target is driven through the real CLI leaf and then READ BACK
// with the reader for its own format, and the assertion is made on the
// label at bit 200: a mask narrowed anywhere in the pipeline still carries
// V03, and still loses V200.
//
// The `.sav` target is the deliberate exception and is asserted on its own
// terms, as a REFUSAL. `pulse convert` has no cohort, so the `.sav` writer
// takes its row path: it buffers the rows, builds an intermediate cohort,
// and that cohort has no metadata sidecar by construction. Without one the
// derived set column is indistinguishable from a real cohort field, so the
// synthesised path emits it as an SPSS variable under names its 206
// constituents already hold — PULSE_SPSS_NAME_COLLISION. That is
// pre-existing and width-independent (it is the same divergence
// TestRoundTrip_IgnoreSidecarRefusesRatherThanLosingTheSet records), it is
// CODED, and it is the acceptable half of this criterion: handled, or
// reported. The handled half for `.sav` is `pulse export spss`, which has
// a sidecar and is covered at 206 constituents in io/spss.
//
// The refusal is asserted by CODE rather than waved through a generic
// "any error will do" branch, because a generic branch passes whatever the
// export happens to do — including breaking for a reason that has nothing
// to do with sets.

import (
	"bytes"
	"context"
	goerrors "errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	pformat "github.com/frankbardon/pulse/io/format"
	"github.com/spf13/afero"
)

// wideBatterySav builds a `.sav` carrying a 206-constituent multiple-
// dichotomy battery over one respondent who ticked members #3, #70 and
// #200 — one bit in each of three different words of a [4]uint64.
func wideBatterySav(t *testing.T) []byte {
	t.Helper()
	const members = 206
	picked := map[int]bool{3: true, 70: true, 200: true}

	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	spec := spsstest.Spec{CharacterEncoding: "UTF-8"}
	names := make([]string, 0, members)
	row := make([]spsstest.Value, 0, members)
	for i := 0; i < members; i++ {
		name := wideVarName(i)
		spec.Vars = append(spec.Vars, spsstest.Var{Name: name, Print: num})
		names = append(names, name)
		if picked[i] {
			row = append(row, spsstest.Num(1))
		} else {
			row = append(row, spsstest.Num(0))
		}
	}
	spec.Cases = [][]spsstest.Value{row}
	spec.MultipleResponseSets = []spsstest.MRSet{{
		Name: "$wide", Kind: spsstest.MRDichotomy, CountedValue: "1",
		Vars: names, Subtype: spsstest.SubtypeMRSets,
	}}

	raw, err := spsstest.Build(spec)
	if err != nil {
		t.Fatalf("building the fixture: %v", err)
	}
	return raw
}

// wideVarName mirrors the SPSS fixtures' member naming: a legal short name
// whose text is what the derived set's dictionary entry becomes.
func wideVarName(i int) string {
	s := ""
	if i < 10 {
		s = "0"
	}
	return "V" + s + itoaCLI(i)
}

func itoaCLI(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func runConvert(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := ConvertCommand()
	var buf bytes.Buffer
	root.Writer = &buf
	err := root.Run(context.Background(), append([]string{"convert"}, args...))
	return buf.String(), err
}

// TestConvertCLI_SavWideSetReachesEveryTarget is the criterion.
//
// For each writable target: convert the 206-option `.sav`, read the result
// back with that format's own reader, and require the wide set to have
// survived — or the convert to have failed with a coded error naming why.
// The one thing no target is allowed to do is succeed while losing it.
func TestConvertCLI_SavWideSetReachesEveryTarget(t *testing.T) {
	source := wideBatterySav(t)

	for _, tc := range []struct {
		format string
		ext    string
	}{
		{pformat.CSV, ".csv"},
		{pformat.TSV, ".tsv"},
		{pformat.NDJSON, ".ndjson"},
		{pformat.JSONArray, ".json"},
		{pformat.Parquet, ".parquet"},
		{pformat.Arrow, ".arrow"},
		{pformat.Excel, ".xlsx"},
		{pformat.SPSS, ".sav"},
	} {
		t.Run(tc.format, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "source.sav")
			if err := os.WriteFile(in, source, 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			out := filepath.Join(dir, "out"+tc.ext)

			_, err := runConvert(t, in, out)

			if tc.format == pformat.SPSS {
				if err == nil {
					t.Fatalf("`pulse convert x.sav out.sav` succeeded; the row path has no sidecar, " +
						"so the derived set column and its constituents must collide")
				}
				code, ok := codedCode(err)
				if !ok {
					t.Fatalf("the refusal is UNCODED, which no caller can look up: %v", err)
				}
				if code != perrors.PULSE_SPSS_NAME_COLLISION {
					t.Errorf("code = %s, want %s — a refusal for some other reason is not this criterion's answer",
						code, perrors.PULSE_SPSS_NAME_COLLISION)
				}
				if exists(out) {
					t.Errorf("a refused convert still wrote %s", out)
				}
				return
			}

			if err != nil {
				t.Fatalf("convert to %s failed: %v", tc.format, err)
			}
			header, first := readBackFirstRow(t, tc.format, out)

			at := indexOfFold(header, "wide")
			if at < 0 {
				t.Fatalf("the %s target carries no %q column; the convert succeeded and dropped it silently (header = %v)",
					tc.format, "wide", header)
			}
			cell := first[at]
			for _, bit := range []int{3, 70, 200} {
				if !strings.Contains(cell, wideVarName(bit)) {
					t.Errorf("%s: the set cell = %q, want it to name %q (bit %d)",
						tc.format, cell, wideVarName(bit), bit)
				}
			}
		})
	}
}

// TestExportPredict_EveryTargetReportsTheWideSetField is the target-aware
// predict criterion stated across ALL of them rather than only the one
// that can refuse.
//
// spss is the sole io.CohortValidator, so it is the only target whose
// predict consults the writer at all. The criterion's teeth are therefore
// in the other seven: a target that does not implement the interface must
// be predicted EXACTLY as before — the wide field present in the reported
// schema at its real type, no TargetWarnings invented, and above all no
// refusal. A false refusal is worse than silence, and a predict that
// quietly stopped reporting a set_u256 column would make `pulse export
// predict` answer for a narrower cohort than the one on disk.
func TestExportPredict_EveryTargetReportsTheWideSetField(t *testing.T) {
	dir := t.TempDir()
	cohort := filepath.Join(dir, "wide.pulse")
	if err := os.WriteFile(cohort, wideSetPulse(t), 0o644); err != nil {
		t.Fatalf("writing the cohort: %v", err)
	}

	for _, format := range []string{
		pformat.CSV, pformat.TSV, pformat.NDJSON, pformat.JSONArray,
		pformat.Parquet, pformat.Arrow, pformat.Excel, pformat.SPSS,
	} {
		t.Run(format, func(t *testing.T) {
			fs := afero.NewOsFs()
			w, err := newWriterForFormat(format, fs, filepath.Join(dir, "out."+format), writerOptions{})
			if err != nil {
				t.Fatalf("newWriterForFormat(%s): %v", format, err)
			}
			job := pio.NewExportJob(cohort, w)
			job.FS = fs

			rep, err := job.Predict(context.Background())
			if err != nil {
				t.Fatalf("predict refused a wide-set export to %s, which it can carry: %v", format, err)
			}
			f := rep.Schema.Field("wide")
			if f == nil {
				t.Fatalf("the predicted schema for %s has no wide field", format)
			}
			if f.Type != encoding.FieldTypeSetU256 {
				t.Errorf("%s: predicted type = %s, want set_u256 — predict must report the cohort's own width",
					format, f.Type)
			}
			if got := len(f.Dictionary.Values()); got != 206 {
				t.Errorf("%s: predicted dictionary = %d entries, want 206", format, got)
			}

			// Only the .sav target consults the writer, and the one
			// diagnostic it is entitled to raise here is the absent
			// sidecar: this cohort was never SPSS-derived.
			for _, warn := range rep.TargetWarnings {
				if format != pformat.SPSS {
					t.Errorf("%s does not implement io.CohortValidator yet raised %s", format, warn.Code)
					continue
				}
				if warn.Code != perrors.PULSE_SPSS_SIDECAR_ABSENT {
					t.Errorf("spss predict raised %s for a wide set the export accepts: %s", warn.Code, warn.Message)
				}
			}
			// And the .sav arm must actually have CONSULTED the validator.
			// Asserting only that the warnings are acceptable passes just
			// as well when no validator ran at all, which would make this
			// whole test blind to the one target it is really about.
			if format == pformat.SPSS && !hasCode(rep.TargetWarnings, perrors.PULSE_SPSS_SIDECAR_ABSENT) {
				t.Errorf("spss predict raised no %s over a cohort with no sidecar; io.CohortValidator was not consulted",
					perrors.PULSE_SPSS_SIDECAR_ABSENT)
			}
		})
	}
}

// wideSetPulse builds a one-record `.pulse` cohort whose only column is a
// 206-entry set_u256 with bits 3, 70 and 200 selected. No sidecar: the
// point is the SCHEMA a predict reports, and a cohort that never came from
// SPSS is the honest shape for asking eight different targets about one.
func wideSetPulse(t *testing.T) []byte {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < 206; i++ {
		if _, err := dict.Add(wideVarName(i)); err != nil {
			t.Fatalf("building the dictionary: %v", err)
		}
	}
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "wide", Type: encoding.FieldTypeSetU256, Dictionary: dict},
	}}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	var m encoding.SetMask
	for _, b := range []int{3, 70, 200} {
		m = m.WithBit(b)
	}
	if err := encoding.WriteSetMask(&buf, encoding.FieldTypeSetU256, m); err != nil {
		t.Fatalf("WriteSetMask: %v", err)
	}
	return buf.Bytes()
}

// readBackFirstRow reads an emitted target with the reader for its own
// format and returns the header plus the first row.
func readBackFirstRow(t *testing.T, format, path string) ([]string, []string) {
	t.Helper()
	r, err := newReaderForFormat(format, afero.NewOsFs(), path, pformat.ReaderOptions{})
	if err != nil {
		t.Fatalf("building a %s reader: %v", format, err)
	}
	defer func() { _ = r.Close() }()

	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("reading the %s header: %v", format, err)
	}
	var first []string
	seen := false
	if err := r.ReadRows(context.Background(), func(row []string) error {
		if !seen {
			first = append([]string(nil), row...)
			seen = true
		}
		return nil
	}); err != nil {
		t.Fatalf("reading %s rows: %v", format, err)
	}
	if !seen {
		t.Fatalf("the %s target carries no rows", format)
	}
	return header, first
}

func hasCode(warns []*perrors.CodedError, code perrors.Code) bool {
	for _, w := range warns {
		if w.Code == code {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func indexOfFold(hay []string, needle string) int {
	for i, h := range hay {
		if strings.EqualFold(h, needle) {
			return i
		}
	}
	return -1
}

// codedCode reports the coded error's code, if the error carries one.
func codedCode(err error) (perrors.Code, bool) {
	var ce *perrors.CodedError
	if goerrors.As(err, &ce) {
		return ce.Code, true
	}
	return "", false
}
