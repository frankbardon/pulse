package spss

import (
	stdjson "encoding/json"
	"strings"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// sidecarFixture imports richSpec and hands back the filesystem, the
// cohort path and the sidecar the import wrote. Every read-path test
// starts from a genuinely written pair rather than a hand-built one, so
// what is under test is what an import actually leaves on disk.
func sidecarFixture(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs, cohort, _ := importFixture(t, richSpec())
	return fs, cohort
}

// cohortStat returns the cheap O(1) pair the staleness check compares.
func cohortStat(t *testing.T, fs afero.Fs, path string) (int64, time.Time) {
	t.Helper()
	info, err := fs.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size(), info.ModTime()
}

// rewriteCohort replaces the cohort's bytes and pins its modification
// time, so a test controls BOTH halves of the cheap check independently
// instead of hoping the clock cooperates.
func rewriteCohort(t *testing.T, fs afero.Fs, path string, data []byte, mod time.Time) {
	t.Helper()
	if err := afero.WriteFile(fs, path, data, 0644); err != nil {
		t.Fatalf("rewriting %s: %v", path, err)
	}
	if err := fs.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

// writeRawSidecar overwrites the sidecar with arbitrary bytes.
func writeRawSidecar(t *testing.T, fs afero.Fs, cohort string, raw []byte) {
	t.Helper()
	if err := afero.WriteFile(fs, SidecarPath(cohort), raw, 0644); err != nil {
		t.Fatalf("writing sidecar: %v", err)
	}
}

// remarshalSidecar reads the sidecar, lets fn mutate the decoded
// document, and writes it back. Used to build documents that are
// structurally wrong in one specific way.
func remarshalSidecar(t *testing.T, fs afero.Fs, cohort string, fn func(*Document)) {
	t.Helper()
	doc := readSidecar(t, fs, cohort)
	fn(doc)
	raw, err := stdjson.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding sidecar: %v", err)
	}
	writeRawSidecar(t, fs, cohort, raw)
}

func codeOf(t *testing.T, err error) perr.Code {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	ce, ok := err.(*perr.CodedError)
	if !ok {
		t.Fatalf("error is %T, want *errors.CodedError: %v", err, err)
	}
	return ce.Code
}

// ---------------------------------------------------------------------------
// The absent / stale split — the decision this file exists to hold
// ---------------------------------------------------------------------------

// TestLoadSidecar_AbsentAndStaleAreNotTheSameVerdict is the guard on
// the deliberate override. An earlier, flatter answer was "a lost
// sidecar is a warning", which conflated two states with opposite risk
// profiles: a cohort that never had a sidecar (benign, and the normal
// case for synth / CSV output) and a cohort whose sidecar no longer
// describes it (the single highest-fidelity-risk state available).
//
// If a later change unifies them, this test is what fails.
func TestLoadSidecar_AbsentAndStaleAreNotTheSameVerdict(t *testing.T) {
	fs, cohort := sidecarFixture(t)

	// Absent: warning, and the export PROCEEDS on a synthesised default.
	absentFS := afero.NewMemMapFs()
	raw, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	if err := afero.WriteFile(absentFS, cohort, raw, 0644); err != nil {
		t.Fatalf("writing cohort: %v", err)
	}
	absent, err := LoadSidecar(absentFS, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("an absent sidecar must not be an error: %v", err)
	}
	if absent.Status != SidecarStatusAbsent {
		t.Errorf("Status = %v, want absent", absent.Status)
	}
	if !absent.Synthesise() {
		t.Error("an absent sidecar must take the synthesised-default path")
	}
	if absent.Warning == nil || absent.Warning.Code != perr.PULSE_SPSS_SIDECAR_ABSENT {
		t.Errorf("Warning = %v, want PULSE_SPSS_SIDECAR_ABSENT", absent.Warning)
	}

	// Stale: an ERROR, and no resolution at all.
	_, mod := cohortStat(t, fs, cohort)
	rewriteCohort(t, fs, cohort, append(raw, 0x00), mod)
	stale, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err == nil {
		t.Fatal("a stale sidecar must be an error, not a warning")
	}
	if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_STALE {
		t.Errorf("code = %s, want PULSE_SPSS_SIDECAR_STALE", got)
	}
	if stale != nil {
		t.Fatalf("a refusal must yield NO resolution, got %+v", stale)
	}

	// And the two codes are genuinely distinct — a single code used for
	// both would let a caller's severity switch collapse them.
	if perr.PULSE_SPSS_SIDECAR_ABSENT == perr.PULSE_SPSS_SIDECAR_STALE {
		t.Fatal("absent and stale share a code; the split is only nominal")
	}
}

// TestLoadSidecar_StaleCannotSilentlyProduceOutput is the acceptance
// gate, made adversarial rather than incidental.
//
// It walks every way a cohort's cheap fingerprint pair can move —
// including the two a naive implementation gets wrong, a rewrite that
// keeps the SIZE and a rewrite that keeps the MTIME — and asserts, for
// each, three things that together mean no output can be produced:
//
//  1. LoadSidecar returns an error coded PULSE_SPSS_SIDECAR_STALE.
//  2. It returns NO resolution, so there is no object carrying the
//     stale Document for a caller to reach through by mistake.
//  3. The refusal is on the error channel, not the warning one — a
//     caller that logs warnings and carries on cannot proceed past it.
func TestLoadSidecar_StaleCannotSilentlyProduceOutput(t *testing.T) {
	base, cohort := sidecarFixture(t)
	original, err := afero.ReadFile(base, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	sidecarRaw, err := afero.ReadFile(base, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	_, originalMod := cohortStat(t, base, cohort)

	tests := []struct {
		name string
		// data is the cohort's new bytes; mod is its new modification
		// time. Both are set explicitly so neither half of the check can
		// pass by accident.
		data []byte
		mod  time.Time
	}{
		{
			// The obvious one: a re-import or re-export rewrites the file.
			name: "rewritten larger, later",
			data: append(append([]byte(nil), original...), make([]byte, 64)...),
			mod:  originalMod.Add(time.Second),
		},
		{
			// Size moved, mtime pinned back. A check that trusted mtime
			// alone — the cheaper of the two stats — would pass this.
			name: "grown but mtime pinned to the original",
			data: append(append([]byte(nil), original...), make([]byte, 64)...),
			mod:  originalMod,
		},
		{
			// Mtime moved, size identical. A check that trusted size
			// alone would pass this, and the cohort here is a DIFFERENT
			// file of the same length — the most dangerous shape, because
			// every column offset still resolves.
			name: "same length, different content, later mtime",
			data: flipLastByte(original),
			mod:  originalMod.Add(time.Second),
		},
		{
			// Truncation. Half a cohort under a full dictionary.
			name: "truncated",
			data: original[:len(original)/2],
			mod:  originalMod.Add(time.Second),
		},
		{
			// An empty cohort still has a sidecar describing 5 variables.
			name: "emptied",
			data: nil,
			mod:  originalMod.Add(time.Second),
		},
		{
			// Not a rewrite at all — a copy, a restore from backup, a
			// `touch`. The contents may well be identical, but the cheap
			// check cannot know that and must not assume it.
			name: "untouched content, later mtime",
			data: original,
			mod:  originalMod.Add(time.Hour),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			writeRawSidecar(t, fs, cohort, sidecarRaw)
			rewriteCohort(t, fs, cohort, tc.data, tc.mod)

			res, err := LoadSidecar(fs, cohort, WriterOptions{})

			// (1) coded, and coded STALE specifically — not a generic
			// file error a caller might retry, and not INVALID, which
			// would send them looking at the wrong file.
			if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_STALE {
				t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_STALE", got)
			}

			// (2) nothing to proceed with. This is the load-bearing
			// assertion: if a resolution came back, a caller could read
			// Document off it and write a .sav that looks authoritative.
			if res != nil {
				t.Fatalf("stale sidecar yielded a usable resolution: %+v", res)
			}

			// (3) the diagnostic names both files and shows WHICH half
			// moved, so the refusal is actionable rather than a wall.
			ce := err.(*perr.CodedError)
			if ce.Details[perr.DetailSPSSCohort] != cohort {
				t.Errorf("Details[%q] = %v, want %q", perr.DetailSPSSCohort, ce.Details[perr.DetailSPSSCohort], cohort)
			}
			if ce.Details[perr.DetailSPSSSidecar] != SidecarPath(cohort) {
				t.Errorf("Details[%q] = %v, want %q", perr.DetailSPSSSidecar, ce.Details[perr.DetailSPSSSidecar], SidecarPath(cohort))
			}
			if _, ok := ce.Details[perr.DetailSPSSExpected]; !ok {
				t.Errorf("Details missing %q — a caller cannot see what moved", perr.DetailSPSSExpected)
			}
			if _, ok := ce.Details[perr.DetailSPSSActual]; !ok {
				t.Errorf("Details missing %q — a caller cannot see what moved", perr.DetailSPSSActual)
			}
		})
	}
}

// flipLastByte returns a copy of the same LENGTH with different
// content, which is the mutation a size-only staleness check misses.
func flipLastByte(in []byte) []byte {
	out := append([]byte(nil), in...)
	if len(out) > 0 {
		out[len(out)-1] ^= 0xFF
	}
	return out
}

// TestLoadSidecar_FreshIsPresentAndCarriesTheDocument pins the positive
// case: an untouched pair resolves to the real metadata, with no
// warning at all.
func TestLoadSidecar_FreshIsPresentAndCarriesTheDocument(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("LoadSidecar on a fresh pair: %v", err)
	}
	if res.Status != SidecarStatusPresent {
		t.Fatalf("Status = %v, want present", res.Status)
	}
	if res.Synthesise() {
		t.Fatal("a fresh sidecar must NOT take the synthesised-default path")
	}
	if res.Document == nil {
		t.Fatal("Status is present but Document is nil")
	}
	if res.Warning != nil {
		t.Errorf("a healthy resolution must raise no warning, got %v", res.Warning)
	}
	if res.Cohort != cohort || res.Path != SidecarPath(cohort) {
		t.Errorf("resolution paths = %q / %q, want %q / %q", res.Cohort, res.Path, cohort, SidecarPath(cohort))
	}
	// The document is the real one, not an empty shell.
	if len(res.Document.Payload.Variables) == 0 {
		t.Error("resolved document carries no variables")
	}
}

// TestLoadSidecar_CheckIsCheap pins the "O(1) size plus mtime, never a
// hash" property by construction: a cohort whose bytes differ from the
// fingerprint's SHA-256 but whose size and mtime still match RESOLVES.
//
// This is the documented residual gap PULSE_INDEX_STALE lives with, and
// asserting it is deliberate — it is the observable consequence of not
// hashing on the read path, and a future change that started hashing
// there would be a performance regression this test would catch.
// VerifyDigest is the authoritative answer; see the test below.
func TestLoadSidecar_CheckIsCheap(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	original, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	size, mod := cohortStat(t, fs, cohort)

	// Same length, different content, mtime pinned back.
	rewriteCohort(t, fs, cohort, flipLastByte(original), mod)
	if gotSize, gotMod := cohortStat(t, fs, cohort); gotSize != size || !gotMod.Equal(mod) {
		t.Fatalf("fixture failed to preserve the cheap pair: %d/%v vs %d/%v", gotSize, gotMod, size, mod)
	}

	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("the cheap check must not catch a size+mtime-preserving edit (that is VerifyDigest's job): %v", err)
	}
	if res.Status != SidecarStatusPresent {
		t.Fatalf("Status = %v, want present", res.Status)
	}
}

