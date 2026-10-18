// Package ewmaparams is the one reading of WIN_EWMA's params, shared by
// the window factory (internal/processing/window) and predict
// (internal/descriptor) so both refuse the same request with the same
// message. It decides what a request MEANS and never smooths a value.
//
// The smoothing factor is given as exactly one of `alpha` or `decay`
// (alpha = 1 − decay). A decay is converted through the EXACT decimal of
// its JSON literal: `decay: 0.7` yields the double nearest 3/10, which is
// the double of the literal `alpha: 0.3` — never the float64 subtraction
// 1.0 − 0.7 (0.30000000000000004).
//
// Leaf: stdlib + the public errors package.
package ewmaparams

import (
	"bytes"
	"encoding/json"
	"math/big"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Params is the typed wire shape of WIN_EWMA's params. Decay stays a raw
// literal so its exact decimal value survives the decode.
type Params struct {
	Alpha *float64        `json:"alpha"`
	Decay json.RawMessage `json:"decay"`
}

// Refusal is one violated rule: Message is the operator-neutral sentence
// (callers prefix their own context) and Details the offending values.
type Refusal struct {
	Message string
	Details map[string]any
}

// Error renders the refusal for callers that wrap it.
func (r *Refusal) Error() string { return r.Message }

// Coded returns the refusal as a PULSE_WINDOW_INVALID error with the
// runtime prefix — the factory's refusal, predict's code (the window
// family's one code, internal/windowparams).
func (r *Refusal) Coded() *errors.CodedError {
	if len(r.Details) == 0 {
		return errors.NewCodedError(errors.PULSE_WINDOW_INVALID, "WIN_EWMA: "+r.Message)
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID, "WIN_EWMA: "+r.Message, r.Details)
}

var (
	ratZero = new(big.Rat)
	ratOne  = big.NewRat(1, 1)
)

// Resolve reads raw params and returns the effective alpha, or the first
// rule the params break.
func Resolve(raw json.RawMessage) (float64, *Refusal) {
	var p Params
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Window, string(types.WIN_EWMA), raw, &p); err != nil {
			return 0, &Refusal{Message: "malformed params: " + err.Error()}
		}
	}
	hasDecay := len(p.Decay) > 0 && !bytes.Equal(bytes.TrimSpace(p.Decay), []byte("null"))
	switch {
	case p.Alpha != nil && hasDecay:
		return 0, &Refusal{
			Message: "params.alpha and params.decay are mutually exclusive; give exactly one (alpha = 1 - decay)",
			Details: map[string]any{"alpha": *p.Alpha, "decay": string(p.Decay)},
		}
	case p.Alpha == nil && !hasDecay:
		return 0, &Refusal{Message: "exactly one of params.alpha or params.decay is required (alpha = 1 - decay)"}
	case p.Alpha != nil:
		if *p.Alpha <= 0 || *p.Alpha > 1 {
			return 0, &Refusal{
				Message: "params.alpha must be in (0, 1]",
				Details: map[string]any{"alpha": *p.Alpha},
			}
		}
		return *p.Alpha, nil
	}
	lit := string(bytes.TrimSpace(p.Decay))
	d, ok := decimalLiteral(lit)
	if !ok {
		return 0, &Refusal{
			Message: "params.decay must be a number",
			Details: map[string]any{"decay": lit},
		}
	}
	if d.Cmp(ratZero) < 0 || d.Cmp(ratOne) >= 0 {
		df, _ := d.Float64()
		return 0, &Refusal{
			Message: "params.decay must be in [0, 1)",
			Details: map[string]any{"decay": df},
		}
	}
	alpha, _ := new(big.Rat).Sub(ratOne, d).Float64()
	return alpha, nil
}

// decimalLiteral parses a JSON number literal exactly. The literal is
// already valid JSON (it came out of a decode); one that opens with a
// digit or '-' is a number, anything else (a string, bool, object or
// array) is not. big.Rat reads every JSON number form exactly.
func decimalLiteral(lit string) (*big.Rat, bool) {
	if lit == "" || (lit[0] != '-' && (lit[0] < '0' || lit[0] > '9')) {
		return nil, false
	}
	return new(big.Rat).SetString(lit)
}
