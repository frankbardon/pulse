package pulse

import (
	"context"
	stderrors "errors"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
)

// DedupOptions configures Pulse.Dedup. It carries the same parent-group
// declaration import takes (pio.GroupDecl) and the same viability-gate
// policy, so a declaration that works on `pulse import --group` works
// unchanged on an existing cohort.
type DedupOptions struct {
	// Groups declares the parent groups (key + the members it
	// determines). A cohort that is already grouped is regrouped from
	// scratch: its existing groups are replaced, not extended.
	Groups []pio.GroupDecl
	// Out writes the converted cohort to a NEW path instead of
	// rewriting the source in place. It must not exist. Empty (the
	// default) rewrites in place.
	Out string
	// ElideConstants also stores every field holding a single value on
	// every record once (as `pulse import --elide-constants`).
	ElideConstants bool
	// RatioFloor is the rows-per-distinct-tuple floor below which a
	// group draws PULSE_DEDUP_LOW_RATIO; 0 selects
	// internal/encoding.DefaultDedupRatioFloor (2).
	RatioFloor float64
	// Strict turns the gate's warnings (PULSE_GROUP_TOO_NARROW,
	// PULSE_DEDUP_LOW_RATIO) into a fatal error of the same code, raised
	// before anything is written.
	Strict bool
	// SuggestGroups detects candidate parent groups over the cohort's
	// records and returns them on DedupResult.GroupCandidates, each with
	// a ready-to-use declaration. Alone (no Groups, no ElideConstants)
	// the call is read-only.
	SuggestGroups bool
}

// DedupResult is Pulse.Dedup's report: the rewrite's figures
// (pio.DedupReport, inlined in JSON) plus the sidecars the rewrite
// invalidated.
type DedupResult struct {
	pio.DedupReport
	// InvalidatedSidecars names each sidecar beside the cohort that an
	// IN-PLACE rewrite invalidated — the point-lookup index and its
	// manifest, the SPSS metadata sidecar — with the command that
	// rebuilds it. Nothing is rebuilt: a dedup changes the cohort's
	// length, so each sidecar self-invalidates through its own size
	// fingerprint, and rebuilding is the caller's to schedule. Empty
	// for an --out rewrite (the source's sidecars still describe the
	// untouched source) and when nothing was written.
	InvalidatedSidecars []StaleSidecar `json:"invalidated_sidecars,omitempty"`
	// SidecarWarnings carries a failure to ENUMERATE those sidecars.
	// The rewrite has already succeeded when this is set, so it is a
	// warning, never an error: any sidecar beside the cohort must be
	// assumed stale.
	SidecarWarnings []*errors.CodedError `json:"sidecar_warnings,omitempty"`
}

// Warnings returns every non-fatal coded finding of the run in report
// order: the viability gate's, then the sidecar scan's.
func (r *DedupResult) Warnings() []*errors.CodedError {
	out := append([]*errors.CodedError(nil), r.GroupWarnings...)
	return append(out, r.SidecarWarnings...)
}

// Dedup converts an existing single-file cohort to a deduplicated
// (format 0x02) cohort grouped by opts.Groups — retro-dedup, the
// existing-cohort twin of `pulse import --group`.
//
// The byte work is pio.DedupJob: the source is read through its logical
// record stream, encoded through the same internal/encoding.GroupEncoder import
// uses into a temp-file spool (memory stays O(dictionaries)), judged by
// the same internal/encoding.DedupGate (width floor before the pass, ratio floor
// after it and BEFORE anything is written, Strict escalating either),
// then assembled into a temp file beside the target, fsynced and
// renamed over it. Any failure leaves the original byte-identical.
//
// Refusals are coded errors: SERVICE_VALIDATION for a shard archive or
// an anchored shard (grouped shards are not accepted by the archive
// tooling yet) and for an --out path that already exists;
// SERVICE_RESOURCE for a missing cohort; the PULSE_GROUP_* family for a
// bad declaration or a member that varies within its key; and, under
// Strict, PULSE_GROUP_TOO_NARROW / PULSE_DEDUP_LOW_RATIO.
//
// Output is transparent: every request returns the same Response
// against the converted cohort as against the original.
func (p *Pulse) Dedup(ctx context.Context, path string, opts DedupOptions) (*DedupResult, error) {
	return observed(p, ctx, opSpec{kind: observe.OpDedup, path: path}, func(ctx context.Context) (*DedupResult, error) {
		return p.dedup(ctx, path, opts)
	})
}

func (p *Pulse) dedup(ctx context.Context, path string, opts DedupOptions) (*DedupResult, error) {
	if err := p.svc.DedupPreflight(ctx, path); err != nil {
		return nil, err
	}
	job := &pio.DedupJob{
		FS:              p.fsys,
		Source:          path,
		Target:          opts.Out,
		Groups:          opts.Groups,
		ElideConstants:  opts.ElideConstants,
		DedupRatioFloor: opts.RatioFloor,
		StrictDedup:     opts.Strict,
		SuggestGroups:   opts.SuggestGroups,
	}
	rep, err := job.Run(ctx)
	if err != nil {
		return nil, err
	}
	res := &DedupResult{DedupReport: *rep}
	if rep.Rewritten && rep.InPlace {
		p.touchManaged(ctx, path)
		sidecars, scanErr := p.InvalidatedSidecars(ctx, path)
		res.InvalidatedSidecars = sidecars
		if scanErr != nil {
			res.SidecarWarnings = append(res.SidecarWarnings, sidecarScanWarning(path, scanErr))
		}
	}
	return res, nil
}

// sidecarScanWarning turns a failed sidecar enumeration into a coded
// warning, keeping the failure's own code when it has one so
// `pulse errors lookup` still works on it.
func sidecarScanWarning(cohort string, err error) *errors.CodedError {
	code := errors.SERVICE_RESOURCE
	var coded *errors.CodedError
	if stderrors.As(err, &coded) {
		code = coded.Code
	}
	return errors.NewCodedErrorWithDetails(code,
		"the rewrite succeeded but the sidecars beside the cohort could not be enumerated; "+
			"any point-lookup index or SPSS metadata sidecar here is now stale and must be rebuilt manually",
		map[string]any{"cohort": cohort, "error": err.Error()})
}
