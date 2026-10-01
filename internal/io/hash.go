package io

import (
	"github.com/frankbardon/pulse/types"
)

// exportJobHashShape projects the canonical hash inputs for an
// ExportJob. The shape mirrors ExportJob's REQUEST-side slots (the
// fields that drive the export adapter's behaviour) and intentionally
// omits the runtime carriers — Target, FS, LabelResolver — which are
// neither serialisable nor part of the job's semantic identity. The
// projection keeps the hash byte-identical to a pre-export-overlay
// ExportJob whenever the new IncludeOverlays slot is left at its
// default (nil) — the slot carries `omitempty` so json.Marshal drops
// it, and the canonical-hash pipeline produces the legacy byte form.
//
// The hash covers:
//
//   - Source — the input .pulse path identifies the cohort being
//     exported. Two jobs against different cohorts are not
//     interchangeable.
//   - Includes — the projection list. Two jobs that emit different
//     field subsets produce different artefacts.
//   - Labels — the label-binding spec. Two jobs that resolve
//     categorical values differently produce different artefacts.
//   - IncludeOverlays — the overlay opt-out toggle. Two jobs with
//     identical Source / Includes / Labels but different overlay
//     emission produce different artefacts, so the cache must
//     distinguish.
//
// LabelResolver is intentionally excluded — it is a runtime carrier
// built from Labels + the embedder's registered tables, not a
// distinct input. Two jobs with identical Labels but different
// LabelResolver pointers share the same hash by design.
type exportJobHashShape struct {
	Source          string                `json:"source,omitempty"`
	Includes        []string              `json:"includes,omitempty"`
	Labels          []*types.LabelBinding `json:"labels,omitempty"`
	IncludeOverlays *bool                 `json:"include_overlays,omitempty"`
}

// Hash returns the canonical content hash of the ExportJob. Two jobs
// with the same logical export shape produce the same hash; two jobs
// with different IncludeOverlays settings (nil / *true / *false)
// produce distinct hashes so consumers caching export artefacts by
// hash key do not return stale results when the overlay opt-out
// flips.
//
// IncludeOverlays participates in the hash composition per
// research/export-embedding-shape.md §8: the slot is a request-side
// input to the adapter, so two jobs differing only in IncludeOverlays
// produce different export artefacts (overlays emitted in one,
// dropped in the other) and the cache key MUST distinguish.
//
// The hash is namespaced under "export_job" to keep ExportJob and
// ConvertJob hashes disjoint even when their shapes overlap.
func (j *ExportJob) Hash() string {
	if j == nil {
		return types.CanonicalHash("export_job", (*exportJobHashShape)(nil))
	}
	return types.CanonicalHash("export_job", &exportJobHashShape{
		Source:          j.Source,
		Includes:        j.Includes,
		Labels:          j.Labels,
		IncludeOverlays: j.IncludeOverlays,
	})
}

// convertJobHashShape projects the canonical hash inputs for a
// ConvertJob. Mirrors exportJobHashShape; KeepPulseAt + SampleRows
// participate because they change the convert artefact (the
// intermediate file path) and the inference behaviour respectively.
// Source and Target are runtime carriers (Reader / Writer
// interfaces) so neither is serialisable nor part of the job's
// semantic identity for cache-keying purposes.
type convertJobHashShape struct {
	KeepPulseAt     string                `json:"keep_pulse_at,omitempty"`
	SampleRows      int                   `json:"sample_rows,omitempty"`
	Includes        []string              `json:"includes,omitempty"`
	Labels          []*types.LabelBinding `json:"labels,omitempty"`
	IncludeOverlays *bool                 `json:"include_overlays,omitempty"`
}

// Hash returns the canonical content hash of the ConvertJob. Same
// rules as ExportJob.Hash — IncludeOverlays participates so cache
// keys differ between overlay-bearing and overlay-stripped converts.
//
// The hash is namespaced under "convert_job" to keep ConvertJob
// hashes disjoint from ExportJob hashes.
func (j *ConvertJob) Hash() string {
	if j == nil {
		return types.CanonicalHash("convert_job", (*convertJobHashShape)(nil))
	}
	return types.CanonicalHash("convert_job", &convertJobHashShape{
		KeepPulseAt:     j.KeepPulseAt,
		SampleRows:      j.SampleRows,
		Includes:        j.Includes,
		Labels:          j.Labels,
		IncludeOverlays: j.IncludeOverlays,
	})
}
