package spss

// The metadata sidecar's READ path: the half an export stands on.
//
// sidecar.go builds and writes the document; this file loads one back
// and decides whether it may be trusted. The two questions it answers
// are deliberately kept apart, because they carry very different risk:
//
//   - Is there a sidecar at all? ABSENT is benign. A `.pulse` produced
//     by synth, by a CSV import or by a processing run never had one and
//     never will, and an export of such a cohort is an ordinary thing to
//     want. It warns (PULSE_SPSS_SIDECAR_ABSENT) and proceeds on a
//     dictionary synthesised from the `.pulse` schema alone.
//   - Does the sidecar still describe THIS cohort? STALE is an error
//     (PULSE_SPSS_SIDECAR_STALE). A stale sidecar holds a complete,
//     plausible SPSS dictionary — codes, labels, missing-value
//     specifications, response-set definitions — and applying it to a
//     cohort whose columns or dictionaries have moved produces a `.sav`
//     that carries every mark of authority and none of the correctness.
//     Downstream syntax reading `IF q1 EQ 5` then addresses a category
//     that is no longer there, and nothing can detect it. There is no
//     partial application and no quiet fallback to defaults.
//
// That split is a deliberate override of a flatter "a lost sidecar is a
// warning" rule, which conflated the benign case with the dangerous
// one. If a later change appears to want them unified, it is reverting
// the decision this file exists to hold.
//
// # Why the check is size + mtime and not a hash
//
// Modelled on encoding.SidecarIndexPath's staleness model, for its
// reasons. Hashing a multi-gigabyte cohort on every export would cost
// more than the export, so the read path compares the cheap O(1) pair
// the document already carries — byte size and modification time — and
// that catches every mutation that goes through a writer, since a
// rewrite moves at least one of them. The residual gap is the same one
// PULSE_INDEX_STALE lives with: an in-place edit preserving BOTH size
// and mtime goes unnoticed. The authoritative answer is the full
// SHA-256 recompute, offered separately as [Document.VerifyDigest] for
// a verify-style pass that can afford it.
//
// # Where the caller knob lives, and why here
//
// There is no `pulse export spss` leaf yet — E5-S6 wires one — so
// --ignore-sidecar is defined here as [WriterOptions.IgnoreSidecar] and
// the CLI leaf mounts it later. That direction is not arbitrary. Writer
// dispatch does NOT route through io/format (readers do; writers live
// in internal/cli's own switch), so there is no shared writer-options
// struct to hang it on, and CLAUDE.md is explicit that the CLI parses
// flags and holds no business logic. Deciding "absent or stale, and
// what follows from that" is business logic. It belongs in the library,
// where a library embedder calling the writer directly gets the same
// enforcement a CLI user does — which a flag parsed in internal/cli
// could not give them.

