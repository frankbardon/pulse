package pulse

import (
	"context"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/service"
	"github.com/frankbardon/pulse/observe"
)

// CohortZoneResult is the report of SetCohortZoneField /
// ClearCohortZoneField: the cohort's default zone field before
// (PreviousZoneField) and after (ZoneField), whether any byte moved
// (Changed false: the cohort already declared it, nothing was written),
// its layout ("single_file" / "shard_archive"), the shard payloads an
// archive rewrite re-emitted, the mandatory PULSE_SHARD_ZONE_REWRITTEN
// warning of an archive rewrite, and the sidecars the rewrite staled.
type CohortZoneResult = service.CohortZoneResult

// SetCohortZoneField declares field as the DEFAULT per-row zone field of
// the cohort at path — the format 0x02 ZONE section an import's
// `--zone-field` writes (ImportJob.ZoneField) — on an existing cohort,
// so every zone-capable slot resolves per row from it unless a request
// overrides (slot tz > slot tz_field > request time_zone_field > request
// time_zone > the cohort default > Options.DefaultTimeZone > UTC).
//
// The rewrite is SCHEMA-ONLY and ATOMIC: only the header's version byte
// and the schema block are re-emitted, the records copied verbatim, the
// result published by one rename — any failure leaves the cohort
// byte-identical. Setting the default a cohort already declares writes
// nothing. A flat cohort becomes 0x02; clearing the default
// (ClearCohortZoneField) returns it to its original bytes.
//
// A shard archive is rewritten whole — its canonical `_schema.pulse` and
// every shard payload, since the shards of one archive must agree on the
// default — with the mandatory PULSE_SHARD_ZONE_REWRITTEN warning. An
// anchored shard path (archive.pulse#shard.pulse) is refused.
//
// The rule is encoding.Schema.ValidateZoneField, as at import: field
// must name a categorical_u8 / u16 / u32 field (else SERVICE_VALIDATION,
// details.option "zone_field", details.zone_field) whose every
// dictionary entry is "UTC" or an IANA Area/Location zone (else
// PULSE_TIMEZONE_UNKNOWN, details.dictionary_index). An empty field is
// SERVICE_VALIDATION — clear with ClearCohortZoneField.
//
// Sidecars: the SPSS metadata sidecar is CARRIED (re-fingerprinted over
// the new bytes — no variable changes); a point-lookup index is staled
// (its fingerprint covers the cohort's bytes) and listed in
// InvalidatedSidecars with its rebuild command, never rebuilt. A
// managed import's expiry slides.
func (p *Pulse) SetCohortZoneField(ctx context.Context, path, field string) (*CohortZoneResult, error) {
	return observed(p, ctx, opSpec{kind: observe.OpCohortZone, path: path}, func(ctx context.Context) (*CohortZoneResult, error) {
		if field == "" {
			return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
				"a default zone field needs a field name; to remove the default use ClearCohortZoneField (`pulse cohort zone clear`)",
				map[string]any{"cohort": path, "option": "zone_field", "reason": "empty_name"})
		}
		return p.setCohortZoneField(ctx, path, field)
	})
}

// ClearCohortZoneField removes the default zone field of the cohort at
// path: SetCohortZoneField's rewrite with no default, so a cohort set
// then cleared is byte-identical to the original. A cohort declaring no
// default is left untouched (Changed false).
func (p *Pulse) ClearCohortZoneField(ctx context.Context, path string) (*CohortZoneResult, error) {
	return observed(p, ctx, opSpec{kind: observe.OpCohortZone, path: path}, func(ctx context.Context) (*CohortZoneResult, error) {
		return p.setCohortZoneField(ctx, path, "")
	})
}

func (p *Pulse) setCohortZoneField(ctx context.Context, path, field string) (*CohortZoneResult, error) {
	// The SPSS sidecar is read BEFORE the rewrite stales it.
	carry, carryWarn := p.prepareSidecarCarry(path)
	res, err := p.svc.SetCohortZoneField(ctx, path, field)
	if err != nil || !res.Changed {
		return res, err
	}
	p.touchManaged(ctx, path)
	if res.Layout == service.CohortLayoutSingleFile {
		sidecars, scanErr := p.InvalidatedSidecars(ctx, path)
		res.InvalidatedSidecars = sidecars
		if scanErr != nil {
			res.SidecarWarnings = append(res.SidecarWarnings, sidecarScanWarning(path, scanErr))
		}
	}
	if carryWarn != nil {
		res.SidecarWarnings = append(res.SidecarWarnings, carryWarn)
		return res, nil
	}
	if carry == nil {
		return res, nil
	}
	schema, err := p.svc.ResolveCanonicalSchema(ctx, path)
	if err == nil {
		if w := carry.WriteFor(p.fsys, path, schema.Logical(), nil, nil); w != nil {
			res.SidecarWarnings = append(res.SidecarWarnings, w)
			return res, nil
		}
		kept := res.InvalidatedSidecars[:0]
		for _, s := range res.InvalidatedSidecars {
			if s.Kind != SidecarKindSPSSMetadata {
				kept = append(kept, s)
			}
		}
		res.InvalidatedSidecars = kept
		return res, nil
	}
	res.SidecarWarnings = append(res.SidecarWarnings, sidecarScanWarning(path, err))
	return res, nil
}
