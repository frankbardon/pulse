package model

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/distlagparams"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// mmm_carry.go is the static face of adstock carry-over (U71 E2-S4,
// #551): the rules predict and the runtime share for scoring a fit that
// adstocks its channels inside the model — REG_MMM_BAYES always,
// REG_MMM_HILL under params.adstock. Such a fit reads RAW spend, and the
// adstock of a scored period sums spend from the periods before it, so
// a scored row needs the history. The fit records it: outputs.
// adstock_state holds each series' last max_lag fitted periods of raw
// spend (AdstockState), written by the engine's buffered driver beside
// the model's own outputs, so a Compose fit, a saved model file (feature
// outputs) and the engine holdout all carry it.
//
// Carry-over is the default for both scoring paths:
//
//   - the engine holdout (RegressionSpec.Holdout) adstocks each held-out
//     row's raw spend at the fit's kernel, in order (the fit's own
//     order), the train rows' last periods carried in;
//   - ATTR_REG_PREDICT with params.order {field[, partition]} reads the
//     scoring frame's RAW spend, orders its rows by that field and
//     adstocks them the same way, the fit's adstock_state carried in.
//     A params.fit ATTR_REG_FITTED / ATTR_REG_RESIDUAL takes the same
//     rule (FitCarries, carryRefusalFor — U71 E2-S5).
//
// params.carryover false opts out (the regression's params for the
// holdout, ATTR_REG_PREDICT's for a score): the scorer then reads the
// channel columns as given — pre-adstocked, exactly as before
// carry-over existed (an in-model adstocked REG_MMM_HILL keeps its
// adstock_holdout refusal). The kernel is the adstock record's: the
// fitted coefficients (REG_MMM_BAYES's posterior means, the population
// value on a geo panel), unnormalised.

// MMMAdstockStateKey is the outputs key of an in-model adstocked fit's
// adstock state.
const MMMAdstockStateKey = "adstock_state"

// MMMCarryoverParam is the params key (a REG_MMM_HILL / REG_MMM_BAYES
// spec's, an ATTR_REG_PREDICT slot's) that opts out of carry-over when
// false.
const MMMCarryoverParam = "carryover"

// The carry-over refusal reasons (PULSE_MODEL_SCORE_INVALID on a
// scoring slot, PROCESSING_CONFIG on a regression spec).
const (
	// ReasonCarryOrderRequired refuses carrying a score with no
	// params.order: the rows' sequence is what carries.
	ReasonCarryOrderRequired = "order_required"
	// ReasonCarryOrderUnused refuses params.order on a score that does
	// not carry (a fit that does not adstock in-model, or carryover
	// false).
	ReasonCarryOrderUnused = "order_unused"
	// ReasonCarryOrderFieldMissing refuses params.order without a field.
	ReasonCarryOrderFieldMissing = "order_field_missing"
	// ReasonCarryOrderField refuses a params.order field the scoring
	// frame lacks, or of a type that cannot order rows.
	ReasonCarryOrderField = "order_field_invalid"
	// ReasonCarryoverUnused refuses params.carryover where nothing
	// carries: a fit (or a spec) that does not adstock in-model.
	ReasonCarryoverUnused = "carryover_unused"
	// ReasonCarryStateMissing refuses carrying a score over a model
	// that holds no adstock state (a file saved before carry-over).
	ReasonCarryStateMissing = "carryover_state_missing"
)

// MMMInModelAdstock reports whether spec's fit adstocks its channels
// inside the model: REG_MMM_BAYES always, REG_MMM_HILL with
// params.adstock.
func MMMInModelAdstock(spec *types.RegressionSpec) bool {
	if spec == nil {
		return false
	}
	if spec.Type == types.REG_MMM_BAYES {
		return true
	}
	_, ok := MMMHillAdstock(spec)
	return ok
}

// carryoverParam is a params object's carryover value (nil when absent,
// null or undecodable).
func carryoverParam(raw json.RawMessage) *bool {
	if len(raw) == 0 {
		return nil
	}
	var p struct {
		Carryover *bool `json:"carryover"`
	}
	if paramread.Probe(raw, &p) != nil {
		return nil
	}
	return p.Carryover
}

