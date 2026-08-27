package spss

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
)

// The ECOSYSTEM check: can something that is not us open what we write?
//
// Every other test in this package answers "does our reader agree with our
// writer", which is a weaker and different question — a misreading shared by
// both halves passes it cleanly. This one hands an emitted file to ReadStat,
// the C reader behind R's haven, Python's pyreadstat and most of what else
// opens a `.sav`, and to the independent implementation in R's `foreign`.
//
// It SKIPS when R, haven or foreign is missing, which is the state on CI and
// on most machines. That is deliberate: a hard dependency on an R toolchain
// would make the package unbuildable for everyone who does not have one, and
// the test is worth having as a local gate for the person changing the byte
// layout even if it is not worth having as a CI gate. When it does run, it is
// the strongest evidence available that the writer emits real SPSS files.
//
// Recorded result at E5-S2: haven 2.5.5 and foreign 0.8.91 both open every
// file below, including the very-long-string, record 7/21 and record 7/22
// cases, and haven surfaces the variable labels, value labels, measure
// levels, display widths, formats (MOYR6, DATE11, DATETIME20, A300), file
// label, documents and user-missing values intact.

// rEnvironment reports whether an R with haven and foreign is available.
func rEnvironment(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("Rscript")
	if err != nil {
		t.Skip("Rscript is not on PATH; skipping the ReadStat / foreign interoperability check")
	}
	probe := exec.Command(bin, "-e",
		`if (requireNamespace("haven", quietly=TRUE) && requireNamespace("foreign", quietly=TRUE)) cat("READY")`)
	out, err := probe.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "READY") {
		t.Skipf("R is present but haven and foreign are not both installed; skipping (%s)", strings.TrimSpace(string(out)))
	}
	return bin
}

