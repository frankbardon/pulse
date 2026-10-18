package model

import (
	stderrors "errors"
	"fmt"
	"maps"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// A failed fit is a Compose `fits` entry whose fit failed on its data (a
// data-dependent refusal — separation, non-convergence, a singular
// battery matrix): there is no model, yet its name stays in the Set's
// one namespace under its static Signature — the one predict's
// FitCatalog judges — so every reference to it passes the same static
// checks at runtime as in predict, and reading it is told apart from
// naming an unbound fit.
//
// A reader asks Failed / FailedOfKind before it reads the model, and
// fails alone: an attribute (ATTR_REG_PREDICT, an ATTR_FORMULA naming
// the fit in a literal coef() call, a fitted vector attribute's
// params.fit, a {population} reference) scores an all-null column with
// one PULSE_FIT_UNAVAILABLE warning (FitUnavailable); a MAT_* slot's
// params.fit fails that slot alone with the fit's own code
// (FailedFitSlotError). A FILTER_EXPRESSION naming it — no filter can
// be null — and a coef() whose fit name is not a literal stay refused
// with the fit's OWN code (FailedFitRefusal, also what Lookup /
// LookupVector / Coef return).
type failedFit struct {
	sig Signature
	err *types.SlotError
}

// AddFailed binds name sig.Name as a failed fit carrying se; a name
// already bound (either kind, or failed) is PULSE_MODEL_NAME_COLLISION.
func (s *Set) AddFailed(sig Signature, se *types.SlotError) error {
	if s.bound(sig.Name) {
		return nameCollision(sig.Name)
	}
	if s.failed == nil {
		s.failed = map[string]failedFit{}
	}
	s.failed[sig.Name] = failedFit{sig: sig, err: se}
	return nil
}

// Failed returns the error of the failed fit bound under name, and
// whether name is one. Nil-receiver-safe.
func (s *Set) Failed(name string) (*types.SlotError, bool) {
	if s == nil {
		return nil, false
	}
	f, ok := s.failed[name]
	return f.err, ok
}

// FailedOfKind is Failed for a reader of kind: false for a failed fit
// of the other kind, whose reader keeps PULSE_MODEL_KIND_MISMATCH
// (Lookup / LookupVector). Nil-receiver-safe.
func (s *Set) FailedOfKind(name string, kind types.FitKind) (*types.SlotError, bool) {
	if s == nil {
		return nil, false
	}
	f, ok := s.failed[name]
	if !ok || f.sig.EffectiveKind() != kind {
		return nil, false
	}
	return f.err, true
}

// FailedFitSlotError is the per-slot error of a slot (a MAT_* slot's
// params.fit) whose fit name failed on its data: the fit's own code,
// message and details, plus details.fit = name.
func FailedFitSlotError(name string, se *types.SlotError) *types.SlotError {
	ce := FailedFitRefusal(name, se)
	return &types.SlotError{Code: string(ce.Code), Message: ce.Message, Details: ce.Details}
}

// ErrFitFailed is the Cause of every FailedFitRefusal: an expression
// evaluator tells the request-level refusal (stderrors.Is) apart from a
// row's evaluation error, whatever the fit's own code.
var ErrFitFailed = stderrors.New("the fit failed on its data")

// FailedFitRefusal is a reader's refusal of the failed fit name: the
// fit's own code and details, plus details.fit = name; its Cause is
// ErrFitFailed.
func FailedFitRefusal(name string, se *types.SlotError) *errors.CodedError {
	details := make(map[string]any, len(se.Details)+1)
	maps.Copy(details, se.Details)
	details["fit"] = name
	ce := errors.NewCodedErrorWithDetails(errors.Code(se.Code),
		fmt.Sprintf("fit %q failed on its data, so it has no model to read: %s", name, se.Message), details)
	ce.Cause = ErrFitFailed
	return ce
}

// FitUnavailable is the PULSE_FIT_UNAVAILABLE warning of a scoring
// slot (label) over the failed fit name.
func FitUnavailable(name, label string, se *types.SlotError) *types.ResponseWarning {
	return &types.ResponseWarning{
		Code: string(errors.PULSE_FIT_UNAVAILABLE),
		Message: fmt.Sprintf("%s scores from fit %q, which failed on its data (%s); its column is null on every row",
			label, name, se.Code),
		Details: map[string]any{"fit": name, "attribute": label, "code": se.Code},
	}
}

// failedOfKind returns the names of s's failed fits of kind.
func (s *Set) failedOfKind(kind types.FitKind) []string {
	if s == nil {
		return nil
	}
	var out []string
	for n, f := range s.failed {
		if f.sig.EffectiveKind() == kind {
			out = append(out, n)
		}
	}
	return out
}
