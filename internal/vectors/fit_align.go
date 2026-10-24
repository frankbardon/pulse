package vectors

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// fit_align.go is the one label-alignment rule between a bound vector
// fit and the slot that reads it (a fitted attribute's or a MAT_* slot's
// params.fit): the fit's member labels must be exactly the slot's, in
// any order — matched by label, never by position. The engine's bind
// (internal/processing) and the field-reference walk (predict, when the
// fit's labels are known statically — a loaded population file) both
// call AlignFit, so both refuse one mismatch with one error.

// AlignFit aligns a bound vector fit's labels (fitLabels, the fit named
// fit) to a slot's member labels: perm[i] is the index, in fitLabels, of
// labels[i]. Unequal label sets are PULSE_VECTOR_REFERENCE_MISMATCH
// reason labels_differ (the reference Match rule) naming the fit under
// details.fit and the slot under details[key] (key "attribute" or
// "matrix"), the message prefixed with slot.
func AlignFit(key, slot, fit string, fitLabels, labels []string) ([]int, *errors.CodedError) {
	t := ReferenceTable{Labels: fitLabels}
	perm, ce := t.Match("", "", labels)
	if ce == nil {
		return perm, nil
	}
	ce.Details["fit"] = fit
	ce.Details[key] = slot
	ce.Message = slot + ": bound vector fit " + strconv.Quote(fit) + " labels [" + strings.Join(fitLabels, ", ") +
		"] are not the slot's member labels [" + strings.Join(labels, ", ") + "]; a fit is matched by label, never by position"
	return nil, ce
}

// AttributeFitLabels returns the labels a fitted attribute slot (route
// typ, resolved members) aligns a bound vector fit to — the labels the
// engine's bind aligns: a fitted attribute's FittedLabels, a comparison
// slot's ComparisonParams labels — and false when they do not resolve
// (that refusal is the slot resolution's) or typ reads no fit.
func AttributeFitLabels(typ types.AttributeType, a *types.Attribute, members Resolved, schema *encoding.Schema) ([]string, bool) {
	if a == nil || len(members.Members) == 0 {
		return nil, false
	}
	switch {
	case IsFittedAttribute(typ), typ == types.ATTR_SEM_SCORE:
		labels, err := FittedLabels("", typ, a.Params, members)
		return labels, err == nil
	case IsComparisonAttribute(typ):
		cfg, err := ComparisonParams("", typ, a.Params, members, schema)
		return cfg.Labels, err == nil
	}
	return nil, false
}