// TestDocument_VerifyDigest_ClosesTheCheapCheckGap is the other half of
// the pair: the authoritative full SHA-256 recompute catches exactly
// what the cheap check cannot.
func TestDocument_VerifyDigest_ClosesTheCheapCheckGap(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	doc := readSidecar(t, fs, cohort)

	if err := doc.VerifyDigest(fs, cohort); err != nil {
		t.Fatalf("an untouched cohort must verify: %v", err)
	}

	original, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	_, mod := cohortStat(t, fs, cohort)
	rewriteCohort(t, fs, cohort, flipLastByte(original), mod)

	err = doc.VerifyDigest(fs, cohort)
	if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_STALE {
		t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_STALE", got)
	}
	ce := err.(*perr.CodedError)
	if _, ok := ce.Details[perr.DetailSPSSExpected]; !ok {
		t.Errorf("Details missing %q", perr.DetailSPSSExpected)
	}
}

func TestDocument_VerifyDigest_Degenerate(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	doc := readSidecar(t, fs, cohort)

	if err := (*Document)(nil).VerifyDigest(fs, cohort); err == nil {
		t.Error("a nil document must not verify")
	}
	if err := doc.VerifyDigest(nil, cohort); err == nil {
		t.Error("a nil filesystem must not verify")
	}
	if err := doc.VerifyDigest(fs, "absent.pulse"); err == nil {
		t.Error("an absent cohort must not verify")
	}
	bad := *doc
	bad.Fingerprint.SHA256 = "not-hex"
	if got := codeOf(t, bad.VerifyDigest(fs, cohort)); got != perr.PULSE_SPSS_SIDECAR_INVALID {
		t.Errorf("code = %s, want PULSE_SPSS_SIDECAR_INVALID for an undecodable digest", got)
	}
}