import (
	"encoding/hex"
	stdjson "encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Caller options
// ---------------------------------------------------------------------------

// WriterOptions is the knob bag for the SPSS WRITE side.
//
// It exists ahead of the writer itself because the sidecar read is the
// writer's first act and already needs a caller knob. Later write-side
// stories add their own fields here rather than growing a parallel
// struct, and E5-S6's CLI leaf maps flags onto it one-for-one — the
// struct is the contract, the flags are a projection of it.
type WriterOptions struct {
	// IgnoreSidecar suppresses the metadata sidecar entirely: the file
	// is not read even if it is present and healthy, and the export
	// proceeds on a dictionary synthesised from the `.pulse` schema
	// alone, exactly as it would for a cohort that never had one.
	//
	// It suppresses the READ rather than only the staleness verdict, and
	// that is the deliberate reading of the flag's name. Two
	// consequences follow, both wanted. Output no longer depends on the
	// state of a file the caller has explicitly opted out of — a flag
	// whose effect flipped with an mtime would be a trap. And an
	// UNREADABLE sidecar cannot block the export either, so the flag is
	// a genuine escape hatch rather than one that works only for the
	// failure mode it was named after.
	//
	// It never causes a stale dictionary to be applied. There is no
	// option that does; the choice is between the recorded metadata and
	// the synthesised default, never between fresh and stale.
	IgnoreSidecar bool

	// Uncompressed writes the data section as flat 8-byte elements
	// instead of SPSS's bytecode compression.
	//
	// The DEFAULT is compressed, because that is what SPSS's own SAVE
	// writes: almost every `.sav` in the world is bytecode-compressed,
	// so it is the path every tool that opens one exercises daily. The
	// two encodings are losslessly equivalent — identical case data,
	// re-importing to identical cohorts — so this knob trades size for
	// a data section a human can read in a hex dump, and nothing else.
	//
	// It does NOT select ZSAV. ZSAV emission is not implemented; asking
	// for it is PULSE_SPSS_COMPRESSION_UNSUPPORTED.
	Uncompressed bool
}

// Compression is the header compression flag these options select: SPSS
// bytecode by default, uncompressed when [WriterOptions.Uncompressed] is
// set. It is what a caller passes as [DictionaryRequest.Compression], so
// the flag the header declares and the encoding the data section is
// written in are decided in one place.
func (o WriterOptions) Compression() int32 {
	if o.Uncompressed {
		return compressionNone
	}
	return compressionBytecode
}

// ---------------------------------------------------------------------------
// Resolution
// ---------------------------------------------------------------------------

// SidecarStatus is which of the four terminal states a sidecar
// resolution reached. It is a typed value rather than a bool pair so a
// consumer switching on it gets a compiler-visible arm per state.
type SidecarStatus int

const (
	// SidecarStatusUnknown is the zero value and is never a resolution
	// this package returns. It exists so a zero-valued struct cannot be
	// mistaken for a decided one.
	SidecarStatusUnknown SidecarStatus = iota

	// SidecarStatusPresent: a sidecar was found, read, validated and
	// confirmed to describe this cohort's current bytes. Document is
	// non-nil and is the authoritative source metadata.
	SidecarStatusPresent

	// SidecarStatusAbsent: no file exists at the sidecar's path. The
	// normal state for a cohort that was never SPSS-derived. Document is
	// nil and the caller synthesises a default dictionary.
	SidecarStatusAbsent

	// SidecarStatusIgnored: a sidecar exists and was not read, because
	// the caller set [WriterOptions.IgnoreSidecar]. Document is nil and
	// the caller synthesises a default dictionary, exactly as for
	// SidecarStatusAbsent — the two differ in what is TRUE, not in what
	// happens next.
	SidecarStatusIgnored
)

// String renders the status for diagnostics.
func (s SidecarStatus) String() string {
	switch s {
	case SidecarStatusPresent:
		return "present"
	case SidecarStatusAbsent:
		return "absent"
	case SidecarStatusIgnored:
		return "ignored"
	case SidecarStatusUnknown:
		return "unknown"
	default:
		return "SidecarStatus(" + strconv.Itoa(int(s)) + ")"
	}
}

// SidecarResolution is what [LoadSidecar] decided, and the whole of
// what a write path needs in order to know which dictionary to build.
//
// It is returned only when the export may proceed. A refusal — a stale
// or invalid sidecar with [WriterOptions.IgnoreSidecar] unset — is an
// ERROR return and no resolution at all, so there is no shape in which
// a caller can hold a resolution that says "do not export" and act on
// it by accident.
type SidecarResolution struct {
	// Cohort is the `.pulse` path the resolution is about.
	Cohort string

	// Path is the sidecar path that was consulted, derived by
	// [SidecarPath]. Reported for every status, including
	// SidecarStatusAbsent, where it is the path that was looked for.
	Path string

	// Status is the terminal state.
	Status SidecarStatus

	// Document is the validated, normalised sidecar. Non-nil if and
	// only if Status is SidecarStatusPresent — see [SidecarResolution.Synthesise].
	Document *Document

	// Warning is the coded diagnostic this resolution raised, nil for
	// SidecarStatusPresent. It is a *errors.CodedError so it can be
	// appended straight onto the warning slice the io/ reports carry
	// (ImportReport.SourceWarnings and its siblings), which is the
	// established channel for a non-fatal PULSE_SPSS_* diagnostic.
	Warning *errors.CodedError
}

// Synthesise reports whether the caller must build a default SPSS
// dictionary from the `.pulse` schema, because no trusted source
// metadata is available.
//
// It is the single question a write path asks, and it is a method
// rather than a `Document == nil` check at each call site so the
// "absent or ignored, same next step" rule is stated once. A nil
// receiver answers true: no resolution is not a licence to invent one.
func (r *SidecarResolution) Synthesise() bool { return r == nil || r.Document == nil }

// ---------------------------------------------------------------------------
// Load
// ---------------------------------------------------------------------------

// LoadSidecar resolves the metadata sidecar for cohortPath.
//
// It is the entry point for the SPSS write side and the only supported
// way to obtain a [Document] from disk. The order of its checks is the
// contract:
//
//  1. IgnoreSidecar set and a sidecar present -> SidecarStatusIgnored,
//     warning, no read. Checked before the read so an unreadable file
//     cannot block a caller who has opted out.
//  2. No file at the sidecar path -> SidecarStatusAbsent, warning.
//  3. Unreadable, not JSON, foreign `kind`, unknown `format_version`,
//     malformed fingerprint, or a broken parallel-array contract ->
//     PULSE_SPSS_SIDECAR_INVALID.
//  4. Cohort size or mtime moved since the sidecar was written ->
//     PULSE_SPSS_SIDECAR_STALE.
//  5. Otherwise SidecarStatusPresent, with the document normalised (see
//     [Document.Normalise]).
//
// Steps 3 and 4 are errors, not warnings, and there is no path on which
// a stale or unreadable document is applied anyway. IgnoreSidecar
// downgrades both to a SidecarStatusIgnored warning — but it reaches
// that decision at step 1, by NOT reading the file, so the downgrade is
// a property of the flag rather than of the failure, and the warning
// cannot say which refusal (if any) it silenced.
//
// The cohort itself must exist: staleness is a comparison and there is
// nothing to compare a sidecar against otherwise. A missing cohort is a
// DATA_FILE error, not a sidecar diagnostic.
func LoadSidecar(fsys afero.Fs, cohortPath string, opts WriterOptions) (*SidecarResolution, error) {
	if fsys == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.LoadSidecar: no filesystem",
			map[string]any{errors.DetailSPSSCohort: cohortPath})
	}
	path := SidecarPath(cohortPath)

	exists, err := sidecarExists(fsys, path, cohortPath)
	if err != nil {
		return nil, err
	}

	// Step 1. The opt-out is honoured BEFORE the read, so that a
	// corrupt sidecar is as ignorable as a healthy one. See
	// WriterOptions.IgnoreSidecar.
	if opts.IgnoreSidecar && exists {
		return &SidecarResolution{
			Cohort:  cohortPath,
			Path:    path,
			Status:  SidecarStatusIgnored,
			Warning: sidecarWarning(errors.PULSE_SPSS_SIDECAR_IGNORED, cohortPath, path),
		}, nil
	}

	// Step 2. Absent is benign — and stays benign under IgnoreSidecar,
	// which has nothing to suppress. Reporting "ignored" for a file that
	// is not there would be as false as reporting "absent" for one that
	// is.
	if !exists {
		return &SidecarResolution{
			Cohort:  cohortPath,
			Path:    path,
			Status:  SidecarStatusAbsent,
			Warning: sidecarWarning(errors.PULSE_SPSS_SIDECAR_ABSENT, cohortPath, path),
		}, nil
	}

	// Step 3.
	doc, err := readSidecarDocument(fsys, cohortPath, path)
	if err != nil {
		return nil, err
	}

	// Step 4. The cheap O(1) pair, never a hash. See the file comment.
	if err := doc.checkFresh(fsys, cohortPath, path); err != nil {
		return nil, err
	}

	// Step 5.
	if why, ok := doc.normalise(); !ok {
		return nil, sidecarInvalid(cohortPath, path, why)
	}
	return &SidecarResolution{
		Cohort:   cohortPath,
		Path:     path,
		Status:   SidecarStatusPresent,
		Document: doc,
	}, nil
}

