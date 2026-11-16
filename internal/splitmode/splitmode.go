// Package splitmode is the one rule set of FEAT_TRAIN_TEST_SPLIT's
// `params.mode`, shared by predict (internal/descriptor), the engine's
// constructor (internal/processing/feature) and the streaming verdict
// (internal/featstream), so the three cannot disagree on which params
// a mode reads or refuses.
//
//   - "random" (the default, also an absent mode): a seeded shuffle,
//     optionally stratified; reads ratios, seed, stratify.
//   - "temporal": the earliest rows by params.order_by go to train, the
//     latest to test (val between); reads ratios and order_by. A seed or
//     a stratify is refused (a time boundary neither shuffles nor
//     balances classes), and so is an order_by outside temporal mode (it
//     would be silently ignored).
//
// Leaf: the public errors package only. It reads no record.
package splitmode

import "github.com/frankbardon/pulse/errors"

// The params.mode values.
const (
	Random   = "random"
	Temporal = "temporal"
)

// Modes lists every params.mode value, the default first (the
// manifest's enum_values).
func Modes() []string { return []string{Random, Temporal} }

// Params is the mode-relevant slice of the split's params, as a lenient
// probe or the constructor's own decode sees it. SeedSet records whether
// the request named a seed at all (a seed of 0 included).
type Params struct {
	Mode     string
	OrderBy  string
	SeedSet  bool
	Stratify string
}

// IsTemporal reports whether mode selects the temporal split.
func IsTemporal(mode string) bool { return mode == Temporal }

const op = "FEAT_TRAIN_TEST_SPLIT"

func refuse(reason, msg string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, op+": "+msg,
		map[string]any{"feature": op, "slot": "mode", "reason": reason})
}

// The details.reason values of Check's refusals.
const (
	ReasonModeUnknown      = "mode_unknown"
	ReasonOrderByMissing   = "order_by_missing"
	ReasonOrderByUnused    = "order_by_unused"
	ReasonSeedTemporal     = "seed_temporal"
	ReasonStratifyTemporal = "stratify_temporal"
)

// Check is the mode rule: nil when p's params agree with its mode, else
// the PROCESSING_CONFIG refusal both sides raise, word for word. It runs
// after the ratios check and before the field-type checks.
func Check(p Params) *errors.CodedError {
	switch p.Mode {
	case "", Random:
		if p.OrderBy != "" {
			return refuse(ReasonOrderByUnused, "'order_by' is read only in mode \"temporal\"; set mode to \"temporal\" or drop order_by")
		}
		return nil
	case Temporal:
		switch {
		case p.OrderBy == "":
			return refuse(ReasonOrderByMissing, "mode \"temporal\" requires 'order_by' (a date, datetime or unsigned integer field)")
		case p.SeedSet:
			return refuse(ReasonSeedTemporal, "mode \"temporal\" assigns rows by order_by, never by a shuffle; drop 'seed'")
		case p.Stratify != "":
			return refuse(ReasonStratifyTemporal, "mode \"temporal\" cuts at a time boundary and cannot stratify; drop 'stratify'")
		}
		return nil
	}
	return refuse(ReasonModeUnknown, "'mode' must be \"random\" or \"temporal\", got \""+p.Mode+"\"")
}
