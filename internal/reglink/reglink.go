// Package reglink is the one rule over a REG_GLM (family, link) pair
// (types.RegressionSpec Family / Link), shared by the no-execute predict
// pass (internal/descriptor), the manifest's link capability and the GLM
// engine (internal/processing/regression) so the three agree on which
// pairs exist and refuse the same requests with the same code, message
// and details. It reads the spec only and imports neither side.
//
// Each family has three links, its default one first (the default when
// Link is empty) — R's glm families, and MASS's negative binomial:
//
//	binomial           logit (canonical), probit, cloglog
//	poisson            log (canonical), identity, sqrt
//	gamma              inverse (canonical), log, identity
//	negative_binomial  log (MASS glm.nb's default), identity, sqrt
//
// The negative binomial's own knob, the fixed θ (RegressionSpec.Theta),
// and the modifiers that family refuses are CheckTheta's.
package reglink

import (
	"math"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// NegativeBinomial is the NB2 family (Var = μ + μ²/θ): θ estimated by
// maximum likelihood (MASS glm.nb) unless RegressionSpec.Theta fixes it.
const NegativeBinomial = "negative_binomial"

// families lists the REG_GLM error families, in their documented order.
var families = []string{"binomial", "poisson", "gamma", NegativeBinomial}

// links maps each family to its links, the default first.
var links = map[string][]string{
	"binomial":       {"logit", "probit", "cloglog"},
	"poisson":        {"log", "identity", "sqrt"},
	"gamma":          {"inverse", "log", "identity"},
	NegativeBinomial: {"log", "identity", "sqrt"},
}

// Families lists the REG_GLM families (binomial, poisson, gamma,
// negative_binomial).
func Families() []string { return slices.Clone(families) }

// KnownFamily reports whether family is one of Families.
func KnownFamily(family string) bool { return slices.Contains(families, family) }

// FamilyRefusal is the PROCESSING_REGRESSION_INVALID_FAMILY refusal of a
// family outside Families (the engine's selectFamily; predict keeps its
// own wording for an empty family).
func FamilyRefusal(family string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_INVALID_FAMILY,
		"REG_GLM Family must be one of "+strings.Join(families, ", ")+"; got "+family,
		map[string]any{"family": family, "allowed": Families()},
	)
}

// Links lists family's links, its canonical link first; nil for an
// unknown family.
func Links(family string) []string { return slices.Clone(links[family]) }

// Canonical is family's canonical link (the default when Link is
// empty; for negative_binomial MASS's default, log); "" for an unknown
// family.
func Canonical(family string) string {
	if l := links[family]; len(l) > 0 {
		return l[0]
	}
	return ""
}

// Resolve is the link a fit of family runs: link itself, or the
// family's canonical link when link is empty.
func Resolve(family, link string) string {
	if link == "" {
		return Canonical(family)
	}
	return link
}

// ByFamily is the manifest's family → links table (each list canonical
// first), a fresh map.
func ByFamily() map[string][]string {
	out := make(map[string][]string, len(links))
	for f, l := range links {
		out[f] = slices.Clone(l)
	}
	return out
}

// All is every link any family accepts, sorted: the link param's enum.
func All() []string {
	var out []string
	for _, l := range links {
		for _, v := range l {
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Check returns the PROCESSING_REGRESSION_INVALID_LINK refusal of a
// REG_GLM spec whose Link is not one of its Family's links, or nil.
// Every other type passes, and so does an empty Link (the canonical
// default) and an unknown family (the family check's refusal, never
// this one).
func Check(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil || spec.Type != types.REG_GLM {
		return nil
	}
	return CheckPair(spec.Family, spec.Link)
}

// CheckPair is Check on a bare (family, link) pair.
func CheckPair(family, link string) *errors.CodedError {
	allowed, ok := links[family]
	if !ok || link == "" || slices.Contains(allowed, link) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(
		errors.PROCESSING_REGRESSION_INVALID_LINK,
		"REG_GLM Family="+family+" requires Link in {"+strings.Join(allowed, ", ")+"}; got "+link,
		map[string]any{"family": family, "link": link, "allowed": slices.Clone(allowed)},
	)
}

// CheckTheta returns the PROCESSING_CONFIG refusal of a regression
// spec's negative binomial knobs, or nil — the one rule the engine
// (ValidateRegression) and predict (validateRegressions) apply:
//
//   - Theta (the fixed θ) is a REG_GLM negative_binomial knob: on any
//     other built-in regression or family it is refused (details.requires
//     "family negative_binomial"); there it must be finite and > 0
//     (details.requires "theta > 0"). Zero is absent: θ is estimated.
//   - negative_binomial refuses a penalty (details.requires
//     "unpenalized"), a selection ("no_selection") and a resample
//     ("no_resample"): their refit drivers hold the family's θ fixed, so
//     an estimated θ would go unrefit and a fixed one is unoracled.
//
// An extension or model regression's theta is the shared-slot table's
// (ExtensionRegressionSlotRefusal), never this rule's; an unknown family
// is the family check's.
func CheckTheta(spec *types.RegressionSpec) *errors.CodedError {
	if spec == nil {
		return nil
	}
	switch spec.Type {
	case types.REG_OLS, types.REG_GLM, types.REG_BAYES_LINEAR:
	default:
		return nil
	}
	t := string(spec.Type)
	nb := spec.Type == types.REG_GLM && spec.Family == NegativeBinomial
	if spec.Theta != 0 && !nb {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			t+" theta is the REG_GLM negative_binomial family's fixed θ; set family negative_binomial, or drop theta",
			map[string]any{"theta": spec.Theta, "type": t, "family": spec.Family, "requires": "family " + NegativeBinomial},
		)
	}
	if !nb {
		return nil
	}
	if spec.Theta != 0 && !(spec.Theta > 0 && !math.IsInf(spec.Theta, 1)) {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM negative_binomial theta must be a finite number > 0 (omit it to estimate θ by maximum likelihood)",
			map[string]any{"theta": spec.Theta, "type": t, "family": spec.Family, "requires": "theta > 0"},
		)
	}
	refuse := func(knob, requires, value string) *errors.CodedError {
		return errors.NewCodedErrorWithDetails(
			errors.PROCESSING_CONFIG,
			"REG_GLM negative_binomial does not support "+knob+" (its refit holds θ fixed, so an estimated θ is never refit); fit without "+knob+", or fit family poisson with a robust covariance",
			map[string]any{knob: value, "type": t, "family": spec.Family, "requires": requires},
		)
	}
	switch {
	case spec.Penalty != "":
		return refuse("penalty", "unpenalized", spec.Penalty)
	case spec.Selection != "":
		return refuse("selection", "no_selection", spec.Selection)
	case spec.Resample != "":
		return refuse("resample", "no_resample", spec.Resample)
	}
	return nil
}
