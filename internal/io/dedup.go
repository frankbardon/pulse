package io

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// Retro-dedup: convert an EXISTING single-file cohort to a grouped
// (format 0x02) cohort.
//
// Import-time declaration (ImportJob.Groups) only helps prospectively;
// every cohort already on disk would otherwise stay 0x01 unless it is
// re-imported from a source that may be gone. DedupJob rewrites the
// cohort itself, through exactly the machinery import uses — the same
// GroupDecl lowering, the same encoding.GroupEncoder, the same
// encoding.DedupGate width and ratio floors with the same Strict
// escalation, the same PlanConstantElision and the same candidate
// detector — so a group judged on import and the same group judged on
// retro-dedup cannot get different verdicts.
//
// The source is read through its LOGICAL record stream, so a cohort
// that is already grouped is regrouped from scratch: re-deduping a 0x02
// cohort with different declarations replaces its groups wholesale.
//
// Memory is O(dictionaries), not O(cohort). The dictionaries precede
// the records on the wire, so the physical rows are spooled to a temp
// file beside the target and the preamble is written only after the
// full pass; the final cohort is then assembled into a second temp file,
// fsynced and renamed over the target. Any failure before the rename —
// a member that varies within its key, a strict gate finding, an I/O
// error, cancellation — removes both temp files and leaves the target
// (and, in place, the original cohort) byte-identical.
//
// Passes over the source: one to encode, plus one observation pass
// shared by constant detection (ElideConstants) and candidate detection
// (SuggestGroups) when either is asked for. Elision cannot ride the
// encode pass because the constant group changes the encoder.

const (
	// dedupTempPattern is the staging file the finished cohort is
	// assembled into before the rename. It sits beside the target so the
	// rename never crosses a filesystem.
	dedupTempPattern = ".dedup-*.tmp"
	// dedupSpoolPattern holds the physical rows while the dictionaries
	// are still being built.
	dedupSpoolPattern = ".dedup-spool-*.tmp"
	// dedupIOBuffer is the read and write buffer size of every pass.
	dedupIOBuffer = 1 << 20
)

// DedupJob converts an existing single-file cohort to a grouped cohort.
// Shard archives are not this job's input — the caller (pulse.Dedup)
// refuses them with a coded error before a byte is read.
type DedupJob struct {
	// FS is the filesystem both Source and Target live on. Required.
	FS afero.Fs
	// Source is the cohort to convert. Any supported format version.
	Source string
	// Target is where the converted cohort is written. Empty (or equal
	// to Source) rewrites Source IN PLACE. A different path must not
	// exist yet: the job never overwrites a file it was not asked to
	// convert.
	Target string
	// Groups declares the parent groups, exactly as ImportJob.Groups.
	Groups []GroupDecl
	// ElideConstants stores every field holding one value on every
	// record once, as ImportJob.ElideConstants. Declared members are
	// never elided.
	ElideConstants bool
	// DedupRatioFloor and StrictDedup are the viability gate's policy,
	// as on ImportJob.
	DedupRatioFloor float64
	StrictDedup     bool
	// SuggestGroups runs candidate detection over the source (before any
	// rewrite) and returns it on DedupReport.GroupCandidates. With no
	// Groups and no ElideConstants the job is detection-only and writes
	// nothing.
	SuggestGroups bool
}