// sidecarExists reports whether a file sits at the sidecar path.
//
// A stat error that is NOT "does not exist" is surfaced rather than
// folded into `false`: an unreadable directory or a permissions fault
// is not the same fact as an absent sidecar, and treating it as one
// would let a machine-level problem quietly downgrade an export's
// fidelity.
func sidecarExists(fsys afero.Fs, path, cohortPath string) (bool, error) {
	_, err := fsys.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.LoadSidecar: stat "+path+": "+err.Error(),
			map[string]any{
				errors.DetailSPSSCohort:  cohortPath,
				errors.DetailSPSSSidecar: path,
			})
	}
}

// readSidecarDocument reads and structurally validates the document.
//
// Everything here is a statement about whether the file IS a sidecar
// this binary can read; nothing here consults the cohort. Freshness is
// a separate question asked afterwards, deliberately, so that an
// unreadable document reports what is wrong with it rather than being
// mislabelled stale.
func readSidecarDocument(fsys afero.Fs, cohortPath, path string) (*Document, error) {
	raw, err := afero.ReadFile(fsys, path)
	if err != nil {
		return nil, sidecarInvalid(cohortPath, path, "reading the file: "+err.Error())
	}
	var doc Document
	if err := stdjson.Unmarshal(raw, &doc); err != nil {
		return nil, sidecarInvalid(cohortPath, path, "the file is not valid sidecar JSON: "+err.Error())
	}
	// Kind first: a document from another producer is not a version
	// question, and "format_version 0 is not 1" would be a misleading
	// thing to say about a file that was never ours.
	if doc.Kind != SidecarKind {
		return nil, sidecarInvalid(cohortPath, path,
			"kind is "+strconv.Quote(doc.Kind)+", want "+strconv.Quote(SidecarKind))
	}
	// An unrecognised version is declined, never read optimistically. A
	// newer Pulse may have moved a slot this binary indexes, and
	// misreading a dictionary is worse than refusing one.
	if doc.FormatVersion != SidecarFormatVersion {
		return nil, sidecarInvalid(cohortPath, path,
			"format_version is "+strconv.Itoa(doc.FormatVersion)+", this binary reads "+strconv.Itoa(SidecarFormatVersion))
	}
	// A fingerprint that will not decode makes the authoritative verify
	// impossible, so the document cannot be trusted even if its cheap
	// pair happens to match.
	if _, ok := doc.Fingerprint.Digest(); !ok {
		return nil, sidecarInvalid(cohortPath, path,
			"the fingerprint sha256 is not a well-formed 32-byte digest")
	}
	return &doc, nil
}

