package service

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep"
	"github.com/frankbardon/pulse/types"
)

// expandCompose returns the effective request Compose and
// ComposeParallel run: the ComposedRequest with its sweep expanded into
// slots appended after the explicit ones (sweep.Expand, the expansion
// predict shares). With EchoRequest on, it is what
// ComposedResponse.NormalizedRequest carries. A sweep-free request
// comes back as the same pointer, after the MaxComposeSlots preflight
// it always ran.
//
// A set sweep on an instance that hides it is refused as an unknown
// field BEFORE its contents are judged, exactly as predict refuses it;
// then the limit is checked on explicit + expanded slots before a single
// sweep slot is rendered, so no product of axes bypasses it.
func (s *Service) expandCompose(composed *types.ComposedRequest) (*types.ComposedRequest, error) {
	if composed == nil || (len(composed.Requests) == 0 && composed.Sweep == nil) {
		return nil, errors.NewCodedError(errors.SERVICE_VALIDATION, "composed request must contain at least one request")
	}
	if composed.Sweep != nil {
		if err := s.slotRefusal(composed); err != nil {
			return nil, err
		}
	}
	exp, err := sweep.Expand(composed, s.composeSlotsPreflight)
	if err != nil {
		return nil, err
	}
	return exp.Composed, nil
}
