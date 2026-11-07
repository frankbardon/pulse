package extend

// SpecValidator is the optional, spec-only refusal hook a registration
// may carry in its Validate field: it judges one request slot's spec
// (S is the category's spec type — types.RegressionSpec for a
// regression model) and returns nil to accept it or an error to refuse
// it. It sees the spec alone: no record, no cohort schema, no instance
// state — exactly what predict has — so predict calls it on the same
// spec the runtime does, before the factory, and both report the same
// refusal.
//
// Pulse calls it in three places, each under recover():
//
//   - predict, once per slot whose spec the engine's own rules accepted;
//   - the runtime, once per slot, after the engine's own spec rules and
//     before the factory (a refusal fails the run, like any other
//     configuration fault — it never fails the slot alone);
//   - pulse.New's probe, on the synthetic probe spec the factory is
//     probed with (a refusal or a panic there fails pulse.New with
//     PULSE_EXTENSION_FACTORY_PANIC, details.stage "validate").
//
// A returned *errors.CodedError (found with errors.As, so a wrapped one
// counts) keeps its code, message and details. Any other error is
// reported as PULSE_EXTENSION_SPEC_REFUSED with the error's text in the
// message and details {category, type, slot} (plus name when the slot
// is named); a panic is PULSE_EXTENSION_SPEC_REFUSED with details.panic.
//
// The hook receives a shallow copy of the slot's spec: it must not
// mutate anything reachable from it (slices, maps, the params object),
// and must not keep it. It must be deterministic and cheap — predict
// calls it on every slot of every request. A nil Validate accepts every
// spec, byte-identically to a registration without the field.
type SpecValidator[S any] func(spec *S) error