// TestEmittedDictionary_OpensInReadStatAndForeign writes an emitted
// dictionary to a temp file and asks two independent readers to parse it.
//
// The files carry ZERO cases, which is a legal `.sav` and exactly the right
// shape for this story: the data section is E5-S3's, and a dictionary-only
// file isolates what E5-S2 actually emits.
func TestEmittedDictionary_OpensInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	// The hard fixture concentrates the records most likely to be rejected:
	// a very long string (7/14), long-string value labels (7/21), a
	// long-string missing value (7/22), numeric user-missing codes, a
	// long-name mapping (7/13), and a MOYR column whose format code is the
	// only record of what its seconds mean.
	long := strings.Repeat("a", 300)
	hard := spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "Q1", LongName: "Satisfaction", Label: "Overall satisfaction",
				Measure: spsstest.MeasureOrdinal,
				Print:   spsstest.Format{Type: spsstest.FormatF, Width: 8},
				Missing: &spsstest.MissingValues{Discrete: []spsstest.Value{spsstest.Num(98), spsstest.Num(99)}},
			},
			{
				Name: "PERIOD", LongName: "FiscalPeriod", Label: "Fiscal period",
				Measure: spsstest.MeasureScale,
				Print:   spsstest.Format{Type: spsstest.FormatType(28), Width: 6},
			},
			{Name: "NOTES", LongName: "OpenEnded", Width: 300, Label: "Open ended", Measure: spsstest.MeasureNominal},
			{Name: "CODE", LongName: "LongCode", Width: 20, Measure: spsstest.MeasureNominal},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(13166064000), spsstest.Text(long), spsstest.Text("ALPHA")},
			{spsstest.Num(98), spsstest.Num(13168742400), spsstest.Text("short"), spsstest.Text("BETA")},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Very dissatisfied"},
				{Value: spsstest.Num(98), Label: "Don't know"},
				{Value: spsstest.Num(99), Label: "Refused"},
			},
		}},
		LongStringValueLabels: []spsstest.LongStringValueLabels{{
			Var:    "LongCode",
			Labels: []spsstest.LongStringValueLabel{{Value: "ALPHA", Label: "Alpha channel"}},
		}},
		LongStringMissingValues: []spsstest.LongStringMissingValues{{
			Var: "LongCode", Values: []string{"BETA"},
		}},
		DisplayParams:     true,
		CharacterEncoding: "UTF-8",
	}

	dir := t.TempDir()
	write := func(name string, plan *DictionaryPlan) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, plan.Bytes, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return path
	}

	richSchema, richRes := exportFixture(t, richSpec())
	hardSchema, hardRes := exportFixture(t, hard)

	files := map[string]string{
		"sidecar-driven": write("sidecar.sav",
			emit(t, DictionaryRequest{Schema: richSchema, Sidecar: richRes, Cases: 0, Compression: compressionNone})),
		"the hard records": write("hard.sav",
			emit(t, DictionaryRequest{Schema: hardSchema, Sidecar: hardRes, Cases: 0, Compression: compressionNone})),
		"synthesised": write("synth.sav", synthesise(t,
			encoding.Field{Name: "respondent_id", Type: encoding.FieldTypeU32, Description: "Respondent identifier"},
			encoding.Field{Name: "income", Type: encoding.FieldTypeF64, Description: "Household income"},
			encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north", "south")},
			encoding.Field{Name: "signed_up", Type: encoding.FieldTypeDate},
			encoding.Field{Name: "last_seen", Type: encoding.FieldTypeDateTime},
			encoding.Field{Name: "media", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "web"), Description: "Media consumed"},
		)),
	}

	for what, path := range files {
		t.Run(what, func(t *testing.T) {
			// Each reader is asked separately and both failures are
			// reported: they are independent implementations, and one
			// refusing where the other does not is itself the finding.
			// The path travels in the environment rather than as an
			// argument: Rscript -e consumes trailing arguments itself.
			script := `
f <- Sys.getenv("PULSE_SAV")
h <- tryCatch({ d <- haven::read_sav(f, user_na=TRUE); paste0("HAVEN OK vars=", ncol(d)) },
              error=function(e) paste("HAVEN FAIL:", conditionMessage(e)))
g <- tryCatch({ suppressWarnings(d <- foreign::read.spss(f, to.data.frame=FALSE)); paste0("FOREIGN OK vars=", length(d)) },
              error=function(e) paste("FOREIGN FAIL:", conditionMessage(e)))
cat(h, "\n", g, "\n", sep="")
`
			cmd := exec.Command(bin, "-e", script)
			cmd.Env = append(os.Environ(), "PULSE_SAV="+path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("running Rscript: %v\n%s", err, out)
			}
			text := string(out)
			for _, want := range []string{"HAVEN OK", "FOREIGN OK"} {
				if !strings.Contains(text, want) {
					t.Errorf("%s did not report %q:\n%s", path, want, text)
				}
			}
			t.Logf("%s\n%s", path, strings.TrimSpace(text))
		})
	}
}

