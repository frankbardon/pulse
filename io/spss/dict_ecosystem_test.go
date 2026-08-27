package spss

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/spsstest"
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
			encoding.Field{Name: "respondent id", Type: encoding.FieldTypeU32, Description: "Respondent identifier"},
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