// ---------------------------------------------------------------------------
// --ignore-sidecar
// ---------------------------------------------------------------------------

// TestLoadSidecar_IgnoreDowngradesStaleToTheAbsentPath is the
// acceptance criterion for the escape hatch: the flag turns the stale
// ERROR into the absent-sidecar WARNING PATH — warn, synthesise a
// default dictionary, proceed. It never applies the stale dictionary.
func TestLoadSidecar_IgnoreDowngradesStaleToTheAbsentPath(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	original, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	_, mod := cohortStat(t, fs, cohort)
	rewriteCohort(t, fs, cohort, append(original, 0x00), mod.Add(time.Second))

	// Without the flag: refused.
	if _, err := LoadSidecar(fs, cohort, WriterOptions{}); err == nil {
		t.Fatal("fixture is not actually stale")
	}

	res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
	if err != nil {
		t.Fatalf("--ignore-sidecar must downgrade the refusal: %v", err)
	}
	if res.Status != SidecarStatusIgnored {
		t.Errorf("Status = %v, want ignored", res.Status)
	}
	if !res.Synthesise() {
		t.Error("the downgrade must take the synthesised-default path")
	}
	if res.Document != nil {
		t.Fatal("--ignore-sidecar handed back the stale document; there is no path that may apply it")
	}
	if res.Warning == nil || res.Warning.Code != perr.PULSE_SPSS_SIDECAR_IGNORED {
		t.Fatalf("Warning = %v, want PULSE_SPSS_SIDECAR_IGNORED", res.Warning)
	}
}

