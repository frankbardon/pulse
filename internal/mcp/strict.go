package mcp

import (
	"encoding/json"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// requestSlotKeys is the set of valid top-level JSON keys on a
// types.Request, derived once from the struct's json tags. Reflection
// keeps it in lockstep with the struct as slots are added.
var requestSlotKeys = jsonObjectKeys(&types.Request{})

// jsonObjectKeys returns the top-level JSON object keys declared by the
// struct pointed to by sample (descx.JSONObjectKeys — shared with the
// service-side request-slot gate).
func jsonObjectKeys(sample any) []string {
	return descx.JSONObjectKeys(sample)
}

// checkUnknownRequestKeys decodes body as a JSON object and verifies
// every top-level key is a recognised types.Request slot. On any
// unknown key it returns a PULSE_REQUEST_UNKNOWN_FIELD CodedError whose
// message and details name the offending key(s), the nearest valid slot
// (Levenshtein), and the full valid-key list — turning the silent
// "unknown keys are dropped" failure into an actionable, first-try
// fixable error.
//
// Returns nil when body is empty, is not a JSON object (the typed
// decoder downstream surfaces the real parse error), or all keys are
// recognised.
func checkUnknownRequestKeys(body []byte) *perr.CodedError {
	if len(body) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	valid := make(map[string]struct{}, len(requestSlotKeys))
	for _, k := range requestSlotKeys {
		valid[k] = struct{}{}
	}
	var unknown []string
	for k := range raw {
		if _, ok := valid[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	// The shape (message, unknown_keys, suggestions, valid_keys) is
	// built in one place, shared with the instance-scoped request-slot
	// gate (descx.SlotRefusal), so an unknown key and a hidden slot
	// cannot drift apart.
	return descx.UnknownFieldError(unknown, requestSlotKeys)
}

// Location detail keys for a nested unknown-key refusal. They are the
// service's located-refusal keys (descx.RefusalAt, as descx.SlotRefusal
// uses them), so an MCP client sees the same details a library caller
// does for the same request.
const (
	locationRequest = "request"
	locationStage   = "stage"
)

// checkUnknownKeysComposed validates each request inside a
// ComposedRequest body, tagging the offending request's index into the
// returned error's details as details.request.
func checkUnknownKeysComposed(body []byte) *perr.CodedError {
	var probe struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	for i, r := range probe.Requests {
		if ce := checkUnknownRequestKeys(r); ce != nil {
			ce.Details[locationRequest] = i
			return ce
		}
	}
	return nil
}

// checkUnknownKeysChain validates each stage's request inside a
// ChainRequest body, tagging the offending stage index into the
// returned error's details as details.stage (no stage name: the
// service's located refusal carries the index alone).
func checkUnknownKeysChain(body []byte) *perr.CodedError {
	var probe struct {
		Stages []struct {
			Request json.RawMessage `json:"request"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	for i, st := range probe.Stages {
		if len(st.Request) == 0 {
			continue
		}
		if ce := checkUnknownRequestKeys(st.Request); ce != nil {
			ce.Details[locationStage] = i
			return ce
		}
	}
	return nil
}
