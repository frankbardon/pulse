package pulse

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/service"
)

// prepareSidecarCarry reads the SPSS metadata sidecar beside a derived
// cohort's SOURCE (Materialize, Rake) before the new cohort is written —
// the target may be the source itself, whose sidecar the write stales.
// An anchored source has no sidecar of its own. A stale or unreadable
// sidecar comes back as a warning: the derived cohort is written without
// one, never refused for it.
func (p *Pulse) prepareSidecarCarry(source string) (*spss.SidecarCarry, *errors.CodedError) {
	if source == "" {
		return nil, nil
	}
	if _, _, anchored := service.SplitAnchorPath(source); anchored {
		return nil, nil
	}
	return spss.PrepareSidecarCarry(p.fsys, source)
}

// carrySidecar writes the carried sidecar beside the cohort just built,
// rewritten against its schema (replaced names the fields whose values
// the write replaced; mirrors the columns holding a recorded field's
// codes, spss.Mirror), and returns the warnings to report: the prepare
// warning, or a failure to carry. A carried sidecar is fresh, so it is
// dropped from built.InvalidatedSidecars — an overwrite no longer leaves
// it stale.
func (p *Pulse) carrySidecar(c *spss.SidecarCarry, prepWarn *errors.CodedError, built *CohortBuildResult, replaced []string, mirrors map[string]spss.Mirror) []*errors.CodedError {
	if prepWarn != nil {
		return []*errors.CodedError{prepWarn}
	}
	if c == nil {
		return nil
	}
	if w := c.WriteFor(p.fsys, built.Target, built.Schema, replaced, mirrors); w != nil {
		return []*errors.CodedError{w}
	}
	dropFreshSPSS(built)
	return nil
}

// carryProjectedSidecar is carrySidecar for a cohort an output block laid
// out: the carried sidecar is rewritten column for column through cols
// (spss.SidecarCarry.WriteProjected — dropped columns lose their
// variables, renamed ones keep theirs under the new name, duplicates get
// copies, rebuilt ones a regenerated variable).
func (p *Pulse) carryProjectedSidecar(c *spss.SidecarCarry, prepWarn *errors.CodedError, built *CohortBuildResult, cols []spss.ProjectedColumn) []*errors.CodedError {
	if prepWarn != nil {
		return []*errors.CodedError{prepWarn}
	}
	if c == nil {
		return nil
	}
	if w := c.WriteProjected(p.fsys, built.Target, built.Schema, cols); w != nil {
		return []*errors.CodedError{w}
	}
	dropFreshSPSS(built)
	return nil
}

// dropFreshSPSS removes the SPSS sidecar from built.InvalidatedSidecars:
// a carried sidecar is fresh, so an overwrite no longer leaves it stale.
func dropFreshSPSS(built *CohortBuildResult) {
	kept := built.InvalidatedSidecars[:0]
	for _, s := range built.InvalidatedSidecars {
		if s.Kind != SidecarKindSPSSMetadata {
			kept = append(kept, s)
		}
	}
	built.InvalidatedSidecars = kept
}
