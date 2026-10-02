package mcp

import (
	"encoding/json"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// requestSlotKeys returns the valid top-level JSON keys on a
// types.Request for inst: every key the struct's json tags declare
// (reflection keeps it in lockstep as slots are added) minus the
// capability-gated slots inst hides (descx.VisibleSlotKeys — the same
// candidate list the library's request-slot gate reports). A nil or
// unscoped inst offers every slot.
func requestSlotKeys(inst *descx.InstanceSnapshot) []string {
	return descx.VisibleSlotKeys(&types.Request{}, inst)
}

// checkUnknownRequestKeys decodes body as a JSON object and verifies
// every top-level key is a recognised types.Request slot. On any
// unknown key it returns a PULSE_REQUEST_UNKNOWN_FIELD CodedError whose
// message and details name the offending key(s), the nearest valid slot
// (Levenshtein), and the full valid-key list — turning the silent
// "unknown keys are dropped" failure into an actionable, first-try
// fixable error.
//
// A slot the instance hides is not a recognised key: it is refused in
// the same shape as a misspelling, and valid_keys / suggestions range
// over the visible slots only.
//
// Returns nil when body is empty, is not a JSON object (the typed
// decoder downstream surfaces the real parse error), or all keys are
// recognised.
func checkUnknownRequestKeys(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	if len(body) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	keys := requestSlotKeys(inst)
	valid := make(map[string]struct{}, len(keys))
	for _, k := range keys {
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
	return descx.UnknownFieldError(unknown, keys)
}

// Location detail keys for a nested unknown-key refusal. They are the
// service's located-refusal keys (descx.RefusalAt, as descx.SlotRefusal
// uses them), so an MCP client sees the same details a library caller
// does for the same request.
const (
	locationRequest = "request"
	locationStage   = "stage"
)

// checkHiddenRootKeys refuses a ComposedRequest / ChainRequest root
// (sample) whose body sets a top-level slot inst hides. The roots are
// otherwise decoded leniently, so only HIDDEN keys are refused here —
// as descx.SlotRefusal refuses them, root before nested, with
// valid_keys / suggestions over the root's visible keys.
func checkHiddenRootKeys(body []byte, sample any, inst *descx.InstanceSnapshot) *perr.CodedError {
	hidden := descx.HiddenSlotKeys(sample, inst)
	if len(hidden) == 0 {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}
	var unknown []string
	for _, k := range hidden {
		if _, set := raw[k]; set {
			unknown = append(unknown, k)
		}
	}
	return descx.UnknownFieldError(unknown, descx.VisibleSlotKeys(sample, inst))
}

// checkUnknownKeysComposed validates each request inside a
// ComposedRequest body, tagging the offending request's index into the
// returned error's details as details.request. A hidden root slot is
// refused first.
func checkUnknownKeysComposed(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	if ce := checkHiddenRootKeys(body, &types.ComposedRequest{}, inst); ce != nil {
		return ce
	}
	var probe struct {
		Requests []json.RawMessage `json:"requests"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	for i, r := range probe.Requests {
		if ce := checkUnknownRequestKeys(r, inst); ce != nil {
			ce.Details[locationRequest] = i
			return ce
		}
	}
	return nil
}

// checkUnknownKeysChain validates each stage's request inside a
// ChainRequest body, tagging the offending stage index into the
// returned error's details as details.stage (no stage name: the
// service's located refusal carries the index alone). A hidden root
// slot is refused first.
func checkUnknownKeysChain(body []byte, inst *descx.InstanceSnapshot) *perr.CodedError {
	if ce := checkHiddenRootKeys(body, &types.ChainRequest{}, inst); ce != nil {
		return ce
	}
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
		if ce := checkUnknownRequestKeys(st.Request, inst); ce != nil {
			ce.Details[locationStage] = i
			return ce
		}
	}
	return nil
}
