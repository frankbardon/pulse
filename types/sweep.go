package types

import (
	"bytes"
	"encoding/json"
)

// SweepSpec is a Compose parameter sweep: ONE request body with axis
// placeholders (`{{axis}}` inside a string, `{"$var": "axis"}` as a
// whole value), a list of values per axis, a label pattern, optional
// per-slot overlays and an optional rank. Before any slot runs, Pulse
// expands it into ordinary compose slots — the Cartesian product of the
// axes (Mode grid, the default; first axis slowest) or the axes walked
// in lockstep (Mode zip) — appended after the ComposedRequest's explicit
// Requests. Each expanded slot then runs exactly like a hand-written
// one.
//
// It rides as the optional `sweep` slot on ComposedRequest, gated by
// capability:compose_sweep. A nil spec is absent: the slot is
// `omitempty`, so a sweep-free request is byte-identical on the wire and
// under CanonicalHash.
type SweepSpec struct {
	// Axes are the swept parameters, at least one. Names are unique
	// identifiers; each axis carries a non-empty list of scalar values.
	Axes []SweepAxis `json:"axes"`

	// Mode is how the axes combine (SweepMode*). Empty is grid.
	Mode SweepMode `json:"mode,omitempty"`

	// Label is the per-slot label pattern, `{{axis}}` placeholders
	// substituted per slot. Empty takes the default `<axis>=<value>`
	// pairs joined by `_`.
	Label string `json:"label,omitempty"`

	// Request is the slot request body as raw JSON — the Request shape
	// with axis placeholders, which survive until substitution. Each
	// expanded body is decoded strictly into a Request.
	Request json.RawMessage `json:"request"`

	// Overlays is an optional raw JSON array of ComposeOverlaySpec
	// bodies, substituted per expanded slot and appended to the
	// ComposedRequest's own Overlays.
	Overlays json.RawMessage `json:"overlays,omitempty"`

	// Rank, when set, orders the finished slots by one number and
	// reports them in ComposedResponse.Ranking.
	Rank *SweepRank `json:"rank,omitempty"`
}

// SweepAxis is one swept parameter: a placeholder Name and the Values it
// takes. Values are JSON scalars — a number, string or boolean. Decoded
// numbers are kept as json.Number so a `{{axis}}` substitution renders
// them exactly as written; a Go caller may supply any integer or finite
// float kind, a json.Number, a string or a bool.
type SweepAxis struct {
	// Name is the placeholder name: an identifier
	// (`[A-Za-z_][A-Za-z0-9_]*`), unique within the sweep.
	Name string `json:"name"`

	// Values is the non-empty list of scalar values the axis takes, in
	// expansion order.
	Values []any `json:"values"`
}

// UnmarshalJSON decodes the axis keeping every numeric value as a
// json.Number, so its text survives to substitution (`0.10` renders
// as `0.10`, an integer past 2^53 keeps every digit). Unknown keys are
// ignored, as the enclosing request's lenient decode does; a misspelled
// `values` leaves the list empty, which validation refuses.
func (a *SweepAxis) UnmarshalJSON(data []byte) error {
	type alias SweepAxis
	var out alias
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return err
	}
	*a = SweepAxis(out)
	return nil
}

// SweepMode names how a sweep's axes combine into slots.
type SweepMode string

const (
	// SweepModeGrid expands the Cartesian product of the axes,
	// row-major with the first axis slowest. The default.
	SweepModeGrid SweepMode = "grid"
	// SweepModeZip walks the axes in lockstep; every axis must carry the
	// same number of values.
	SweepModeZip SweepMode = "zip"
)

// AllSweepModes returns every SweepMode value.
func AllSweepModes() []SweepMode {
	return []SweepMode{SweepModeGrid, SweepModeZip}
}

// SweepRank orders a sweep's finished slots by one number read off each
// slot's unshaped Response and reports them in ComposedResponse.Ranking.
type SweepRank struct {
	// By is the path to the ranked scalar inside each slot's Response.
	By string `json:"by"`

	// Order is the sort direction (SweepRankOrder*). Empty is asc.
	Order SweepRankOrder `json:"order,omitempty"`

	// Top, when set, keeps only the first Top ranked entries; at least
	// 1. Nil keeps every slot.
	Top *int `json:"top,omitempty"`
}

// SweepRankOrder is the sort direction of a sweep rank.
type SweepRankOrder string

const (
	// SweepRankAsc ranks the smallest value first. The default.
	SweepRankAsc SweepRankOrder = "asc"
	// SweepRankDesc ranks the largest value first.
	SweepRankDesc SweepRankOrder = "desc"
)

// AllSweepRankOrders returns every SweepRankOrder value.
func AllSweepRankOrders() []SweepRankOrder {
	return []SweepRankOrder{SweepRankAsc, SweepRankDesc}
}

// RankEntry is one ranked sweep slot in ComposedResponse.Ranking: the
// slot's Label, the ranked Value and its 1-based Rank. A slot whose
// value at the rank path is null (undefined) has no entry — it is left
// out with a PULSE_SWEEP_RANK_PATH warning on its own response — so
// Value is always finite.
type RankEntry struct {
	// Label is the ranked slot's final label.
	Label string `json:"label"`

	// Value is the number read at SweepRank.By.
	Value float64 `json:"value"`

	// Rank is the 1-based position in rank order.
	Rank int `json:"rank"`
}
