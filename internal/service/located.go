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

// locate adds key=idx to the details of a located refusal in err's
// chain (descx.RefusalAt) and returns it; any other error is returned
// unchanged.
func locate(err error, key string, idx int) error {
	var l *locatedRefusal
	if !stderrors.As(err, &l) {
		return err
	}
	return descx.RefusalAt(l.ce, key, idx)
}