// MMMCarryoverSet reports whether spec's params set carryover.
func MMMCarryoverSet(spec *types.RegressionSpec) bool {
	return spec != nil && carryoverParam(spec.Params) != nil
}

// MMMCarryover reports whether spec's engine holdout carries adstock:
// an in-model adstocked spec whose params.carryover is not false.
func MMMCarryover(spec *types.RegressionSpec) bool {
	if !MMMInModelAdstock(spec) {
		return false
	}
	c := carryoverParam(spec.Params)
	return c == nil || *c
}

// MMMOrder is the field (and series partition, "" for one series) an
// in-model adstocked spec orders its rows by: spec.Order's; a
// REG_MMM_BAYES geo panel's series partition is params.geo when the
// order names none.
func MMMOrder(spec *types.RegressionSpec) (field, partition string) {
	if spec == nil {
		return "", ""
	}
	if spec.Order != nil {
		field, partition = spec.Order.Field, spec.Order.Partition
	}
	if spec.Type == types.REG_MMM_BAYES && partition == "" {
		partition = MMMBayesGeoField(spec)
	}
	return field, partition
}

// AdstockState is an in-model adstocked fit's carry-over state
// (outputs.adstock_state): per series, its last MaxLag fitted periods of
// each channel's raw spend.
type AdstockState struct {
	// Field orders the fit's rows; Partition splits them into series
	// ("" for one series) — the fit's order.
	Field     string `json:"field"`
	Partition string `json:"partition,omitempty"`
	// Kernel and MaxLag are the adstock kernel (its params are the
	// fit's coefficients) and its window.
	Kernel string `json:"kernel"`
	MaxLag int    `json:"max_lag"`
	// Step is the order field's period: the smallest step between two
	// consecutive fitted rows of a series (1 when none).
	Step int64 `json:"step"`
	// Series are the fit's series, ascending by key.
	Series []AdstockSeries `json:"series"`
}

// AdstockSeries is one series' tail: its key (the partition's label or
// unsigned code in decimal; absent on one series), the order values of
// its last fitted periods (ascending) and each channel's raw spend
// there (null where the spend is null).
type AdstockSeries struct {
	Key   string             `json:"key,omitempty"`
	Order []int64            `json:"order"`
	Spend map[string][]Float `json:"spend"`
}

// Value is s in its outputs form: the JSON value a model file holds and
// reads back (map[string]any), so a fit's in-process outputs and a
// loaded file's read alike.
func (s *AdstockState) Value() (any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding outputs.adstock_state")
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, "encoding outputs.adstock_state")
	}
	return v, nil
}

// ReadAdstockState reads outs' adstock_state: the state and true; false
// when outs holds none; an error when it does not read as one.
func ReadAdstockState(outs map[string]any) (*AdstockState, bool, error) {
	v, ok := outs[MMMAdstockStateKey]
	if !ok || v == nil {
		return nil, false, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, true, fmt.Errorf("outputs.adstock_state: %w", err)
	}
	var s AdstockState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, true, fmt.Errorf("outputs.adstock_state: %w", err)
	}
	if s.Field == "" || s.MaxLag < 1 || s.Step < 1 {
		return nil, true, fmt.Errorf("outputs.adstock_state: needs field, max_lag ≥ 1 and step ≥ 1")
	}
	for _, sr := range s.Series {
		for ch, sp := range sr.Spend {
			if len(sp) != len(sr.Order) {
				return nil, true, fmt.Errorf("outputs.adstock_state: series %q channel %q holds %d spends for %d periods", sr.Key, ch, len(sp), len(sr.Order))
			}
		}
	}
	return &s, true, nil
}