// DedupReport describes a retro-dedup run.
type DedupReport struct {
	// Source and Target are the paths read and written; InPlace is true
	// when they are the same file.
	Source  string `json:"source"`
	Target  string `json:"target"`
	InPlace bool   `json:"in_place"`
	// Rewritten is false when nothing was written: a detection-only run,
	// or one whose every declared group was dropped by the width floor
	// with no elision to apply. The target is then untouched.
	Rewritten bool `json:"rewritten"`
	// Records is the record count of the source (and of the rewrite).
	Records int64 `json:"records"`
	// FormatVersionBefore/After, BytesBefore/After and
	// StrideBefore/After describe the source and the written cohort.
	// Without a rewrite the After figures repeat the Before ones.
	FormatVersionBefore int   `json:"format_version_before"`
	FormatVersionAfter  int   `json:"format_version_after"`
	BytesBefore         int64 `json:"bytes_before"`
	BytesAfter          int64 `json:"bytes_after"`
	StrideBefore        int   `json:"stride_before"`
	StrideAfter         int   `json:"stride_after"`
	// ElidedConstants names the fields ElideConstants stored once.
	ElidedConstants []string `json:"elided_constants,omitempty"`
	// Groups reports every declaration's verdict and figures, in
	// declaration order — the same GroupReport import produces.
	Groups []GroupReport `json:"groups,omitempty"`
	// GroupWarnings carries the viability gate's findings
	// (PULSE_GROUP_TOO_NARROW, PULSE_DEDUP_LOW_RATIO).
	GroupWarnings []*errors.CodedError `json:"group_warnings,omitempty"`
	// GroupCandidates is the SuggestGroups detection report, measured
	// over the source's records.
	GroupCandidates *GroupDetection `json:"group_candidates,omitempty"`
}

// Run executes the job. See the package comment above DedupJob for the
// passes and the atomicity guarantee.
func (j *DedupJob) Run(ctx context.Context) (*DedupReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("DedupJob.FS is required")
	}
	if len(j.Groups) == 0 && !j.ElideConstants && !j.SuggestGroups {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_GROUP_DECLARATION_INVALID,
			"dedup needs at least one parent-group declaration, constant elision, or group suggestion — there is nothing to do",
			map[string]any{"cohort": j.Source})
	}
	target := j.Target
	if target == "" {
		target = j.Source
	}
	rep := &DedupReport{Source: j.Source, Target: target, InPlace: filepath.Clean(target) == filepath.Clean(j.Source)}
	if !rep.InPlace {
		exists, err := afero.Exists(j.FS, target)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("checking dedup target %s", target))
		}
		if exists {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				fmt.Sprintf("dedup target %s already exists; pick a new path, or omit the target to rewrite the source in place", target),
				map[string]any{"cohort": j.Source, "target": target})
		}
	}
	fi, err := j.FS.Stat(j.Source)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, fmt.Sprintf("opening cohort for dedup: %s", j.Source))
	}
	rep.BytesBefore = fi.Size()

	// Declarations: resolved and width-screened against the logical
	// schema before any record is read, as import does.
	src, err := j.openLogical(ctx)
	if err != nil {
		return nil, err
	}
	_ = src.close()
	flat := src.flat
	rep.FormatVersionBefore = int(src.version)
	rep.StrideBefore = src.stored.RecordByteSize()

	gate := encx.DedupGate{RatioFloor: j.DedupRatioFloor, Strict: j.StrictDedup}
	var (
		specs      []encx.GroupSpec
		screen     []encx.GroupViability
		groupWarns []*errors.CodedError
	)
	if declared := groupSpecs(j.Groups); len(declared) > 0 {
		if _, err := encx.NewGroupEncoder(flat, declared); err != nil {
			return nil, err
		}
		if specs, screen, groupWarns, err = gate.ScreenWidths(flat, declared); err != nil {
			return nil, err
		}
	}

	// Observation pass: constancy and/or candidate detection.
	allSpecs := append([]encx.GroupSpec(nil), specs...)
	if j.ElideConstants || j.SuggestGroups {
		plan, detection, rows, err := j.observe(ctx, flat, specs, gate.Floor())
		if err != nil {
			return nil, err
		}
		rep.Records = rows
		rep.GroupCandidates = detection
		if plan != nil && plan.Spec != nil {
			rep.ElidedConstants = plan.Fields
			allSpecs = append(allSpecs, *plan.Spec)
		}
	}

	if len(allSpecs) == 0 {
		// Nothing to write: detection only, or every declared group was
		// dropped by the width floor and there is nothing to elide.
		if len(j.Groups) > 0 {
			rep.Groups = groupReports(flat, j.Groups, screen, nil)
		}
		rep.GroupWarnings = groupWarns
		if !j.ElideConstants && !j.SuggestGroups {
			rep.Records, _, _ = src.stored.RecordCountForPayload(rep.BytesBefore - src.preamble)
		}
		rep.FormatVersionAfter, rep.BytesAfter, rep.StrideAfter = rep.FormatVersionBefore, rep.BytesBefore, rep.StrideBefore
		return rep, nil
	}

	written, rows, ratio, ratioWarns, err := j.rewrite(ctx, target, flat, allSpecs, specs, gate)
	if err != nil {
		return nil, err
	}
	rep.Rewritten = true
	rep.Records = rows
	rep.FormatVersionAfter = int(written.RequiredFormatVersion())
	rep.StrideAfter = written.RecordByteSize()
	if fi, err := j.FS.Stat(target); err == nil {
		rep.BytesAfter = fi.Size()
	}
	if len(j.Groups) > 0 {
		rep.Groups = groupReports(written, j.Groups, screen, ratio)
	}
	rep.GroupWarnings = append(groupWarns, ratioWarns...)
	return rep, nil
}

