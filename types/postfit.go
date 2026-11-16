package types

import "encoding/json"

// PostFitType names a post-fit operator: an embedder-registered
// POSTFIT_<NAMESPACE>_<NAME> (pulse.PostFitRegistration). The category
// is extension-only — Pulse ships no built-in post-fit operator — so the
// set of valid values is the instance's registrations.
type PostFitType string

// PostFitSpec is one entry of ComposedRequest.PostFit: a run of a
// registered post-fit operator over the batch's finished fits. It runs
// once, after the optimize blocks and before the nested-model
// comparisons, and reads no record: only the fits Fits names, the
// batch's slot results (the result() reader), every optimize block's
// solution and its own Params. Its result lands in
// ComposedResponse.PostFit at the same index.
type PostFitSpec struct {
	// Label names the entry. Required and unique among the batch's
	// post-fit entries; the result echoes it.
	Label string `json:"label"`

	// Type is the registered operator (POSTFIT_<NAMESPACE>_<NAME>).
	// Required; a name the instance does not register is
	// PULSE_POSTFIT_INVALID reason type_unknown.
	Type PostFitType `json:"type"`

	// Fits names the `fits` entries the operator reads, each a
	// regression fit, each at most once. The operator sees only these.
	Fits []string `json:"fits,omitempty"`

	// Params is the operator's params object, checked against the
	// registration's declared Params exactly as an extension operator's
	// slot params are (PULSE_REQUEST_UNKNOWN_FIELD /
	// PULSE_REQUEST_PARAM_INVALID at post_fit[i].params.<key>).
	Params json.RawMessage `json:"params,omitempty"`
}

// PostFitResult is one ComposedResponse.PostFit entry: the typed output
// of the ComposedRequest.PostFit entry at the same index, held to the
// registration's declared output schema. Undefined (NaN) figures are
// null on the wire.
//
// An entry that fails on its data — a fit it reads failed, or the
// operator returned an error — keeps its identity and Error (Scalars
// and Tables absent) and warns PULSE_SLOT_FAILED (slot_kind post_fit);
// under Strict the batch fails with the entry's own code.
type PostFitResult struct {
	// Label and Type echo the spec.
	Label string      `json:"label"`
	Type  PostFitType `json:"type"`

	// Scalars are the operator's named scalar outputs, every one the
	// registration declares (NaN → null).
	Scalars map[string]float64 `json:"scalars,omitempty"`

	// Tables are the operator's named tables, every one the registration
	// declares.
	Tables map[string]*PostFitTable `json:"tables,omitempty"`

	// Error is the entry's failure, nil on success.
	Error *SlotError `json:"error,omitempty"`
}

// PostFitTable is one named table a post-fit operator returns: its
// column names in the registration's declared order, then its rows,
// each holding one cell per column. A cell is a float64 (a float
// column; NaN → null on the wire), a string (a string column) or nil
// (null, on either kind).
type PostFitTable struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// MarshalJSON writes the post-fit result with every non-finite float as
// null; see MarshalFinite.
func (r PostFitResult) MarshalJSON() ([]byte, error) {
	type alias PostFitResult
	return MarshalFinite((*alias)(&r))
}