// checkFresh is the cheap O(1) staleness comparison.
//
// Both halves are compared and both are reported, because which one
// moved is diagnostic: a changed size means the cohort was rewritten,
// a changed mtime alone can mean it was merely touched or copied. The
// verdict is the same either way — the document no longer provably
// describes these bytes — but a caller deciding whether to re-import or
// to re-stamp needs to see which.
func (d *Document) checkFresh(fsys afero.Fs, cohortPath, path string) error {
	info, err := fsys.Stat(cohortPath)
	if err != nil {
		// The cohort, not the sidecar. Staleness is a comparison and
		// there is nothing to compare against, so this is a plain file
		// fault rather than a sidecar verdict.
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.LoadSidecar: stat cohort "+cohortPath+": "+err.Error(),
			map[string]any{
				errors.DetailSPSSCohort:  cohortPath,
				errors.DetailSPSSSidecar: path,
			})
	}
	size := info.Size()
	if size < 0 {
		size = 0
	}
	gotSize, gotMod := uint64(size), info.ModTime().UnixNano()
	if gotSize == d.Fingerprint.SourceSize && gotMod == d.Fingerprint.SourceModTime {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_STALE,
		"spss: the metadata sidecar "+path+" no longer matches the cohort "+cohortPath+
			" — "+staleWhat(d.Fingerprint.SourceSize, gotSize, d.Fingerprint.SourceModTime, gotMod)+
			" changed since the sidecar was written, so its SPSS dictionary describes a different"+
			" version of this data; applying it would produce a .sav that looks authoritative and is wrong",
		map[string]any{
			errors.DetailSPSSCohort:  cohortPath,
			errors.DetailSPSSSidecar: path,
			errors.DetailSPSSExpected: map[string]any{
				"size":     d.Fingerprint.SourceSize,
				"mod_time": d.Fingerprint.SourceModTime,
			},
			errors.DetailSPSSActual: map[string]any{
				"size":     gotSize,
				"mod_time": gotMod,
			},
		})
}

