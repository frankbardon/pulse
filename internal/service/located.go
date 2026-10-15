package service

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// locatedRefusal marks a coded error as a located request-level
// refusal — one a multi-request root (Compose, ProcessChain) tags with
// its position, details.request / details.stage, when it surfaces
// through that root's Process call. The marked refusals are the zone
// refusals (resolveZones / resolveFacetZones) and the v1 join-count
// rule (descx.JoinCountRefusal, in Process); the validators locate the
// same refusals at the same points with descx.RefusalAt. It unwraps to
// the coded error itself, so errors.As still finds the code and
// details, and an unlocated caller (a single Process) sees the
// refusal unchanged.
type locatedRefusal struct{ ce *errors.CodedError }

func (l *locatedRefusal) Error() string { return l.ce.Error() }
func (l *locatedRefusal) Unwrap() error { return l.ce }

// markLocated wraps a coded err as a located refusal; nil and uncoded
// errors pass through.
func markLocated(err error) error {
	var ce *errors.CodedError
	if err == nil || !stderrors.As(err, &ce) {
		return err
	}
	return &locatedRefusal{ce: ce}
}

// locate adds key=idx to the details of the coded error in err's chain
// and returns it: a marked located refusal is rebuilt in place
// (descx.RefusalAt); any other coded error — a slot's or stage's
// operator-construction, label, overlay or data refusal raised deeper
// in Process or the chain stage loop — is located through
// locatedChain, which keeps err's message and hands errors.As the
// located copy first. So every coded refusal a Compose slot or chain
// stage raises carries details.request / details.stage, as predict
// locates it. A coded error already carrying key, and any uncoded error
// (a ctx error), are returned unchanged.
func locate(err error, key string, idx int) error {
	var l *locatedRefusal
	if stderrors.As(err, &l) {
		return descx.RefusalAt(l.ce, key, idx)
	}
	var ce *errors.CodedError
	if err == nil || !stderrors.As(err, &ce) {
		return err
	}
	if _, ok := ce.Details[key]; ok {
		return err
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details[key] = idx
	return &locatedChain{ce: &errors.CodedError{Code: ce.Code, Message: ce.Message, Details: details, Cause: err}}
}

// locatedChain is a coded error located by locate. Error is the
// original error's text; Unwrap is the located copy, whose Cause is the
// original error — so errors.As finds the located copy first, while
// errors.Is and a chain walk (ScopeRefusal) still reach every link.
type locatedChain struct{ ce *errors.CodedError }

func (l *locatedChain) Error() string { return l.ce.Cause.Error() }
func (l *locatedChain) Unwrap() error { return l.ce }
