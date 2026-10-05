package service

import (
	"context"

	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// SetDefaultMultiplicity installs pulse.Options.DefaultMultiplicity
// (nil = none), the multiple-comparison block every test, post-test and
// overlay inherits field by field after its own block and its
// request's. pulse.New validates it first. The block is copied.
func (s *Service) SetDefaultMultiplicity(m *types.Multiplicity) {
	if m == nil {
		s.defaultMultiplicity = nil
		return
	}
	c := *m
	s.defaultMultiplicity = &c
}

// DefaultMultiplicity returns a copy of the installed default
// multiplicity block, or nil.
func (s *Service) DefaultMultiplicity() *types.Multiplicity {
	if s.defaultMultiplicity == nil {
		return nil
	}
	c := *s.defaultMultiplicity
	return &c
}

// composeSlotKey marks a context as running one slot of a Compose
// batch: the batch resolved every slot's multiplicity with the whole
// ComposedRequest (where the `compose` family is legal), so Process
// does not re-resolve the slot standalone.
type composeSlotKey struct{}

// withinCompose returns ctx marked as a Compose slot context.
func withinCompose(ctx context.Context) context.Context {
	return context.WithValue(ctx, composeSlotKey{}, true)
}

// isComposeSlot reports whether ctx runs a Compose slot.
func isComposeSlot(ctx context.Context) bool {
	v, _ := ctx.Value(composeSlotKey{}).(bool)
	return v
}

// resolveMultiplicity runs the single multiplicity-resolution pass
// (internal/descriptor.ResolveMultiplicity — the function predict
// calls) for one standalone Request: Process (every arm), ProcessStream
// and every ProcessChain stage. A Compose slot is skipped: its batch
// resolved it (resolveComposeMultiplicity). A located refusal: a chain
// adds details.stage.
func (s *Service) resolveMultiplicity(ctx context.Context, req *types.Request) error {
	if isComposeSlot(ctx) {
		return nil
	}
	_, err := descx.ResolveMultiplicity(req, s.defaultMultiplicity, s.instance)
	return markLocated(err)
}

// resolveComposeMultiplicity is resolveMultiplicity for a whole
// ComposedRequest (descx.ResolveComposeMultiplicity), run by Compose
// and ComposeParallel before any slot starts. A slot's refusal already
// carries details.request.
func (s *Service) resolveComposeMultiplicity(req *types.ComposedRequest) error {
	_, err := descx.ResolveComposeMultiplicity(req, s.defaultMultiplicity, s.instance)
	return err
}