// staleWhat names which half of the cheap pair moved, for the message.
func staleWhat(wantSize, gotSize uint64, wantMod, gotMod int64) string {
	switch {
	case wantSize != gotSize && wantMod != gotMod:
		return "its byte size and modification time have both"
	case wantSize != gotSize:
		return "its byte size has"
	default:
		return "its modification time has"
	}
}

// ---------------------------------------------------------------------------
// Authoritative verification
// ---------------------------------------------------------------------------

// VerifyDigest recomputes the cohort's full SHA-256 and compares it
// against the one this document recorded.
//
// It is the authoritative freshness answer, and the mirror of what
// `pulse index verify` does for the sidecar point-lookup index: the
// read path takes the cheap O(1) size+mtime pair on every call, and
// this is available for a verify-style pass that can afford to read the
// whole cohort. It closes the one gap the cheap check has — an in-place
// edit that preserves both size and mtime — at the cost of a full read,
// which is exactly why it is not on the export path.
//
// A mismatch is PULSE_SPSS_SIDECAR_STALE, the same verdict the cheap
// check returns, because it is the same fact discovered more expensively.
func (d *Document) VerifyDigest(fsys afero.Fs, cohortPath string) error {
	if d == nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Document.VerifyDigest: no document",
			map[string]any{errors.DetailSPSSCohort: cohortPath})
	}
	path := SidecarPath(cohortPath)
	want, ok := d.Fingerprint.Digest()
	if !ok {
		return sidecarInvalid(cohortPath, path,
			"the fingerprint sha256 is not a well-formed 32-byte digest")
	}
	if fsys == nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Document.VerifyDigest: no filesystem",
			map[string]any{errors.DetailSPSSCohort: cohortPath})
	}
	f, err := fsys.Open(cohortPath)
	if err != nil {
		return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss.Document.VerifyDigest: opening cohort "+cohortPath+": "+err.Error(),
			map[string]any{errors.DetailSPSSCohort: cohortPath, errors.DetailSPSSSidecar: path})
	}
	defer func() { _ = f.Close() }()

	got, ferr := encoding.ComputeFingerprint(f)
	if ferr != nil {
		return ferr
	}
	if got == want {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_STALE,
		"spss: the metadata sidecar "+path+" records a different SHA-256 than the cohort "+
			cohortPath+" now hashes to — the cohort's contents changed after the sidecar was"+
			" written, so its SPSS dictionary describes a different version of this data",
		map[string]any{
			errors.DetailSPSSCohort:   cohortPath,
			errors.DetailSPSSSidecar:  path,
			errors.DetailSPSSExpected: map[string]any{"sha256": d.Fingerprint.SHA256},
			errors.DetailSPSSActual:   map[string]any{"sha256": hex.EncodeToString(got[:])},
		})
}

// ---------------------------------------------------------------------------
// Normalisation
// ---------------------------------------------------------------------------

// Normalise fills in what an OLDER document left implicit, so every
// consumer sees one shape regardless of which Pulse wrote the file.
//
// Today that is exactly one slot. MRSet.Fields — the short-name ->
// Pulse-field-name projection of a response set's member list — was
// added additively AFTER the document shape shipped, under
// `omitempty`, and SidecarFormatVersion did not move for it because
// nothing was renamed or removed. So the version cannot tell an older
// document from a newer one, and an ABSENT `fields` key means "this
// document predates the slot", never "this set has no members".
// Treating it as an error would reject every sidecar written before the
// slot existed; the correct reading is to resolve the short names here,
// exactly as the writer would have.
//
// A `fields` array that is PRESENT but not the same length as
// `variables` is a different matter and IS rejected. The two are a
// parallel array whose entire contract is that they are index-for-index
// — an MC set may legitimately name one variable twice, so a consumer
// cannot re-derive the pairing by matching — and silently repairing a
// broken one would hand the caller a member list bound to the wrong
// columns. That is precisely the silent misread the slot was added to
// prevent.
//
// Normalise is idempotent: a document that needs nothing is untouched,
// and running it twice changes nothing the first run did not.
func (d *Document) Normalise() error {
	if why, ok := d.normalise(); !ok {
		return errors.NewCodedError(errors.PULSE_SPSS_SIDECAR_INVALID,
			"spss: this metadata sidecar cannot be read — "+why)
	}
	return nil
}

