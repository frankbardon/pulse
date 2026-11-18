package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// Layout values of CohortZoneResult.Layout.
const (
	CohortLayoutSingleFile   = "single_file"
	CohortLayoutShardArchive = "shard_archive"
)

// CohortZoneResult reports one SetCohortZoneField call: the default
// zone field before and after, whether any byte moved, the cohort's
// layout, how many shard payloads an archive rewrite re-emitted, and the
// mandatory PULSE_SHARD_ZONE_REWRITTEN warning of an archive rewrite.
//
// InvalidatedSidecars and SidecarWarnings are filled by the facade
// (Pulse.SetCohortZoneField): the sidecars beside the cohort the rewrite
// staled, and any failure to enumerate or carry them.
type CohortZoneResult struct {
	// Cohort is the path rewritten (or left alone, when unchanged).
	Cohort string `json:"cohort"`
	// ZoneField is the default zone field the cohort declares now; ""
	// after a clear.
	ZoneField string `json:"zone_field"`
	// PreviousZoneField is the default it declared before the call.
	PreviousZoneField string `json:"previous_zone_field"`
	// Changed is false when the cohort already declared ZoneField: then
	// nothing was written and no sidecar went stale.
	Changed bool `json:"changed"`
	// Layout is "single_file" or "shard_archive".
	Layout string `json:"layout"`
	// ShardsRewritten is the number of shard payloads an archive rewrite
	// re-emitted (0 for a single file).
	ShardsRewritten int `json:"shards_rewritten,omitempty"`
	// Warnings carries PULSE_SHARD_ZONE_REWRITTEN on a changed archive.
	// Shaped like the descriptor envelope's warning entries; never nil.
	Warnings []encx.CohesionWarning `json:"warnings"`
	// InvalidatedSidecars names each sidecar beside the cohort the
	// rewrite staled (the point-lookup index; the SPSS metadata sidecar
	// only when it could not be carried), with its rebuild command.
	InvalidatedSidecars []StaleSidecar `json:"invalidated_sidecars,omitempty"`
	// SidecarWarnings carries a failure to enumerate, or to carry, the
	// sidecars beside the cohort; the rewrite itself has succeeded.
	SidecarWarnings []*errors.CodedError `json:"sidecar_warnings,omitempty"`
}

// SetCohortZoneField declares field as the default zone field (format
// 0x02 ZONE section) of the cohort at path — "" clears it — through a
// SCHEMA-ONLY atomic rewrite: the record region is copied verbatim and
// the result is published by one rename, so any failure leaves the
// cohort byte-identical. An unchanged default writes nothing.
//
// A single file is rewritten in place (encx.SetZoneFieldFile). A shard
// archive rewrites its canonical `_schema.pulse` AND every shard
// payload's schema block — the shards of one archive must agree on the
// default (ValidateStructuralCohesion) — and reports the mandatory
// PULSE_SHARD_ZONE_REWRITTEN warning plus the per-shard point-lookup
// sidecars it staled.
//
// Refusals: SERVICE_VALIDATION for an anchored shard path (a shard
// cannot diverge from its archive) and, with details.option
// "zone_field", for a name resolving to no field or to a non-categorical
// one (encoding.Schema.ValidateZoneField, re-coded by
// encx.ZoneFieldOptionRefusal); PULSE_TIMEZONE_UNKNOWN for a dictionary
// entry that is not a zone; SERVICE_RESOURCE for a missing cohort; the
// archive's own open codes.
func (s *Service) SetCohortZoneField(ctx context.Context, path, field string) (*CohortZoneResult, error) {
	if archivePath, anchor, ok := splitAnchorPath(path); ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			fmt.Sprintf("a default zone field is set on the whole archive, not one shard (%s): every shard of an archive must declare the same default — run it on %s", path, archivePath),
			map[string]any{"cohort": path, "archive": archivePath, "shard": anchor,
				"option": encx.ZoneFieldOption, "reason": "anchored_shard"})
	}
	archive, err := s.pathIsShardArchive(path)
	if err != nil {
		return nil, err
	}
	if archive {
		return s.setArchiveZoneField(ctx, path, field)
	}
	rep, err := encx.SetZoneFieldFile(s.fs.Fs(), path, field)
	if err != nil {
		return nil, err
	}
	return &CohortZoneResult{Cohort: path, ZoneField: rep.Current, PreviousZoneField: rep.Previous,
		Changed: rep.Changed, Layout: CohortLayoutSingleFile, Warnings: []encx.CohesionWarning{}}, nil
}