// TestEmittedFile_ValuesReadInReadStatAndForeign is the E5-S2 structural
// check taken one step further, now that there is a data section: it compares
// the VALUES two independent readers see against the values that were
// written.
//
// Structure was the only question E5-S2 could ask — a dictionary-only file
// has nothing else in it — and structure is the weaker half. A file whose
// dictionary is perfect and whose case bytes are laid out one element to the
// left opens cleanly in every tool and reports the wrong numbers, which is
// precisely the failure mode a Pulse-reads-its-own-Pulse test cannot catch.
//
// Both readers are asked for the same file in both write modes, because the
// compressed and uncompressed sections are supposed to be equivalent to
// something that is not us, not merely to each other.
//
// Recorded result at E5-S3: haven 2.5.5 (ReadStat) and foreign 0.8.91 both
// read every value below back exactly, in both modes — the numerics, the
// string, the DATE column as an R Date, the system-missing cells as NA, and
// the set indicators as their counted value.
func TestEmittedFile_ValuesReadInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	// A cohort covering the four things a data section can get wrong
	// independently: element order, the numeric encoding, the string
	// padding, and the two missing states.
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "ID", Type: encoding.FieldTypeU32},
		{Name: "INCOME", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "REGION", Type: encoding.FieldTypeCategoricalU8, Dictionary: dictOf(t, "north", "south")},
		{Name: "WHEN", Type: encoding.FieldTypeDate},
		{Name: "MEDIA", Type: encoding.FieldTypeSetU8, Dictionary: dictOf(t, "tv", "web")},
	}}
	const day = 19786 // 2024-03-04
	cases := []Case{
		{{Num: 1}, {Num: 1234.5}, {Num: 0}, {Num: day}, {Mask: 0b01}},
		{{Num: 2}, {Num: -0.25}, {Num: 1}, {Num: day + 1}, {Mask: 0b11}},
		{{Num: 3}, {Null: true}, {Num: 0}, {Num: day + 2}, {Mask: 0}},
	}

	dir := t.TempDir()
	for _, mode := range []struct {
		name string
		opts WriterOptions
	}{
		{"bytecode", WriterOptions{}},
		{"uncompressed", WriterOptions{Uncompressed: true}},
	} {
		t.Run(mode.name, func(t *testing.T) {
			plan := planFor(t, s, mode.opts)
			path := filepath.Join(dir, mode.name+".sav")
			if err := os.WriteFile(path, encodeCases(t, plan, s, cases...), 0o644); err != nil {
				t.Fatalf("writing %s: %v", path, err)
			}

			// Every value is rendered to a canonical string on the R
			// side, so a difference in R's own printing cannot be
			// mistaken for a difference in the data. The two readers
			// hand back a DATE column differently — haven converts it to
			// an R Date, foreign leaves the raw SPSS second count — so
			// each is brought to the same ISO text its own way, and both
			// must name the same day.
			script := `
f <- Sys.getenv("PULSE_SAV")
j <- function(x) paste(x, collapse="|")
n <- function(x) j(ifelse(is.na(x), "NA", sprintf("%.4f", as.numeric(x))))
h <- tryCatch({
  d <- haven::read_sav(f)
  paste0("HAVEN OK|", j(names(d)), "|", n(d$ID), "|", n(d$INCOME), "|",
         j(as.character(d$REGION)), "|", j(format(as.Date(d$WHEN), "%Y-%m-%d")), "|",
         n(d$tv), "|", n(d$web))
}, error=function(e) paste("HAVEN FAIL:", conditionMessage(e)))
g <- tryCatch({
  d <- suppressWarnings(foreign::read.spss(f, to.data.frame=FALSE))
  paste0("FOREIGN OK|", n(d$ID), "|", n(d$INCOME), "|",
         j(trimws(as.character(d$REGION))), "|",
         j(format(as.Date(as.numeric(d$WHEN)/86400, origin="1582-10-14"), "%Y-%m-%d")), "|",
         n(d$tv), "|", n(d$web))
}, error=function(e) paste("FOREIGN FAIL:", conditionMessage(e)))
cat(h, "\n", g, "\n", sep="")
`
			cmd := exec.Command(bin, "-e", script)
			cmd.Env = append(os.Environ(), "PULSE_SAV="+path)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("running Rscript: %v\n%s", err, out)
			}
			text := string(out)
			t.Logf("%s\n%s", path, strings.TrimSpace(text))

			// The values, per column, in case order — including the set
			// indicators, which are tv on rows 1 and 2 and web on row 2.
			// Each reader is checked on ITS OWN line: a substring test
			// over the whole output would let one reader's answer stand
			// in for the other's.
			values := "1.0000|2.0000|3.0000|" +
				"1234.5000|-0.2500|NA|" +
				"north|south|north|" +
				"2024-03-04|2024-03-05|2024-03-06|" +
				"1.0000|1.0000|0.0000|" +
				"0.0000|1.0000|0.0000"
			for prefix, want := range map[string]string{
				// Column order is asserted for haven only: it is the
				// reader that names them, and the names are also proof
				// the set expanded into two indicator variables rather
				// than staying one column.
				"HAVEN OK|":   "HAVEN OK|ID|INCOME|REGION|WHEN|tv|web|" + values,
				"FOREIGN OK|": "FOREIGN OK|" + values,
			} {
				line := readerLine(text, prefix)
				if line != want {
					t.Errorf("%s reported\n  %q\nwant\n  %q", strings.TrimSuffix(prefix, "|"), line, want)
				}
			}
		})
	}
}