// normalise is the shared implementation, reporting a reason rather
// than an error so LoadSidecar can attach the cohort and sidecar paths
// its caller needs without re-wrapping and double-prefixing a message.
func (d *Document) normalise() (string, bool) {
	if d == nil {
		return "", true
	}
	return d.Payload.resolveMRSetFields()
}

// resolveMRSetFields backfills the short-name -> field-name projection.
//
// The lookup mirrors setFieldResolver in sidecar_build.go exactly:
// case-insensitive, because SPSS variable names are, and FIRST
// declaration wins, because that is the rule the import applied when it
// assigned columns. A member naming a variable the document has no
// entry for resolves to "" — the same "" the writer emits for one — so
// an old document and a new one agree entry for entry.
func (p *Payload) resolveMRSetFields() (string, bool) {
	if len(p.MultipleResponseSets) == 0 {
		return "", true
	}
	var byShortName map[string]string
	for i := range p.MultipleResponseSets {
		set := &p.MultipleResponseSets[i]
		if len(set.Fields) == len(set.Variables) {
			// Already resolved (or both empty). Nothing to do, and
			// nothing to second-guess: the writer's own resolution wins
			// over any re-derivation here.
			continue
		}
		if len(set.Fields) != 0 {
			return "multiple_response_sets[" + strconv.Itoa(i) + "] " + strconv.Quote(set.Name) +
				": fields has " + strconv.Itoa(len(set.Fields)) + " entries and variables has " +
				strconv.Itoa(len(set.Variables)) +
				"; they are a parallel array and must be index-for-index", false
		}
		if byShortName == nil {
			byShortName = make(map[string]string, len(p.Variables))
			for _, v := range p.Variables {
				key := strings.ToUpper(v.ShortName)
				if key == "" {
					continue
				}
				if _, dup := byShortName[key]; dup {
					continue
				}
				byShortName[key] = v.Name
			}
		}
		set.Fields = make([]string, len(set.Variables))
		for j, short := range set.Variables {
			set.Fields[j] = byShortName[strings.ToUpper(short)]
		}
	}
	return "", true
}

// ---------------------------------------------------------------------------
// Diagnostic constructors
// ---------------------------------------------------------------------------

// sidecarWarning builds one of the two non-fatal sidecar diagnostics.
//
// Both name the cohort and the sidecar path and nothing else. In
// particular an IGNORED warning does NOT say whether it suppressed a
// refusal or a healthy document, because LoadSidecar genuinely does not
// know — the flag skips the read, which is what makes it a total escape
// hatch — and a detail that could only ever be guessed at is worse than
// an absent one.
func sidecarWarning(code errors.Code, cohortPath, path string) *errors.CodedError {
	details := map[string]any{
		errors.DetailSPSSCohort:  cohortPath,
		errors.DetailSPSSSidecar: path,
	}
	var msg string
	switch code {
	case errors.PULSE_SPSS_SIDECAR_ABSENT:
		msg = "spss: no metadata sidecar found at " + path + "; the export will synthesise a" +
			" default SPSS dictionary from the .pulse schema alone, so value labels, measure" +
			" levels, print formats and missing-value specifications will not be restated." +
			" This is the normal state for a cohort that was never SPSS-derived"
	default:
		msg = "spss: the metadata sidecar " + path + " was not read because --ignore-sidecar was" +
			" set; the export will synthesise a default SPSS dictionary from the .pulse schema alone"
	}
	return errors.NewCodedErrorWithDetails(code, msg, details)
}

// sidecarInvalid builds the PULSE_SPSS_SIDECAR_INVALID refusal.
func sidecarInvalid(cohortPath, path, why string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_SIDECAR_INVALID,
		"spss: "+path+" is not a metadata sidecar this binary can read — "+why,
		map[string]any{
			errors.DetailSPSSCohort:  cohortPath,
			errors.DetailSPSSSidecar: path,
		})
}
