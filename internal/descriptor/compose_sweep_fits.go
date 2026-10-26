package descriptor

import (
	"bytes"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// SweepFitScope is the model.SweepScope a Compose batch's from_sweep
// fits are judged against (model.CheckFitsIn) and resolved over
// (model.ResolveSweepFits): written is the request as written (its
// sweep and rank), effective the expanded request whose sweep slots are
// effective.Requests[explicit : explicit+count] (banner.Expansion's
// Explicit and len(SweepLabels)). The runtime (Service.expandCompose)
// and PredictCompose build it from the same expansion, so both refuse
// and resolve a batch alike.
func SweepFitScope(written, effective *types.ComposedRequest, explicit, count int) model.SweepScope {
	if written == nil || written.Sweep == nil {
		return model.SweepScope{}
	}
	scope := model.SweepScope{Present: true, Ranked: written.Sweep.Rank != nil, Reads: SweepSlotReadsFit}
	if effective != nil && explicit >= 0 && count > 0 && explicit+count <= len(effective.Requests) {
		scope.Slots = effective.Requests[explicit : explicit+count]
	}
	return scope
}

// SweepSlotReadsFit reports whether slot reads the bound-model name fit
// — the from_sweep cycle: a literal coef() / sweep_axis() call naming
// it in any slot expression (vectors.ExprSlots, plus every regression's
// holdout predicate), or a params.fit naming it on an attribute or a
// matrix.
func SweepSlotReadsFit(slot *types.Request, fit string) bool {
	if slot == nil || fit == "" {
		return false
	}
	for _, e := range vectors.ExprSlots(slot) {
		if coefcall.NamesFit(e.Src, fit) {
			return true
		}
	}
	for _, r := range slot.Regressions {
		if r != nil && r.Holdout != "" && coefcall.NamesFit(r.Holdout, fit) {
			return true
		}
	}
	for _, a := range slot.Attributes {
		if a != nil && paramsFitIs(a.Params, fit) {
			return true
		}
	}
	for _, m := range slot.Matrices {
		if paramsFitIs(m.Params, fit) {
			return true
		}
	}
	return false
}

// paramsFitIs reports whether raw is a params object whose `fit` is
// fit. Params are read loosely: a malformed object is the slot's own
// decode to refuse.
func paramsFitIs(raw []byte, fit string) bool {
	if len(bytes.TrimSpace(raw)) == 0 || !bytes.Contains(raw, []byte(fit)) {
		return false
	}
	var p struct {
		Fit *string `json:"fit"`
	}
	return paramread.Probe(raw, &p) == nil && p.Fit != nil && *p.Fit == fit
}
