package spss

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