// TestLoadSidecar_IgnoreSuppressesTheReadNotOnlyTheVerdict pins the
// deliberate reading of the flag's name. It is documented to suppress
// the sidecar READ, which has two visible consequences: a HEALTHY
// sidecar is ignored too (so the flag's effect does not flip with an
// mtime), and an UNREADABLE one cannot block the export (so the escape
// hatch works for the failure it was named after AND for the one it
// was not).
func TestLoadSidecar_IgnoreSuppressesTheReadNotOnlyTheVerdict(t *testing.T) {
	t.Run("healthy sidecar is still ignored", func(t *testing.T) {
		fs, cohort := sidecarFixture(t)
		res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
		if err != nil {
			t.Fatalf("LoadSidecar: %v", err)
		}
		if res.Status != SidecarStatusIgnored || !res.Synthesise() {
			t.Errorf("Status = %v / Synthesise = %v, want ignored / true", res.Status, res.Synthesise())
		}
	})

	t.Run("unreadable sidecar cannot block", func(t *testing.T) {
		fs, cohort := sidecarFixture(t)
		writeRawSidecar(t, fs, cohort, []byte("{ this is not json"))

		if got := codeOf(t, mustErr(t, fs, cohort)); got != perr.PULSE_SPSS_SIDECAR_INVALID {
			t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_INVALID without the flag", got)
		}
		res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
		if err != nil {
			t.Fatalf("--ignore-sidecar must survive an unreadable sidecar: %v", err)
		}
		if res.Status != SidecarStatusIgnored {
			t.Errorf("Status = %v, want ignored", res.Status)
		}
	})
}

// mustErr calls LoadSidecar with default options and requires a failure.
func mustErr(t *testing.T, fs afero.Fs, cohort string) error {
	t.Helper()
	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err == nil {
		t.Fatalf("want an error, got resolution %+v", res)
	}
	return err
}

