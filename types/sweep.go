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
// The sampling modes (Mode random / lhs) draw Samples slots instead:
// every axis carries a continuous Range rather than Values, and each
// slot takes one seeded draw per axis — uniform (log-uniform under
// scale log) for random, one draw per equal-probability stratum per
// axis for lhs. The draws are a pure function of the spec and the
// effective seed (RNGSeed, or one derived from the spec when zero), so
// predict lists the same slots the runtime runs.
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

	// Samples is the number of slots a sampling mode (random / lhs)
	// draws, at least 1; required there and refused under grid / zip.
	// Zero is absent.
	Samples int `json:"samples,omitempty"`

	// RNGSeed seeds a sampling mode's draws. Zero derives the seed from
	// the spec's wire form with rng_seed excluded (never the clock), the
	// raw Request / Overlays bodies in canonical form (keys sorted at
	// every depth, whitespace dropped, scalars as written), so the same
	// spec — whatever its key order — always draws the same slots; the
	// effective seed is echoed as ComposedResponse.SweepSeed and
	// predict's SweepSummary.RNGSeed. Refused under grid / zip.
	RNGSeed int64 `json:"rng_seed,omitempty"`

	// Label is the per-slot label pattern, `{{axis}}` placeholders
	// substituted per slot. Empty takes the default `<axis>=<value>`
	// pairs joined by `_` — prefixed `sample=<index>_` under a sampling
	// mode, so every drawn slot's label is unique.
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
// float kind, a json.Number, a string or a bool. Under a sampling mode
// (random / lhs) the axis carries a Range instead of Values; each drawn
// value is a number rendered in its shortest round-trip form.
type SweepAxis struct {
	// Name is the placeholder name: an identifier
	// (`[A-Za-z_][A-Za-z0-9_]*`), unique within the sweep.
	Name string `json:"name"`

	// Values is the non-empty list of scalar values the axis takes, in
	// expansion order (grid / zip). Absent on a range axis.
	Values []any `json:"values,omitempty"`

	// Range is the continuous interval a sampling mode (random / lhs)
	// draws the axis from. Required there, refused under grid / zip.
	Range *SweepRange `json:"range,omitempty"`
}

// SweepRange is a sampled sweep axis's closed interval [Min, Max] —
// draws never leave it: both bounds finite, Min below Max, and Min
// above zero under Scale log.
type SweepRange struct {
	// Min is the lower bound; required.
	Min *float64 `json:"min"`
	// Max is the upper bound; required, above Min.
	Max *float64 `json:"max"`
	// Scale is how draws spread over the interval (SweepScale*). Empty
	// is linear.
	Scale SweepScale `json:"scale,omitempty"`
}

// SweepScale names how a sampled axis spreads its draws.
type SweepScale string

const (
	// SweepScaleLinear draws uniformly on [min, max]. The default.
	SweepScaleLinear SweepScale = "linear"
	// SweepScaleLog draws uniformly on [log min, log max] and
	// exponentiates — log-uniform, for a parameter spanning decades
	// (a ridge lambda). Needs min > 0.
	SweepScaleLog SweepScale = "log"
)

// AllSweepScales returns every SweepScale value.
func AllSweepScales() []SweepScale {
	return []SweepScale{SweepScaleLinear, SweepScaleLog}
}

// UnmarshalJSON decodes the axis keeping every numeric value as a
// json.Number, so its text survives to substitution (`0.10` renders
// as `0.10`, an integer past 2^53 keeps every digit). Unknown keys are
// not judged here: a JSON request root decodes through the shared strict
// decoder, which checks the axis object against these fields first, so
// a misspelled `values` is refused as an unknown key.
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
	// SweepModeRandom draws SweepSpec.Samples slots, each axis value an
	// independent seeded draw from the axis Range. Compose sweep only;
	// an item for_each refuses it.
	SweepModeRandom SweepMode = "random"
	// SweepModeLHS draws SweepSpec.Samples slots as a Latin hypercube:
	// each axis Range is cut into Samples equal-probability strata and
	// every stratum is drawn exactly once per axis, the strata paired
	// across axes by a seeded permutation. Compose sweep only.
	SweepModeLHS SweepMode = "lhs"
)

// AllSweepModes returns every SweepMode value.
func AllSweepModes() []SweepMode {
	return []SweepMode{SweepModeGrid, SweepModeZip, SweepModeRandom, SweepModeLHS}
}

// Sampled reports whether m is a sampling mode (random or lhs).
func (m SweepMode) Sampled() bool {
	return m == SweepModeRandom || m == SweepModeLHS
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
// slot's Label, the ranked Value, its 1-based Rank, its 0-based Slot
// in the sweep's expansion order and the typed Axes values it ran
// with. Slot and Axes are always set, so a caller reads the winning
// axis values off the entry and never parses a label. A slot whose
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

	// Slot is the slot's 0-based index in the sweep's expansion order;
	// its response is ComposedResponse.Responses[explicit + Slot],
	// explicit being the number of hand-written `requests`.
	Slot int `json:"slot"`

	// Axes maps each sweep axis name to the value the slot ran with,
	// typed as the axis declared it: a number stays a number (a drawn
	// random / lhs value in its shortest round-trip form), a string or
	// boolean is preserved. Under JSON it reads back as float64 /
	// string / bool.
	Axes map[string]any `json:"axes"`
}