// Channels are the channels s carries spend for, sorted.
func (s *AdstockState) Channels() []string {
	set := map[string]bool{}
	for _, sr := range s.Series {
		for ch := range sr.Spend {
			set[ch] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

// Weights are each channel's kernel weights (lags 0..MaxLag) at the
// fit's coefficients coefs — the adstock record's kernel
// (coefAdstock): unnormalised, a REG_MMM_BAYES fit's posterior means.
// A channel whose kernel coefficients are missing or invalid is an
// error.
func (s *AdstockState) Weights(coefs map[string]float64) (map[string][]float64, error) {
	res := &types.RegressionResult{Coefficients: coefs}
	maxLag := s.MaxLag
	out := map[string][]float64{}
	for _, ch := range s.Channels() {
		a, ok := coefAdstock(res, ch, &maxLag, s.Kernel, "")
		if !ok {
			return nil, fmt.Errorf("outputs.adstock_state: channel %q has no valid %s kernel coefficients", ch, s.Kernel)
		}
		w, err := a.Weights()
		if err != nil {
			return nil, err
		}
		out[ch] = w
	}
	return out, nil
}

// MMMAdstockKernelName is spec's in-model adstock kernel ("geometric"
// for REG_MMM_BAYES's default) and max_lag (MMMBayesDefaultMaxLag when
// absent).
func MMMAdstockKernelName(spec *types.RegressionSpec) (string, int) {
	_, maxLag, kernel := lineageChannels(spec)
	if kernel == "" {
		kernel = distlagparams.KernelGeometric
	}
	l := MMMBayesDefaultMaxLag
	if maxLag != nil {
		l = *maxLag
	}
	return kernel, l
}

// fileCarryFace is a loaded model file's Signature.Carries and
// CarryState, read off its coefficients and outputs exactly as
// Bound.Signature reads the bound result's.
func fileCarryFace(f *File) (carries, state bool) {
	coefs := make(map[string]float64, len(f.Coefficients))
	for k, v := range f.Coefficients {
		coefs[k] = float64(v)
	}
	if !boundCarries(f.Type, coefs) {
		return false, false
	}
	_, has := f.Outputs[MMMAdstockStateKey]
	return true, has
}

// MMMChannels are a media-mix spec's params.channels that are
// predictors, in params order (none for any other spec).
func MMMChannels(spec *types.RegressionSpec) []string {
	ch, _, _ := lineageChannels(spec)
	return ch
}

// boundCarries reports whether a bound fit of type t with coefficients
// coefs adstocks in-model: REG_MMM_BAYES, or a REG_MMM_HILL whose
// coefficient table names a kernel parameter.
func boundCarries(t types.RegressionType, coefs map[string]float64) bool {
	switch t {
	case types.REG_MMM_BAYES:
		return true
	case types.REG_MMM_HILL:
		for k := range coefs {
			if strings.HasPrefix(k, MMMDecayPrefix) || strings.HasPrefix(k, MMMAdstockShapePrefix) {
				return true
			}
		}
	}
	return false
}

// ScoreOrder is ATTR_REG_PREDICT's params.order: the scoring frame's
// field ordering the rows a carried score adstocks, and the partition
// splitting them into series (a geo panel's market field).
type ScoreOrder struct {
	Field     string `json:"field"`
	Partition string `json:"partition,omitempty"`
}

// ScoreCarries reports whether attr is an ATTR_REG_PREDICT slot that
// carries adstock — params.order set, params.carryover not false — and
// so takes the two-pass drive (its pre-pass reads every scored row's
// spend). Judged from the slot's own params alone, so every routing
// site and predict agree before any model is bound.
func ScoreCarries(attr *types.Attribute) bool {
	if attr == nil || attr.Type != types.ATTR_REG_PREDICT {
		return false
	}
	p, err := DecodeScoreParams(attr.Params)
	if err != nil {
		return false
	}
	return p.Order != nil && (p.Carryover == nil || *p.Carryover)
}

// carryRefusal is the carry-over rule over a scoring reference p to a
// fit of signature sig (nil when it holds): a carrying score
// (sig.Carries, carryover not false) needs params.order with a field
// and a model holding its adstock state; params.order on a score that
// does not carry, and params.carryover on a fit that does not adstock
// in-model, are refused.
func carryRefusal(p ScoreParams, sig Signature) *errors.CodedError {
	return carryRefusalFor(types.ATTR_REG_PREDICT, p.Carryover, p.Order, p.Fit, sig)
}

// carryRefusalFor is carryRefusal over a scoring slot of type t (an
// ATTR_REG_PREDICT score, or a params.fit ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL — fit_reference.go) with params.carryover carry,
// params.order order and params.fit fit.
func carryRefusalFor(t types.AttributeType, carry *bool, order *ScoreOrder, fit string, sig Signature) *errors.CodedError {
	p := ScoreParams{Fit: fit, Carryover: carry, Order: order}
	scoreInvalid := func(fit, reason, what string, extra map[string]any) *errors.CodedError {
		return scoreInvalidFor(t, fit, reason, what, extra)
	}
	if !sig.Carries {
		if p.Carryover != nil {
			return scoreInvalid(p.Fit, ReasonCarryoverUnused,
				fmt.Sprintf("params.carryover applies to a fit that adstocks its channels in-model (REG_MMM_BAYES, REG_MMM_HILL with params.adstock); %q is not one — drop params.carryover", p.Fit),
				map[string]any{"type": string(sig.Type)})
		}
		if p.Order != nil {
			return scoreInvalid(p.Fit, ReasonCarryOrderUnused,
				fmt.Sprintf("params.order orders a carried adstock, and %q adstocks nothing in-model; drop params.order", p.Fit),
				map[string]any{"type": string(sig.Type)})
		}
		return nil
	}
	if p.Carryover != nil && !*p.Carryover {
		if p.Order != nil {
			return scoreInvalid(p.Fit, ReasonCarryOrderUnused,
				"params.order orders a carried adstock, and params.carryover false scores the channel columns as given; drop one",
				map[string]any{"type": string(sig.Type)})
		}
		return nil
	}
	if p.Order == nil {
		return scoreInvalid(p.Fit, ReasonCarryOrderRequired,
			fmt.Sprintf("scores %q, a %s fit that adstocks raw spend in-model, by carrying its adstock from the fit's last periods into the scored rows; name params.order {field[, partition]} ordering the scoring rows (the raw spend columns are then adstocked here), or set params.carryover false to score pre-adstocked channel columns as given", p.Fit, sig.Type),
			map[string]any{"type": string(sig.Type)})
	}
	if p.Order.Field == "" {
		return scoreInvalid(p.Fit, ReasonCarryOrderFieldMissing, "params.order needs field, the column ordering the scored rows (an unsigned integer period, a date or a datetime)", nil)
	}
	if !sig.CarryState {
		return scoreInvalid(p.Fit, ReasonCarryStateMissing,
			fmt.Sprintf("cannot carry adstock into %q's scored rows: the model holds no outputs.adstock_state (a file saved before carry-over); refit and save it again, or set params.carryover false", p.Fit),
			map[string]any{"type": string(sig.Type)})
	}
	return nil
}

// CarryOrderFieldInvalid is the PULSE_MODEL_SCORE_INVALID refusal
// (reason order_field_invalid) of a carried score's params.order field
// or partition the scoring frame lacks (fieldType "") or holds as a
// type that cannot order (or key) rows.
func CarryOrderFieldInvalid(fit, slot, field, fieldType string) *errors.CodedError {
	return CarryOrderFieldInvalidFor(types.ATTR_REG_PREDICT, fit, slot, field, fieldType)
}

// CarryOrderFieldInvalidFor is CarryOrderFieldInvalid over a scoring
// slot of type t (a carried params.fit ATTR_REG_FITTED /
// ATTR_REG_RESIDUAL included).
func CarryOrderFieldInvalidFor(t types.AttributeType, fit, slot, field, fieldType string) *errors.CodedError {
	details := map[string]any{"slot": slot, "field": field}
	what := fmt.Sprintf("params.%s %q is not a column of the scoring frame", slot, field)
	if fieldType != "" {
		details["field_type"] = fieldType
		what = fmt.Sprintf("params.%s %q is a %s field; order by an unsigned integer period, a date or a datetime (a partition: categorical, unsigned integer, date, datetime or packed_bool)", slot, field, fieldType)
	}
	return scoreInvalidFor(t, fit, ReasonCarryOrderField, what, details)
}