// TestLoadSidecar_IgnoreOnAnAbsentSidecarStaysAbsent: the flag has
// nothing to suppress, and reporting "ignored" for a file that is not
// there would be as false as reporting "absent" for one that is.
func TestLoadSidecar_IgnoreOnAnAbsentSidecarStaysAbsent(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	if err := fs.Remove(SidecarPath(cohort)); err != nil {
		t.Fatalf("removing sidecar: %v", err)
	}
	res, err := LoadSidecar(fs, cohort, WriterOptions{IgnoreSidecar: true})
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	if res.Status != SidecarStatusAbsent {
		t.Errorf("Status = %v, want absent", res.Status)
	}
	if res.Warning == nil || res.Warning.Code != perr.PULSE_SPSS_SIDECAR_ABSENT {
		t.Errorf("Warning = %v, want PULSE_SPSS_SIDECAR_ABSENT", res.Warning)
	}
}

// ---------------------------------------------------------------------------
// Document validation
// ---------------------------------------------------------------------------

// TestLoadSidecar_ForeignOrUnreadableDocumentIsRefused: a file at the
// sidecar path ASSERTS that this cohort has source metadata. Falling
// back to a synthesised default because the file will not parse would
// silently substitute invented codes for recorded ones — the same class
// of loss a stale document causes, reached differently.
func TestLoadSidecar_ForeignOrUnreadableDocumentIsRefused(t *testing.T) {
	tests := []struct {
		name string
		// mutate rewrites the sidecar in place. Exactly one of mutate /
		// raw is set.
		mutate func(*Document)
		raw    []byte
		want   string
	}{
		{name: "not json", raw: []byte("this is not a document"), want: "not valid sidecar JSON"},
		{name: "empty file", raw: []byte(""), want: "not valid sidecar JSON"},
		{name: "json but not an object", raw: []byte("[1,2,3]"), want: "not valid sidecar JSON"},
		{
			name:   "foreign kind",
			mutate: func(d *Document) { d.Kind = "readstat" },
			want:   "kind is",
		},
		{
			name:   "missing kind",
			mutate: func(d *Document) { d.Kind = "" },
			want:   "kind is",
		},
		{
			// A document from a NEWER Pulse may have moved a slot this
			// binary indexes. Declining is safer than reading optimistically.
			name:   "newer format version",
			mutate: func(d *Document) { d.FormatVersion = SidecarFormatVersion + 1 },
			want:   "format_version is",
		},
		{
			name:   "absent format version",
			mutate: func(d *Document) { d.FormatVersion = 0 },
			want:   "format_version is",
		},
		{
			name:   "fingerprint is not hex",
			mutate: func(d *Document) { d.Fingerprint.SHA256 = "zzzz" },
			want:   "well-formed 32-byte digest",
		},
		{
			name:   "fingerprint is the wrong length",
			mutate: func(d *Document) { d.Fingerprint.SHA256 = "abcd" },
			want:   "well-formed 32-byte digest",
		},
		{
			// The parallel-array contract MRSet.Fields consumers index
			// against. Silently repairing it would bind a member list to
			// the wrong columns.
			name: "mrset fields is the wrong length",
			mutate: func(d *Document) {
				d.Payload.MultipleResponseSets[0].Fields = []string{"only_one"}
			},
			want: "index-for-index",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, cohort := sidecarFixture(t)
			if tc.mutate != nil {
				remarshalSidecar(t, fs, cohort, tc.mutate)
			} else {
				writeRawSidecar(t, fs, cohort, tc.raw)
			}

			res, err := LoadSidecar(fs, cohort, WriterOptions{})
			if res != nil {
				t.Fatalf("a refusal must yield NO resolution, got %+v", res)
			}
			if got := codeOf(t, err); got != perr.PULSE_SPSS_SIDECAR_INVALID {
				t.Fatalf("code = %s, want PULSE_SPSS_SIDECAR_INVALID", got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message %q does not explain the fault (want it to mention %q)", err.Error(), tc.want)
			}
			ce := err.(*perr.CodedError)
			if ce.Details[perr.DetailSPSSSidecar] != SidecarPath(cohort) {
				t.Errorf("Details[%q] = %v, want %q", perr.DetailSPSSSidecar,
					ce.Details[perr.DetailSPSSSidecar], SidecarPath(cohort))
			}
		})
	}
}