// TestExportedThroughTheAdapter_ReadsInReadStatAndForeign is the E5-S6
// acceptance check: the whole user-facing path, not the encoder underneath
// it.
//
// Every earlier ecosystem check in this file drove the encoder directly.
// This one goes through pio.ExportJob — the same dispatch `pulse export
// spss` uses, including SetPulseSchema, WriteHeader, the CohortWriter
// hand-off and Close — against a cohort a real import produced. It is the
// first point at which a mistake in the WIRING, as opposed to in the
// bytes, becomes visible: a writer that quietly took the rendered-row path
// would still emit a plausible file, and only an independent reader
// comparing values catches it.
//
// Recorded result at E5-S6: haven 2.5.5 and foreign 0.8.91 both open the
// file and report the same variables and the same values as the source
// `.sav`. Cross-checked outside the test suite through the built binary —
// `pulse import spss` then `pulse export spss`, opened in R — with the
// value labels, variable labels and the response-set indicators intact.
func TestExportedThroughTheAdapter_ReadsInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	fs, cohort, _ := importFixture(t, richSpec())
	w := NewWriter(fs, "out.sav", WriterOptions{})
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ExportJob.Run: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	path := filepath.Join(t.TempDir(), "adapter.sav")
	if err := os.WriteFile(path, w.Bytes(), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	script := `
f <- Sys.getenv("PULSE_SAV")
j <- function(x) paste(x, collapse="|")
n <- function(x) j(ifelse(is.na(x), "NA", sprintf("%.4f", as.numeric(x))))
h <- tryCatch({
  d <- haven::read_sav(f, user_na=TRUE)
  paste0("HAVEN OK|", j(names(d)), "|", n(unclass(d$Satisfaction)), "|", n(d$WT), "|",
         j(trimws(as.character(d$REGION))), "|", n(d$MD1), "|", n(d$MD2), "|",
         j(names(attr(d$Satisfaction, "labels"))))
}, error=function(e) paste("HAVEN FAIL:", conditionMessage(e)))
g <- tryCatch({
  d <- suppressWarnings(foreign::read.spss(f, to.data.frame=FALSE, use.value.labels=FALSE))
  paste0("FOREIGN OK|", n(d$Satisfaction), "|", n(d$WT), "|",
         j(trimws(as.character(d$REGION))), "|", n(d$MD1), "|", n(d$MD2))
}, error=function(e) paste("FOREIGN FAIL:", conditionMessage(e)))
cat(h, "\n", g, "\n", sep="")
`
	cmd := exec.Command(bin, "-e", script)
	cmd.Env = append(os.Environ(), "PULSE_SAV="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running Rscript: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("%s\n%s", path, strings.TrimSpace(text))

	// The source fixture's own values, per column, in case order. The
	// derived `brands` set column must NOT appear: it is folded away, so a
	// reader sees exactly the five variables the source declared.
	values := "1.0000|5.0000|9.0000|" +
		"1.5000|0.5000|2.5000|" +
		"North|South|North|" +
		"1.0000|0.0000|1.0000|" +
		"0.0000|1.0000|1.0000"
	for prefix, want := range map[string]string{
		"HAVEN OK|": "HAVEN OK|Satisfaction|WT|REGION|MD1|MD2|" + values +
			"|Very dissatisfied|Neutral|Never observed",
		"FOREIGN OK|": "FOREIGN OK|" + values,
	} {
		line := readerLine(text, prefix)
		if line != want {
			t.Errorf("%s reported\n  %q\nwant\n  %q", strings.TrimSuffix(prefix, "|"), line, want)
		}
	}
}

// readerLine picks the one output line a reader wrote. An absent line comes
// back empty, which fails the comparison with the whole expectation in the
// message — a reader that errored reports its own FAIL line in the log above.
func readerLine(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// TestEmittedFoldedFile_ReadsInReadStatAndForeign is the E5-S5 acceptance
// criterion put to something that is not us.
//
// The cohort under test is the one that can go wrong quietly: it carries a
// derived `<var>_missing` reason sibling and a derived `set_*` column, and
// the emitted file must show an independent reader EXACTLY the three
// variables the source `.sav` declared — no artefacts — with the user-missing
// code 99 restored to its original numeric value and still declared missing.
//
// A reader of our own output cannot make this case: our reader re-applies the
// missing specification and regenerates the sibling, so a file in which 99
// had been flattened to system-missing would come back with a plausible
// reason column either way. haven and foreign both surface the raw value and
// the declared missing specification separately, which is what makes the
// distinction visible.
//
// Recorded result at E5-S5: haven 2.5.5 reports names INCOME, Q1A, Q1B — no
// INCOME_missing, no media — with the stored doubles 30000, 99, NA, na_values
// = 99, and is.na() TRUE on the second case because the file declares 99
// missing; foreign 0.8.91 (use.missings=FALSE) reports the same three columns
// and the same values, and lists INCOME under its own `missings` attribute.
func TestEmittedFoldedFile_ReadsInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	fs, cohort, _ := importFixture(t, bothKindsSpec())
	path := filepath.Join(t.TempDir(), "folded.sav")
	if err := os.WriteFile(path, exportCohort(t, fs, cohort, WriterOptions{}), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	script := `
f <- Sys.getenv("PULSE_SAV")
j <- function(x) paste(x, collapse="|")
n <- function(x) j(ifelse(is.na(x), "NA", sprintf("%.4f", as.numeric(x))))
h <- tryCatch({
  d <- haven::read_sav(f, user_na=TRUE)
  raw <- as.numeric(unclass(d$INCOME))
  paste0("HAVEN OK|", j(names(d)), "|", n(raw), "|", j(is.na(d$INCOME)), "|",
         j(as.character(attr(d$INCOME, "na_values"))))
}, error=function(e) paste("HAVEN FAIL:", conditionMessage(e)))
g <- tryCatch({
  d <- suppressWarnings(foreign::read.spss(f, to.data.frame=FALSE, use.missings=FALSE))
  paste0("FOREIGN OK|", j(names(d)), "|", n(d$INCOME), "|",
         j(names(attr(d, "missings"))[sapply(attr(d, "missings"), function(m) m$type != "none")]))
}, error=function(e) paste("FOREIGN FAIL:", conditionMessage(e)))
cat(h, "\n", g, "\n", sep="")
`
	cmd := exec.Command(bin, "-e", script)
	cmd.Env = append(os.Environ(), "PULSE_SAV="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running Rscript: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("%s\n%s", path, strings.TrimSpace(text))

	for prefix, want := range map[string]string{
		// The variable list is the artefact check: three names, and
		// neither INCOME_missing nor media among them. The values are the
		// restore check: the stored double really is 99, not the
		// system-missing sentinel. haven's is.na flags are the third
		// column and are the DECLARATION check — the file still says 99 is
		// missing, so haven reports the value as NA while carrying 99
		// underneath, which is exactly what the source file did.
		"HAVEN OK|":   "HAVEN OK|INCOME|Q1A|Q1B|30000.0000|99.0000|NA|FALSE|TRUE|TRUE|99",
		"FOREIGN OK|": "FOREIGN OK|INCOME|Q1A|Q1B|30000.0000|99.0000|NA|INCOME",
	} {
		line := readerLine(text, prefix)
		if line != want {
			t.Errorf("%s reported\n  %q\nwant\n  %q", strings.TrimSuffix(prefix, "|"), line, want)
		}
	}
}

// TestEmittedNonUTF8File_ReadsInReadStatAndForeign is the strongest check
// E5-S4 can be given, and it is the reason this file exists at all.
//
// Every other test of the transcode asks "does our reader agree with our
// writer", and a writer that emitted UTF-8 under a windows-1252 declaration
// would pass all of them: our reader would decode the bytes with the
// declared codepage, get mojibake, and both halves would agree about it only
// because the fixture happens to be Latin-1 and the wrong answer is still
// valid text. An INDEPENDENT reader that honours record 7/20 cannot be
// fooled that way — it either shows the right characters or it does not.
//
// Recorded result at E5-S4: both readers open the emitted windows-1252 file
// and hand back the right text. haven 2.5.5 announces "re-encoding from
// CP1252" and returns UTF-8 (`Zürich` as 5a c3 bc 72 69 63 68), with the
// long variable name, the value labels and the string data all intact.
// foreign 0.8.91 does NOT transcode on its own — it hands back the raw
// codepage bytes and its own trimming then fails in a UTF-8 locale, which is
// its documented behaviour and not a fault in the file — and returns exactly
// the same text when told the encoding with reencode="CP1252". Both facts
// together say the emitted bytes really are windows-1252 and the declaration
// really does name them.
func TestEmittedNonUTF8File_ReadsInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	fs, cohort, _ := importFixture(t, latin1Spec())
	path := filepath.Join(t.TempDir(), "latin1.sav")
	if err := os.WriteFile(path, exportCohort(t, fs, cohort, WriterOptions{}), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}

	// Each string is rendered as its UTF-8 BYTES on the R side. Comparing
	// rendered characters would depend on the R session's locale; comparing
	// bytes does not, and it is the byte-level claim that is being made.
	script := `
f <- Sys.getenv("PULSE_SAV")
b <- function(x) paste(sapply(as.character(x), function(s) paste(charToRaw(enc2utf8(s)), collapse="")), collapse="|")
h <- tryCatch({
  d <- haven::read_sav(f)
  paste0("HAVEN OK|", b(names(d)[1]), "|", b(trimws(as.character(d$CITY))), "|",
         b(sort(names(attr(d$SEX, "labels")))))
}, error=function(e) paste("HAVEN FAIL:", conditionMessage(e)))
g <- tryCatch({
  d <- suppressWarnings(foreign::read.spss(f, to.data.frame=FALSE, reencode="CP1252"))
  paste0("FOREIGN OK|", b(names(d)[1]), "|", b(trimws(as.character(d$CITY))), "|",
         b(sort(names(attr(d, "label.table")$SEX))))
}, error=function(e) paste("FOREIGN FAIL:", conditionMessage(e)))
cat(h, "\n", g, "\n", sep="")
`
	cmd := exec.Command(bin, "-e", script)
	cmd.Env = append(os.Environ(), "PULSE_SAV="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running Rscript: %v\n%s", err, out)
	}
	text := string(out)
	t.Logf("%s\n%s", path, strings.TrimSpace(text))

	// "Identität", "Zürich"|"Genève", "Männlich"|"Weiblich" — as UTF-8
	// bytes, which is what these characters are once a reader that honours
	// the CP1252 declaration has done its job. A writer that had emitted
	// UTF-8 under that declaration would produce a different string here,
	// because the reader would decode each of those bytes separately.
	//
	// The value labels are sorted on the R side: haven keys them by value
	// and foreign by label, so their natural orders differ, and the claim
	// being made is about the CHARACTERS rather than about either reader's
	// ordering.
	const (
		identitat = "4964656e746974c3a474"
		cities    = "5ac3bc72696368|47656ec3a87665"
		sexLabels = "4dc3a46e6e6c696368|576569626c696368"
	)
	for _, prefix := range []string{"HAVEN OK|", "FOREIGN OK|"} {
		want := prefix + identitat + "|" + cities + "|" + sexLabels
		if line := readerLine(text, prefix); line != want {
			t.Errorf("%s reported\n  %q\nwant\n  %q", strings.TrimSuffix(prefix, "|"), line, want)
		}
	}
}

// TestRoundTrippedFile_ReadsIdenticallyInReadStatAndForeign is the strongest
// check this effort can be given, and it is the E5-S7 acceptance criterion
// pointed at something that is not us.
//
// Every round-trip assertion in roundtrip_test.go compares OUR reader against
// OUR writer. A misreading shared by both halves passes all of them: a
// dictionary emitted one element to the left, a value label bound to the
// wrong variable, a missing specification whose sign was dropped — each would
// come back through our own reader exactly as it went in, and the cohorts
// would match.
//
// So the round-tripped artefact is handed to ReadStat (via R's haven) and to
// the independent implementation in R's foreign, and what each reader sees in
// the SOURCE file is compared against what the same reader sees in the file
// that has been through the whole import -> export -> import -> export cycle.
// Neither reader shares any code with anything here, and neither is told what
// to expect: the expectation is the reader's OWN reading of the source.
//
// The file under test is the second export — the fixed point — rather than
// the first, because a cycle that drifted would drift furthest there.
//
// Recorded result at E5-S7: haven 2.5.5 and foreign 0.8.91 each report an
// identical rendering of the source and of the round-tripped file across all
// 17 variables of the kitchen-sink fixture, including the numeric
// user-missing codes and their na_values declarations, the na_range on the
// range-specified variables, the value labels (including the ones declared
// only on user-missing codes), the categorical codes, the DATE and DATETIME
// instants, the MOYR raw seconds, the 600-byte very long string and the
// re-padded short string.
func TestRoundTrippedFile_ReadsIdenticallyInReadStatAndForeign(t *testing.T) {
	bin := rEnvironment(t)

	// Both byte orders. The big-endian arm is not decoration: a numeric
	// missing-value slot is a flt64 in the SOURCE file's order and the
	// emitted file is always little-endian, so a verbatim re-emission is
	// the one fidelity loss in this matrix that OUR reader cannot see —
	// it would regenerate the same reason column either way. ReadStat
	// reports it as an na_values list of subnormals.
	for _, order := range []struct {
		name string
		spec spsstest.Spec
	}{
		{"little-endian", kitchenSinkSpec()},
		{"big-endian", bigEndian(kitchenSinkSpec())},
	} {
		t.Run(order.name, func(t *testing.T) {
			roundTripThroughR(t, bin, order.spec)
		})
	}
}

// roundTripThroughR runs one fixture through the whole cycle and asks both
// R readers whether the artefact reads the same as the source.
func roundTripThroughR(t *testing.T, bin string, spec spsstest.Spec) {
	t.Helper()

	c := runCycle(t, spec, WriterOptions{})

	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
		return path
	}
	source := write("source.sav", c.source)
	round := write("round.sav", c.again)

	// Both files are rendered to one canonical string per reader, by a
	// function that walks EVERY column rather than a hand-picked list: a
	// comparison that named its columns would be silent about a variable
	// that changed shape, which is one of the things this is here to catch.
	//
	// The renders are emitted rather than compared on the R side so a
	// mismatch reports WHERE it differs. `foreign` is asked with
	// use.missings=FALSE so it hands back the stored code rather than NA,
	// which is the value under test.
	script := `
render_haven <- function(f) {
  d <- haven::read_sav(f, user_na=TRUE)
  parts <- paste0("names=", paste(names(d), collapse=","))
  for (nm in names(d)) {
    v <- d[[nm]]
    if (is.character(v)) {
      parts <- c(parts, paste0(nm, "=", paste(trimws(v), collapse="|")))
    } else {
      raw <- as.numeric(unclass(v))
      parts <- c(parts, paste0(nm, "=", paste(ifelse(is.na(raw), "NA", sprintf("%.6f", raw)), collapse="|")))
    }
    labs <- attr(v, "labels")
    if (!is.null(labs) && length(labs) > 0) {
      o <- order(as.character(labs), names(labs))
      parts <- c(parts, paste0(nm, "#", paste(paste0(as.character(labs)[o], ":", names(labs)[o]), collapse="|")))
    }
    nav <- attr(v, "na_values")
    if (!is.null(nav) && length(nav) > 0) parts <- c(parts, paste0(nm, "!", paste(sort(as.character(nav)), collapse="|")))
    nar <- attr(v, "na_range")
    if (!is.null(nar) && length(nar) > 0) parts <- c(parts, paste0(nm, "~", paste(as.character(nar), collapse="|")))
  }
  paste(parts, collapse=";")
}
render_foreign <- function(f) {
  d <- suppressWarnings(foreign::read.spss(f, to.data.frame=FALSE,
                                           use.value.labels=FALSE, use.missings=FALSE))
  parts <- paste0("names=", paste(names(d), collapse=","))
  for (nm in names(d)) {
    v <- d[[nm]]
    if (is.character(v)) {
      parts <- c(parts, paste0(nm, "=", paste(trimws(v), collapse="|")))
    } else {
      raw <- as.numeric(v)
      parts <- c(parts, paste0(nm, "=", paste(ifelse(is.na(raw), "NA", sprintf("%.6f", raw)), collapse="|")))
    }
  }
  lt <- attr(d, "label.table")
  for (nm in sort(names(lt))) {
    tab <- lt[[nm]]
    if (is.null(tab) || length(tab) == 0) next
    o <- order(as.character(tab), names(tab))
    parts <- c(parts, paste0(nm, "#", paste(paste0(as.character(tab)[o], ":", names(tab)[o]), collapse="|")))
  }
  ms <- attr(d, "missings")
  for (nm in sort(names(ms))) {
    m <- ms[[nm]]
    if (is.null(m) || m$type == "none") next
    parts <- c(parts, paste0(nm, "!", m$type, ":", paste(as.character(m$value), collapse="|")))
  }
  paste(parts, collapse=";")
}
emit <- function(tag, fn, f) {
  cat(tag, tryCatch(fn(f), error=function(e) paste("FAIL:", conditionMessage(e))), "\n", sep="")
}
emit("HAVEN-SOURCE|",   render_haven,   Sys.getenv("PULSE_SAV_SOURCE"))
emit("HAVEN-ROUND|",    render_haven,   Sys.getenv("PULSE_SAV_ROUND"))
emit("FOREIGN-SOURCE|", render_foreign, Sys.getenv("PULSE_SAV_SOURCE"))
emit("FOREIGN-ROUND|",  render_foreign, Sys.getenv("PULSE_SAV_ROUND"))
`
	cmd := exec.Command(bin, "-e", script)
	cmd.Env = append(os.Environ(),
		"PULSE_SAV_SOURCE="+source, "PULSE_SAV_ROUND="+round)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running Rscript: %v\n%s", err, out)
	}
	text := string(out)

	for _, reader := range []string{"HAVEN", "FOREIGN"} {
		src := strings.TrimPrefix(readerLine(text, reader+"-SOURCE|"), reader+"-SOURCE|")
		got := strings.TrimPrefix(readerLine(text, reader+"-ROUND|"), reader+"-ROUND|")
		switch {
		case src == "":
			t.Errorf("%s produced no reading of the source file:\n%s", reader, text)
		case strings.HasPrefix(src, "FAIL:"):
			t.Errorf("%s could not read the source fixture: %s", reader, src)
		case strings.HasPrefix(got, "FAIL:"):
			t.Errorf("%s could not read the round-tripped file: %s", reader, got)
		case src != got:
			t.Errorf("%s reads the round-tripped file differently from the source:\n%s",
				reader, firstDifference(src, got))
		default:
			t.Logf("%s: %d characters of reading, identical across the cycle", reader, len(src))
		}
	}
}

// firstDifference reports the first ';'-separated segment on which two
// renderings disagree. A whole-string diff of a seventeen-variable fixture
// is unreadable, and an unreadable failure is one that gets deleted rather
// than fixed.
func firstDifference(want, got string) string {
	a, b := strings.Split(want, ";"), strings.Split(got, ";")
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return "  segment " + strconv.Itoa(i) + "\n  source: " + a[i] + "\n  round:  " + b[i]
		}
	}
	if len(a) != len(b) {
		return "  the source rendered " + strconv.Itoa(len(a)) +
			" segment(s) and the round trip " + strconv.Itoa(len(b))
	}
	return "  (the renderings differ but no segment does; check the separator)"
}
