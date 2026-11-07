// Package extspec is the one rule behind an extension registration's
// opt-in, spec-only Validate hook (extend.SpecValidator): how a hook's
// verdict on one request slot's spec becomes a refusal. Predict
// (internal/descriptor) and the runtime adapters both call Refusal, so
// the code, message and details they report are byte-identical. It is
// a neutral leaf — it imports only the public errors package — and it
// never executes a request: the hook sees the spec alone.
package extspec

import (
	stderrors "errors"
	"fmt"

	"github.com/frankbardon/pulse/errors"
)

// Site names the request slot a Validate hook judges, as the refusal's
// details spell it.
type Site struct {
	// Category is the extension category ("regression", "aggregator",
	// …), as the PULSE_EXTENSION_* codes' details name it.
	Category string
	// Type is the operator's registered name (REG_<NS>_<NAME>, …).
	Type string
	// Slot is the request slot key the spec rides ("regressions", …).
	Slot string
	// Name is the slot's own name or label, when it has one ("" omits
	// details.name).
	Name string
}

// details is the refusal's details map: category, type, slot, then
// name when the slot is named.
func (s Site) details() map[string]any {
	d := map[string]any{"category": s.Category, "type": s.Type, "slot": s.Slot}
	if s.Name != "" {
		d["name"] = s.Name
	}
	return d
}

// Refusal runs validate over a shallow copy of spec under recover() and
// returns its verdict as a refusal, nil when validate is nil, spec is
// nil or the hook accepts the spec:
//
//   - a returned *errors.CodedError (errors.As, so a wrapped one
//     counts) is returned as is — its code, message and details;
//   - any other error is PULSE_EXTENSION_SPEC_REFUSED, message
//     "<category> <type> refused its spec: <error>", details site;
//   - a panic is PULSE_EXTENSION_SPEC_REFUSED, message
//     "<category> <type> Validate panicked: <value>", details site plus
//     panic.
func Refusal[S any](validate func(*S) error, spec *S, site Site) (cerr *errors.CodedError) {
	if validate == nil || spec == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			d := site.details()
			d["panic"] = fmt.Sprintf("%v", r)
			cerr = errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_SPEC_REFUSED,
				fmt.Sprintf("%s %s Validate panicked: %v", site.Category, site.Type, r), d)
		}
	}()
	c := *spec
	err := validate(&c)
	if err == nil {
		return nil
	}
	var coded *errors.CodedError
	if stderrors.As(err, &coded) && coded != nil {
		return coded
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_SPEC_REFUSED,
		fmt.Sprintf("%s %s refused its spec: %v", site.Category, site.Type, err), site.details())
}
