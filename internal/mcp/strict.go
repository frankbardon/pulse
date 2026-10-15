package mcp

import (
	"encoding/json"
	stderrors "errors"

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
// zero value.
func strictDecode[In any](raw json.RawMessage, inst *descx.InstanceSnapshot) (In, error) {
	var in In
	if len(raw) == 0 {
		return in, nil
	}
	err := strictjson.DecodeInto(raw, &in, descx.StrictOptions(inst))
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