// TestLoadSidecar_MissingCohortIsNotASidecarVerdict: staleness is a
// COMPARISON. With no cohort there is nothing to compare against, so
// the answer is a plain file fault rather than "stale" — which would
// send a caller to re-import when the actual problem is a missing path.
func TestLoadSidecar_MissingCohortIsNotASidecarVerdict(t *testing.T) {
	fs, cohort := sidecarFixture(t)
	if err := fs.Remove(cohort); err != nil {
		t.Fatalf("removing cohort: %v", err)
	}
	err := mustErr(t, fs, cohort)
	if got := codeOf(t, err); got != perr.DATA_FILE {
		t.Errorf("code = %s, want DATA_FILE for an absent cohort", got)
	}
}

func TestLoadSidecar_NoFilesystemIsCoded(t *testing.T) {
	if got := codeOf(t, func() error {
		_, err := LoadSidecar(nil, "out.pulse", WriterOptions{})
		return err
	}()); got != perr.DATA_FILE {
		t.Errorf("code = %s, want DATA_FILE", got)
	}
}

// ---------------------------------------------------------------------------
// Normalisation — the MRSet.Fields back-compat rule
// ---------------------------------------------------------------------------

// TestNormalise_BackfillsFieldsForAPreE4S5Document is the compatibility
// rule this story must honour. MRSet.Fields arrived additively under
// `omitempty` and SidecarFormatVersion did NOT move for it, so the
// version cannot distinguish an older document from a newer one: an
// absent `fields` key means "written before the slot existed", never
// "this set has no members". Rejecting it would refuse every sidecar
// written before E4-S5.
func TestNormalise_BackfillsFieldsForAPreE4S5Document(t *testing.T) {
	fs, cohort := sidecarFixture(t)

	// Strip the slot, exactly as a pre-E4-S5 writer would have left it.
	var wantFields [][]string
	remarshalSidecar(t, fs, cohort, func(d *Document) {
		if len(d.Payload.MultipleResponseSets) == 0 {
			t.Fatal("fixture declares no response sets; nothing to back-fill")
		}
		for i := range d.Payload.MultipleResponseSets {
			set := &d.Payload.MultipleResponseSets[i]
			wantFields = append(wantFields, append([]string(nil), set.Fields...))
			set.Fields = nil
		}
	})
	// Confirm the on-disk document really has no `fields` key — an
	// `omitempty` slot that still serialised would make this vacuous.
	rawDoc, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	if strings.Contains(string(rawDoc), `"fields"`) {
		t.Fatal("fixture still carries a fields key; the back-compat case is not being exercised")
	}

	res, err := LoadSidecar(fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatalf("a pre-E4-S5 document must load, not error: %v", err)
	}
	for i, set := range res.Document.Payload.MultipleResponseSets {
		if len(set.Fields) != len(set.Variables) {
			t.Fatalf("set %q: Fields has %d entries, Variables has %d — the parallel-array contract is broken",
				set.Name, len(set.Fields), len(set.Variables))
		}
		for j := range set.Fields {
			if set.Fields[j] != wantFields[i][j] {
				t.Errorf("set %q member %d: back-filled %q, the writer resolved %q",
					set.Name, j, set.Fields[j], wantFields[i][j])
			}
		}
	}
}

// TestNormalise_ResolutionRulesMatchTheWriter pins the two rules the
// back-fill shares with sidecar_build.go's setFieldResolver, because a
// divergence would silently bind a member to a different column than
// the import actually read.
func TestNormalise_ResolutionRulesMatchTheWriter(t *testing.T) {
	doc := &Document{
		FormatVersion: SidecarFormatVersion,
		Kind:          SidecarKind,
		Payload: Payload{
			Variables: []Variable{
				{Name: "first_q1", ShortName: "Q1"},
				{Name: "second_q1", ShortName: "q1"}, // duplicate, case-insensitively
				{Name: "media_a", ShortName: "MEDIA_A"},
			},
			MultipleResponseSets: []MRSet{{
				Name: "$m", Kind: MRSetKindCategory,
				// Lower-case spelling, a repeat, and a member no
				// variable declares.
				Variables: []string{"q1", "media_a", "q1", "GHOST"},
			}},
		},
	}
	if err := doc.Normalise(); err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	got := doc.Payload.MultipleResponseSets[0].Fields
	want := []string{"first_q1", "media_a", "first_q1", ""}
	if len(got) != len(want) {
		t.Fatalf("Fields = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Fields[%d] = %q, want %q (case-insensitive, first declaration wins, unknown -> \"\")",
				i, got[i], want[i])
		}
	}
}

