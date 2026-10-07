package service

import (
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// limitsPreflight is the process pre-flight for the instance resource
// limits: it refuses the first certain breach predict reports for req
// over schema (descx.LimitRefusal — the shared rule) with
// PULSE_LIMIT_EXCEEDED. Every Request arm calls it right after
// checkFieldRefs, so a certain breach is refused before any record is
// decoded. It reads only the schema.
func (s *Service) limitsPreflight(req *types.Request, schema *encoding.Schema) error {
	if err := descx.LimitRefusal(req, schema, s.instance, s.Limits()); err != nil {
		return markLocated(err)
	}
	return nil
}

// composeSlotsPreflight refuses a Compose call carrying more requests
// than MaxComposeSlots — the whole call, before any slot runs (FailFast
// does not apply).
func (s *Service) composeSlotsPreflight(composed *types.ComposedRequest) error {
	if err := limits.CheckComposeSlots(s.Limits(), len(composed.Requests)); err != nil {
		return err
	}
	return nil
}
