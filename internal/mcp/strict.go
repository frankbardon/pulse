package mcp

import (
	"encoding/json"
	stderrors "errors"
	"reflect"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/strictjson"
	"github.com/frankbardon/pulse/types"
)

// strictDecode is every tool's argument decode: the shared strict
// request decoder (internal/strictjson), so an unknown key ANYWHERE in
// the arguments — a request root, a nested sort entry, an aggregation,
// a Compose slot, a chain stage — is PULSE_REQUEST_UNKNOWN_FIELD
// located by details.path (and details.request / fit / stage inside a
// nested request), with a nearest-key suggestion. A slot the instance
// hides is refused exactly as an unknown key (descx.StrictOptions), and
// valid_keys / suggestions range over the visible keys only. A root
// `_meta` example block is tolerated. Empty arguments decode to the
// zero value. Every other fault (malformed JSON, a wrong-typed value)
// is SERVICE_VALIDATION (strictjson.DecodeRequestInto), never a raw
// encoding/json error.
func strictDecode[In any](raw json.RawMessage, inst *descx.InstanceSnapshot) (In, error) {
	var in In
	if len(raw) == 0 {
		return in, nil
	}
	err := strictjson.DecodeRequestInto(raw, &in, descx.StrictOptions(inst), "tool arguments")
	return in, err
}

// strictSampleDecode is strictDecode for pulse_sample, whose argument
// struct (SampleIn) spreads types.SampleRequest's union slots at its
// root: a union key the instance hides (capability:union) is refused as
// an unknown argument, valid_keys over the visible ones.
func strictSampleDecode(raw json.RawMessage, inst *descx.InstanceSnapshot) (SampleIn, error) {
	var in SampleIn
	if len(raw) == 0 {
		return in, nil
	}
	opts := descx.StrictOptions(inst)
	if hidden := descx.HiddenSlotKeys(&types.SampleRequest{}, inst); len(hidden) > 0 {
		base := opts.Hidden
		sampleIn := reflect.TypeFor[SampleIn]()
		opts.Hidden = func(t reflect.Type) []string {
			if t == sampleIn {
				return hidden
			}
			if base == nil {
				return nil
			}
			return base(t)
		}
	}
	err := strictjson.DecodeRequestInto(raw, &in, opts, "tool arguments")
	return in, err
}

// checkUnknownKeys returns the strict decoder's unknown-key refusal for
// body decoded as root (nil when every key is recognised, or the body
// is not a JSON object — the typed decode surfaces that fault).
func checkUnknownKeys[Root any](body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	if len(body) == 0 {
		return nil
	}
	var root Root
	err := strictjson.DecodeInto(body, &root, descx.StrictOptions(inst))
	var ce *perr.CodedError
	if stderrors.As(err, &ce) && ce.Code == perr.PULSE_REQUEST_UNKNOWN_FIELD {
		return ce
	}
	return nil
}

// checkUnknownRequestKeys is checkUnknownKeys over a types.Request.
func checkUnknownRequestKeys(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	return checkUnknownKeys[types.Request](body, inst)
}

// checkUnknownKeysComposed is checkUnknownKeys over a ComposedRequest:
// the root's own keys first (a hidden `overlays` before the nested
// requests), then each request located by details.request and each fit
// request by details.fit.
func checkUnknownKeysComposed(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	return checkUnknownKeys[types.ComposedRequest](body, inst)
}

// checkUnknownKeysChain is checkUnknownKeys over a ChainRequest, each
// stage's request located by details.stage.
func checkUnknownKeysChain(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	return checkUnknownKeys[types.ChainRequest](body, inst)
}