// TestNormalise_DoesNotSecondGuessAResolvedDocument: where the writer
// already resolved the slot, its answer stands. Re-deriving would let
// this binary's view of the mapping override the one the import
// actually applied.
func TestNormalise_DoesNotSecondGuessAResolvedDocument(t *testing.T) {
	doc := &Document{
		FormatVersion: SidecarFormatVersion,
		Kind:          SidecarKind,
		Payload: Payload{
			Variables: []Variable{{Name: "renamed", ShortName: "Q1"}},
			MultipleResponseSets: []MRSet{{
				Name: "$m", Kind: MRSetKindDichotomy,
				Variables: []string{"Q1"},
				Fields:    []string{"what_the_import_actually_used"},
			}},
		},
	}
	if err := doc.Normalise(); err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	if got := doc.Payload.MultipleResponseSets[0].Fields[0]; got != "what_the_import_actually_used" {
		t.Errorf("Fields[0] = %q; the writer's own resolution must win", got)
	}
}

func TestNormalise_IsIdempotentAndNilSafe(t *testing.T) {
	if err := (*Document)(nil).Normalise(); err != nil {
		t.Errorf("Normalise on a nil document: %v", err)
	}
	fs, cohort := sidecarFixture(t)
	doc := readSidecar(t, fs, cohort)
	if err := doc.Normalise(); err != nil {
		t.Fatalf("Normalise: %v", err)
	}
	first := append([]string(nil), doc.Payload.MultipleResponseSets[0].Fields...)
	if err := doc.Normalise(); err != nil {
		t.Fatalf("Normalise (second run): %v", err)
	}
	second := doc.Payload.MultipleResponseSets[0].Fields
	if len(first) != len(second) {
		t.Fatalf("Normalise is not idempotent: %v then %v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("Normalise is not idempotent at %d: %q then %q", i, first[i], second[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Surface hygiene
// ---------------------------------------------------------------------------

// TestSidecarResolution_NilSynthesises: no resolution is not a licence
// to invent a dictionary from nothing — but it is also not a reason to
// panic. A nil receiver answers "synthesise", the safe reading.
func TestSidecarResolution_NilSynthesises(t *testing.T) {
	if !(*SidecarResolution)(nil).Synthesise() {
		t.Error("a nil resolution must report Synthesise() == true")
	}
}

func TestSidecarStatus_StringsAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range []SidecarStatus{
		SidecarStatusUnknown, SidecarStatusPresent, SidecarStatusAbsent, SidecarStatusIgnored,
	} {
		if seen[s.String()] {
			t.Errorf("duplicate SidecarStatus rendering %q", s.String())
		}
		seen[s.String()] = true
	}
	if got := SidecarStatus(99).String(); !strings.Contains(got, "99") {
		t.Errorf("SidecarStatus(99).String() = %q, want it to name the value", got)
	}
}

// TestSidecarCodes_AreRegisteredWithFixups: every code this file can
// raise must carry Message + Fixup metadata, or `pulse errors lookup`
// answers nothing for the diagnostic a user just hit.
func TestSidecarCodes_AreRegisteredWithFixups(t *testing.T) {
	for _, code := range []perr.Code{
		perr.PULSE_SPSS_SIDECAR_ABSENT,
		perr.PULSE_SPSS_SIDECAR_STALE,
		perr.PULSE_SPSS_SIDECAR_INVALID,
		perr.PULSE_SPSS_SIDECAR_IGNORED,
	} {
		meta, ok := perr.Lookup(string(code))
		if !ok {
			t.Errorf("%s has no codeMetadata entry", code)
			continue
		}
		if meta.Message == "" {
			t.Errorf("%s has an empty Message", code)
		}
		if len(meta.Fixups) == 0 && !meta.FixupNotApplicable {
			t.Errorf("%s has no Fixup and is not marked FixupNotApplicable", code)
		}
	}
}