// setArchiveZoneField is SetCohortZoneField over a shard archive. The
// new canonical schema is judged first (its dictionaries are the union
// every shard's is a prefix of, so a canonical that passes admits every
// shard). The DIST trailer carries every shard's counts — a zone
// rewrite moves no record word — re-stamped with the identity of the
// re-emitted payload, which a pre-pass learns before `_schema.pulse`
// (written first) is (the double read an archive-moving add accepts,
// U73 #720).
func (s *Service) setArchiveZoneField(ctx context.Context, path, field string) (*CohortZoneResult, error) {
	fsys := s.fs.Fs()
	a, err := openArchiveFile(fsys, path)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	doc, err := readSchemaDocEntry(a.arch)
	if err != nil {
		return nil, err
	}
	res := &CohortZoneResult{Cohort: path, ZoneField: field, PreviousZoneField: doc.Schema.ZoneField,
		Layout: CohortLayoutShardArchive, Warnings: []encx.CohesionWarning{}}
	if doc.Schema.ZoneField == field {
		return res, nil
	}
	next := encx.WithZoneField(doc.Schema, field)
	if err := encx.ZoneFieldOptionRefusal(next.ValidateZoneField()); err != nil {
		return nil, err
	}
	// An unreadable index manifest aborts before anything is written.
	manifest, indexEntries, err := readArchiveIndexes(fsys, path)
	if err != nil {
		return nil, err
	}

	stored := &archiveShards{a: a}
	for _, e := range a.arch.Entries() {
		if e.Name != encx.ReservedSchemaName {
			stored.names = append(stored.names, e.Name)
		}
	}
	rewrite := func(name string, payload []byte) ([]byte, error) {
		out, _, err := encx.SetZoneFieldBytes(payload, field)
		if err != nil {
			return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
				fmt.Sprintf("re-declaring the default zone field of shard %q", name))
		}
		return out, nil
	}
	aggregate, err := sumStoredRecords(stored)
	if err != nil {
		return nil, err
	}
	prior := readPriorDistinct(a.arch)
	pass := s.newDistinctPass(next)
	refs := keptShardRefs(a, stored.names)
	fresh := map[string][]encx.DistinctCount{}
	for i := range refs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		payload, err := stored.loadShard(i)
		if err != nil {
			return nil, err
		}
		if payload, err = rewrite(refs[i].Name, payload); err != nil {
			return nil, err
		}
		refs[i].Stored = encx.PayloadIdentity(payload)
		if prior.Stale(refs[i].Name, refs[i].Current) {
			counts, err := pass.count(bytes.NewReader(payload), refs[i].Name)
			if err != nil {
				return nil, err
			}
			fresh[refs[i].Name] = counts
		}
	}
	dist := encx.NextArchiveDistinct(next, refs, prior, fresh)

	w, err := newArchiveWriter(fsys, path)
	if err != nil {
		return nil, err
	}
	defer w.abort()
	if err := w.writeSchemaDoc(next, aggregate, stored.shardCount(), dist); err != nil {
		return nil, err
	}
	if err := writeStoredShards(w, a, stored, rewrite); err != nil {
		return nil, err
	}
	// The source handle is released before the rename replaces its file.
	if err := a.Close(); err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("closing archive %s", path))
	}
	if err := w.commit(); err != nil {
		return nil, err
	}
	res.Changed = true
	res.ShardsRewritten = stored.shardCount()
	if manifest != nil {
		// Every shard payload's bytes moved, so every per-shard sidecar's
		// fingerprint is stale.
		res.InvalidatedSidecars = staleArchiveSidecars(path, manifest, indexEntries)
	}
	res.Warnings = append(res.Warnings, zoneRewrittenWarning(path, res))
	return res, nil
}

// zoneRewrittenWarning renders the mandatory PULSE_SHARD_ZONE_REWRITTEN
// warning of an archive zone rewrite.
func zoneRewrittenWarning(archive string, res *CohortZoneResult) encx.CohesionWarning {
	what := fmt.Sprintf("declared %q as the default zone field", res.ZoneField)
	if res.ZoneField == "" {
		what = fmt.Sprintf("cleared the default zone field %q", res.PreviousZoneField)
	}
	return encx.CohesionWarning{
		Code: string(errors.PULSE_SHARD_ZONE_REWRITTEN),
		Message: fmt.Sprintf("%s of %s: the canonical schema and all %d shard payload(s) were rewritten (records copied verbatim) and the archive republished",
			what, archive, res.ShardsRewritten),
		Details: map[string]any{
			"archive":             archive,
			"zone_field":          res.ZoneField,
			"previous_zone_field": res.PreviousZoneField,
			"shards_rewritten":    res.ShardsRewritten,
		},
	}
}