// logicalSource is an open cohort positioned at its first logical
// record.
type logicalSource struct {
	f        afero.File
	version  byte
	preamble int64            // header + schema block bytes
	stored   *encoding.Schema // the schema as stored (grouped or not)
	flat     *encoding.Schema // its logical (ungrouped) view
	rows     io.Reader        // the logical record stream
}

func (s *logicalSource) close() error { return s.f.Close() }

// openLogical opens Source and returns its logical record stream. The
// stream checks ctx on every read, so a cancelled job stops mid-pass.
func (j *DedupJob) openLogical(ctx context.Context) (*logicalSource, error) {
	f, err := j.FS.Open(j.Source)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, fmt.Sprintf("opening cohort for dedup: %s", j.Source))
	}
	br := bufio.NewReaderSize(f, dedupIOBuffer)
	cr := &preambleCounter{r: br}
	stored, version, err := encx.ReadPreamble(cr)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	lr, flat, err := encx.NewLogicalStream(br, stored)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &logicalSource{f: f, version: version, preamble: cr.n, stored: stored, flat: flat, rows: ctxReader{ctx: ctx, r: lr}}, nil
}

// observe runs the observation pass: every logical record is folded
// into a ConstantDetector (ElideConstants) and a candidate detector
// (SuggestGroups). Fields claimed by an admitted declared group are
// reserved from both.
func (j *DedupJob) observe(ctx context.Context, flat *encoding.Schema, specs []encx.GroupSpec, floor float64) (*encx.ConstantPlan, *GroupDetection, int64, error) {
	src, err := j.openLogical(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	defer src.close()

	var det *encx.ConstantDetector
	if j.ElideConstants {
		if det, err = encx.NewConstantDetector(flat); err != nil {
			return nil, nil, 0, err
		}
	}
	var (
		cd  *candidateDetector
		l   measuredLayout
		pad []byte // measured rows always carry the full null bitmap
	)
	if j.SuggestGroups {
		l = newMeasuredLayout(flat)
		reserved := make([]bool, len(flat.Fields))
		for _, sp := range specs {
			for _, name := range sp.Members {
				if fi := fieldIndex(flat, name); fi >= 0 {
					reserved[fi] = true
				}
			}
		}
		cd = newCandidateDetector(&l, reserved)
		pad = make([]byte, l.stride)
	}
	rows, err := encx.ForEachRecord(src.rows, flat.RecordByteSize(), func(rec []byte) error {
		if det != nil {
			if err := det.Observe(rec); err != nil {
				return err
			}
		}
		if cd != nil {
			if len(rec) == l.stride {
				cd.observe(rec)
			} else {
				clear(pad)
				copy(pad, rec)
				cd.observe(pad)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, 0, err
	}

	var plan *encx.ConstantPlan
	if det != nil {
		if plan, err = encx.PlanConstantElision(det, groupMemberNames(specs)); err != nil {
			return nil, nil, 0, err
		}
	}
	var detection *GroupDetection
	if cd != nil {
		pre, err := preambleBytes(flat)
		if err != nil {
			return nil, nil, 0, err
		}
		if detection, err = cd.finish(flat, floor, rows, pre+rows*int64(flat.RecordByteSize())); err != nil {
			return nil, nil, 0, err
		}
	}
	return plan, detection, rows, nil
}

// rewrite runs the encode pass into a spool, applies the ratio floor,
// and — only if the gate lets it through — assembles the cohort into a
// temp file beside target, fsyncs it and renames it over target.
// declared are the admitted declared specs (allSpecs' leading groups).
func (j *DedupJob) rewrite(ctx context.Context, target string, flat *encoding.Schema, allSpecs, declared []encx.GroupSpec, gate encx.DedupGate) (*encoding.Schema, int64, []encx.GroupViability, []*errors.CodedError, error) {
	fail := func(err error) (*encoding.Schema, int64, []encx.GroupViability, []*errors.CodedError, error) {
		return nil, 0, nil, nil, err
	}
	enc, err := encx.NewGroupEncoder(flat, allSpecs)
	if err != nil {
		return fail(err)
	}
	dir, base := filepath.Dir(target), filepath.Base(target)

	spool, err := afero.TempFile(j.FS, dir, base+dedupSpoolPattern)
	if err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("creating dedup spool beside %s", target)))
	}
	spoolName := spool.Name()
	defer func() {
		_ = spool.Close()
		_ = j.FS.Remove(spoolName)
	}()

	src, err := j.openLogical(ctx)
	if err != nil {
		return fail(err)
	}
	sw := bufio.NewWriterSize(spool, dedupIOBuffer)
	rows, err := enc.EncodeStream(sw, src.rows)
	_ = src.close()
	if err != nil {
		return fail(err)
	}
	if err := sw.Flush(); err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, "flushing dedup spool"))
	}
	written := enc.Schema()

	// Ratio floor: over the dictionaries the full pass built, BEFORE the
	// target is touched, so a strict refusal leaves nothing behind.
	ratio, ratioWarns, err := gate.AssessRatios(written, declared, rows)
	if err != nil {
		return fail(err)
	}

	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, "rewinding dedup spool"))
	}
	tmp, err := afero.TempFile(j.FS, dir, base+dedupTempPattern)
	if err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("creating temp file for dedup of %s", target)))
	}
	tmpName := tmp.Name()
	abort := func(e error) (*encoding.Schema, int64, []encx.GroupViability, []*errors.CodedError, error) {
		_ = tmp.Close()
		_ = j.FS.Remove(tmpName)
		return fail(e)
	}
	tw := bufio.NewWriterSize(tmp, dedupIOBuffer)
	if err := encx.WritePreamble(tw, written); err != nil {
		return abort(err)
	}
	if _, err := io.Copy(tw, ctxReader{ctx: ctx, r: spool}); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("writing deduped cohort for %s", target)))
	}
	if err := tw.Flush(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("flushing deduped cohort for %s", target)))
	}
	// fsync BEFORE the rename: a rename that lands while the temp file's
	// bytes are still only in the page cache publishes a name that a
	// crash can leave pointing at nothing.
	if err := tmp.Sync(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("syncing deduped cohort for %s", target)))
	}
	if err := tmp.Close(); err != nil {
		_ = j.FS.Remove(tmpName)
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("closing deduped cohort for %s", target)))
	}
	if err := ctx.Err(); err != nil {
		_ = j.FS.Remove(tmpName)
		return fail(err)
	}
	if err := j.FS.Rename(tmpName, target); err != nil {
		_ = j.FS.Remove(tmpName)
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("renaming %s onto %s", tmpName, target)))
	}
	return written, rows, ratio, ratioWarns, nil
}

// ctxReader fails a read once ctx is done, so a long pass stops at the
// next record boundary instead of running to the end.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// fieldIndex is the index of the field named name in s, or -1.
func fieldIndex(s *encoding.Schema, name string) int {
	for i := range s.Fields {
		if s.Fields[i].Name == name {
			return i
		}
	}
	return -1
}

// preambleCounter counts the bytes read through it.
type preambleCounter struct {
	r io.Reader
	n int64
}

func (c *preambleCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
