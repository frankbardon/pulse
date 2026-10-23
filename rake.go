package pulse

import (
	"context"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/rake"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// RakeRequest is a Pulse.Rake run: the source cohort, the new cohort to
// write, the margins the weights must reproduce, and the solver knobs.
// See internal/rake.Request for every field; JSON keys are snake_case
// (`cohort`, `out`, `weight_field`, `margins`, `by`, `base_weight`,
// `missing`, `min_weight`, `max_weight`, `max_iter`, `tol`, `total`,
// `overwrite`).
type RakeRequest = rake.Request

// RakeMargin is one target distribution of a RakeRequest: `field` +
// `targets` (a rim margin) or two or more `fields` + `cells` (a joint
// margin — cell weighting), with `targets_as` "shares" (default) or
// "counts".
type RakeMargin = rake.Margin

// RakeCell is one joint-margin target: one category per margin field
// and its share or count.
type RakeCell = rake.Cell

// RakeMarginReport is one margin's audit: per category the observed
// share before raking, the target share and the achieved share.
type RakeMarginReport = rake.MarginReport

// RakeCategoryReport is one margin category of a RakeMarginReport.
type RakeCategoryReport = rake.CategoryReport

// RakeGroupReport is one `by` group's fit: its convergence, margins and
// efficiency.
type RakeGroupReport = rake.GroupReport

// RakeEfficiency is the Kish weighting-efficiency report of the output
// weights; deff_kish is the unequal-weighting design effect only.
// Undefined figures are NaN in Go and null on the JSON wire.
type RakeEfficiency = rake.Efficiency

// Rake solver defaults, applied when a RakeRequest leaves the knob at
// zero.
const (
	RakeDefaultMaxIter     = rake.DefaultMaxIter
	RakeDefaultTol         = rake.DefaultTol
	RakeDefaultWeightField = rake.DefaultWeightField
)

// Rake null-margin-value policies (RakeRequest.Missing).
const (
	// RakeMissingRefuse (the default; also the empty string) refuses a
	// record whose margin value is null with PULSE_RAKE_MISSING_VALUE.
	RakeMissingRefuse = rake.MissingRefuse
	// RakeMissingBase keeps the record's current factor for a margin
	// whose value it lacks.
	RakeMissingBase = rake.MissingBase
)

// RakeMissingPolicies returns every RakeRequest.Missing spelling, the
// default first (refuse, base); any other value is PULSE_RAKE_INVALID.
// The slice is a fresh copy.
func RakeMissingPolicies() []string { return rake.MissingPolicies() }

// RakeResult is what a successful Pulse.Rake wrote and how well the
// weights fit.
type RakeResult struct {
	// Target is the cohort path written.
	Target string `json:"target"`
	// Records is the number of rows written: every source record.
	Records int64 `json:"records"`
	// WeightField names the output weight column (nullable f64).
	WeightField string `json:"weight_field"`
	// Iterations is the number of sweeps run (under `by`, the most any
	// group ran).
	Iterations int `json:"iterations"`
	// Converged reports that every margin category reached its target
	// share within tol (under `by`, in every group).
	Converged bool `json:"converged"`
	// Residual is the largest |achieved − target| share gap left (under
	// `by`, the worst group's).
	Residual float64 `json:"residual"`
	// Margins audits each margin over every record: observed, target
	// and achieved share per category.
	Margins []RakeMarginReport `json:"margins"`
	// Groups holds each `by` group's own fit, in the by field's order;
	// absent without `by`.
	Groups []RakeGroupReport `json:"groups,omitempty"`
	// Efficiency is the Kish efficiency of the output weights over every
	// record.
	Efficiency RakeEfficiency `json:"efficiency"`
	// Warnings are the non-fatal coded findings: PULSE_RAKE_NOT_CONVERGED
	// per group that stopped at max_iter, the cohort write's
	// PULSE_FIELD_DESCRIPTION_LOW_QUALITY per weak source description,
	// then a PULSE_SPSS_SIDECAR_STALE / _INVALID when the source's SPSS
	// sidecar could not be carried, then PULSE_RAKE_SIDECARS_INVALIDATED
	// when an overwrite left stale sidecars beside the target.
	Warnings []*errors.CodedError `json:"warnings,omitempty"`
	// InvalidatedSidecars names each sidecar beside the target that an
	// Overwrite invalidated, with its rebuild command.
	InvalidatedSidecars []StaleSidecar `json:"invalidated_sidecars,omitempty"`
}

// MarshalJSON writes every undefined figure as null
// (types.MarshalFinite).
func (r RakeResult) MarshalJSON() ([]byte, error) {
	type plain RakeResult
	return types.MarshalFinite(plain(r))
}

// Rake computes survey weights for a cohort and writes them, beside
// every source column, into a NEW single-file cohort: rim weighting
// (raking) to one or more margins, cell weighting (post-stratification)
// when a margin joins several fields, optionally from a base design
// weight, within each group of a `by` field, under min / max caps.
//
// The solver (internal/rake) is iterative proportional fitting over the
// RECORDS: each sweep rescales, margin by margin in request order, every
// category's records so its weighted share meets its target; caps
// (relative to the group's mean weight) apply after each sweep; sweeps
// stop when the largest achieved-vs-target share gap is within tol
// (default 1e-6) or after max_iter (default 100) sweeps — then the run
// still writes, with a PULSE_RAKE_NOT_CONVERGED warning. Each group is
// finally normalised to sum to its record count (mean weight 1) or to
// `total`. A single-margin request (one rim or one joint margin)
// converges in one sweep and is exact. The output weights are
// PROBABILITY weights: name the column as a request's `weight` (kind
// probability, the default). RakeResult.Efficiency reports Kish n_eff
// and deff_kish — the unequal-weighting design effect only, never a
// complex-sample (strata / cluster) design effect.
//
// Refusals, all raised before anything is created at out:
// PULSE_RAKE_INVALID (a malformed request; details.reason),
// PULSE_RAKE_UNKNOWN_CATEGORY (a target the field cannot hold) — both
// judged on the schema alone — then, after reading every record,
// PULSE_RAKE_MISSING_VALUE (a null margin value under missing "refuse",
// or any null `by` value), PULSE_RAKE_UNTARGETED_CATEGORY (a record's
// category has no target) and PULSE_RAKE_EMPTY_CELL (a positive target
// no record of its group can carry). An anchored target
// (`archive.pulse#name.pulse`) is SERVICE_VALIDATION.
//
// The source may be a single file, a shard archive (its records read in
// shard order against the canonical schema) or an anchored shard; the
// output is always an ungrouped (0x01) single file written atomically
// through NewCohortBuilder — any failure leaves nothing at out. Its
// schema is the source's logical fields verbatim (a replaced
// weight_field keeps its position) plus the weight, a nullable f64 with
// a generated description. The source's SPSS metadata sidecar is
// carried — rewritten for the output schema and re-fingerprinted, so value
// labels survive to a .sav export; a stale or unreadable one (or a
// replaced variable its response sets or derived registry name) is not,
// with one warning carrying the cause's code. No other sidecar is carried;
// a managed-import source slides its expiry, as a Process read does.
func (p *Pulse) Rake(ctx context.Context, req RakeRequest) (*RakeResult, error) {
	return observed(p, ctx, opSpec{kind: observe.OpRake, path: req.Cohort}, func(ctx context.Context) (*RakeResult, error) {
		return p.rake(ctx, &req)
	})
}

func (p *Pulse) rake(ctx context.Context, req *RakeRequest) (*RakeResult, error) {
	if strings.Contains(req.Out, "#") {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"rake writes a single-file cohort; an anchored archive#shard target is not accepted",
			map[string]any{"target": req.Out, "reason": "anchored_target"})
	}
	if strings.TrimSpace(req.Cohort) == "" {
		// Compile raises the coded refusal before any open is attempted.
		_, err := rake.Compile(req, &encoding.Schema{})
		return nil, err
	}
	cohort, err := p.svc.Open(ctx, req.Cohort)
	if err != nil {
		return nil, err
	}
	reader, err := cohort.OpenRecordReader()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	carry, carryWarn := p.prepareSidecarCarry(req.Cohort)
	src := reader.Schema()
	plan, err := rake.Compile(req, src)
	if err != nil {
		return nil, err
	}
	weightField := encoding.Field{
		Name:        plan.WeightField(),
		Type:        encoding.FieldTypeF64,
		Nullable:    true,
		Description: plan.Description(),
	}
	replace := plan.ReplaceIndex()
	// Schema.Fields is the LOGICAL list: a grouped source's parent
	// groups and an archive's layout never reach the output.
	fields := append([]encoding.Field(nil), src.Fields...)
	if replace >= 0 {
		fields[replace] = weightField
	} else {
		fields = append(fields, weightField)
	}
	// The builder opens before any record is read, so an existing target
	// without Overwrite (SERVICE_VALIDATION) costs no scan.
	b, err := p.NewCohortBuilder(ctx, req.Out, encoding.Schema{Fields: fields},
		CohortBuilderOptions{Overwrite: req.Overwrite})
	if err != nil {
		return nil, err
	}
	data, err := plan.Load(ctx, reader.Len(), reader.RecordAt)
	if err != nil {
		_ = b.Abort()
		return nil, err
	}
	sol, err := plan.Solve(ctx, data)
	if err != nil {
		_ = b.Abort()
		return nil, err
	}

	for i := int64(0); i < reader.Len(); i++ {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				_ = b.Abort()
				return nil, err
			}
		}
		row, err := reader.RecordAt(i)
		if err != nil {
			_ = b.Abort()
			return nil, err
		}
		if replace >= 0 {
			row[replace] = sol.Weights[i]
		} else {
			row = append(row, sol.Weights[i])
		}
		if err := b.Append(CohortRow(row)); err != nil {
			_ = b.Abort()
			return nil, err
		}
	}
	built, err := b.Close()
	if err != nil {
		return nil, err
	}
	var replaced []string
	if replace >= 0 {
		replaced = []string{plan.WeightField()}
	}
	carried := p.carrySidecar(carry, carryWarn, built, replaced, nil)
	p.touchManaged(ctx, req.Cohort)
	rep := sol.Report
	res := &RakeResult{
		Target:              built.Target,
		Records:             built.Records,
		WeightField:         plan.WeightField(),
		Iterations:          rep.Iterations,
		Converged:           rep.Converged,
		Residual:            rep.Residual,
		Margins:             rep.Margins,
		Groups:              rep.Groups,
		Efficiency:          rep.Efficiency,
		Warnings:            append(append(append([]*errors.CodedError(nil), sol.Warnings...), built.Warnings...), carried...),
		InvalidatedSidecars: built.InvalidatedSidecars,
	}
	if len(built.InvalidatedSidecars) > 0 {
		paths := make([]string, len(built.InvalidatedSidecars))
		for i, s := range built.InvalidatedSidecars {
			paths[i] = s.Path
		}
		res.Warnings = append(res.Warnings, errors.NewCodedErrorWithDetails(errors.PULSE_RAKE_SIDECARS_INVALIDATED,
			"the overwritten cohort's sidecars are now stale; rebuild them (invalidated_sidecars names each with its rebuild command)",
			map[string]any{"target": built.Target, "sidecars": paths}))
	}
	return res, nil
}
